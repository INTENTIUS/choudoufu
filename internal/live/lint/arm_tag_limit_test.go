// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120 unit 12: arm-tag-limit. Each case is one
// configuration and the substrings the warning must carry, or none when it
// must not fire.

func armTagLimitIssues(t *testing.T, src string, lctx Context) []Issue {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out []Issue
	for _, issue := range CheckWith(t.Context(), loadConfigDir(t, dir), lctx) {
		if issue.Rule == RuleARMTagLimit {
			out = append(out, issue)
		}
	}
	return out
}

// nTags is an HCL object constructor of n literal tags.
func nTags(n int) string {
	var b strings.Builder
	b.WriteString("{\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    k%d = \"v\"\n", i)
	}
	b.WriteString("  }")
	return b.String()
}

func TestARMTagLimit(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []string // nil: must not fire
	}{
		"cosmos at the ARM type's documented 15": {
			src: `resource "azurerm_cosmosdb_account" "a" {
  name = "a"
  tags = ` + nTags(14) + `
}`,
			want: []string{"would carry 16 tags", "14 from its own tags, plus tofu-estate, tofu-address", "the most it takes is 15",
				"50 (azurerm 5.10.0's own validation of tags, commonschema.Tags())",
				"15 (Microsoft.DocumentDB/databaseAccounts: azapi v2.13.0: the tags description of Microsoft.DocumentDB/databaseAccounts@"},
		},
		"cosmos with room": {
			src: `resource "azurerm_cosmosdb_account" "a" {
  name = "a"
  tags = ` + nTags(13) + `
}`,
		},
		"resource group at ARM's 50": {
			src: `resource "azurerm_resource_group" "rg" {
  name = "rg"
  tags = ` + nTags(49) + `
}`,
			want: []string{"would carry 51 tags", "the most it takes is 50"},
		},
		"resource group with room": {
			src: `resource "azurerm_resource_group" "rg" {
  name = "rg"
  tags = ` + nTags(48) + `
}`,
		},
		"azurerm's own 15 on a key vault secret": {
			src: `resource "azurerm_key_vault_secret" "s" {
  name = "s"
  tags = ` + nTags(14) + `
}`,
			want: []string{"would carry 16 tags", "15 (azurerm 5.10.0's own validation of tags, commonschema.TagsWithMaximumElements(15))"},
		},
		"a count block carries tofu-slot": {
			src: `resource "azurerm_resource_group" "rg" {
  count = 2
  name  = "rg-${count.index}"
  tags  = ` + nTags(48) + `
}`,
			want: []string{"would carry 51 tags", "tofu-slot"},
		},
		"a configured marker is not counted twice": {
			src: `resource "azurerm_resource_group" "rg" {
  name = "rg"
  tags = merge(` + nTags(47) + `, { "Tofu-Estate" = "prod" })
}`,
		},
		"lowercase keys on a data share account": {
			src: `resource "azurerm_data_share_account" "d" {
  name = "d"
  tags = { "Tofu-Estate" = "prod" }
}`,
			want: []string{`the marker tag Tofu-Estate: tag key "Tofu-Estate" is not lowercase; azurerm_data_share_account requires lowercase keys`},
		},
		"an unknown value still takes a slot": {
			src: `variable "owner" {}
resource "azurerm_cosmosdb_account" "a" {
  name = "a"
  tags = merge(` + nTags(13) + `, { owner = var.owner })
}`,
			want: []string{"would carry 16 tags", "not known until apply"},
		},
		"keys it cannot read are skipped": {
			src: `resource "azurerm_resource_group" "src" {
  name = "src"
}
resource "azurerm_cosmosdb_account" "a" {
  name = "a"
  tags = azurerm_resource_group.src.tags
}`,
		},
		"azapi top-level tags": {
			src: `resource "azapi_resource" "a" {
  type      = "Microsoft.DocumentDB/databaseAccounts@2026-03-15"
  name      = "a"
  parent_id = "/subscriptions/0/resourceGroups/rg"
  tags      = ` + nTags(14) + `
}`,
			want: []string{"would carry 16 tags", "Microsoft.DocumentDB/databaseAccounts"},
		},
		"azapi body.tags": {
			src: `resource "azapi_resource" "a" {
  type      = "Microsoft.DocumentDB/databaseAccounts@2026-03-15"
  name      = "a"
  parent_id = "/subscriptions/0/resourceGroups/rg"
  body = {
    tags = ` + nTags(14) + `
  }
}`,
			want: []string{"would carry 16 tags"},
		},
		"azapi type that takes no tags": {
			src: `resource "azapi_resource" "a" {
  type      = "Microsoft.Network/virtualNetworks/subnets@2023-09-01"
  name      = "a"
  parent_id = "/x"
  tags      = ` + nTags(60) + `
}`,
		},
		"another family": {
			src: `resource "aws_s3_bucket" "b" {
  bucket = "b"
  tags   = ` + nTags(60) + `
}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			issues := armTagLimitIssues(t, tc.src, Context{})
			if tc.want == nil {
				if len(issues) != 0 {
					t.Fatalf("fired: %s", issues[0].Detail)
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("got %d arm-tag-limit issues, want 1: %v", len(issues), issues)
			}
			for _, w := range tc.want {
				if !strings.Contains(issues[0].Detail, w) {
					t.Errorf("detail does not carry %q:\n%s", w, issues[0].Detail)
				}
			}
			if issues[0].Rule.Severity() != SeverityWarning {
				t.Error("arm-tag-limit is not a warning")
			}
		})
	}
}

// TestARMTagLimitAsksTheSchema: with schemas, a type whose tags is not
// ARM's map (an API Management named value's list of labels) carries no
// marker and is never counted, whatever its tags hold.
func TestARMTagLimitAsksTheSchema(t *testing.T) {
	src := `resource "azurerm_resource_group" "rg" {
  name = "rg"
  tags = ` + nTags(49) + `
}`
	listTags := map[string]providers.Schema{"azurerm_resource_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.List(cty.String), Optional: true},
	}}}}
	if issues := armTagLimitIssues(t, src, Context{Schemas: listTags}); len(issues) != 0 {
		t.Errorf("a schema with no ARM tags map still counted markers: %s", issues[0].Detail)
	}
	mapTags := map[string]providers.Schema{"azurerm_resource_group": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"name": {Type: cty.String, Required: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}}}}
	if issues := armTagLimitIssues(t, src, Context{Schemas: mapTags}); len(issues) != 1 {
		t.Errorf("the arm-tags schema did not fire: %v", issues)
	}
}

// TestARMTagLimitHonoursTheTargetScope: a block -target/-exclude removed
// from the plan is never stamped, so it cannot overflow. Its own fixture,
// as gcp-label-count has, because an azurerm pair in the shared targeting
// fixture would also move unadmitted-type's counts there.
func TestARMTagLimitHonoursTheTargetScope(t *testing.T) {
	src := `resource "azurerm_resource_group" "kept" {
  name = "kept"
  tags = ` + nTags(49) + `
}
resource "azurerm_resource_group" "dropped" {
  name = "dropped"
  tags = ` + nTags(49) + `
}`
	keep := func(r addrs.ConfigResource) bool { return r.Resource.Name == "kept" }
	issues := armTagLimitIssues(t, src, Context{Scope: keep})
	if len(issues) != 1 || issues[0].Construct != "azurerm_resource_group.kept" {
		t.Errorf("issues %v; want the kept block alone", issues)
	}
	if issues := armTagLimitIssues(t, src, Context{}); len(issues) != 2 {
		t.Errorf("untargeted: %d issues, want 2", len(issues))
	}
}
