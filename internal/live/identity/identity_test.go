// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// estateDir is the P0.1 fixture, the estate every later phase plans
// against. Identity resolution is expected to classify all of it without
// touching a cloud.
func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

// TestResolveEstate pins the exact classification of every resource
// instance in the fixture. Every expectation here is a claim about what
// OpenTofu can know before it has read anything: an import ID spelled out
// in full for the concrete ones, the composition formula for the
// parent-derived ones, and nothing at all for the ones only discovery can
// find.
func TestResolveEstate(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	want := map[string]string{
		// Client-named: the name in config is the import ID.
		`aws_s3_bucket.data`:           `CONCRETE tofu-stateless-e2e-data`,
		`aws_iam_role.app`:             `CONCRETE tofu-stateless-e2e-app`,
		`aws_cloudwatch_log_group.app`: `CONCRETE /stateless-e2e/app`,

		// count = var.enabled ? 1 : 0 with enabled defaulting to true, so
		// exactly one instance, at index 0.
		`aws_cloudwatch_log_group.optional[0]`: `CONCRETE /stateless-e2e/optional`,

		// Named singleton child: bucket = aws_s3_bucket.data.id, and the
		// bucket's id is its (client-named) bucket name, so this collapses
		// to a literal instead of becoming a formula.
		`aws_s3_bucket_policy.data`: `CONCRETE tofu-stateless-e2e-data`,

		// The four S3 bucket children (#19's second slice): the same
		// named-singleton-child collapse as the bucket policy above.
		`aws_s3_bucket_versioning.data`:                           `CONCRETE tofu-stateless-e2e-data`,
		`aws_s3_bucket_public_access_block.data`:                  `CONCRETE tofu-stateless-e2e-data`,
		`aws_s3_bucket_server_side_encryption_configuration.data`: `CONCRETE tofu-stateless-e2e-data`,
		`aws_s3_bucket_lifecycle_configuration.data`:              `CONCRETE tofu-stateless-e2e-data`,

		// Same slice: a concrete composite (both halves client-chosen,
		// joined by the provider's documented colon) and a client-named
		// alias whose name argument is the whole import ID.
		`aws_iam_role_policy.app`: `CONCRETE tofu-stateless-e2e-app:tofu-stateless-e2e-app-inline`,
		`aws_kms_alias.main`:      `CONCRETE alias/tofu-stateless-e2e-main`,

		// Same slice: an alarm named by its alarm_name argument.
		`aws_cloudwatch_metric_alarm.cpu`: `CONCRETE tofu-stateless-e2e-cpu`,

		// Same slice, via #20's zone: name and type are config data, the
		// Z-ID is live, and the provider's import syntax joins the three
		// with underscores.
		`aws_route53_record.app`: `PARENT_DERIVED ${aws_route53_zone.main.zone_id}_app.stateless-e2e.example.com_A`,

		// Attachment composite: role name comes from the concrete role,
		// policy ARN is a literal, so the whole composite is concrete.
		`aws_iam_role_policy_attachment.app`: `CONCRETE tofu-stateless-e2e-app/arn:aws:iam::aws:policy/ReadOnlyAccess`,

		// Receipts (PE.3, RA.6): client-named the same way a bucket or role
		// is — the name argument is a literal parameter path. Two flavors,
		// same identity shape.
		`aws_ssm_parameter.demo_effect`:    `CONCRETE /tofu-receipts/stateless-e2e/demo-effect`,
		`aws_ssm_parameter.demo_existence`: `CONCRETE /tofu-receipts/stateless-e2e/demo-existence`,

		// First slice of the survey's client-named cohort (#19): a table
		// named by its name argument, and a cluster whose import ID is the
		// name even though the provider's id attribute is the ARN.
		`aws_dynamodb_table.events`: `CONCRETE tofu-stateless-e2e-events`,
		`aws_ecs_cluster.app`:       `CONCRETE tofu-stateless-e2e-cluster`,

		// Parent-derived: the route table ID is live, the destination is
		// config data, and the provider's import syntax joins them with an
		// underscore.
		`aws_route.internet_gateway`: `PARENT_DERIVED ${aws_route_table.main.id}_0.0.0.0/0`,

		// Parent-derived, one per subnet key. The keys are propagated from
		// aws_subnet.this's for_each, and each.value.id is that subnet
		// instance's live ID.
		`aws_route_table_association.this["a"]`: `PARENT_DERIVED ${aws_subnet.this["a"].id}/${aws_route_table.main.id}`,
		`aws_route_table_association.this["b"]`: `PARENT_DERIVED ${aws_subnet.this["b"].id}/${aws_route_table.main.id}`,

		// Server-assigned: nothing in config names these.
		`aws_vpc.main`:              `NEEDS_DISCOVERY`,
		`aws_subnet.this["a"]`:      `NEEDS_DISCOVERY`,
		`aws_subnet.this["b"]`:      `NEEDS_DISCOVERY`,
		`aws_security_group.main`:   `NEEDS_DISCOVERY`,
		`aws_route_table.main`:      `NEEDS_DISCOVERY`,
		`aws_internet_gateway.main`: `NEEDS_DISCOVERY`,
		`aws_eip.pool[0]`:           `NEEDS_DISCOVERY`,
		`aws_eip.pool[1]`:           `NEEDS_DISCOVERY`,
		`aws_eip.pool[2]`:           `NEEDS_DISCOVERY`,

		// First slice of the survey's marker cohort (#20): a KMS key whose
		// UUID nothing in config names, and a hosted zone whose ID Route 53
		// mints even though the zone's domain name is right there in the
		// block.
		`aws_kms_key.main`:      `NEEDS_DISCOVERY`,
		`aws_route53_zone.main`: `NEEDS_DISCOVERY`,

		// Second slice of the marker cohort (#20): three ELBv2 objects, each
		// named in configuration and each identified by an ARN ELBv2 mints.
		`aws_lb.main`:             `NEEDS_DISCOVERY`,
		`aws_lb_target_group.app`: `NEEDS_DISCOVERY`,
		`aws_lb_listener.app`:     `NEEDS_DISCOVERY`,

		// Third slice of the marker cohort (#20): per-rule security group
		// resources, a launch template and an EBS volume (all EC2-minted
		// IDs), plus an ACM certificate and a Step Functions state machine
		// (ARN-identified, like the ELBv2 chain).
		`aws_vpc_security_group_ingress_rule.https`: `NEEDS_DISCOVERY`,
		`aws_vpc_security_group_egress_rule.all`:    `NEEDS_DISCOVERY`,
		`aws_launch_template.app`:                   `NEEDS_DISCOVERY`,
		`aws_acm_certificate.app`:                   `NEEDS_DISCOVERY`,
		`aws_sfn_state_machine.pipeline`:            `NEEDS_DISCOVERY`,
		`aws_ebs_volume.data`:                       `NEEDS_DISCOVERY`,

		// #21's parent-derived slice: the target group's ARN is live, the
		// target and port are config data, and the provider's import ID
		// joins the three with commas.
		`aws_lb_target_group_attachment.app`: `PARENT_DERIVED ${aws_lb_target_group.app.arn},10.42.1.55,80`,

		// Account-derived (SURVEY.md flag F2): the name is right there in
		// configuration, and the import identity wraps it in an account and a
		// region this run was not given. Resolve passes no CloudContext, so
		// it defers to marker discovery rather than erroring -
		// TestResolveInCloudContext is the other half of this.
		`aws_sns_topic.alerts`: `NEEDS_DISCOVERY`,
	}

	assertClassifications(t, result, want)
}

