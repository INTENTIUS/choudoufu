// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #391's third finding: [moduleOutputLookup] evaluates every
// output a child module declares to answer ONE cross-module reference, and
// used to abort its WHOLE returned object - refusing every other,
// independently-answerable output too - the instant any single one of them
// failed. terraform-aws-eks's own eks module ships 27 outputs; several need
// live attributes marker discovery has not swept yet, and which one a run
// hit first was non-deterministic (Go's own map iteration order over
// child.Module.Outputs). The function's own doc comment already promised
// the narrower behavior these tests assert ("refuses only that call"); the
// code did not implement it until this issue's fix.
//
// testdata/provider-config-demand-sibling-output is provider-config-
// demand's own fixture (issue #313's real shape: a provider block's
// argument crosses a data source, which crosses a module output, which
// crosses a managed resource attribute) with one addition: the child
// module declares a SECOND output, other_instance_id, over a DIFFERENT
// managed resource (aws_instance.other) that no test here ever supplies a
// live value for, so it is permanently unreadable.

// TestModuleOutputSiblingRefusalDoesNotBlockAnAnswerableOutput is case (a):
// data.aws_zone.of_cluster's own value (module.child.cluster_id) must
// resolve once aws_eks_cluster.this's value is supplied, regardless of
// aws_instance.other staying permanently unreadable and feeding a
// COMPLETELY DIFFERENT sibling output of the very same module call.
func TestModuleOutputSiblingRefusalDoesNotBlockAnAnswerableOutput(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand-sibling-output"), nil)

	live := map[string]cty.Value{
		"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("prod-cluster"),
		}),
		// Deliberately nothing for module.child.aws_instance.other: its own
		// output must stay unreadable, and that is exactly what the second
		// test below (the mutation check) pins down.
	}

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: live})
	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("aws_zone", "of_cluster"))
	if !ok {
		t.Fatalf("data.aws_zone.of_cluster was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.aws_zone.of_cluster refused even though its own dependency chain (aws_eks_cluster.this.id) is fully supplied: %s", src.ReasonDetail)
	}

	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			name := req.Config.GetAttr("name")
			if name.IsNull() || !name.IsKnown() {
				t.Fatalf("aws_zone was read with an unknown name: %#v", name)
			}
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    name,
				"zone_id": cty.StringVal("Z-" + name.AsString()),
			})}
		},
	}
	results, diags := ReadProviderConfigs(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	got, ok := results["data.aws_zone.of_cluster"]
	if !ok {
		t.Fatalf("no result under data.aws_zone.of_cluster; keys: %v", keysOf(results))
	}
	if zoneID := got.GetAttr("zone_id"); zoneID.AsString() != "Z-prod-cluster" {
		t.Fatalf("zone_id = %#v, want %#v - the value carried through module.child.cluster_id", zoneID, cty.StringVal("Z-prod-cluster"))
	}
}

// TestModuleOutputSiblingRefusalStillRefusesItsOwnReference is the mutation
// check: the fix must be precise, not a blanket "every module output
// always succeeds" hack. A reference that names the ACTUALLY-broken
// output (other_instance_id, over aws_instance.other, which this test
// still never supplies) must keep refusing, with the same class of reason
// it always had.
func TestModuleOutputSiblingRefusalStillRefusesItsOwnReference(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand-sibling-output"), nil)

	live := map[string]cty.Value{
		"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("prod-cluster"),
		}),
	}

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: live})
	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("aws_zone", "of_other"))
	if !ok {
		t.Fatalf("data.aws_zone.of_other was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("data.aws_zone.of_other resolved eligible, want refused: aws_instance.other.id was never supplied, so module.child.other_instance_id has no value to give it")
	}
}
