// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Ground-truth tests over tools/importdocs-gen/testdata/docs: real
// website/docs/r/*.html.markdown files fetched from
// hashicorp/terraform-provider-aws at the pinned v6.58.0 tag and committed
// verbatim, so this package's parser is graded against the provider's
// actual prose - not a hand-shaped fixture that happens to match whatever
// the code does today. Every claim here was checked against the fetched
// doc before being written down (issue's own instruction: "verify these
// claims against the actual fetched docs first").
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func readTestdataDoc(t *testing.T, slug string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "docs", slug+".html.markdown")) //nolint:gosec // a committed test fixture
	if err != nil {
		t.Fatalf("reading testdata doc %s: %v", slug, err)
	}
	return string(data)
}

func mustRow(t *testing.T, tfType, slug string) Row {
	t.Helper()
	doc := readTestdataDoc(t, slug)
	row, ok := buildRow(tfType, doc)
	if !ok {
		t.Fatalf("%s: buildRow returned ok=false", tfType)
	}
	return row
}

func wantComposed(t *testing.T, row Row, want bool) {
	t.Helper()
	if row.ComposedOfArguments == nil {
		t.Fatalf("%s: ComposedOfArguments = nil, want %v", row.TFType, want)
	}
	if *row.ComposedOfArguments != want {
		t.Errorf("%s: ComposedOfArguments = %v, want %v", row.TFType, *row.ComposedOfArguments, want)
	}
}

func wantSeparator(t *testing.T, row Row, want string) {
	t.Helper()
	if row.Separator == nil {
		t.Fatalf("%s: Separator = nil, want %q", row.TFType, want)
	}
	if *row.Separator != want {
		t.Errorf("%s: Separator = %q, want %q", row.TFType, *row.Separator, want)
	}
}

// TestGroundTruth_IAMRolePolicyAttachment: separator "/", composed true
// (role name and policy ARN, both real config arguments - the doc's own
// Identity Schema names them, and its legacy prose states the separator
// directly: "using the role name and policy arn separated by `/`").
func TestGroundTruth_IAMRolePolicyAttachment(t *testing.T) {
	row := mustRow(t, "aws_iam_role_policy_attachment", "iam_role_policy_attachment")
	wantComposed(t, row, true)
	wantSeparator(t, row, "/")
	if row.ImportIDExample != "test-role/arn:aws:iam::xxxxxxxxxxxx:policy/test-policy" {
		t.Errorf("ImportIDExample = %q", row.ImportIDExample)
	}
}

// TestGroundTruth_Route53Record: separator "_", composed true (hosted
// zone ID + record name + record type - the Identity Schema's Required
// list, all three real Argument Reference names; the doc states the
// separator directly: "separated by underscores (`_`)").
func TestGroundTruth_Route53Record(t *testing.T) {
	row := mustRow(t, "aws_route53_record", "route53_record")
	wantComposed(t, row, true)
	wantSeparator(t, row, "_")
}

// TestGroundTruth_LambdaAlias: composed true - the pilot's rejection
// (the ruling is in tools/row-gen/rejected.json), now automated.
// aws_lambda_alias carries no Identity Schema in v6.58.0; the doc's only
// signal is "using `function_name/alias`", a two-segment format token.
func TestGroundTruth_LambdaAlias(t *testing.T) {
	row := mustRow(t, "aws_lambda_alias", "lambda_alias")
	wantComposed(t, row, true)
	wantSeparator(t, row, "/")
}

// TestGroundTruth_LambdaFunction: composed true, single argument
// (function_name) - the Identity Schema's lone Required attribute, which
// matches a real Argument Reference name. No separator: a single-segment
// ID has nothing to join.
func TestGroundTruth_LambdaFunction(t *testing.T) {
	row := mustRow(t, "aws_lambda_function", "lambda_function")
	wantComposed(t, row, true)
	if row.Separator != nil {
		t.Errorf("Separator = %q, want nil (single-argument ID)", *row.Separator)
	}
	if len(row.Arguments) != 1 || row.Arguments[0] != "function_name" {
		t.Errorf("Arguments = %v, want [function_name]", row.Arguments)
	}
}

