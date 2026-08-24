// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file is the day2_rename/corpus-lambda-simple unit: internal/live/mv/
// mv.go's materialize() (around line 944) calls projection.BuildFrom -
// the record-store-less convenience wrapper - instead of
// projection.BuildWith(..., projection.Options{RecordStore: ...}), the one
// live-plan's own path uses (internal/command/live_plan.go). A rename
// target whose OWN identity is identity.ClassParentDerived puts materialize
// on the "whole resolution list" branch (materialize's own doc comment:
// "the whole resolution list goes in for that case"), and that list, for
// any estate whose configuration also declares a record_store, ordinarily
// contains at least one identity.ClassRecordBacked sibling (corpus-lambda-
// simple's random_pet.this, local_file.archive_plan[0], and so on). Without
// the estate's RecordStore threaded through, the projection builder hits
// that sibling and raises "Record-backed instance with no record store"
// (internal/live/projection/build.go:1676) - reachable on the rename of a
// plainly taggable resource that has nothing to do with the record-backed
// sibling itself, wherever the two live in the same configuration.

const (
	materializeParentType  = "test_record_backed_parent"
	materializeChildType   = "test_taggable_child"
	materializeChildLiveID = "child-abc123"
)

// materializeRecordStoreProviderAddr is the one provider configuration
// every fixture below resolves to.
var materializeRecordStoreProviderAddr = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

func materializeParentSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Computed: true},
			},
		},
	}
}

func materializeChildSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Computed: true},
				"tags": {Type: cty.Map(cty.String), Optional: true},
			},
		},
	}
}

// materializeRecordStoreConfig loads a fixture that mirrors corpus-lambda-
// simple's own shape at a distance: one record-backed resource
// (materializeParentType, standing in for random_pet.this) and one
// ordinary taggable resource whose identity formula reads the first's "id"
// (materializeChildType, standing in for aws_lambda_function.this reading
// "${random_pet.this.id}-lambda-simple").
func materializeRecordStoreConfig(t *testing.T) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "test_record_backed_parent" "suffix" {
  provider = aws
}

