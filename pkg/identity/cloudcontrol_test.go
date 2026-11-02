// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

// GitHub issue #2286: the Cloud Control identifier translation moved from
// plunge (INTENTIUS/plunge#15, #19, #28, #31), with the tests plunge had for
// it. Written for epic:engine-consolidation and not run in the unit that
// added them.

import (
	"context"
	"errors"
	"testing"

	"github.com/intentius/choudoufu/pkg/registry"
)

func embeddedRoster(t *testing.T) *registry.Roster {
	t.Helper()
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("registry.Embedded: %s", err)
	}
	return roster
}

// translationFor is the translation CloudControlTranslation picks for tfType
// through cfnType, with the registry's primary identifier.
func translationFor(t *testing.T, tfType, cfnType string) *CloudControlID {
	t.Helper()
	primary := embeddedRoster(t).PrimaryIdentifier(cfnType)
	if len(primary) == 0 {
		t.Fatalf("the registry has no primary identifier for %s", cfnType)
	}
	tr, reason := CloudControlTranslation(tfType, cfnType, primary)
	if reason != "" || tr == nil {
		t.Fatalf("%s through %s: no translation (%s)", tfType, cfnType, reason)
	}
	return tr
}

func wantID(t *testing.T, tr *CloudControlID, name, identifier string, props map[string]any, want string) {
	t.Helper()
	got, ok := tr.Translate(identifier, props)
	if !ok {
		t.Errorf("%s: %q was not translated", name, identifier)
		return
	}
	if got != want {
		t.Errorf("%s: %q translated to %q, want %q", name, identifier, got, want)
	}
}

// The grammar translation follows each row's own separators and order, and
// refuses a row it cannot follow.
func TestCloudControlGrammarTranslation(t *testing.T) {
	t.Parallel()

	// aws_timestreamwrite_table's id is TABLE_NAME:DATABASE_NAME, the reverse
	// of its primary identifier's order.
	tr, reason := ccTerraformID("aws_timestreamwrite_table", "AWS::Timestream::Table", []string{"DatabaseName", "TableName"})
	if reason != "" {
		t.Fatalf("aws_timestreamwrite_table: %s", reason)
	}
	wantID(t, tr, "timestream", "db|tbl", nil, "tbl:db")
	if _, ok := tr.Translate("db", nil); ok {
		t.Error("an identifier with the wrong number of parts is not translated")
	}
	if _, ok := tr.Translate("db|", nil); ok {
		t.Error("an identifier with an empty part is not translated")
	}

	// A rename: AWS::Kendra::Faq's Id is the faq_id argument.
	tr, reason = ccTerraformID("aws_kendra_faq", "AWS::Kendra::Faq", []string{"Id", "IndexId"})
	if reason != "" {
		t.Fatalf("aws_kendra_faq: %s", reason)
	}
	wantID(t, tr, "kendra faq", "f-1|i-1", nil, "f-1/i-1")

	// A server-assigned row has no grammar to translate into. (The written-out
	// translation covers aws_guardduty_ipset; the grammar rule alone still
	// gives the reason.)
	if _, reason = ccTerraformID("aws_guardduty_ipset", "AWS::GuardDuty::IPSet", []string{"Id", "DetectorId"}); reason == "" {
		t.Error("aws_guardduty_ipset: the grammar rule should give a reason")
	}

	for in, want := range map[string]string{"VPCConnectionId": "vpc_connection_id", "KeyspaceName": "keyspace_name"} {
		if got := snakeCase(in); got != want {
			t.Errorf("snakeCase(%q) = %q, want %q", in, got, want)
		}
	}
}

