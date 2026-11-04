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

func init() {
	proofs["aws-security-group-rule-cidr-order"] = proof{
		schema: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                {Type: cty.String, Computed: true},
			"cidr_blocks":       {Type: cty.List(cty.String), Optional: true},
			"ipv6_cidr_blocks":  {Type: cty.List(cty.String), Optional: true},
			"security_group_id": {Type: cty.String, Required: true},
		}},
		forceNew: []string{"cidr_blocks", "ipv6_cidr_blocks"},
		prior: cty.ObjectVal(map[string]cty.Value{
			"id":                cty.StringVal("sgrule-1"),
			"cidr_blocks":       cty.ListVal([]cty.Value{cty.StringVal("10.0.0.0/24"), cty.StringVal("10.0.1.0/24")}),
			"ipv6_cidr_blocks":  cty.ListVal([]cty.Value{cty.StringVal("2001:db8::/64"), cty.StringVal("2001:db8:1::/64")}),
			"security_group_id": cty.StringVal("sg-1"),
		}),
		config: cty.ObjectVal(map[string]cty.Value{
			"id":                cty.NullVal(cty.String),
			"cidr_blocks":       cty.ListVal([]cty.Value{cty.StringVal("10.0.1.0/24"), cty.StringVal("10.0.0.0/24")}),
			"ipv6_cidr_blocks":  cty.ListVal([]cty.Value{cty.StringVal("2001:db8:1::/64"), cty.StringVal("2001:db8::/64")}),
			"security_group_id": cty.StringVal("sg-1"),
		}),
	}
}

// TestRealChangeStillPlans checks an override does not hide a real change:
// a CIDR that is added still replaces the rule.
func TestRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-security-group-rule-cidr-order")
	p := proofs[o.Name]
	cfg := p.config.AsValueMap()
	cfg["cidr_blocks"] = cty.ListVal([]cty.Value{cty.StringVal("10.0.1.0/24"), cty.StringVal("10.0.2.0/24")})
	p.config = cty.ObjectVal(cfg)
	resp := planWith(t, o, p, true)
	if len(resp.RequiresReplace) == 0 {
		t.Fatalf("a different CIDR set planned no replacement")
	}
}
