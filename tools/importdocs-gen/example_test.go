// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

func argFor(args []ExampleArgument, path string) *ExampleArgument {
	for i := range args {
		if strings.Join(args[i].Path, ".") == path {
			return &args[i]
		}
	}
	return nil
}

// TestExampleReachesNestedBlocks is the case issue #136 names first.
// aws_s3_bucket_versioning's override exists to supply
// versioning_configuration { status = "Enabled" }, and the page's own
// example sets exactly that, two levels down.
func TestExampleReachesNestedBlocks(t *testing.T) {
	args := exampleArguments("## Example Usage\n\n```terraform\n"+`
resource "aws_s3_bucket" "example" {
  bucket = "example-bucket"
}

resource "aws_s3_bucket_versioning" "versioning_example" {
  bucket = aws_s3_bucket.example.id
  versioning_configuration {
    status = "Enabled"
  }
}
`+"```\n", "aws_s3_bucket_versioning")

	got := argFor(args, "versioning_configuration.status")
	if got == nil {
		t.Fatalf("versioning_configuration.status not extracted; got %v", args)
	}
	if got.Value != "Enabled" || !got.IsString {
		t.Errorf("got %+v, want Enabled as a string", got)
	}
	if got.Incomplete {
		t.Error("marked incomplete, but nothing in its own block was dropped; the reference that " +
			"was dropped (bucket) is in the outer block")
	}
	// The cross-resource reference is recorded, never seeded (issue #177):
	// the entry carries what the example reads and no value at all.
	a := argFor(args, "bucket")
	if a == nil {
		t.Fatal("the dropped reference `bucket` was not recorded")
	}
	if !a.Reference || a.Value != "" || a.RefType != "aws_s3_bucket" || a.RefAttr != "id" {
		t.Errorf("bucket recorded as %+v, want a valueless reference entry naming aws_s3_bucket.id", a)
	}
}

// TestReferenceInTheSameBlockMarksItIncomplete is the measurement that
// changed this design, so it is pinned.
//
// The SSE example sets kms_master_key_id from a KMS key resource and
// sse_algorithm to "aws:kms" in the same block. Keeping the literal and
// dropping the reference yields a configuration the provider rejects, which
// is why estate-gen's override hardcodes "AES256" instead. Extracting the
// literal without saying so would have made that type worse while looking
// like progress.
func TestReferenceInTheSameBlockMarksItIncomplete(t *testing.T) {
	args := exampleArguments("## Example Usage\n\n```terraform\n"+`
resource "aws_s3_bucket_server_side_encryption_configuration" "example" {
  rule {
    apply_server_side_encryption_by_default {
      kms_master_key_id = aws_kms_key.mykey.arn
      sse_algorithm     = "aws:kms"
    }
  }
}
`+"```\n", "aws_s3_bucket_server_side_encryption_configuration")

	got := argFor(args, "rule.apply_server_side_encryption_by_default.sse_algorithm")
	if got == nil {
		t.Fatalf("sse_algorithm not extracted; got %v", args)
	}
	if !got.Incomplete {
		t.Error("sse_algorithm is not marked incomplete, though the reference beside it was dropped. " +
			"A consumer would seed aws:kms with no key, which the provider rejects.")
	}
}

// TestUnrenderableSiblingDoesNotMarkIncomplete is the other half of that
// distinction. aws_iam_role's example sets assume_role_policy next to a tags
// map. The map has no literal rendering here, but that is this parser's
// limit rather than evidence the policy is wired to anything, and treating
// the two the same suppressed the very override class the issue wants
// retired.
func TestUnrenderableSiblingDoesNotMarkIncomplete(t *testing.T) {
	args := exampleArguments("## Example Usage\n\n```terraform\n"+`
resource "aws_iam_role" "test_role" {
  name = "test_role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{ Action = "sts:AssumeRole", Effect = "Allow" }]
  })

  tags = {
    tag-key = "tag-value"
  }
}
`+"```\n", "aws_iam_role")

	policy := argFor(args, "assume_role_policy")
	if policy == nil {
		t.Fatalf("assume_role_policy not extracted; got %v", args)
	}
	if policy.Incomplete {
		t.Error("assume_role_policy is marked incomplete because a tags map sits beside it; a map " +
			"this parser cannot render says nothing about whether the policy stands alone")
	}
	if !strings.Contains(policy.Value, `"sts:AssumeRole"`) {
		t.Errorf("jsonencode was not evaluated; value = %q", policy.Value)
	}
}

