// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"testing"
)

// TestStatelessDiscoveryProviderIgnoresRecordBacked pins a bug a real
// end-to-end run against live/e2e/record-store/ caught: with nothing
// waiting on marker discovery, statelessDiscoveryProvider's fallback used
// to treat every managed resource in the whole configuration - including
// GitHub issue #73's record-backed types (null_resource, terraform_data,
// time_*, non-sensitive random_*) - as a candidate for "which provider
// does the undeclared-resource sweep run through", which meant an
// otherwise ordinary single-provider (aws) estate that also declared a
// null_resource refused outright with "Marker discovery across several
// provider configurations", even though a record-backed resource has no
// marker and was never going to be swept for at all.
func TestStatelessDiscoveryProviderIgnoresRecordBacked(t *testing.T) {
	dir := t.TempDir()
	const src = `
resource "aws_s3_bucket" "data" {
  bucket = "my-bucket"
}

resource "null_resource" "trigger" {
  triggers = {
    input = "value"
  }
}

resource "terraform_data" "replacement" {
  input = "value"
}

resource "time_static" "created" {}

resource "random_pet" "name" {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := statelessTestLoadConfig(t, dir)

	// No needs-discovery resolutions: the same shape a run with nothing
	// waiting on marker discovery has, which is what drives
	// statelessDiscoveryProvider into its whole-configuration fallback.
	addr, diags := statelessDiscoveryProvider(cfg, nil)
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics (record-backed types should never be counted here):\n%s", diags.Err())
	}
	if addr.Provider.Type != "aws" {
		t.Errorf("provider = %s, want the aws provider alone (null/terraform/time/random must be excluded)", addr)
	}
}
