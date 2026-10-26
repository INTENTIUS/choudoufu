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
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// INTENTIUS/choudoufu#2267: live-plan -resource end to end for each cone
// leg (#2260). internal/live/discovery's conelegs_test.go drives each leg
// alone; these drive the command, with a fake provider for the family and
// the family's sweep leg built over a fake cluster, Cloud Asset Inventory
// or Resource Graph client, so that what is checked is the command's own
// wiring: which instances make the cone, which leg is built for them, that
// the leg runs narrowed (Cone set) and never as the estate sweep, and that
// the instance it finds reads as observed in the drift record.

// coneRecord is the last drift record for addr, failing the test when the
// stream has none.
func coneRecord(t *testing.T, stdout, addr string) (map[string]any, map[string]any) {
	t.Helper()
	_, records, settle := driftLines(t, stdout)
	for i := len(records) - 1; i >= 0; i-- {
		if records[i]["address"] == addr {
			return records[i], settle
		}
	}
	t.Fatalf("no drift record for %s in:\n%s", addr, stdout)
	return nil, nil
}

// assertConeObserved is what every family's test asks of the record: the
// instance was bound and read (in-sync, or drifted where the fake's plan
// differs), and the run made no sweep call.
func assertConeObserved(t *testing.T, stdout, addr string) {
	t.Helper()
	rec, settle := coneRecord(t, stdout, addr)
	if status := rec["status"]; status != "in-sync" && status != "drifted" {
		t.Errorf("%s status %v, want in-sync or drifted: the cone leg should have found its object (record %v)", addr, status, rec)
	}
	reads, _ := settle["reads"].(map[string]any)
	if reads["sweepCalls"] != float64(0) {
		t.Errorf("drift-settle sweepCalls = %v, want 0: a cone leg's listings are scans", reads["sweepCalls"])
	}
}

// coneLegKubernetes replaces the label-list leg's builder with one over
// cluster, recording the leg the command built.
func coneLegKubernetes(t *testing.T, cluster kubesweep.Sweeper, types []string) *[]discovery.KubernetesSweep {
	t.Helper()
	var built []discovery.KubernetesSweep
	var mu sync.Mutex
	orig := sweepLegBuilders[substrate.SweepLabelList]
	sweepLegBuilders[substrate.SweepLabelList] = func(ctx context.Context, p *projectionProviders, sub substrate.Substrate, addr addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
		p.rememberKubernetesSweeper(addr, cluster)
		leg := discovery.KubernetesSweep{Client: cluster, Types: types}
		mu.Lock()
		built = append(built, leg)
		mu.Unlock()
		return leg, false, nil
	}
	t.Cleanup(func() { sweepLegBuilders[substrate.SweepLabelList] = orig })
	return &built
}

