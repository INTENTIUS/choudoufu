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

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// limitsDir is the root of the limits wing (PE.1), relative to this package.
// It lives outside internal/live/lint, at the repository root, the same
// way the P0.1 estate fixture does: it is an e2e fixture, not a lint fixture,
// even though this file is the only place that walks it today.
func limitsDir(t *testing.T) string {
	return flocitest.LimitsDir(t)
}

// limitationsDoc is live/LIMITATIONS.md, read by TestLimitationsDocCoversDirs
// to keep the doc, the fixtures, and this table from drifting apart silently.
const limitationsDoc = "../../../live/LIMITATIONS.md"

// enforcedLimits is every live/e2e/limits directory whose construct is
// rejected by lint today, paired with the exact rule that must fire. CheckContext()
// must report that rule, and only that rule, for each of these directories.
var enforcedLimits = map[string]Rule{
	"local-exec":           RuleProvisioner,
	"remote-exec":          RuleProvisioner,
	"null-resource":        RuleLogicalResource,
	"terraform-data":       RuleLogicalResource,
	"local-file":           RuleLogicalResource,
	"local-sensitive-file": RuleLogicalResource,
	"random-password":      RuleLogicalResource,
	"time-sleep":           RuleLogicalResource,
	"moved-block":          RuleMovedBlock,
	"child-module":         RuleChildModule,
	"backend-block":        RuleStateBackend,
	"cloud-block":          RuleStateBackend,
	"unadmitted-type":      RuleUnadmittedType,
	"markerless-type":      RuleMarkerlessType,
	"count-index-in-tag":   RuleCountIndex,
	"foreach-invalid-key":  RuleForEachKey,
	"overlong-address":     RuleOverlongAddress,
	// GitHub issue #378's reservation. The fixture writes a tofu-address by
	// hand out of tofu.marker_module_prefix, which is the one plausible
	// reason an operator would ever reach for the symbol - and the one the
	// rule most needs to refuse, because such a marker is not verified by
	// the pass that would otherwise have written it.
	"reserved-symbol": RuleReservedSymbol,
	// GitHub issue #103. Its fixture carries a fourth resource that must
	// NOT be refused - ignore_changes on a single non-marker tag key - and
	// TestIgnoreChangesAdmitsAForeignTagKey is what pins that half.
	"ignore-changes": RuleIgnoreChanges,
	// GitHub issue #104. Its fixture's second module call maps to the
	// default provider configuration, which live mode already uses, and
	// must not be refused - TestModuleProvidersAdmitsTheDefaultMapping.
	"module-providers": RuleModuleProviders,
	// GitHub issue #123, the root-resource route into the same
	// environment-configured-provider fallback #104's rule covers for
	// module mappings. The admitted twin - an alias a root provider block
	// does declare - is pinned by TestCheck's
	// undeclared-provider-alias-declared case.
	"undeclared-provider-alias": RuleUndeclaredProviderAlias,
	// module-provider-block moved to notYetEnforcedLimits under GitHub issue
	// #201: see that table's entry for why nothing here fires it anymore.
	// Wave-3 audit of #72: a child module's live configuration was decoded
	// and silently ignored; now refused.
	"child-live-config": RuleChildLiveConfig,
	// The three policy rules joined the limits wing under GitHub issue
	// #110's fourth criterion. They were the only rules in ruleInfo citing
	// the issue tracker as their documentation, which is not a document a
	// user deciding whether their configuration can move has any reason to
	// read. internal/live/lint/testdata keeps the wider policy matrix
	// (valid combinations, defaults written out, an empty scope block);
	// these three are the refusals, one per rule, in the shape the limits
	// wing requires - doc entry, fixture, and asserted rule.
	"policy-verb": RulePolicyVerb,
	// GitHub issue #365 slice 3. The one shape the secrets toggle refuses:
	// a spelling outside the vocabulary. See live/LIMITATIONS.md,
	// "strict-secrets".
	"strict-secrets": RuleStrictSecrets,
	// GitHub issue #365's own principle, not the typo case above: a real
	// secret-generating type (random_password) refused because the toggle
	// is actually set, with the Detail naming the setting by value.
	// TestStrictSecretsRefusalToggleIsTheObstacle is this directory's
	// mutation check. See live/LIMITATIONS.md, "strict-secrets-refusal".
	"strict-secrets-refusal": RuleLogicalResource,
	// GitHub issue #365 ruling 4 (rfc/20260823-foundation-order-ruling.md).
	// See live/LIMITATIONS.md, "strict-no-source-create".
	"strict-no-source-create": RuleStrictNoSourceCreate,
	"policy-scope":            RulePolicyScope,
	"policy-threshold":        RulePolicyThreshold,
	// GitHub issue #365's first strict-profile toggle. The fixture sets
	// marker_repair = "never", a setting the schema defines and no build
	// implements yet; internal/live/lint/testdata carries the rest of the
	// matrix (the default omitted, the default written out, the other
	// unimplemented setting, and a typo).
	//
	// This is an entry the day the mechanism lands has to revisit rather
	// than delete: "never" and "report" stop being refusals then, and the
	// fixture moves to whatever the rule still catches - a value outside
	// the vocabulary.
	"strict-marker-repair": RuleStrictMarkerRepair,
	// GitHub issue #365's third toggle, the markers "record" selection.
	// Two directories because the two rules have different authors: the
	// first fixture's block is wrong on its own terms (a selection with no
	// record_store to hold what it moves), the second's names a type the
	// provider will not import back, which is a fact about the provider
	// rather than about the file.
	//
	// Both fixtures declare no resources at all, which is what keeps each
	// to exactly one rule. internal/live/lint/testdata carries the rest of
	// the matrix, including the two halves that need provider schemas.
	"strict-markers":              RuleStrictMarkers,
	"strict-markers-unrecordable": RuleStrictMarkersUnrecordable,
}

