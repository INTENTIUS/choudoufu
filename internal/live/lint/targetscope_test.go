// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// lintTargetScopeFixture holds two blocks for each rule under test, one
// labelled "..._targeted" and one "..._excluded", plus the
// whole-configuration controls. It lives under internal/command/testdata for
// the reason its own header gives: TestIdentityGolden sweeps every
// configuration directory under internal/live and live, and a repro fixture
// must not add pinned rows there.
const lintTargetScopeFixture = "../../command/testdata/live-lint-target-scope-1256"

// scopeKeepingTargeted keeps exactly the blocks whose label ends
// "_targeted". Everything else - including every block in a child module,
// of which this fixture has none - is out of the plan graph.
func scopeKeepingTargeted() identity.Scope {
	return func(a addrs.ConfigResource) bool {
		return strings.HasSuffix(a.Resource.Name, "_targeted")
	}
}

// scopeKeepingNothing is the run whose plan graph holds none of the
// fixture's blocks: the shape an operator produces by targeting a resource
// in some other part of a large configuration.
func scopeKeepingNothing() identity.Scope {
	return func(addrs.ConfigResource) bool { return false }
}

// countRule is how many issues carry this rule.
func countRule(issues []Issue, rule Rule) int {
	n := 0
	for _, iss := range issues {
		if iss.Rule == rule {
			n++
		}
	}
	return n
}