// CloudControlTranslation picks the written-out translation over the grammar
// rule, and the single-part rule for a one-part identifier.
func TestCloudControlTranslationPicksTheRule(t *testing.T) {
	t.Parallel()
	tr, reason := CloudControlTranslation("aws_guardduty_ipset", "AWS::GuardDuty::IPSet", []string{"Id", "DetectorId"})
	if reason != "" || tr == nil || tr.hand == nil {
		t.Fatalf("aws_guardduty_ipset: want the written-out translation, got %+v (%s)", tr, reason)
	}
	tr, reason = CloudControlTranslation("aws_s3_bucket", "AWS::S3::Bucket", []string{"BucketName"})
	if reason != "" || tr == nil || tr.single == nil {
		t.Fatalf("aws_s3_bucket: want the single-part translation, got %+v (%s)", tr, reason)
	}
	if tr, reason = CloudControlTranslation("aws_s3_bucket", "AWS::S3::Bucket", nil); tr != nil || reason == "" {
		t.Errorf("no primary identifier: want no translation and a reason, got %+v (%q)", tr, reason)
	}
}

// A nil translation (a type that is not translated at all) needs nothing and
// translates nothing.
func TestNilCloudControlID(t *testing.T) {
	t.Parallel()
	var tr *CloudControlID
	if tr.NeedsAccount() || tr.ModelProperty() != "" || tr.WantsModel("x", nil) {
		t.Error("a nil translation needs no account and no model")
	}
	if _, ok := tr.Translate("x", nil); ok {
		t.Error("a nil translation translates nothing")
	}
	got, err := tr.ResolveAccount(context.Background(), AccountSourceFunc(func(context.Context) (string, error) {
		t.Error("the account source is not called for a translation that needs no account")
		return "", nil
	}))
	if err != nil || got != nil {
		t.Errorf("ResolveAccount on nil: %+v, %v", got, err)
	}
}

// The written-out translations (INTENTIUS/plunge#19): each turns a listed
// identifier, and for SageMaker the model's ARN, into the Terraform id the
// type is imported by. Budgets actions take the account ID from the engine.
func TestHandWrittenCloudControlTranslations(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tfType, cfnType, identifier string
		props                       map[string]any
		want                        string
	}{
		{"aws_eip", "AWS::EC2::EIP", "203.0.113.7|eipalloc-0abc", nil, "eipalloc-0abc"},
		{"aws_ecs_service", "AWS::ECS::Service",
			"arn:aws:ecs:us-east-1:123456789012:service/main/web|arn:aws:ecs:us-east-1:123456789012:cluster/main",
			nil, "main/web"},
		{"aws_guardduty_ipset", "AWS::GuardDuty::IPSet", "ip1|det1", nil, "det1:ip1"},
		{"aws_guardduty_threatintelset", "AWS::GuardDuty::ThreatIntelSet", "ti1|det1", nil, "det1:ti1"},
		{"aws_guardduty_publishing_destination", "AWS::GuardDuty::PublishingDestination", "det1|pd1", nil,
			"det1:pd1"},
		{"aws_quicksight_folder", "AWS::QuickSight::Folder", "123456789012|f1", nil, "123456789012,f1"},
		{"aws_sagemaker_space", "AWS::SageMaker::Space", "d-1|space1",
			map[string]any{"SpaceArn": "arn:aws:sagemaker:us-east-1:123456789012:space/d-1/space1"},
			"arn:aws:sagemaker:us-east-1:123456789012:space/d-1/space1"},
	} {
		tr, ok := ccHandTranslation(tc.tfType, tc.cfnType)
		if !ok {
			t.Errorf("%s: no written-out translation", tc.cfnType)
			continue
		}
		wantID(t, tr, tc.cfnType, tc.identifier, tc.props, tc.want)
	}

	tr, _ := ccHandTranslation("aws_sagemaker_app", "AWS::SageMaker::App")
	if got := tr.ModelProperty(); got != "AppArn" {
		t.Errorf("aws_sagemaker_app reads %q, want AppArn", got)
	}
	if !tr.WantsModel("a|JupyterServer|d-1|u", nil) {
		t.Error("aws_sagemaker_app wants the model when the list handler left AppArn out")
	}
	if _, ok := tr.Translate("a|JupyterServer|d-1|u", nil); ok {
		t.Error("without the model's ARN there is no id")
	}
}

