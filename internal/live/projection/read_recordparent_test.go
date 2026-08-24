// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #391's own shape, the reason [ReadInstances]'s recordBacked
// branch now materializes from opts.RecordStore instead of always omitting:
// corpus-eks-basic's aws_eks_cluster.this[0] is a ClassParentDerived
// resolution whose identity FORMULA names random_string.suffix, a GitHub
// issue #364 record-backed instance with no live object at all - its value
// exists only in the estate's record store. [builder.renderFormula] never
// looks a parent up on its own; it only consults b.live, so that parent's
// value has to already be in the SAME [ReadInstances] call's read set for
// the derived instance's own formula to render at all.
//
// This file's fixtures stand in for that shape with types a mock provider
// can serve without a real cloud: null_resource.suffix plays
// random_string.suffix's role (record-backed, no live object), and
// aws_cloudwatch_log_group.app plays aws_eks_cluster.this[0]'s (a live-read
// resource whose own import ID is a formula over the parent's value).

const recordParentEstate = "record-parent-unit"

func recordParentAddrs(t *testing.T) (parent, child addrs.AbsResourceInstance) {
	t.Helper()
	return mustAddr(t, `null_resource.suffix`), mustAddr(t, `aws_cloudwatch_log_group.app`)
}

// recordParentFormula is the hand-built ClassParentDerived formula every
// test in this file uses: the child's import ID is the literal "log-"
// followed by the parent's own "id" attribute, once known.
func recordParentFormula(parent addrs.AbsResourceInstance) *identity.Formula {
	return &identity.Formula{
		Parts: []identity.Part{
			{Literal: "log-"},
			{Parent: &identity.ParentRef{Instance: parent, Attr: "id"}},
		},
		Parents: []addrs.AbsResourceInstance{parent},
	}
}

// recordParentProviders combines the null and aws mocks a single call needs:
// [SingleProvider] only ever serves one provider configuration, and this
// fixture genuinely declares two.
func recordParentProviders(t *testing.T, cloud *fakeCloud) Providers {
	t.Helper()
	null := nullResourceProvider()
	aws := cloud.provider(t)
	return ProviderFunc(func(_ context.Context, want addrs.AbsProviderConfig) (providers.Interface, error) {
		switch want.String() {
		case nullProvider.String():
			return null, nil
		case awsProvider.String():
			return aws, nil
		default:
			t.Fatalf("no provider configured for %s in this test", want)
			return nil, nil
		}
	})
}

// seedRecordParent writes the parent's record into store, exactly as a
// migration would (SeedRecordForInstance, GitHub issue #340's writer).
func seedRecordParent(t *testing.T, store staterecord.Store, prefix string, parent addrs.AbsResourceInstance, id string) {
	t.Helper()
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"triggers": cty.MapValEmpty(cty.String),
	})
	seeded, err := testSeedRecordForInstance(context.Background(), store, prefix, parent, val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("seeding the record-backed parent: %s", err)
	}
	if seeded != SeedWritten {
		t.Fatalf("seeding the record-backed parent into an empty store = %v, want SeedWritten", seeded)
	}
}

// TestReadInstancesRendersAParentDerivedFormulaOverARecordBackedParent is
// case (a) of GitHub issue #391's unit: a two-hop chain - a record-rung
// value, then the managed identity it feeds - resolving in the SAME
// [ReadInstances] call once opts.RecordStore is supplied.
func TestReadInstancesRendersAParentDerivedFormulaOverARecordBackedParent(t *testing.T) {
	cfg := loadConfig(t, "testdata/record-parent-derived")
	parent, child := recordParentAddrs(t)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/" + recordParentEstate
	seedRecordParent(t, store, prefix, parent, "934zdibr")

	cloud := newFakeCloud()
	readOwned(cloud, "log-934zdibr")

	resolutions := []identity.Resolution{
		{Addr: parent, Class: identity.ClassRecordBacked},
		{Addr: child, Class: identity.ClassParentDerived, Formula: recordParentFormula(parent)},
	}

	got, diags := ReadInstances(context.Background(), cfg, resolutions, recordParentProviders(t, cloud), Options{
		Ownership:   &Ownership{Estate: readEstate},
		RecordStore: NewRecordEnvelopeStore(store, prefix),
	})
	assertNoErrors(t, diags)

	if len(got.Unread) != 0 {
		t.Fatalf("unread %v, want nothing: the parent's record and the child's live read should both succeed", got.Unread)
	}
	parentVal, ok := got.Values[parent.String()]
	if !ok {
		t.Fatalf("no value for the record-backed parent %s; keys: %v", parent, readKeys(got))
	}
	if id := parentVal.GetAttr("id"); id.AsString() != "934zdibr" {
		t.Errorf("parent id = %q, want %q", id.AsString(), "934zdibr")
	}
	childVal, ok := got.Values[child.String()]
	if !ok {
		t.Fatalf("no value for the parent-derived child %s; keys: %v", child, readKeys(got))
	}
	if name := childVal.GetAttr("name"); name.AsString() != "log-934zdibr" {
		t.Errorf("child name = %q, want %q (the rendered formula, parent id read from the record store)", name.AsString(), "log-934zdibr")
	}
}