// TestLivePlanResource_kubernetesConeLegBindsARefusedInstance: the
// Kubernetes cone leg end to end. kubernetes_config_map.reader's name reads
// another resource, so the static evaluator refuses it; the object the
// cluster holds for it carries its address annotation. Before #2267 a
// Kubernetes instance never joined -resource's cone (no Kubernetes instance
// needs discovery), so the leg was never built and the reader planned a
// create. Now the refused instance makes the cone, the label-list leg runs
// for its type, binds reader-hello by its annotation, and the record reads
// it as observed.
func TestLivePlanResource_kubernetesConeLegBindsARefusedInstance(t *testing.T) {
	writeRefusedHookupDir(t, true)
	cluster := refusedHookupCluster()
	built := coneLegKubernetes(t, cluster, []string{"kubernetes_config_map"})
	m, done := refusedHookupMeta(t, refusedHookupProvider())

	c := &LivePlanCommand{Meta: m}
	code := c.Run([]string{"-no-color", "-resource=kubernetes_config_map.reader"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	if len(*built) == 0 {
		t.Fatalf("no Kubernetes leg was built for the cone\nstdout:\n%s\nstderr:\n%s", out.Stdout(), out.Stderr())
	}
	assertConeObserved(t, out.Stdout(), "kubernetes_config_map.reader")
	if strings.Contains(out.Stdout()+out.Stderr(), "orphan_smoke-k8s_reader-hello") {
		t.Errorf("reader-hello was filed as an orphan; the cone files none\n%s", out.Stdout())
	}
}

// coneGCPSearcher is a Cloud Asset client that answers address searches
// and counts the estate-wide ones.
type coneGCPSearcher struct {
	assets []gcpsweep.Asset

	mu        sync.Mutex
	searches  int
	addresses []string
}

func (f *coneGCPSearcher) Search(context.Context, string, string) ([]gcpsweep.Asset, error) {
	f.mu.Lock()
	f.searches++
	f.mu.Unlock()
	return f.assets, nil
}

func (f *coneGCPSearcher) SearchAddress(_ context.Context, _, estate, address string) ([]gcpsweep.Asset, error) {
	f.mu.Lock()
	f.addresses = append(f.addresses, address)
	f.mu.Unlock()
	var out []gcpsweep.Asset
	for _, a := range f.assets {
		if a.Labels[markers.TagEstate] == estate && a.Labels[markers.TagAddress] == address {
			out = append(out, a)
		}
	}
	return out, nil
}

const coneGCPEstate = "cone-gcp"

const coneGCPRoot = `
terraform {
  live {
    estate = "` + coneGCPEstate + `"
  }
}

provider "google" {
  project = "proj-a"
  region  = "us-central1"
}

resource "google_vertex_ai_index_endpoint" "site" {
  display_name = "site"
  region       = "us-central1"
}
`

// TestLivePlanResource_gcpConeLegSearchesByAddress: the GCP cone leg end to
// end. google_vertex_ai_index_endpoint is server-assigned, so the instance
// needs discovery; the leg is built with Cone set, searches Cloud Asset
// Inventory for the instance's own tofu-address label and never the estate,
// and the endpoint it finds is imported and read as observed.
func TestLivePlanResource_gcpConeLegSearchesByAddress(t *testing.T) {
	td := t.TempDir()
	t.Chdir(td)
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(coneGCPRoot), 0o600); err != nil {
		t.Fatal(err)
	}
	const endpointID = "projects/proj-a/locations/us-central1/indexEndpoints/site-key"
	addr := "google_vertex_ai_index_endpoint.site"
	instance, diags := addrs.ParseAbsResourceInstanceStr(addr)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	labelVals := map[string]cty.Value{
		markers.TagEstate:  cty.StringVal(coneGCPEstate),
		markers.TagAddress: cty.StringVal(markers.GoogleAddressValue(instance)),
	}
	searcher := &coneGCPSearcher{assets: []gcpsweep.Asset{{
		AssetType: "aiplatform.googleapis.com/IndexEndpoint",
		Name:      "//aiplatform.googleapis.com/" + endpointID,
		Labels:    map[string]string{markers.TagEstate: coneGCPEstate, markers.TagAddress: markers.GoogleAddressValue(instance)},
	}}}

	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                     {Type: cty.String, Computed: true},
		"name":                   {Type: cty.String, Computed: true},
		"display_name":           {Type: cty.String, Required: true},
		"region":                 {Type: cty.String, Optional: true, Computed: true},
		"project":                {Type: cty.String, Optional: true, Computed: true},
		markers.GoogleLabelsAttr: {Type: cty.Map(cty.String), Optional: true},
		"effective_labels":       {Type: cty.Map(cty.String), Computed: true},
		"terraform_labels":       {Type: cty.Map(cty.String), Computed: true},
	}}}
	cloud := &recBoundARM{objects: map[string]cty.Value{endpointID: cty.ObjectVal(map[string]cty.Value{
		"id":                     cty.StringVal(endpointID),
		"name":                   cty.StringVal("site-key"),
		"display_name":           cty.StringVal("site"),
		"region":                 cty.StringVal("us-central1"),
		"project":                cty.StringVal("proj-a"),
		markers.GoogleLabelsAttr: cty.MapVal(labelVals),
		"effective_labels":       cty.MapVal(labelVals),
		"terraform_labels":       cty.MapVal(labelVals),
	})}}
	providerBlock := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"project": {Type: cty.String, Optional: true},
		"region":  {Type: cty.String, Optional: true},
	}}

	var built []discovery.GCPAssetSweep
	var mu sync.Mutex
	orig := sweepLegBuilders[substrate.SweepAssetSearch]
	sweepLegBuilders[substrate.SweepAssetSearch] = func(ctx context.Context, p *projectionProviders, sub substrate.Substrate, pa addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
		leg := discovery.GCPAssetSweep{Searcher: searcher, Project: "proj-a"}
		if p.mgr != nil {
			if s, d := p.mgr.GetProviderSchema(ctx, pa.Provider); !d.HasErrors() {
				leg.Labelled, leg.Unlabelled = googleLabelSplit(s)
			}
		}
		mu.Lock()
		built = append(built, leg)
		mu.Unlock()
		return leg, false, nil
	}
	t.Cleanup(func() { sweepLegBuilders[substrate.SweepAssetSearch] = orig })

	view, done := testView(t)
	m := Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("google"): func() (providers.Interface, error) {
				return pairLister{cloud.provider("google_vertex_ai_index_endpoint", schema, providerBlock)}, nil
			},
		}},
	}
	code := (&LivePlanCommand{Meta: m}).Run([]string{"-no-color", "-resource=" + addr})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	if len(built) == 0 {
		t.Fatalf("no GCP leg was built for the cone\nstdout:\n%s\nstderr:\n%s", out.Stdout(), out.Stderr())
	}
	searcher.mu.Lock()
	searches, addresses := searcher.searches, append([]string(nil), searcher.addresses...)
	searcher.mu.Unlock()
	if searches != 0 {
		t.Errorf("the cone leg ran %d estate-wide search(es); it searches by address only", searches)
	}
	if len(addresses) == 0 || addresses[0] != addr {
		t.Errorf("address searches %v, want one for %s", addresses, addr)
	}
	assertConeObserved(t, out.Stdout(), addr)
}

