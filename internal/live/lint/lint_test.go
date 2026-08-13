// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
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
			// GitHub issue #67: "delete" is not in declared+tagged's valid
			// set (see internal/live/policy.ValidVerbs).
			name: "policy verb invalid for its quadrant",
			dir:  "testdata/policy-invalid-verb",
			want: []wantIssue{
				{
					rule:      RulePolicyVerb,
					construct: `policy.declared_tagged = "delete"`,
					file:      "testdata/policy-invalid-verb/main.tf",
					line:      8,
				},
			},
		},
		{
			// GitHub issue #67: the maintainer's own example, minus the
			// scope block a delete quadrant needs.
			name: "policy delete quadrant with no scope block",
			dir:  "testdata/policy-unscoped-delete",
			want: []wantIssue{
				{
					rule:      RulePolicyScope,
					construct: `policy.undeclared_untagged = "delete"`,
					file:      "testdata/policy-unscoped-delete/main.tf",
					line:      8,
				},
			},
		},
		{
			name: "policy threshold not positive",
			dir:  "testdata/policy-bad-threshold",
			want: []wantIssue{
				{
					rule:      RulePolicyThreshold,
					construct: "policy.threshold = 0",
					file:      "testdata/policy-bad-threshold/main.tf",
					line:      9,
				},
			},
		},
		{
			// A scope block that narrows nothing is the same refusal as no
			// scope block at all.
			name: "policy delete quadrant with an empty scope block",
			dir:  "testdata/policy-empty-scope",
			want: []wantIssue{
				{
					rule:      RulePolicyScope,
					construct: `policy.undeclared_untagged = "delete"`,
					file:      "testdata/policy-empty-scope/main.tf",
					line:      8,
				},
			},
		},
		{
			// Every verb is valid for its quadrant, and the delete quadrant
			// carries a scope block: checkLivePolicy has nothing to refuse.
			name: "policy block, lint-clean",
			dir:  "testdata/policy-valid",
			want: nil,
		},
		{
			// Only one quadrant is set. The other three - including
			// undeclared_tagged, whose default is "delete", today's
			// unscoped sweep - resolve to internal/live/policy.DefaultVerb
			// and are not treated as if the configuration had written
			// "delete" itself, so this fixture needs no scope block.
			name: "policy block, quadrants left to their default",
			dir:  "testdata/policy-default-omitted",
			want: nil,
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
					construct: "aws_customer_gateway.web",
					file:      "testdata/unadmitted/main.tf",
					line:      5,
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
					construct: "aws_customer_gateway.web",
					file:      "testdata/multiple/main.tf",
					line:      11,
				},
				{
					rule:      RuleProvisioner,
					construct: `provisioner "local-exec" on aws_customer_gateway.web`,
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
			// A static module call is admitted (59b: the five walkers traverse
			// it, so there is nothing left for RuleChildModule to refuse), and
			// the walk still goes into the child and names it, which is what
			// makes a fix-one-then-rerun loop unnecessary: an operator sees
			// everything wrong inside a module at once, module path included.
			name: "static child module is admitted, walked, and named",
			dir:  "testdata/child-module",
			want: []wantIssue{
				{
					rule:      RuleUnadmittedType,
					construct: "aws_customer_gateway.web",
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

// TestAdmissionTableCoversEstate is a guard on the table itself rather than
// on the check: every managed resource type used by a fixture must be
// admitted. The universe is not just the demo estate but its union with
// every per-cohort verification estate under live/e2e/estates (#48, phase 3
// of #38's decision) - table == union(estate, estates/*), read straight off
// the fixtures rather than pinned as a hand-maintained list, so that adding
// a estates/<cohort> directory extends the pin with no test-file edits.
func TestAdmissionTableCoversEstate(t *testing.T) {
	fixtureTypes := map[string]bool{}
	for _, dir := range flocitest.FixtureDirs(t) {
		cfg := loadConfigDir(t, dir)
		for _, rc := range cfg.Module.ManagedResources {
			fixtureTypes[rc.Type] = true
		}
	}
	for resourceType := range fixtureTypes {
		if !admitted(resourceType, nil, nil) {
			t.Errorf("%s is used by a fixture but is missing from the v0 admission table", resourceType)
		}
	}
	if got, want := len(admittedTypesV0), len(fixtureTypes); got != want {
		t.Errorf("v0 admission table has %d types, want exactly the fixtures' %d", got, want)
	}
}

func TestClassifyLogicalType(t *testing.T) {
	tests := []struct {
		resourceType string
		wantLogical  bool
		wantClass    LogicalClass
		wantPrefix   string
	}{
		// RECORD_ADMITTED: verified against provider docs, per logicalTypes.
		{"random_pet", true, ClassRecordAdmitted, "random_"},
		{"random_id", true, ClassRecordAdmitted, "random_"},
		{"random_shuffle", true, ClassRecordAdmitted, "random_"},
		{"random_integer", true, ClassRecordAdmitted, "random_"},
		{"time_sleep", true, ClassRecordAdmitted, "time_"},
		{"time_static", true, ClassRecordAdmitted, "time_"},
		{"time_offset", true, ClassRecordAdmitted, "time_"},
		{"time_rotating", true, ClassRecordAdmitted, "time_"},
		{"null_resource", true, ClassRecordAdmitted, "null_"},
		// terraform_data is the one entry with no family prefix of its own -
		// the audit's whole point (admission.go used to miss it entirely).
		{"terraform_data", true, ClassRecordAdmitted, ""},

		// SECRET_REFUSED: named rows and the tls_ family default alike.
		{"random_password", true, ClassSecretRefused, "random_"},
		{"random_bytes", true, ClassSecretRefused, "random_"},
		{"tls_private_key", true, ClassSecretRefused, "tls_"},
		{"tls_self_signed_cert", true, ClassSecretRefused, "tls_"},
		{"tls_locally_signed_cert", true, ClassSecretRefused, "tls_"},
		{"tls_cert_request", true, ClassSecretRefused, "tls_"},
		// A hypothetical tls_ type with no row of its own still defaults to
		// SECRET_REFUSED, not the generic OTHER_REFUSED every other
		// unlisted family member gets - the tls_ family's evidence is
		// uniform enough to extend to a type nobody has reviewed yet.
		{"tls_hypothetical_new_type", true, ClassSecretRefused, "tls_"},

		// OTHER_REFUSED: local_* (current wording family) and any other
		// family member this table has no specific opinion about.
		{"local_file", true, ClassOtherRefused, "local_"},
		{"local_sensitive_file", true, ClassOtherRefused, "local_"},
		{"random_string", true, ClassOtherRefused, "random_"},

		// Not logical at all.
		{"aws_vpc", false, "", ""},
		// Prefix matching must not be fooled by a type that merely starts
		// with the same letters as a banned family.
		{"randomizer_widget", false, "", ""},
		{"localstack_thing", false, "", ""},
	}
	for _, test := range tests {
		lt, logical := ClassifyLogicalType(test.resourceType)
		if logical != test.wantLogical {
			t.Errorf("ClassifyLogicalType(%q) ok = %v, want %v", test.resourceType, logical, test.wantLogical)
			continue
		}
		if !logical {
			continue
		}
		if lt.Class != test.wantClass {
			t.Errorf("ClassifyLogicalType(%q).Class = %q, want %q", test.resourceType, lt.Class, test.wantClass)
		}
		if lt.Prefix != test.wantPrefix {
			t.Errorf("ClassifyLogicalType(%q).Prefix = %q, want %q", test.resourceType, lt.Prefix, test.wantPrefix)
		}
		if lt.Type != test.resourceType {
			t.Errorf("ClassifyLogicalType(%q).Type = %q, want %q", test.resourceType, lt.Type, test.resourceType)
		}
	}
}

// TestLogicalTypesTableWellFormed checks the per-type table's internal
// consistency: every row's map key matches its own Type field, every
// RECORD_ADMITTED and SECRET_REFUSED row carries non-empty Evidence (the
// provider-docs citation the classification rests on), no ClassOtherRefused
// row exists in the hand-written table (that class is only ever the
// no-row-found default; a hand-written row always has more to say than
// that), and every row's Prefix - when set - is one resourceType actually
// starts with, drawn from logicalFamilyPrefixes, except terraform_data's
// deliberate empty Prefix.
func TestLogicalTypesTableWellFormed(t *testing.T) {
	validPrefix := make(map[string]bool, len(logicalFamilyPrefixes))
	for _, p := range logicalFamilyPrefixes {
		validPrefix[p] = true
	}

	for key, lt := range logicalTypes {
		if lt.Type != key {
			t.Errorf("logicalTypes[%q].Type = %q, want %q", key, lt.Type, key)
		}
		switch lt.Class {
		case ClassRecordAdmitted, ClassSecretRefused:
			if lt.Evidence == "" {
				t.Errorf("logicalTypes[%q] (%s) has no Evidence", key, lt.Class)
			}
		case ClassOtherRefused:
			t.Errorf("logicalTypes[%q] is ClassOtherRefused; that class should never need a hand-written row, only the ClassifyLogicalType default", key)
		default:
			t.Errorf("logicalTypes[%q] has unknown Class %q", key, lt.Class)
		}
		if lt.Prefix == "" {
			if key != "terraform_data" {
				t.Errorf("logicalTypes[%q] has an empty Prefix; only terraform_data is expected to", key)
			}
			continue
		}
		if !validPrefix[lt.Prefix] {
			t.Errorf("logicalTypes[%q].Prefix = %q, not one of logicalFamilyPrefixes", key, lt.Prefix)
		}
		if !strings.HasPrefix(key, lt.Prefix) {
			t.Errorf("logicalTypes[%q].Prefix = %q, but the type does not start with it", key, lt.Prefix)
		}
	}
}

// TestLogicalResourceDetailsRenderByClass checks that the Detail lint
// produces for a logical resource names its class and, for the two
// audited classes, cites the evidence and (for RECORD_ADMITTED) the #73
// forwarding address - and that the OTHER_REFUSED wording is exactly the
// original, class-agnostic template, byte for byte, since that class's
// whole point is that nothing changed for it.
func TestLogicalResourceDetailsRenderByClass(t *testing.T) {
	t.Run("RECORD_ADMITTED names the class and #73", func(t *testing.T) {
		lt, ok := ClassifyLogicalType("null_resource")
		if !ok || lt.Class != ClassRecordAdmitted {
			t.Fatalf("null_resource classified %+v, ok=%v; want ClassRecordAdmitted", lt, ok)
		}
		detail := logicalResourceDetail("null_resource", lt)
		for _, want := range []string{"RECORD_ADMITTED", "#73", lt.Evidence} {
			if !strings.Contains(detail, want) {
				t.Errorf("RECORD_ADMITTED Detail = %q, want it to contain %q", detail, want)
			}
		}
	})

	t.Run("SECRET_REFUSED names the class and the no-secrets rule", func(t *testing.T) {
		lt, ok := ClassifyLogicalType("random_password")
		if !ok || lt.Class != ClassSecretRefused {
			t.Fatalf("random_password classified %+v, ok=%v; want ClassSecretRefused", lt, ok)
		}
		detail := logicalResourceDetail("random_password", lt)
		for _, want := range []string{"SECRET_REFUSED", "secret manager", lt.Evidence} {
			if !strings.Contains(detail, want) {
				t.Errorf("SECRET_REFUSED Detail = %q, want it to contain %q", detail, want)
			}
		}
	})

	t.Run("OTHER_REFUSED wording is byte-identical to the pre-table template", func(t *testing.T) {
		lt, ok := ClassifyLogicalType("local_file")
		if !ok || lt.Class != ClassOtherRefused {
			t.Fatalf("local_file classified %+v, ok=%v; want ClassOtherRefused", lt, ok)
		}
		got := logicalResourceDetail("local_file", lt)
		want := `"local_file" is a logical resource (local_*): it has no existence outside the record ` +
			"that OpenTofu keeps of it, so that record is the store live resource " +
			"markers remove. Nothing can recover its value from the live system, because " +
			"there is no live system holding it. Pass the value in as a variable or " +
			"a local, or read it from a resource that really exists"
		if got != want {
			t.Errorf("OTHER_REFUSED Detail =\n%q\nwant\n%q", got, want)
		}
	})
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

// ---------------------------------------------------------------------------
// #45: CheckWith and the schema fallback admitted() applies when the caller
// has provider schemas to offer.
// ---------------------------------------------------------------------------

// fakeType is a minimal provider type shape for building a fake schema,
// enough to drive [identity.SynthesizeTypeIdentity] the same way
// internal/live/identity's own tests do. Kept local to this package rather
// than shared, because sharing it would mean exporting identity's test-only
// helpers for a use nothing else has.
type fakeType struct {
	// args are the type's configuration arguments: name to "req" (required)
	// or "optcomp" (Optional+Computed, the legacy SDK's shape for an
	// argument a provider may also fill in).
	args map[string]string
	// identity are the identity schema's attributes: name to "req"
	// (required for import) or "opt" (optional for import).
	identity map[string]string
}

func fakeProviderSchemas(types map[string]fakeType) map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(types))
	for name, ft := range types {
		block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
		for argName, kind := range ft.args {
			attr := &configschema.Attribute{Type: cty.String}
			switch kind {
			case "req":
				attr.Required = true
			case "optcomp":
				attr.Optional, attr.Computed = true, true
			default:
				panic("unknown argument kind " + kind)
			}
			block.Attributes[argName] = attr
		}

		schema := providers.Schema{Block: block}
		if ft.identity != nil {
			body := &configschema.Object{
				Attributes: map[string]*configschema.Attribute{},
				Nesting:    configschema.NestingSingle,
			}
			for attrName, kind := range ft.identity {
				attr := &configschema.Attribute{Type: cty.String}
				switch kind {
				case "req":
					attr.Required = true
				case "opt":
					attr.Optional = true
				default:
					panic("unknown identity attribute kind " + kind)
				}
				body.Attributes[attrName] = attr
			}
			schema.IdentitySchema = body
			schema.IdentitySchemaVersion = 1
		}
		out[name] = schema
	}
	return out
}

// thingSchema is aws_thing's shape from identity's own fallback tests: a
// single required identity attribute, name, that is also a required
// argument, with the AWS context pair (account_id, region) as the only
// optional-for-import attributes. SynthesizeTypeIdentity admits this shape
// outright.
func thingSchema() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_thing": {
			args:     map[string]string{"name": "req"},
			identity: map[string]string{"name": "req", "account_id": "opt", "region": "opt"},
		},
	})
}