// TestGroundTruth_KMSKey: composed false - the Identity Schema's Required
// `id` matches nothing in Argument Reference (a KMS key's ID is minted by
// AWS, never a configuration argument), the opaque-server-assigned shape.
func TestGroundTruth_KMSKey(t *testing.T) {
	row := mustRow(t, "aws_kms_key", "kms_key")
	wantComposed(t, row, false)
	if row.Separator != nil {
		t.Errorf("Separator = %q, want nil (opaque ID)", *row.Separator)
	}
}

// TestGroundTruth_Route: separator "_", composed true. aws_route's
// Identity Schema Required list is just route_table_id (the destination is
// one of three Optional alternatives), so the schema alone would miss the
// separator entirely; the doc's "using `ROUTETABLEID_DESTINATION`" format
// token is what actually carries it.
func TestGroundTruth_Route(t *testing.T) {
	row := mustRow(t, "aws_route", "route")
	wantComposed(t, row, true)
	wantSeparator(t, row, "_")
}

// TestGroundTruth_LambdaLayerVersionPermission: the pilot's other
// rejection (table.go: "layer-arn,version-number" - a composite the
// configuration already supplies, not the registry's opaque Id"). No
// Identity Schema in v6.58.0; the doc names both arguments directly in its
// "using `layer_name` and `version_number`, separated by a comma (`,`)"
// sentence.
func TestGroundTruth_LambdaLayerVersionPermission(t *testing.T) {
	row := mustRow(t, "aws_lambda_layer_version_permission", "lambda_layer_version_permission")
	wantComposed(t, row, true)
	wantSeparator(t, row, ",")
	want := []string{"layer_name", "version_number"}
	if !equalStrings(row.Arguments, want) {
		t.Errorf("Arguments = %v, want %v", row.Arguments, want)
	}
}

// TestGroundTruth_VPCSecurityGroupIngressRule: composed false. A single
// Identity Schema Required attribute, `id`, that names the security group
// rule's own AWS-minted ID rather than any configuration argument - the
// same opaque shape as aws_kms_key, and one of table.go's 17 hand-written
// server-assigned types, so the row-gen demotion hook must NOT touch it.
func TestGroundTruth_VPCSecurityGroupIngressRule(t *testing.T) {
	row := mustRow(t, "aws_vpc_security_group_ingress_rule", "vpc_security_group_ingress_rule")
	wantComposed(t, row, false)
}

// TestGroundTruth_LBTargetGroupAttachment: composed true, separator ",".
// Both an Identity Schema (Required: target_group_arn, target_id) and a
// "using `target_group_arn`, `target_id`, ... separated by commas (`,`)"
// sentence naming all four possible components directly - the two signal
// families agreeing, which is what the richest docs look like.
func TestGroundTruth_LBTargetGroupAttachment(t *testing.T) {
	row := mustRow(t, "aws_lb_target_group_attachment", "lb_target_group_attachment")
	wantComposed(t, row, true)
	wantSeparator(t, row, ",")
	for _, want := range []string{"target_group_arn", "target_id"} {
		found := false
		for _, a := range row.Arguments {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("Arguments = %v, want it to include %q", row.Arguments, want)
		}
	}
}

// TestGroundTruth_ACMCertificate: composed false. Caught a real parser bug
// during the full sweep: this doc's Identity Schema uses "-" as its bullet
// marker ("- `arn` (String) ARN of the certificate."), not the "*" every
// other sampled doc uses, and bulletNameRe originally only matched "*" -
// silently reading the section as empty and misclassifying every such type
// as unsure rather than opaque. `arn` matches nothing in Argument
// Reference (it is never a settable argument), so this is opaque, the
// same shape as aws_kms_key.
func TestGroundTruth_ACMCertificate(t *testing.T) {
	row := mustRow(t, "aws_acm_certificate", "acm_certificate")
	wantComposed(t, row, false)
	if row.Separator != nil {
		t.Errorf("Separator = %q, want nil (opaque ID)", *row.Separator)
	}
}

// TestGroundTruth_ECSCluster: composed nil (unsure). No Identity Schema in
// v6.58.0, and the legacy prose ("using the cluster name") names its
// argument in plain English with no backticks and no format token - this
// tool has no source to resolve it from, so it must say so rather than
// guess. Documents the honest gap, not a claim from the issue.
func TestGroundTruth_ECSCluster(t *testing.T) {
	row := mustRow(t, "aws_ecs_cluster", "ecs_cluster")
	if row.ComposedOfArguments != nil {
		t.Errorf("ComposedOfArguments = %v, want nil (no resolvable signal in this doc)", *row.ComposedOfArguments)
	}
	if row.Separator != nil {
		t.Errorf("Separator = %v, want nil", *row.Separator)
	}
}

