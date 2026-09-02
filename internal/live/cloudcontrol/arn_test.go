// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import "testing"

// TestParseARN is table-driven from real ARN shapes (issue #51), one per
// service the identity table's ARN join (internal/live/discovery/tagging.go)
// cares about, covering all three resource-field grammars ParseARN's doc
// comment names.
func TestParseARN(t *testing.T) {
	tests := []struct {
		name   string
		arn    string
		want   ARN
		wantOK bool
	}{
		{
			name: "iam role: type/id",
			arn:  "arn:aws:iam::123456789012:role/deploy",
			want: ARN{Partition: "aws", Service: "iam", Region: "", Account: "123456789012", ResourceType: "role", ResourceID: "deploy"},
		},
		{
			name: "s3 bucket: bare id, no region or account",
			arn:  "arn:aws:s3:::my-estate-bucket",
			want: ARN{Partition: "aws", Service: "s3", Region: "", Account: "", ResourceID: "my-estate-bucket"},
		},
		{
			name: "ec2 instance: type/id",
			arn:  "arn:aws:ec2:us-east-1:123456789012:instance/i-0123456789abcdef0",
			want: ARN{Partition: "aws", Service: "ec2", Region: "us-east-1", Account: "123456789012", ResourceType: "instance", ResourceID: "i-0123456789abcdef0"},
		},
		{
			name: "lambda function: type:id",
			arn:  "arn:aws:lambda:us-east-1:123456789012:function:my-function",
			want: ARN{Partition: "aws", Service: "lambda", Region: "us-east-1", Account: "123456789012", ResourceType: "function", ResourceID: "my-function"},
		},
		{
			name: "sns topic: bare id",
			arn:  "arn:aws:sns:us-east-1:123456789012:alerts",
			want: ARN{Partition: "aws", Service: "sns", Region: "us-east-1", Account: "123456789012", ResourceID: "alerts"},
		},
		{
			name: "dynamodb table: type/id",
			arn:  "arn:aws:dynamodb:us-east-1:123456789012:table/orders",
			want: ARN{Partition: "aws", Service: "dynamodb", Region: "us-east-1", Account: "123456789012", ResourceType: "table", ResourceID: "orders"},
		},
		{
			name: "logs log-group: type:id, id itself starting with /",
			arn:  "arn:aws:logs:us-east-1:123456789012:log-group:/estate/app",
			want: ARN{Partition: "aws", Service: "logs", Region: "us-east-1", Account: "123456789012", ResourceType: "log-group", ResourceID: "/estate/app"},
		},
		{
			name: "elasticloadbalancing target group: type/id, id itself carrying a /",
			arn:  "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/app-tg/6d0ecf831eec9f09",
			want: ARN{Partition: "aws", Service: "elasticloadbalancing", Region: "us-east-1", Account: "123456789012", ResourceType: "targetgroup", ResourceID: "app-tg/6d0ecf831eec9f09"},
		},
		{
			name: "elasticloadbalancing v2 load balancer: type/id, id carrying two /",
			arn:  "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/main/50dc6c495c0c9188",
			want: ARN{Partition: "aws", Service: "elasticloadbalancing", Region: "us-east-1", Account: "123456789012", ResourceType: "loadbalancer", ResourceID: "app/main/50dc6c495c0c9188"},
		},
		{
			name: "elasticloadbalancing classic load balancer: type/id, single-segment id",
			arn:  "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/classic-name",
			want: ARN{Partition: "aws", Service: "elasticloadbalancing", Region: "us-east-1", Account: "123456789012", ResourceType: "loadbalancer", ResourceID: "classic-name"},
		},
		{
			name: "acm certificate: ARN service differs from the CFN service segment",
			arn:  "arn:aws:acm:us-east-1:123456789012:certificate/8f9c1b2e-0000-0000-0000-000000000000",
			want: ARN{Partition: "aws", Service: "acm", Region: "us-east-1", Account: "123456789012", ResourceType: "certificate", ResourceID: "8f9c1b2e-0000-0000-0000-000000000000"},
		},
		{
			name: "step functions state machine: type:id, ARN service differs from the CFN service segment",
			arn:  "arn:aws:states:us-east-1:123456789012:stateMachine:pipeline",
			want: ARN{Partition: "aws", Service: "states", Region: "us-east-1", Account: "123456789012", ResourceType: "stateMachine", ResourceID: "pipeline"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseARN(tt.arn)
			if !ok {
				t.Fatalf("ParseARN(%q) ok = false, want true", tt.arn)
			}
			if got != tt.want {
				t.Errorf("ParseARN(%q) = %+v, want %+v", tt.arn, got, tt.want)
			}
		})
	}
}

// TestParseARNRefusesMalformed pins the "never guess" side: anything short
// of the six-field arn:... shape, or an empty resource field, is refused
// rather than parsed partway.
func TestParseARNRefusesMalformed(t *testing.T) {
	tests := []string{
		"",
		"not an arn at all",
		"arn:aws:s3",                       // too few fields
		"arn:aws:iam::123456789012:",       // empty resource field
		"urn:aws:iam::123456789012:role/x", // wrong leading literal
	}
	for _, s := range tests {
		if _, ok := ParseARN(s); ok {
			t.Errorf("ParseARN(%q) ok = true, want false", s)
		}
	}
}
