// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// gcpLabels writes n literal labels, label-0 = "x" onwards.
func gcpLabels(n int) string {
	var b strings.Builder
	b.WriteString("{\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    label-%d = \"x\"\n", i)
	}
	b.WriteString("  }")
	return b.String()
}

func loadGCPFiles(t *testing.T, files map[string]string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return loadConfigDir(t, dir)
}

func onlyRule(issues []Issue, rule Rule) []Issue {
	var out []Issue
	for _, issue := range issues {
		if issue.Rule == rule {
			out = append(out, issue)
		}
	}
	return out
}

// TestGCPLabelCount pins the count [checkGCPLabelCount] makes: the block's
// labels, its provider's default_labels and attribution label as a union,
// plus two markers, or three on a count block. Each case sits one label
// either side of GCP's 64 where it can.
func TestGCPLabelCount(t *testing.T) {
	const providerOff = `
provider "google" {
  add_terraform_attribution_label = false
}
`
	bucket := func(labels string, extra string) string {
		return fmt.Sprintf(`
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
  labels   = %s
%s
}
`, labels, extra)
	}

	cases := []struct {
		name    string
		src     string
		refuse  bool
		mention string
	}{
		{name: "62 labels and two markers fit", src: providerOff + bucket(gcpLabels(62), "")},
		{name: "63 labels and two markers do not", src: providerOff + bucket(gcpLabels(63), ""), refuse: true, mention: "would carry 65 labels"},
		{name: "a count block counts tofu-slot", src: providerOff + bucket(gcpLabels(62), "  count = 2"), refuse: true, mention: "would carry 65 labels"},
		{name: "the attribution label is on by default", src: bucket(gcpLabels(62), ""), refuse: true, mention: "would carry 65 labels"},
		{
			name: "default_labels join the union, and a shared key counts once",
			src: `
provider "google" {
  add_terraform_attribution_label = false
  default_labels = {
    label-0 = "shared"
    team    = "a"
  }
}
` + bucket(gcpLabels(62), ""),
			refuse:  true,
			mention: "would carry 65 labels",
		},
		{
			name: "a key whose value is unknown is counted",
			src: providerOff + `
resource "google_storage_bucket" "src" {
  name     = "src"
  location = "EU"
}
` + bucket(strings.Replace(gcpLabels(63), `label-0 = "x"`, `label-0 = google_storage_bucket.src.name`, 1), ""),
			refuse:  true,
			mention: "counted, since the key takes a slot",
		},
		{
			name: "merge() is the union of its arguments",
			src: providerOff + `
locals {
  common = { for i in range(60) : "label-${i}" => "x" }
}
` + bucket(`merge(local.common, { a = "1", b = "2", c = "3" })`, ""),
			refuse:  true,
			mention: "would carry 65 labels",
		},
		{
			name: "keys that cannot be read are skipped",
			src: providerOff + `
variable "labels" {
  type = map(string)
}
` + bucket(`var.labels`, ""),
		},
		{
			name: "an unreadable default_labels is skipped",
			src: `
variable "defaults" {
  type = map(string)
}
provider "google" {
  default_labels = var.defaults
}
` + bucket(gcpLabels(63), ""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadGCPFiles(t, map[string]string{"main.tf": gcpRequiredProviders + tc.src})
			got := onlyRule(CheckContext(t.Context(), cfg), RuleGCPLabelCount)
			if !tc.refuse {
				if len(got) != 0 {
					t.Fatalf("want no %s, got %v", RuleGCPLabelCount, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one %s, got %d: %v", RuleGCPLabelCount, len(got), got)
			}
			if !strings.Contains(got[0].Detail, tc.mention) {
				t.Errorf("Detail does not say %q:\n%s", tc.mention, got[0].Detail)
			}
			if !HasErrors(got) {
				t.Errorf("%s must refuse: the create would fail", RuleGCPLabelCount)
			}
		})
	}
}

// TestGCPLabelCountReadsTheSurfaceWhenSchemasArePresent: with schemas, a
// type whose labels map carries no marker is not counted. The template's
// labels propagate to the VMs made from it ([markers.GooglePropagatedLabels]),
// so this run never stamps them.
func TestGCPLabelCountReadsTheSurfaceWhenSchemasArePresent(t *testing.T) {
	src := gcpRequiredProviders + `
provider "google" {
  add_terraform_attribution_label = false
}
resource "google_compute_instance_template" "t" {
  name   = "t"
  labels = ` + gcpLabels(63) + `
}
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
  labels   = ` + gcpLabels(63) + `
}
`
	labelled := func(description string) providers.Schema {
		return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"name":             {Type: cty.String, Required: true},
			"location":         {Type: cty.String, Optional: true},
			"labels":           {Type: cty.Map(cty.String), Optional: true, Description: description},
			"effective_labels": {Type: cty.Map(cty.String), Computed: true},
			"terraform_labels": {Type: cty.Map(cty.String), Computed: true},
		}}}
	}
	schemas := map[string]providers.Schema{
		// hashicorp/google 8.6.0's own words for the template's labels.
		"google_compute_instance_template": labelled("A set of key/value label pairs to assign to instances created from this template."),
		"google_storage_bucket":            labelled("A set of key/value label pairs to assign to the bucket."),
	}

	cfg := loadGCPFiles(t, map[string]string{"main.tf": src})
	got := onlyRule(CheckWith(t.Context(), cfg, Context{Schemas: schemas}), RuleGCPLabelCount)
	if len(got) != 1 || got[0].Construct != "google_storage_bucket.b" {
		t.Fatalf("want exactly google_storage_bucket.b refused, got %v", got)
	}
}

