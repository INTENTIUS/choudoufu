// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// GitHub issue #2293: a live block with no record_store takes its store from
// the backend block beside it, s3 to s3, gcs to gcs, azurerm to azurerm.
func TestLiveRecordStoreDerivedFromBackend(t *testing.T) {
	for name, tc := range map[string]struct {
		backend    string
		wantType   string
		wantBucket string
		check      func(t *testing.T, rs *LiveRecordStore)
	}{
		"s3": {
			backend: `backend "s3" {
    bucket = "acme-tfstate"
    key    = "prod/network/terraform.tfstate"
    region = "eu-west-1"
  }`,
			wantType:   "s3",
			wantBucket: "acme-tfstate",
			check: func(t *testing.T, rs *LiveRecordStore) {
				if rs.Region != "eu-west-1" || !rs.RegionSet {
					t.Errorf("region = %q, want eu-west-1", rs.Region)
				}
			},
		},
		"gcs": {
			backend: `backend "gcs" {
    bucket = "acme-state"
    prefix = "prod/network"
  }`,
			wantType:   "gcs",
			wantBucket: "acme-state",
		},
		"azurerm": {
			backend: `backend "azurerm" {
    resource_group_name  = "rg"
    storage_account_name = "acmestate"
    container_name       = "tfstate"
    key                  = "prod.terraform.tfstate"
  }`,
			wantType: "azurerm",
			check: func(t *testing.T, rs *LiveRecordStore) {
				if rs.AzureRM.StorageAccountName != "acmestate" || rs.AzureRM.ContainerName != "tfstate" || rs.AzureRM.ResourceGroupName != "rg" {
					t.Errorf("azurerm = %+v", rs.AzureRM)
				}
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			p := testParser(map[string]string{
				"mod/main.tf": "terraform {\n  live {\n    estate = \"unit\"\n  }\n  " + tc.backend + "\n}\n",
			})
			mod, diags := p.LoadConfigDir("mod")
			if diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Error())
			}
			rs := mod.Live.RecordStore
			if rs == nil || !rs.Derived || rs.Implied {
				t.Fatalf("record store not derived: %+v", rs)
			}
			if rs.Type != tc.wantType || rs.Bucket != tc.wantBucket {
				t.Errorf("store = %s %q, want %s %q", rs.Type, rs.Bucket, tc.wantType, tc.wantBucket)
			}
			if !strings.HasSuffix(rs.DeclRange.Filename, "main.tf") {
				t.Errorf("DeclRange = %v, want the backend block", rs.DeclRange)
			}
			if tc.check != nil {
				tc.check(t, rs)
			}
		})
	}
}

// An explicit record_store wins over the backend block.
func TestLiveRecordStoreExplicitWinsOverBackend(t *testing.T) {
	p := testParser(map[string]string{
		"mod/main.tf": `
terraform {
  live {
    estate = "unit"
    record_store "local" {}
  }
  backend "s3" {
    bucket = "acme-tfstate"
    key    = "terraform.tfstate"
  }
}
`,
	})
	mod, diags := p.LoadConfigDir("mod")
	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %s", diags.Error())
	}
	if rs := mod.Live.RecordStore; rs.Type != "local" || rs.Derived || rs.Implied {
		t.Errorf("store = %+v, want the declared local one", rs)
	}
}

// A backend with no record store counterpart, or one whose bucket comes from
// -backend-config, leaves the implied local store and says why.
func TestLiveRecordStoreNotDerivedSaysWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		backend string
		want    string
	}{
		"consul":        {`backend "consul" {}`, "no record store counterpart"},
		"partial s3":    {`backend "s3" {}`, "no literal bucket"},
		"azurerm no rg": {`backend "azurerm" {` + "\n" + `storage_account_name = "a"` + "\n" + `container_name = "c"` + "\n" + `}`, "resource_group_name"},
	} {
		t.Run(name, func(t *testing.T) {
			p := testParser(map[string]string{
				"mod/main.tf": "terraform {\n  live {\n    estate = \"unit\"\n  }\n  " + tc.backend + "\n}\n",
			})
			mod, diags := p.LoadConfigDir("mod")
			if diags.HasErrors() {
				t.Fatalf("unexpected errors: %s", diags.Error())
			}
			if rs := mod.Live.RecordStore; !rs.Implied || rs.Derived {
				t.Errorf("store = %+v, want the implied local one", rs)
			}
			if !strings.Contains(mod.Live.RecordStoreNote, tc.want) {
				t.Errorf("note = %q, want it to mention %q", mod.Live.RecordStoreNote, tc.want)
			}
		})
	}
}

func TestEstateFromStateKey(t *testing.T) {
	for key, want := range map[string]string{
		"prod/network/terraform.tfstate": "prod-network",
		"prod/network":                   "prod-network",
		"terraform.tfstate":              "",
		"Prod_Network/app.tfstate":       "prod-network",
		"/envs//dev/":                    "envs-dev",
		"1-prod/terraform.tfstate":       "",
	} {
		if got := EstateFromStateKey(key); got != want {
			t.Errorf("EstateFromStateKey(%q) = %q, want %q", key, got, want)
		}
	}
}

// The root path is taken from the repository top, and a Terragrunt cache
// tail is stripped so a unit gets its own directory's name.
func TestEstateFromRootPath(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(repo, "live", "prod", "vpc")
	cache := filepath.Join(unit, ".terragrunt-cache", "abc", "def")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := EstateFromRootPath(unit); got != "live-prod-vpc" {
		t.Errorf("unit = %q, want live-prod-vpc", got)
	}
	if got := EstateFromRootPath(cache); got != "live-prod-vpc" {
		t.Errorf("cache = %q, want live-prod-vpc", got)
	}
}
