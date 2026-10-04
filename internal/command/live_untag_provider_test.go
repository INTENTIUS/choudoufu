// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// untagProviderEstate is the estate these fixtures label their orphans
// with, the value a release checks the label against (GitHub issue #1743).
const untagProviderEstate = "prod"

// GitHub issue #1657: undeclared_tagged = "untag" releases each target
// through the provider configuration whose sweep found it. These drive the
// runner the way PriorState and AfterApply do around a real apply: the
// sweep's passes are merged by discovery.Merge, the untag work is captured,
// and AfterApply releases it against a fake cloud where each provider
// configuration sees only its own region or cluster.

// untagCloud is every object the fake providers can reach, keyed by the
// scope a configured provider sees (its region, or "cluster") and then the
// object's import id.
type untagCloud struct {
	mu      sync.Mutex
	objects map[string]map[string]cty.Value
}

func (c *untagCloud) get(scope, id string) (cty.Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.objects[scope][id]
	return v, ok
}

func (c *untagCloud) put(scope, id string, v cty.Value) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.objects[scope][id] = v
}

func untagTaggedSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}
}

func untagLabelledSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"data": {Type: cty.Map(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{"metadata": {
			Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1,
			Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":      {Type: cty.String, Optional: true},
				"namespace": {Type: cty.String, Optional: true},
				"labels":    {Type: cty.Map(cty.String), Optional: true},
			}},
		}},
	}}
}

func untagStrMap(m map[string]string) cty.Value {
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

func untagQueue(id string, tags map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal(id), "name": cty.StringVal(id), "tags": untagStrMap(tags),
	})
}

func untagConfigMap(id string, labels map[string]string) cty.Value {
	ns, name, _ := strings.Cut(id, "/")
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(id),
		"data": cty.MapValEmpty(cty.String),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal(name), "namespace": cty.StringVal(ns), "labels": untagStrMap(labels),
		})}),
	})
}

// untagFakeProvider is one provider instance: it learns its scope when it
// is configured, and reads and writes only that scope of the cloud.
func untagFakeProvider(cloud *untagCloud, schema providers.GetProviderSchemaResponse, scopeOf func(cty.Value) string) providers.Factory {
	return func() (providers.Interface, error) {
		var scope string
		p := &tofu.MockProvider{GetProviderSchemaResponse: &schema}
		p.ConfigureProviderFn = func(req providers.ConfigureProviderRequest) providers.ConfigureProviderResponse {
			scope = scopeOf(req.Config)
			return providers.ConfigureProviderResponse{}
		}
		p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
			id := req.Target.ID
			if v, ok := cloud.get(scope, id); ok {
				resp.ImportedResources = []providers.ImportedResource{{TypeName: req.TypeName, State: v}}
			}
			return resp
		}
		p.ReadResourceFn = func(req providers.ReadResourceRequest) (resp providers.ReadResourceResponse) {
			v, ok := cloud.get(scope, req.PriorState.GetAttr("id").AsString())
			if !ok {
				resp.NewState = cty.NullVal(req.PriorState.Type())
				return resp
			}
			resp.NewState = v
			return resp
		}
		p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
			return providers.PlanResourceChangeResponse{PlannedState: req.ProposedNewState}
		}
		p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
			cloud.put(scope, req.PlannedState.GetAttr("id").AsString(), req.PlannedState)
			return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
		}
		return p, nil
	}
}

type untagRecordingView struct {
	progressRecordingView
	reports []views.LivePolicyReport
}

func (v *untagRecordingView) Policy(r views.LivePolicyReport) { v.reports = append(v.reports, r) }

var (
	untagAWS  = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	untagWest = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws"), Alias: "west"}
	untagKube = addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")}
)

func untagOrphan(typeName, importID string) discovery.OwnedResource {
	return discovery.OwnedResource{
		TypeName:   typeName,
		ImportID:   importID,
		Marker:     typeName + ".gone",
		Normalized: typeName + ".gone",
		Withheld:   "undeclared_tagged = \"untag\"",
		PolicyVerb: policy.Untag,
	}
}