// TestReadInstancesCapsARecordBackedReadCycle is case (b): a Formula whose
// parent NAMES ITSELF is a genuinely unresolvable demand (identity
// resolution never produces one; this fixture proves ReadInstances' own
// orderWork refuses it with the honest cyclic-formula diagnostic rather
// than spinning). It is the same termination guarantee
// [statelessProviderDataReads]'s and [statelessResolve]'s own bounded
// passes rely on one level up: a single [ReadInstances] call never loops on
// its own input, however that input is shaped.
func TestReadInstancesCapsARecordBackedReadCycle(t *testing.T) {
	cfg := loadConfig(t, "testdata/record-parent-derived")
	parent, child := recordParentAddrs(t)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/" + recordParentEstate

	cloud := newFakeCloud()

	// The child's own formula names itself as a parent - unresolvable by
	// construction, and nothing this run can read would ever change that.
	selfFormula := &identity.Formula{
		Parts:   []identity.Part{{Parent: &identity.ParentRef{Instance: child, Attr: "id"}}},
		Parents: []addrs.AbsResourceInstance{child},
	}
	resolutions := []identity.Resolution{
		{Addr: parent, Class: identity.ClassRecordBacked},
		{Addr: child, Class: identity.ClassParentDerived, Formula: selfFormula},
	}

	got, diags := ReadInstances(context.Background(), cfg, resolutions, recordParentProviders(t, cloud), Options{
		Ownership:   &Ownership{Estate: readEstate},
		RecordStore: NewRecordEnvelopeStore(store, prefix),
	})
	assertNoErrors(t, diags)

	if _, ok := got.Values[child.String()]; ok {
		t.Fatalf("%s was materialized from a self-referencing formula; it must refuse instead", child)
	}
	reasons := unreadReasons(got)
	if got := reasons[child.String()]; got != ReasonCycle {
		t.Errorf("%s omitted with reason %v, want %v", child, got, ReasonCycle)
	}
}

// TestReadInstancesOmitsARecordBackedParentWithoutRecordStore is the
// mutation check for case (a): remove opts.RecordStore (the record-rung
// supply) and the identical two-hop case must fail again, for the same
// honest reason it always did before this unit - the parent omitted as
// unreadable, and the child's formula left unrendered for want of it.
func TestReadInstancesOmitsARecordBackedParentWithoutRecordStore(t *testing.T) {
	cfg := loadConfig(t, "testdata/record-parent-derived")
	parent, child := recordParentAddrs(t)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/" + recordParentEstate
	seedRecordParent(t, store, prefix, parent, "934zdibr")

	cloud := newFakeCloud()
	readOwned(cloud, "log-934zdibr")

	resolutions := []identity.Resolution{
		{Addr: parent, Class: identity.ClassRecordBacked},
		{Addr: child, Class: identity.ClassParentDerived, Formula: recordParentFormula(parent)},
	}

	// Same fixture, same seeded record, same resolutions as the positive
	// test above - only opts.RecordStore is gone.
	got, diags := ReadInstances(context.Background(), cfg, resolutions, recordParentProviders(t, cloud), Options{
		Ownership: &Ownership{Estate: readEstate},
	})
	assertNoErrors(t, diags)

	if len(got.Values) != 0 {
		t.Fatalf("read %v with no RecordStore given, want nothing: the parent has no live object and this call was never told where its record is", readKeys(got))
	}
	reasons := unreadReasons(got)
	if got := reasons[parent.String()]; got != ReasonUnreadable {
		t.Errorf("parent omitted with reason %v, want %v", got, ReasonUnreadable)
	}
	if got := reasons[child.String()]; got != ReasonParentUnavailable {
		t.Errorf("child omitted with reason %v, want %v (its formula's one parent was never read)", got, ReasonParentUnavailable)
	}
}
