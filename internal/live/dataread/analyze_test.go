// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// loadConfig loads a fixture directory the way the CLI does, the same
// helper shape identity's tests use.
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

// loadConfigTree is [loadConfig] with a walker that actually resolves child
// modules by treating a module call's source address as a path relative to
// the calling module's own directory - the same reading
// internal/live/identity/identity_test.go's own loadConfigTree uses. Every
// existing fixture uses [loadConfig], whose walker fails the test the
// moment anything calls a module; a fixture with a real module tree (issue
// #212's cross-module-* fixtures) needs this one instead.
func loadConfigTree(t *testing.T, dir string, vars map[string]cty.Value) *configs.Config {
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

	dirs := map[string]string{"": dir}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			parentDir := dirs[req.Parent.Path.String()]
			sourcePath := filepath.Join(parentDir, req.SourceAddr.String())
			dirs[req.Path.String()] = sourcePath

			childMod, modDiags := parser.LoadConfigDir(sourcePath, req.Call)
			return childMod, nil, modDiags
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

func dataAddr(typeName, name string) addrs.Resource {
	return addrs.Resource{Mode: addrs.DataResourceMode, Type: typeName, Name: name}
}

func analyzeFixture(t *testing.T, name string) *Analysis {
	t.Helper()
	cfg := loadConfig(t, filepath.Join("testdata", name), nil)
	return Analyze(context.Background(), cfg, Options{})
}

// TestAnalyzeDemandIsTransitiveAndDemandDriven: both links of a chain are
// demanded, in dependency order, and a declared-but-unreferenced data
// source is not read at all - demand comes from what identity resolution
// asks for, not from what the configuration declares.
func TestAnalyzeDemandIsTransitiveAndDemandDriven(t *testing.T) {
	a := analyzeFixture(t, "eligible-chain")

	var order []string
	for _, src := range a.Demanded() {
		order = append(order, src.Resource.String())
		if !src.Eligible {
			t.Errorf("%s classified ineligible: %s", src.Resource.String(), src.ReasonDetail)
		}
	}
	want := []string{"data.aws_route53_zone.primary", "data.aws_route53_zone.sub"}
	if len(order) != len(want) {
		t.Fatalf("demanded %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("demanded order %v, want %v (dependencies must come first)", order, want)
		}
	}
	if _, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_region", "unused")); ok {
		t.Errorf("data.aws_region.unused was demanded; nothing identity-bearing references it")
	}
}

// TestAnalyzeManagedReferenceIsIneligible: eligibility rule 4 through the
// arguments, with the class wording naming the managed dependency.
func TestAnalyzeManagedReferenceIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-managed")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "of_instance"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source reading a managed resource classified eligible")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	for _, part := range []string{"needed to resolve the identity of", "managed resource", "cannot be read before the plan"} {
		if !strings.Contains(src.ReasonDetail, part) {
			t.Errorf("the wording lacks %q: %s", part, src.ReasonDetail)
		}
	}
}

// TestAnalyzeDependsOnManagedIsIneligible: rule 4's depends_on half.
func TestAnalyzeDependsOnManagedIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-depends-on")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "gated"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source with depends_on naming a managed resource classified eligible")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	if !strings.Contains(src.ReasonDetail, "depends_on") {
		t.Errorf("the wording does not name depends_on: %s", src.ReasonDetail)
	}
}

// TestAnalyzeSelfForEachArgumentIsEligiblePerInstance: a for_each-expanded
// data source whose own argument reads its own each.value is ordinary
// per-block repetition scoping, not a dynamic value (#193) - it classifies
// eligible, and PerInstance so Read knows the instances can genuinely
// differ.
func TestAnalyzeSelfForEachArgumentIsEligiblePerInstance(t *testing.T) {
	a := analyzeFixture(t, "eligible-self-each-count")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "by_each"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("a for_each block reading its own each.value classified ineligible: %s", src.ReasonDetail)
	}
	if !src.PerInstance {
		t.Fatalf("a for_each block reading its own each.value was not marked PerInstance")
	}
}

// TestAnalyzeSelfCountArgumentIsEligiblePerInstance: count's count.index
// half of the same rule.
func TestAnalyzeSelfCountArgumentIsEligiblePerInstance(t *testing.T) {
	a := analyzeFixture(t, "eligible-self-each-count")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "by_count"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("a count block reading its own count.index classified ineligible: %s", src.ReasonDetail)
	}
	if !src.PerInstance {
		t.Fatalf("a count block reading its own count.index was not marked PerInstance")
	}
}