// coneAzureGraph is a Resource Graph client that answers the type-narrowed
// query and counts the estate-wide one.
type coneAzureGraph struct {
	resources []azsweep.Resource

	mu       sync.Mutex
	searches int
	types    [][]string
}

func (g *coneAzureGraph) Search(context.Context, string, string) ([]azsweep.Resource, error) {
	g.mu.Lock()
	g.searches++
	g.mu.Unlock()
	return g.resources, nil
}

func (g *coneAzureGraph) SearchTypes(_ context.Context, _, _ string, armTypes []string) ([]azsweep.Resource, error) {
	g.mu.Lock()
	g.types = append(g.types, append([]string(nil), armTypes...))
	g.mu.Unlock()
	return g.resources, nil
}

const (
	coneAzureEstate = "cone-az"
	coneAzureSub    = "00000000-0000-0000-0000-000000000002"
	coneAzureCertID = "/subscriptions/" + coneAzureSub + "/resourceGroups/cone-rg/providers/Microsoft.Web/certificates/cert-minted"
)

const coneAzureRoot = `
terraform {
  live {
    estate = "` + coneAzureEstate + `"
  }
}

provider "azurerm" {
  subscription_id = "` + coneAzureSub + `"
  features {
  }
}

resource "azurerm_app_service_managed_certificate" "cert" {
  custom_hostname_binding_id = "/subscriptions/` + coneAzureSub + `/resourceGroups/cone-rg/providers/Microsoft.Web/sites/app/hostNameBindings/www.example.com"
}
`

