// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// wantIssue is the part of an [Issue] a test asserts on: which rule fired,
// which construct it named, where it pointed, and which module it was in. The
// detail prose is deliberately not asserted, so that rewording an explanation
// does not break the suite.
type wantIssue struct {
	rule      Rule
	construct string
	module    string
	file      string
	line      int
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want []wantIssue
	}{
		{
			name: "provisioner block",
			dir:  "testdata/provisioner",
			want: []wantIssue{
				{
					rule:      RuleProvisioner,
					construct: `provisioner "local-exec" on aws_s3_bucket.data`,
					file:      "testdata/provisioner/main.tf",
					line:      7,
				},
			},
		},
		{
			name: "standalone connection block",
			dir:  "testdata/connection",
			want: []wantIssue{
				{
					rule:      RuleProvisioner,
					construct: "connection block on aws_s3_bucket.data",
					file:      "testdata/connection/main.tf",
					line:      7,
				},
			},
		},
		{
			name: "terraform_remote_state data source",
			dir:  "testdata/remote-state",
			want: []wantIssue{
				{
					rule:      RuleRemoteState,
					construct: "data.terraform_remote_state.network",
					file:      "testdata/remote-state/main.tf",
					line:      3,
				},
			},
		},
		{
			name: "moved block",
			dir:  "testdata/moved",
			want: []wantIssue{
				{
					rule:      RuleMovedBlock,
					construct: "moved block",
					file:      "testdata/moved/main.tf",
					line:      8,
				},
			},
		},
		{
			name: "logical resources, one per banned prefix",
			dir:  "testdata/logical",
			want: []wantIssue{
				{
					rule:      RuleLogicalResource,
					construct: "random_pet.name",
					file:      "testdata/logical/main.tf",
					line:      5,
				},
				{
					rule:      RuleLogicalResource,
					construct: "tls_private_key.signing",
					file:      "testdata/logical/main.tf",
					line:      9,
				},
				{
					rule:      RuleLogicalResource,
					construct: "time_sleep.wait",
					file:      "testdata/logical/main.tf",
					line:      13,
				},
				{
					rule:      RuleLogicalResource,
					construct: "null_resource.trigger",
					file:      "testdata/logical/main.tf",
					line:      17,
				},
				{
					rule:      RuleLogicalResource,
					construct: "local_file.rendered",
					file:      "testdata/logical/main.tf",
					line:      20,
				},
			},
		},
		{
			name: "resource type outside the admission table",
			dir:  "testdata/unadmitted",
			want: []wantIssue{
				{
					rule:      RuleUnadmittedType,
					construct: "aws_instance.web",
					file:      "testdata/unadmitted/main.tf",
					line:      4,
				},
			},
		},
		{
			name: "backend block",
			dir:  "testdata/backend",
			want: []wantIssue{
				{
					rule:      RuleStateBackend,
					construct: `backend "local"`,
					file:      "testdata/backend/main.tf",
					line:      4,
				},
			},
		},
		{
			name: "cloud block",
			dir:  "testdata/cloud",
			want: []wantIssue{
				{
					rule:      RuleStateBackend,
					construct: "cloud block",
					file:      "testdata/cloud/main.tf",
					line:      4,
				},
			},
		},
		{
			name: "count.index in every position it must be caught",
			dir:  "testdata/count-index",
			want: []wantIssue{
				{
					rule:      RuleCountIndex,
					construct: "count.index in aws_vpc.plain_arg",
					file:      "testdata/count-index/main.tf",
					line:      9,
				},
				{
					rule:      RuleCountIndex,
					construct: "count.index in aws_vpc.in_tag",
					file:      "testdata/count-index/main.tf",
					line:      19,
				},
				{
					rule:      RuleCountIndex,
					construct: "count.index in aws_security_group.nested_block",
					file:      "testdata/count-index/main.tf",
					line:      32,
				},
				{
					rule:      RuleCountIndex,
					construct: "count.index in aws_vpc.conditional",
					file:      "testdata/count-index/main.tf",
					line:      46,
				},
			},
		},
		{
			name: "several rules at once, reported in source order",
			dir:  "testdata/multiple",
			want: []wantIssue{
				{
					rule:      RuleStateBackend,
					construct: `backend "local"`,
					file:      "testdata/multiple/main.tf",
					line:      6,
				},
				{
					rule:      RuleUnadmittedType,
					construct: "aws_instance.web",
					file:      "testdata/multiple/main.tf",
					line:      11,
				},
				{
					rule:      RuleProvisioner,
					construct: `provisioner "local-exec" on aws_instance.web`,
					file:      "testdata/multiple/main.tf",
					line:      14,
				},
				{
					rule:      RuleLogicalResource,
					construct: "null_resource.trigger",
					file:      "testdata/multiple/main.tf",
					line:      19,
				},
				{
					rule:      RuleRemoteState,
					construct: "data.terraform_remote_state.network",
					file:      "testdata/multiple/main.tf",
					line:      22,
				},
				{
					rule:      RuleMovedBlock,
					construct: "moved block",
					file:      "testdata/multiple/main.tf",
					line:      26,
				},
			},
		},
		{
			// The module call is rejected in its own right (RuleChildModule),
			// and the walk still goes into the child and names it, which is
			// what makes a fix-one-then-rerun loop unnecessary: an operator
			// sees the module block and everything wrong inside it at once.
			name: "child module is refused, walked, and named",
			dir:  "testdata/child-module",
			want: []wantIssue{
				{
					rule:      RuleChildModule,
					construct: `module "compute"`,
					file:      "testdata/child-module/main.tf",
					line:      8,
				},
				{
					rule:      RuleUnadmittedType,
					construct: "aws_instance.web",
					module:    "module.compute",
					file:      "testdata/child-module/child/main.tf",
					line:      1,
				},
			},
		},
		{
			name: "admitted types with surviving meta-arguments",
			dir:  "testdata/clean",
			want: nil,
		},
		{
			name: "receipt leaf rule: argument, depends_on, and output references",
			dir:  "testdata/receipt-leaf",
			want: []wantIssue{
				{
					rule:      RuleReceiptLeaf,
					construct: "aws_s3_bucket.leaks_via_argument references receipt aws_ssm_parameter.demo_effect",
					file:      "testdata/receipt-leaf/main.tf",
					line:      21,
				},
				{
					rule:      RuleReceiptLeaf,
					construct: "aws_iam_role.leaks_via_depends_on references receipt aws_ssm_parameter.demo_effect",
					file:      "testdata/receipt-leaf/main.tf",
					line:      33,
				},
				{
					rule:      RuleReceiptLeaf,
					construct: "output.leaks_via_output references receipt aws_ssm_parameter.demo_effect",
					file:      "testdata/receipt-leaf/main.tf",
					line:      37,
				},
			},
		},
		{
			name: "receipt leaf rule: dynamic name is not statically evident, so not flagged",
			dir:  "testdata/receipt-leaf-dynamic-name",
			want: nil,
		},
		{
			name: "receipt value rule: SecureString, raw input, and local indirection; both flavors clean",
			dir:  "testdata/receipt-value",
			want: []wantIssue{
				{
					rule:      RuleReceiptValue,
					construct: "receipt aws_ssm_parameter.secure declares type SecureString",
					file:      "testdata/receipt-value/main.tf",
					line:      18,
				},
				{
					rule:      RuleReceiptValue,
					construct: "receipt aws_ssm_parameter.raw_input value is not visibly a hash or a constant",
					file:      "testdata/receipt-value/main.tf",
					line:      25,
				},
				{
					rule:      RuleReceiptValue,
					construct: "receipt aws_ssm_parameter.hash_via_local value is not visibly a hash or a constant",
					file:      "testdata/receipt-value/main.tf",
					line:      35,
				},
			},
		},
		{
			name: "receipt secret rule: sensitive variable in value flagged, pointer variable clean",
			dir:  "testdata/receipt-secret",
			want: []wantIssue{
				{
					rule:      RuleReceiptSecret,
					construct: "receipt aws_ssm_parameter.hashes_the_secret value reads sensitive variable var.db_password",
					file:      "testdata/receipt-secret/main.tf",
					line:      23,
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := loadConfigDir(t, test.dir)
			assertIssues(t, CheckContext(t.Context(), cfg), test.want)
		})
	}
}