// constructsFor is every Construct string one rule reported, sorted, with
// the fixture's 1011-character labels shortened so a failure message stays
// readable.
func constructsFor(issues []Issue, rule Rule) []string {
	var out []string
	for _, iss := range issues {
		if iss.Rule != rule {
			continue
		}
		c := iss.Construct
		if len(c) > 80 {
			c = c[:30] + "..." + c[len(c)-30:]
		}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// mentionsTargeted and mentionsExcluded read the fixture's own naming
// convention back off whatever the rule reported, so an assertion does not
// have to know each rule's Construct format. They read the RAW Construct,
// not [constructsFor]'s shortened rendering: that shortening elides the
// middle of a long string, and the receipt-secret rule's construct carries
// the label there - which is how this pair failed on its first run.
func mentions(issues []Issue, rule Rule, want string) bool {
	for _, iss := range issues {
		if iss.Rule == rule && strings.Contains(iss.Construct, want) {
			return true
		}
	}
	return false
}

func mentionsTargeted(issues []Issue, rule Rule) bool {
	return mentions(issues, rule, "_targeted")
}

func mentionsExcluded(issues []Issue, rule Rule) bool {
	return mentions(issues, rule, "_excluded")
}

func loadLintTargetScopeFixture(t *testing.T) *configs.Config {
	t.Helper()
	return loadConfigDir(t, lintTargetScopeFixture)
}

// ruleScopeVerdict is what GitHub issue #1256 decided about one rule.
type ruleScopeVerdict int

const (
	// ruleScoped: the rule is raised about one resource BLOCK, and the
	// block is skipped when this run's -target / -exclude leaves it out of
	// the plan graph. See [scopeExcludes].
	ruleScoped ruleScopeVerdict = iota

	// ruleWholeConfig: the rule is not about a resource block at all - the
	// live block's own settings, a module call, a backend - so there is no
	// [addrs.ConfigResource] for a scope to answer about.
	ruleWholeConfig

	// ruleBlockButUnscoped: the rule DOES name a resource block and still
	// does not narrow, because the hazard it names outlives this run's plan
	// graph. Two members, and each one's reason is at its own raising site.
	// This is the interesting bucket: a rule landing here by default rather
	// than by argument is the failure #1256 exists to prevent.
	ruleBlockButUnscoped
)

// lintRuleTargetScope is GitHub issue #1256's audit, one entry per member of
// [Rules]. A rule this map does not carry fails
// [TestEveryRuleHasATargetScopeVerdict], which is the guard: a rule added
// tomorrow is visibly in or out, and nobody has to remember that this
// question exists.
//
// It is a classification guard, the same kind internal/command's
// TestEveryLiveOrchestratorCallIsClassifiedForTargeting is, and it has the
// same bound: it cannot tell that a scoped rule reads its scope correctly,
// only that somebody decided. [TestLintHonoursTheTargetScope] is where the
// behaviour is pinned, and [TestEveryScopedRuleIsInTheFixture] is what stops
// that test covering less than it claims.
var lintRuleTargetScope = map[Rule]struct {
	verdict ruleScopeVerdict
	why     string
}{
	// ---- scoped: raised about one resource block -------------------
	RuleProvisioner:     {ruleScoped, "a tainted bit for a create this run will not perform"},
	RuleLogicalResource: {ruleScoped, "admission of a block this run will not instantiate"},
	RuleUnadmittedType:  {ruleScoped, "same admission question, through the generated table"},
	RuleMarkerlessType:  {ruleScoped, "nowhere to write a marker for an object this run will not create; check.NodeStampUnmarkedApply narrowed the identical question in #1203"},
	RuleCountIndex:      {ruleScoped, "two instances colliding on one live identity, for instances this run will not plan"},
	RuleIgnoreChanges:   {ruleScoped, "markers that would be ignored on an apply that will not happen here"},
	RuleGenerateName:    {ruleScoped, "a server-minted name for an object this run will not create"},
	RuleForEachKey:      {ruleScoped, "a key that cannot survive into a marker this run will not stamp (resource half; the module-call half is not an addrs.ConfigResource)"},
	RuleOverlongAddress: {ruleScoped, "an address that will not fit in a marker this run will not stamp"},
	RuleReceiptLeaf:     {ruleScoped, "narrowed on the REFERRING block; a reference is the dependency edge targeting follows, so targeting the referrer keeps the receipt too"},
	RuleReceiptValue:    {ruleScoped, "the receipt block's own declaration, for a receipt this run will not write"},
	RuleReceiptSecret:   {ruleScoped, "same block, same run"},

	// ---- names a block, and still does not narrow ------------------
	RuleMovedBlock:              {ruleBlockButUnscoped, "internal/live/discovery's declaredInstances is built on lint refusing exactly the statements it leaves out; and a moved endpoint is an addrs.MoveEndpoint whose FROM address the configuration no longer declares, so the plan graph has no vertex for it. See checkMovedBlocks"},
	RuleUndeclaredProviderAlias: {ruleBlockButUnscoped, "the estate sweep's provider set is statelessManagedResourceProviders, read off the configuration rather than off the target set, so a stray alias still configures a provider from the environment alone on a narrowed run. See checkUndeclaredProviderAlias"},

	// ---- not about a resource block at all -------------------------
	RuleStateBackend:              {ruleWholeConfig, "a backend or cloud block; names no resource"},
	RuleChildModule:               {ruleWholeConfig, "a module call's expansion; a module call is not an addrs.ConfigResource"},
	RuleChildLiveConfig:           {ruleWholeConfig, "a live block in a child module"},
	RuleModuleProviders:           {ruleWholeConfig, "a module call's providers mapping"},
	RuleModuleProviderBlock:       {ruleWholeConfig, "a provider block in a child module, judged over the whole call chain"},
	RulePolicyVerb:                {ruleWholeConfig, "the live block's ownership policy"},
	RulePolicyScope:               {ruleWholeConfig, "same block"},
	RulePolicyThreshold:           {ruleWholeConfig, "same block"},
	RuleRetry:                     {ruleWholeConfig, "the live block's retry setting"},
	RuleStrictMarkerRepair:        {ruleWholeConfig, "the live block's strict settings"},
	RuleStrictSecrets:             {ruleWholeConfig, "same block"},
	RuleStrictNoSourceCreate:      {ruleWholeConfig, "same block"},
	RuleStrictProviderChange:      {ruleWholeConfig, "same block"},
	RuleStrictMarkers:             {ruleWholeConfig, "the markers selection is a declaration about the estate, malformed on every run whatever this one touches; checkStrictMarkers runs once over the whole tree, outside checkConfig"},
	RuleStrictMarkersUnrecordable: {ruleWholeConfig, "same selection, and its unit is a TYPE rather than a block"},
}

// scopedRules is [lintRuleTargetScope]'s scoped half, so the behavioural
// test below cannot drift from the classification above.
func scopedRuleSet() []Rule {
	var out []Rule
	for _, rule := range Rules() {
		if lintRuleTargetScope[rule].verdict == ruleScoped {
			out = append(out, rule)
		}
	}
	return out
}

// scopedRules are the members of [scopedRuleSet] this fixture actually
// trips. Two of the twelve are absent, and both ride the identical
// `continue` in checkManagedResources that RuleProvisioner,
// RuleLogicalResource, RuleUnadmittedType, RuleCountIndex and
// RuleIgnoreChanges are pinned on here: RuleMarkerlessType (whose #1203
// twin, check.NodeStampUnmarkedApply, is pinned by
// TestNodeStampUnmarkedApplyHonoursTheTargetScope over the same question)
// and RuleGenerateName (Kubernetes only). Stated rather than silently
// omitted, and [TestEveryScopedRuleIsInTheFixture] holds the pair to
// exactly those two.
var scopedRules = []Rule{
	RuleProvisioner,
	RuleLogicalResource,
	RuleUnadmittedType,
	RuleCountIndex,
	RuleIgnoreChanges,
	RuleForEachKey,
	RuleOverlongAddress,
	RuleReceiptValue,
	RuleReceiptSecret,
	RuleReceiptLeaf,
}

// unscopedRules are the rules that must keep firing over a block the run
// excludes, each for the reason [lintRuleTargetScope] records. The counts
// are what the fixture declares.
var unscopedRules = map[Rule]int{
	RuleStateBackend:            1,
	RuleUndeclaredProviderAlias: 2,
	RuleMovedBlock:              1,
}

// TestEveryRuleHasATargetScopeVerdict is GitHub issue #1256's forward-looking
// guard, and the reason this unit is an audit rather than a patch: the
// population is [Rules], which is ruleInfo's keys and is itself held complete
// by TestEveryRuleConstantIsRegistered, so a rule added tomorrow fails this
// test until somebody has said whether this run's target set reaches it.
//
// It fails in both directions - an unclassified rule, and a classified rule
// that no longer exists.
func TestEveryRuleHasATargetScopeVerdict(t *testing.T) {
	all := Rules()
	if len(all) == 0 {
		t.Fatal("Rules() returned nothing; a guard that enumerates an empty population passes for the wrong reason")
	}

	for _, rule := range all {
		c, ok := lintRuleTargetScope[rule]
		if !ok {
			t.Errorf("%s has no entry in lintRuleTargetScope. Decide whether this run's -target/-exclude scope reaches it: "+
				"a rule raised about one resource block refuses a run over a block the operator deliberately left out, "+
				"which is GitHub issues #1176, #1203 and #1256.", rule)
			continue
		}
		if strings.TrimSpace(c.why) == "" {
			t.Errorf("%s is classified with no reason, which is silencing the guard rather than deciding", rule)
		}
	}

	declared := map[Rule]bool{}
	for _, rule := range all {
		declared[rule] = true
	}
	for rule := range lintRuleTargetScope {
		if !declared[rule] {
			t.Errorf("lintRuleTargetScope classifies %q, which Rules() no longer reports. Remove the entry, or the rule was renamed without updating it.", rule)
		}
	}
}

// TestLintHonoursTheTargetScope is GitHub issue #1256: before it,
// [Context] had exactly one field, so the target set did not reach this
// package at all and a per-resource rule refused a run over a block the
// operator had deliberately left out - #1176's shape at the earliest stage
// on the live path.
//
// The three arms are #1203's, and the middle one is what makes this a fix
// rather than a hole. Narrowing a run must not disable a check protecting
// something the run DOES touch, so a scope that keeps one of two blocks
// must still refuse that one. The two ways to get this wrong fail
// differently, which is the whole reason the third arm exists beside the
// second:
//
//	no narrowing at all  -> "keeps one" FAIL, "keeps neither" FAIL
//	narrowed too far     -> "keeps one" FAIL, "keeps neither" PASS
//
// Both were run against this test before it was trusted green.
func TestLintHonoursTheTargetScope(t *testing.T) {
	cfg := loadLintTargetScopeFixture(t)

	t.Run("no scope: every block refuses, exactly as before #1256", func(t *testing.T) {
		issues := CheckWith(t.Context(), cfg, Context{})
		for _, rule := range scopedRules {
			if n := countRule(issues, rule); n != 2 {
				t.Errorf("%s: want 2 issues on an untargeted run, got %d: %v", rule, n, constructsFor(issues, rule))
			}
		}
		for rule, want := range unscopedRules {
			if n := countRule(issues, rule); n != want {
				t.Errorf("%s: want %d issues on an untargeted run, got %d: %v", rule, want, n, constructsFor(issues, rule))
			}
		}
	})

	t.Run("scope keeps the targeted half: that half still refuses", func(t *testing.T) {
		issues := CheckWith(t.Context(), cfg, Context{Scope: scopeKeepingTargeted()})
		for _, rule := range scopedRules {
			if n := countRule(issues, rule); n != 1 {
				t.Errorf("%s: want exactly 1 issue, got %d: %v.\n"+
					"Narrowing a run must not disable a check for a block the run still holds, and must not leave one in place for a block it does not.",
					rule, n, constructsFor(issues, rule))
				continue
			}
			if !mentionsTargeted(issues, rule) {
				t.Errorf("%s: the surviving issue should name the _targeted block, got %v", rule, constructsFor(issues, rule))
			}
			if mentionsExcluded(issues, rule) {
				t.Errorf("%s: the _excluded block is outside the plan graph and must not be refused, got %v", rule, constructsFor(issues, rule))
			}
		}
		// The whole-configuration rules do not move at all.
		for rule, want := range unscopedRules {
			if n := countRule(issues, rule); n != want {
				t.Errorf("%s does not read the target set and must report %d issues whatever the scope; got %d: %v",
					rule, want, n, constructsFor(issues, rule))
			}
		}
	})

	t.Run("scope keeps nothing: the per-resource rules go silent, the rest do not", func(t *testing.T) {
		issues := CheckWith(t.Context(), cfg, Context{Scope: scopeKeepingNothing()})
		for _, rule := range scopedRules {
			if n := countRule(issues, rule); n != 0 {
				t.Errorf("%s: a run whose plan graph holds neither block must not be refused for either; got %d: %v",
					rule, n, constructsFor(issues, rule))
			}
		}
		for rule, want := range unscopedRules {
			if n := countRule(issues, rule); n != want {
				t.Errorf("%s must still report %d issues when the scope keeps nothing - it is not about any one resource block; got %d: %v",
					rule, want, n, constructsFor(issues, rule))
			}
		}
		// RuleMovedBlock's statement deliberately names an EXCLUDED block
		// as its destination, which is the exact shape
		// internal/live/discovery's declaredInstances relies on lint
		// refusing: scoping this rule would let it through and the sweep
		// would read the moved resource as an orphan.
		if !mentionsTargeted(issues, RuleUndeclaredProviderAlias) || !mentionsExcluded(issues, RuleUndeclaredProviderAlias) {
			t.Errorf("both undeclared-alias resources must still be named when the scope keeps nothing, got %v",
				constructsFor(issues, RuleUndeclaredProviderAlias))
		}
	})
}

// TestEveryScopedRuleIsInTheFixture is the guard against this test quietly
// covering less than it claims: a rule listed in [scopedRules] that the
// fixture stopped tripping would make every one of its assertions vacuous
// in the "keeps nothing" arm (0 == 0) and merely wrong in the others.
func TestEveryScopedRuleIsInTheFixture(t *testing.T) {
	issues := CheckWith(t.Context(), loadLintTargetScopeFixture(t), Context{})
	for _, rule := range scopedRules {
		if countRule(issues, rule) == 0 {
			t.Errorf("%s is listed as scoped but the fixture no longer trips it, so its assertions prove nothing", rule)
		}
	}
	for rule := range unscopedRules {
		if countRule(issues, rule) == 0 {
			t.Errorf("%s is listed as unscoped but the fixture no longer trips it, so its assertions prove nothing", rule)
		}
	}

	// And the other direction: the two scoped rules this fixture does NOT
	// cover are named in [scopedRules]' own doc comment, so a third one
	// appearing there silently - a new scoped rule nobody wrote a fixture
	// block for - has to fail rather than quietly widen the gap.
	covered := map[Rule]bool{}
	for _, rule := range scopedRules {
		covered[rule] = true
	}
	uncovered := map[Rule]bool{}
	for _, rule := range scopedRuleSet() {
		if !covered[rule] {
			uncovered[rule] = true
		}
	}
	for rule := range uncovered {
		if rule != RuleMarkerlessType && rule != RuleGenerateName {
			t.Errorf("%s is classified scoped and this fixture does not exercise it. Add a _targeted/_excluded pair to %s, "+
				"or say here why it cannot be one - the two standing exceptions are markerless-type and generate-name.",
				rule, lintTargetScopeFixture)
		}
	}
	if len(uncovered) != 2 {
		t.Errorf("want exactly the two documented uncovered scoped rules, got %d: %v", len(uncovered), uncovered)
	}
}

// TestResidueAttributesHonoursTheTargetScope is the same ruling applied to
// the warning that rides beside the rules: the perpetual diff it describes
// is a diff over a block the run proposes, and a block -target / -exclude
// left out of the plan graph is not proposed here.
//
// Warning severity, so nothing is refused either way - but a warning an
// operator cannot act on without abandoning the narrowing they asked for is
// the same shape, and #1203's audit filed it under #1256 with the rules.
func TestResidueAttributesHonoursTheTargetScope(t *testing.T) {
	const src = `
resource "aws_ssm_parameter" "wo_targeted" {
  name     = "/app/targeted"
  type     = "String"
  value_wo = "hunter2"
}

resource "aws_ssm_parameter" "wo_excluded" {
  name     = "/app/excluded"
  type     = "String"
  value_wo = "hunter2"
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %s", err)
	}
	cfg := loadConfigDir(t, dir)

	check := func(t *testing.T, scope identity.Scope, want int, wantAddrs ...string) {
		t.Helper()
		diags := CheckResidueAttributes(cfg, Context{Schemas: residueTestSchemas(), Scope: scope})
		if len(diags) != want {
			t.Fatalf("want %d warnings, got %d: %s", want, len(diags), diags.ErrWithWarnings())
		}
		for _, addr := range wantAddrs {
			found := false
			for _, d := range diags {
				if strings.Contains(d.Description().Detail, addr) {
					found = true
				}
			}
			if !found {
				t.Errorf("no warning named %s: %s", addr, diags.ErrWithWarnings())
			}
		}
		for _, d := range diags {
			if len(wantAddrs) == 1 && strings.Contains(d.Description().Detail, "wo_excluded") {
				t.Errorf("wo_excluded is outside the plan graph and must not be warned about: %s", d.Description().Detail)
			}
		}
	}

	t.Run("no scope", func(t *testing.T) { check(t, nil, 2, "wo_targeted", "wo_excluded") })
	t.Run("keeps one", func(t *testing.T) { check(t, scopeKeepingTargeted(), 1, "wo_targeted") })
	t.Run("keeps neither", func(t *testing.T) { check(t, scopeKeepingNothing(), 0) })
}