// TestAnalyzeArgumentInvariantAcrossInstancesIsNotPerInstance: the common
// case - a for_each-expanded block whose arguments do not read each/count
// at all - must not be marked PerInstance, or every for_each/count block in
// the corpus would silently lose the one-call-per-block cost bound
// [doc.go] promises.
func TestAnalyzeArgumentInvariantAcrossInstancesIsNotPerInstance(t *testing.T) {
	a := analyzeFixture(t, "read-expansion")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_record", "b"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.test_record.b classified ineligible: %s", src.ReasonDetail)
	}
	if src.PerInstance {
		t.Fatalf("data.test_record.b's arguments never read count.index, but it was marked PerInstance")
	}
}

// TestAnalyzeEachWithoutForEachStaysRefused: the self-repetition relaxation
// only applies to a block's OWN count/for_each. each.value in a block with
// no for_each set at all is not this block's own repetition value - it
// stays refused exactly as it always has, matching stock OpenTofu's own
// "each.value cannot be used in this context" error for the same
// construct.
func TestAnalyzeEachWithoutForEachStaysRefused(t *testing.T) {
	a := analyzeFixture(t, "ineligible-each-without-for-each")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "no_for_each"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("each.value with no for_each on the block classified eligible")
	}
	if src.PerInstance {
		t.Fatalf("each.value with no for_each on the block was marked PerInstance")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
}

// TestAnalyzeNonStaticProviderIsIneligible: rule 3, the ConfiguredProvider
// line, classified offline.
func TestAnalyzeNonStaticProviderIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-provider")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "primary"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source whose provider block reads a resource classified eligible")
	}
	if src.ReasonSummary != SummaryProviderNotConfigurable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryProviderNotConfigurable)
	}
	if !strings.Contains(src.ReasonDetail, "provider.aws") {
		t.Errorf("the wording does not name the provider configuration: %s", src.ReasonDetail)
	}
}

// TestAnalyzeCycleIsIneligible: a data-to-data cycle terminates and both
// members refuse rather than looping.
func TestAnalyzeCycleIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-cycle")

	for _, name := range []string{"a", "b"} {
		src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", name))
		if !ok {
			// Only the demanded member of the cycle is guaranteed a record;
			// b is demanded through a.
			if name == "a" {
				t.Fatalf("data.aws_route53_zone.a was not classified")
			}
			continue
		}
		if src.Eligible {
			t.Fatalf("data.aws_route53_zone.%s is in a cycle and classified eligible", name)
		}
		if src.ReasonSummary != SummaryNotReadable {
			t.Errorf("%s refused under %q, want %q", name, src.ReasonSummary, SummaryNotReadable)
		}
	}
}

// TestAnalyzeTfeOutputsEligibleWithProviderToken: #179 stage 2's own rule -
// a tfe_outputs source with a static token argument in its provider block
// goes through the same eligibility pipeline a same-stack source does, and
// passes it.
func TestAnalyzeTfeOutputsEligibleWithProviderToken(t *testing.T) {
	a := analyzeFixture(t, "tfe-eligible")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("tfe_outputs", "app"))
	if !ok {
		t.Fatalf("the demanded tfe_outputs source was not classified at all")
	}
	if !src.TfeOutputs {
		t.Fatalf("a tfe_outputs source was not marked TfeOutputs")
	}
	if src.RemoteState {
		t.Fatalf("a tfe_outputs source was marked RemoteState; only terraform_remote_state should be")
	}
	if !src.Eligible {
		t.Fatalf("a tfe_outputs source with a static token argument classified ineligible: %s", src.ReasonDetail)
	}
}

