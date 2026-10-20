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

const gcpRequiredProviders = `
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
    google-beta = {
      source = "hashicorp/google-beta"
    }
  }
}
`

// TestGCPMultiProject pins which google blocks [checkGCPMultiProject]
// counts. The limits fixture covers two provider blocks; these cover the
// resource half, google-beta, a child module, and the unknowns it skips.
// Each configuration's resources are also refused as unadmitted-type
// (lint has no schemas here), so only this rule's issues are read.
func TestGCPMultiProject(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  []string // substrings of the one warning's Detail; nil means no warning
	}{
		{
			name: "resource project differs from provider",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "EU"
  project  = "audit"
}
`},
			want: []string{`"app" by provider "google"`, `"audit" by google_storage_bucket.logs`},
		},
		{
			name: "google-beta counts with google",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
provider "google-beta" {
  project = "beta"
}
`},
			want: []string{`"app" by provider "google"`, `"beta" by provider "google-beta"`},
		},
		{
			name: "child module resource",
			files: map[string]string{
				"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
module "net" {
  source = "./net"
}
`,
				"net/main.tf": `
resource "google_compute_network" "this" {
  name    = "net"
  project = "shared-vpc"
}
`,
			},
			want: []string{`"shared-vpc" by module.net.google_compute_network.this`},
		},
		{
			name: "one project named twice",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
variable "project" {
  default = "app"
}
provider "google" {
  project = var.project
}
resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "EU"
  project  = "app"
}
`},
		},
		{
			name: "unknown projects are skipped",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
variable "project" {}
provider "google" {
  project = "app"
}
provider "google" {
  alias   = "other"
  project = var.project
}
resource "google_storage_bucket" "logs" {
  for_each = toset(["a"])
  name     = each.key
  location = "EU"
  project  = each.value
}
resource "google_storage_bucket" "data" {
  name     = "data"
  location = "EU"
  project  = google_storage_bucket.logs["a"].project
}
`},
		},
		{
			name: "an aws provider's project-shaped argument is not google's",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  tags = {
    project = "other"
  }
}
`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, src := range tc.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cfg := loadConfigDir(t, dir)

			var got []Issue
			for _, issue := range CheckContext(t.Context(), cfg) {
				if issue.Rule == RuleGCPMultiProject {
					got = append(got, issue)
				}
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("want no %s warning, got %v", RuleGCPMultiProject, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one %s warning, got %d: %v", RuleGCPMultiProject, len(got), got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got[0].Detail, want) {
					t.Errorf("Detail does not name %s:\n%s", want, got[0].Detail)
				}
			}
			if HasErrors(got) {
				t.Errorf("%s must be a warning, never a refusal", RuleGCPMultiProject)
			}
		})
	}
}
