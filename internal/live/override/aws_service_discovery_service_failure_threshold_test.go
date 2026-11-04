// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// serviceDiscoveryService is the service's value. health_check_custom_config
// is a list block of at most one, compared whole by the fake: the block is
// ForceNew in the provider, as its failure_threshold is.
func serviceDiscoveryService(id, name string, threshold int64) cty.Value {
	idv, arn := cty.StringVal(id), cty.StringVal("arn:aws:servicediscovery:us-east-1:1:service/"+id)
	if id == "" {
		idv, arn = cty.NullVal(cty.String), cty.NullVal(cty.String)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":           idv,
		"arn":          arn,
		"name":         cty.StringVal(name),
		"namespace_id": cty.StringVal("ns-1"),
		"health_check_custom_config": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"failure_threshold": cty.NumberIntVal(threshold),
		})}),
	})
}

func init() {
	proofs["aws-service-discovery-service-failure-threshold"] = proof{
		schema: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":           {Type: cty.String, Computed: true},
				"arn":          {Type: cty.String, Computed: true},
				"name":         {Type: cty.String, Required: true},
				"namespace_id": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"health_check_custom_config": {
					Nesting:  configschema.NestingList,
					MaxItems: 1,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"failure_threshold": {Type: cty.Number, Optional: true},
					}},
				},
			},
		},
		forceNew: []string{"name", "namespace_id", "health_check_custom_config"},
		prior:    serviceDiscoveryService("srv-1", "s1-svc", 1),
		config:   serviceDiscoveryService("", "s1-svc", 5),
	}
}

// TestServiceDiscoveryServiceRealChangeStillPlans checks the override does
// not hide a real change. Changing failure_threshold itself changes nothing
// on AWS, so the real changes are: another attribute of the service still
// replaces it with a threshold of 5 configured, and a prior AWS stored as
// something other than 1 still plans the threshold change.
func TestServiceDiscoveryServiceRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-service-discovery-service-failure-threshold")

	p := proofs[o.Name]
	p.config = serviceDiscoveryService("", "s1-renamed", 5)
	resp := planWith(t, o, p, true)
	if !hasReplacePath(resp.RequiresReplace, "name") {
		t.Errorf("a renamed service planned no replacement of name: %#v", resp.RequiresReplace)
	}

	p = proofs[o.Name]
	p.prior = serviceDiscoveryService("srv-1", "s1-svc", 3)
	resp = planWith(t, o, p, true)
	if !hasReplacePath(resp.RequiresReplace, "health_check_custom_config") {
		t.Errorf("a prior threshold of 3 against 5 planned no replacement: %#v", resp.RequiresReplace)
	}
	if hasWarning(resp.Diagnostics, "Provider override applied", o.Name) {
		t.Errorf("prior threshold 3: the override claims to have applied")
	}
}

func hasReplacePath(paths []cty.Path, attr string) bool {
	for _, p := range paths {
		if p.Equals(cty.GetAttrPath(attr)) {
			return true
		}
	}
	return false
}