// TestAnalyzeDataRefInForEachValueIsDiscovered is #209: a data source
// reference reachable only through a for_each map literal's VALUE position
// - never named directly by any identity-bearing argument, never named by
// the for_each expression's own keys - must still be classified demanded.
// Before [analyzer.scanForEachDataRefs] existed, Analyze's only discovery
// path was reading [configs.RefusedReference] diagnostics off a probe
// round's resolution attempt, and resolve.go's own #178 key-set fix
// (staticForEachKeys) discards exactly that diagnostic once it proves the
// for_each's key set is knowable without the value - the fixture's
// "content_data_api" key needs no data read at all, only the value does -
// so the data source was invisible to Analyze no matter how many rounds it
// ran. It has to be discovered by reading the for_each expression directly.
func TestAnalyzeDataRefInForEachValueIsDiscovered(t *testing.T) {
	a := analyzeFixture(t, "foreach-value-data-ref")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("tfe_outputs", "security"))
	if !ok {
		t.Fatalf("data.tfe_outputs.security, referenced only inside a for_each map value, was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.tfe_outputs.security classified ineligible: %s", src.ReasonDetail)
	}
	if src.PerInstance {
		t.Error("data.tfe_outputs.security has no count/for_each of its own and was wrongly marked PerInstance")
	}
}

// TestAnalyzeDataRefInForEachValueThroughLocalIsDiscovered is #209's second
// corpus shape (cloud-platform-infrastructure's transit-gateway module): the
// for_each names a local rather than an object constructor directly, and
// the data reference is reachable only by chasing that local's own
// definition - the identical one level of aliasing
// [resolver.staticForEachKeys] chases in resolve.go before falling back to
// a keys-only expansion.
func TestAnalyzeDataRefInForEachValueThroughLocalIsDiscovered(t *testing.T) {
	a := analyzeFixture(t, "foreach-value-data-ref-local")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("tfe_outputs", "security"))
	if !ok {
		t.Fatalf("data.tfe_outputs.security, reachable only through a local's own for_each object, was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.tfe_outputs.security classified ineligible: %s", src.ReasonDetail)
	}
}

// TestAnalyzeTfeOutputsEligibleWithNoTokenAnywhere: the maintainer's ruling
// on #181 - eligibility models the owner running the configuration, and an
// owner running tfe_outputs has a token by construction, the same treatment
// stage 1 already gives the aws provider block. The auth-surface check
// moved to read time (see read_test.go's
// TestReadTfeOutputsMissingTokenRefusesAtReadTimeWithoutCallingTheProvider),
// so Analyze - which never even configures a provider - classifies this
// eligible regardless of what credentials this process happens to have.
func TestAnalyzeTfeOutputsEligibleWithNoTokenAnywhere(t *testing.T) {
	t.Setenv("TFE_TOKEN", "")
	t.Setenv("HOME", t.TempDir()) // isolate from any real ~/.terraform.d/credentials.tfrc.json
	t.Setenv("TF_CLI_CONFIG_FILE", "")

	a := analyzeFixture(t, "tfe-missing-token")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("tfe_outputs", "app"))
	if !ok {
		t.Fatalf("the demanded tfe_outputs source was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("a tfe_outputs source with no token anywhere classified ineligible at analysis time: %s (the ruling moved this check to read time)", src.ReasonDetail)
	}
}

// TestAnalyzeRemoteStateEligibleWithStaticBackendConfig: #179 stage 3 -
// terraform_remote_state goes through the same eligibility pipeline a
// same-stack source does. Static eligibility still requires the backend
// block's own arguments (backend, config, workspace) to be statically
// evaluable; a fully literal backend and config passes. Backend
// reachability and credentials are never consulted here - the same ruling
// [TestAnalyzeTfeOutputsEligibleWithNoTokenAnywhere] pins for tfe_outputs.
func TestAnalyzeRemoteStateEligibleWithStaticBackendConfig(t *testing.T) {
	a := analyzeFixture(t, "remote-state-eligible")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("terraform_remote_state", "network"))
	if !ok {
		t.Fatalf("the demanded terraform_remote_state source was not classified at all")
	}
	if !src.RemoteState {
		t.Fatalf("a terraform_remote_state source was not marked RemoteState")
	}
	if src.TfeOutputs {
		t.Fatalf("a terraform_remote_state source was marked TfeOutputs")
	}
	if !src.Eligible {
		t.Fatalf("a terraform_remote_state source with a static backend and config classified ineligible: %s", src.ReasonDetail)
	}
}