// TestCheckEstate is the clean-pass case that matters: the P0.1 estate fixture,
// which is the configuration every later phase is built and demoed against. If
// it ever stops passing, either the estate has drifted out of the subset or a
// rule has become too broad.
func TestCheckEstate(t *testing.T) {
	// The estate lives outside this package, at the repository root, because
	// it is an e2e fixture rather than a lint fixture: live/e2e/estate
	// relative to the root, and this package is internal/live/lint.
	estate := filepath.Join("..", "..", "..", "live", "e2e", "estate")

	cfg := loadConfigDir(t, estate)

	// Guard against passing vacuously: if the estate ever moves or empties out,
	// "no issues" would otherwise look like success.
	if got := len(cfg.Module.ManagedResources); got < 10 {
		t.Fatalf("loaded only %d managed resources from %s; is the fixture still there?", got, estate)
	}

	issues := CheckContext(t.Context(), cfg)
	for _, issue := range issues {
		t.Errorf("estate fixture is outside the stateless subset: %s", issue)
	}
}

func TestCheckNilConfig(t *testing.T) {
	if issues := CheckContext(t.Context(), nil); len(issues) != 0 {
		t.Fatalf("expected no issues for a nil config, got %d", len(issues))
	}
}

// TestDiagnostics checks that issues survive the trip into tfdiags with their
// summary, source range, docs citation, and construct name intact, since
// that conversion is the only thing P1.4 will call.
func TestDiagnostics(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/moved")
	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	diags := Diagnostics(issues)
	if got, want := len(diags), 1; got != want {
		t.Fatalf("expected %d diagnostics, got %d", want, got)
	}

	diag := diags[0]
	if diag.Severity() != tfdiags.Error {
		t.Errorf("expected an error diagnostic, got severity %v", diag.Severity())
	}
	desc := diag.Description()
	if got, want := desc.Summary, RuleMovedBlock.Summary(); got != want {
		t.Errorf("summary = %q, want %q", got, want)
	}
	for _, want := range []string{"moved block", string(RuleMovedBlock), RuleMovedBlock.DocsRef(), "choudoufu live-mv"} {
		if !strings.Contains(desc.Detail, want) {
			t.Errorf("detail does not mention %q:\n%s", want, desc.Detail)
		}
	}
	src := diag.Source()
	if src.Subject == nil {
		t.Fatalf("diagnostic has no source subject")
	}
	if got, want := src.Subject.Start.Line, 8; got != want {
		t.Errorf("subject line = %d, want %d", got, want)
	}
}