resource "test_taggable_child" "this" {
  provider = aws
}
`)
	return loadConfigDir(t, dir)
}

// materializeRecordStoreProvider serves both fixture types out of one mock:
// materializeParentType is never read through it (materializeRecord reads
// the record store, not the provider), and materializeChildType answers
// ImportResourceState/ReadResource keyed by ID, exactly the pair mv.go's
// own materialize() drives for a "derived" resolution.
func materializeRecordStoreProvider(t *testing.T) providers.Interface {
	t.Helper()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				materializeParentType: materializeParentSchema(),
				materializeChildType:  materializeChildSchema(),
			},
		},
	}
	p.ConfigureProviderCalled = true

	childObj := cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(materializeChildLiveID),
		"tags": cty.MapValEmpty(cty.String),
	})

	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if r.TypeName != materializeChildType || r.Target.ID != materializeChildLiveID {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: childObj}},
		}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		idVal := r.PriorState.GetAttr("id")
		if idVal.IsNull() || !idVal.IsKnown() || idVal.AsString() != materializeChildLiveID {
			return providers.ReadResourceResponse{NewState: cty.NullVal(materializeChildSchema().Block.ImpliedType())}
		}
		return providers.ReadResourceResponse{NewState: childObj}
	}
	return p
}

// materializeRecordStoreFixture builds the mover, the parent/child
// addresses and the resolution list every test below shares; only the
// RecordStore handed to req varies between the positive case and its
// mutation check.
func materializeRecordStoreFixture(t *testing.T, store *projection.RecordStore) (m *mover, resolution identity.Resolution) {
	t.Helper()
	ctx := t.Context()

	parentAddr := mustAddr(t, materializeParentType+".suffix")
	childAddr := mustAddr(t, materializeChildType+".this")
	cfg := materializeRecordStoreConfig(t)
	provider := materializeRecordStoreProvider(t)

	if store != nil {
		parentVal := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("abc123")})
		if _, err := projection.SeedRecordForInstance(ctx, store, parentAddr, materializeRecordStoreProviderAddr, parentVal, nil, states.ObjectReady); err != nil {
			t.Fatalf("seeding the record-backed parent: %s", err)
		}
	}

	formula := &identity.Formula{
		Parts: []identity.Part{
			{Literal: "child-"},
			{Parent: &identity.ParentRef{Instance: parentAddr, Attr: "id"}},
		},
		Parents: []addrs.AbsResourceInstance{parentAddr},
	}
	resolution = identity.Resolution{Addr: childAddr, Class: identity.ClassParentDerived, Formula: formula}
	parentResolution := identity.Resolution{Addr: parentAddr, Class: identity.ClassRecordBacked}

	req := Request{
		Estate:      materializeStoreEstate,
		Old:         childAddr,
		New:         childAddr,
		Config:      cfg,
		Resolutions: []identity.Resolution{resolution, parentResolution},
		Providers:   projection.SingleProvider(materializeRecordStoreProviderAddr, provider),
		RecordStore: store,
	}
	m = &mover{
		req: req,
		res: &Result{
			Old:      childAddr,
			New:      childAddr,
			TypeName: materializeChildType,
			Anchor:   childAddr,
		},
		provider: provider,
		schema:   materializeChildSchema(),
	}
	return m, resolution
}

const materializeStoreEstate = "materialize-record-store-test"

// TestMaterializeThreadsTheRecordStoreThroughAParentDerivedRename is the
// positive case: a rename target whose own identity formula depends on a
// record-backed sibling in the SAME configuration must materialize
// successfully once the estate has a record store, exactly as live-plan's
// own projection.BuildWith call already does.
func TestMaterializeThreadsTheRecordStoreThroughAParentDerivedRename(t *testing.T) {
	ctx := t.Context()
	store := recordFallbackStore(t)
	m, resolution := materializeRecordStoreFixture(t, store)

	obj, diags := m.materialize(ctx, resolution)
	if diags.HasErrors() {
		t.Fatalf("materialize refused a parent-derived rename with the estate's own record store configured: %s", diags.Err())
	}
	if obj == nil {
		t.Fatal("materialize returned no object for a resolvable parent-derived rename")
	}
	got := obj.Value.GetAttr("id")
	if got.IsNull() || got.AsString() != materializeChildLiveID {
		t.Errorf("materialized object has id %#v, want %q (the formula rendered from the record-backed parent's live id)", got, materializeChildLiveID)
	}
}

// TestMaterializeWithNoRecordStoreStillRefusesTheSameWay is the mutation
// check named in the unit brief: an estate with NO record store must
// behave exactly as it does today. req.RecordStore is nil (no live block's
// record_store, or a caller - today, only this package's own unit tests -
// that never wired one in), so the record-backed sibling can never
// materialize and the whole rename is correctly refused with the internal-
// inconsistency diagnostic build.go itself raises - unreachable in
// practice once internal/live/lint's admission gate holds, but exactly the
// wording an operator whose estate never declared a record_store should
// still see if that gate were ever bypassed.
func TestMaterializeWithNoRecordStoreStillRefusesTheSameWay(t *testing.T) {
	ctx := t.Context()
	m, resolution := materializeRecordStoreFixture(t, nil)

	obj, diags := m.materialize(ctx, resolution)
	if obj != nil {
		t.Fatal("materialize returned an object for a record-backed sibling with no record store to read it from")
	}
	if !diags.HasErrors() {
		t.Fatal("materialize accepted a parent-derived rename whose formula depends on a record-backed sibling, with no record store configured")
	}
	const wantSummary = "Record-backed instance with no record store"
	var found bool
	for _, d := range diags {
		if d.Description().Summary == wantSummary {
			found = true
			if !strings.Contains(d.Description().Detail, "no record store was configured") {
				t.Errorf("the no-record-store refusal detail changed:\n%s", d.Description().Detail)
			}
		}
	}
	if !found {
		t.Errorf("no %q diagnostic; got %v", wantSummary, diags)
	}
}