// TestAnalyzeRemoteStateNonStaticBackendConfigIsIneligible: eligibility
// rule 1 applies to terraform_remote_state's own arguments exactly as it
// does to any data source's - "the backend block's own arguments must be
// static" is the same rule, not a new one, so the class-agnostic
// [SummaryNotReadable] wording is what fires, naming the managed
// dependency.
func TestAnalyzeRemoteStateNonStaticBackendConfigIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "remote-state-ineligible-backend")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("terraform_remote_state", "network"))
	if !ok {
		t.Fatalf("the demanded terraform_remote_state source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a terraform_remote_state source whose config reads a managed resource classified eligible")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	for _, part := range []string{"needed to resolve the identity of", "managed resource", "cannot be read before the plan"} {
		if !strings.Contains(src.ReasonDetail, part) {
			t.Errorf("the wording lacks %q: %s", part, src.ReasonDetail)
		}
	}
}

// TestAnalyzeCrossModuleVariableChainIsEligible is issue #212's chain,
// confirmed directly: a descendant module's data source (data.test_zone.sub
// in child/) reads a module-call variable whose value is itself an
// ancestor's own data source's attribute (data.test_zone.root.name in the
// root). Before the fix, [configs.ModuleCall.decodeStaticVariables]'s
// closure evaluated var.zone_name through the root's StaticEvaluator as it
// existed when the module tree was FIRST built - before analysis had
// attached any data lookup to anything - so the reference refused as
// unreadable and every source in the chain classified ineligible. The fix
// gives the ancestor module its own live, data-lookup-attached evaluator
// (see liveeval.go) so the reference now resolves like any other same-stack
// data reference.
func TestAnalyzeCrossModuleVariableChainIsEligible(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "cross-module-eligible"), nil)
	a := Analyze(context.Background(), cfg, Options{})

	childModule := addrs.Module{"child"}
	rootSrc, ok := a.SourceFor(addrs.RootModule, dataAddr("test_zone", "root"))
	if !ok {
		t.Fatalf("the root module's data.test_zone.root was not classified at all")
	}
	if !rootSrc.Eligible {
		t.Fatalf("data.test_zone.root classified ineligible: %s", rootSrc.ReasonDetail)
	}

	subSrc, ok := a.SourceFor(childModule, dataAddr("test_zone", "sub"))
	if !ok {
		t.Fatalf("the child module's data.test_zone.sub was not classified at all")
	}
	if !subSrc.Eligible {
		t.Fatalf("data.test_zone.sub classified ineligible: %s", subSrc.ReasonDetail)
	}

	// The dependency edge must be attributed to the ROOT module, not the
	// child module data.test_zone.sub itself lives in - the whole point of
	// #212's fix.
	var sawCrossModuleDep bool
	for _, dep := range subSrc.Deps {
		if dep.Resource.String() == "data.test_zone.root" {
			if dep.Module.String() != addrs.RootModule.String() {
				t.Errorf("data.test_zone.root dependency attributed to module %q, want the root module", dep.Module.String())
			}
			sawCrossModuleDep = true
		}
	}
	if !sawCrossModuleDep {
		t.Fatalf("data.test_zone.sub's Deps %v does not name data.test_zone.root at all", subSrc.Deps)
	}

	recordSrc, ok := a.SourceFor(childModule, dataAddr("test_record", "b"))
	if !ok {
		t.Fatalf("the child module's data.test_record.b was not classified at all")
	}
	if !recordSrc.Eligible {
		t.Fatalf("data.test_record.b classified ineligible: %s", recordSrc.ReasonDetail)
	}
}

// TestAnalyzeCrossModuleVariableChainStillPropagatesRefusal is
// TestAnalyzeCrossModuleVariableChainIsEligible's negative twin: the
// ancestor's own data source depends on a managed resource (rule 4), so it
// is ineligible on its own regardless of any module boundary. Widening
// eligibility to cross a module boundary must not stop a genuine refusal
// from propagating across that same boundary - answers "did this change
// turn any warning into silence?".
func TestAnalyzeCrossModuleVariableChainStillPropagatesRefusal(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "cross-module-ineligible"), nil)
	a := Analyze(context.Background(), cfg, Options{})

	rootSrc, ok := a.SourceFor(addrs.RootModule, dataAddr("test_zone", "root"))
	if !ok {
		t.Fatalf("the root module's data.test_zone.root was not classified at all")
	}
	if rootSrc.Eligible {
		t.Fatalf("data.test_zone.root reads a managed resource but classified eligible")
	}

	subSrc, ok := a.SourceFor(addrs.Module{"child"}, dataAddr("test_zone", "sub"))
	if !ok {
		t.Fatalf("the child module's data.test_zone.sub was not classified at all")
	}
	if subSrc.Eligible {
		t.Fatalf("data.test_zone.sub classified eligible even though its ancestor's own data source depends on a managed resource - the refusal did not propagate across the module boundary")
	}
	if subSrc.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", subSrc.ReasonSummary, SummaryNotReadable)
	}
	if !strings.Contains(subSrc.ReasonDetail, "data.test_zone.root") {
		t.Errorf("the wording does not name the ancestor's own unreadable source: %s", subSrc.ReasonDetail)
	}
}