// TestGCPLabelCountHonoursTheTargetScope is TestLintHonoursTheTargetScope's
// three arms for this rule, on a fixture of its own: see
// TestEveryScopedRuleIsInTheFixture for why it is not in the shared one.
func TestGCPLabelCountHonoursTheTargetScope(t *testing.T) {
	src := gcpRequiredProviders + `
provider "google" {
  add_terraform_attribution_label = false
}
resource "google_storage_bucket" "crowded_targeted" {
  name     = "t"
  location = "EU"
  labels   = ` + gcpLabels(63) + `
}
resource "google_storage_bucket" "crowded_excluded" {
  name     = "e"
  location = "EU"
  labels   = ` + gcpLabels(63) + `
}
`
	cfg := loadGCPFiles(t, map[string]string{"main.tf": src})
	for _, arm := range []struct {
		name  string
		scope identity.Scope
		want  []string
	}{
		{"no scope", nil, []string{"google_storage_bucket.crowded_excluded", "google_storage_bucket.crowded_targeted"}},
		{"keeps the targeted half", scopeKeepingTargeted(), []string{"google_storage_bucket.crowded_targeted"}},
		{"keeps nothing", scopeKeepingNothing(), nil},
	} {
		t.Run(arm.name, func(t *testing.T) {
			got := constructsFor(onlyRule(CheckWith(t.Context(), cfg, Context{Scope: arm.scope}), RuleGCPLabelCount), RuleGCPLabelCount)
			if strings.Join(got, ",") != strings.Join(arm.want, ",") {
				t.Errorf("got %v, want %v", got, arm.want)
			}
		})
	}
}

// TestMarkerKeysAreLegalGCPLabelKeys: a label key is 1 to 63 characters,
// lowercase, starting with a letter, of [a-z0-9_-]. The stamp writes only
// these three keys, so nothing it adds can be an invalid key.
func TestMarkerKeysAreLegalGCPLabelKeys(t *testing.T) {
	legal := regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	for _, key := range []string{markers.TagEstate, markers.TagAddress, markers.TagSlot, gcpAttributionLabel} {
		if !legal.MatchString(key) {
			t.Errorf("%q is not a legal GCP label key", key)
		}
	}
}