// notYetEnforcedLimits is every live/e2e/limits directory whose
// construct is banned or bounded by the roadmap but has no lint rule yet.
// CheckContext() must report nothing for these today. This is a pin, not an
// endorsement: the day a rule lands for one of these, this test starts
// failing loudly (Check no longer returns zero issues), and the fix is to
// move the directory into enforcedLimits and update LIMITATIONS.md in the
// same change, not to relax the assertion.
// RA.3 moved foreach-dotted-key (issue #178: renamed foreach-invalid-key,
// since "." and ":" are admitted now) out of this list and into
// enforcedLimits: RuleForEachKey (internal/live/lint/foreach_key.go) rejects
// a key carrying anything outside the AWS tag-value set. overlong-address
// followed the same path: RuleOverlongAddress
// (internal/live/lint/overlong_address.go) now measures the escaped
// address against MARKERS.md's 256-character cap.
var notYetEnforcedLimits = []string{
	"duplicate-identity", // enforced at resolve time (internal/live/identity), not lint
	// module-provider-block is a poor fit for this bucket's own doc comment
	// ("banned or bounded by the roadmap") - it is neither, after GitHub
	// issue #201's parity fix - but it is the closer of the two available
	// buckets: CheckContext() reports nothing for it, same as every other
	// entry here. This fixture's module call names no count, for_each,
	// enabled or depends_on, so internal/configs/provider_validation.go
	// (the upstream code this fork forked verbatim) admits it at
	// config-build time same as stock OpenTofu, and
	// [checkModuleProviderBlocks] admits it too. The refusal this rule DOES
	// still express - a call chain that does use one of those four
	// reaching a content-bearing local block - can never reach a built
	// *configs.Config at all: internal/configs.BuildConfig's own
	// validateProviderConfigs (config_build.go:45) hard-errors on that
	// combination before internal/live/lint ever runs, in every entry point
	// this fork has (internal/live/check.Load, internal/configs/configload,
	// and this package's own loadConfigDir) - see
	// internal/configs/testdata/config-diagnostics/nested-provider for
	// upstream's own fixture proving it. No buildable HCL can trigger
	// RuleModuleProviderBlock's refusal branch through the ordinary live
	// path any longer; it is left in the rule as defense in depth for any
	// future caller that constructs a *configs.Config without going through
	// BuildConfig. live/e2e/limits/module-provider-block and its
	// live/LIMITATIONS.md heading are now stale in the same way -
	// documenting a limit that parity has closed - and are left as found;
	// retiring that limits-wing entry (directory, heading, and this
	// bucket's own doc comment) is follow-up work, not this fix.
	"module-provider-block",
}

