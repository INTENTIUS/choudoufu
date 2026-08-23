// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
)

// TestAnIdentityKindRecordIsInvisibleToOrphanDiscovery is GitHub issue
// #364's replacement for TestLocatedKeysAreInvisibleToOrphanDiscovery,
// TestResidueKeysAreInvisibleToOrphanDiscovery and
// TestProvisionedKeysAreInvisibleToOrphanDiscovery, which each pinned the
// identical property against what were three separate, never-enumerated
// namespace roots. Since the envelope collapse there is only one root
// (RecordKeyPrefix) and it IS enumerated - discoverOrphanedRecords lists it
// - so the property now rests entirely on the envelope's own "kind" field:
// a kind=identity key must never be materialized as an undeclared
// prior-state entry (which is what proposes destroying it), while a
// kind=object key still must be, exactly as it always was.
//
// One store holds three keys: a real kind=object record (an orphaned
// record-backed instance whose configuration is gone - the positive
// control, proving discoverOrphanedRecords still does its job at all), a
// kind=identity record carrying only an import identity, and a second
// kind=identity record carrying only residue and a provisioner taint with
// no identity at all - the shape an ordinary marker-tracked instance's
// envelope takes today, before GitHub issue #364's unit A2 starts writing
// identity for it too. Neither of the identity-kind keys may be
// materialized; the object-kind key must be.
func TestAnIdentityKindRecordIsInvisibleToOrphanDiscovery(t *testing.T) {
	ctx := context.Background()
	const estate = "orphan-estate"
	prefix := RecordKeyPrefix(estate)
	raw := localHintStore(t)
	store := NewRecordEnvelopeStore(raw, prefix)

	objectAddr := locatedTestAddr(t, "null_resource", "orphaned")
	identityAddr := locatedTestAddr(t, "aws_eip_association", "orphaned")
	residueOnlyAddr := locatedTestAddr(t, "aws_lambda_function", "orphaned")

	of, err := encodeObjectFields(cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("orphan-object-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	}), nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding the object fixture: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, objectAddr, "", func(env *recordEnvelope) {
		env.Kind = recordKindObject
		env.Object = of
	}); err != nil {
		t.Fatalf("writing the kind=object fixture: %s", err)
	}

	if _, err := store.mergeEnvelope(ctx, identityAddr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: "eipassoc-orphan"}
	}); err != nil {
		t.Fatalf("writing the kind=identity fixture: %s", err)
	}

	rf, err := encodeResidueFields(map[string]cty.Value{"filename": cty.StringVal("orphan.zip")})
	if err != nil {
		t.Fatalf("encoding the residue fixture: %s", err)
	}
	if _, err := store.mergeEnvelope(ctx, residueOnlyAddr, "", func(env *recordEnvelope) {
		env.Residue = rf
		env.Provisioned = &provisionedFields{Tainted: true}
	}); err != nil {
		t.Fatalf("writing the residue/provisioned-only fixture: %s", err)
	}

	// No configuration at all, and no resolutions: every address here would
	// be undeclared, exactly the shape a removed resource block leaves
	// behind.
	cfg := loadConfig(t, writeEmptyFixture(t))
	provs := SingleProvider(nullProvider, nullResourceProvider())

	res, diags := BuildWith(ctx, cfg, nil, provs, Options{RecordStore: store})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{objectAddr.String()})
	for _, addr := range []addrs.AbsResourceInstance{identityAddr, residueOnlyAddr} {
		if res.Has(addr) {
			t.Errorf("%s materialized from a kind=identity key; discoverOrphanedRecords must never treat an identity, residue or provisioner-taint record as delete authority for the object it names", addr)
		}
	}
}

