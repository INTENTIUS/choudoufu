// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #340. A migration wrote markers for stampable resources and
// nothing at all for record-backed ones, so a migrated estate's random_pet,
// null_resource, terraform_data and local_file lost their generated values
// at the moment of migration: live-import reported success, and the first
// live-plan afterwards proposed creating every one of them from scratch.
//
// Everything in this file asserts on the RENDERED VALUE that ends up in the
// store - the pet name itself, read back out of the payload's own JSON -
// rather than on a "did it write" boolean, because a record written with the
// wrong value produces an EMPTY plan that is wrong, which no verdict-level
// check can see.

const petEstate = "pet-estate"

// petName is the generated value the whole mechanism exists to carry. It is
// the only place it exists: no cloud API can be asked for it.
const petName = "quietly-noble-lemur"

// recordTestPrefix is what a real run passes: [projection.RecordKeyPrefix]
// over the estate, since these tests declare no key_prefix override.
func recordTestPrefix() string { return projection.RecordKeyPrefix(petEstate) }

// petSchema is hashicorp/random's random_pet, narrowed to the attributes
// that matter here. Note there is no "tags" attribute and there could not
// be: this is the whole reason the type never reaches a stamp.
func petSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":        {Type: cty.String, Computed: true},
		"keepers":   {Type: cty.Map(cty.String), Optional: true},
		"length":    {Type: cty.Number, Optional: true},
		"prefix":    {Type: cty.String, Optional: true},
		"separator": {Type: cty.String, Optional: true},
	}}}
}

// petProvider answers the schema and counts reads. hashicorp/random's own
// ReadResource is a pure prior-passthrough, and this one records that it was
// asked at all so a test can assert the migration never pretends to consult
// a live system that has nothing to say.
type petProvider struct {
	*tofu.MockProvider
	reads int
}

func newPetProvider() *petProvider {
	pp := &petProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"random_pet": petSchema(),
				// hashicorp/local's local_file, narrowed. content is
				// Sensitive in the real schema, which is what
				// TestApprove_RecordsAnObjectCarryingASensitiveAttribute
				// is about.
				"local_file": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":       {Type: cty.String, Computed: true},
					"filename": {Type: cty.String, Required: true},
					"content":  {Type: cty.String, Optional: true, Sensitive: true},
				}}},
			},
		},
	}}
	pp.ConfigureProviderCalled = true
	pp.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		pp.reads++
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	return pp
}

func (p *petProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

// petState is a one-resource tfstate: random_pet.this, exactly as plain
// terraform leaves it after the cold apply corpus-lambda-simple performs.
func petState(id string) *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "random_pet", Name: "this"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"` + id + `","keepers":null,"length":2,"prefix":null,"separator":"-"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("random")},
		addrs.NoKey,
	)
	return state
}

func petStore(t *testing.T) staterecord.Store {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return store
}