// TestLimitsEnforced checks that every directory in enforcedLimits fails lint
// with exactly its named rule, and nothing else.
func TestLimitsEnforced(t *testing.T) {
	for dir, rule := range enforcedLimits {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)

			if len(issues) == 0 {
				t.Fatalf("CheckContext() reported no issues for %s, want at least one %s", dir, rule)
			}
			for _, issue := range issues {
				if issue.Rule != rule {
					t.Errorf("%s: got rule %q, want exactly %q (and no other rule): %s", dir, issue.Rule, rule, issue)
				}
			}
		})
	}
}

// logicalLimitsClasses pairs every logical-resource limits fixture with the
// [LogicalClass] its one resource classifies as, so
// TestLogicalLimitsDetailsRender can check that the class actually shows up
// in the Detail an operator sees - not just that RuleLogicalResource fired
// (TestLimitsEnforced already pins that), but that the new per-type
// classification (GitHub issue #73's groundwork) reached the message.
var logicalLimitsClasses = map[string]LogicalClass{
	"null-resource":          ClassRecordAdmitted,
	"terraform-data":         ClassRecordAdmitted,
	"time-sleep":             ClassRecordAdmitted,
	"random-password":        ClassSecretRefused,
	"local-sensitive-file":   ClassSecretRefused,
	"local-file":             ClassExternalAdmitted,
	"strict-secrets-refusal": ClassSecretRefused,
}

// TestLogicalLimitsDetailsRender checks that every logical-resource limits
// fixture's single issue carries the right class-specific wording in its
// Detail: RECORD_ADMITTED and SECRET_REFUSED name their class outright and
// carry the forwarding language specific to them (#73, and the secret
// manager address, respectively), while OTHER_REFUSED carries neither by
// design - its whole point is that the original, class-agnostic wording is
// unchanged (TestLogicalResourceDetailsRenderByClass in lint_test.go pins
// that wording byte-identical to the pre-table template).
func TestLogicalLimitsDetailsRender(t *testing.T) {
	for dir, class := range logicalLimitsClasses {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)

			var logical []Issue
			for _, issue := range issues {
				if issue.Rule == RuleLogicalResource {
					logical = append(logical, issue)
				}
			}
			if len(logical) != 1 {
				t.Fatalf("%s: got %d RuleLogicalResource issues, want exactly 1: %v", dir, len(logical), logical)
			}

			detail := logical[0].Detail
			switch class {
			case ClassRecordAdmitted:
				if !strings.Contains(detail, string(class)) {
					t.Errorf("%s: Detail = %q, want it to contain class %q", dir, detail, class)
				}
				if !strings.Contains(detail, "#73") {
					t.Errorf("%s: RECORD_ADMITTED Detail = %q, want it to cite #73", dir, detail)
				}
			case ClassSecretRefused:
				if !strings.Contains(detail, string(class)) {
					t.Errorf("%s: Detail = %q, want it to contain class %q", dir, detail, class)
				}
				if !strings.Contains(detail, "secret manager") {
					t.Errorf("%s: SECRET_REFUSED Detail = %q, want the secret-manager forwarding address", dir, detail)
				}
			case ClassOtherRefused:
				if strings.Contains(detail, string(ClassRecordAdmitted)) || strings.Contains(detail, string(ClassSecretRefused)) {
					t.Errorf("%s: OTHER_REFUSED Detail = %q, want no other class's wording (current wording preserved)", dir, detail)
				}
			}
		})
	}
}

// stripStrictBlock removes the `strict { ... }` block from src by deleting
// everything from the literal "strict {" through its matching closing
// brace, assuming - as every limits-wing fixture's strict block does - that
// no nested `{` appears inside it. Used only to build a mutation-check
// twin of an already-committed fixture from its own text, so the twin can
// never drift from what is actually committed under live/e2e/limits.
func stripStrictBlock(t *testing.T, src string) string {
	t.Helper()
	start := strings.Index(src, "strict {")
	if start < 0 {
		t.Fatalf("stripStrictBlock: no \"strict {\" found in:\n%s", src)
	}
	end := strings.Index(src[start:], "}")
	if end < 0 {
		t.Fatalf("stripStrictBlock: no closing brace found after \"strict {\" in:\n%s", src)
	}
	return src[:start] + src[start+end+1:]
}

