// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// Option A (the maintainer's ruling of 2026-10-20) reads a declared
// markerless instance with no record as not owned. A stock `moved` block
// does not re-key the record store (only live-mv does), so after a plain
// rename the instance's record sits at its old address. Before the fix the
// renamed instance read as unrecorded: on Kubernetes, whose family answers
// UnrecordedCreates, it was left out of the prior state and the plan
// proposed creating an object that already exists and is this estate's;
// on google, whose firewall answers UnrecordedNameHeld,
// corpus-k8s-infra-gcp-gcve's day2_rename refused with "Unlabelled live
// object holds the declared name".

const k8sStubType = "kubernetes_unlabelled_stub"

var k8sProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("kubernetes"),
}

// k8sStubSchema has no metadata block and no field_manager: no marker
// surface, and not a field-granular patch type.
func k8sStubSchema() providers.Schema {
	str := func() *configschema.Attribute {
		return &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	}
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": str(), "name": str(),
	}}}
}

const k8sStubID = "default/app"

// k8sStubCloud is a cluster where the stub object exists.
func k8sStubCloud() providers.Interface {
	schema := k8sStubSchema()
	obj := objectFor(schema, map[string]string{"id": k8sStubID, "name": "app"})
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{k8sStubType: schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    objectFor(schema, map[string]string{"id": r.Target.ID}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		if r.PriorState.GetAttr("id").AsString() == k8sStubID {
			return providers.ReadResourceResponse{NewState: obj}
		}
		return providers.ReadResourceResponse{NewState: cty.NullVal(schema.Block.ImpliedType())}
	}
	return p
}

// buildMovedK8s plans testdata/unrecorded-k8s-moved with the stub's record
// seeded at each of recordedAt.
func buildMovedK8s(t *testing.T, recordedAt ...string) (*Result, tfdiags.Diagnostics) {
	t.Helper()
	ctx := context.Background()
	rs := newTestLocatedStore(localHintStore(t), ownershipEstate).rs
	for _, at := range recordedAt {
		if _, err := SeedLocatedForInstance(ctx, rs, mustAddr(t, at), k8sProvider, LocatedRecord{ImportID: k8sStubID}); err != nil {
			t.Fatalf("seeding the record at %s: %s", at, err)
		}
	}
	return BuildWith(ctx, loadConfig(t, "testdata/unrecorded-k8s-moved"),
		[]identity.Resolution{{Addr: mustAddr(t, k8sStubType+".app"), Class: identity.ClassConcrete, ImportID: k8sStubID}},
		SingleProvider(k8sProvider, k8sStubCloud()),
		Options{RecordStore: rs, Ownership: &Ownership{Estate: ownershipEstate}})
}

// TestUnrecordedMoved_k8sRecordAtMovedFromStaysOwned: a record at exactly
// one moved-from address, the direct one or the head of the chain, keeps
// the renamed instance owned, so the plan is the move and proposes no
// create. Red without recordedAtMovedFrom: the instance is omitted as
// ReasonUnowned with the SummaryUnrecordedMarkerless warning.
func TestUnrecordedMoved_k8sRecordAtMovedFromStaysOwned(t *testing.T) {
	for _, at := range []string{k8sStubType + ".app_v2", k8sStubType + ".app_v1"} {
		t.Run(at, func(t *testing.T) {
			res, diags := buildMovedK8s(t, at)
			assertNoErrors(t, diags)
			if hasDiag(diags, SummaryUnrecordedMarkerless, "") {
				t.Errorf("the renamed instance was read as unrecorded, so the plan proposes its create:\n%s", renderDiags(diags))
			}
			assertMaterialized(t, res, []string{k8sStubType + ".app"})
		})
	}
}

// TestUnrecordedMoved_k8sRecordOutsideTheChainIsUnowned keeps option A
// whole beside the fix: no record, or one at an address no moved block
// leads from, is no claim on the object.
func TestUnrecordedMoved_k8sRecordOutsideTheChainIsUnowned(t *testing.T) {
	for _, at := range [][]string{nil, {k8sStubType + ".unrelated"}} {
		t.Run(strings.Join(at, ","), func(t *testing.T) {
			res, diags := buildMovedK8s(t, at...)
			assertNoErrors(t, diags)
			assertOmitted(t, res, map[string]Reason{k8sStubType + ".app": ReasonUnowned})
			if !hasDiag(diags, SummaryUnrecordedMarkerless, k8sStubID) {
				t.Errorf("no %q warning:\n%s", SummaryUnrecordedMarkerless, renderDiags(diags))
			}
		})
	}
}

// TestUnrecordedMoved_severalRecordedOriginsRefuseByName: when more than one
// moved-from address holds a record, nothing says which is this instance's.
// The plan refuses with an error naming the instance and every recorded
// moved-from address; it neither picks one nor falls through to option A's
// generic warning. Red before the rule: the commit this rule replaced
// answered false and fell through to SummaryUnrecordedMarkerless.
func TestUnrecordedMoved_severalRecordedOriginsRefuseByName(t *testing.T) {
	v1, v2 := k8sStubType+".app_v1", k8sStubType+".app_v2"
	res, diags := buildMovedK8s(t, v1, v2)
	errs := errorDiagsWith(diags, SummaryMovedRecordsAmbiguous)
	if len(errs) != 1 {
		t.Fatalf("want one %q error, got:\n%s", SummaryMovedRecordsAmbiguous, renderDiags(diags))
	}
	detail := errs[0].Description().Detail
	for _, want := range []string{k8sStubType + ".app", v1, v2} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not name %s:\n%s", want, detail)
		}
	}
	if hasDiag(diags, SummaryUnrecordedMarkerless, "") || hasDiag(diags, SummaryNameHeld, "") {
		t.Errorf("the ambiguity fell through to option A's generic refusal:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{k8sStubType + ".app": ReasonUnowned})
}