// TestDecodeEnvelopeAcceptsV1AsObject is item 6's v1-conformance pin,
// asserted directly against [decodeEnvelope] rather than through the
// compat wrapper: a v1 payload (no "format_version", no "kind", the flat
// shape recordPayload wrote before this envelope existed) decodes with
// Kind [recordKindObject] and its Object member populated from the legacy
// top-level fields, with nothing left over in the Legacy* fields once
// decoded.
func TestDecodeEnvelopeAcceptsV1AsObject(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("v1-value")})
	of, err := encodeObjectFields(val, []byte("private-bytes"), states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding: %s", err)
	}
	// The exact v1 wire shape: value_type/attrs/private/status at the
	// envelope's own top level, no format_version, no kind, no address.
	v1 := struct {
		ValueType json.RawMessage `json:"value_type"`
		Attrs     json.RawMessage `json:"attrs"`
		Private   []byte          `json:"private,omitempty"`
	}{ValueType: of.ValueType, Attrs: of.Attrs, Private: of.Private}
	raw, err := json.Marshal(v1)
	if err != nil {
		t.Fatalf("marshaling the v1 fixture: %s", err)
	}

	env, err := decodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decodeEnvelope refused a v1 payload: %s", err)
	}
	if env.Kind != recordKindObject {
		t.Errorf("Kind = %q, want %q for a v1 payload", env.Kind, recordKindObject)
	}
	if env.Object == nil {
		t.Fatal("Object is nil after decoding a v1 payload")
	}
	if env.Address != "" {
		t.Errorf("Address = %q, want empty - a v1 payload never carried one", env.Address)
	}
	if len(env.LegacyValueType) != 0 || len(env.LegacyAttrs) != 0 || len(env.LegacyPrivate) != 0 {
		t.Error("decodeEnvelope left the Legacy* fields populated after folding them into Object")
	}
	gotVal, gotPrivate, gotStatus, err := decodeObjectValue(env.Object)
	if err != nil {
		t.Fatalf("decodeObjectValue: %s", err)
	}
	if !gotVal.RawEquals(val) {
		t.Errorf("decoded value = %#v, want %#v", gotVal, val)
	}
	if string(gotPrivate) != "private-bytes" {
		t.Errorf("decoded private = %q, want %q", gotPrivate, "private-bytes")
	}
	if gotStatus != states.ObjectReady {
		t.Errorf("decoded status = %s, want %s", gotStatus, states.ObjectReady)
	}
}

// TestDecodeEnvelopeRefusesAnUnrecognizedPayload is the negative half:
// something that matches neither v1's flat shape nor v2's kind-tagged one
// (no format_version, no kind, no legacy value_type, no member at all) must
// be a decode ERROR, never an empty-but-valid envelope. mergeEnvelope's own
// isEmpty() rule means nothing this package ever writes looks like this on
// disk, so anything that does is foreign, corrupted or truncated - reading
// it as "nothing recorded, nothing tainted" would be exactly the silent
// under-run this whole mechanism exists to prevent.
func TestDecodeEnvelopeRefusesAnUnrecognizedPayload(t *testing.T) {
	for name, raw := range map[string][]byte{
		"empty object":     []byte(`{}`),
		"unrelated fields": []byte(`{"formatVersion":"from-the-future","somethingElse":true}`),
		"not json":         []byte(`{`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEnvelope(raw); err == nil {
				t.Errorf("decodeEnvelope accepted %s as a valid envelope", raw)
			}
		})
	}
}

// retiredLocatedPayload is [locatedPayload]'s exact wire shape as it was
// before GitHub issue #364's envelope collapse (choudoufu before
// 0be41c03ef, internal/live/projection/located.go) - kept here, decode-only,
// as a fixture rather than resurrected in production code. Its two fields
// that matter for this test are "address" (which the current envelope ALSO
// calls "address") and "identity" (which the current envelope ALSO calls
// "identity", but as an entirely different shape: this is
// map[string]string, keyed by identity-schema attribute name, and today's
// [identityPayload] is {ImportID, Attrs}). "formatVersion" and "importID"
// deliberately do not collide with anything in [recordEnvelope]'s own JSON
// tags ("format_version", no "import_id" field at the envelope's own top
// level) - only "address" and "identity" do, and "identity" is the collision
// this test exists to close.
type retiredLocatedPayload struct {
	FormatVersion string            `json:"formatVersion"`
	Address       string            `json:"address"`
	ImportID      string            `json:"importID,omitempty"`
	Identity      map[string]string `json:"identity,omitempty"`
}

