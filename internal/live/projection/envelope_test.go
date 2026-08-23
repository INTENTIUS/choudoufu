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
