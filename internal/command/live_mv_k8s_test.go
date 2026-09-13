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
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// live-mv on a Kubernetes object (GitHub issue #1081's fifth item; the
// smoke that proves it on a kind cluster is claim 23,
// live/smoke/scenarios/k8s-the-label-is-the-boundary.sh). The marker is one
// label and carries no address, so the command's two halves come apart: a
// rename within one estate reports nothing to write and exits 0, and
// -from-estate is the one governed label write, through the provider.

// k8sCluster is one live ConfigMap behind a mock hashicorp/kubernetes
// provider, counting reads and writes so a test can assert on both.
type k8sCluster struct {
	*tofu.MockProvider
	object  cty.Value
	reads   int
	applies int
}

func k8sConfigMapSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"metadata": {
				Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"name":             {Type: cty.String, Optional: true, Computed: true},
					"namespace":        {Type: cty.String, Optional: true},
					"labels":           {Type: cty.Map(cty.String), Optional: true},
					"annotations":      {Type: cty.Map(cty.String), Optional: true},
					"uid":              {Type: cty.String, Computed: true},
					"resource_version": {Type: cty.String, Computed: true},
					"generation":       {Type: cty.Number, Computed: true},
				}},
			},
		},
	}}
}

func k8sConfigMapObject(labels map[string]string) cty.Value {
	labelVal := cty.MapValEmpty(cty.String)
	if len(labels) > 0 {
		vals := make(map[string]cty.Value, len(labels))
		for k, v := range labels {
			vals[k] = cty.StringVal(v)
		}
		labelVal = cty.MapVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("boundary/database"),
		"data": cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("database")}),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("database"),
			"namespace":        cty.StringVal("boundary"),
			"labels":           labelVal,
			"annotations":      cty.NullVal(cty.Map(cty.String)),
			"uid":              cty.StringVal("6bc3dcc0"),
			"resource_version": cty.StringVal("575"),
			"generation":       cty.NumberIntVal(1),
		})}),
	})
}

func newK8sCluster(labels map[string]string) *k8sCluster {
	c := &k8sCluster{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"kubernetes_config_map": k8sConfigMapSchema()},
		},
	}, object: k8sConfigMapObject(labels)}
	c.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		c.reads++
		if r.Target.ID != "boundary/database" {
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: c.object}},
		}
	}
	c.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		c.reads++
		return providers.ReadResourceResponse{NewState: c.object}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applies++
		c.object = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

// ListResourceStream is the list protocol the stateless list client asks
// for by assertion; the provider serves no list schema, so it is never
// called.
func (*k8sCluster) ListResourceStream(context.Context, providers.ListResourceRequest, func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	return nil
}

func (c *k8sCluster) labels(t *testing.T) map[string]string {
	t.Helper()
	got, ok := markers.LabelsOf(c.object)
	if !ok {
		t.Fatal("the live object has no readable labels")
	}
	return got
}

// k8sEstateConfig writes a one-block configuration for estate data under
// the given block name, into a fresh working directory.
func k8sEstateConfig(t *testing.T, blockName string) {
	t.Helper()
	td := t.TempDir()
	body := `terraform {
  live {
    estate = "data"
  }

  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
    }
  }
}

provider "kubernetes" {}

resource "kubernetes_config_map" "` + blockName + `" {
  metadata {
    name      = "database"
    namespace = "boundary"
  }
  data = { greeting = "database" }
}
`
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(td)
}

func newK8sLiveMvCommand(t *testing.T, cluster *k8sCluster) (*LiveMvCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()
	view, done := testView(t)
	c := &LiveMvCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{
				Providers: map[addrs.Provider]providers.Factory{
					addrs.NewDefaultProvider("kubernetes"): providers.FactoryFixed(cluster),
				},
			},
		},
	}
	return c, done
}