// TestLivePlanResource_azureConeLegNarrowsByType: the Azure cone leg end to
// end. azurerm_app_service_managed_certificate's id is minted by the
// service and it carries tags, so the instance needs discovery; the leg is
// built with Cone set, sends the Resource Graph query narrowed to the
// type's ARM type and never the estate query, and the certificate it finds
// by its tofu-address tag is imported and read as observed.
func TestLivePlanResource_azureConeLegNarrowsByType(t *testing.T) {
	for _, name := range azsweep.EnvNames() {
		t.Setenv(name, "")
	}
	t.Setenv("ARM_USE_CLI", "false")
	td := t.TempDir()
	t.Chdir(td)
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(coneAzureRoot), 0o600); err != nil {
		t.Fatal(err)
	}
	addr := "azurerm_app_service_managed_certificate.cert"
	tags := map[string]string{markers.TagEstate: coneAzureEstate, markers.TagAddress: markers.EscapeAddress(addr)}
	graph := &coneAzureGraph{resources: []azsweep.Resource{{
		ID: strings.ToLower(coneAzureCertID), Type: "microsoft.web/certificates", Name: "cert-minted",
		ResourceGroup: "cone-rg", SubscriptionID: coneAzureSub, Tags: tags,
	}}}

	schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                         {Type: cty.String, Computed: true},
		"custom_hostname_binding_id": {Type: cty.String, Required: true},
		"tags":                       {Type: cty.Map(cty.String), Optional: true},
	}}}
	cloud := &recBoundARM{objects: map[string]cty.Value{coneAzureCertID: cty.ObjectVal(map[string]cty.Value{
		"id":                         cty.StringVal(coneAzureCertID),
		"custom_hostname_binding_id": cty.StringVal("/subscriptions/" + coneAzureSub + "/resourceGroups/cone-rg/providers/Microsoft.Web/sites/app/hostNameBindings/www.example.com"),
		"tags":                       recBoundTagsFor(coneAzureEstate, addr),
	})}}
	providerBlock := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"subscription_id": {Type: cty.String, Optional: true}},
		BlockTypes: map[string]*configschema.NestedBlock{"features": {Nesting: configschema.NestingList, Block: configschema.Block{}}},
	}

	var built []discovery.ResourceGraphSweep
	var mu sync.Mutex
	orig := sweepLegBuilders[substrate.SweepResourceGraph]
	sweepLegBuilders[substrate.SweepResourceGraph] = func(ctx context.Context, p *projectionProviders, sub substrate.Substrate, pa addrs.AbsProviderConfig) (discovery.Sweeper, bool, tfdiags.Diagnostics) {
		leg := discovery.ResourceGraphSweep{Coverage: azsweep.Coverage{coneAzureSub: graph}, Subscriptions: []string{coneAzureSub}}
		mu.Lock()
		built = append(built, leg)
		mu.Unlock()
		return leg, false, nil
	}
	t.Cleanup(func() { sweepLegBuilders[substrate.SweepResourceGraph] = orig })

	view, done := testView(t)
	m := Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{Providers: map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("azurerm"): func() (providers.Interface, error) {
				return pairLister{cloud.provider("azurerm_app_service_managed_certificate", schema, providerBlock)}, nil
			},
		}},
	}
	code := (&LivePlanCommand{Meta: m}).Run([]string{"-no-color", "-resource=" + addr})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	if len(built) == 0 {
		t.Fatalf("no Azure leg was built for the cone\nstdout:\n%s\nstderr:\n%s", out.Stdout(), out.Stderr())
	}
	graph.mu.Lock()
	searches, types := graph.searches, append([][]string(nil), graph.types...)
	graph.mu.Unlock()
	if searches != 0 {
		t.Errorf("the cone leg sent the estate query %d time(s)", searches)
	}
	if len(types) != 1 || len(types[0]) != 1 || types[0][0] != "microsoft.web/certificates" {
		t.Errorf("type-narrowed queries %v, want one, for microsoft.web/certificates", types)
	}
	assertConeObserved(t, out.Stdout(), addr)
}

// recBoundTagsFor is recBoundTags for another estate.
func recBoundTagsFor(estate, addr string) cty.Value {
	return cty.MapVal(map[string]cty.Value{
		markers.TagEstate:  cty.StringVal(estate),
		markers.TagAddress: cty.StringVal(markers.EscapeAddress(addr)),
	})
}