// TestAnalyzeDynamicBlockIteratorIsEligible is issue #212's second fix,
// confirmed: before it, collectBodyExpressions had no model for a
// "dynamic" block's iterator at all - the block's own for_each was
// silently skipped (its name collided with [metaArguments]) and "content"
// walked as an ordinary nested block, so statement.value/condition.value/
// stmt.value each evaluated as an undefined variable and the source
// classified ineligible. This fixture exercises the three shapes that
// actually appear in terraform-aws-modules/iam: an ordinary iterator, a
// dynamic block nested inside another reading the OUTER block's iterator
// from the inner block's own content, and a renamed iterator via
// `iterator = stmt`.
func TestAnalyzeDynamicBlockIteratorIsEligible(t *testing.T) {
	a := analyzeFixture(t, "dynamic-block-iterator")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("test_zone", "policy"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if !src.Eligible {
		t.Fatalf("data.test_zone.policy classified ineligible: %s", src.ReasonDetail)
	}
}

// TestCollectDynamicBlockNoLabelRefusesRatherThanVanishing: a "dynamic"
// block with no label is invalid HCL that OpenTofu's own schema decode
// would refuse anyway, but this phase draws no schema at all - answers
// "does a completeness check silently skip what it does not recognize?"
// for that shape: it must not let the malformed block's would-be content
// simply disappear from the walk and read as trivially eligible.
func TestCollectDynamicBlockNoLabelRefusesRatherThanVanishing(t *testing.T) {
	body := parseHCLBody(t, `
dynamic {
  for_each = ["a"]
  content {
    sid = "x"
  }
}
`)
	var out []namedExpr
	collectBodyExpressions(body, "argument", &out)
	if len(out) != 1 || out[0].expr != nil {
		t.Fatalf("collectBodyExpressions on a labelless dynamic block produced %d expressions (want exactly one expression-less sentinel): %+v", len(out), out)
	}
}

// TestCollectDynamicBlockNoContentRefusesRatherThanVanishing is the same
// question for a "dynamic" block missing its required "content" sub-block.
func TestCollectDynamicBlockNoContentRefusesRatherThanVanishing(t *testing.T) {
	body := parseHCLBody(t, `
dynamic "statement" {
  for_each = ["a"]
}
`)
	var out []namedExpr
	var sawForEach, sawSentinel bool
	collectBodyExpressions(body, "argument", &out)
	for _, ne := range out {
		if ne.expr == nil {
			sawSentinel = true
			continue
		}
		if strings.Contains(ne.label, "for_each") {
			sawForEach = true
		}
	}
	if !sawForEach {
		t.Errorf("the dynamic block's own for_each was not collected at all: %+v", out)
	}
	if !sawSentinel {
		t.Fatalf("a dynamic block with no content block produced no refusing sentinel: %+v", out)
	}
}

// parseHCLBody parses src as a bare HCL body (no surrounding block) for a
// collectBodyExpressions unit test that needs a real *hclsyntax.Body
// without a whole fixture directory.
func parseHCLBody(t *testing.T, src string) *hclsyntax.Body {
	t.Helper()
	f, diags := hclsyntax.ParseConfig([]byte(src), "test.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing test HCL: %s", diags.Error())
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("parsed body is not *hclsyntax.Body")
	}
	return body
}

// TestAnalyzeEmptyForConfigurationsWithoutDataDemand: the phase costs
// nothing for a configuration that never needed it.
func TestAnalyzeEmptyForConfigurationsWithoutDataDemand(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("..", "identity", "testdata", "count-length"), nil)
	a := Analyze(context.Background(), cfg, Options{})
	if !a.Empty() {
		t.Fatalf("a configuration with no data sources produced demand: %v", a.Demanded())
	}
	if res, diags := Read(context.Background(), cfg, a, nil); res != nil || diags.HasErrors() {
		t.Fatalf("Read on an empty analysis did something: %v, %s", res, diags.Err())
	}
}