// TestDecodeEnvelopeRefusesARetiredLocatedPayload is the adversarial-audit
// follow-up to unit A1 (2026-08-23): a retired pre-#364 locatedPayload
// carrying a COMPOSITE identity decodes its "identity" key straight into
// [recordEnvelope.Identity], because json.Unmarshal allocates the pointer
// for any non-null JSON object at that key - even though none of THIS
// struct's own fields ("import_id", "attrs") appear anywhere inside the old
// map[string]string shape, so the allocated [identityPayload] is non-nil
// and entirely empty.
//
// Before this test's fix landed, that non-nil-but-vacuous pointer defeated
// decodeEnvelope's all-four-nil garbage check, so this payload fell through
// to being resolved as Kind=[recordKindObject] with Object still nil - a
// shape [RecordStore.mergeEnvelope] never writes and no reader may ever be
// handed. Nothing downstream actually mis-binds an instance to it (build.go's
// materializeRecord and [RecordStore.GetIdentity]'s own Empty() check both
// refuse a nil Object/empty identity loudly), which is why this was a
// decode-contract regression and not a live safety bug - but the contract
// is the thing this test pins.
//
// Mutation to check: delete the Kind-vs-content switch at the end of
// decodeEnvelope. This test fails with the OLD symptom: Kind ==
// recordKindObject, Object == nil, no error. Deleting ONLY the
// identityPayload/residueFields/provisionedFields normalization step and
// leaving the switch in place does NOT reproduce the symptom for THIS
// fixture - the switch's own "kind=object requires Object != nil" check is
// an independent, sufficient backstop here, since Kind still resolves to
// recordKindObject with Object nil either way.
// TestDecodeEnvelopeRefusesAKindContentMismatch's "identity kind, empty
// identity object" sub-test is what isolates the normalization step's own
// necessity instead - see its doc comment.
func TestDecodeEnvelopeRefusesARetiredLocatedPayload(t *testing.T) {
	raw, err := json.Marshal(retiredLocatedPayload{
		FormatVersion: "tofu-live-located-v1",
		Address:       "aws_apigatewayv2_route.pets",
		Identity:      map[string]string{"api_id": "aabbccddee", "id": "1122334"},
	})
	if err != nil {
		t.Fatalf("marshaling the retired locatedPayload fixture: %s", err)
	}

	env, err := decodeEnvelope(raw)
	if err == nil {
		t.Fatalf("decodeEnvelope accepted a retired locatedPayload as %s with Object=%v Identity=%v; want a decode error",
			env.Kind, env.Object, env.Identity)
	}
}

// retiredResiduePayload is [residuePayload]'s exact wire shape from the same
// pre-#364 commit - the FLAT shape (formatVersion/address/attributes all at
// the payload's own top level), unlike today's [recordEnvelope], which nests
// residue data one level deeper under a "residue" key.
//
// That nesting is why residue turns out NOT to be located.go's sibling in
// the way it first looks: [residueAttrValue]'s own "attrType"/"attrValue"
// tags never changed, but the retired shape's "attributes" key has nothing
// in [recordEnvelope] to land on - only a top-level "residue" key ever
// reaches [recordEnvelope.Residue] - so this payload decodes with every one
// of Object/Identity/Residue/Provisioned nil, Kind == "", and no legacy
// value_type either: exactly the shape the PRE-EXISTING all-four-nil check
// already refused, before this file's #364-A2-audit fix touched anything.
// Kept here anyway, alongside locatedPayload's real regression, as the
// negative control that not every retired shape collides - the located
// case is a genuine key-name collision ("identity" means something on both
// sides), and this one demonstrates the same audit did not also need to
// invent a residue-side fix that was never there to make.
type retiredResiduePayload struct {
	FormatVersion string                             `json:"formatVersion"`
	Address       string                             `json:"address"`
	Attributes    map[string]retiredResidueAttrValue `json:"attributes"`
}

type retiredResidueAttrValue struct {
	Type  json.RawMessage `json:"attrType"`
	Value json.RawMessage `json:"attrValue"`
}

// TestDecodeEnvelopeRefusesARetiredResiduePayload is
// TestDecodeEnvelopeRefusesARetiredLocatedPayload's sibling for the OTHER
// retired per-instance namespace, added because the 2026-08-23 adversarial
// audit named it - see [retiredResiduePayload]'s own doc comment for why
// this one was already safe before that audit's fix, unlike the located
// case. Passing before AND after the located-side fix is the correct
// behavior here, not a sign the test proves nothing: it pins that the fix
// did not need to (and does not) touch this shape.
func TestDecodeEnvelopeRefusesARetiredResiduePayload(t *testing.T) {
	raw, err := json.Marshal(retiredResiduePayload{
		FormatVersion: "tofu-live-residue-v1",
		Address:       "aws_route53_record.wp-prod-staging",
		Attributes: map[string]retiredResidueAttrValue{
			"allow_overwrite": {Type: json.RawMessage(`"bool"`), Value: json.RawMessage(`true`)},
		},
	})
	if err != nil {
		t.Fatalf("marshaling the retired residuePayload fixture: %s", err)
	}

	env, err := decodeEnvelope(raw)
	if err == nil {
		t.Fatalf("decodeEnvelope accepted a retired residuePayload as %s with Object=%v Residue=%v; want a decode error",
			env.Kind, env.Object, env.Residue)
	}
}