// The cases below are issue #135's. Each was a row whose separator sat
// unscraped in its own documentation, and each is graded against the real
// page rather than a fixture shaped to match the code.

// TestGroundTruth_CognitoIdentityProvider: separator ":", from the example
// alone. The page names its parts in prose ("using their User Pool ID and
// Provider Name") and states no separator, so nothing but the example
// 'us-west-2_abc123:CorpAD' decides. splitSegments declined it because
// segmentRe requires a leading letter and this token's own shape defeated
// that, not because the character was ambiguous.
func TestGroundTruth_CognitoIdentityProvider(t *testing.T) {
	row := mustRow(t, "aws_cognito_identity_provider", "cognito_identity_provider")
	wantSeparator(t, row, ":")
}

// TestGroundTruth_BedrockAgentAlias: separator ",", stated in prose.
//
// The page says "using the alias ID and the agent ID separated by `,`" -
// it names the character in backticks and the parts in plain words. The
// separator was found all along and then discarded, because classifyGrammar
// recorded one only once the ID was known to be composed, and no argument
// had matched. Two different facts, folded together, losing the stronger.
func TestGroundTruth_BedrockAgentAlias(t *testing.T) {
	row := mustRow(t, "aws_bedrockagent_agent_alias", "bedrockagent_agent_alias")
	wantSeparator(t, row, ",")
}

// TestGroundTruth_APIGatewayBasePathMapping: separator "/", with a
// documented trailing separator.
//
// The example is 'example.com/' and the page explains it: "For an empty
// base_path or, in other words, a root path". A rule requiring non-empty
// segments would refuse the one example whose shape the doc goes out of its
// way to describe.
func TestGroundTruth_APIGatewayBasePathMapping(t *testing.T) {
	row := mustRow(t, "aws_api_gateway_base_path_mapping", "api_gateway_base_path_mapping")
	wantSeparator(t, row, "/")
}

// TestGroundTruth_EC2TransitGatewayRoute: separator "_", and the case that
// proves prose has to outrank the example.
//
// The example is 'tgw-rtb-12345678_0.0.0.0/0'. Its only non-underscore
// candidate is the '/' inside the CIDR, so a rule reading the example alone
// answers "/" - confidently, and wrongly. The page says "using the EC2
// Transit Gateway Route Table, an underscore, and the destination".
func TestGroundTruth_EC2TransitGatewayRoute(t *testing.T) {
	row := mustRow(t, "aws_ec2_transit_gateway_route", "ec2_transit_gateway_route")
	wantSeparator(t, row, "_")
}

// TestGroundTruth_GuardDutyFilter: separator ":", from an example whose
// segments are a hex detector ID and a name.
func TestGroundTruth_GuardDutyFilter(t *testing.T) {
	row := mustRow(t, "aws_guardduty_filter", "guardduty_filter")
	wantSeparator(t, row, ":")
}

// TestGroundTruth_CognitoResourceServer records a row that stays
// unresolved, on purpose.
//
// Issue #135 expected "|" here, and the character is right - the page's
// example is 'us-west-2_abc123|https://example.com'. Nothing in the
// evidence says so. The prose names the parts ("using their User Pool ID
// and Identifier") without naming a separator, and in the example the ':'
// of the URL scheme is as good a candidate as the '|', with '/' present
// too. The issue proposed identity_schema_required's arity as the
// tiebreak; this type carries none.
//
// So this asserts nil. Writing "|" here would be reading the answer off the
// issue rather than off the page, and the next type with a URL in its ID
// would get whatever the rule guessed. It is a stated extraction gap.
func TestGroundTruth_CognitoResourceServer(t *testing.T) {
	row := mustRow(t, "aws_cognito_resource_server", "cognito_resource_server")
	if row.Separator != nil {
		t.Errorf("aws_cognito_resource_server: Separator = %q, want nil.\n"+
			"If this now resolves, the evidence for it must be in the page rather than in the issue - "+
			"say which sentence supplies it.", *row.Separator)
	}
}
