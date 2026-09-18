// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// GitHub issue #1203. #1176 found two live-path passes that reasoned over
// the whole configuration while the run had been narrowed by -target /
// -exclude, and found both by accident: one estate happened to trip them.
// This file is the guard that makes the next one visible on the day it is
// written rather than on the day an estate trips it.
//
// The mechanism is the one TestLayersClassifyEveryLivePackage
// (internal/live/check) already uses for a different question: enumerate
// the population from the source, not from memory, and require every member
// to be classified. Adding a pass to a live orchestrator is then a test
// failure until somebody has said whether it honours the target set.
//
// It is a classification guard, not a behavioural one. It cannot tell that
// a pass reads its scope correctly - only that somebody decided. The
// behaviour is pinned by each pass's own test
// (TestNodeStampUnmarkedApplyHonoursTheTargetScope,
// discovery's targetscope_test.go, projection's out-of-scope omission).

// liveOrchestrators are the functions this fork runs the live path from.
// Every pass a live run performs is called, directly or through a
// stateless* helper, from one of these four.
var liveOrchestrators = []struct{ file, recv, fn string }{
	// "choudoufu live-plan" and the "-estate" flag form.
	{"live_plan.go", "LivePlanCommand", "livePlan"},
	// Plain "choudoufu plan" / "choudoufu apply" under a live block.
	{"live_mode.go", "statelessRunner", "PriorState"},
	{"live_mode.go", "statelessRunner", "WriteBack"},
	{"live_mode.go", "statelessRunner", "AfterApply"},
}

// targetScopeVerdict is what somebody decided about one pass.
type targetScopeVerdict int

const (
	// scopeAware: the pass is handed this run's [identity.Scope] and
	// narrows by it. Adding one of these means wiring scope through.
	scopeAware targetScopeVerdict = iota

	// planDerived: the pass reads the plan, the projection or another
	// already-narrowed product rather than the configuration, so the
	// target set reached it before it ran. Nothing to wire.
	planDerived

	// wholeConfigByDesign: the pass looks at everything deliberately, and
	// the reason is recorded beside it. The estate sweep's declared set is
	// the canonical case - see [identity.Scope]'s own doc comment for why
	// an out-of-scope block keeps its resolution.
	wholeConfigByDesign

	// notAPass: a constructor, a renderer over an already-computed result,
	// or a pure lookup. It raises nothing and reads nothing live.
	notAPass

	// unscopedKnownGap: the pass reasons over the whole configuration, the
	// target set does not reach it, and that is a filed defect rather than
	// a decision. The issue number is the classification's whole point:
	// this bucket is meant to shrink.
	unscopedKnownGap
)

