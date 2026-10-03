// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// staleModuleRoot writes a root calling module "m" from source, with a
// .terraform/modules manifest recording an earlier install of it from
// installedSource at installedVersion (empty for an unversioned source), and
// the installed package itself declaring one S3 bucket.
func staleModuleRoot(t *testing.T, callArgs, installedSource, installedVersion string) string {
	t.Helper()
	dir := t.TempDir()
	root := `terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

module "m" {
` + callArgs + `
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(root), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(dir, ".terraform", "modules", "m")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "main.tf"), []byte(`resource "aws_s3_bucket" "b" {
  bucket = "installed-bucket"
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := map[string]string{"Key": "m", "Source": installedSource, "Dir": ".terraform/modules/m"}
	if installedVersion != "" {
		rec["Version"] = installedVersion
	}
	b, err := json.Marshal(map[string][]map[string]string{"Modules": {{"Key": "", "Source": "", "Dir": "."}, rec}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".terraform", "modules", "modules.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestLoadRefusesAModuleInstalledFromAnotherSource is the #1750 finding: a
// pin bump (oci://...?tag=1.0.0 -> ?tag=1.1.0) that has not been re-inited
// leaves the old package in .terraform/modules. Stock's loader refuses that
// ("Module source has changed", configload/loader_load.go); this one read the
// stale package as the module the configuration names and reported its
// resources as resolved - the previous version's estate presented as the
// current one, with nothing in the report to say so.
func TestLoadRefusesAModuleInstalledFromAnotherSource(t *testing.T) {
	dir := staleModuleRoot(t,
		`  source = "oci://registry.example/largeset/shared?tag=1.1.0"`,
		"oci://registry.example/largeset/shared?tag=1.0.0", "")
	r := Dir(t.Context(), dir, Context{})
	if len(r.Load.UnresolvedModules) != 1 {
		t.Fatalf("a module installed from tag=1.0.0 and called at tag=1.1.0 was read as current: %d instance(s) resolved, unresolved modules %+v",
			r.Instances, r.Load.UnresolvedModules)
	}
	u := r.Load.UnresolvedModules[0]
	if u.Source != "oci://registry.example/largeset/shared?tag=1.1.0" || !strings.Contains(u.Reason, "tag=1.0.0") {
		t.Errorf("the unread module should name the configured source and the installed one, got %+v", u)
	}
	if r.Instances != 0 {
		t.Errorf("%d instance(s) resolved from the stale package", r.Instances)
	}
}

// TestLoadRefusesAnInstalledVersionTheConstraintExcludes: the same staleness
// for a registry module whose version argument moved.
func TestLoadRefusesAnInstalledVersionTheConstraintExcludes(t *testing.T) {
	dir := staleModuleRoot(t,
		"  source  = \"example/shared/aws\"\n  version = \"1.1.0\"",
		"example/shared/aws", "1.0.0")
	r := Dir(t.Context(), dir, Context{})
	if len(r.Load.UnresolvedModules) != 1 || !strings.Contains(r.Load.UnresolvedModules[0].Reason, "1.0.0") {
		t.Fatalf("an installed 1.0.0 called with version = 1.1.0 was read as current: %d instance(s), unresolved %+v",
			r.Instances, r.Load.UnresolvedModules)
	}
}

// TestLoadReadsAnInstalledModuleThatMatches is the control: the two tests
// above would pass against a loader that never reads an installed module.
func TestLoadReadsAnInstalledModuleThatMatches(t *testing.T) {
	for name, tc := range map[string]struct{ call, src, ver string }{
		"oci":      {`  source = "oci://registry.example/largeset/shared?tag=1.1.0"`, "oci://registry.example/largeset/shared?tag=1.1.0", ""},
		"registry": {"  source  = \"example/shared/aws\"\n  version = \"~> 1.0\"", "example/shared/aws", "1.0.0"},
	} {
		dir := staleModuleRoot(t, tc.call, tc.src, tc.ver)
		r := Dir(t.Context(), dir, Context{})
		if len(r.Load.UnresolvedModules) != 0 || r.Instances != 1 {
			t.Errorf("%s: an installed module matching its call was not read: %d instance(s), unresolved %+v", name, r.Instances, r.Load.UnresolvedModules)
		}
	}
}