// TestOnlyTheRequestedTypesBlockIsRead. Every example wires several
// resources together, and reading the wrong block would attribute a KMS
// key's description to an S3 bucket.
func TestOnlyTheRequestedTypesBlockIsRead(t *testing.T) {
	doc := "## Example Usage\n\n```terraform\n" + `
resource "aws_kms_key" "mykey" {
  description             = "This key is used to encrypt bucket objects"
  deletion_window_in_days = 10
}

resource "aws_s3_bucket" "mybucket" {
  bucket = "mybucket"
}
` + "```\n"

	args := exampleArguments(doc, "aws_s3_bucket")
	if a := argFor(args, "description"); a != nil {
		t.Errorf("read the aws_kms_key block: description = %q", a.Value)
	}
	if a := argFor(args, "bucket"); a == nil || a.Value != "mybucket" {
		t.Errorf("did not read the aws_s3_bucket block; got %v", args)
	}
}

// TestFirstVariantWinsRatherThanTheUnion. Pages show several variants of the
// same resource - versioning enabled, then suspended - and merging them
// produces a configuration that sets one argument twice, or worse, combines
// settings the provider treats as mutually exclusive.
func TestFirstVariantWinsRatherThanTheUnion(t *testing.T) {
	doc := "## Example Usage\n\n### With Versioning Enabled\n\n```terraform\n" + `
resource "aws_s3_bucket_versioning" "a" {
  versioning_configuration { status = "Enabled" }
}
` + "```\n\n### With Versioning Suspended\n\n```terraform\n" + `
resource "aws_s3_bucket_versioning" "b" {
  versioning_configuration { status = "Suspended" }
}
` + "```\n"

	args := exampleArguments(doc, "aws_s3_bucket_versioning")
	if len(args) != 1 {
		t.Fatalf("got %d arguments, want 1 (the first variant only): %v", len(args), args)
	}
	if args[0].Value != "Enabled" {
		t.Errorf("status = %q, want the first variant's Enabled", args[0].Value)
	}
}

// TestVariantScoreIgnoresALaterBlockElement is aws_secretsmanager_secret_rotation's
// page, reduced. The Basic variant carries the one thing a consumer needs -
// rotation_rules { automatically_after_days = 30 } - while the partner
// variant's only complete literals are a SECOND
// external_secret_rotation_metadata element, which no consumer can place:
// only element zero has somewhere to land. Counting them ranked the partner
// variant first and cost the security cohort its terraform validate pass
// ("one of automatically_after_days, schedule_expression must be
// specified", measured 2026-08-15).
func TestVariantScoreIgnoresALaterBlockElement(t *testing.T) {
	doc := "## Example Usage\n\n### Basic\n\n```terraform\n" + `
resource "aws_secretsmanager_secret_rotation" "a" {
  secret_id           = aws_secretsmanager_secret.example.id
  rotation_lambda_arn = aws_lambda_function.example.arn

  rotation_rules {
    automatically_after_days = 30
  }
}
` + "```\n\n### Managed External\n\n```terraform\n" + `
resource "aws_secretsmanager_secret_rotation" "b" {
  secret_id                         = aws_secretsmanager_secret.example.id
  external_secret_rotation_role_arn = aws_iam_role.example.arn

  external_secret_rotation_metadata {
    key   = "adminSecretArn"
    value = aws_secretsmanager_secret.example.arn
  }

  external_secret_rotation_metadata {
    key   = "apiVersion"
    value = "v65.0"
  }

  rotation_rules {
    automatically_after_days = var.rotation_days
  }
}
` + "```\n"

	args := exampleArguments(doc, "aws_secretsmanager_secret_rotation")
	got := argFor(args, "rotation_rules.automatically_after_days")
	if got == nil || got.Reference || got.Value != "30" {
		t.Fatalf("rotation_rules.automatically_after_days = %+v, want the Basic variant's literal 30", got)
	}
}

