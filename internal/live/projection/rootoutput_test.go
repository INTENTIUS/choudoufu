// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

func newRootOutputStore(t *testing.T, estate string) (*RootOutputStore, staterecord.Store) {
	t.Helper()
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return NewRootOutputStore(raw, estate), raw
}

// TestRootOutputStoreRoundTripsByValue is the by-value pin: whatever goes
// into the store has to come back out as the SAME cty value, type included.
// A value that comes back with a different type is not a cosmetic problem -
// it becomes the "before" side of a plan's output diff, and a string that
// returned as a number would render a change that is not there.
func TestRootOutputStoreRoundTripsByValue(t *testing.T) {
	store, _ := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	cases := map[string]cty.Value{
		"a_string": cty.StringVal("builds/b982f072.zip"),
		"a_number": cty.NumberIntVal(42),
		"a_bool":   cty.False,
		"a_null":   cty.NullVal(cty.String),
		"a_list":   cty.ListVal([]cty.Value{cty.StringVal("x"), cty.StringVal("y")}),
		"an_object": cty.ObjectVal(map[string]cty.Value{
			"arn":  cty.StringVal("arn:aws:lambda:us-east-1:1:function:f"),
			"size": cty.NumberIntVal(7),
		}),
		"an_empty_tuple": cty.EmptyTupleVal,
	}

	for name, want := range cases {
		if _, err := store.Put(ctx, name, want, ""); err != nil {
			t.Fatalf("Put(%q): %s", name, err)
		}
	}
	for name, want := range cases {
		got, version, exists, err := store.Get(ctx, name)
		if err != nil {
			t.Fatalf("Get(%q): %s", name, err)
		}
		if !exists {
			t.Fatalf("Get(%q): nothing recorded", name)
		}
		if version == "" {
			t.Errorf("Get(%q): no version returned for a record that exists", name)
		}
		if !got.RawEquals(want) {
			t.Errorf("Get(%q) = %#v, want %#v", name, got, want)
		}
	}

	if _, _, exists, err := store.Get(ctx, "never_written"); err != nil || exists {
		t.Errorf("Get of an unwritten name = (exists %v, err %v), want (false, nil)", exists, err)
	}
}

// TestRootOutputStoreNilIsNothingRemembered pins the no-record_store case: a
// nil store answers "nothing recorded" rather than panicking, which is what
// makes every caller's nil check optional rather than load-bearing.
func TestRootOutputStoreNilIsNothingRemembered(t *testing.T) {
	if s := NewRootOutputStore(nil, "example-estate"); s != nil {
		t.Fatalf("NewRootOutputStore(nil, ...) = %v, want nil", s)
	}
	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	if s := NewRootOutputStore(raw, ""); s != nil {
		t.Fatalf("NewRootOutputStore(store, \"\") = %v, want nil", s)
	}

	var nilStore *RootOutputStore
	if _, _, exists, err := nilStore.Get(t.Context(), "anything"); err != nil || exists {
		t.Errorf("nil Get = (exists %v, err %v), want (false, nil)", exists, err)
	}
	if _, err := nilStore.Put(t.Context(), "anything", cty.StringVal("x"), ""); err == nil {
		t.Errorf("nil Put returned no error; a caller that has nowhere to write must be told so")
	}
	// The two bulk entry points must be silent no-ops on a nil store, since
	// that is how every estate without a record_store block reaches them.
	if got := ReadRootOutputValues(t.Context(), nil, nil); got != nil {
		t.Errorf("ReadRootOutputValues(nil, nil) = %#v, want nil", got)
	}
	WriteRootOutputValues(t.Context(), nil, states.NewState())
}

