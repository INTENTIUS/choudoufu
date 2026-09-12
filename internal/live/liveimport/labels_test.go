// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The label carrier (GitHub issue #1073): a hashicorp/kubernetes type
// carries its marker in metadata[0].labels, and live-import stamps it
// there the way it stamps tags on an AWS type - one label, tofu-estate,
// no address. Before labels.go existed every such type was UNTAGGABLE.
//
// Proving it red: make ratifyOne's labelled always false and
// TestRatify_LabelSurfaceTypeIsStamped reports UNTAGGABLE with nothing
// written; make changedOutsideLabels skip the whole metadata block and
// TestApproveLabel_RefusesAPlanThatChangesMoreThanLabels applies a rename.

// configMapSchema is kubernetes_config_map narrowed to what the label
// surface needs: a metadata block of exactly one element holding name,
// namespace, labels, annotations and the server's own fields, beside a
// data map and the provider's id.
func configMapSchema() providers.Schema {
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

func labelMap(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	vals := make(map[string]cty.Value, len(m))
	for k, v := range m {
		vals[k] = cty.StringVal(v)
	}
	return cty.MapVal(vals)
}

// configMapObject is a live ConfigMap as the provider reads it back.
func configMapObject(labels map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("refk8s/app-config"),
		"data": cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":             cty.StringVal("app-config"),
			"namespace":        cty.StringVal("refk8s"),
			"labels":           labelMap(labels),
			"annotations":      cty.NullVal(cty.Map(cty.String)),
			"uid":              cty.StringVal("6bc3dcc0"),
			"resource_version": cty.StringVal("575"),
			"generation":       cty.NumberIntVal(1),
		})}),
	})
}

// labelCapturingProvider is [capturingProvider]'s sibling for the label
// shape: every plan is accepted as proposed unless a test's plan hook
// rewrites it, and every apply is recorded rather than sent anywhere.
type labelCapturingProvider struct {
	*tofu.MockProvider
	applyCount    int
	appliedObject cty.Value
	planHook      func(cty.Value) cty.Value
}

func newLabelCapturingProvider() *labelCapturingProvider {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	c := &labelCapturingProvider{MockProvider: p}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		planned := r.ProposedNewState
		if c.planHook != nil {
			planned = c.planHook(planned)
		}
		return providers.PlanResourceChangeResponse{PlannedState: planned}
	}
	p.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.applyCount++
		c.appliedObject = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func configMapEligible(liveLabels map[string]string) (*eligible, *labelCapturingProvider) {
	p := newLabelCapturingProvider()
	e := &eligible{residuable: residuable{
		provider: p,
		schema:   configMapSchema(),
		typeName: "kubernetes_config_map",
		applied:  configMapObject(liveLabels),
		identity: cty.NilVal,
	}, labelled: true}
	return e, p
}

func TestApproveLabel_WritesTheEstateLabelAndNothingElse(t *testing.T) {
	e, p := configMapEligible(map[string]string{"app": "web"})
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_config_map.app"), e, "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	if p.applyCount != 1 {
		t.Fatalf("applied %d times, want 1", p.applyCount)
	}
	got, ok := markers.LabelsOf(p.appliedObject)
	if !ok {
		t.Fatal("the applied object has no readable labels")
	}
	if got[markers.TagEstate] != testEstate || got["app"] != "web" || len(got) != 2 {
		t.Errorf("applied labels = %v, want app=web plus tofu-estate=%s and nothing else", got, testEstate)
	}
	if _, hasAddr := got[markers.TagAddress]; hasAddr {
		t.Errorf("a tofu-address label was written; the Kubernetes marker is the estate alone (#1016)")
	}
	if d := p.appliedObject.GetAttr("data"); !d.RawEquals(e.applied.GetAttr("data")) {
		t.Errorf("data changed across a labels-only write: %#v", d)
	}
	meta := p.appliedObject.GetAttr("metadata").Index(cty.NumberIntVal(0))
	if meta.GetAttr("name").AsString() != "app-config" || meta.GetAttr("namespace").AsString() != "refk8s" {
		t.Errorf("the object's name or namespace moved: %#v", meta)
	}
	if !strings.Contains(out.Detail, "no address") {
		t.Errorf("the detail does not say the marker carries no address: %s", out.Detail)
	}
}

func TestApproveLabel_AlreadyStampedIsIdempotent(t *testing.T) {
	e, p := configMapEligible(map[string]string{"app": "web", markers.TagEstate: testEstate})
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_config_map.app"), e, "")
	if out.Outcome != OutcomeAlreadyStamped {
		t.Fatalf("outcome = %s (%s), want ALREADY_STAMPED", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times on an already-stamped object, want 0", p.applyCount)
	}
}

func TestApproveLabel_AnotherEstatesObjectIsRefused(t *testing.T) {
	e, p := configMapEligible(map[string]string{markers.TagEstate: "other-team"})
	out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_config_map.app"), e, "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "other-team") {
		t.Fatalf("outcome = %s (%s), want FAILED naming other-team", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times on another estate's object, want 0", p.applyCount)
	}
}

func TestApproveLabel_AnIllegalLabelValueIsRefused(t *testing.T) {
	// A legal estate name (64 lowercase letters) that is one character
	// over what a label value may be.
	estate := strings.Repeat("a", markers.LabelMaxValue+1)
	e, p := configMapEligible(nil)
	out := approveOne(context.Background(), estate, mustAddr(t, "kubernetes_config_map.app"), e, "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "not a legal Kubernetes label value") {
		t.Fatalf("outcome = %s (%s), want FAILED as not a legal label value", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times with an illegal label value, want 0", p.applyCount)
	}
}

func TestApproveLabel_RefusesAPlanThatChangesMoreThanLabels(t *testing.T) {
	cases := map[string]struct {
		hook func(cty.Value) cty.Value
		want string
	}{
		"a data change outside the metadata block": {
			hook: func(v cty.Value) cty.Value {
				m := v.AsValueMap()
				m["data"] = cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("tampered")})
				return cty.ObjectVal(m)
			},
			want: "data",
		},
		"a rename inside the metadata block": {
			hook: func(v cty.Value) cty.Value {
				m := v.AsValueMap()
				elem := m["metadata"].Index(cty.NumberIntVal(0)).AsValueMap()
				elem["name"] = cty.StringVal("app-config-renamed")
				m["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(elem)})
				return cty.ObjectVal(m)
			},
			want: "metadata.name",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, p := configMapEligible(nil)
			p.planHook = tc.hook
			out := approveOne(context.Background(), testEstate, mustAddr(t, "kubernetes_config_map.app"), e, "")
			if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, tc.want) {
				t.Fatalf("outcome = %s (%s), want FAILED naming %s", out.Outcome, out.Detail, tc.want)
			}
			if p.applyCount != 0 {
				t.Errorf("applied %d times despite the plan changing more than labels, want 0", p.applyCount)
			}
		})
	}
}

