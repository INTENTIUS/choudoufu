// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #341. A migration recorded residue only for a resource it
// could STAMP, because ratifyOne returned at the taggability check before
// building the one carrier Approve's residue call took. So every admitted
// resource whose provider schema has no tags argument migrated with no
// residue record at all, and the first live-plan afterwards proposed a
// phantom update for every argument the provider's own Read never gives back
// - forever, since applying it does not settle it.
//
// The measured case is aws_route53_record.allow_overwrite: Route 53 has no
// such field, so no read anywhere can return it. .corpus/mastino/global/dns
// crossed clean through migrate with all 63 rendered identities correct and
// still planned "0 to add, 14 to change, 0 to destroy", the whole diff being
// one "+ allow_overwrite = true" line each.
//
// Everything here asserts on the RENDERED VALUE the store ends up holding,
// never on a "did it write" boolean: a residue record with the wrong value
// produces an EMPTY plan that is wrong, which no verdict-level check sees.

const untaggableEstate = "untaggable-residue-estate"

// routeRecordSchema is hashicorp/aws's aws_route53_record, narrowed to what
// this mechanism looks at. There is no tags attribute and there could not be
// - a Route 53 record set carries no tags at all, which is the whole reason
// the type never reaches a stamp - and allow_overwrite is the residue-shaped
// argument: Optional, non-Sensitive, and a pure input the API has no field
// for.
func routeRecordSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":              {Type: cty.String, Computed: true},
		"zone_id":         {Type: cty.String, Required: true},
		"name":            {Type: cty.String, Required: true},
		"type":            {Type: cty.String, Required: true},
		"ttl":             {Type: cty.Number, Optional: true},
		"allow_overwrite": {Type: cty.Bool, Optional: true},
	}}}
}

// attachmentSchema is aws_iam_role_policy_attachment, the second untaggable
// admitted type this file uses. It exists so the derivation check below has a
// type from an unrelated service with an unrelated identity shape: nothing in
// the fix may key on which type this is.
func attachmentSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":              {Type: cty.String, Computed: true},
		"role":            {Type: cty.String, Required: true},
		"policy_arn":      {Type: cty.String, Required: true},
		"allow_overwrite": {Type: cty.Bool, Optional: true},
	}}}
}

// taggableRecordSchema is routeRecordSchema with a tags argument bolted on.
// It is not a real AWS type; it is the control for the derivation check -
// same resource type name, same residue-shaped argument, one schema
// difference - so a test can prove the fix keys on the SCHEMA and not on the
// type name.
func taggableRecordSchema() providers.Schema {
	blk := *routeRecordSchema().Block
	attrs := make(map[string]*configschema.Attribute, len(blk.Attributes)+1)
	for k, v := range blk.Attributes {
		attrs[k] = v
	}
	attrs["tags"] = &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
}

// untaggableProvider answers those schemas and reproduces the SDKv2 shape
// this whole class turns on: ReadResource never sources allow_overwrite from
// anywhere, it returns whatever PriorState already held. Every other
// attribute is answered fresh from a fixed "live" object, which is how a test
// can tell "recorded because residue-shaped" apart from "recorded because
// everything is".
//
// It counts the calls Approve's tag write would make, so a test can assert by
// construction that an untaggable resource is never stamped.
type untaggableProvider struct {
	*tofu.MockProvider
	reads  int
	plans  int
	writes int
}

func newUntaggableProvider() *untaggableProvider {
	up := &untaggableProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"aws_route53_record":             routeRecordSchema(),
				"aws_iam_role_policy_attachment": attachmentSchema(),
				"aws_route53_zone":               taggableRecordSchema(),
			},
		},
	}}
	up.ConfigureProviderCalled = true
	up.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		up.reads++
		prior := r.PriorState
		if prior == cty.NilVal || prior.IsNull() || !prior.Type().IsObjectType() {
			return providers.ReadResourceResponse{NewState: cty.NullVal(cty.DynamicPseudoType)}
		}
		out := make(map[string]cty.Value, len(prior.Type().AttributeTypes()))
		for name, ty := range prior.Type().AttributeTypes() {
			switch name {
			case "allow_overwrite":
				// Never read from the remote. Whatever the prior held is what
				// comes back - null included. This is the residue shape.
				out[name] = prior.GetAttr(name)
			case "id":
				out[name] = prior.GetAttr(name)
			default:
				out[name] = liveAnswerFor(name, ty)
			}
		}
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(out)}
	}
	up.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		up.plans++
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	up.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		up.writes++
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return up
}

