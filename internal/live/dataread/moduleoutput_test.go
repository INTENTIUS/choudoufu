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

// TestModuleOutputSiblingDependencyDoesNotPoisonAnUnrelatedOutput is
// corpus-eks-basic's own gauntlet wall (test_plan), traced to a fourth
// finding one layer under the two tests above: even once a sibling
// output's own VALUE failure stops leaking, evaluating that output still
// recorded whatever DATA SOURCE dependency its own expression touched onto
// the OUTER classify() call's shared dependency set. moduleOutputLookup's
// per-output loop shares ONE `record` closure - analyze.go's own
// lookupFactory/classify tracking, which decides Source.Deps and
// therefore Source.Eligible - across every output it evaluates while
// answering ONE cross-module reference, not scoped to the output the
// caller actually reads.
//
// The child module's third output, poison_output, is wired so it ALWAYS
// refuses classification outright (data.aws_zone.poison names a managed
// resource in depends_on, rule 4, unconditionally) and is reached ONLY by
// evaluating module.child as a whole, the same way other_instance_id is -
// never by of_cluster's own argument, which names nothing but
// module.child.cluster_id. Before this fix, data.aws_zone.of_cluster's own
// Deps wrongly included data.aws_zone.poison, and poison's own permanent
// refusal propagated onto of_cluster through the ordinary dependency-
// refusal path (analyze.go's classify(), "it depends on %s, which cannot
// be read before the plan") - exactly corpus-eks-basic's real wall,
// reproduced here with no cloud and no floci.
func TestModuleOutputSiblingDependencyDoesNotPoisonAnUnrelatedOutput(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "provider-config-demand-sibling-output"), nil)

	live := map[string]cty.Value{
		"module.child.aws_eks_cluster.this": cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal("prod-cluster"),
		}),
	}

	analysis := AnalyzeProviderConfigs(context.Background(), cfg, Options{LiveManagedResults: live})
	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("aws_zone", "of_cluster"))
	if !ok {
		t.Fatalf("data.aws_zone.of_cluster was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.aws_zone.of_cluster refused, poisoned by an unrelated sibling output's own dependency (module.child.poison_output, gated behind data.aws_zone.poison's depends_on on a managed resource) that of_cluster's own argument (module.child.cluster_id) never names: %s", src.ReasonDetail)
	}
	for _, dep := range src.Deps {
		if dep.Resource.Type == "aws_zone" && dep.Resource.Name == "poison" {
			t.Fatalf("data.aws_zone.of_cluster's own Deps wrongly include data.aws_zone.poison, a dependency of the unrelated sibling output poison_output, not of cluster_id: %v", src.Deps)
		}
	}
}
