// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// namesDataFixture is a small, hand-written names_data.hcl excerpt covering
// every shape parseNamesData must handle: a plain top-level service (ec2)
// with a sub_service (vpc) whose own sdk block is empty (must inherit ec2's
// "EC2"), a service whose AWS id needs the sdk.id fallback to
// names.provider_name_upper (s3), and a service carrying attributes and
// blocks this join has no use for (cli_v2_command, env_var, brand) to prove
// the `Remain hcl.Body` fields keep gohcl from refusing the file over them.
var namesDataFixture = []byte(`
service "ec2" {
  sdk {
    id            = "EC2"
    arn_namespace = "ec2"
  }
  names {
    provider_name_upper = "EC2"
  }
  resource_prefix {
    correct = "aws_ec2_"
  }

  sub_service "vpc" {
    sdk {
      id = ""
    }
    names {
      provider_name_upper = "VPC"
    }
    resource_prefix {
      correct = "aws_vpc_"
    }
  }
}

service "s3" {
  cli_v2_command {
    aws_cli_v2_command           = "s3api"
    aws_cli_v2_command_no_dashes = "s3api"
  }
  sdk {
    id = ""
  }
  names {
    provider_name_upper = "S3"
  }
  env_var {
    tf_aws_env_var = "TF_AWS_S3_ENDPOINT"
  }
  resource_prefix {
    correct = "aws_s3_"
  }
  brand = "AWS"
}
`)

func TestParseNamesDataFixture(t *testing.T) {
	services, err := parseNamesData(namesDataFixture, "fixture.hcl")
	if err != nil {
		t.Fatalf("parseNamesData: %v", err)
	}

	byPrefix := map[string]namesDataService{}
	for _, s := range services {
		byPrefix[s.Prefix] = s
	}

	if len(services) != 3 {
		t.Fatalf("parsed %d families, want 3 (ec2, its vpc sub_service, s3): %+v", len(services), services)
	}
	if got := byPrefix["ec2"].AWSID; got != "EC2" {
		t.Errorf(`ec2's AWSID = %q, want "EC2" (sdk.id)`, got)
	}
	if got := byPrefix["vpc"].AWSID; got != "EC2" {
		t.Errorf(`vpc's AWSID = %q, want "EC2" (inherited from parent ec2, since vpc's own sdk.id is empty)`, got)
	}
	if got := byPrefix["s3"].AWSID; got != "S3" {
		t.Errorf(`s3's AWSID = %q, want "S3" (sdk.id empty, falls back to names.provider_name_upper)`, got)
	}
}

func TestNormalizeServiceID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"EC2", "ec2"},
		{"Application Auto Scaling", "applicationautoscaling"},
		{"S3 Control", "s3control"},
		{"API Gateway", "apigateway"},
	}
	for _, c := range cases {
		if got := normalizeServiceID(c.in); got != c.want {
			t.Errorf("normalizeServiceID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeriveServiceAliases(t *testing.T) {
	services := []namesDataService{
		{Prefix: "ec2", AWSID: "EC2"},
		{Prefix: "vpc", AWSID: "EC2"},
		{Prefix: "s3", AWSID: "S3"},
		{Prefix: "nomatch", AWSID: "Application Cost Profiler"}, // no CFN service normalizes to this
		{Prefix: "noid", AWSID: ""},                             // skipped outright, not even a mismatch
	}
	cfnTypes := []string{"AWS::EC2::Instance", "AWS::S3::Bucket"}

	aliases, mismatches := deriveServiceAliases(services, cfnTypes)

	want := map[string][]string{"ec2": {"EC2"}, "vpc": {"EC2"}, "s3": {"S3"}}
	if len(aliases) != len(want) {
		t.Fatalf("aliases = %v, want %v", aliases, want)
	}
	for k, v := range want {
		if len(aliases[k]) != 1 || aliases[k][0] != v[0] {
			t.Errorf("aliases[%q] = %v, want %v", k, aliases[k], v)
		}
	}

	if len(mismatches) != 1 || mismatches[0].Prefix != "nomatch" {
		t.Errorf("mismatches = %+v, want exactly one entry for prefix %q", mismatches, "nomatch")
	}
}

func TestDeriveServiceAliasesDuplicatePrefix(t *testing.T) {
	// Two families claiming the same prefix (never observed against the
	// pinned file, but not assumed impossible - see deriveServiceAliases'
	// own doc comment): both must come back as mismatches, and neither
	// silently wins.
	services := []namesDataService{
		{Prefix: "dup", AWSID: "EC2"},
		{Prefix: "dup", AWSID: "S3"},
	}
	cfnTypes := []string{"AWS::EC2::Instance", "AWS::S3::Bucket"}

	aliases, mismatches := deriveServiceAliases(services, cfnTypes)
	if len(aliases) != 0 {
		t.Errorf("aliases = %v, want none - a duplicated prefix must never resolve", aliases)
	}
	if len(mismatches) != 2 {
		t.Errorf("mismatches = %+v, want exactly 2 (one per claimant)", mismatches)
	}
}
