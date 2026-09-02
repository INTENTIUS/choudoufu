// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// A cloud resource whose identity is derived from a record-backed parent
// (internal/live/identity's resolver.parentPart record-backed branch) is
// only resolvable because of an ordering inside builder.run that nothing
// used to depend on: every ClassRecordBacked resolution is materialized
// into b.live BEFORE the concrete and derived phases run, so a formula
// naming a record-backed parent finds its value already there. renderFormula
// itself is class-agnostic - it looks the parent up in b.live and does not
// ask what class it was.
//
// The two tests below pin both halves of that. If someone moves the
// record-backed loop after the derived loop, or teaches renderFormula to
// consult the class, TestDerivedFromRecordBackedParent goes red rather than
// the fix quietly degrading into "the plan proposes creating it every run",
// which is what ReasonParentUnavailable looks like from the outside.

// writeRecordParentFixture writes a module where a log group's name is
// derived from a null_resource's id: one record-backed parent, one
// cloud-backed child.
func writeRecordParentFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
resource "null_resource" "trigger" {
  triggers = {
    input = "value"
  }
}

resource "aws_cloudwatch_log_group" "child" {
  name = "log-${null_resource.trigger.id}"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// twoProviders serves the null provider and the fake-cloud aws provider
// from one [Providers], which no existing helper does: SingleProvider
// answers a single AbsProviderConfig and refuses every other by design.
func twoProviders(t *testing.T, cloud *fakeCloud) Providers {
	t.Helper()
	null := nullResourceProvider()
	aws := cloud.provider(t)
	return ProviderFunc(func(_ context.Context, want addrs.AbsProviderConfig) (providers.Interface, error) {
		switch want.Provider.Type {
		case "null":
			return null, nil
		default:
			return aws, nil
		}
	})
}

// seedNullRecord puts a null_resource record carrying the given id into a
// fresh local store, and returns the store and its key prefix.
func seedNullRecord(t *testing.T, addr addrs.AbsResourceInstance, id string) (staterecord.Store, string) {
	t.Helper()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/record-parent"
	val := cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"triggers": cty.MapVal(map[string]cty.Value{"input": cty.StringVal("value")}),
	})
	payload, err := encodeRecordPayload(val, nil, states.ObjectReady)
	if err != nil {
		t.Fatalf("encoding the fixture payload: %s", err)
	}
	if _, err := store.PutIfAbsent(context.Background(), RecordKey(prefix, addr), payload); err != nil {
		t.Fatalf("seeding the store: %s", err)
	}
	return store, prefix
}

// recordParentResolutions returns the two resolutions deliberately
// child-first, so that a build which happened to work only on a favourable
// input order would not pass.
func recordParentResolutions(parent, child addrs.AbsResourceInstance) []identity.Resolution {
	return []identity.Resolution{
		{
			Addr:  child,
			Class: identity.ClassParentDerived,
			Formula: &identity.Formula{
				Parts: []identity.Part{
					{Literal: "log-"},
					{Parent: &identity.ParentRef{Instance: parent, Attr: "id"}},
				},
				Parents: []addrs.AbsResourceInstance{parent},
			},
		},
		{Addr: parent, Class: identity.ClassRecordBacked},
	}
}

// TestDerivedFromRecordBackedParent: with the parent's record present, the
// child's formula renders from it and the child is imported by the exact ID
// that composition produces. This is the ordering claim - the parent's
// value has to be in b.live before the derived phase begins, and the
// record-backed phase is the only thing that puts it there.
func TestDerivedFromRecordBackedParent(t *testing.T) {
	cfg := loadConfig(t, writeRecordParentFixture(t))
	parent := mustAddr(t, `null_resource.trigger`)
	child := mustAddr(t, `aws_cloudwatch_log_group.child`)

	store, prefix := seedNullRecord(t, parent, "abc123")

	cloud := newFakeCloud()
	cloud.put("aws_cloudwatch_log_group", "log-abc123",
		map[string]string{"id": "log-abc123", "name": "log-abc123"})

	res, diags := BuildWith(context.Background(), cfg, recordParentResolutions(parent, child),
		twoProviders(t, cloud), Options{
			RecordStore: NewRecordEnvelopeStore(store, prefix),
		})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{
		`aws_cloudwatch_log_group.child`,
		`null_resource.trigger`,
	})

	// The import the child was asked for is the whole proof that the
	// parent's record was read before the formula rendered.
	wantImport := "aws_cloudwatch_log_group/log-abc123"
	found := false
	for _, got := range cloud.imports {
		if got == wantImport {
			found = true
		}
	}
	if !found {
		t.Errorf("provider was asked to import %v, want %s among them", cloud.imports, wantImport)
	}
}

// TestDerivedFromAbsentRecordBackedParent is the first-run half, and the
// reason the ordering above cannot be replaced by "just look the parent up
// lazily". With no record yet, materializeRecord omits the parent with
// ReasonAbsent, and the child has to be omitted as ReasonParentUnavailable
// rather than imported against a hole - which makes the plan propose
// creating both, in dependency order, exactly as a stock run would.
func TestDerivedFromAbsentRecordBackedParent(t *testing.T) {
	cfg := loadConfig(t, writeRecordParentFixture(t))
	parent := mustAddr(t, `null_resource.trigger`)
	child := mustAddr(t, `aws_cloudwatch_log_group.child`)

	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}

	cloud := newFakeCloud() // the child does not exist live either

	res, diags := BuildWith(context.Background(), cfg, recordParentResolutions(parent, child),
		twoProviders(t, cloud), Options{
			RecordStore: NewRecordEnvelopeStore(store, "tofu-records/record-parent"),
		})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, nil)
	assertOmitted(t, res, map[string]Reason{
		`null_resource.trigger`:          ReasonAbsent,
		`aws_cloudwatch_log_group.child`: ReasonParentUnavailable,
	})

	// Nothing was imported: a child whose parent has no record must not
	// reach the provider with a half-rendered ID.
	if len(cloud.imports) != 0 {
		t.Errorf("provider was asked to import %v with no record for the parent", cloud.imports)
	}
}
