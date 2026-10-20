// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120, ruled on #1122: an ARM create is a PUT, and a PUT at
// an id that already exists updates the object there. A declared Azure
// object read at its declared id with no tofu-estate tag is therefore
// refused by name, as an Error, before any create is proposed - the
// declared_untagged default made an error, where on AWS it stays a warning
// beside a create the cloud refuses.
//
// The summary is spelled out rather than read off the constant, so a
// rename of the operator-facing text is a visible test change.
const overwriteSummary = "Unmarked live object at the declared id would be overwritten"

const armOverwriteID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com"

func armOverwriteBuilder(t *testing.T, own *Ownership) *builder {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    azapi   = { source = "azure/azapi" }
  }
}
resource "azurerm_dns_zone" "z" {
  name                = "example.com"
  resource_group_name = "rg"
}
resource "azapi_resource" "z" {
  type      = "Microsoft.Network/dnsZones@2018-05-01"
  parent_id = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"
  name      = "example.com"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := k8sOwnershipBuilder(own)
	b.cfg = loadConfig(t, dir)
	return b
}

func armTagsSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}
}

func armObject(tags map[string]string) cty.Value {
	tagVal := cty.NullVal(cty.Map(cty.String))
	if tags != nil {
		vals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			vals[k] = cty.StringVal(v)
		}
		tagVal = cty.MapVal(vals)
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(armOverwriteID),
		"name": cty.StringVal("example.com"),
		"tags": tagVal,
	})
}

func armOverwriteAddr(t *testing.T, typ string) addrs.AbsResourceInstance {
	t.Helper()
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typ, Name: "z"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

func TestARMOverwrite_unmarkedReadObjectIsAnError(t *testing.T) {
	for _, typ := range []string{"azurerm_dns_zone", "azapi_resource"} {
		for name, policy := range map[string]string{"no policy block": "", "declared_untagged=refuse": "refuse"} {
			t.Run(typ+"/"+name, func(t *testing.T) {
				own := &Ownership{Estate: k8sOwnershipEstate}
				if policy != "" {
					own.Policy = buildPolicy(t, "", policy)
				}
				b := armOverwriteBuilder(t, own)
				addr := armOverwriteAddr(t, typ)
				if got := b.checkOwnership(addr, typ, armOverwriteID, armTagsSchema(), armObject(nil), true, false, false); got != ownershipUnowned {
					t.Fatalf("verdict %d, want ownershipUnowned", got)
				}
				errs := errorDiagsWith(b.diags, overwriteSummary)
				if len(errs) != 1 {
					t.Fatalf("want exactly one Error %q, got:\n%s", overwriteSummary, renderDiags(b.diags))
				}
				detail := errs[0].Description().Detail
				for _, want := range []string{
					armOverwriteID, addr.String(), "PUT",
					`policy { declared_untagged = "adopt" }`,
					markers.TagEstate + `="` + k8sOwnershipEstate + `"`,
					markers.TagAddress,
				} {
					if !strings.Contains(detail, want) {
						t.Errorf("the refusal does not say %q:\n%s", want, detail)
					}
				}
				if hasDiag(b.diags, SummaryOutsideEstate, armOverwriteID) {
					t.Errorf("also reported under the warning:\n%s", renderDiags(b.diags))
				}
				if om, ok := b.omitted[addr.String()]; !ok || om.Reason != ReasonUnowned {
					t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
				}
			})
		}
	}
}

func TestARMOverwrite_whatTheRuleDoesNotReach(t *testing.T) {
	addr := armOverwriteAddr(t, "azurerm_dns_zone")
	t.Run("adopt admits", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "adopt")})
		if got := b.checkOwnership(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(nil), true, false, false); got != ownershipOK || b.diags.HasErrors() {
			t.Errorf("verdict %d diags:\n%s", got, renderDiags(b.diags))
		}
	})
	t.Run("this estate's marker in another case is owned", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		obj := armObject(map[string]string{"Tofu-Estate": k8sOwnershipEstate, "TOFU-ADDRESS": addr.String()})
		if got := b.checkOwnership(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), obj, true, false, false); got != ownershipOK || b.diags.HasErrors() {
			t.Errorf("verdict %d diags:\n%s", got, renderDiags(b.diags))
		}
	})
	t.Run("another estate keeps its own refusal", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnership(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(map[string]string{markers.TagEstate: "other"}), true, false, false)
		if len(errorDiagsWith(b.diags, overwriteSummary)) != 0 {
			t.Errorf("an object marked for another estate read as unmarked:\n%s", renderDiags(b.diags))
		}
	})
	t.Run("a record naming another id is not the declared id", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnershipAt(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(nil), true, false, true, false)
		if len(errorDiagsWith(b.diags, overwriteSummary)) != 0 {
			t.Errorf("an object at a record's other id raised the declared-id error:\n%s", renderDiags(b.diags))
		}
	})
	t.Run("AWS is unchanged", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate})
		b.checkOwnership(k8sConfigMapAddr(t), "aws_cloudwatch_log_group", "/somebody/logs", tagSurfaceSchema(), tagSurfaceObject(nil), true, false, false)
		if b.diags.HasErrors() || !hasDiag(b.diags, SummaryOutsideEstate, "/somebody/logs") {
			t.Errorf("the AWS warning changed:\n%s", renderDiags(b.diags))
		}
	})
}
