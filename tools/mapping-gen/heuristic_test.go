// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"testing"
)

func TestPascalToSnake(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Bucket", "bucket"},
		{"VPC", "vpc"},
		{"DBInstance", "db_instance"},
		{"DBParameterGroup", "db_parameter_group"},
		{"TargetGroup", "target_group"},
		{"NatGateway", "nat_gateway"},
		{"BucketPolicy", "bucket_policy"},
		{"AutoScalingGroup", "auto_scaling_group"},
		{"S3", "s3"},
	}
	for _, c := range cases {
		if got := pascalToSnake(c.in); got != c.want {
			t.Errorf("pascalToSnake(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNameCandidates(t *testing.T) {
	cases := []struct {
		cfnType string
		want    []string
	}{
		// Service casing agrees, so there is exactly one candidate.
		{"AWS::S3::Bucket", []string{"aws_s3_bucket"}},
		{"AWS::S3::BucketPolicy", []string{"aws_s3_bucket_policy"}},
		{"AWS::IAM::Role", []string{"aws_iam_role"}},
		// CloudFront: the snake conversion sees a word boundary TF's own
		// name does not, so the plain-lowercase candidate is the one that
		// actually matches the real TF type.
		{"AWS::CloudFront::Distribution", []string{"aws_cloud_front_distribution", "aws_cloudfront_distribution"}},
		// Not a well-formed AWS::Service::Resource string.
		{"AWS::Bad", nil},
	}
	for _, c := range cases {
		got := nameCandidates(c.cfnType)
		if !equalSlices(got, c.want) {
			t.Errorf("nameCandidates(%q) = %v, want %v", c.cfnType, got, c.want)
		}
	}
}

func TestBuildNameIndexAgainstFixture(t *testing.T) {
	roster, err := loadStaticRoster(filepath.Join("testdata", "cfn-types.json"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := buildNameIndex(roster)
	if err != nil {
		t.Fatalf("buildNameIndex: %v", err)
	}

	wantHits := map[string]string{
		"aws_s3_bucket":               "AWS::S3::Bucket",
		"aws_s3_bucket_policy":        "AWS::S3::BucketPolicy",
		"aws_iam_role":                "AWS::IAM::Role",
		"aws_iam_role_policy":         "AWS::IAM::RolePolicy",
		"aws_iam_policy":              "AWS::IAM::Policy",
		"aws_ecs_cluster":             "AWS::ECS::Cluster",
		"aws_ecr_repository":          "AWS::ECR::Repository",
		"aws_cloudfront_distribution": "AWS::CloudFront::Distribution",
		"aws_dynamodb_table":          "AWS::DynamoDB::Table",
		"aws_kms_key":                 "AWS::KMS::Key",
		"aws_kms_alias":               "AWS::KMS::Alias",
		"aws_lambda_function":         "AWS::Lambda::Function",
		"aws_sns_topic":               "AWS::SNS::Topic",
		"aws_sqs_queue":               "AWS::SQS::Queue",
		"aws_ssm_parameter":           "AWS::SSM::Parameter",
		"aws_ssm_document":            "AWS::SSM::Document",
		"aws_secretsmanager_secret":   "AWS::SecretsManager::Secret",
	}
	for tf, wantCFN := range wantHits {
		gotCFN, ok := index[tf]
		if !ok {
			t.Errorf("index[%q] missing, want %q", tf, wantCFN)
			continue
		}
		if gotCFN != wantCFN {
			t.Errorf("index[%q] = %q, want %q", tf, gotCFN, wantCFN)
		}
	}

	// These are exactly the curated-68 alias cases: the fixture carries
	// their CFN type, but the real TF name does not match either casing
	// candidate, so no heuristic hit should appear for them.
	wantMisses := []string{
		"aws_autoscaling_group",       // AutoScaling/AutoScalingGroup -> doubled, not TF's autoscaling_group
		"aws_cloudwatch_event_rule",   // Events/Rule -> aws_events_rule, not this
		"aws_cloudwatch_metric_alarm", // CloudWatch/Alarm -> aws_cloudwatch_alarm, missing "metric_"
		"aws_db_instance",             // RDS/DBInstance -> aws_rds_db_instance, not aws_db_instance
		"aws_ebs_volume",              // EC2/Volume -> aws_ec2_volume, not aws_ebs_volume
		"aws_eip",                     // EC2/EIP -> aws_ec2_eip, not aws_eip
		"aws_vpc",                     // EC2/VPC -> aws_ec2_vpc, not aws_vpc
		"aws_acm_certificate",         // CertificateManager/Certificate -> aws_certificatemanager_certificate
	}
	for _, tf := range wantMisses {
		if cfn, ok := index[tf]; ok {
			t.Errorf("index[%q] = %q, want no heuristic hit (this is one of the curated alias cases)", tf, cfn)
		}
	}
}

func TestBuildNameIndexCollision(t *testing.T) {
	// Two different CFN types whose candidates coincide: EC2/FooBar
	// (snake and lower both "ec2") and a service literally named "Ec2"
	// (snake and lower also both "ec2") both producing "aws_ec2_foo_bar".
	// buildNameIndex must refuse to silently pick one.
	roster := staticRoster{"AWS::EC2::FooBar", "AWS::Ec2::FooBar"}
	_, err := buildNameIndex(roster)
	if err == nil {
		t.Fatal("buildNameIndex: want an error for a candidate two CFN types both produce, got nil")
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