// routeSchema is aws_route's own shape, the same one
// internal/live/identity/synthesize_test.go stands up to pin #39 from the
// resolver side: one required identity attribute, route_table_id, plus the
// three destination_* arguments the real AWS provider marks optional for
// import. A single required attribute that is not the whole identity -
// exactly the shape #39 taught the fallback to refuse.
func routeSchema() map[string]providers.Schema {
	return fakeProviderSchemas(map[string]fakeType{
		"aws_route": {
			args: map[string]string{
				"route_table_id":              "req",
				"destination_cidr_block":      "optcomp",
				"destination_ipv6_cidr_block": "optcomp",
				"destination_prefix_list_id":  "optcomp",
			},
			identity: map[string]string{
				"route_table_id":              "req",
				"destination_cidr_block":      "opt",
				"destination_ipv6_cidr_block": "opt",
				"destination_prefix_list_id":  "opt",
			},
		},
	})
}

// TestCheckWithSchemaAdmitsTypeOutsideTable is the acceptance case: a type
// with no row in admittedTypesV0 passes CheckWith once schemas describe it
// completely enough, and is refused exactly as before when no schemas are
// offered - admitted() only ever grows with schemas, never shrinks.
func TestCheckWithSchemaAdmitsTypeOutsideTable(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/schema-admitted")

	if issues := CheckWith(t.Context(), cfg, Context{Schemas: thingSchema()}); len(issues) != 0 {
		t.Fatalf("aws_thing was refused even with a schema that admits it: %v", issues)
	}

	issues := CheckContext(t.Context(), cfg)
	if len(issues) != 1 || issues[0].Rule != RuleUnadmittedType {
		t.Fatalf("aws_thing was not refused with no schemas: %v", issues)
	}
}

