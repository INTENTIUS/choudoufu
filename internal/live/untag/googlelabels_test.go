// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1119: the GCP release. The object is read the way
// hashicorp/google reads one on an imported prior that names none of its
// labels: labels and terraform_labels empty, effective_labels holding
// everything. The release must still find tofu-estate and remove it from
// labels, and only from labels.

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

func googleBucketReadBack(effective map[string]string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"name":             cty.StringVal("logs"),
		"location":         cty.StringVal("EU"),
		"labels":           cty.MapValEmpty(cty.String),
		"terraform_labels": cty.MapValEmpty(cty.String),
		"effective_labels": labelMap(effective),
	})
}

func googleBucketTarget() Target {
	return Target{TypeName: "google_storage_bucket", ProviderType: "google", ImportID: "p/logs", Marker: "prod-net"}
}

func TestRelease_GoogleLabelsReleasesTheEstateLabel(t *testing.T) {
	c := newFakeCluster("google_storage_bucket", googleBucketSchema(),
		googleBucketReadBack(map[string]string{"team": "net", markers.TagEstate: "prod-net"}))
	// The provider's own diff (tpgresource.setLabelsFields) recomputes
	// terraform_labels from labels and drops from effective_labels a key
	// that leaves labels; the fake plans the same.
	var proposed cty.Value
	c.planHook = func(v cty.Value) cty.Value {
		proposed = v
		m := v.AsValueMap()
		eff := map[string]cty.Value{}
		for k, val := range m["effective_labels"].AsValueMap() {
			if k == markers.TagEstate && !m["labels"].HasIndex(cty.StringVal(k)).True() {
				continue
			}
			eff[k] = val
		}
		m["effective_labels"] = cty.MapVal(eff)
		m["terraform_labels"] = m["labels"]
		return cty.ObjectVal(m)
	}

	res, diags := Release(context.Background(), c, nil, testKey, "prod-net", []Target{googleBucketTarget()})
	if out := res.Outcomes[0]; !out.OK || diags.HasErrors() {
		t.Fatalf("outcome = %s (diags %v), want RELEASED", out, diags.Err())
	}
	if c.applied != 1 {
		t.Fatalf("applied %d times, want 1: a read with empty labels must still plan the release", c.applied)
	}
	labels := proposed.GetAttr("labels")
	if labels.LengthInt() != 0 {
		t.Errorf("planned labels = %#v, want empty: only tofu-estate was in them, and team was never the configuration's", labels)
	}
	if !proposed.GetAttr("location").RawEquals(cty.StringVal("EU")) {
		t.Errorf("location moved: %#v", proposed.GetAttr("location"))
	}
}

func TestRelease_GoogleLabelsRefusesAPlanThatChangesMore(t *testing.T) {
	c := newFakeCluster("google_storage_bucket", googleBucketSchema(),
		googleBucketReadBack(map[string]string{markers.TagEstate: "prod-net"}))
	c.planHook = func(v cty.Value) cty.Value {
		m := v.AsValueMap()
		m["location"] = cty.StringVal("US")
		return cty.ObjectVal(m)
	}
	res, _ := Release(context.Background(), c, nil, testKey, "prod-net", []Target{googleBucketTarget()})
	out := res.Outcomes[0]
	if out.OK || c.applied != 0 {
		t.Fatalf("outcome = %s, applied %d; want a refusal with no write", out, c.applied)
	}
	if !strings.Contains(out.Detail, "location") || !strings.Contains(out.Detail, "labels-only") {
		t.Errorf("the refusal does not name location as a labels-only write: %s", out.Detail)
	}
}
