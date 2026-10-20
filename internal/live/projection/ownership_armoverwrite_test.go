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
	t.Run("another estate is the marked refusal, not the unmarked one", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnership(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(map[string]string{markers.TagEstate: "other"}), true, false, false)
		if len(errorDiagsWith(b.diags, overwriteSummary)) != 0 {
			t.Errorf("an object marked for another estate read as unmarked:\n%s", renderDiags(b.diags))
		}
		// GitHub issue #2135: an Error of its own, naming the marker.
		if errs := errorDiagsWith(b.diags, SummaryOverwriteMarkedAtKey); len(errs) != 1 || !strings.Contains(errs[0].Description().Detail, `tofu-estate="other" and no tofu-address`) {
			t.Errorf("want one %q naming the other estate's marker, got:\n%s", SummaryOverwriteMarkedAtKey, renderDiags(b.diags))
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

// TestARMOverwrite_holdsUnderTheVerbsThatLeaveTheCreate: declared_untagged's
// keep and report leave the unmarked object out of the estate exactly as
// refuse does, so the plan would still propose the create, and on Azure the
// create is the takeover. The Error holds under both, naming the verb the
// operator wrote and why it did not apply (#1120; lint's
// policy-overwrite-at-key refuses the same verbs before any plan). keep,
// which on AWS is a refusal with no warning at all, is not quiet here.
// converge, like adopt, admits the object, and no create is proposed.
func TestARMOverwrite_holdsUnderTheVerbsThatLeaveTheCreate(t *testing.T) {
	for _, typ := range []string{"azurerm_dns_zone", "azapi_resource"} {
		for _, verb := range []string{"keep", "report"} {
			t.Run(typ+"/"+verb, func(t *testing.T) {
				b := armOverwriteBuilder(t, &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", verb)})
				addr := armOverwriteAddr(t, typ)
				if got := b.checkOwnership(addr, typ, armOverwriteID, armTagsSchema(), armObject(nil), true, false, false); got != ownershipUnowned {
					t.Fatalf("verdict %d, want ownershipUnowned", got)
				}
				errs := errorDiagsWith(b.diags, overwriteSummary)
				if len(errs) != 1 {
					t.Fatalf("declared_untagged = %q let the create stand: want exactly one Error %q, got:\n%s", verb, overwriteSummary, renderDiags(b.diags))
				}
				detail := errs[0].Description().Detail
				for _, want := range []string{
					`policy { declared_untagged = "` + verb + `" } does not change this`,
					`policy { declared_untagged = "adopt" }`,
					"#1546", "#1120",
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
	t.Run("converge admits", func(t *testing.T) {
		addr := armOverwriteAddr(t, "azurerm_dns_zone")
		b := armOverwriteBuilder(t, &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "converge")})
		if got := b.checkOwnership(addr, "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(nil), true, false, false); got != ownershipOK || b.diags.HasErrors() {
			t.Errorf("verdict %d diags:\n%s", got, renderDiags(b.diags))
		}
	})
	t.Run("the default names no verb", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "refuse")})
		b.checkOwnership(armOverwriteAddr(t, "azurerm_dns_zone"), "azurerm_dns_zone", armOverwriteID, armTagsSchema(), armObject(nil), true, false, false)
		errs := errorDiagsWith(b.diags, overwriteSummary)
		if len(errs) != 1 || strings.Contains(errs[0].Description().Detail, "does not change this") {
			t.Errorf("the default refusal:\n%s", renderDiags(b.diags))
		}
	})
	t.Run("AWS keep stays quiet", func(t *testing.T) {
		b := k8sOwnershipBuilder(&Ownership{Estate: policyEstate, Policy: buildPolicy(t, "", "keep")})
		b.checkOwnership(k8sConfigMapAddr(t), "aws_cloudwatch_log_group", "/somebody/logs", tagSurfaceSchema(), tagSurfaceObject(nil), true, false, false)
		if len(b.diags) != 0 {
			t.Errorf("AWS keep changed:\n%s", renderDiags(b.diags))
		}
	})
}

// TestARMOverwrite_namesWhatTheProviderWouldDo: GitHub issue #2133, finding
// 5. Both providers read the id before their create PUT in their default
// configuration, so the refusal is earlier than an apply-time error, not
// the only thing between the plan and a takeover, and it says so per
// provider: azurerm's read is the one a features flag turns off, azapi's
// has no switch. The refusal still fires under both.
func TestARMOverwrite_namesWhatTheProviderWouldDo(t *testing.T) {
	for typ, wants := range map[string][]string{
		"azurerm_dns_zone": {
			"hashicorp/azurerm reads the id before that PUT",
			"a resource with the ID ... already exists",
			"skip_import_check_on_create_and_allow_overwriting_existing_resources = true",
			"before anything is applied",
			"refuses before azurerm would",
		},
		"azapi_resource": {
			"azure/azapi reads the id before that PUT",
			`"Resource already exists"`,
			"no setting that turns the read off",
			"before anything is applied",
			"refuses before azapi would",
		},
	} {
		t.Run(typ, func(t *testing.T) {
			b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
			addr := armOverwriteAddr(t, typ)
			b.checkOwnership(addr, typ, armOverwriteID, armTagsSchema(), armObject(nil), true, false, false)
			errs := errorDiagsWith(b.diags, overwriteSummary)
			if len(errs) != 1 {
				t.Fatalf("want exactly one Error %q, got:\n%s", overwriteSummary, renderDiags(b.diags))
			}
			detail := errs[0].Description().Detail
			for _, want := range wants {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, detail)
				}
			}
			if strings.Contains(detail, "Nothing in this provider is known to read the id first") {
				t.Errorf("the refusal fell back to the unknown-provider sentence for %s:\n%s", typ, detail)
			}
		})
	}
}