// TestAdmissionTableCoversEstate is a guard on the table itself rather than on
// the check: every managed resource type in the estate fixture must be
// admitted, stated once as a list so that dropping an entry from the table
// fails here with a clear message rather than only through the estate walk.
func TestAdmissionTableCoversEstate(t *testing.T) {
	estateTypes := []string{
		"aws_vpc",
		"aws_subnet",
		"aws_security_group",
		"aws_route_table",
		"aws_internet_gateway",
		"aws_route",
		"aws_route_table_association",
		"aws_s3_bucket",
		"aws_s3_bucket_policy",
		"aws_iam_role",
		"aws_iam_role_policy_attachment",
		"aws_cloudwatch_log_group",
		"aws_eip",
		"aws_ssm_parameter",
		"aws_dynamodb_table",
		"aws_ecs_cluster",
		"aws_kms_key",
		"aws_route53_zone",
		"aws_s3_bucket_versioning",
		"aws_s3_bucket_public_access_block",
		"aws_s3_bucket_server_side_encryption_configuration",
		"aws_s3_bucket_lifecycle_configuration",
		"aws_iam_role_policy",
		"aws_kms_alias",
		"aws_route53_record",
		"aws_cloudwatch_metric_alarm",
		"aws_lb",
		"aws_lb_target_group",
		"aws_lb_listener",
		"aws_lb_target_group_attachment",
		"aws_sns_topic",
	}
	for _, resourceType := range estateTypes {
		if !admitted(resourceType) {
			t.Errorf("%s is used by the estate fixture but is missing from the v0 admission table", resourceType)
		}
	}
	if got, want := len(admittedTypesV0), len(estateTypes); got != want {
		t.Errorf("v0 admission table has %d types, want exactly the estate's %d", got, want)
	}
}

