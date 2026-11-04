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

// backupOptInRead is what AWS returns for resource_type_opt_in_preference
// after { "DynamoDB" = true } is applied: every resource type in the region.
func backupOptInRead() cty.Value {
	m := map[string]cty.Value{}
	for _, k := range []string{
		"Aurora", "CloudFormation", "DSQL", "DocumentDB", "DynamoDB", "EBS", "EC2", "EFS", "EKS", "FSx",
		"Neptune", "RDS", "Redshift", "Redshift Serverless", "S3", "SAP HANA on Amazon EC2",
		"Storage Gateway", "Timestream", "VirtualMachine",
	} {
		m[k] = cty.True
	}
	return cty.MapVal(m)
}

func init() {
	proofs["aws-backup-region-settings-subset"] = proof{
		schema: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":                              {Type: cty.String, Computed: true},
			"resource_type_opt_in_preference": {Type: cty.Map(cty.Bool), Required: true},
			// Optional and Computed in the provider. The fake plans a
			// computed attribute from prior whatever the config says, which
			// SDKv2 does not do for a configured one, so it is declared
			// Optional here to make the fake plan it from config as the
			// provider does.
			"resource_type_management_preference": {Type: cty.Map(cty.Bool), Optional: true},
		}},
		prior: cty.ObjectVal(map[string]cty.Value{
			"id":                              cty.StringVal("us-east-1"),
			"resource_type_opt_in_preference": backupOptInRead(),
			"resource_type_management_preference": cty.MapVal(map[string]cty.Value{
				"DynamoDB": cty.True,
				"EFS":      cty.True,
			}),
		}),
		config: cty.ObjectVal(map[string]cty.Value{
			"id": cty.NullVal(cty.String),
			"resource_type_opt_in_preference": cty.MapVal(map[string]cty.Value{
				"DynamoDB": cty.True,
			}),
			"resource_type_management_preference": cty.MapVal(map[string]cty.Value{
				"EFS": cty.True,
			}),
		}),
	}
}

// TestBackupRegionSettingsRealChangeStillPlans checks the override does not
// hide a real change: a configured key whose value differs from prior, or
// that prior does not have, still plans an update, in either map.
func TestBackupRegionSettingsRealChangeStillPlans(t *testing.T) {
	o, _ := Lookup("aws-backup-region-settings-subset")
	for name, tc := range map[string]struct {
		attr string
		val  cty.Value
	}{
		"opt-in value flipped": {"resource_type_opt_in_preference", cty.MapVal(map[string]cty.Value{
			"DynamoDB": cty.False,
		})},
		"opt-in key AWS did not return": {"resource_type_opt_in_preference", cty.MapVal(map[string]cty.Value{
			"DynamoDB":        cty.True,
			"NewService":      cty.True,
			"Storage Gateway": cty.True,
		})},
		"management value flipped": {"resource_type_management_preference", cty.MapVal(map[string]cty.Value{
			"DynamoDB": cty.False,
		})},
	} {
		t.Run(name, func(t *testing.T) {
			p := proofs[o.Name]
			cfg := p.config.AsValueMap()
			cfg[tc.attr] = tc.val
			p.config = cty.ObjectVal(cfg)
			resp := planWith(t, o, p, true)
			if resp.PlannedState.RawEquals(p.prior) {
				t.Fatalf("a different %s planned no change", tc.attr)
			}
			got := resp.PlannedState.GetAttr(tc.attr)
			if !got.RawEquals(tc.val) {
				t.Errorf("%s planned %#v, want the configured %#v", tc.attr, got, tc.val)
			}
		})
	}
}

func TestConfiguredKeysMatchPrior(t *testing.T) {
	prior := cty.MapVal(map[string]cty.Value{"A": cty.True, "B": cty.False})
	for _, tc := range []struct {
		config cty.Value
		want   bool
	}{
		{cty.MapVal(map[string]cty.Value{"A": cty.True}), true},
		{cty.MapVal(map[string]cty.Value{"A": cty.True, "B": cty.False}), true},
		{cty.MapVal(map[string]cty.Value{"B": cty.True}), false},
		{cty.MapVal(map[string]cty.Value{"C": cty.False}), false},
		{cty.MapVal(map[string]cty.Value{"A": cty.True, "C": cty.True}), false},
		{cty.MapVal(map[string]cty.Value{"A": cty.NullVal(cty.Bool)}), false},
	} {
		if got := configuredKeysMatchPrior(prior, tc.config); got != tc.want {
			t.Errorf("configuredKeysMatchPrior(%#v) = %v, want %v", tc.config, got, tc.want)
		}
	}
}