// A budgets action id needs the account ID, which Cloud Control does not
// carry: the engine's AccountSource supplies it (plunge reads it with STS,
// INTENTIUS/plunge#31), and its error comes back as it is.
func TestBudgetsActionTakesTheAccountFromTheEngine(t *testing.T) {
	t.Parallel()
	tr, ok := ccHandTranslation("aws_budgets_budget_action", "AWS::Budgets::BudgetsAction")
	if !ok || !tr.NeedsAccount() {
		t.Fatal("aws_budgets_budget_action needs the account ID")
	}
	if _, ok := tr.Translate("act-1|monthly", nil); ok {
		t.Error("without the account ID there is no id")
	}
	wantID(t, tr.WithAccount("123456789012"), "WithAccount", "act-1|monthly", nil, "123456789012:act-1:monthly")

	calls := 0
	resolved, err := tr.ResolveAccount(context.Background(), AccountSourceFunc(func(context.Context) (string, error) {
		calls++
		return "210987654321", nil
	}))
	if err != nil || calls != 1 {
		t.Fatalf("ResolveAccount: %v after %d calls", err, calls)
	}
	wantID(t, resolved, "ResolveAccount", "act-1|monthly", nil, "210987654321:act-1:monthly")
	if _, ok := tr.Translate("act-1|monthly", nil); ok {
		t.Error("ResolveAccount returns a copy and leaves the translation it was called on without an account")
	}

	skip := errors.New("the stack names custom endpoints and none for STS")
	if _, err := tr.ResolveAccount(context.Background(), AccountSourceFunc(func(context.Context) (string, error) {
		return "", skip
	})); !errors.Is(err, skip) {
		t.Errorf("ResolveAccount should return the source's error as it is, got %v", err)
	}
	if _, err := tr.ResolveAccount(context.Background(), nil); err == nil {
		t.Error("ResolveAccount with no source refuses a translation that needs an account")
	}
}

// Each single-part table row names a type joined to a CloudFormation type
// with a single-part identifier, as the registry has it, and no type is in
// two tables.
func TestSingleTranslationTablesMatchTheRegistry(t *testing.T) {
	t.Parallel()
	roster := embeddedRoster(t)
	seen := map[string]string{}
	check := func(table, tfType, cfnType string) {
		if prev, ok := seen[tfType]; ok {
			t.Errorf("%s is in %s and %s", tfType, prev, table)
		}
		seen[tfType] = table
		got, ok := roster.CloudControlType(tfType)
		if !ok {
			t.Errorf("%s (%s) has no Cloud Control type", tfType, table)
		} else if got != cfnType {
			t.Errorf("%s (%s): registry says %s, table says %s", tfType, table, got, cfnType)
		}
		if n := roster.IdentifierArity(cfnType); n != 1 {
			t.Errorf("%s (%s) has a %d-part identifier", cfnType, table, n)
		}
	}
	for tf, p := range ccSingleProps {
		check("ccSingleProps", tf, p[0])
	}
	for tf, b := range ccSingleBuilds {
		check("ccSingleBuilds", tf, b.cfnType)
	}
	for tf := range ccSingleUncovered {
		cfn, ok := roster.CloudControlType(tf)
		if !ok {
			t.Errorf("%s (ccSingleUncovered) has no Cloud Control type", tf)
			continue
		}
		check("ccSingleUncovered", tf, cfn)
	}
}

