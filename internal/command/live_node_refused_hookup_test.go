// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1730: the command layer hands the instances the static
// evaluator refused (projection.NodeResolver.StaticRefusals) to discovery
// as discovery.Request.NodeRefused, through nodeRefusedAddrs, and the
// Kubernetes leg binds an object whose address annotation names one of
// them (#1640). internal/live/discovery tests the binding with a
// hand-built NodeRefused; nothing below the kind scenario
// (k8s-greenfield's step 6) tested that a real plan builds that set and
// passes it on. These do, through both entry points that sweep: plain
// "choudoufu plan" under a live block (live_mode.go) and "choudoufu
// live-plan -estate" (live_plan.go).
//
// The configuration is step 6's: kubernetes_config_map.reader's name reads
// another resource's data, so the static evaluator refuses it and it is
// absent from the resolutions. The cluster holds reader-hello, carrying
// the estate label and the address annotation kubernetes_config_map.reader.
// With the hookup, the leg binds reader-hello at that address. Without
// it, reader-hello is an undeclared object filed as an orphan, and the
// block the plan must not create a second object for is left with
// nothing.

const refusedHookupConfig = `
provider "kubernetes" {}

resource "kubernetes_config_map" "app" {
  metadata {
    name      = "app-config"
    namespace = "smoke-k8s"
  }
  data = { greeting = "hello" }
}

resource "kubernetes_config_map" "reader" {
  metadata {
    name      = "reader-${kubernetes_config_map.app.data["greeting"]}"
    namespace = "smoke-k8s"
  }
  data = { reads = "app-config" }
}
`

const refusedHookupLiveBlock = `
terraform {
  live {
    estate = "smoke-k8s"
  }
}
`

var refusedHookupLabels = map[string]string{"tofu-estate": "smoke-k8s"}

// refusedHookupCluster is the cluster after step 6's first apply: both
// ConfigMaps, labelled, reader-hello annotated with its block's address.
func refusedHookupCluster() *liveLsStubSweeper {
	return &liveLsStubSweeper{
		kinds: []kubesweep.Kind{heldTestCM},
		objects: map[string][]kubesweep.Object{"ConfigMap": {
			{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "app-config", ImportID: "smoke-k8s/app-config", Labels: refusedHookupLabels, Address: "kubernetes_config_map.app"},
			{Kind: "ConfigMap", Namespace: "smoke-k8s", Name: "reader-hello", ImportID: "smoke-k8s/reader-hello", Labels: refusedHookupLabels, Address: "kubernetes_config_map.reader"},
		}},
	}
}

// refusedHookupStubLeg replaces the label-list leg's builder for the
// test's duration, so the leg lists the stub cluster instead of dialling
// the one the provider block names. Everything else about the pass -
// the request, NodeRefused included - is the command's own.
func refusedHookupStubLeg(t *testing.T, cluster kubesweep.Sweeper) {
	t.Helper()
	orig := sweepLegBuilders[substrate.SweepLabelList]
	sweepLegBuilders[substrate.SweepLabelList] = func(ctx context.Context, p *statelessProviders, sub substrate.Substrate, addr addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
		p.rememberKubernetesSweeper(addr, cluster)
		return discovery.KubernetesSweep{Client: cluster, Types: []string{"kubernetes_config_map"}}, false, nil
	}
	t.Cleanup(func() { sweepLegBuilders[substrate.SweepLabelList] = orig })
}

// refusedHookupK8s is a kubernetes provider that speaks the list protocol
// the stateless list client asks for by assertion, serving no list schema,
// as newK8sCluster's does.
type refusedHookupK8s struct{ *tofu.MockProvider }

func (refusedHookupK8s) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

// refusedHookupProvider is a kubernetes provider whose import reads the
// object the stub cluster holds under the requested id.
func refusedHookupProvider() refusedHookupK8s {
	p := testProvider()
	p.GetProviderSchemaResponse = &providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: nil},
		ResourceTypes: map[string]providers.Schema{"kubernetes_config_map": objectMetaShapeSchema()},
	}
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		ns, name, _ := strings.Cut(req.Target.ID, "/")
		data := map[string]cty.Value{"greeting": cty.StringVal("hello")}
		if name == "reader-hello" {
			data = map[string]cty.Value{"reads": cty.StringVal("app-config")}
		}
		labels := map[string]cty.Value{}
		for k, v := range refusedHookupLabels {
			labels[k] = cty.StringVal(v)
		}
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: req.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"data": cty.MapVal(data),
				"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
					"name":      cty.StringVal(name),
					"namespace": cty.StringVal(ns),
					"labels":    cty.MapVal(labels),
					"uid":       cty.StringVal("uid-" + name),
				})}),
			}),
		}}}
	}
	return refusedHookupK8s{p}
}

