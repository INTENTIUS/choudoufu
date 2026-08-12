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

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
	"github.com/opentofu/opentofu/internal/tfdiags"
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
	if got, want := result.Len(), 21; got != want {
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
		`aws_eip.pool[0]`,
		`aws_eip.pool[1]`,
		`aws_eip.pool[2]`,
		`aws_internet_gateway.main`,
		`aws_route_table.main`,
		`aws_security_group.main`,
		`aws_subnet.this["a"]`,
		`aws_subnet.this["b"]`,
		`aws_vpc.main`,
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

	live := map[string]string{
		`aws_route_table.main`: "rtb-0a1b2c3d",
		`aws_subnet.this["a"]`: "subnet-1111",
		`aws_subnet.this["b"]`: "subnet-2222",
	}
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		if attr != "id" {
			return "", false
		}
		v, ok := live[parent.String()]
		return v, ok
	}

	tests := map[string]string{
		`aws_route.internet_gateway`:            "rtb-0a1b2c3d_0.0.0.0/0",
		`aws_route_table_association.this["a"]`: "subnet-1111/rtb-0a1b2c3d",
		`aws_route_table_association.this["b"]`: "subnet-2222/rtb-0a1b2c3d",
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
			wantDetail:  `"aws_instance"`,
			wantAbsent:  `aws_instance.app`,
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

// TestTableCoversFixtureTypes keeps the v0 table and the fixture in step:
// the table exists to cover exactly the estate's types, and a type added to
// one without the other is the kind of drift that shows up later as a
// mystery NEEDS_DISCOVERY.
func TestTableCoversFixtureTypes(t *testing.T) {
	cfg := loadConfig(t, estateDir(t), nil)

	used := make(map[string]bool)
	for _, rc := range cfg.Module.ManagedResources {
		used[rc.Type] = true
	}
	for typeName := range used {
		if _, ok := LookupType(typeName); !ok {
			t.Errorf("fixture uses %s, which the v0 identity table does not cover", typeName)
		}
	}
	for _, typeName := range AdmittedTypes() {
		if !used[typeName] {
			t.Errorf("the v0 identity table covers %s, which the fixture does not use", typeName)
		}
	}
	if got, want := len(AdmittedTypes()), 16; got != want {
		t.Errorf("table covers %d types, want the fixture's %d", got, want)
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
			if len(comp.Attrs) == 0 && comp.Literal == "" {
				t.Errorf("%s: component %d is neither an argument nor a separator", typeName, i)
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
