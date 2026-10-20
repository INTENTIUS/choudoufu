// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
)

// loadLiveSource parses src as one configuration file and returns its live
// block's record store and the diagnostics.
func loadLiveSource(t *testing.T, src string) (*LiveRecordStore, hcl.Diagnostics) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	mod, diags := testModuleFromDir(dir)
	if mod == nil || mod.Live == nil {
		return nil, diags
	}
	return mod.Live.RecordStore, diags
}

func liveWithStore(body string) string {
	return "terraform {\n  live {\n    estate = \"prod\"\n\n" + body + "\n  }\n}\n"
}

func TestModule_liveRecordStoreGCS(t *testing.T) {
	rs, diags := loadLiveSource(t, liveWithStore(`
    record_store "gcs" {
      bucket                                = "my-records"
      prefix                                = "team/prod"
      credentials                           = "/secrets/sa.json"
      impersonate_service_account           = "records@acme.iam.gserviceaccount.com"
      impersonate_service_account_delegates = ["a@acme.iam.gserviceaccount.com"]
      kms_encryption_key                    = "projects/p/locations/l/keyRings/r/cryptoKeys/k"
      storage_custom_endpoint               = "http://localhost:4443/storage/v1/"
      allow_insecure                        = ["noncurrent_lifecycle"]
    }`))
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics: %s", diags.Error())
	}
	if rs == nil || rs.Type != "gcs" {
		t.Fatalf("record store = %+v, want type gcs", rs)
	}
	if rs.Bucket != "my-records" || !rs.KeyPrefixSet || rs.KeyPrefix != "team/prod" {
		t.Errorf("bucket %q, prefix %q (set %v); want my-records and team/prod from stock's \"prefix\"", rs.Bucket, rs.KeyPrefix, rs.KeyPrefixSet)
	}
	want := LiveRecordStoreGCS{
		Credentials:                        "/secrets/sa.json",
		ImpersonateServiceAccount:          "records@acme.iam.gserviceaccount.com",
		ImpersonateServiceAccountDelegates: []string{"a@acme.iam.gserviceaccount.com"},
		KMSEncryptionKey:                   "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		StorageCustomEndpoint:              "http://localhost:4443/storage/v1/",
	}
	got := rs.GCS
	if got.Credentials != want.Credentials || got.ImpersonateServiceAccount != want.ImpersonateServiceAccount ||
		!slices.Equal(got.ImpersonateServiceAccountDelegates, want.ImpersonateServiceAccountDelegates) ||
		got.KMSEncryptionKey != want.KMSEncryptionKey || got.StorageCustomEndpoint != want.StorageCustomEndpoint {
		t.Errorf("GCS = %+v, want %+v", got, want)
	}
	if !slices.Equal(rs.AllowInsecure, []string{"noncurrent_lifecycle"}) {
		t.Errorf("AllowInsecure = %v", rs.AllowInsecure)
	}
}

func TestModule_liveRecordStoreGCSRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		body, want string
	}{
		"no bucket": {`record_store "gcs" {}`, `record_store "gcs" requires a "bucket" argument`},
		"both prefixes": {`record_store "gcs" {
      bucket     = "b"
      prefix     = "a"
      key_prefix = "a"
    }`, `Set one of them`},
		"prefix in another estate": {`record_store "gcs" {
      bucket = "b"
      prefix = "tofu-records/other"
    }`, `The "prefix" argument was set to "tofu-records/other"`},
		"both encryption keys": {`record_store "gcs" {
      bucket             = "b"
      encryption_key     = "a2V5"
      kms_encryption_key = "projects/p/locations/l/keyRings/r/cryptoKeys/k"
    }`, `not both`},
		"an S3 waiver name": {`record_store "gcs" {
      bucket         = "b"
      allow_insecure = ["versioning"]
    }`, `names "versioning", which is not something record_store "gcs" asserts`},
		"a region": {`record_store "gcs" {
      bucket = "b"
      region = "us-east1"
    }`, `has no meaning for record_store "gcs"`},
		"a kubernetes argument": {`record_store "gcs" {
      bucket    = "b"
      namespace = "records"
    }`, `has no meaning for record_store "gcs"`},
		"a path": {`record_store "gcs" {
      bucket = "b"
      path   = ".records"
    }`, `has no meaning for record_store "gcs"`},
		"reads_outputs_of": {`record_store "gcs" {
      bucket = "b"
      reads_outputs_of "network" {}
    }`, `tofu-outputs/ prefix in the bucket's IAM policy`},
		"a gcs argument on s3": {`record_store "s3" {
      bucket      = "b"
      credentials = "/secrets/sa.json"
    }`, `"credentials" argument is one of stock's gcs backend arguments`},
		"prefix on s3": {`record_store "s3" {
      bucket = "b"
      prefix = "a"
    }`, `record_store "s3" spells it "key_prefix"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, diags := loadLiveSource(t, liveWithStore("    "+tc.body))
			if !diags.HasErrors() {
				t.Fatal("the configuration loaded with no errors")
			}
			if !strings.Contains(diags.Error(), tc.want) {
				t.Errorf("wrong diagnostic, want %q:\n%s", tc.want, diags.Error())
			}
		})
	}
}