func refusedHookupMeta(t *testing.T, p refusedHookupK8s) (Meta, func(*testing.T) *terminal.TestOutput) {
	t.Helper()
	view, done := testView(t)
	return Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("kubernetes"): providers.FactoryFixed(p),
		}},
	}, done
}

func writeRefusedHookupDir(t *testing.T, liveBlock bool) {
	t.Helper()
	td := t.TempDir()
	src := refusedHookupConfig
	if liveBlock {
		src = refusedHookupLiveBlock + src
	}
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(td)
}

// assertRefusedInstanceBound is what both entry points must show: the plan
// is empty, and reader-hello is neither destroyed as an orphan nor imported
// beside that destroy. With the hookup gone the same run plans "1 to
// import, 0 to add, 0 to change, 1 to destroy": reader-hello imported at
// kubernetes_config_map.reader and destroyed at
// kubernetes_config_map.orphan_smoke-k8s_reader-hello, in one plan.
func assertRefusedInstanceBound(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "orphan_smoke-k8s_reader-hello") {
		t.Errorf("reader-hello was filed as an orphan: the refused instance's address never reached the Kubernetes leg's binding\n%s", stdout)
	}
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("the plan is not empty over an object its address annotation binds to a declared block\n%s", stdout)
	}
}

// TestNodeRefusedHookup_plainPlan drives live_mode.go's call: plain
// "choudoufu plan" under a live block.
func TestNodeRefusedHookup_plainPlan(t *testing.T) {
	writeRefusedHookupDir(t, true)
	refusedHookupStubLeg(t, refusedHookupCluster())
	m, done := refusedHookupMeta(t, refusedHookupProvider())

	var captured *statelessRunner
	defer statelessRunnerTestHook(func(r *statelessRunner) { captured = r })()

	c := &PlanCommand{Meta: m}
	code := c.Run([]string{"-no-color"})
	out := done(t)
	if captured == nil {
		t.Fatal("no stateless runner was installed, so this plan did not take the live-block path")
	}
	// The premise: the static evaluator refused the reader, so it reached
	// the node rather than the resolutions. Without this the test would
	// pass for a configuration the evaluator resolves on its own.
	if _, ok := captured.resolver.StaticRefusals["kubernetes_config_map.reader"]; !ok {
		t.Fatalf("kubernetes_config_map.reader was not statically refused; StaticRefusals = %v", captured.resolver.StaticRefusals)
	}
	assertRefusedInstanceBound(t, code, out.Stdout(), out.Stderr())
	if got := captured.resolver.MarkerIndex["kubernetes_config_map.reader"].ID; got != "smoke-k8s/reader-hello" {
		t.Errorf("the node's marker index answers %q for kubernetes_config_map.reader, want smoke-k8s/reader-hello (index: %v)", got, captured.resolver.MarkerIndex)
	}
}

// TestNodeRefusedHookup_livePlanEstate drives live_plan.go's call:
// "choudoufu live-plan -estate" over a configuration with no live block.
func TestNodeRefusedHookup_livePlanEstate(t *testing.T) {
	writeRefusedHookupDir(t, false)
	refusedHookupStubLeg(t, refusedHookupCluster())
	m, done := refusedHookupMeta(t, refusedHookupProvider())

	c := &LivePlanCommand{Meta: m}
	code := c.Run([]string{"-no-color", "-estate=smoke-k8s"})
	out := done(t)
	if !strings.Contains(out.Stdout(), `kubernetes_config_map.app.data["greeting"]`) {
		t.Fatalf("the reader's static refusal is not in the output, so this run does not exercise a refused instance\nstdout:\n%s\nstderr:\n%s", out.Stdout(), out.Stderr())
	}
	assertRefusedInstanceBound(t, code, out.Stdout(), out.Stderr())
}
