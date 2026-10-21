// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #1120: policy-overwrite-at-key. A declared_untagged verb that
// leaves the create standing (keep, report) is refused in an estate that
// declares an azurerm or azapi resource, and nowhere else.

func policyOverwriteIssues(t *testing.T, src string) []Issue {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	var out []Issue
	for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
		if issue.Rule == RulePolicyOverwriteAtKey {
			out = append(out, issue)
		}
	}
	return out
}

func policyOverwriteSrc(verb, resources string) string {
	return `terraform {
  live {
    estate = "e"
    policy {
      declared_untagged = "` + verb + `"
    }
  }
}
` + resources
}

const (
	overwriteAzurerm = `resource "azurerm_resource_group" "rg" {
  name     = "rg"
  location = "westeurope"
}
`
	overwriteAzapi = `resource "azapi_resource" "zone" {
  type      = "Microsoft.Network/dnsZones@2018-05-01"
  parent_id = "/subscriptions/0/resourceGroups/rg"
  name      = "example.com"
}
`
	overwriteAWS = `resource "aws_s3_bucket" "b" {
  bucket = "b"
}
`
	overwriteK8s = `resource "kubernetes_config_map_v1" "c" {
  metadata {
    name = "c"
  }
}
`
	overwriteModule = `module "child" {
  source = "./child"
}
`
)

func TestPolicyOverwriteAtKey(t *testing.T) {
	for name, tc := range map[string]struct {
		verb, resources string
		want            []string // nil: must not fire
	}{
		"keep with azurerm": {verb: "keep", resources: overwriteAWS + overwriteAzurerm,
			want: []string{`assigned "keep"`, "azurerm_resource_group.rg, through provider azurerm", "PUT", "#1546", "#1120", `"adopt"`, "tofu-estate"}},
		"report with azapi": {verb: "report", resources: overwriteAzapi,
			want: []string{`assigned "report"`, "azapi_resource.zone, through provider azapi"}},
		"refuse, the default, written out": {verb: "refuse", resources: overwriteAzurerm},
		"adopt admits":                     {verb: "adopt", resources: overwriteAzurerm},
		"converge admits":                  {verb: "converge", resources: overwriteAzurerm},
		"keep with AWS only":               {verb: "keep", resources: overwriteAWS},
		"keep with Kubernetes only":        {verb: "keep", resources: overwriteK8s},
		"an invalid verb is policy-verb's": {verb: "delete", resources: overwriteAzurerm},
	} {
		t.Run(name, func(t *testing.T) {
			got := policyOverwriteIssues(t, policyOverwriteSrc(tc.verb, tc.resources))
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("fired: %v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one %s, got %v", RulePolicyOverwriteAtKey, got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got[0].Detail, w) {
					t.Errorf("detail does not carry %q:\n%s", w, got[0].Detail)
				}
			}
			if got[0].Construct != `policy.declared_untagged = "`+tc.verb+`"` {
				t.Errorf("construct %q", got[0].Construct)
			}
		})
	}
}

// TestPolicyOverwriteAtKeySeesChildModules: an azurerm resource declared
// only in a child module is in the estate the root's policy governs.
func TestPolicyOverwriteAtKeySeesChildModules(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(policyOverwriteSrc("keep", overwriteModule)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child", "main.tf"), []byte(overwriteAzurerm), 0o600); err != nil {
		t.Fatal(err)
	}
	var got []Issue
	for _, issue := range CheckContext(t.Context(), loadConfigDir(t, dir)) {
		if issue.Rule == RulePolicyOverwriteAtKey {
			got = append(got, issue)
		}
	}
	if len(got) != 1 || !strings.Contains(got[0].Detail, "module.child.azurerm_resource_group.rg") {
		t.Errorf("got %v", got)
	}
}