// liveAnswerFor is what the fake remote holds for an attribute the provider
// really does read. The values match untaggableState's own, so nothing here
// looks like drift.
func liveAnswerFor(name string, ty cty.Type) cty.Value {
	switch name {
	case "zone_id":
		return cty.StringVal("ZLIVE0001")
	case "name":
		return cty.StringVal("staging3.datacite.org")
	case "type":
		return cty.StringVal("A")
	case "ttl":
		return cty.NumberIntVal(300)
	case "role":
		return cty.StringVal("service-role")
	case "policy_arn":
		return cty.StringVal("arn:aws:iam::aws:policy/ReadOnlyAccess")
	case "tags":
		return cty.MapValEmpty(cty.String)
	}
	return cty.NullVal(ty)
}

func (p *untaggableProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

// untaggableState is a one-resource tfstate carrying allow_overwrite = true,
// exactly as stock terraform leaves it after a cold apply of a block that
// declares it - and the only place that value has ever existed.
func untaggableState(typeName, name string, attrsJSON string) *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(attrsJSON),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return state
}

const routeRecordAttrs = `{"id":"ZLIVE0001_staging3.datacite.org_A","zone_id":"ZLIVE0001","name":"staging3.datacite.org","type":"A","ttl":300,"allow_overwrite":true}`

func untaggableResidueStore(t *testing.T) *projection.RecordStore {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(untaggableEstate))
}

// recordedResidue reads what the residue store actually holds for addr.
func recordedResidue(t *testing.T, store *projection.RecordStore, addr addrs.AbsResourceInstance) map[string]cty.Value {
	t.Helper()
	attrs, _, _, exists, err := store.GetResidue(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the residue record for %s: %s", addr, err)
	}
	if !exists {
		return nil
	}
	return attrs
}

