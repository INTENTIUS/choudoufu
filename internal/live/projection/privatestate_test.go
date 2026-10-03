// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file's tests are GitHub issue #1239's. See privatestate.go for the
// mechanism and the measured population.
//
// The private the founding member writes is hashicorp/aws's
// WriteOnlyValueStore's: terraform-plugin-framework's Data.Bytes over a
// map[string][]byte, so each value is base64 of the JSON the provider set.
// The provider sets strconv.Quote(sha256-hex), a JSON string. The key and
// hash below are a fixture's, not aws_transfer_host_key's, because nothing
// in the mechanism names either.
const privateWOKey = "body_wo"

// woHashPrivate is a framework private carrying one write-only hash.
func woHashPrivate(t *testing.T, hash string) []byte {
	t.Helper()
	quoted, err := json.Marshal(hash)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(map[string][]byte{privateWOKey: quoted})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// privateKeys decodes a private's top-level keys, failing the test on any
// private that is not a JSON object.
func privateKeys(t *testing.T, private []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(private, &m); err != nil {
		t.Fatalf("private %q is not a JSON object: %s", private, err)
	}
	return m
}

func TestApplyTimePrivate(t *testing.T) {
	sdkOnly := []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":{"delete":300000000000},"schema_version":"0"}`)

	cases := []struct {
		name    string
		private []byte
		want    map[string]bool // keys expected; nil means "nothing recorded"
		opaque  bool            // the whole input comes back unchanged
	}{
		{name: "nil", private: nil},
		{name: "empty", private: []byte{}},
		{name: "whitespace", private: []byte("  \n")},
		{name: "null literal", private: []byte("null")},
		{name: "empty object", private: []byte("{}")},
		// #1185's carrier and helper/schema's own version key are rebuilt by
		// every import and read; recording them would be recording what the
		// next plan re-derives anyway, and the timeouts half is the
		// configuration's, which must win.
		{name: "SDKv2 meta only", private: sdkOnly},
		{name: "schema_version only", private: []byte(`{"schema_version":"1"}`)},
		{name: "framework write-only hash", private: []byte(`{"body_wo":"ImFiYyI="}`), want: map[string]bool{"body_wo": true}},
		{
			name:    "SDKv2 meta beside an apply-time key",
			private: []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":{"delete":1},"schema_version":"0","learned":"eA=="}`),
			want:    map[string]bool{"learned": true},
		},
		// A shape nothing here can read is not provably re-derivable, so it
		// is kept whole rather than dropped.
		{name: "not JSON", private: []byte("\x00opaque"), opaque: true},
		{name: "JSON array", private: []byte(`["a"]`), opaque: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyTimePrivate(tc.private)
			switch {
			case tc.opaque:
				if !bytes.Equal(got, tc.private) {
					t.Errorf("applyTimePrivate(%q) = %q, want the input unchanged", tc.private, got)
				}
			case tc.want == nil:
				if got != nil {
					t.Errorf("applyTimePrivate(%q) = %q, want nil: nothing here is apply-time data", tc.private, got)
				}
			default:
				keys := privateKeys(t, got)
				if len(keys) != len(tc.want) {
					t.Errorf("applyTimePrivate(%q) kept keys %v, want exactly %v", tc.private, keys, tc.want)
				}
				for k := range tc.want {
					if _, ok := keys[k]; !ok {
						t.Errorf("applyTimePrivate(%q) dropped %q", tc.private, k)
					}
				}
			}
		})
	}

	// The input is never aliased: a caller that keeps the result must not
	// see a later write to the provider's buffer.
	in := []byte("\x00opaque")
	out := applyTimePrivate(in)
	in[0] = 'X'
	if out[0] != 0 {
		t.Error("applyTimePrivate returned an alias of its input for an opaque private")
	}
}