// Every type whose id is another model property reads it from the model,
// whatever the identifier, and asks for the resource when the list handler
// left the property out.
func TestSingleIdentifierReadsTheIDProperty(t *testing.T) {
	t.Parallel()
	for tf, p := range ccSingleProps {
		tr := translationFor(t, tf, p[0])
		identifier := "arn:aws:example:us-east-1:123456789012:thing/listed"
		if !tr.WantsModel(identifier, map[string]any{}) {
			t.Errorf("%s: wants the model without %s", tf, p[1])
		}
		if _, ok := tr.Translate(identifier, map[string]any{}); ok {
			t.Errorf("%s: without the model's %s there is no id", tf, p[1])
		}
		props := map[string]any{p[1]: "the-id"}
		if tr.WantsModel(identifier, props) {
			t.Errorf("%s: wants no model once %s is there", tf, p[1])
		}
		wantID(t, tr, tf, identifier, props, "the-id")
	}
}

// Spot checks of ccSingleProps rows with real identifiers and models.
func TestSingleIdentifierTranslatedTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		tfType, cfnType, identifier string
		props                       map[string]any
		want                        string
	}{
		{"aws_vpclattice_service", "AWS::VpcLattice::Service",
			"arn:aws:vpc-lattice:us-east-1:123456789012:service/svc-0abc",
			map[string]any{"Id": "svc-0abc", "Arn": "arn:aws:vpc-lattice:us-east-1:123456789012:service/svc-0abc"},
			"svc-0abc"},
		{"aws_kinesis_firehose_delivery_stream", "AWS::KinesisFirehose::DeliveryStream", "logs",
			map[string]any{"DeliveryStreamName": "logs",
				"Arn": "arn:aws:firehose:us-east-1:123456789012:deliverystream/logs"},
			"arn:aws:firehose:us-east-1:123456789012:deliverystream/logs"},
		{"aws_sagemaker_endpoint", "AWS::SageMaker::Endpoint",
			"arn:aws:sagemaker:us-east-1:123456789012:endpoint/infer",
			map[string]any{"EndpointName": "infer"}, "infer"},
		{"aws_dms_endpoint", "AWS::DMS::Endpoint", "arn:aws:dms:us-east-1:123456789012:endpoint:ABCDEF",
			map[string]any{"EndpointIdentifier": "source-db"}, "source-db"},
		{"aws_ecs_task_definition", "AWS::ECS::TaskDefinition",
			"arn:aws:ecs:us-east-1:123456789012:task-definition/web:3", map[string]any{"Family": "web"}, "web"},
		{"aws_amplify_app", "AWS::Amplify::App", "arn:aws:amplify:us-east-1:123456789012:apps/d1a2b3",
			map[string]any{"AppId": "d1a2b3"}, "d1a2b3"},
	} {
		wantID(t, translationFor(t, tc.tfType, tc.cfnType), tc.tfType, tc.identifier, tc.props, tc.want)
	}
}

