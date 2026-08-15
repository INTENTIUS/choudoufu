// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import "testing"

// TestValueNamesRequiredArgument pins the shapes that rewrote rule 3's
// matching (issue #132). Every case is a real page: the three the old
// substring containment got right must keep resolving, and the five it got
// wrong - an argument's token floating inside the resource's own id prefix
// or a placeholder describing the resource's own identifier - must refuse.
func TestValueNamesRequiredArgument(t *testing.T) {
	cases := []struct {
		name     string
		example  string
		tfType   string
		required []string
		want     string
		wantOK   bool
	}{
		{"id value whose prefix is the argument's token, aws_vpc_dhcp_options_association",
			"vpc-0f001273ec18911b1", "aws_vpc_dhcp_options_association", []string{"vpc_id", "dhcp_options_id"}, "vpc_id", true},
		{"placeholder describing the argument, aws_codebuild_fleet",
			"fleet-name", "aws_codebuild_fleet", []string{"name"}, "name", true},
		{"placeholder describing the argument, aws_codebuild_project",
			"project-name", "aws_codebuild_project", []string{"name"}, "name", true},
		{"the resource's own id prefix embedding a token, aws_vpc_endpoint",
			"vpce-3ecf2a57", "aws_vpc_endpoint", []string{"vpc_id", "service_name"}, "", false},
		{"a token inside a longer own prefix, aws_ec2_local_gateway_route_table_vpc_association",
			"lgw-vpc-assoc-1234567890abcdef", "aws_ec2_local_gateway_route_table_vpc_association", []string{"local_gateway_route_table_id", "vpc_id"}, "", false},
		{"a token inside a longer own prefix, aws_vpc_encryption_control",
			"vpcec-12345678901234567", "aws_vpc_encryption_control", []string{"vpc_id"}, "", false},
		{"a token inside a longer own prefix, aws_vpc_ipam_resource_discovery_association",
			"ipam-res-disco-assoc-0178368ad2146a492", "aws_vpc_ipam_resource_discovery_association", []string{"ipam_id", "ipam_resource_discovery_id"}, "", false},
		{"placeholder describing the resource's own identifier, aws_mailmanager_rule_set",
			"rule-set-id", "aws_mailmanager_rule_set", []string{"rule_set_name"}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := valueNamesRequiredArgument(tc.example, tc.tfType, tc.required)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("valueNamesRequiredArgument(%q) = (%q, %v), want (%q, %v)", tc.example, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// TestIdentitySchemaOrder is issue #134's guard: the third fallback in
// tryGrammarComposite, which takes the provider's own Identity Schema as the
// segment order when the value-token bijection is ambiguous.
//
// The refusals matter more than the acceptances here. Arity alone fits a lot
// of things, and the whole reason this rule is safe is that it also demands
// the example corroborate the order.
func TestIdentitySchemaOrder(t *testing.T) {
	sep := func(s string) *string { return &s }
	cases := []struct {
		name    string
		row     importGrammarRow
		want    []string
		wantOK  bool
		because string
	}{
		{
			name: "ambiguous by value token, settled by the schema's order",
			row: importGrammarRow{
				ImportIDExample:        "role_of_mypolicy_name:mypolicy_name",
				Separator:              sep(":"),
				Arguments:              []string{"name", "role"},
				IdentitySchemaRequired: []string{"role", "name"},
			},
			want:    []string{"role", "name"},
			wantOK:  true,
			because: "both segments contain \"name\", so the bijection declines; the schema says role first and the first segment says \"role\"",
		},
		{
			name: "arity fits but the order is contradicted",
			row: importGrammarRow{
				// The documented example leads with a principal id while the
				// schema leads with instance_arn - aws_ssoadmin_account_assignment.
				ImportIDExample:        "principal-1234,GROUP,target-1,AWS_ACCOUNT,permission-set-arn,instance-arn",
				Separator:              sep(","),
				Arguments:              []string{"instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"},
				IdentitySchemaRequired: []string{"instance_arn", "permission_set_arn", "principal_id", "principal_type", "target_id", "target_type"},
			},
			wantOK:  false,
			because: "segment 0 names principal, not instance - the order fits by length and by nothing else",
		},
		{
			name: "nothing in the example corroborates the order",
			row: importGrammarRow{
				// aws_opensearchserverless_access_policy: real values that
				// echo neither argument name.
				ImportIDExample:        "example/data",
				Separator:              sep("/"),
				Arguments:              []string{"name", "type"},
				IdentitySchemaRequired: []string{"name", "type"},
			},
			wantOK:  false,
			because: "an uncorroborated order is a coincidence of length, even when it happens to be right",
		},
		{
			name: "the schema describes a different identity",
			row: importGrammarRow{
				ImportIDExample:        "cluster-1:addon-1",
				Separator:              sep(":"),
				Arguments:              []string{"cluster_name", "addon_name"},
				IdentitySchemaRequired: []string{"cluster_name", "region"},
			},
			wantOK:  false,
			because: "the schema's set is not the argument set, so it is not a reordering of this identity",
		},
		{
			name: "arity mismatch",
			row: importGrammarRow{
				ImportIDExample:        "a:b:c",
				Separator:              sep(":"),
				Arguments:              []string{"cluster_name", "addon_name"},
				IdentitySchemaRequired: []string{"cluster_name", "addon_name"},
			},
			wantOK:  false,
			because: "three segments against two attributes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := identitySchemaOrder(tc.row)
			if ok != tc.wantOK {
				t.Fatalf("identitySchemaOrder ok = %v, want %v (%s)", ok, tc.wantOK, tc.because)
			}
			if !tc.wantOK {
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("order = %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}