func TestWithRecordedPrivate(t *testing.T) {
	recorded := []byte(`{"body_wo":"ImFiYyI="}`)

	t.Run("nothing recorded", func(t *testing.T) {
		in := []byte(`{"x":"eQ=="}`)
		got, changed := withRecordedPrivate(in, nil)
		if changed || !bytes.Equal(got, in) {
			t.Errorf("got %q (changed %v), want the input unchanged", got, changed)
		}
	})

	for _, empty := range [][]byte{nil, {}, []byte("null")} {
		t.Run("read private "+string(empty), func(t *testing.T) {
			got, changed := withRecordedPrivate(empty, recorded)
			if !changed || !bytes.Equal(got, recorded) {
				t.Errorf("got %q (changed %v), want the recorded private whole: the framework's read returns nil for an imported instance", got, changed)
			}
		})
	}

	t.Run("SDKv2 meta kept, recorded key added", func(t *testing.T) {
		in := []byte(`{"e2bfb730-ecaa-11e6-8f88-34363bc7c4c0":{"delete":300000000000},"schema_version":"0"}`)
		got, changed := withRecordedPrivate(in, recorded)
		if !changed {
			t.Fatal("changed = false, want the recorded key added")
		}
		keys := privateKeys(t, got)
		for _, k := range []string{sdkv2TimeoutMetaKey, sdkv2SchemaVersionKey, "body_wo"} {
			if _, ok := keys[k]; !ok {
				t.Errorf("merged private %s lost or lacks %q", got, k)
			}
		}
	})

	// The read speaks for this run; the record only answers what the read
	// could not know. Mutating the fill into an overwrite turns this red.
	t.Run("a key the read produced wins", func(t *testing.T) {
		in := []byte(`{"body_wo":"ImZyZXNoIg=="}`)
		got, changed := withRecordedPrivate(in, recorded)
		if changed || !bytes.Equal(got, in) {
			t.Errorf("got %q (changed %v), want the read's own value left standing", got, changed)
		}
	})

	t.Run("a private this package cannot read is left alone", func(t *testing.T) {
		in := []byte("\x00theirs")
		got, changed := withRecordedPrivate(in, recorded)
		if changed || !bytes.Equal(got, in) {
			t.Errorf("got %q (changed %v), want the provider's opaque private unchanged", got, changed)
		}
	})

	t.Run("an opaque recording does not merge into an object", func(t *testing.T) {
		in := []byte(`{"x":"eQ=="}`)
		got, changed := withRecordedPrivate(in, []byte("\x00opaque"))
		if changed || !bytes.Equal(got, in) {
			t.Errorf("got %q (changed %v), want the input unchanged", got, changed)
		}
	})
}

// TestRecordsProviderPrivateOnlyUnderStore pins the secrets gate: the
// founding member's private is taken of a write-only argument, and the two
// settings that keep secret material out of the record keep this out too.
func TestRecordsProviderPrivateOnlyUnderStore(t *testing.T) {
	for _, tc := range []struct {
		secrets strict.Secrets
		want    bool
	}{
		{strict.Store, true},
		{strict.Refuse, false},
		{strict.SSM, false},
	} {
		if got := recordsProviderPrivate(tc.secrets); got != tc.want {
			t.Errorf("recordsProviderPrivate(%q) = %v, want %v", tc.secrets, got, tc.want)
		}
	}
}

// privateStubSchema is [stubNamespaceSchema] plus one write-only argument,
// the shape whose provider keeps a hash in the private.
func privateStubSchema() providers.Schema {
	s := stubNamespaceSchema()
	attrs := make(map[string]*configschema.Attribute, len(s.Block.Attributes)+1)
	for k, v := range s.Block.Attributes {
		attrs[k] = v
	}
	attrs[privateWOKey] = &configschema.Attribute{Type: cty.String, Optional: true, WriteOnly: true}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs, BlockTypes: s.Block.BlockTypes}}
}

func privateStubSchemas() *tofu.Schemas {
	return &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		addrs.NewDefaultProvider("stub"): {
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"stub_ns": privateStubSchema()},
		},
	}}
}

// privateStubFinalState is an apply's final state for one stub_ns
// instance whose provider wrote private.
func privateStubFinalState(t *testing.T, addr addrs.AbsResourceInstance, private []byte) *states.State {
	t.Helper()
	ty := privateStubSchema().Block.ImpliedType()
	attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name, aty := range ty.AttributeTypes() {
		attrs[name] = cty.NullVal(aty)
	}
	attrs["id"] = cty.StringVal(addr.Resource.Resource.Name)
	attrs["name"] = cty.StringVal(addr.Resource.Resource.Name)
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: cty.ObjectVal(attrs), Private: private}).Encode(ty, 0, 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	st := states.NewState()
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	st.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, provAddr, addrs.NoKey)
	return st
}