// TestResolveEstateDisabled checks the conditional idiom from the other
// side: with enabled = false the optional log group expands to zero
// instances and simply is not in the result, rather than appearing with an
// empty identity.
func TestResolveEstateDisabled(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), map[string]cty.Value{
		"enabled": cty.False,
	})

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	if _, ok := result.Get(mustAddr(t, `aws_cloudwatch_log_group.optional[0]`)); ok {
		t.Error("aws_cloudwatch_log_group.optional[0] is present with enabled = false; count = 0 must expand to no instances")
	}
	if got, want := result.Len(), 42; got != want {
		t.Errorf("resolved %d instances, want %d", got, want)
	}
	if _, ok := result.Get(mustAddr(t, `aws_cloudwatch_log_group.app`)); !ok {
		t.Error("the unconditional log group disappeared along with the conditional one")
	}
}

// TestEstateNeedsDiscoveryList checks the roadmap's second output: the list
// of resources P2's marker discovery has to find.
func TestEstateNeedsDiscoveryList(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	var got []string
	for _, r := range result.NeedsDiscovery() {
		got = append(got, r.Addr.String())
		if r.Reason == "" {
			t.Errorf("%s is NEEDS_DISCOVERY with no reason", r.Addr)
		}
		if r.ImportID != "" || r.Formula != nil {
			t.Errorf("%s is NEEDS_DISCOVERY but carries an identity payload", r.Addr)
		}
	}

	want := []string{
		`aws_acm_certificate.app`,
		`aws_ebs_volume.data`,
		`aws_eip.pool[0]`,
		`aws_eip.pool[1]`,
		`aws_eip.pool[2]`,
		`aws_internet_gateway.main`,
		`aws_kms_key.main`,
		`aws_launch_template.app`,
		`aws_lb.main`,
		`aws_lb_listener.app`,
		`aws_lb_target_group.app`,
		`aws_route53_zone.main`,
		`aws_route_table.main`,
		`aws_security_group.main`,
		`aws_sfn_state_machine.pipeline`,
		`aws_sns_topic.alerts`,
		`aws_subnet.this["a"]`,
		`aws_subnet.this["b"]`,
		`aws_vpc.main`,
		`aws_vpc_security_group_egress_rule.all`,
		`aws_vpc_security_group_ingress_rule.https`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("needs-discovery list mismatch\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestEstateFormulaParents checks the machine-readable half of a formula:
// the parent set P1.3 has to resolve before it can render an import ID.
func TestEstateFormulaParents(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	tests := map[string][]string{
		`aws_route.internet_gateway`:            {`aws_route_table.main`},
		`aws_route_table_association.this["a"]`: {`aws_route_table.main`, `aws_subnet.this["a"]`},
		`aws_route_table_association.this["b"]`: {`aws_route_table.main`, `aws_subnet.this["b"]`},
		`aws_lb_target_group_attachment.app`:    {`aws_lb_target_group.app`},
	}
	for addr, wantParents := range tests {
		res, ok := result.Get(mustAddr(t, addr))
		if !ok {
			t.Fatalf("%s missing from result", addr)
		}
		var got []string
		for _, p := range res.Formula.Parents {
			got = append(got, p.String())
		}
		if strings.Join(got, ",") != strings.Join(wantParents, ",") {
			t.Errorf("%s parents = %v, want %v", addr, got, wantParents)
		}
	}
}

// TestFormulaRender exercises the P1.3-facing half of a formula: hand it
// live IDs and it produces the provider's import ID; withhold one and it
// refuses rather than producing a string with a hole in it.
func TestFormulaRender(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	// Keyed by parent address and identity attribute: the ELBv2 chain reads
	// its parent's arn where the EC2 types read an id, and a formula that
	// asked for the wrong one must not render.
	live := map[string]string{
		`aws_route_table.main` + "\x00id":     "rtb-0a1b2c3d",
		`aws_subnet.this["a"]` + "\x00id":     "subnet-1111",
		`aws_subnet.this["b"]` + "\x00id":     "subnet-2222",
		`aws_lb_target_group.app` + "\x00arn": "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/tofu-stateless-e2e-tg/73d2c",
	}
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		v, ok := live[parent.String()+"\x00"+attr]
		return v, ok
	}

	tests := map[string]string{
		`aws_route.internet_gateway`:            "rtb-0a1b2c3d_0.0.0.0/0",
		`aws_route_table_association.this["a"]`: "subnet-1111/rtb-0a1b2c3d",
		`aws_route_table_association.this["b"]`: "subnet-2222/rtb-0a1b2c3d",
		`aws_lb_target_group_attachment.app`:    "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/tofu-stateless-e2e-tg/73d2c,10.42.1.55,80",
	}
	for addr, want := range tests {
		res, ok := result.Get(mustAddr(t, addr))
		if !ok {
			t.Fatalf("%s missing from result", addr)
		}
		got, ok := res.Formula.Render(lookup)
		if !ok {
			t.Errorf("%s: render reported an unknown parent", addr)
			continue
		}
		if got != want {
			t.Errorf("%s: import ID = %q, want %q", addr, got, want)
		}
	}

	// A parent whose live ID is not known yet must not render.
	res, _ := result.Get(mustAddr(t, `aws_route.internet_gateway`))
	if _, ok := res.Formula.Render(func(addrs.AbsResourceInstance, string) (string, bool) {
		return "", false
	}); ok {
		t.Error("render succeeded with an unknown parent ID")
	}
}

// TestResolveInCloudContext is the account-derived mechanism's own test: the
// same two blocks that defer to marker discovery under [Resolve] resolve
// concrete under [ResolveIn] once the run says which account and region it is
// against, and the strings they resolve to are the provider's real import
// identities.
func TestResolveInCloudContext(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := ResolveIn(context.Background(), cfg, CloudContext{
		AccountID: "000000000000",
		Region:    "us-east-1",
	})
	assertNoErrors(t, diags)

	want := map[string]string{
		`aws_sns_topic.alerts`: `arn:aws:sns:us-east-1:000000000000:tofu-stateless-e2e-alerts`,
	}
	for addr, wantID := range want {
		res, ok := result.Get(mustAddr(t, addr))
		if !ok {
			t.Fatalf("%s missing from the result", addr)
		}
		if res.Class != ClassConcrete {
			t.Errorf("%s = %s with a cloud context, want CONCRETE", addr, res.Class)
			continue
		}
		if res.ImportID != wantID {
			t.Errorf("%s import ID = %q, want %q", addr, res.ImportID, wantID)
		}
	}

	// Nothing else moved: a cloud context changes the classification of the
	// types whose identity embeds one of its values and of nothing else.
	if res, _ := result.Get(mustAddr(t, `aws_kms_key.main`)); res.Class != ClassNeedsDiscovery {
		t.Errorf("aws_kms_key.main = %s with a cloud context, want NEEDS_DISCOVERY", res.Class)
	}
	if res, _ := result.Get(mustAddr(t, `aws_s3_bucket.data`)); res.Class != ClassConcrete {
		t.Errorf("aws_s3_bucket.data = %s with a cloud context, want CONCRETE", res.Class)
	}
}

// TestResolveInPartialCloudContext pins the halfway case: a run that knows
// the region and not the account still cannot name an SQS queue, and says
// which value it is missing rather than building a URL with a hole in it.
func TestResolveInPartialCloudContext(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	result, diags := ResolveIn(context.Background(), cfg, CloudContext{Region: "us-east-1"})
	assertNoErrors(t, diags)

	res, ok := result.Get(mustAddr(t, `aws_sns_topic.alerts`))
	if !ok {
		t.Fatal("aws_sns_topic.alerts missing from the result")
	}
	if res.Class != ClassNeedsDiscovery {
		t.Fatalf("aws_sns_topic.alerts = %s with no account ID, want NEEDS_DISCOVERY", res.Class)
	}
	if !strings.Contains(res.Reason, "AWS account ID") {
		t.Errorf("reason does not name the missing value: %q", res.Reason)
	}
	if res.ImportID != "" {
		t.Errorf("a needs-discovery resolution carries an import ID: %q", res.ImportID)
	}
}

func TestResolveErrors(t *testing.T) {
	tests := []struct {
		dir string
		// wantSummary is the diagnostic summary the failure must carry.
		wantSummary string
		// wantDetail is a fragment the message must name, so that the
		// error points at the actual construct rather than being generic.
		wantDetail string
		// wantAbsent is an address that must not appear in the result: a
		// failed resolution is an omission plus an error, never a guess.
		wantAbsent string
	}{
		{
			dir:         "unknown-type",
			wantSummary: "Resource type outside the live-markers subset",
			wantDetail:  `"aws_customer_gateway"`,
			wantAbsent:  `aws_customer_gateway.app`,
		},
		{
			dir:         "unevaluable-name",
			wantSummary: "Unable to compute static value",
			wantDetail:  `var.suffix`,
			wantAbsent:  `aws_s3_bucket.data`,
		},
		{
			dir:         "non-identity-attr",
			wantSummary: "Not an identity attribute",
			wantDetail:  `"cidr_block" is not an identity attribute of aws_vpc`,
			wantAbsent:  `aws_s3_bucket_policy.data`,
		},
		{
			dir:         "computed-for-each",
			wantSummary: "Non-static for_each expression",
			wantDetail:  `aws_route_table_association.this`,
			wantAbsent:  `aws_route_table_association.this["a"]`,
		},
		{
			dir:         "dynamic-count",
			wantSummary: "Dynamic value in static context",
			wantDetail:  `aws_eip.pool`,
			wantAbsent:  `aws_cloudwatch_log_group.per_eip[0]`,
		},
		{
			dir:         "computed-expression",
			wantSummary: "Identity not resolvable from configuration",
			wantDetail:  `aws_cloudwatch_log_group.app.name`,
			wantAbsent:  `aws_cloudwatch_log_group.app`,
		},
		{
			dir:         "missing-identity-arg",
			wantSummary: "Identity argument not set",
			wantDetail:  `"bucket"`,
			wantAbsent:  `aws_s3_bucket.data`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			cfg := loadConfig(t, filepath.Join("testdata", tc.dir), nil)
			result, diags := Resolve(context.Background(), cfg)

			if !diags.HasErrors() {
				t.Fatalf("no error diagnostics; resolution produced %d instances", result.Len())
			}
			if !hasDiag(diags, tc.wantSummary, tc.wantDetail) {
				t.Errorf("no diagnostic with summary %q naming %q. got:\n%s", tc.wantSummary, tc.wantDetail, renderDiags(diags))
			}
			if _, ok := result.Get(mustAddr(t, tc.wantAbsent)); ok {
				t.Errorf("%s was resolved anyway; an unresolvable identity must be omitted, not guessed", tc.wantAbsent)
			}
		})
	}
}

// TestDuplicateIdentity checks the ambiguity rule: two resources of one
// type resolving to one identity is an error naming both, not a silent
// double binding.
func TestDuplicateIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "duplicate-identity"), nil)
	_, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatal("two resources sharing a bucket name were accepted")
	}
	if !hasDiag(diags, "Two resources with the same identity", `aws_s3_bucket.one and aws_s3_bucket.two both resolve to the identity "estate-shared"`) {
		t.Errorf("wrong diagnostic:\n%s", renderDiags(diags))
	}
}

