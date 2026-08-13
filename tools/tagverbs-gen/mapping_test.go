// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMappedServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mapping.json")
	data := `{
		"rows": [
			{"tf_type": "aws_vpc", "cfn_type": "AWS::EC2::VPC", "via": "name"},
			{"tf_type": "aws_subnet", "cfn_type": "AWS::EC2::Subnet", "via": "name"},
			{"tf_type": "aws_kms_key", "cfn_type": "AWS::KMS::Key", "via": "alias"},
			{"tf_type": "aws_s3_bucket_versioning", "cfn_type": null, "via": "fold", "fold_parent": "AWS::S3::Bucket"},
			{"tf_type": "aws_account_region", "cfn_type": null, "via": "none"}
		]
	}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil { //nolint:gosec // a test fixture
		t.Fatal(err)
	}

	got, err := loadMappedServices(path)
	if err != nil {
		t.Fatalf("loadMappedServices: %v", err)
	}
	want := []string{"EC2", "KMS"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestServiceOf(t *testing.T) {
	cases := map[string]string{
		"AWS::Lambda::Function":                     "Lambda",
		"AWS::EC2::VPC":                             "EC2",
		"AWS::ElasticLoadBalancingV2::LoadBalancer": "ElasticLoadBalancingV2",
	}
	for cfn, want := range cases {
		got, ok := serviceOf(cfn)
		if !ok || got != want {
			t.Errorf("serviceOf(%q) = %q, %v, want %q, true", cfn, got, ok, want)
		}
	}
	if _, ok := serviceOf("not-a-cfn-type"); ok {
		t.Error("serviceOf accepted a malformed CFN type")
	}
}