// liveTargetScopeClassification is the audit of GitHub issue #1203, one
// entry per call the extractor below finds in [liveOrchestrators].
//
// A call the extractor finds and this map does not carry fails the test.
// That is the guard: a pass added tomorrow is visibly in or out.
var liveTargetScopeClassification = map[string]struct {
	verdict targetScopeVerdict
	why     string
}{
	// ---- scope-aware -----------------------------------------------
	"statelessTargetScope":         {scopeAware, "computes the scope; #352"},
	"statelessResolve":             {scopeAware, "identity.Context.Scope; #352"},
	"statelessDataReads":           {scopeAware, "dataread.Options.Scope; #352"},
	"statelessRootOutputDataReads": {scopeAware, "dataread.Options.Scope; #352"},
	"statelessDiscover":            {scopeAware, "discovery.Request.Scope; #1176"},
	"projection.BuildWith":         {scopeAware, "projection.Options.Scope; #1176"},
	"statelessUnmarkedApplyGaps":   {scopeAware, "check.NodeStampUnmarkedApply's scope; #1203"},
	"lint.CheckWith":               {scopeAware, "lint.Context.Scope; #1256. The twelve per-resource rules narrow; moved-block, the live-block settings, the module-call rules and undeclared-provider-alias stay whole-configuration, each with its reason at its own raising site"},
	"lint.CheckResidueAttributes":  {scopeAware, "lint.Context.Scope, same struct; #1256"},
	"statelessPolicyReconcile":     {scopeAware, "discovery.ReconcileRequest.Scope; #1257. The roster is still listed and reported in full; what narrows is discovery.ReconcileResult.Proposable, which is both the set merged in as destroy proposals and the set the threshold guard counts"},

	// ---- narrowed before they run ----------------------------------
	"statelessKubernetesDryRun":        {planDerived, "iterates plan.Changes.Resources, which targeting already pruned"},
	"foreign.Lookalikes":               {planDerived, "reads statelessPlannedCreates(plan)"},
	"projection.ApplyRootOutputValues": {planDerived, "evaluates outputs against projResult.State, itself scoped by BuildWith"},
	"projection.WriteBack":             {planDerived, "reads the final state of an apply that already honoured -target"},
	"untag.Release":                    {planDerived, "releases the tags PriorState captured from a scoped projection"},

	// ---- whole-configuration on purpose ----------------------------
	"statelessEstateFor":              {wholeConfigByDesign, "the estate name is a property of the configuration, not of one resource"},
	"statelessMarkerEstate":           {wholeConfigByDesign, "same question, said out loud; warning-severity and names no resource"},
	"statelessPolicy":                 {wholeConfigByDesign, "resolves the live block's ownership policy; not per-resource"},
	"projection.ReadRootOutputValues": {wholeConfigByDesign, "root outputs are not resources and carry no address to target"},
	"foreign.Classify":                {wholeConfigByDesign, "classifies live objects nothing declares; an estate fact, not a run's"},
	"collectDeposedRecords":           {wholeConfigByDesign, "record reads for crash-window recovery; errors are swallowed, nothing is refused"},

	// ---- filed gaps ------------------------------------------------
	"statelessProviderDataReads": {unscopedKnownGap, "dataread.AnalyzeProviderConfigs and projection.PlanInstances run unscoped; provider work, not a refusal. #1258"},

	// ---- not a pass ------------------------------------------------
	"lint.Diagnostics":                    {notAPass, "renders issues"},
	"lint.HasErrors":                      {notAPass, "reads issues"},
	"identity.DowngradeForNodeResolution": {notAPass, "rewrites diagnostic severities"},
	"identity.SelectionFor":               {notAPass, "reads the live block's markers selection"},
	"identity.NoSourceCreateFor":          {notAPass, "reads the strict profile"},
	"projection.NewMarkerIndex":           {notAPass, "indexes resolutions already in hand"},
	"projection.NewRecordStore":           {notAPass, "opens a store"},
	"projection.NewRecordEnvelopeStore":   {notAPass, "wraps a store"},
	"projection.NewRootOutputStore":       {notAPass, "wraps a store"},
	"projection.RecordStoreKeyPrefix":     {notAPass, "computes a key prefix"},
	"statelessAdoptionReport":             {notAPass, "renders"},
	"statelessBoundReport":                {notAPass, "renders"},
	"statelessForeignReport":              {notAPass, "renders"},
	"statelessLookalikeReport":            {notAPass, "renders"},
	"statelessOmissions":                  {notAPass, "renders"},
	"statelessOwnershipWith":              {notAPass, "builds the ownership rule"},
	"statelessPlannedCreates":             {notAPass, "reads the plan"},
	"statelessPolicyReport":               {notAPass, "renders"},
	"statelessPolicyTagKey":               {notAPass, "reads a field"},
	"statelessNeedsDiscoverySet":          {notAPass, "indexes resolutions already in hand"},
	"statelessUnownedReport":              {notAPass, "renders"},
	"statelessUntagTargets":               {notAPass, "reads discovery's result"},
	"statelessReleasedReport":             {notAPass, "renders untag.Release's outcome"},
}