// TestWriteBackRecordsTheApplyTimePrivate is the write half end to end
// through the real write-back: the hash lands in the record, the SDKv2
// keys beside it do not, and a later apply whose private carries no
// apply-time data clears it rather than leaving the old one to be handed
// back.
func TestWriteBackRecordsTheApplyTimePrivate(t *testing.T) {
	t.Setenv(strict.EnvPin, "")
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("private1239"))
	addr := mustAddr(t, `stub_ns.plain`)
	schemas := privateStubSchemas()

	withHash := woHashPrivate(t, "abc")
	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: privateStubFinalState(t, addr, withHash),
		Schemas:    schemas,
	}))
	got, found, err := store.GetProviderPrivate(ctx, addr)
	if err != nil {
		t.Fatalf("GetProviderPrivate: %s", err)
	}
	if !found {
		t.Fatal("an apply whose provider wrote a write-only hash into its private recorded nothing, so the next live plan hands the provider an empty private and it proposes a replace")
	}
	if !bytes.Equal(got, applyTimePrivate(withHash)) {
		t.Errorf("recorded private = %s, want %s", got, applyTimePrivate(withHash))
	}

	// The next apply's provider wrote nothing of its own (the write-only
	// argument was removed from configuration, say): the record must
	// follow, as a state file's private would.
	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:            store,
		FinalState:       privateStubFinalState(t, addr, []byte(`{"schema_version":"0"}`)),
		Schemas:          schemas,
		EnvelopeVersions: []RecordVersion{},
	}))
	if got, found, err := store.GetProviderPrivate(ctx, addr); err != nil || found {
		t.Errorf("after an apply whose private carries no apply-time data the record still holds %s (found %v, err %v); the next plan would hand back a private the provider no longer wrote", got, found, err)
	}
}

// TestWriteBackRecordsNoPrivateUnderRefuse: `strict { secrets = "refuse" }`
// keeps the private out of the record.
func TestWriteBackRecordsNoPrivateUnderRefuse(t *testing.T) {
	t.Setenv(strict.EnvPin, "1") // the pin's answer for an omitted setting is Refuse
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("private1239"))
	addr := mustAddr(t, `stub_ns.plain`)

	assertNoErrors(t, WriteBack(ctx, WriteBackRequest{
		Store:      store,
		FinalState: privateStubFinalState(t, addr, woHashPrivate(t, "abc")),
		Schemas:    privateStubSchemas(),
	}))
	if got, found, err := store.GetProviderPrivate(ctx, addr); err != nil || found {
		t.Errorf("under secrets = refuse the record holds a provider private %s (found %v, err %v)", got, found, err)
	}
}

// TestRecordResidueForInstanceRecordsTheMigratedPrivate is the migration
// half: a state file's private reaches the record even for an instance
// with no residue attribute at all, which is the founding member's shape.
func TestRecordResidueForInstanceRecordsTheMigratedPrivate(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("private1239"))
	addr := mustAddr(t, `stub_ns.plain`)
	schema := privateStubSchema()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":         cty.StringVal("plain"),
		"name":       cty.StringVal("plain"),
		privateWOKey: cty.NullVal(cty.String),
		"timeouts":   cty.NullVal(stubTimeoutsType),
	})
	read := func(prior cty.Value) (cty.Value, error) { return prior, nil }
	priv := woHashPrivate(t, "abc")

	recorded, err := RecordResidueForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, schema, applied, strict.Store, read, cty.NilVal, nil, priv)
	if err != nil {
		t.Fatalf("RecordResidueForInstance: %s", err)
	}
	if !recorded {
		t.Fatal("recorded = false: a migrated instance whose state file carries a write-only hash recorded nothing")
	}
	got, found, err := store.GetProviderPrivate(ctx, addr)
	if err != nil || !found || !bytes.Equal(got, applyTimePrivate(priv)) {
		t.Errorf("recorded private = %s (found %v, err %v), want %s", got, found, err, applyTimePrivate(priv))
	}

	other := mustAddr(t, `stub_ns.held`)
	if recorded, err := RecordResidueForInstance(ctx, store, other, addrs.AbsProviderConfig{}, schema, applied, strict.Refuse, read, cty.NilVal, nil, priv); err != nil || recorded {
		t.Errorf("under secrets = refuse a migration recorded the private (recorded %v, err %v)", recorded, err)
	}
}