// TestAdmittedRefusesRouteWithTableRowBypassed pins #39's guard from lint's
// own admission check, not just from the resolver: with aws_route's table
// row removed for the duration of this test, admitted() falls through to
// the schema fallback, and the fallback has to refuse it for the same
// reason [identity.SynthesizeTypeIdentity]'s own test does - route_table_id
// alone names a route table, not a route.
//
// The table row is bypassed rather than left in place because admitted()
// checks the table first: aws_route is in admittedTypesV0 today, so a
// fixture-only test would never reach the fallback at all.
func TestAdmittedRefusesRouteWithTableRowBypassed(t *testing.T) {
	if _, inTable := admittedTypesV0["aws_route"]; !inTable {
		t.Fatal("aws_route is not in the table; this test no longer bypasses anything")
	}
	delete(admittedTypesV0, "aws_route")
	t.Cleanup(func() { admittedTypesV0["aws_route"] = struct{}{} })

	cfg := loadConfigDir(t, "testdata/schema-admitted-route-bypassed")
	signal, diags := identity.ScanConfig(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("scanning the fixture: %s", diags.Err())
	}

	if admitted("aws_route", routeSchema(), signal) {
		t.Fatal("aws_route was synthesized from route_table_id alone, which #39 exists to refuse")
	}
}