// TestVariantScoreIgnoresALiteralInsideAnUnreachableBlock is
// aws_ssm_maintenance_window_task's page - all four of its fences, because
// the two-fence reduction of it agrees with any rule and proves nothing.
//
// Run Command holds two complete literals, but they sit inside a parameter
// block nested in run_command_parameters, which drops three references and
// so may never be created; it is not a candidate at all. Step Functions is
// a candidate and ties Automation on dropped references (three each), so
// the page's own lead example wins. Counting complete literals instead
// ranked Run Command first, then Step Functions, and either way the fixture
// lost both the automation_parameters member and the literal task_arn.
func TestVariantScoreIgnoresALiteralInsideAnUnreachableBlock(t *testing.T) {
	doc := "## Example Usage\n\n### Automation Tasks\n\n```terraform\n" + `
resource "aws_ssm_maintenance_window_task" "a" {
  max_concurrency = 2
  task_arn        = "AWS-RestartEC2Instance"
  task_type       = "AUTOMATION"
  window_id       = aws_ssm_maintenance_window.example.id

  targets {
    key    = "InstanceIds"
    values = [aws_instance.example.id]
  }

  task_invocation_parameters {
    automation_parameters {
      document_version = "$LATEST"

      parameter {
        name   = "InstanceId"
        values = [aws_instance.example.id]
      }
    }
  }
}
` + "```\n\n### Lambda Tasks\n\n```terraform\n" + `
resource "aws_ssm_maintenance_window_task" "b" {
  max_concurrency = 2
  task_arn        = aws_lambda_function.example.arn
  task_type       = "LAMBDA"
  window_id       = aws_ssm_maintenance_window.example.id

  targets {
    key    = "InstanceIds"
    values = [aws_instance.example.id]
  }

  task_invocation_parameters {
    lambda_parameters {
      payload = "{\"key1\":\"value1\"}"
    }
  }
}
` + "```\n\n### Run Command Tasks\n\n```terraform\n" + `
resource "aws_ssm_maintenance_window_task" "c" {
  max_concurrency = 2
  task_arn        = "AWS-RunShellScript"
  task_type       = "RUN_COMMAND"
  window_id       = aws_ssm_maintenance_window.example.id

  targets {
    key    = "InstanceIds"
    values = [aws_instance.example.id]
  }

  task_invocation_parameters {
    run_command_parameters {
      output_s3_bucket = aws_s3_bucket.example.id
      service_role_arn = aws_iam_role.example.arn
      timeout_seconds  = 600

      notification_config {
        notification_arn  = aws_sns_topic.example.arn
        notification_type = "Command"
      }

      parameter {
        name   = "commands"
        values = ["date"]
      }
    }
  }
}
` + "```\n\n### Step Function Tasks\n\n```terraform\n" + `
resource "aws_ssm_maintenance_window_task" "d" {
  max_concurrency = 2
  task_arn        = aws_sfn_activity.example.id
  task_type       = "STEP_FUNCTIONS"
  window_id       = aws_ssm_maintenance_window.example.id

  targets {
    key    = "InstanceIds"
    values = [aws_instance.example.id]
  }

  task_invocation_parameters {
    step_functions_parameters {
      input = "{\"key1\":\"value1\"}"
      name  = "example"
    }
  }
}
` + "```\n"

	args := exampleArguments(doc, "aws_ssm_maintenance_window_task")
	got := argFor(args, "task_invocation_parameters.automation_parameters.document_version")
	if got == nil || got.Value != "$LATEST" {
		t.Fatalf("automation_parameters.document_version = %+v, want the Automation variant's literal; got %v", got, args)
	}
	if a := argFor(args, "task_arn"); a == nil || a.Reference || a.Value != "AWS-RestartEC2Instance" {
		t.Errorf("task_arn = %+v, want the Automation variant's literal rather than a Lambda/Step Functions reference", a)
	}
}

// TestVariantPreferenceMeasuresSelfContainment: the property #177 names is
// how much a fence gives up to its siblings, and a fence's SIZE is not that
// property. A big wired-up variant must not outrank a small self-contained
// one just by carrying more arguments.
func TestVariantPreferenceMeasuresSelfContainment(t *testing.T) {
	doc := "## Example Usage\n\n### Wired\n\n```terraform\n" + `
resource "aws_thing" "a" {
  one   = aws_other.x.id
  two   = aws_other.x.arn
  three = aws_other.x.name

  block {
    p = "1"
    q = "2"
    r = "3"
  }
}
` + "```\n\n### Self contained\n\n```terraform\n" + `
resource "aws_thing" "b" {
  one = aws_other.x.id

  block {
    p = "9"
  }
}
` + "```\n"

	args := exampleArguments(doc, "aws_thing")
	got := argFor(args, "block.p")
	if got == nil || got.Value != "9" {
		t.Fatalf("block.p = %+v, want the self-contained variant's 9; the wired variant is merely larger", got)
	}
}