// projectWithRecordedPrivate runs testdata/timeouts through BuildWith
// against the framework stub, with stub_ns.plain's record carrying a
// write-only hash. The stub's import and read return the framework's own
// empty private, which is the live path's whole problem.
func projectWithRecordedPrivate(t *testing.T, recorded []byte) (*tofu.MockProvider, *Result) {
	t.Helper()
	ctx := context.Background()
	cfg := loadConfig(t, "testdata/timeouts")
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("private1239"))
	if _, err := store.mergeEnvelope(ctx, mustAddr(t, `stub_ns.plain`), "", func(env *recordEnvelope) {
		env.Residue = &residueFields{ProviderPrivate: recorded}
	}); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	p := frameworkStubProvider()
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("stub")}
	res, diags := BuildWith(ctx, cfg, []identity.Resolution{
		{Addr: mustAddr(t, `stub_ns.held`), Class: identity.ClassConcrete, ImportID: "held", IdentityValues: map[string]string{"name": "held"}},
		{Addr: mustAddr(t, `stub_ns.plain`), Class: identity.ClassConcrete, ImportID: "plain", IdentityValues: map[string]string{"name": "plain"}},
		{Addr: mustAddr(t, `stub_ns.unparseable`), Class: identity.ClassConcrete, ImportID: "unparseable", IdentityValues: map[string]string{"name": "unparseable"}},
	}, SingleProvider(provAddr, p), Options{RecordStore: store})
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{`stub_ns.held`, `stub_ns.plain`, `stub_ns.unparseable`})
	return p, res
}

// TestProjectionRestoresTheRecordedPrivate is the read half: the projected
// prior's private carries the recorded hash, and the plan hands it to the
// provider as PriorPrivate - which is what the provider's
// RequiresReplaceWO-shaped plan modifier reads.
//
// Proven-red shape (not run, per this effort's no-testing ruling): delete
// the restoreProviderPrivate call in materialize and the first assertion
// reads an empty private, which is the measured pre-fix projection.
func TestProjectionRestoresTheRecordedPrivate(t *testing.T) {
	recorded := []byte(`{"body_wo":"ImFiYyI="}`)
	p, res := projectWithRecordedPrivate(t, recorded)
	plain := mustAddr(t, `stub_ns.plain`)

	ri := res.State.ResourceInstance(plain)
	if ri == nil || ri.Current == nil {
		t.Fatalf("no object recorded for %s", plain)
	}
	if !bytes.Equal(ri.Current.Private, recorded) {
		t.Errorf("%s's projected private = %q, want the recorded %q: the import and read returned the framework's empty private and nothing put the apply-time hash back", plain, ri.Current.Private, recorded)
	}
	held := res.State.ResourceInstance(mustAddr(t, `stub_ns.held`))
	if held == nil || held.Current == nil || len(held.Current.Private) != 0 {
		t.Errorf("stub_ns.held has no record and gained a private anyway: %v", held)
	}

	// The plan carries it to the provider.
	var priorPrivate []byte
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if !r.PriorState.IsNull() && r.PriorState.GetAttr("name").AsString() == "plain" {
			priorPrivate = r.PriorPrivate
		}
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState, PlannedPrivate: r.PriorPrivate}
	}
	cfg := loadConfig(t, "testdata/timeouts")
	if _, diags := stubContext(t, p).Plan(context.Background(), cfg, res.State, &tofu.PlanOpts{Mode: plans.NormalMode, SetVariables: stubVariables()}); diags.HasErrors() {
		t.Fatalf("planning against the projected state: %s", diags.Err())
	}
	if !bytes.Equal(priorPrivate, recorded) {
		t.Errorf("PlanResourceChange for %s received PriorPrivate %q, want %q", plain, priorPrivate, recorded)
	}
}

// TestRestoreProviderPrivateSkipsAStaleRecord: a record whose captured
// identity names a different object is not restored. Handing one object's
// write-only hash to another's plan would tell the provider a value was
// sent that never was, and the replace it owes would be silenced.
func TestRestoreProviderPrivateSkipsAStaleRecord(t *testing.T) {
	ctx := context.Background()
	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("private1239"))
	addr := mustAddr(t, `stub_ns.plain`)
	recorded := []byte(`{"body_wo":"ImFiYyI="}`)
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = identityPayloadFrom(LocatedRecord{ImportID: "the-old-object"})
		env.Residue = &residueFields{ProviderPrivate: recorded}
	}); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	b := &builder{opts: Options{RecordStore: store}}

	stale := &states.ResourceInstanceObject{Status: states.ObjectReady}
	b.restoreProviderPrivate(ctx, wanted{addr: addr, importID: "the-new-object"}, stale)
	if len(stale.Private) != 0 {
		t.Errorf("restored %q onto a different object than the record names", stale.Private)
	}

	// The control: the same record, the same object.
	same := &states.ResourceInstanceObject{Status: states.ObjectReady}
	b.restoreProviderPrivate(ctx, wanted{addr: addr, importID: "the-old-object"}, same)
	if !bytes.Equal(same.Private, recorded) {
		t.Errorf("restored %q for the object the record names, want %q", same.Private, recorded)
	}
}