// TestDisabledLifecycle covers the fork's lifecycle.enabled meta-argument
// as an expansion input: false means zero instances, not an instance with
// no identity.
func TestDisabledLifecycle(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "disabled-lifecycle"), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_s3_bucket.on`: `CONCRETE estate-on`,
	})
}

// TestChildModulesRejected checks that a configuration with child modules
// is refused outright rather than resolved as if the modules were not
// there, which would silently omit every resource inside them.
//
// The refusal an operator actually reads is lint's RuleChildModule, which
// names the module block and points at it; this one is the invariant behind
// it, and says so, because a configuration that got this far went past lint.
func TestChildModulesRejected(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)
	cfg.Children = map[string]*configs.Config{"network": {}}

	result, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("child modules were accepted")
	}
	if !hasDiag(diags, "Configuration with child modules reached identity resolution", `"network"`) {
		t.Errorf("wrong diagnostic:\n%s", renderDiags(diags))
	}
	if result.Len() != 0 {
		t.Errorf("resolved %d instances despite refusing the configuration", result.Len())
	}
}

// TestTableCoversFixtureTypes keeps the v0 table and the fixtures in step:
// the table exists to cover exactly the union of the demo estate and every
// per-cohort verification estate under live/e2e/estates (#48, phase 3 of
// #38's decision), and a type added to one side without the other is the
// kind of drift that shows up later as a mystery NEEDS_DISCOVERY. The
// universe is read straight off the fixtures rather than pinned as a
// hardcoded count, so a new estates/<cohort> directory extends it with no
// test-file edits.
func TestTableCoversFixtureTypes(t *testing.T) {
	used := make(map[string]bool)
	for _, dir := range flocitest.FixtureDirs(t) {
		cfg := loadConfig(t, dir, nil)
		for _, rc := range cfg.Module.ManagedResources {
			used[rc.Type] = true
		}
	}
	for typeName := range used {
		if _, ok := LookupType(typeName); !ok {
			t.Errorf("a fixture uses %s, which the v0 identity table does not cover", typeName)
		}
	}
	for _, typeName := range AdmittedTypes() {
		if !used[typeName] {
			t.Errorf("the v0 identity table covers %s, which no fixture uses", typeName)
		}
	}
	if got, want := len(AdmittedTypes()), len(used); got != want {
		t.Errorf("table covers %d types, want the fixtures' %d", got, want)
	}
}

func TestTableEntriesWellFormed(t *testing.T) {
	for _, typeName := range AdmittedTypes() {
		entry, _ := LookupType(typeName)
		if entry.Type != typeName {
			t.Errorf("%s: entry is keyed as %s", typeName, entry.Type)
		}
		if entry.ImportSyntax == "" {
			t.Errorf("%s: no documented import syntax", typeName)
		}
		if entry.ServerAssigned {
			if entry.Reason == "" {
				t.Errorf("%s: server-assigned with no reason for operators", typeName)
			}
			if len(entry.Components) != 0 {
				t.Errorf("%s: server-assigned but carries identity components", typeName)
			}
			continue
		}
		if len(entry.Components) == 0 {
			t.Errorf("%s: no identity components and not server-assigned", typeName)
		}
		for i, comp := range entry.Components {
			switch {
			case comp.Cloud != CloudNone:
				if comp.Cloud != CloudAccountID && comp.Cloud != CloudRegion {
					t.Errorf("%s: component %d names the unknown cloud value %q", typeName, i, comp.Cloud)
				}
				if len(comp.Attrs) != 0 || comp.Literal != "" {
					t.Errorf("%s: component %d is a cloud value and also carries an argument or a separator", typeName, i)
				}
			case len(comp.Attrs) == 0 && comp.Literal == "":
				t.Errorf("%s: component %d is neither an argument, a separator, nor a cloud value", typeName, i)
			}
		}
	}
}

// TestCloudComponentsHaveAnEmptyContextAnswer is the property the whole
// mechanism rests on: an entry that names a cloud value must classify
// needs-discovery when the run supplies none, never error and never build a
// half-rendered identity. Stated over the table rather than over the two
// types that have such components today, so a third one inherits the check.
func TestCloudComponentsHaveAnEmptyContextAnswer(t *testing.T) {
	var empty CloudContext
	for _, typeName := range AdmittedTypes() {
		entry, _ := LookupType(typeName)
		for _, comp := range entry.Components {
			if comp.Cloud == CloudNone {
				continue
			}
			if _, ok := empty.value(comp.Cloud); ok {
				t.Errorf("%s: the zero CloudContext claims to know its %s", typeName, comp.Cloud)
			}
			if missing, ok := (&resolver{}).missingCloudValue(entry); !ok || missing == CloudNone {
				t.Errorf("%s: an empty context does not report a missing cloud value", typeName)
			}
			if reason := cloudReason(entry, comp.Cloud); !strings.Contains(reason, entry.ImportSyntax) {
				t.Errorf("%s: the needs-discovery reason does not show the import syntax: %q", typeName, reason)
			}
		}
	}
}

// ---- helpers ---------------------------------------------------------

// loadConfig loads a configuration directory the way the CLI does, with
// input variable values coming from the caller and falling back to declared
// defaults. A required variable with no value produces an error at use
// time, which is what the unevaluable-name case relies on.
func loadConfig(t *testing.T, dir string, vars map[string]cty.Value) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if val, ok := vars[v.Name]; ok {
				return val, nil
			}
			if v.Required() {
				return cty.NilVal, hcl.Diagnostics{{
					Severity: hcl.DiagError,
					Summary:  "No value for required variable",
					Detail:   fmt.Sprintf("The root module input variable %q is not set.", v.Name),
					Subject:  v.DeclRange.Ptr(),
				}}
			}
			return v.Default, nil
		},
		dir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("test fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// assertClassifications compares the whole result against an expected map
// of address to "CLASS payload", so that an unexpected extra instance fails
// just as loudly as a wrong one.
func assertClassifications(t *testing.T, result *Result, want map[string]string) {
	t.Helper()

	got := make(map[string]string, result.Len())
	for _, r := range result.All() {
		switch r.Class {
		case ClassConcrete:
			got[r.Addr.String()] = "CONCRETE " + r.ImportID
		case ClassParentDerived:
			got[r.Addr.String()] = "PARENT_DERIVED " + r.Formula.String()
		case ClassNeedsDiscovery:
			got[r.Addr.String()] = "NEEDS_DISCOVERY"
		}
	}

	var addrList []string
	for addr := range want {
		addrList = append(addrList, addr)
	}
	for addr := range got {
		if _, ok := want[addr]; !ok {
			addrList = append(addrList, addr)
		}
	}
	sort.Strings(addrList)

	for _, addr := range addrList {
		w, inWant := want[addr]
		g, inGot := got[addr]
		switch {
		case !inGot:
			t.Errorf("%s: missing from result, want %q", addr, w)
		case !inWant:
			t.Errorf("%s: unexpected in result, got %q", addr, g)
		case g != w:
			t.Errorf("%s:\n got %q\nwant %q", addr, g, w)
		}
	}
}

func assertNoErrors(t *testing.T, diags tfdiags.Diagnostics) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics:\n%s", renderDiags(diags))
	}
}

func hasDiag(diags tfdiags.Diagnostics, summary, detailFragment string) bool {
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == summary && strings.Contains(desc.Detail, detailFragment) {
			return true
		}
	}
	return false
}

func renderDiags(diags tfdiags.Diagnostics) string {
	var buf strings.Builder
	for _, d := range diags {
		desc := d.Description()
		fmt.Fprintf(&buf, "- [%s] %s: %s\n", d.Severity(), desc.Summary, desc.Detail)
	}
	return buf.String()
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}