// TestCheckWithRefusesRouteInIdentityLayerVoice is the same guard exercised
// through the full CheckWith path, checking that the refusal's wording
// changes once schemas are offered: the plain "not in the table" sentence
// nil-schema callers have always gotten gains the identity layer's own
// clause about aws_route's schema, via [identity.SchemaRefusal], rather than
// staying silent about why schemas did not save it.
func TestCheckWithRefusesRouteInIdentityLayerVoice(t *testing.T) {
	delete(admittedTypesV0, "aws_route")
	t.Cleanup(func() { admittedTypesV0["aws_route"] = struct{}{} })

	cfg := loadConfigDir(t, "testdata/schema-admitted-route-bypassed")
	schemas := routeSchema()

	// "The provider" only ever appears in the clause [identity.SchemaRefusal]
	// adds; the base "not in the table" sentence never says it, so it is a
	// safe marker for "this refusal talked about schemas" without pinning
	// exact wording. ("identity schema" is not safe: the base sentence's own
	// closing clause, "...and, later, provider identity schemas", contains
	// it regardless of whether any were offered.)
	const schemaVoiceMarker = "The provider"

	withoutSchemas := CheckContext(t.Context(), cfg)
	if len(withoutSchemas) != 1 || withoutSchemas[0].Rule != RuleUnadmittedType {
		t.Fatalf("expected one unadmitted-type issue with no schemas, got %v", withoutSchemas)
	}
	if strings.Contains(withoutSchemas[0].Detail, schemaVoiceMarker) {
		t.Errorf("a run with no schemas explained itself in terms of schemas: %s", withoutSchemas[0].Detail)
	}

	withSchemas := CheckWith(t.Context(), cfg, Context{Schemas: schemas})
	if len(withSchemas) != 1 || withSchemas[0].Rule != RuleUnadmittedType {
		t.Fatalf("expected one unadmitted-type issue with schemas that still refuse the type, got %v", withSchemas)
	}
	if !strings.Contains(withSchemas[0].Detail, schemaVoiceMarker) {
		t.Errorf("a run with schemas that refused the type did not explain itself in terms of schemas: %s", withSchemas[0].Detail)
	}
}

// TestCheckWithNilSchemasByteIdenticalToCheckContext pins the property the
// whole #45 design rests on: a caller that offers no schemas gets exactly
// what CheckContext has always given, over every fixture in this package,
// full [Issue] value included rather than just the summary [assertIssues]
// checks elsewhere in this file. CheckContext is defined as CheckWith
// called with a zero [Context], so this also nails that definition down
// against a future change that gives Context another field and forgets to
// keep the two in sync.
func TestCheckWithNilSchemasByteIdenticalToCheckContext(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("reading testdata: %s", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join("testdata", name))

			withContext := CheckContext(t.Context(), cfg)
			withNilSchemas := CheckWith(t.Context(), cfg, Context{})

			if diff := cmp.Diff(withContext, withNilSchemas, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("CheckWith with no schemas disagreed with CheckContext\n%s", diff)
			}
		})
	}
}