// untagRun merges the passes the way liveDiscover does, captures the
// untag work the way PriorState does, and runs AfterApply. Before #1657 the
// capture also took liveDiscover's "primary" provider configuration
// (the first in address order, untagAWS in both tests below) and released
// every target through it.
func untagRun(t *testing.T, cloud *untagCloud, passes []discovery.Pass) (*untagRecordingView, []string) {
	t.Helper()
	config := liveLsLoadConfig(t, `
provider "aws" {
  region = "us-east-1"
}
provider "aws" {
  alias  = "west"
  region = "us-west-2"
}
provider "kubernetes" {}
`)
	awsSchema := providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{"region": {Type: cty.String, Optional: true}}}},
		ResourceTypes: map[string]providers.Schema{"aws_sqs_queue": untagTaggedSchema()},
	}
	kubeSchema := providers.GetProviderSchemaResponse{
		Provider:      providers.Schema{Block: &configschema.Block{}},
		ResourceTypes: map[string]providers.Schema{"kubernetes_config_map_v1": untagLabelledSchema()},
	}
	lib := plugins.NewLibrary(plugins.ProviderFactories{
		untagAWS.Provider:  untagFakeProvider(cloud, awsSchema, func(v cty.Value) string { return v.GetAttr("region").AsString() }),
		untagKube.Provider: untagFakeProvider(cloud, kubeSchema, func(cty.Value) string { return "cluster" }),
	}, nil)

	merged, _, diags := discovery.Merge("prod", passes, false)
	if diags.HasErrors() {
		t.Fatalf("Merge: %v", diags.Err())
	}

	view := &untagRecordingView{}
	r := &liveRunner{lib: lib, view: view}
	r.captureUntag(liveUntagTargets(merged), markers.TagEstate, untagProviderEstate, config)
	applyDiags := r.AfterApply(context.Background())

	var errs []string
	for _, d := range applyDiags {
		errs = append(errs, d.Description().Summary+": "+d.Description().Detail)
	}
	return view, errs
}

func untagReleased(view *untagRecordingView) []string {
	var out []string
	for _, rep := range view.reports {
		for _, r := range rep.Released {
			out = append(out, r.TypeName+" "+r.LiveID+" ok="+map[bool]string{true: "true", false: "false"}[r.OK]+" "+r.Detail)
		}
	}
	sort.Strings(out)
	return out
}

// TestUntagReleasesThroughTheProviderThatFoundIt_twoRegions: an orphan in
// us-west-2, found by the aws.west pass, must lose its tag. Released
// through the primary (us-east-1) instead, the import finds nothing and the
// release is reported done on an object that still carries the tag.
func TestUntagReleasesThroughTheProviderThatFoundIt_twoRegions(t *testing.T) {
	cloud := &untagCloud{objects: map[string]map[string]cty.Value{
		"us-east-1": {"east-q": untagQueue("east-q", map[string]string{"tofu-estate": "prod", "team": "a"})},
		"us-west-2": {"west-q": untagQueue("west-q", map[string]string{"tofu-estate": "prod", "team": "b"})},
		"cluster":   {},
	}}
	passes := []discovery.Pass{
		{Provider: untagAWS, Region: "us-east-1", Result: &discovery.Result{Estate: "prod", Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{untagOrphan("aws_sqs_queue", "east-q")}}}},
		{Provider: untagWest, Region: "us-west-2", Result: &discovery.Result{Estate: "prod", Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{untagOrphan("aws_sqs_queue", "west-q")}}}},
	}
	view, errs := untagRun(t, cloud, passes)
	if len(errs) != 0 {
		t.Errorf("AfterApply diagnostics: %v", errs)
	}
	for scope, id := range map[string]string{"us-east-1": "east-q", "us-west-2": "west-q"} {
		v, _ := cloud.get(scope, id)
		if tags := v.GetAttr("tags").AsValueMap(); tags["tofu-estate"] != cty.NilVal {
			t.Errorf("%s/%s still carries tofu-estate after the release; released: %v", scope, id, untagReleased(view))
		}
	}
}