// TestRootOutputStoreRefusesARecordForAnotherOutput pins the key/payload
// cross-check. A key that has been copied, renamed or hand-edited into
// pointing at another output must answer about NOTHING rather than about
// that other output: the answer becomes a plan's "before" side, and one
// output's value standing in for another's renders a change that is not
// there or hides one that is.
func TestRootOutputStoreRefusesARecordForAnotherOutput(t *testing.T) {
	store, raw := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	payload, err := json.Marshal(rootOutputPayload{
		FormatVersion: rootOutputFormatVersion,
		Name:          "some_other_output",
		Type:          json.RawMessage(`"string"`),
		Value:         json.RawMessage(`"not mine"`),
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if _, err := raw.PutIfAbsent(ctx, RootOutputKey("example-estate", "mine"), payload); err != nil {
		t.Fatalf("PutIfAbsent: %s", err)
	}

	_, _, exists, err := store.Get(ctx, "mine")
	if err == nil {
		t.Fatalf("Get returned no error for a record naming another output")
	}
	if exists {
		t.Errorf("Get reported exists=true for a record it refused")
	}
	if !strings.Contains(err.Error(), "some_other_output") {
		t.Errorf("Get error %q does not name the output the record claims to be for", err)
	}
}

// TestRootOutputStoreRefusesAnUnknownFormat pins the version gate: a payload
// this build does not understand is refused rather than half-read into a
// value that would become a plan's "before" side.
func TestRootOutputStoreRefusesAnUnknownFormat(t *testing.T) {
	store, raw := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	payload := []byte(`{"formatVersion":"tofu-live-root-output-v99","name":"mine","type":"string","value":"x"}`)
	if _, err := raw.PutIfAbsent(ctx, RootOutputKey("example-estate", "mine"), payload); err != nil {
		t.Fatalf("PutIfAbsent: %s", err)
	}
	if _, _, exists, err := store.Get(ctx, "mine"); err == nil || exists {
		t.Errorf("Get of an unknown format = (exists %v, err %v), want (false, non-nil)", exists, err)
	}
}

// TestRootOutputPayloadIsNotAResourceRecord is the namespace-safety pin one
// layer down from the key: even if one of these payloads somehow reached
// [decodeRecordPayload] - the function orphan discovery's destroy path reads
// a record with - it must not decode into a resource object. The payload
// shares no field name with [recordPayload] precisely so this holds.
func TestRootOutputPayloadIsNotAResourceRecord(t *testing.T) {
	payload, err := json.Marshal(rootOutputPayload{
		FormatVersion: rootOutputFormatVersion,
		Name:          "mine",
		Type:          json.RawMessage(`"string"`),
		Value:         json.RawMessage(`"x"`),
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if _, _, _, err := decodeRecordPayload(payload); err == nil {
		t.Fatalf("decodeRecordPayload accepted a root output payload; the two shapes must stay mutually unreadable")
	}
}

// TestRootOutputNamespaceIsDisjointFromTheRecordRoot pins the reason this is
// a sixth namespace rather than a key under the record root: orphan
// discovery lists the record root and proposes DESTROYING what it finds with
// no configuration behind it. A root output names no live object at all, so
// one of its keys turning up in that listing would be a destroy proposal for
// something that never existed.
func TestRootOutputNamespaceIsDisjointFromTheRecordRoot(t *testing.T) {
	const estate = "example-estate"
	key := RootOutputKey(estate, "local_filename")

	for _, other := range []string{
		// GitHub issue #364 collapsed the located, residue and provisioned
		// namespaces into the record root itself; RecordKeyPrefix alone now
		// stands in for what used to be four separate prefixes here.
		RecordKeyPrefix(estate),
	} {
		if strings.HasPrefix(key, other+"/") || strings.HasPrefix(other, RootOutputKeyPrefix(estate)+"/") {
			t.Errorf("the root output key %q and the namespace %q overlap; every namespace in this store must be prefix-disjoint from every other", key, other)
		}
	}
	if !strings.HasPrefix(key, "tofu-outputs/"+estate+"/") {
		t.Errorf("RootOutputKey = %q, want it rooted at tofu-outputs/%s/", key, estate)
	}
	// An estate's keys must not reach another estate's.
	if strings.HasPrefix(RootOutputKey("example-estate-two", "x"), RootOutputKeyPrefix(estate)+"/") {
		t.Errorf("one estate's root output key falls under another estate's prefix")
	}
}

// TestWriteRootOutputValuesSkipsSensitive pins the one deliberate omission
// from what a migration and a write-back carry across. A sensitive output's
// value is NOT written, because HANDOFF.md's "no secrets stored by the tool"
// toggle has no wiring that reaches this namespace yet and writing first
// would put material into a store an operator who had turned that toggle on
// believed was free of it. The cost is that a sensitive output keeps
// rendering as "+ name = (sensitive value)", which is what it rendered as
// before this namespace existed.
func TestWriteRootOutputValuesSkipsSensitive(t *testing.T) {
	store, _ := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	state := states.NewState()
	root := state.RootModule()
	root.SetOutputValue("plain", cty.StringVal("visible"), false, "")
	root.SetOutputValue("secret", cty.StringVal("hunter2"), true, "")

	WriteRootOutputValues(ctx, store, state)

	got, _, exists, err := store.Get(ctx, "plain")
	if err != nil || !exists {
		t.Fatalf("Get(plain) = (exists %v, err %v), want a recorded value", exists, err)
	}
	if !got.RawEquals(cty.StringVal("visible")) {
		t.Errorf("Get(plain) = %#v, want %#v", got, cty.StringVal("visible"))
	}
	if _, _, exists, err := store.Get(ctx, "secret"); err != nil || exists {
		t.Errorf("Get(secret) = (exists %v, err %v), want nothing recorded: a sensitive output's value must not be written", exists, err)
	}
}

// TestWriteRootOutputValuesUpdatesAnExistingRecord pins the apply path: the
// value an apply settles must REPLACE what the last one settled, unlike a
// record-backed resource's record, which [SeedRecordForInstance] refuses to
// overwrite with different bytes.
//
// The two rules differ because the two records mean different things. A
// resource record is an identity, and a stale one adopts or displaces a real
// object, so a conflicting write is a hard refusal. This is a remembered
// value with nothing behind it, and refusing to update it would freeze the
// plan's "before" side at whatever the migration saw - every apply after the
// first would then render a change it had already made.
func TestWriteRootOutputValuesUpdatesAnExistingRecord(t *testing.T) {
	store, _ := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	first := states.NewState()
	first.RootModule().SetOutputValue("filename", cty.StringVal("builds/old.zip"), false, "")
	WriteRootOutputValues(ctx, store, first)

	second := states.NewState()
	second.RootModule().SetOutputValue("filename", cty.StringVal("builds/new.zip"), false, "")
	WriteRootOutputValues(ctx, store, second)

	got, _, exists, err := store.Get(ctx, "filename")
	if err != nil || !exists {
		t.Fatalf("Get = (exists %v, err %v), want a recorded value", exists, err)
	}
	if !got.RawEquals(cty.StringVal("builds/new.zip")) {
		t.Errorf("Get = %#v, want the value the SECOND state settled on", got)
	}
}

// TestReadRootOutputValuesIsBoundedByTheConfiguration pins what stops this
// namespace needing a listing: only the names the configuration declares are
// ever asked for. A key left behind by a deleted `output` block is therefore
// never read, never reported and never swept - it sits inert, which is the
// whole reason a namespace with no List is safe to leave keys in.
func TestReadRootOutputValuesIsBoundedByTheConfiguration(t *testing.T) {
	store, _ := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	state := states.NewState()
	root := state.RootModule()
	// cert_id is declared by the testdata/output-eval fixture; the other two
	// are not, and stand in for outputs a later edit removed.
	root.SetOutputValue("cert_id", cty.StringVal("cert-999"), false, "")
	root.SetOutputValue("deleted_one", cty.StringVal("gone"), false, "")
	root.SetOutputValue("deleted_two", cty.StringVal("also gone"), false, "")
	WriteRootOutputValues(ctx, store, state)

	got := ReadRootOutputValues(ctx, store, loadConfig(t, "testdata/output-eval"))
	if _, ok := got["deleted_one"]; ok {
		t.Errorf("ReadRootOutputValues returned %q, which the configuration does not declare", "deleted_one")
	}
	if _, ok := got["deleted_two"]; ok {
		t.Errorf("ReadRootOutputValues returned %q, which the configuration does not declare", "deleted_two")
	}
	if v, ok := got["cert_id"]; !ok {
		t.Errorf("ReadRootOutputValues did not return cert_id, which the configuration does declare and the store holds")
	} else if !v.RawEquals(cty.StringVal("cert-999")) {
		t.Errorf("cert_id = %#v, want %#v", v, cty.StringVal("cert-999"))
	}
}

// TestReadRootOutputValuesSkipsAnUnreadableRecord pins the blast-radius rule
// this whole file inherits from [ApplyRootOutputValues]: nothing here may
// fail a run. A corrupt record costs its own output a prior value and
// nothing else - not the other outputs, and not the estate.
func TestReadRootOutputValuesSkipsAnUnreadableRecord(t *testing.T) {
	store, raw := newRootOutputStore(t, "example-estate")
	ctx := t.Context()

	if _, err := raw.PutIfAbsent(ctx, RootOutputKey("example-estate", "cert_id"), []byte("{not json")); err != nil {
		t.Fatalf("PutIfAbsent: %s", err)
	}
	if _, err := store.Put(ctx, "cert_label", cty.StringVal("cert-cert-999"), ""); err != nil {
		t.Fatalf("Put: %s", err)
	}

	got := ReadRootOutputValues(ctx, store, loadConfig(t, "testdata/output-eval"))
	if _, ok := got["cert_id"]; ok {
		t.Errorf("cert_id was returned from a record that is not valid JSON")
	}
	if v, ok := got["cert_label"]; !ok {
		t.Errorf("cert_label was dropped because a DIFFERENT output's record was corrupt")
	} else if !v.RawEquals(cty.StringVal("cert-cert-999")) {
		t.Errorf("cert_label = %#v, want %#v", v, cty.StringVal("cert-cert-999"))
	}
}