// TestStrictSecretsRefusalToggleIsTheObstacle is
// live/e2e/limits/strict-secrets-refusal's mutation check: GitHub issue
// #365's own audit criterion, "a fixture proving it refuses exactly what
// it names", read literally. TestLimitsEnforced already proves the
// committed fixture (strict { secrets = "refuse" } set) is refused; this
// proves the SAME resource, with only that block removed, is not - so the
// refusal in TestLimitsEnforced is provably caused by the toggle and
// nothing else about the fixture (no record_store block of its own, no
// live block quirk, nothing).
func TestStrictSecretsRefusalToggleIsTheObstacle(t *testing.T) {
	fixture := filepath.Join(limitsDir(t), "strict-secrets-refusal")
	src, err := os.ReadFile(filepath.Join(fixture, "main.tf")) //nolint:gosec // a fixed path under the checkout
	if err != nil {
		t.Fatalf("reading %s: %s", fixture, err)
	}

	mutated := stripStrictBlock(t, string(src))

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mutated), 0o600); err != nil {
		t.Fatalf("writing mutated fixture: %s", err)
	}

	cfg := loadConfigDir(t, dir)
	issues := CheckContext(t.Context(), cfg)
	for _, issue := range issues {
		if issue.Rule == RuleLogicalResource {
			t.Errorf("random_password.db was still refused after removing strict { secrets = \"refuse\" }: %s", issue)
		}
	}
}

// childModuleFixtureDetails is the substring each of the refused module
// calls in live/e2e/limits/child-module/main.tf is expected to carry in its
// Detail, keyed by the call's own name.
//
// Issue #195 additionally admitted a statically-evaluable count with no
// count.index leak (see child_module.go's top-of-file comment), so the
// fixture's "counted" call ("count = 1", a literal, and no count.index
// anywhere in its body) is a THIRD admitted shape alongside "network" and
// "keyed-static". The fixture also carries "counted-leaking" - an equally
// static count whose own arguments index into a collection at count.index
// (suffix = var.suffixes[count.index]) - which is the case #195 still
// refuses, and "keyed" - a for_each reading another resource's attribute,
// not knowable from configuration alone - which was always refused.
// live/e2e/limits/child-module's own comments were stale after #195 (they
// used to describe "counted" as refused permanently) and have been
// refreshed to match the assertions below, which are the actual, verified
// behavior.
var childModuleFixtureDetails = map[string]string{
	"keyed":           "statically evaluable",
	"counted-leaking": "count.index",
}

// childModuleFixtureAdmitted is every module call in the fixture that must
// carry no RuleChildModule issue at all: the two 59b/59c shapes plus
// "counted" (issue #195).
var childModuleFixtureAdmitted = []string{"network", "keyed-static", "counted"}

// TestChildModuleDichotomy checks that live/e2e/limits/child-module/, which
// carries a static call, a statically-keyed for_each call, a statically-
// evaluable count call and a non-statically-keyed for_each call in one
// tree, gets exactly one RuleChildModule issue back - for the non-static
// for_each call, with the Detail that names its own reason - and none at
// all for the three admitted calls.
func TestChildModuleDichotomy(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join(limitsDir(t), "child-module"))
	issues := CheckContext(t.Context(), cfg)

	got := make(map[string]Issue, len(issues))
	for _, issue := range issues {
		if issue.Rule != RuleChildModule {
			continue
		}
		// Construct is `module "<name>"`; the name is what
		// childModuleFixtureDetails is keyed by.
		name := strings.TrimSuffix(strings.TrimPrefix(issue.Construct, `module "`), `"`)
		got[name] = issue
	}

	for _, name := range childModuleFixtureAdmitted {
		if _, ok := got[name]; ok {
			t.Errorf("child-module: the admitted call %q carries a RuleChildModule issue", name)
		}
	}

	if len(got) != len(childModuleFixtureDetails) {
		t.Fatalf("child-module: got %d distinct RuleChildModule module calls %v, want exactly %d: %v", len(got), got, len(childModuleFixtureDetails), childModuleFixtureDetails)
	}

	for name, wantSubstring := range childModuleFixtureDetails {
		issue, ok := got[name]
		if !ok {
			t.Errorf("child-module: no issue reported for module %q", name)
			continue
		}
		if !strings.Contains(issue.Detail, wantSubstring) {
			t.Errorf("child-module: module %q Detail = %q, want it to contain %q", name, issue.Detail, wantSubstring)
		}
	}
}