// TestDecodeEnvelopeRefusesAKindContentMismatch is the direct, mutation-
// provable pin for decodeEnvelope's own Kind-vs-content validation. It
// builds v2-shaped envelopes by hand, bypassing every legacy-decode path,
// so each sub-test isolates exactly one way Kind can disagree with what an
// envelope actually carries.
//
// The fix has two independently necessary halves, and this test is what
// tells them apart - TestDecodeEnvelopeRefusesARetiredLocatedPayload
// cannot, because for that one fixture the switch alone is already
// sufficient (Kind there defaults to [recordKindObject] with Object nil
// regardless of whether the Identity pointer was normalized first):
//
//   - "object kind, no object", "identity kind, carries an object" and
//     "unrecognized kind" go red if the "switch env.Kind" block at the end
//     of decodeEnvelope is deleted. Kind is explicit and correct in each of
//     these; only the CONTENT disagrees with it, so none of them depends on
//     the identityPayload/residueFields/provisionedFields normalization
//     step at all.
//   - "identity kind, empty identity object" is the one that isolates the
//     OTHER half. Kind explicitly says "identity" and env.Identity really
//     is non-nil after json.Unmarshal (an empty JSON object still
//     allocates the pointer), so the switch's own "carries none of the
//     three" check cannot see the emptiness unless the normalization step
//     ran first to collapse it back to nil. Delete that normalization and
//     only this one sub-test goes red.
func TestDecodeEnvelopeRefusesAKindContentMismatch(t *testing.T) {
	for name, raw := range map[string][]byte{
		"object kind, no object":               []byte(`{"format_version":2,"address":"null_resource.x","kind":"object"}`),
		"identity kind, carries an object":     []byte(`{"format_version":2,"address":"null_resource.x","kind":"identity","object":{"value_type":"\"string\"","attrs":"\"v\""}}`),
		"identity kind, carries nothing":       []byte(`{"format_version":2,"address":"aws_vpc.x","kind":"identity"}`),
		"identity kind, empty identity object": []byte(`{"format_version":2,"address":"aws_vpc.x","kind":"identity","identity":{}}`),
		"unrecognized kind":                    []byte(`{"format_version":2,"address":"aws_vpc.x","kind":"bogus","identity":{"import_id":"vpc-1"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEnvelope(raw); err == nil {
				t.Errorf("decodeEnvelope accepted %s as a valid envelope", raw)
			}
		})
	}
}

// TestRecordEnvelopeRoundTripsPerKind is item 6's round-trip requirement,
// one sub-test per member the envelope carries: writing through
// mergeEnvelope and reading back through each member's own accessor
// reproduces exactly what was written, and the members do not interfere
// with each other when they share one key - the central claim GitHub issue
// #364 rests on.
func TestRecordEnvelopeRoundTripsPerKind(t *testing.T) {
	ctx := context.Background()
	prefix := RecordKeyPrefix("roundtrip-estate")

	t.Run("object", func(t *testing.T) {
		store := NewRecordEnvelopeStore(localHintStore(t), prefix)
		addr := locatedTestAddr(t, "terraform_data", "rt")
		val := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("rt-object")})
		of, err := encodeObjectFields(val, nil, states.ObjectTainted)
		if err != nil {
			t.Fatalf("encoding: %s", err)
		}
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Kind = recordKindObject
			env.Object = of
		}); err != nil {
			t.Fatalf("mergeEnvelope: %s", err)
		}
		env, _, exists, err := store.getRaw(ctx, addr)
		if err != nil || !exists {
			t.Fatalf("getRaw: exists=%v err=%v", exists, err)
		}
		gotVal, _, gotStatus, err := decodeObjectValue(env.Object)
		if err != nil {
			t.Fatalf("decodeObjectValue: %s", err)
		}
		if !gotVal.RawEquals(val) || gotStatus != states.ObjectTainted {
			t.Errorf("round trip = (%#v, %s), want (%#v, %s)", gotVal, gotStatus, val, states.ObjectTainted)
		}
	})

	t.Run("identity", func(t *testing.T) {
		store := NewRecordEnvelopeStore(localHintStore(t), prefix)
		addr := locatedTestAddr(t, "aws_eip_association", "rt")
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Identity = &identityPayload{ImportID: "eipassoc-rt"}
		}); err != nil {
			t.Fatalf("mergeEnvelope: %s", err)
		}
		rec, _, keyExists, found, err := store.GetIdentity(ctx, addr)
		if err != nil || !keyExists || !found {
			t.Fatalf("GetIdentity: keyExists=%v found=%v err=%v", keyExists, found, err)
		}
		if rec.ImportID != "eipassoc-rt" {
			t.Errorf("ImportID = %q, want %q", rec.ImportID, "eipassoc-rt")
		}
	})

	t.Run("residue", func(t *testing.T) {
		store := NewRecordEnvelopeStore(localHintStore(t), prefix)
		addr := locatedTestAddr(t, "aws_lambda_function", "rt")
		want := map[string]cty.Value{"filename": cty.StringVal("rt.zip")}
		rf, err := encodeResidueFields(want)
		if err != nil {
			t.Fatalf("encoding: %s", err)
		}
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Residue = rf
		}); err != nil {
			t.Fatalf("mergeEnvelope: %s", err)
		}
		attrs, _, keyExists, found, err := store.GetResidue(ctx, addr)
		if err != nil || !keyExists || !found {
			t.Fatalf("GetResidue: keyExists=%v found=%v err=%v", keyExists, found, err)
		}
		if got := attrs["filename"]; !got.RawEquals(want["filename"]) {
			t.Errorf("filename = %#v, want %#v", got, want["filename"])
		}
	})

	t.Run("provisioned", func(t *testing.T) {
		store := NewRecordEnvelopeStore(localHintStore(t), prefix)
		addr := locatedTestAddr(t, "aws_instance", "rt")
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Provisioned = &provisionedFields{Tainted: true}
		}); err != nil {
			t.Fatalf("mergeEnvelope: %s", err)
		}
		tainted, _, keyExists, err := store.getProvisioned(ctx, addr)
		if err != nil || !keyExists || !tainted {
			t.Fatalf("getProvisioned: keyExists=%v tainted=%v err=%v", keyExists, tainted, err)
		}
	})

	t.Run("all four coexist on one key", func(t *testing.T) {
		store := NewRecordEnvelopeStore(localHintStore(t), prefix)
		addr := locatedTestAddr(t, "aws_eip_association", "combined")
		residue := map[string]cty.Value{"note": cty.StringVal("sent-value")}
		rf, err := encodeResidueFields(residue)
		if err != nil {
			t.Fatalf("encoding residue: %s", err)
		}
		if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
			env.Identity = &identityPayload{ImportID: "eipassoc-combined"}
			env.Residue = rf
			env.Provisioned = &provisionedFields{Tainted: true}
		}); err != nil {
			t.Fatalf("mergeEnvelope: %s", err)
		}

		rec, v1, keyExists1, identityFound, err := store.GetIdentity(ctx, addr)
		if err != nil || !keyExists1 || !identityFound || rec.ImportID != "eipassoc-combined" {
			t.Fatalf("GetIdentity: rec=%+v keyExists=%v found=%v err=%v", rec, keyExists1, identityFound, err)
		}
		attrs, v2, keyExists2, residueFound, err := store.GetResidue(ctx, addr)
		if err != nil || !keyExists2 || !residueFound || !attrs["note"].RawEquals(residue["note"]) {
			t.Fatalf("GetResidue: attrs=%v keyExists=%v found=%v err=%v", attrs, keyExists2, residueFound, err)
		}
		tainted, v3, keyExists3, err := store.getProvisioned(ctx, addr)
		if err != nil || !keyExists3 || !tainted {
			t.Fatalf("getProvisioned: tainted=%v keyExists=%v err=%v", tainted, keyExists3, err)
		}
		// All three reads observed the SAME physical key: the version must
		// agree across all of them, which is the property write-back's own
		// dedup (builder.recordEnvelopeVersion) and merge (mergeEnvelope's
		// fresh-read-before-write) both depend on.
		if v1 != v2 || v2 != v3 {
			t.Errorf("the three accessors disagree on the shared key's version: identity=%q residue=%q provisioned=%q", v1, v2, v3)
		}
	})
}