// The written-out single-part translations.
func TestSingleIdentifierBuiltIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, tfType, cfnType, identifier string
		props                             map[string]any
		want                              string
		ok                                bool
	}{
		{"rule on the default bus", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/nightly", nil, "nightly", true},
		{"rule on the default bus, named", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/default/nightly", nil, "nightly", true},
		{"rule on a custom bus", "aws_cloudwatch_event_rule", "AWS::Events::Rule",
			"arn:aws:events:us-east-1:123456789012:rule/orders/nightly", nil, "orders/nightly", true},
		{"rule identifier not an ARN", "aws_cloudwatch_event_rule", "AWS::Events::Rule", "nightly", nil, "", false},
		{"proxy endpoint", "aws_db_proxy_endpoint", "AWS::RDS::DBProxyEndpoint", "reader",
			map[string]any{"DBProxyName": "main"}, "main/reader", true},
		{"proxy endpoint without its proxy", "aws_db_proxy_endpoint", "AWS::RDS::DBProxyEndpoint", "reader",
			nil, "", false},
		{"guardrail", "aws_bedrock_guardrail", "AWS::Bedrock::Guardrail",
			"arn:aws:bedrock:us-east-1:123456789012:guardrail/gr1",
			map[string]any{"GuardrailId": "gr1", "Version": "DRAFT"}, "gr1,DRAFT", true},
		{"security lake subscriber", "aws_securitylake_subscriber", "AWS::SecurityLake::Subscriber",
			"arn:aws:securitylake:us-east-1:123456789012:subscriber/9f8e7d", nil, "9f8e7d", true},
	} {
		tr := translationFor(t, tc.tfType, tc.cfnType)
		got, ok := tr.Translate(tc.identifier, tc.props)
		if ok != tc.ok {
			t.Errorf("%s: ok = %v, want %v", tc.name, ok, tc.ok)
			continue
		}
		if tc.ok && got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A type whose id needs a value Cloud Control does not carry has no
// translation, with the reason.
func TestSingleIdentifierUncoveredTypes(t *testing.T) {
	t.Parallel()
	roster := embeddedRoster(t)
	for tf := range ccSingleUncovered {
		cfn, ok := roster.CloudControlType(tf)
		if !ok {
			t.Errorf("%s has no Cloud Control type", tf)
			continue
		}
		tr, reason := CloudControlTranslation(tf, cfn, roster.PrimaryIdentifier(cfn))
		if tr != nil || reason == "" {
			t.Errorf("%s: want no translation and a reason, got %+v (%q)", tf, tr, reason)
		}
	}
}

// A primary identifier that is the id passes through; one of the wrong shape,
// as an emulator lists it, is repaired from the model or cut from the ARN, and
// kept as it is when neither can be done.
func TestSingleIdentifierOfTheWrongShape(t *testing.T) {
	t.Parallel()
	const fnARN = "arn:aws:lambda:us-east-1:000000000000:function:rec-r2-4507-handler"
	const queueURL = "http://sqs.us-east-1.localhost.localstack.cloud:4566/000000000000/events"
	for _, tc := range []struct {
		name, tfType, cfnType, identifier string
		props                             map[string]any
		wantsModel                        bool
		want                              string
	}{
		{"lambda by name", "aws_lambda_function", "AWS::Lambda::Function", "rec-r2-4507-handler", nil, false,
			"rec-r2-4507-handler"},
		{"lambda by ARN, model has the name", "aws_lambda_function", "AWS::Lambda::Function", fnARN,
			map[string]any{"FunctionName": "rec-r2-4507-handler"}, false, "rec-r2-4507-handler"},
		{"lambda by ARN, no model", "aws_lambda_function", "AWS::Lambda::Function", fnARN, nil, true,
			"rec-r2-4507-handler"},
		{"queue by URL", "aws_sqs_queue", "AWS::SQS::Queue", queueURL, nil, false, queueURL},
		{"queue by name, model has the URL", "aws_sqs_queue", "AWS::SQS::Queue", "events",
			map[string]any{"QueueUrl": queueURL}, false, queueURL},
		{"queue by name, no URL anywhere", "aws_sqs_queue", "AWS::SQS::Queue", "events", nil, true, "events"},
		{"topic by ARN", "aws_sns_topic", "AWS::SNS::Topic", "arn:aws:sns:us-east-1:000000000000:alerts", nil,
			false, "arn:aws:sns:us-east-1:000000000000:alerts"},
		{"bucket by name", "aws_s3_bucket", "AWS::S3::Bucket", "logs", nil, false, "logs"},
		{"role by ARN", "aws_iam_role", "AWS::IAM::Role", "arn:aws:iam::000000000000:role/svc/worker", nil, true,
			"worker"},
		{"security group by ID", "aws_security_group", "AWS::EC2::SecurityGroup", "sg-0abc", nil, false,
			"sg-0abc"},
		{"parameter by name", "aws_ssm_parameter", "AWS::SSM::Parameter", "/app/db/host", nil, false,
			"/app/db/host"},
	} {
		tr := translationFor(t, tc.tfType, tc.cfnType)
		if got := tr.WantsModel(tc.identifier, tc.props); got != tc.wantsModel {
			t.Errorf("%s: WantsModel = %v, want %v", tc.name, got, tc.wantsModel)
		}
		wantID(t, tr, tc.name, tc.identifier, tc.props, tc.want)
	}
}
