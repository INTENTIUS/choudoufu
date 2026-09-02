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

// TestStatelessManagedResourceProvidersIgnoresRecordBacked pins a bug a
// real end-to-end run against live/e2e/record-store/ caught: the
// estate-wide sweep's provider candidate set used to treat every managed
// resource in the whole configuration - including GitHub issue #73's
// record-backed types (null_resource, terraform_data, time_*,
// non-sensitive random_*) - as a candidate for "which providers does the
// undeclared-resource sweep run through", which meant an otherwise
// ordinary single-provider (aws) estate that also declared a null_resource
// either refused outright (before issue #69's multi-provider sweep
// support) or, after it, ran a pointless sweep attempt through
// null/terraform/time/random - providers with no listable, taggable
// resources and therefore nothing a marker sweep could ever find - even
// though a record-backed resource has no marker and was never going to be
// swept for at all.
func TestStatelessManagedResourceProvidersIgnoresRecordBacked(t *testing.T) {
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

	providers := statelessManagedResourceProviders(cfg)
	if len(providers) != 1 {
		t.Fatalf("providers = %v, want exactly one (aws) - null/terraform/time/random must be excluded", providers)
	}
	if providers[0].Provider.Type != "aws" {
		t.Errorf("provider = %s, want aws", providers[0])
	}
}
