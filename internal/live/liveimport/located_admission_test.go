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

// This file is corpus-alb-complete/test_plan's first #364 wall: ratifyOne's
// admission gate (identity.LookupType or admittedByProviderSchema) never
// asked identity.LocatedType, the third way a type can be admitted (GitHub
// issue #270: MarkerlessTypes membership, no ratified row, and a schema
// this run can fully record an identity from). aws_cognito_user_pool_client
// is exactly this shape - see internal/live/identity's
// docimportid_possessive_test.go, which already pins that LocatedType
// admits it once schemas are in hand. Before locatedByProviderSchema
// existed, live-import stamped it StatusUnadmittedType and wrote no record
// at all, so live-plan read it [ABSENT] and proposed creating a second one
// beside the real, already-migrated object.

const locatedAdmissionEstate = "located-admission-estate"

// cognitoUserPoolClientLiveSchema is the same shape
// internal/live/identity/docimportid_possessive_test.go's
// cognitoUserPoolClientBlock uses, restated here rather than exported
// across packages: a required user_pool_id, a client-set name, a
// provider-minted id, and a Sensitive, non-Deprecated client_secret that
// this route must never touch. No tags argument at all - Cognito user pool
// CLIENTS (unlike the pool itself) carry no tags.
func cognitoUserPoolClientLiveSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"user_pool_id":  {Type: cty.String, Required: true},
		"name":          {Type: cty.String, Required: true},
		"id":            {Type: cty.String, Computed: true},
		"client_secret": {Type: cty.String, Computed: true, Sensitive: true},
	}}}
}

type locatedAdmissionProvider struct {
	*tofu.MockProvider
	reads int
}

func newLocatedAdmissionProvider() *locatedAdmissionProvider {
	p := &locatedAdmissionProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"aws_cognito_user_pool_client": cognitoUserPoolClientLiveSchema(),
			},
		},
	}}
	p.ConfigureProviderCalled = true
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		p.reads++
		// The live object matches the state exactly: this test is about
		// admission and recording, not drift.
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	return p
}

func (p *locatedAdmissionProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return p, nil
}

func locatedAdmissionState(attrsJSON string) *states.State {
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_cognito_user_pool_client", Name: "this"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(attrsJSON),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")},
		addrs.NoKey,
	)
	return state
}

const cognitoUserPoolClientAttrs = `{"id":"3ho4ek12345678909nh3fmhpko","user_pool_id":"us-west-2_abc123","name":"app","client_secret":"s3cr3t"}`

func locatedAdmissionStoreOrFail(t *testing.T) *projection.RecordStore {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	return projection.NewRecordEnvelopeStore(store, projection.RecordKeyPrefix(locatedAdmissionEstate))
}

// TestRatifyAdmitsAMarkerlessNoRatifiedRowTypeThroughLocatedType is the
// admission fix itself, asserted where a wrong answer would have shown
// before this unit: a type with NO row in identity.LookupType and no wire
// identity schema (admittedByProviderSchema also answers false for it) must
// still be admitted - as UNTAGGABLE, with a residue carrier - rather than
// StatusUnadmittedType, when identity.LocatedType would route it onto the
// record-located path.
func TestRatifyAdmitsAMarkerlessNoRatifiedRowTypeThroughLocatedType(t *testing.T) {
	store := locatedAdmissionStoreOrFail(t)
	p := newLocatedAdmissionProvider()

	rat, diags := Ratify(context.Background(), Request{
		Estate:      locatedAdmissionEstate,
		State:       locatedAdmissionState(cognitoUserPoolClientAttrs),
		Providers:   p,
		RecordStore: store,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	if got := len(rat.Entries); got != 1 {
		t.Fatalf("got %d entries, want 1", got)
	}
	entry := rat.Entries[0]
	if entry.Status == StatusUnadmittedType {
		t.Fatalf("status = UNADMITTED_TYPE: ratifyOne's admission gate did not consult identity.LocatedType, "+
			"so this type - no ratified row, no wire identity schema, but a documented import ID grammar "+
			"identity.LocatedType already admits it through - is unreachable, exactly corpus-alb-complete's "+
			"own #309 wall: %s", entry.Detail)
	}
	if entry.Status != StatusUntaggable {
		t.Errorf("status = %s, want %s - the type has no tags argument at all", entry.Status, StatusUntaggable)
	}

	rep, diags := rat.Approve(context.Background())
	if diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	out := onlyOutcome(t, rep)
	if out.Outcome != OutcomeSkipped {
		t.Errorf("outcome = %s, want %s", out.Outcome, OutcomeSkipped)
	}

	addr := mustAddr(t, "aws_cognito_user_pool_client.this")
	rec, _, _, found, err := store.GetIdentity(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading the identity record for %s: %s", addr, err)
	}
	if !found {
		t.Fatalf("no identity record was written for %s - this is #309's exact symptom: the FIRST live-plan "+
			"after this migrate reads it [ABSENT] and proposes creating a duplicate beside the real object", addr)
	}
	// BY VALUE, and in the documented order: a reading that swapped the
	// user-pool and client segments would be the same shape, the same
	// length and a different object (see docimportid_possessive_test.go).
	const want = "us-west-2_abc123/3ho4ek12345678909nh3fmhpko"
	if rec.ImportID != want {
		t.Errorf("recorded import ID = %q, want %q", rec.ImportID, want)
	}
}

// TestRatifyDoesNotRecordAnUnadmittedInstance is the mutation-check
// boundary: an instance whose type genuinely has neither a ratified row,
// nor a wire identity schema, nor a LocatedType-admitting shape (here,
// because the fixture is registered under a DIFFERENT type name the fake
// schema map does not answer for) still refuses as UNADMITTED_TYPE and
// writes no record - a wrong answer here would be exactly HANDOFF's
// forbidden shape: fabricating a record for an instance the ratified state
// gives no evidence for.
func TestRatifyDoesNotRecordAnUnadmittedInstance(t *testing.T) {
	store := locatedAdmissionStoreOrFail(t)
	p := &locatedAdmissionProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{}, // no schema for anything
		},
	}}
	p.ConfigureProviderCalled = true

	rat, diags := Ratify(context.Background(), Request{
		Estate:      locatedAdmissionEstate,
		State:       locatedAdmissionState(cognitoUserPoolClientAttrs),
		Providers:   p,
		RecordStore: store,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify returned errors: %s", diags.Err())
	}
	entry := rat.Entries[0]
	if entry.Status != StatusUnadmittedType {
		t.Fatalf("status = %s, want %s - the fake provider serves no schema for this type at all, so nothing "+
			"should have been able to admit it", entry.Status, StatusUnadmittedType)
	}

	if _, diags := rat.Approve(context.Background()); diags.HasErrors() {
		t.Fatalf("Approve returned errors: %s", diags.Err())
	}
	addr := mustAddr(t, "aws_cognito_user_pool_client.this")
	if _, _, _, found, err := store.GetIdentity(context.Background(), addr); err != nil {
		t.Fatalf("reading the identity record for %s: %s", addr, err)
	} else if found {
		t.Fatalf("a record was written for an UNADMITTED_TYPE instance - a fabricated identity with no ratified " +
			"evidence behind it, exactly the failure HANDOFF's safety rule forbids")
	}
}
