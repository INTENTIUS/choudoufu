// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugincache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeProvider(t *testing.T, dir, host, version, name string, mode os.FileMode) {
	t.Helper()
	platform := filepath.Join(dir, host, "hashicorp", "aws", version, runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(platform, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platform, name), []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestPackage(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "registry.terraform.io", "6.59.0", "terraform-provider-aws_v6.59.0_x5", 0o755)
	// A non-executable file under another version is not an install.
	writeProvider(t, dir, "registry.terraform.io", "6.60.0", "terraform-provider-aws_v6.60.0_x5", 0o644)

	for _, tc := range []struct {
		name, dir, host, version string
		want                     bool
	}{
		{"cached", dir, "registry.terraform.io", "6.59.0", true},
		{"other host", dir, "registry.opentofu.org", "6.59.0", false},
		{"other version", dir, "registry.terraform.io", "6.58.0", false},
		{"not executable", dir, "registry.terraform.io", "6.60.0", false},
		{"no cache", "", "registry.terraform.io", "6.59.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := Package(tc.dir, tc.host, "hashicorp", "aws", tc.version); got != tc.want {
				t.Errorf("Package(%s, %s) = %v, want %v", tc.host, tc.version, got, tc.want)
			}
		})
	}
}

func TestFromEnv(t *testing.T) {
	dir := t.TempDir()
	writeProvider(t, dir, "registry.terraform.io", "6.59.0", "terraform-provider-aws_v6.59.0_x5", 0o755)

	t.Setenv(EnvDir, dir)
	if got, ok := FromEnv("registry.terraform.io", "hashicorp", "aws", "6.59.0"); !ok || got != dir {
		t.Errorf("FromEnv = %q, %v; want %q, true", got, ok, dir)
	}
	t.Setenv(EnvDir, "")
	if _, ok := FromEnv("registry.terraform.io", "hashicorp", "aws", "6.59.0"); ok {
		t.Error("FromEnv reported a hit with TF_PLUGIN_CACHE_DIR empty")
	}
}

func TestDefaultHost(t *testing.T) {
	for bin, want := range map[string]string{
		"terraform":                   "registry.terraform.io",
		"/opt/homebrew/bin/terraform": "registry.terraform.io",
		"tofu":                        "registry.opentofu.org",
		"/tmp/x/choudoufu":            "registry.opentofu.org",
	} {
		if got := DefaultHost(bin); got != want {
			t.Errorf("DefaultHost(%q) = %q, want %q", bin, got, want)
		}
	}
}
