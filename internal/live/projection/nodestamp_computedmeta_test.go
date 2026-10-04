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
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// Epic #1885, reference-k8s-workloads' test_plan: a Job whose configuration
// declares no labels, whose live object carries the label the API server
// copied from its pod template. Stock plans nothing; choudoufu planned
// `- "app" = "migrate" -> null` because the stamp turned the null labels of
// an Optional+Computed map into {tofu-estate}.
//
// jobTypeSchema is configMapTypeSchema with metadata.labels
// Optional+Computed, as hashicorp/kubernetes 3.2.1's jobMetadataSchema
// declares it for kubernetes_job_v1 and kubernetes_job.
func jobTypeSchema() providers.Schema {
	s := configMapTypeSchema()
	labels := *s.Block.BlockTypes["metadata"].Block.Attributes["labels"]
	labels.Computed = true
	s.Block.BlockTypes["metadata"].Block.Attributes["labels"] = &labels
	return s
}

func jobAddr() addrs.AbsResourceInstance {
	return addrs.AbsResourceInstance{Resource: addrs.Resource{
		Mode: addrs.ManagedResourceMode, Type: "kubernetes_job_v1", Name: "migrate",
	}.Instance(addrs.NoKey)}
}

func strMap(m map[string]string) cty.Value {
	if m == nil {
		return cty.NullVal(cty.Map(cty.String))
	}
	vals := map[string]cty.Value{}
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

// planConfig runs the node's two configuration hooks in the order
// NodeAbstractResourceInstance.plan runs them, and returns what the
// provider would be proposed.
func planConfig(t *testing.T, n *NodeResolver, schema providers.Schema, evaluated, prior cty.Value) cty.Value {
	t.Helper()
	adjusted, diags := n.AdjustConfigValue(context.Background(), jobAddr(), evaluated, schema)
	if diags.HasErrors() {
		t.Fatalf("AdjustConfigValue: %s", diags.Err())
	}
	if pa, ok := any(n).(tofu.PriorConfigValueAdjuster); ok {
		adjusted = pa.AdjustConfigValueToPrior(context.Background(), jobAddr(), evaluated, adjusted, prior, schema)
	}
	return objchange.ProposedNew(schema.Block, prior, adjusted)
}

func withAnnotations(v cty.Value, ann cty.Value) cty.Value {
	attrs := v.AsValueMap()
	elem := attrs["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
	elem["annotations"] = ann
	attrs["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(elem)})
	return cty.ObjectVal(attrs)
}

// Proving it red: without NodeResolver's AdjustConfigValueToPrior the
// proposed labels are {tofu-estate} alone and app is planned away.
func TestAdjustConfigValueToPrior_KeepsServerSetLabelsOfAComputedMap(t *testing.T) {
	n := &NodeResolver{Estate: "refk8swl"}
	prior := withAnnotations(configMapTestConfig(strMap(map[string]string{"app": "migrate", markers.TagEstate: "refk8swl"})),
		strMap(map[string]string{markers.AddressAnnotation: "kubernetes_job_v1.migrate"}))
	got := planConfig(t, n, jobTypeSchema(), configMapTestConfig(cty.NullVal(cty.Map(cty.String))), prior)
	labels := requireLabels(t, got)
	if len(labels) != 2 || labels["app"] != "migrate" || labels[markers.TagEstate] != "refk8swl" {
		t.Fatalf("proposed labels = %v, want app=migrate and %s=refk8swl, as the live object holds them", labels, markers.TagEstate)
	}
	if !got.RawEquals(prior) {
		t.Errorf("proposed object differs from the prior, so the plan is not empty:\n got %#v\nwant %#v", got, prior)
	}
}

// The marker itself is never carried from the prior: under an untag of
// tofu-estate the prior's label is released, and a declared map is left
// as stamped, the way stock plans it.
func TestAdjustConfigValueToPrior_NeverCarriesAMarkerOrOverridesADeclaredMap(t *testing.T) {
	prior := configMapTestConfig(strMap(map[string]string{"app": "migrate", markers.TagEstate: "refk8swl"}))

	n := &NodeResolver{Estate: "refk8swl", PolicyUntag: map[string]string{jobAddr().String(): markers.TagEstate}}
	adjusted, _ := n.AdjustConfigValue(context.Background(), jobAddr(), configMapTestConfig(strMap(map[string]string{"team": "x"})), jobTypeSchema())
	got := n.AdjustConfigValueToPrior(context.Background(), jobAddr(), configMapTestConfig(strMap(map[string]string{"team": "x"})), adjusted, prior, jobTypeSchema())
	if l := requireLabels(t, got); len(l) != 1 || l["team"] != "x" {
		t.Errorf("declared labels under an untag = %v, want team=x alone", l)
	}

	n = &NodeResolver{Estate: "refk8swl"}
	adjusted, _ = n.AdjustConfigValue(context.Background(), jobAddr(), configMapTestConfig(cty.NullVal(cty.Map(cty.String))), configMapTypeSchema())
	got = n.AdjustConfigValueToPrior(context.Background(), jobAddr(), configMapTestConfig(cty.NullVal(cty.Map(cty.String))), adjusted, prior, configMapTypeSchema())
	if l := requireLabels(t, got); len(l) != 1 || l[markers.TagEstate] != "refk8swl" {
		t.Errorf("labels on a type whose labels are not Computed = %v, want the stamp alone", l)
	}
}
