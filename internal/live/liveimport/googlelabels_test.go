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

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1119: the GCP carrier, the tags write on a hashicorp/google
// resource's top-level labels map (approveGoogleLabels).

func googleBucketSchema() providers.Schema {
	m := cty.Map(cty.String)
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Required: true},
		"location":         {Type: cty.String, Required: true},
		"labels":           {Type: m, Optional: true},
		"terraform_labels": {Type: m, Computed: true},
		"effective_labels": {Type: m, Computed: true},
	}}}
}

func googleBucketObject(labels map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"name":             cty.StringVal("logs"),
		"location":         cty.StringVal("EU"),
		"labels":           labelMap(labels),
		"terraform_labels": labelMap(labels),
		"effective_labels": labelMap(labels),
	})
}

func googleBucketEligible(labels map[string]string) (*eligible, *labelCapturingProvider) {
	p := newLabelCapturingProvider()
	e := &eligible{residuable: residuable{
		provider: p,
		schema:   googleBucketSchema(),
		typeName: "google_storage_bucket",
		applied:  googleBucketObject(labels),
		identity: cty.NilVal,
	}, googleLabelled: true}
	return e, p
}

func TestApproveGoogleLabels_WritesBothMarkers(t *testing.T) {
	addr := mustAddr(t, "google_storage_bucket.logs")
	e, p := googleBucketEligible(map[string]string{"team": "net"})
	out := approveOne(context.Background(), testEstate, addr, e, "")
	if out.Outcome != OutcomeStamped {
		t.Fatalf("outcome = %s (%s), want STAMPED", out.Outcome, out.Detail)
	}
	got := map[string]string{}
	for k, v := range p.appliedObject.GetAttr("labels").AsValueMap() {
		got[k] = v.AsString()
	}
	if len(got) != 3 || got["team"] != "net" || got[markers.TagEstate] != testEstate || got[markers.TagAddress] != markers.GoogleAddressValue(addr) {
		t.Errorf("applied labels = %v, want team plus tofu-estate and the encoded tofu-address", got)
	}
}

func TestApproveGoogleLabels_RefusesAPlanThatChangesMore(t *testing.T) {
	e, p := googleBucketEligible(nil)
	p.planHook = func(v cty.Value) cty.Value {
		m := v.AsValueMap()
		m["location"] = cty.StringVal("US")
		return cty.ObjectVal(m)
	}
	out := approveOne(context.Background(), testEstate, mustAddr(t, "google_storage_bucket.logs"), e, "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "Approve is a labels-only write") || !strings.Contains(out.Detail, "location") {
		t.Fatalf("outcome = %s (%s), want FAILED as a labels-only write naming location", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times, want 0", p.applyCount)
	}
}

func TestApproveGoogleLabels_AnotherEstatesObjectIsRefused(t *testing.T) {
	e, p := googleBucketEligible(map[string]string{markers.TagEstate: "other-team"})
	out := approveOne(context.Background(), testEstate, mustAddr(t, "google_storage_bucket.logs"), e, "")
	if out.Outcome != OutcomeFailed || !strings.Contains(out.Detail, "other-team") {
		t.Fatalf("outcome = %s (%s), want FAILED naming other-team", out.Outcome, out.Detail)
	}
	if p.applyCount != 0 {
		t.Errorf("applied %d times on another estate's object, want 0", p.applyCount)
	}
}