// labelCloudProvider is the fake cloud for the end-to-end case: one
// ConfigMap that ReadResource answers with and ApplyResourceChange
// overwrites, so the label a migration writes can be read back off it.
type labelCloudProvider struct {
	*tofu.MockProvider
	object cty.Value
}

func newLabelCloudProvider(labels map[string]string) *labelCloudProvider {
	c := &labelCloudProvider{MockProvider: &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"kubernetes_config_map": configMapSchema()},
		},
	}, object: configMapObject(labels)}
	c.ConfigureProviderCalled = true
	c.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: c.object}
	}
	c.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		return providers.PlanResourceChangeResponse{PlannedState: r.ProposedNewState}
	}
	c.ApplyResourceChangeFn = func(r providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		c.object = r.PlannedState
		return providers.ApplyResourceChangeResponse{NewState: r.PlannedState}
	}
	return c
}

func (c *labelCloudProvider) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return c, nil
}

// configMapState is a stock state holding kubernetes_config_map.app as
// stock terraform records it, labels included.
func configMapState(labels map[string]string) *states.State {
	var lbl string
	if len(labels) == 0 {
		lbl = "{}"
	} else {
		parts := make([]string, 0, len(labels))
		for k, v := range labels {
			parts = append(parts, `"`+k+`":"`+v+`"`)
		}
		lbl = "{" + strings.Join(parts, ",") + "}"
	}
	state := states.NewState()
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map", Name: "app"}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"refk8s/app-config","data":{"greeting":"hello"},"metadata":[{"name":"app-config","namespace":"refk8s","labels":` + lbl + `,"annotations":null,"uid":"6bc3dcc0","resource_version":"575","generation":1}]}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("kubernetes")},
		addrs.NoKey,
	)
	return state
}

// TestRatify_LabelSurfaceTypeIsStamped is the end-to-end case reference-k8s's
// migrate stage measures (#1067): a stock state holding a Kubernetes object
// ratifies VERIFIED rather than UNTAGGABLE, and -approve writes exactly the
// tofu-estate label onto the live object, reported on the summary line as
// one newly stamped and nothing skipped.
func TestRatify_LabelSurfaceTypeIsStamped(t *testing.T) {
	cloud := newLabelCloudProvider(map[string]string{"app": "web"})
	rat, diags := Ratify(context.Background(), Request{
		Estate:    testEstate,
		State:     configMapState(map[string]string{"app": "web"}),
		Providers: cloud,
	})
	if diags.HasErrors() {
		t.Fatalf("Ratify: %s", diags.Err())
	}
	if len(rat.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(rat.Entries))
	}
	if e := rat.Entries[0]; e.Status != StatusVerified {
		t.Fatalf("kubernetes_config_map.app ratified %s (%s), want VERIFIED: the label surface is a carrier (#1073)", e.Status, e.Detail)
	}
	if got, _ := markers.LabelsOf(cloud.object); got[markers.TagEstate] != "" {
		t.Fatal("Ratify wrote a label; it must never write")
	}

	rep, sdiags := rat.Approve(context.Background())
	if sdiags.HasErrors() {
		t.Fatalf("Approve: %s", sdiags.Err())
	}
	if len(rep.Outcomes) != 1 || rep.Outcomes[0].Outcome != OutcomeStamped {
		t.Fatalf("Approve outcomes = %+v, want one STAMPED", rep.Outcomes)
	}
	got, ok := markers.LabelsOf(cloud.object)
	if !ok || got[markers.TagEstate] != testEstate || got["app"] != "web" || len(got) != 2 {
		t.Errorf("live labels after approve = %v, want app=web plus tofu-estate=%s", got, testEstate)
	}
	for _, o := range rep.Outcomes {
		if o.Outcome == OutcomeSkipped || o.Outcome == OutcomeFailed {
			t.Errorf("an outcome reads %s: nothing may be skipped or fail on a label-surface migration (%s)", o.Outcome, o.Detail)
		}
	}

	// A second run over the now-labelled object is a no-op.
	rat2, _ := Ratify(context.Background(), Request{Estate: testEstate, State: configMapState(got), Providers: cloud})
	rep2, _ := rat2.Approve(context.Background())
	if len(rep2.Outcomes) != 1 || rep2.Outcomes[0].Outcome != OutcomeAlreadyStamped {
		t.Errorf("second approve outcomes = %+v, want one ALREADY_STAMPED", rep2.Outcomes)
	}
}