// TestLimitsNotYetEnforced checks that every directory in notYetEnforcedLimits
// produces no issues today. This pins current behavior on purpose: these
// constructs ARE banned or bounded by the roadmap, but no lint rule exists to
// catch them yet. When one lands, this assertion fails, which is the point —
// silence here would mean the gap stayed hidden.
func TestLimitsNotYetEnforced(t *testing.T) {
	for _, dir := range notYetEnforcedLimits {
		t.Run(dir, func(t *testing.T) {
			cfg := loadConfigDir(t, filepath.Join(limitsDir(t), dir))
			issues := CheckContext(t.Context(), cfg)
			if len(issues) != 0 {
				t.Errorf("CheckContext() reported %d issues for %s, want 0 (documented, not yet enforced); "+
					"if a rule now catches this, move %s into enforcedLimits and update live/LIMITATIONS.md",
					len(issues), dir, dir)
				for _, issue := range issues {
					t.Logf("  got: %s", issue)
				}
			}
		})
	}
}

// TestLimitsDirsMatchTable is the completeness check: every subdirectory of
// live/e2e/limits must appear in exactly one of enforcedLimits or
// notYetEnforcedLimits, and every entry in those two tables must correspond
// to a real directory. Either direction of mismatch means the fixture tree
// and this test drifted apart.
func TestLimitsDirsMatchTable(t *testing.T) {
	entries, err := os.ReadDir(limitsDir(t))
	if err != nil {
		t.Fatalf("reading %s: %s", limitsDir(t), err)
	}

	actual := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			actual[entry.Name()] = true
		}
	}

	expected := make(map[string]bool)
	for dir := range enforcedLimits {
		expected[dir] = true
	}
	for _, dir := range notYetEnforcedLimits {
		if expected[dir] {
			t.Errorf("%s appears in both enforcedLimits and notYetEnforcedLimits", dir)
		}
		expected[dir] = true
	}

	for dir := range actual {
		if !expected[dir] {
			t.Errorf("live/e2e/limits/%s exists but is not in enforcedLimits or notYetEnforcedLimits in this test", dir)
		}
	}
	for dir := range expected {
		if !actual[dir] {
			t.Errorf("%s is listed in this test but live/e2e/limits/%s does not exist", dir, dir)
		}
	}
}

// TestLimitationsDocCoversDirs checks that live/LIMITATIONS.md has an
// entry for every limits directory, and names no directory that does not
// exist. The doc is kept simple enough for this to work as a plain substring
// search: every entry in LIMITATIONS.md is required to head its section with
// "### <dir-name>", matching the directory name under live/e2e/limits
// exactly, so doc, fixtures, and this table cannot drift apart silently.
func TestLimitationsDocCoversDirs(t *testing.T) {
	content, err := os.ReadFile(limitationsDoc)
	if err != nil {
		t.Fatalf("reading %s: %s", limitationsDoc, err)
	}
	doc := string(content)

	var allDirs []string
	for dir := range enforcedLimits {
		allDirs = append(allDirs, dir)
	}
	allDirs = append(allDirs, notYetEnforcedLimits...)
	sort.Strings(allDirs)

	for _, dir := range allDirs {
		heading := "### " + dir
		if !strings.Contains(doc, heading) {
			t.Errorf("live/LIMITATIONS.md has no %q heading for live/e2e/limits/%s", heading, dir)
		}
	}

	// The other direction: every "### " heading in the doc should name a
	// directory this test knows about, so a stale doc entry (a renamed or
	// removed fixture) is caught too.
	known := make(map[string]bool)
	for _, dir := range allDirs {
		known[dir] = true
	}
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "### "))
		if !known[name] {
			t.Errorf("live/LIMITATIONS.md heading %q does not match any known limits directory", line)
		}
	}
}

// TestIgnoreChangesAdmitsAForeignTagKey is the other half of GitHub issue
// #103's rule, and the half a blanket refusal would have got wrong.
//
// Ignoring one tag key that something outside this configuration writes is an
// ordinary thing to want, and it does not touch the ownership markers. The
// fixture carries three refused shapes and this one admitted shape, and
// TestLimitsEnforced only checks that every issue is the right rule - it
// would pass just as happily if all four were refused.
func TestIgnoreChangesAdmitsAForeignTagKey(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join(limitsDir(t), "ignore-changes"))
	issues := CheckContext(t.Context(), cfg)

	for _, issue := range issues {
		if strings.Contains(issue.Construct, "one_foreign_key") {
			t.Errorf(`ignore_changes = [tags["Owner"]] was refused: %s`+
				"\nOwner is not a marker key, so ignoring it leaves tofu-estate and tofu-address alone.", issue)
		}
	}

	// And the three that must fire, each named, so that a rule narrowed too
	// far shows up here rather than as a silently smaller count.
	want := map[string]bool{"whole_tags": false, "marker_key": false, "everything": false}
	for _, issue := range issues {
		for name := range want {
			if strings.Contains(issue.Construct, name) {
				want[name] = true
			}
		}
	}
	for name, fired := range want {
		if !fired {
			t.Errorf("aws_s3_bucket.%s was not refused; its ignore_changes covers the ownership markers", name)
		}
	}
}

