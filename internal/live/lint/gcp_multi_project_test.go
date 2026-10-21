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
variable "projects" {
  type = set(string)
}
provider "google" {
  project = "app"
}
provider "google" {
  alias   = "other"
  project = var.project
}
resource "google_storage_bucket" "logs" {
  for_each = var.projects
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
			// GitHub issue #2145: each.value used to be skipped even where
			// the for_each collection is known, so this estate over three
			// projects read as one.
			name: "each.value is read per instance",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
resource "google_storage_bucket" "logs" {
  for_each = toset(["app", "audit"])
  name     = "logs-${each.key}"
  location = "EU"
  project  = each.value
}
`},
			want: []string{`"app" by google_storage_bucket.logs["app"], provider "google"`, `"audit" by google_storage_bucket.logs["audit"]`},
		},
		{
			name: "a for_each map's values are read per instance",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
locals {
  envs = { dev = "proj-dev", prod = "proj-prod" }
}
resource "google_storage_bucket" "env" {
  for_each = local.envs
  name     = each.key
  location = "EU"
  project  = each.value
}
`},
			want: []string{`"proj-dev" by google_storage_bucket.env["dev"]`, `"proj-prod" by google_storage_bucket.env["prod"]`},
		},
		{
			name: "count.index is read per instance",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
variable "projects" {
  default = ["one", "two"]
}
resource "google_storage_bucket" "b" {
  count    = 2
  name     = "b${count.index}"
  location = "EU"
  project  = var.projects[count.index]
}
`},
			want: []string{`"one" by google_storage_bucket.b[0]`, `"two" by google_storage_bucket.b[1]`},
		},
		{
			// A created project whose id configuration states is that id,
			// and a second project beside the provider's.
			name: "a referenced project argument is read from its block",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
resource "google_project" "p" {
  name       = "p"
  project_id = "made-here"
}
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
  project  = google_project.p.project_id
}
`},
			want: []string{`"made-here" by google_storage_bucket.b`, `"app" by provider "google"`},
		},
		{
			// An id only apply knows is named, never guessed.
			name: "a referenced project known only at apply is named",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
variable "suffix" {}
provider "google" {
  project = "app"
}
resource "google_project" "p" {
  name       = "p"
  project_id = "p-${var.suffix}"
}
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
  project  = google_project.p.project_id
}
resource "google_storage_bucket" "c" {
  name     = "c"
  location = "EU"
  project  = "audit"
}
`},
			want: []string{"google_storage_bucket.b (its project is google_project.p.project_id, which is known only after apply)"},
		},
		{
			name: "a created project with a known id is that id",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
provider "google" {
  project = "app"
}
resource "google_project" "p" {
  name       = "p"
  project_id = "app"
}
resource "google_storage_bucket" "b" {
  name     = "b"
  location = "EU"
  project  = google_project.p.project_id
}
`},
		},
		{
			// The warning still fires on what it can read, and names what
			// it could not, so its list of projects is not taken as whole.
			name: "unknowns are named when the warning fires",
			files: map[string]string{"main.tf": gcpRequiredProviders + `
variable "projects" {
  type = set(string)
}
provider "google" {
  project = "app"
}
resource "google_storage_bucket" "audit" {
  name     = "audit"
  location = "EU"
  project  = "audit"
}
resource "google_storage_bucket" "logs" {
  for_each = var.projects
  name     = each.key
  location = "EU"
  project  = each.value
}
resource "google_storage_bucket" "data" {
  name     = "data"
  location = "EU"
  project  = data.google_project.x.project_id
}
data "google_project" "x" {}
`},
			want: []string{
				"Not counted, because their project cannot be known before apply",
				"google_storage_bucket.data (its project refers to data, which is known only after apply)",
				"google_storage_bucket.logs (its project is read per instance, and its for_each collection cannot be read before apply)",
			},
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