func untaggableRatify(t *testing.T, store *projection.RecordStore, state *states.State, p *untaggableProvider) *Ratification {
	t.Helper()
	rat, diags := Ratify(context.Background(), Request{
		Estate:      untaggableEstate,
		State:       state,
		Providers:   p,
		RecordStore: store,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	return rat
}

// TestApprove_RecordsResidueForAnUntaggableResource is #341's direct
// counterpart: after an approved migration of an untaggable aws_route53_record
// the residue store holds allow_overwrite = true, which is the only thing a
// later cold live-plan can fill it from.
//
// It also pins every count the fix must NOT move. Eighteen live/e2e crossing
// scripts assert live-import's two summary lines by exact string, and the
// verdict for an untaggable resource is still "there is nowhere to write a
// marker" - so the status stays UNTAGGABLE, the outcome stays SKIPPED, and
// nothing is planned or applied against the live object.
func TestApprove_RecordsResidueForAnUntaggableResource(t *testing.T) {
	addr := mustAddr(t, "aws_route53_record.wp-prod-staging")
	store := untaggableResidueStore(t)
	p := newUntaggableProvider()

	rat := untaggableRatify(t, store, untaggableState("aws_route53_record", "wp-prod-staging", routeRecordAttrs), p)

	if got := len(rat.Entries); got != 1 {
		t.Fatalf("got %d entries, want 1", got)
	}
	entry := rat.Entries[0]
	if entry.Status != StatusUntaggable {
		t.Errorf("status = %s, want %s - an untaggable resource's verdict must not move: %s", entry.Status, StatusUntaggable, entry.Detail)
	}
	if entry.LiveID != "ZLIVE0001_staging3.datacite.org_A" {
		t.Errorf("LiveID = %q, want the state's own id", entry.LiveID)
	}

	// Ratify writes nothing, and it makes no live call for an untaggable
	// resource either: there is nothing a read could tell it that would
	// change any of the three things it decides.
	if got := recordedResidue(t, store, addr); got != nil {
		t.Fatalf("Ratify recorded residue %v; Ratify must never write", got)
	}
	if p.reads != 0 {
		t.Errorf("Ratify read the provider %d time(s) for an untaggable resource; it has no verdict to draw from one", p.reads)
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %s, want %s - the MARKER write is what is skipped, and that is what this axis reports: %s", out.Outcome, OutcomeSkipped, out.Detail)
	}
	if p.plans != 0 || p.writes != 0 {
		t.Errorf("the provider was asked to plan %d and apply %d change(s) for an untaggable resource; a migration must never attempt a tag write on one", p.plans, p.writes)
	}

	attrs := recordedResidue(t, store, addr)
	if attrs == nil {
		t.Fatalf("no residue was recorded for %s - this is #341's exact symptom: the FIRST live-plan after this migrate proposes \"+ allow_overwrite = true\" and applying it never settles", addr)
	}
	got, ok := attrs["allow_overwrite"]
	if !ok {
		t.Fatalf("residue attrs = %v, want allow_overwrite present", attrs)
	}
	if !got.RawEquals(cty.True) {
		t.Errorf("residue allow_overwrite = %#v, want %#v", got, cty.True)
	}
	// The arguments the provider really does read must NOT be recorded. A
	// residue record that answered for them would mask real out-of-band drift
	// with a stored value, which is the empty-plan-that-is-wrong failure the
	// whole classifier exists to avoid.
	for _, name := range []string{"zone_id", "name", "type", "ttl"} {
		if _, ok := attrs[name]; ok {
			t.Errorf("%q was recorded as residue; the provider answers it fresh from the remote, so classifyResidue must not have judged it residue-shaped", name)
		}
	}
}

// TestApprove_UntaggableWithoutARecordStoreRecordsNothing is the mutation
// check for the fix and the reproduction of #341 itself: remove the store -
// the one stated obstacle - and the value goes nowhere, exactly as it did
// before this path existed. It also pins the deliberate no-record_store
// behaviour, where an estate simply keeps the visible perpetual diff.
func TestApprove_UntaggableWithoutARecordStoreRecordsNothing(t *testing.T) {
	addr := mustAddr(t, "aws_route53_record.wp-prod-staging")
	// A real store, deliberately NOT handed to Ratify: the witness that
	// nothing was written anywhere, rather than a participant.
	witness := untaggableResidueStore(t)
	p := newUntaggableProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    untaggableEstate,
		State:     untaggableState("aws_route53_record", "wp-prod-staging", routeRecordAttrs),
		Providers: p,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if out := onlyOutcome(t, rep); out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %s, want %s", out.Outcome, OutcomeSkipped)
	}
	if got := recordedResidue(t, witness, addr); got != nil {
		t.Errorf("something wrote %v to a store this run was never given", got)
	}
	if p.reads != 0 {
		t.Errorf("the provider was read %d time(s) with no record store to write to", p.reads)
	}
}

// TestApprove_UntaggableResidueIsIdempotent holds the same promise
// OutcomeAlreadyStamped holds for a tag and OutcomeAlreadyRecorded for a
// record: live-import -approve is documented as safe to re-run, so a second
// migration over the same state file must not fail on the residue write.
func TestApprove_UntaggableResidueIsIdempotent(t *testing.T) {
	addr := mustAddr(t, "aws_route53_record.wp-prod-staging")
	store := untaggableResidueStore(t)
	ctx := context.Background()

	for i := 1; i <= 2; i++ {
		rat := untaggableRatify(t, store, untaggableState("aws_route53_record", "wp-prod-staging", routeRecordAttrs), newUntaggableProvider())
		if _, diags := rat.Approve(ctx); diags.HasErrors() {
			t.Fatalf("Approve run %d returned errors: %s", i, diags.Err())
		}
		attrs := recordedResidue(t, store, addr)
		if attrs == nil || !attrs["allow_overwrite"].RawEquals(cty.True) {
			t.Fatalf("after run %d the residue store holds %v, want allow_overwrite = true", i, attrs)
		}
	}
}

// TestUntaggableResidueKeysOnTheSchemaAndNotTheType is the derivation check,
// and the one that would fail if someone ever "fixed" this by naming
// aws_route53_record anywhere.
//
// Three resources, one run:
//
//   - aws_route53_record       untaggable  -> residue recorded, never stamped
//   - aws_iam_role_policy_attachment
//     untaggable  -> residue recorded, never stamped
//     (a different service, a different identity shape, no cloud
//     object of its own to speak of - and the same code path)
//   - aws_route53_zone         TAGGABLE    -> stamped, and residue recorded
//     through the path issue #327 already
//     built
//
// The only thing separating the third from the first two here is a tags
// attribute in the schema, which is exactly the property [taggable] reads.
func TestUntaggableResidueKeysOnTheSchemaAndNotTheType(t *testing.T) {
	store := untaggableResidueStore(t)
	p := newUntaggableProvider()

	state := states.NewState()
	add := func(typeName, name, attrsJSON string) {
		state.RootModule().SetResourceInstanceCurrent(
			addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.Instance(addrs.NoKey),
			&states.ResourceInstanceObjectSrc{AttrsJSON: []byte(attrsJSON), Status: states.ObjectReady},
			addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
			addrs.NoKey,
		)
	}
	add("aws_route53_record", "wp-prod-staging", routeRecordAttrs)
	add("aws_iam_role_policy_attachment", "ro",
		`{"id":"service-role/arn:aws:iam::aws:policy/ReadOnlyAccess","role":"service-role","policy_arn":"arn:aws:iam::aws:policy/ReadOnlyAccess","allow_overwrite":true}`)
	add("aws_route53_zone", "production",
		`{"id":"ZLIVE0001","zone_id":"ZLIVE0001","name":"staging3.datacite.org","type":"A","ttl":300,"allow_overwrite":true,"tags":{}}`)

	rat := untaggableRatify(t, store, state, p)
	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}

	wantStatus := map[string]Status{
		"aws_route53_record.wp-prod-staging": StatusUntaggable,
		"aws_iam_role_policy_attachment.ro":  StatusUntaggable,
		"aws_route53_zone.production":        StatusVerified,
	}
	for _, e := range rat.Entries {
		want, known := wantStatus[e.Addr.String()]
		if !known {
			t.Fatalf("unexpected entry %s", e.Addr)
		}
		if e.Status != want {
			t.Errorf("%s status = %s, want %s: %s", e.Addr, e.Status, want, e.Detail)
		}
	}

	wantOutcome := map[string]Outcome{
		"aws_route53_record.wp-prod-staging": OutcomeSkipped,
		"aws_iam_role_policy_attachment.ro":  OutcomeSkipped,
		"aws_route53_zone.production":        OutcomeStamped,
	}
	for _, o := range rep.Outcomes {
		if o.Outcome != wantOutcome[o.Addr.String()] {
			t.Errorf("%s outcome = %s, want %s: %s", o.Addr, o.Outcome, wantOutcome[o.Addr.String()], o.Detail)
		}
	}

	// All three carry the same residue-shaped argument and all three must
	// have it recorded - two of them through the path this issue added.
	for _, s := range []string{
		"aws_route53_record.wp-prod-staging",
		"aws_iam_role_policy_attachment.ro",
		"aws_route53_zone.production",
	} {
		addr := mustAddr(t, s)
		attrs := recordedResidue(t, store, addr)
		if attrs == nil {
			t.Errorf("no residue was recorded for %s", addr)
			continue
		}
		if !attrs["allow_overwrite"].RawEquals(cty.True) {
			t.Errorf("%s residue allow_overwrite = %#v, want %#v", addr, attrs["allow_overwrite"], cty.True)
		}
	}
}

// TestUntaggableResidueSurvivesAnUnadmittedType confirms the fix did not
// widen what a migration touches. An unadmitted type still gets no carrier of
// any kind, so nothing reads it, nothing classifies it, and nothing is
// stored: this run has no identity knowledge for it and a residue record
// about an object it cannot address would be worse than none.
func TestUntaggableResidueSurvivesAnUnadmittedType(t *testing.T) {
	store := untaggableResidueStore(t)
	p := newUntaggableProvider()
	p.GetProviderSchemaResponse.ResourceTypes["aws_totally_fictional_type"] = routeRecordSchema()

	rat := untaggableRatify(t, store,
		untaggableState("aws_totally_fictional_type", "x", routeRecordAttrs), p)

	if got := rat.Entries[0].Status; got != StatusUnadmittedType {
		t.Fatalf("status = %s, want %s", got, StatusUnadmittedType)
	}
	if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	if got := recordedResidue(t, store, mustAddr(t, "aws_totally_fictional_type.x")); got != nil {
		t.Errorf("residue %v was recorded for an unadmitted type", got)
	}
	if p.reads != 0 {
		t.Errorf("the provider was read %d time(s) for an unadmitted type", p.reads)
	}
}