// TestLiveMv_k8sMovesBetweenEstates is the carve, on Kubernetes: the live
// ConfigMap carries tofu-estate=app, this configuration is estate data and
// declares the same address, and -from-estate=app rewrites the one label.
func TestLiveMv_k8sMovesBetweenEstates(t *testing.T) {
	k8sEstateConfig(t, "database")
	cluster := newK8sCluster(map[string]string{"app": "web", markers.TagEstate: "app"})

	c, done := newK8sLiveMvCommand(t, cluster)
	code := c.Run([]string{"-no-color", "-from-estate=app", "kubernetes_config_map.database", "kubernetes_config_map.database"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	got := cluster.labels(t)
	if got[markers.TagEstate] != "data" || got["app"] != "web" || len(got) != 2 {
		t.Errorf("live labels = %v, want app=web plus tofu-estate=data and nothing else", got)
	}
	if cluster.applies != 1 {
		t.Errorf("applied %d times, want 1", cluster.applies)
	}
	report := output.Stdout()
	for _, want := range []string{
		"Relabelled one live object into this estate. This was a cluster write.",
		`"app" -> "data"`,
		"boundary/database",
		"admission policy",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "tofu-address") {
		t.Errorf("the report names a tofu-address the object never carried:\n%s", report)
	}
}

// TestLiveMv_k8sRenameHasNothingToWrite: the block was renamed, the object
// is bound by its namespace and name, and live-mv says so and exits 0
// without reading or writing anything.
func TestLiveMv_k8sRenameHasNothingToWrite(t *testing.T) {
	k8sEstateConfig(t, "database_renamed")
	cluster := newK8sCluster(map[string]string{markers.TagEstate: "data"})

	c, done := newK8sLiveMvCommand(t, cluster)
	code := c.Run([]string{"-no-color", "kubernetes_config_map.database", "kubernetes_config_map.database_renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if cluster.reads != 0 || cluster.applies != 0 {
		t.Errorf("a rename with nothing to write reached the cluster: %d reads, %d applies", cluster.reads, cluster.applies)
	}
	report := output.Stdout()
	for _, want := range []string{
		"Nothing to write",
		"carries no address",
		"record store",
		"-from-estate",
		"kubernetes_config_map.database_renamed",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}
	if output.Stderr() != "" {
		t.Errorf("a successful nothing-to-write rename wrote to stderr:\n%s", output.Stderr())
	}
}

// TestLiveMv_k8sJSON: the -json document names the surface, carries no
// escaped markers for an object that has no address, and reports a rename
// with nothing to write as a success with no write in it.
func TestLiveMv_k8sJSON(t *testing.T) {
	k8sEstateConfig(t, "database")
	cluster := newK8sCluster(map[string]string{markers.TagEstate: "app"})

	c, done := newK8sLiveMvCommand(t, cluster)
	code := c.Run([]string{"-no-color", "-json", "-from-estate=app", "kubernetes_config_map.database", "kubernetes_config_map.database"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	rep := decodeMvJSON(t, output.Stdout())
	if rep.MarkerSurface != "label" || !rep.Written || !rep.Verified || rep.NothingToWrite {
		t.Errorf("move document: marker_surface=%q written=%v verified=%v nothing_to_write=%v", rep.MarkerSurface, rep.Written, rep.Verified, rep.NothingToWrite)
	}
	if rep.From.Marker != "" || rep.To.Marker != "" {
		t.Errorf("the document claims escaped markers on an object with no address: from %q, to %q", rep.From.Marker, rep.To.Marker)
	}
	if rep.From.Estate != "app" || rep.To.Estate != "data" {
		t.Errorf("estates: from %q to %q, want app to data", rep.From.Estate, rep.To.Estate)
	}

	k8sEstateConfig(t, "database_renamed")
	c2, done2 := newK8sLiveMvCommand(t, cluster)
	code = c2.Run([]string{"-no-color", "-json", "kubernetes_config_map.database", "kubernetes_config_map.database_renamed"})
	output = done2(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	rep = decodeMvJSON(t, output.Stdout())
	if !rep.NothingToWrite || rep.Written || rep.Refusal != nil || rep.MarkerSurface != "label" {
		t.Errorf("rename document: nothing_to_write=%v written=%v refusal=%v marker_surface=%q", rep.NothingToWrite, rep.Written, rep.Refusal, rep.MarkerSurface)
	}
}