// TestVariantScoreStillPrefersMoreReachableLiterals: the rule this narrows
// is still the rule. aws_sagemaker_workteam's page leads with an all-
// reference Cognito variant and keeps the literal Oidc one second, and
// nothing about reachability changes that.
func TestVariantScoreStillPrefersMoreReachableLiterals(t *testing.T) {
	doc := "## Example Usage\n\n### Cognito\n\n```terraform\n" + `
resource "aws_sagemaker_workteam" "a" {
  workforce_name = aws_sagemaker_workforce.example.id

  member_definition {
    cognito_member_definition {
      client_id  = aws_cognito_user_pool_client.example.id
      user_pool  = aws_cognito_user_pool_domain.example.user_pool_id
      user_group = aws_cognito_user_group.example.id
    }
  }
}
` + "```\n\n### Oidc\n\n```terraform\n" + `
resource "aws_sagemaker_workteam" "b" {
  workforce_name = aws_sagemaker_workforce.example.id

  member_definition {
    oidc_member_definition {
      groups = ["example"]
    }
  }
}
` + "```\n"

	args := exampleArguments(doc, "aws_sagemaker_workteam")
	got := argFor(args, "member_definition.oidc_member_definition.groups")
	if got == nil || !got.IsList || got.Value != `["example"]` {
		t.Fatalf("oidc_member_definition.groups = %+v, want the second variant's list literal", got)
	}
}

// TestSubHeadingsStayInsideTheExampleSection. s3_bucket_versioning's example
// lives under "### With Versioning Enabled", so a section parser that
// stopped at any heading would find nothing at all.
func TestSubHeadingsStayInsideTheExampleSection(t *testing.T) {
	doc := "## Example Usage\n\n### A Sub Heading\n\n```terraform\n" + `
resource "aws_thing" "x" { mode = "on" }
` + "```\n\n## Argument Reference\n\n```terraform\nresource \"aws_thing\" \"y\" { mode = \"off\" }\n```\n"

	args := exampleArguments(doc, "aws_thing")
	if len(args) != 1 || args[0].Value != "on" {
		t.Fatalf("got %v, want the block under the sub-heading", args)
	}
}

// TestUnparseableExampleYieldsNothing. The provider's docs carry partial
// snippets and the occasional typo, and inventing a value from one is worse
// than having none.
func TestUnparseableExampleYieldsNothing(t *testing.T) {
	doc := "## Example Usage\n\n```terraform\nresource \"aws_thing\" \"x\" { mode = \n```\n"
	if args := exampleArguments(doc, "aws_thing"); len(args) != 0 {
		t.Errorf("extracted %v from an example that does not parse", args)
	}
}

// TestNumbersAndBoolsAreDistinguishableFromStrings, so a consumer writing
// them back out does not quote a number.
func TestNumbersAndBoolsAreDistinguishableFromStrings(t *testing.T) {
	doc := "## Example Usage\n\n```terraform\n" + `
resource "aws_thing" "x" {
  name    = "n"
  count_v = 10
  enabled = true
}
` + "```\n"
	args := exampleArguments(doc, "aws_thing")
	for _, tc := range []struct {
		path     string
		value    string
		isString bool
	}{
		{"name", "n", true},
		{"count_v", "10", false},
		{"enabled", "true", false},
	} {
		got := argFor(args, tc.path)
		if got == nil {
			t.Errorf("%s not extracted", tc.path)
			continue
		}
		if got.Value != tc.value || got.IsString != tc.isString {
			t.Errorf("%s = %+v, want value %q isString %v", tc.path, got, tc.value, tc.isString)
		}
	}
}

// TestLabelledNestedBlocksAreSkipped. A label is part of the block's
// identity, and seeding one without knowing what the label means is a guess.
func TestLabelledNestedBlocksAreSkipped(t *testing.T) {
	doc := "## Example Usage\n\n```terraform\n" + `
resource "aws_thing" "x" {
  name = "n"
  setting "some_label" {
    mode = "on"
  }
}
` + "```\n"
	args := exampleArguments(doc, "aws_thing")
	if a := argFor(args, "setting.mode"); a != nil {
		t.Errorf("extracted %v from a labelled block", a)
	}
}

// TestExtractionIsDeterministic. hclsyntax bodies hold attributes in a map,
// so without the sort the artifact churns on every regeneration.
func TestExtractionIsDeterministic(t *testing.T) {
	doc := "## Example Usage\n\n```terraform\n" + `
resource "aws_thing" "x" {
  zeta  = "z"
  alpha = "a"
  mid   = "m"
  nest { b = "2" a = "1" }
}
` + "```\n"
	first := exampleArguments(doc, "aws_thing")
	for i := 0; i < 20; i++ {
		again := exampleArguments(doc, "aws_thing")
		if len(again) != len(first) {
			t.Fatalf("run %d returned %d arguments, first returned %d", i, len(again), len(first))
		}
		for j := range first {
			if strings.Join(again[j].Path, ".") != strings.Join(first[j].Path, ".") {
				t.Fatalf("run %d ordered arguments differently: %v vs %v", i, again, first)
			}
		}
	}
}