func TestLogicalType(t *testing.T) {
	tests := []struct {
		resourceType string
		wantPrefix   string
		wantLogical  bool
	}{
		{"random_pet", "random_", true},
		{"tls_private_key", "tls_", true},
		{"time_sleep", "time_", true},
		{"null_resource", "null_", true},
		{"local_file", "local_", true},
		{"aws_vpc", "", false},
		// Prefix matching must not be fooled by a type that merely starts with
		// the same letters as a banned family.
		{"randomizer_widget", "", false},
		{"localstack_thing", "", false},
	}
	for _, test := range tests {
		prefix, logical := logicalType(test.resourceType)
		if logical != test.wantLogical || prefix != test.wantPrefix {
			t.Errorf("logicalType(%q) = (%q, %v), want (%q, %v)",
				test.resourceType, prefix, logical, test.wantPrefix, test.wantLogical)
		}
	}
}

// assertIssues compares what Check produced against the expected issues, in
// order. Every issue must also carry a detail, which is required rather than
// compared.
func assertIssues(t *testing.T, got []Issue, want []wantIssue) {
	t.Helper()

	summary := make([]wantIssue, len(got))
	for i, issue := range got {
		summary[i] = wantIssue{
			rule:      issue.Rule,
			construct: issue.Construct,
			module:    issue.Module.String(),
			file:      filepath.ToSlash(issue.Subject.Filename),
			line:      issue.Subject.Start.Line,
		}
		if issue.Detail == "" {
			t.Errorf("issue %d has no detail: %s", i, issue)
		}
	}

	if diff := cmp.Diff(want, summary, cmp.AllowUnexported(wantIssue{}), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("wrong issues\n%s", diff)
	}
}

// loadConfigDir builds a configuration tree from a directory the same way the
// commands do (configs.Parser plus configs.BuildConfig), with a module walker
// that resolves local source addresses straight from disk. That is enough for
// these fixtures, which use no registry or remote modules, and it avoids
// needing an installed .terraform/modules directory the way configload does.
func loadConfigDir(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	rootMod, diags := parser.LoadConfigDir(dir, configs.RootModuleCallForTesting())
	if diags.HasErrors() {
		t.Fatalf("failed to load %s: %s", dir, diags.Error())
	}

	walker := configs.ModuleWalkerFunc(func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
		sourceAddr, ok := req.SourceAddr.(addrs.ModuleSourceLocal)
		if !ok {
			return nil, nil, hcl.Diagnostics{{
				Severity: hcl.DiagError,
				Summary:  "Unsupported module source in test fixture",
				Detail:   "Only local module sources are supported by these tests.",
				Subject:  req.SourceAddrRange.Ptr(),
			}}
		}
		childDir := filepath.Join(req.Parent.Module.SourceDir, string(sourceAddr))
		mod, diags := parser.LoadConfigDir(childDir, req.Call)
		return mod, nil, diags
	})

	cfg, diags := configs.BuildConfig(t.Context(), rootMod, walker)
	if diags.HasErrors() {
		t.Fatalf("failed to build config from %s: %s", dir, diags.Error())
	}
	return cfg
}