// TestUntagReleasesThroughTheProviderThatFoundIt_awsAndKubernetes: a
// Kubernetes orphan in an estate whose primary is the AWS provider. Released
// through the AWS provider, it fails on a schema that has no Kubernetes type.
func TestUntagReleasesThroughTheProviderThatFoundIt_awsAndKubernetes(t *testing.T) {
	cloud := &untagCloud{objects: map[string]map[string]cty.Value{
		"us-east-1": {},
		"us-west-2": {},
		"cluster":   {"orphans/stale": untagConfigMap("orphans/stale", map[string]string{"tofu-estate": "prod", "app": "x"})},
	}}
	passes := []discovery.Pass{
		{Provider: untagAWS, Region: "us-east-1", Result: &discovery.Result{Estate: "prod"}},
		{Provider: untagKube, Result: &discovery.Result{Estate: "prod", Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{untagOrphan("kubernetes_config_map_v1", "orphans/stale")}}}},
	}
	view, errs := untagRun(t, cloud, passes)
	if len(errs) != 0 {
		t.Errorf("AfterApply diagnostics: %v", errs)
	}
	v, _ := cloud.get("cluster", "orphans/stale")
	labels := v.GetAttr("metadata").Index(cty.NumberIntVal(0)).GetAttr("labels").AsValueMap()
	if labels["tofu-estate"] != cty.NilVal {
		t.Errorf("the ConfigMap still carries tofu-estate after the release; released: %v", untagReleased(view))
	}
	if labels["app"] == cty.NilVal {
		t.Errorf("the release removed a label it did not own: %v", labels)
	}
}

// TestUntagRefusesAnUnattributedTarget: a target no caller attributed to a
// provider configuration is refused with an error, and nothing is released
// through a guessed one.
func TestUntagRefusesAnUnattributedTarget(t *testing.T) {
	cloud := &untagCloud{objects: map[string]map[string]cty.Value{
		"us-east-1": {"east-q": untagQueue("east-q", map[string]string{"tofu-estate": "prod"})},
	}}
	disco := &discovery.Result{Estate: "prod", Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{untagOrphan("aws_sqs_queue", "east-q")}}}
	view := &untagRecordingView{}
	r := &liveRunner{lib: plugins.NewLibrary(plugins.ProviderFactories{}, nil), view: view}
	r.captureUntag(liveUntagTargets(disco), markers.TagEstate, untagProviderEstate, liveLsLoadConfig(t, `provider "aws" {}`))
	diags := r.AfterApply(context.Background())
	if !diags.HasErrors() || !strings.Contains(diags.Err().Error(), "aws_sqs_queue east-q") {
		t.Fatalf("diagnostics = %v, want an error naming the unattributed target", diags.Err())
	}
	if len(view.reports) != 0 {
		t.Errorf("reported %v, want no release report: nothing was released", view.reports)
	}
	if v, _ := cloud.get("us-east-1", "east-q"); v.GetAttr("tags").AsValueMap()["tofu-estate"] == cty.NilVal {
		t.Error("the tag was removed through a provider nobody attributed the target to")
	}
}

// GitHub issue #1743: the value a release checks the label against is the
// policy's tag_value, which defaults to the estate name.
func TestLivePolicyTagValueIsTheEstatesValue(t *testing.T) {
	if got := livePolicyTagValue(policy.Build(nil, "prod")); got != "prod" {
		t.Errorf("default tag value = %q, want the estate name %q", got, "prod")
	}
	custom := policy.Build(&policy.Raw{TagKey: "keep-me", TagKeySet: true, TagValue: "yes", TagValueSet: true}, "prod")
	if got := livePolicyTagValue(custom); got != "yes" {
		t.Errorf("tag value = %q, want the configured %q", got, "yes")
	}
	if got := livePolicyTagValue(nil); got != "" {
		t.Errorf("no policy gave tag value %q", got)
	}
}