// TestModuleProvidersAdmitsTheDefaultMapping is the other half of GitHub
// issue #104's rule, updated by GitHub issue #188's narrowing.
//
// `providers = { aws = aws }` describes exactly what live mode already does:
// the module's resources are served by the root's default configuration.
// Refusing it would refuse a configuration that works, which is the cost
// this project's goal weighs heaviest.
//
// `providers = { aws = aws.useast1 }` used to ask for something the run
// would not do, back when nothing in the live path read a module call's
// providers mapping at all. Issue #188 built and wired
// internal/live/providerscope.Resolve into every site that resolves a
// resource's provider configuration (internal/command/live_plan.go,
// internal/live/discovery's inScope, internal/live/projection/build.go), so
// this mapping is now honoured rather than silently ignored: module "east"'s
// resources are planned, applied and swept against the root's aliased
// us-east-1 configuration, exactly as the mapping asks. Refusing it would
// again be refusing a configuration that now works.
func TestModuleProvidersAdmitsTheDefaultMapping(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join(limitsDir(t), "module-providers"))
	issues := CheckContext(t.Context(), cfg)

	for _, issue := range issues {
		switch {
		case strings.Contains(issue.Construct, `module "west"`):
			t.Errorf("the default mapping was refused: %s\nproviders = { aws = aws } is what live mode does anyway.", issue)
		case strings.Contains(issue.Construct, `module "east"`):
			t.Errorf("the aliased-parent mapping was refused: %s\nGitHub issue #188 resolves it through internal/live/providerscope instead of ignoring it.", issue)
		}
	}

	// The configuration_aliases shape, which the first version of this rule
	// admitted and issue #188 does not change: a resource's own `provider =
	// aws.primary` reference was never resolved by walking the module call's
	// mapping (it names the alias directly), so this shape's refusal still
	// turns on whether the root declares a matching provider block. An
	// adversarial audit reproduced the provider-key computation and found
	// the module's resources resolving aws.primary against a root that does
	// not declare it, so the provider is configured from the environment
	// with nothing from the configuration reaching it.
	var aliased bool
	for _, issue := range issues {
		if strings.Contains(issue.Construct, `module "aliased"`) {
			aliased = true
			if !strings.Contains(issue.Detail, "from the environment") {
				t.Errorf("the refusal does not name what actually happens: %s", issue.Detail)
			}
		}
	}
	if !aliased {
		t.Error(`module "aliased" maps a child-side alias the root does not declare, and was not refused`)
	}
}

// TestIgnoreChangesSkipsResourcesWithNoMarkers covers the two populations an
// adversarial audit found the first version of #103's rule refusing, each
// with a reason that was untrue of it.
//
// A record-backed logical type keeps its state in a record rather than a
// marker, and under a record_store it is admitted - so this rule was its only
// refusal, explained as losing tags it does not have. An untaggable type
// carries no marker either, and aws_autoscaling_group's tag blocks are not
// the tags map the stamp pass writes into.
func TestIgnoreChangesSkipsResourcesWithNoMarkers(t *testing.T) {
	const src = `
terraform {
  live {
    estate = "limits"
    record_store "local" {
      path = "./records"
    }
  }
}

resource "null_resource" "effect" {
  lifecycle {
    ignore_changes = all
  }
}

resource "terraform_data" "effect" {
  lifecycle {
    ignore_changes = all
  }
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing the fixture: %s", err)
	}
	cfg := loadConfigDir(t, dir)
	for _, issue := range CheckContext(t.Context(), cfg) {
		if issue.Rule == RuleIgnoreChanges {
			t.Errorf("a record-backed logical type was refused: %s\nIts identity is a persisted record, not a marker.", issue)
		}
	}
}