// recordedID reads the value a record actually holds for addr, straight out
// of the store, decoding the payload as JSON rather than through this
// package's own encoder - so a bug that encoded and decoded symmetrically
// wrong would still be caught. "" means no record at that key.
func recordedID(t *testing.T, store staterecord.Store, addr addrs.AbsResourceInstance) string {
	t.Helper()
	raw, _, exists, err := store.Get(context.Background(), projection.RecordKey(recordTestPrefix(), addr))
	if err != nil {
		t.Fatalf("reading the record for %s: %s", addr, err)
	}
	if !exists {
		return ""
	}
	var payload struct {
		Object struct {
			Attrs json.RawMessage `json:"attrs"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("the stored record for %s is not JSON: %s", addr, err)
	}
	var attrs struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(payload.Object.Attrs, &attrs); err != nil {
		t.Fatalf("the stored record's attrs for %s are not JSON: %s", addr, err)
	}
	return attrs.ID
}

func petRatify(t *testing.T, store staterecord.Store, state *states.State, p *petProvider) *Ratification {
	t.Helper()
	rat, diags := Ratify(context.Background(), Request{
		Estate:      petEstate,
		State:       state,
		Providers:   p,
		RecordStore: projection.NewRecordEnvelopeStore(store, recordTestPrefix()),
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	return rat
}

func onlyOutcome(t *testing.T, rep *StampReport) StampOutcome {
	t.Helper()
	if len(rep.Outcomes) != 1 {
		t.Fatalf("got %d outcomes, want exactly 1: %+v", len(rep.Outcomes), rep.Outcomes)
	}
	return rep.Outcomes[0]
}

// TestApprove_SeedsTheRecordStoreForARecordBackedInstance is the positive
// case, and the direct counterpart of the corpus-lambda-simple failure: after
// an approved migration the record store holds random_pet.this's generated
// name, which is the only thing a later live-plan can resolve
// "${random_pet.this.id}-lambda-simple" from.
func TestApprove_SeedsTheRecordStoreForARecordBackedInstance(t *testing.T) {
	addr := mustAddr(t, "random_pet.this")
	store := petStore(t)
	p := newPetProvider()

	rat := petRatify(t, store, petState(petName), p)

	if got := len(rat.Entries); got != 1 {
		t.Fatalf("got %d entries, want 1", got)
	}
	entry := rat.Entries[0]
	if entry.Status != StatusUntaggable {
		t.Errorf("status = %s, want %s (a record-backed type has no live object to tag): %s", entry.Status, StatusUntaggable, entry.Detail)
	}
	if entry.LiveID != petName {
		t.Errorf("LiveID = %q, want %q", entry.LiveID, petName)
	}

	// Ratify writes nothing, this mechanism included.
	if got := recordedID(t, store, addr); got != "" {
		t.Fatalf("Ratify seeded the record store with %q; Ratify must never write", got)
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeRecorded {
		t.Errorf("outcome = %s, want %s: %s", out.Outcome, OutcomeRecorded, out.Detail)
	}
	if got := recordedID(t, store, addr); got != petName {
		t.Errorf("the record store holds id = %q, want %q - this is #340's exact symptom", got, petName)
	}

	// No live read was made for it. There is nothing in any cloud to ask.
	if p.reads != 0 {
		t.Errorf("the provider was read %d time(s) for a record-backed instance; the state is its only source of truth", p.reads)
	}
}

// TestApprove_WithoutARecordStoreLeavesARecordBackedInstanceSkipped is the
// mutation check for the fix, and the reproduction of #340 itself: remove
// the store - the one stated obstacle - and the value goes nowhere, exactly
// as it did before this path existed. It also pins the deliberate
// no-record_store behavior, where a record-backed type is not admitted for
// planning either.
func TestApprove_WithoutARecordStoreLeavesARecordBackedInstanceSkipped(t *testing.T) {
	addr := mustAddr(t, "random_pet.this")
	// A real store, deliberately NOT handed to Ratify: it is the witness
	// that nothing was written anywhere, not a participant.
	witness := petStore(t)
	p := newPetProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    petEstate,
		State:     petState(petName),
		Providers: p,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %s, want %s with no record_store declared: %s", out.Outcome, OutcomeSkipped, out.Detail)
	}
	if got := recordedID(t, witness, addr); got != "" {
		t.Errorf("something wrote %q to a store this run was never given", got)
	}
}

// TestApprove_IsIdempotentForARecordBackedInstance holds the same promise
// OutcomeAlreadyStamped holds for a tag: running the migration twice over
// the same state file writes once.
func TestApprove_IsIdempotentForARecordBackedInstance(t *testing.T) {
	addr := mustAddr(t, "random_pet.this")
	store := petStore(t)

	first, diags := petRatify(t, store, petState(petName), newPetProvider()).Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("the first Approve returned errors: %s", diags.Err())
	}
	if got := onlyOutcome(t, first).Outcome; got != OutcomeRecorded {
		t.Fatalf("the first outcome = %s, want %s", got, OutcomeRecorded)
	}

	second, diags := petRatify(t, store, petState(petName), newPetProvider()).Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("the second Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, second)
	if out.Outcome != OutcomeAlreadyRecorded {
		t.Errorf("the second outcome = %s, want %s: %s", out.Outcome, OutcomeAlreadyRecorded, out.Detail)
	}
	if got := recordedID(t, store, addr); got != petName {
		t.Errorf("the record store holds id = %q after two runs, want %q", got, petName)
	}
}

// TestApprove_NeverOverwritesAnExistingRecord is the "a wrong marker
// outranks a missing one" half, applied to the record - the carrier a
// record-backed resource's identity actually has. The store's value can be
// newer than the state file a migration was pointed at, so a mismatch is a
// loud refusal and the stored value is left exactly as it was.
func TestApprove_NeverOverwritesAnExistingRecord(t *testing.T) {
	addr := mustAddr(t, "random_pet.this")
	store := petStore(t)

	// An earlier migration - or an apply since - established one value.
	if _, diags := petRatify(t, store, petState("the-live-value"), newPetProvider()).Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("seeding the first value returned errors: %s", diags.Err())
	}
	if got := recordedID(t, store, addr); got != "the-live-value" {
		t.Fatalf("setup failed: the record holds %q", got)
	}

	// Now migrate again from a STALE state file naming a different value.
	rep, diags := petRatify(t, store, petState("a-stale-tfstates-value"), newPetProvider()).Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeFailed {
		t.Errorf("outcome = %s, want %s: %s", out.Outcome, OutcomeFailed, out.Detail)
	}
	if got := recordedID(t, store, addr); got != "the-live-value" {
		t.Errorf("the record now holds %q; a migration must never overwrite a record it did not write", got)
	}
}

// TestApprove_RecordsAnObjectCarryingASensitiveAttribute pins what a
// migration writes for a record-backed instance whose object is MARKED.
//
// hashicorp/local marks local_file.content sensitive and local_file is
// RECORD_BACKED, so a real migration decodes a marked value out of the state
// (Decode re-applies AttrSensitivePaths) and hands it to an encoder that
// ctyjson would panic on. Both halves are asserted here: the value reaches
// the store, and so does the fact that it was sensitive.
//
// The second half used to pin the OPPOSITE - "nothing in the payload records
// that content was sensitive" - as a deliberate limitation. It cost this
// estate its sixth wall: live-plan runs with SkipRefresh, so the record's
// marks are the only marks the plan's "before" side has, and an unmarked
// before against a schema-marked after is a difference. corpus-lambda-simple
// replanned "~ content = (sensitive value)" with OpenTofu's own renderer
// saying "The value is unchanged", forever.
func TestApprove_RecordsAnObjectCarryingASensitiveAttribute(t *testing.T) {
	addr := mustAddr(t, "local_file.archive_plan")

	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "local_file", Name: "archive_plan"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"d8186d18","filename":"builds/plan.json","content":"a-secret-build-plan"}`),
			AttrSensitivePaths: []cty.PathValueMarks{
				{Path: cty.GetAttrPath("content"), Marks: cty.NewValueMarks(marks.Sensitive)},
			},
			Status: states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("local")},
		addrs.NoKey,
	)

	store := petStore(t)
	p := newPetProvider()
	rat, diags := Ratify(context.Background(), Request{
		Estate:      petEstate,
		State:       state,
		Providers:   p,
		RecordStore: projection.NewRecordEnvelopeStore(store, recordTestPrefix()),
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if out := onlyOutcome(t, rep); out.Outcome != OutcomeRecorded {
		t.Fatalf("outcome = %s, want %s: %s", out.Outcome, OutcomeRecorded, out.Detail)
	}

	raw, _, exists, err := store.Get(context.Background(), projection.RecordKey(recordTestPrefix(), addr))
	if err != nil || !exists {
		t.Fatalf("reading the record: exists = %v, err = %v", exists, err)
	}
	var payload struct {
		Object struct {
			Attrs json.RawMessage `json:"attrs"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("the stored record is not JSON: %s", err)
	}
	var attrs struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(payload.Object.Attrs, &attrs); err != nil {
		t.Fatalf("the stored attrs are not JSON: %s", err)
	}
	if attrs.Content != "a-secret-build-plan" {
		t.Errorf("the record holds content = %q, want the state's own value", attrs.Content)
	}
	// The mark, pinned by VALUE rather than by "the word sensitive appears
	// somewhere": the payload has to name the path that was marked, in the
	// state file's own encoding, or projection cannot re-apply it.
	var sens struct {
		Object struct {
			SensitiveAttrs json.RawMessage `json:"sensitive_attributes"`
		} `json:"object"`
	}
	if err := json.Unmarshal(raw, &sens); err != nil {
		t.Fatalf("the stored record is not JSON: %s", err)
	}
	if got, want := string(sens.Object.SensitiveAttrs), `[[{"type":"get_attr","value":"content"}]]`; got != want {
		t.Errorf("the record's sensitive_attributes = %s, want %s", got, want)
	}
	// And nothing else in the object is claimed sensitive: filename and id
	// are not marked in the state, so a migration that marked them would be
	// inventing sensitivity rather than carrying it.
	for _, attr := range []string{"filename", `"id"`} {
		if bytes.Contains(sens.Object.SensitiveAttrs, []byte(attr)) {
			t.Errorf("the record's sensitive_attributes names %s, which the state did not mark: %s", attr, sens.Object.SensitiveAttrs)
		}
	}
}

// TestRecordBackedTypeReadsTheGeneratedTable is the derivation check: this
// mechanism keys on identity.TypeIdentity.RecordBacked and on nothing else,
// so it covers every type row-gen classifies that way rather than a list
// someone maintains here. All four providers the class spans are checked,
// plus two negatives.
func TestRecordBackedTypeReadsTheGeneratedTable(t *testing.T) {
	for _, typeName := range []string{"random_pet", "null_resource", "terraform_data", "local_file", "time_sleep", "random_id"} {
		if !recordBackedType(typeName) {
			t.Errorf("%s is not treated as record-backed; the generated table says it is", typeName)
		}
	}
	for _, typeName := range []string{"aws_vpc", "aws_totally_fictional_type"} {
		if recordBackedType(typeName) {
			t.Errorf("%s is treated as record-backed and must not be", typeName)
		}
	}
}