// TestEveryLiveOrchestratorCallIsClassifiedForTargeting is GitHub issue
// #1203's forward-looking guard: the population is extracted from the
// source of the four [liveOrchestrators], so a pass added to one of them
// fails this test until [liveTargetScopeClassification] says whether the
// run's target set reaches it.
//
// It fails in both directions. An unclassified call is the case it exists
// for; a classified call the orchestrators no longer make is a stale entry,
// which is how TestLayersClassifyEveryLivePackage catches the same rot.
func TestEveryLiveOrchestratorCallIsClassifiedForTargeting(t *testing.T) {
	found := liveOrchestratorCalls(t)
	if len(found) == 0 {
		t.Fatal("extracted no calls at all; the orchestrator list or the extractor is wrong, and a guard that finds nothing passes for the wrong reason")
	}

	for _, name := range found {
		if _, ok := liveTargetScopeClassification[name]; !ok {
			t.Errorf("%s is called from a live orchestrator and is not classified in liveTargetScopeClassification. "+
				"Decide whether this run's -target/-exclude scope reaches it: if it reasons over the whole "+
				"configuration while the run has been narrowed, it can refuse over a resource the run will not "+
				"touch, which is GitHub issues #1176 and #1203.", name)
		}
	}

	// The reason is load-bearing, not decoration: an entry with no reason
	// is somebody silencing the guard rather than deciding, and a filed gap
	// with no issue number is a note nobody can follow.
	for name, c := range liveTargetScopeClassification {
		if strings.TrimSpace(c.why) == "" {
			t.Errorf("%s is classified with no reason; say why, or the next reader has to re-derive it", name)
		}
		if c.verdict == unscopedKnownGap && !strings.Contains(c.why, "#") {
			t.Errorf("%s is classified unscopedKnownGap with no issue number in its reason: %q", name, c.why)
		}
	}

	inFound := make(map[string]bool, len(found))
	for _, n := range found {
		inFound[n] = true
	}
	for name := range liveTargetScopeClassification {
		if !inFound[name] {
			t.Errorf("liveTargetScopeClassification carries %q, which no live orchestrator calls any more; remove the entry", name)
		}
	}
}

// liveOrchestratorCalls is the mechanical population: every call inside one
// of the [liveOrchestrators]' bodies whose callee is either a selector on a
// live-path package, or a package-local helper named stateless*/collect*.
//
// That filter is the definition of "a live-path pass" this audit used, and
// it is deliberately syntactic: a rule a reader can re-run is worth more
// than a list somebody curated. Its bound, stated rather than left to be
// discovered: it sees one level. A pass reached only from inside another
// helper is classified with that helper, not separately.
func liveOrchestratorCalls(t *testing.T) []string {
	t.Helper()

	livePackages := map[string]bool{
		"lint": true, "identity": true, "dataread": true, "stamp": true,
		"discovery": true, "projection": true, "foreign": true,
		"check": true, "untag": true, "policy": true, "markers": true,
	}

	seen := map[string]bool{}
	fset := token.NewFileSet()
	for _, orch := range liveOrchestrators {
		file, err := parser.ParseFile(fset, orch.file, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %s", orch.file, err)
		}
		body := orchestratorBody(file, orch.recv, orch.fn)
		if body == nil {
			t.Fatalf("%s: could not find func (%s) %s; the orchestrator list is stale, and a guard that cannot find its population is not a guard", orch.file, orch.recv, orch.fn)
		}
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				pkg, ok := fn.X.(*ast.Ident)
				if ok && livePackages[pkg.Name] {
					seen[pkg.Name+"."+fn.Sel.Name] = true
				}
			case *ast.Ident:
				if strings.HasPrefix(fn.Name, "stateless") || strings.HasPrefix(fn.Name, "collect") {
					seen[fn.Name] = true
				}
			}
			return true
		})
	}

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// orchestratorBody finds one method's body by receiver type name and method
// name.
func orchestratorBody(file *ast.File, recv, name string) *ast.BlockStmt {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || ident.Name != recv {
			continue
		}
		return fn.Body
	}
	return nil
}
