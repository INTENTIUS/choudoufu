// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestReadParallelismSetting covers the resolver itself: what each setting
// means, and which ones are refused.
func TestReadParallelismSetting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     bool
		value   string
		want    int
		wantErr bool
	}{
		{name: "unset", set: false, want: projection.DefaultReadParallelism},
		{name: "empty", set: true, value: "", want: projection.DefaultReadParallelism},
		{name: "sequential", set: true, value: "1", want: 1},
		{name: "turned down", set: true, value: "2", want: 2},
		{name: "surrounding whitespace", set: true, value: "  4\n", want: 4},
		{name: "turned up", set: true, value: "24", want: 24},
		{name: "zero", set: true, value: "0", wantErr: true},
		{name: "negative", set: true, value: "-1", wantErr: true},
		{name: "not a number", set: true, value: "lots", wantErr: true},
		{name: "fractional", set: true, value: "2.5", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// t.Setenv first either way: it is what registers the cleanup
			// that puts the process's own value back, and the unset case has
			// to see no variable at all rather than an empty one.
			t.Setenv(readParallelismEnvVar, tc.value)
			if !tc.set {
				if err := os.Unsetenv(readParallelismEnvVar); err != nil {
					t.Fatal(err)
				}
			}

			got, diags := readParallelismSetting()
			if diags.HasErrors() != tc.wantErr {
				t.Fatalf("%s=%q: HasErrors is %v, want %v (%s)", readParallelismEnvVar, tc.value, diags.HasErrors(), tc.wantErr, diags.Err())
			}
			if tc.wantErr {
				// A refused setting still hands back a usable number, so a
				// caller that drops the diagnostics cannot pass zero into the
				// projection, where zero reads as "unset" and answers ten.
				if got != projection.DefaultReadParallelism {
					t.Errorf("a refused setting returned %d; want the default %d so that no caller can pass zero to the projection", got, projection.DefaultReadParallelism)
				}
				return
			}
			if got != tc.want {
				t.Errorf("%s=%q resolved to %d, want %d", readParallelismEnvVar, tc.value, got, tc.want)
			}
		})
	}
}

// TestReadParallelismRefusesInStocksOwnWords checks the refusal against an
// EXTERNAL source rather than against a copy of itself: it runs live-import's
// own -parallelism through [arguments.ParseLiveImport] and requires the
// sentence that refusal renders to appear verbatim in this one.
//
// This fork now bounds three phases of one pipeline, and three spellings of the
// same refusal is how they start reading as three different rules. live-import's
// own is the one restated from internal/tofu/context.go for issue #583, and
// [sweepParallelismSetting] already restates it for issue #612; if it is ever
// reworded, this fails rather than letting them drift.
func TestReadParallelismRefusesInStocksOwnWords(t *testing.T) {
	for _, bad := range []string{"0", "-1"} {
		t.Run(bad, func(t *testing.T) {
			_, liDiags := arguments.ParseLiveImport([]string{"-state=x.tfstate", "-estate=e", "-parallelism=" + bad})
			if !liDiags.HasErrors() {
				t.Fatalf("live-import accepted -parallelism=%s; this test reads its refusal, so there is nothing to compare against", bad)
			}
			// -state and -estate are both given, so the parallelism check is
			// the only one that can have fired. If that ever stops being true
			// this reads the wrong diagnostic and fails loudly rather than
			// comparing against nothing.
			if len(liDiags) != 1 {
				t.Fatalf("live-import produced %d diagnostics for -parallelism=%s, want just its parallelism refusal: %s", len(liDiags), bad, liDiags.Err())
			}
			stockSummary := liDiags[0].Description().Summary
			stockDetail := liDiags[0].Description().Detail

			t.Setenv(readParallelismEnvVar, bad)
			_, diags := readParallelismSetting()
			if !diags.HasErrors() {
				t.Fatalf("%s=%s was accepted; want the same refusal stock's own -parallelism makes", readParallelismEnvVar, bad)
			}
			desc := diags[0].Description()
			if desc.Summary != stockSummary {
				t.Errorf("summary %q, want live-import's own %q", desc.Summary, stockSummary)
			}
			if !strings.Contains(desc.Detail, stockDetail) {
				t.Errorf("detail\n  %q\ndoes not contain live-import's own sentence\n  %q", desc.Detail, stockDetail)
			}
		})
	}
}

// TestReadParallelismReachesEveryProjectionOptions is the wiring pin GitHub
// issue #626 exists for.
//
// The defect it reports was not a wrong value, it was no reference at all:
// projection.Options.ReadParallelism was set by nothing outside
// internal/live/projection, so the engine's default of ten was the only setting
// a run could have. That stayed green through every behavioural test in this
// package, and it would have stayed green through a behavioural test written
// FOR it, because against a mock cloud the width of the read pass changes no
// output, no diagnostic and no call count - see
// TestLivePlan_readParallelismBoundsTheReadPass, which has to instrument the
// provider itself to see any difference at all.
//
// So this test reads the source, and it is structural rather than a string
// match: it requires that every projection.Options this package builds sets the
// field, that the value is an identifier, and that the identifier traces to
// [readParallelismSetting]'s own result - through a parameter for the one site
// that lives in a helper. A hard-coded constant, a second local or a dropped
// argument would each fail it, and "the file contains ReadParallelism" would
// catch none of them.
//
// # What this test is NOT, since issue #640
//
// It is the PROVENANCE half only: for the sites it names, that the value came
// from the run. It used to carry the completeness half too, as a count of
// three projection.Options across live_plan.go and live_mode.go, and that
// count was green while internal/live/mv/mv.go's fourth construction sat
// unwired - a scope of two files cannot see a fourth site in a third package,
// so the count passed precisely by not looking. Completeness is now
// [TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded], which walks the
// whole checkout; a new construction site fails there rather than here.
func TestReadParallelismReachesEveryProjectionOptions(t *testing.T) {
	fset := token.NewFileSet()
	planFile := parseCommandSource(t, fset, "live_plan.go")
	modeFile := parseCommandSource(t, fset, "live_mode.go")
	mvFile := parseCommandSource(t, fset, "live_mv.go")

	// The two entry points that resolve the setting. Each one has to build a
	// projection.Options whose ReadParallelism is the local its own
	// readParallelismSetting call produced.
	type entryPoint struct {
		file *ast.File
		name string
		what string
	}
	resolvedIn := map[string]string{}
	for _, ep := range []entryPoint{
		{file: planFile, name: "livePlan", what: `live-plan's own "-estate" form`},
		{file: modeFile, name: "PriorState", what: `the live-block path plain "choudoufu plan" and "choudoufu apply" run`},
	} {
		fn := findAnyFuncDecl(t, ep.file, ep.name)
		resolved := identAssignedFromCall(fn, "readParallelismSetting")
		if resolved == "" {
			t.Fatalf("%s no longer calls readParallelismSetting. It is %s, and nothing else can carry %s to the projection from there.", ep.name, ep.what, readParallelismEnvVar)
		}
		resolvedIn[ep.name] = resolved

		values := projectionOptionsField(fn, "ReadParallelism")
		if len(values) != 1 {
			t.Fatalf("%s builds %d projection.Options that set ReadParallelism, want exactly 1. That field being unset is precisely the defect GitHub issue #626 reports: it exists, nothing in internal/command references it, and the engine's default becomes the only setting a run can have.", ep.name, len(values))
		}
		ident, ok := values[0].(*ast.Ident)
		if !ok {
			t.Fatalf("%s sets projection.Options.ReadParallelism from a %T (%s), not from an identifier. Issue #626 is about this value coming from the run rather than from a constant.", ep.name, values[0], exprText(values[0]))
		}
		if ident.Name != resolved {
			t.Errorf("%s sets ReadParallelism from %q, not from the %q that readParallelismSetting resolved", ep.name, ident.Name, resolved)
		}
	}

	// The third site is in a helper, so the value has to arrive as an
	// argument, and every caller has to pass its own resolved local.
	helper := findAnyFuncDecl(t, planFile, "statelessProviderDataReads")
	params := paramNames(helper)
	values := projectionOptionsField(helper, "ReadParallelism")
	if len(values) != 1 {
		t.Fatalf("statelessProviderDataReads builds %d projection.Options that set ReadParallelism, want exactly 1", len(values))
	}
	ident, ok := values[0].(*ast.Ident)
	if !ok {
		t.Fatalf("statelessProviderDataReads sets projection.Options.ReadParallelism from a %T (%s), not from a parameter", values[0], exprText(values[0]))
	}
	paramIndex := -1
	for i, name := range params {
		if name == ident.Name {
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		t.Fatalf("statelessProviderDataReads sets ReadParallelism from %q, which is not one of its parameters (parameters: %v). Issue #626's knob has to arrive from the caller, so that one run cannot read at two different widths.", ident.Name, params)
	}

	callers := map[string]string{"live_plan.go": resolvedIn["livePlan"], "live_mode.go": resolvedIn["PriorState"]}
	calls := 0
	for _, f := range []struct {
		name string
		file *ast.File
	}{{"live_plan.go", planFile}, {"live_mode.go", modeFile}} {
		ast.Inspect(f.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "statelessProviderDataReads" {
				return true
			}
			calls++
			if paramIndex >= len(call.Args) {
				t.Errorf("a statelessProviderDataReads call at %s passes %d arguments, too few to carry %s", fset.Position(call.Pos()), len(call.Args), params[paramIndex])
				return true
			}
			arg, ok := call.Args[paramIndex].(*ast.Ident)
			if !ok || arg.Name != callers[f.name] {
				t.Errorf("the statelessProviderDataReads call at %s passes %s for %s, not the %q that readParallelismSetting resolved in that function", fset.Position(call.Pos()), exprText(call.Args[paramIndex]), params[paramIndex], callers[f.name])
			}
			return true
		})
	}
	if calls != 2 {
		t.Errorf("found %d calls to statelessProviderDataReads across live_plan.go and live_mode.go, want 2 - one per entry point. A caller this test cannot see is a read pass this variable does not reach.", calls)
	}

	// The fourth entry point, GitHub issue #640's. live-mv builds no
	// projection.Options of its own - internal/live/mv's materialize builds
	// it, out of a Request field - so the provenance check here is that the
	// command resolves the setting and puts it in the request. The engine
	// half, that mv.go's projection.Options reads that field rather than a
	// constant, is [TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded]'s.
	mvFn := findAnyFuncDecl(t, mvFile, "liveMv")
	mvResolved := identAssignedFromCall(mvFn, "readParallelismSetting")
	if mvResolved == "" {
		t.Fatalf("liveMv no longer calls readParallelismSetting. It is live-mv's only entry point, and nothing else can carry %s into the rename's read pass.", readParallelismEnvVar)
	}
	mvValues := compositeLitField(mvFn, "mv", "Request", "ReadParallelism")
	if len(mvValues) != 1 {
		t.Fatalf("liveMv builds %d mv.Request that set ReadParallelism, want exactly 1. Unset is issue #640's defect: live-mv's read pass runs at the engine's default whatever %s says.", len(mvValues), readParallelismEnvVar)
	}
	if id, ok := mvValues[0].(*ast.Ident); !ok || id.Name != mvResolved {
		t.Errorf("liveMv sets mv.Request.ReadParallelism from %s, not from the %q that readParallelismSetting resolved", exprText(mvValues[0]), mvResolved)
	}
}

// projectionOptionsExclusions names every projection.Options construction in
// the tree that deliberately does NOT set ReadParallelism, with the reason.
//
// A site listed here is a decision. A site missing from here and from the
// wiring is GitHub issue #626's defect - a read pass running at the engine's
// default whatever an operator sets - and
// [TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded] fails on it. An entry
// here that no longer matches a site is also a failure, so the list cannot
// outlive the code it excuses.
//
// Keys are the repository-relative file and the enclosing function or method,
// deliberately not a line number: a line number goes stale on any edit above
// it and would make this list noise.
var projectionOptionsExclusions = []struct{ file, fn, reason string }{
	{
		file: "internal/live/projection/build.go",
		fn:   "BuildFrom",
		// This one is not an unbounded read pass that someone forgot; it is
		// the definition of the default. BuildFrom is the package's own
		// no-options wrapper over buildFrom, and Options{} there is what
		// makes ReadParallelism zero, which readconcurrency.go reads as
		// DefaultReadParallelism. Threading a bound into it would mean
		// giving it an options parameter, at which point it is BuildWith,
		// which already exists and is what all four command-side callers
		// use.
		reason: "BuildFrom is the options-free wrapper whose empty Options IS the engine default; a caller that wants a bound calls BuildWith",
	},
}

// TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded is the completeness
// half of issue #626's pin, rewritten for GitHub issue #640.
//
// What it replaces, and why the replacement is a different shape. The original
// completeness check counted projection.Options constructions across
// live_plan.go and live_mode.go and required the answer to be 3. That number
// was correct and the check was green, and the whole time there was a fourth
// construction in internal/live/mv/mv.go reading at the engine default. The
// count did not miss it; the count could not see it, because its scope was two
// files in one package and the gap was in another package entirely. A
// completeness check whose scope is narrower than the thing it claims to be
// complete about is not a weak check, it is a check of something else.
//
// So this one's scope is the checkout. It walks every non-test Go file, finds
// every projection.Options composite literal - qualified anywhere, and bare
// inside package projection itself, which is where the type is declared - and
// requires each to either set ReadParallelism from something that is not a
// constant, or appear in [projectionOptionsExclusions] with a reason. A stale
// exclusion fails too.
//
// # What it does not cover, stated rather than implied
//
//   - Test files. The defect class is a read pass a user's run makes at a
//     width the operator did not choose; internal/live/projection's own tests
//     build a hundred-odd Options{} deliberately unbounded, and pulling those
//     in would mean an exclusion list longer than the thing it guards. A
//     _test.go file that reads at the wrong width misleads nobody outside it.
//   - A value that is an identifier but the WRONG identifier. That is
//     [TestReadParallelismReachesEveryProjectionOptions]'s job, per site, and
//     it can only be done where the provenance is known.
//   - A type alias for projection.Options, which would construct one under
//     another name. None exists; nothing here would notice one.
//
// The spellings that would otherwise be invisible to a composite-literal sweep
// are forbidden instead of analysed: `var o projection.Options` and a slice or
// map of them fail with an instruction to spell the literal out. That is
// cheaper than following an assignment chain and it cannot be wrong.
func TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded(t *testing.T) {
	root := repoRootFromCommandTest(t)
	fset := token.NewFileSet()

	type site struct {
		file string
		fn   string
		pos  token.Position
		val  ast.Expr // the ReadParallelism value; nil when the field is unset
	}
	var sites []site

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case path == root:
				return nil
			// Not our source: git's own store, a vendored tree, the docs
			// site's theme submodule, and testdata, which holds Go files
			// that are fixtures rather than code and need not even parse.
			case name == ".git" || name == "vendor" || name == "testdata" || name == "node_modules":
				return fs.SkipDir
			case strings.HasPrefix(name, "."):
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		// Cheap prefilter over a superset of both spellings, so that 1700
		// files are read and only a handful are parsed. It cannot skip a
		// construction: every one of them contains the word.
		if !bytes.Contains(src, []byte("Options")) {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		file, parseErr := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if parseErr != nil {
			return fmt.Errorf("parsing %s: %w", rel, parseErr)
		}
		// Bare Options{} means projection.Options only inside the package
		// that declares it. Elsewhere a bare Options{} is some other
		// package's own, and there are many.
		bare := file.Name.Name == "projection"

		isOpts := func(e ast.Expr) bool {
			if isSelector(e, "projection", "Options") {
				return true
			}
			if !bare {
				return false
			}
			id, ok := e.(*ast.Ident)
			return ok && id.Name == "Options"
		}

		record := func(fn string, n ast.Node) {
			ast.Inspect(n, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CompositeLit:
					if !isOpts(x.Type) {
						// A slice or map OF them constructs elements this
						// sweep cannot see, since an element literal carries
						// no type of its own.
						switch container := x.Type.(type) {
						case *ast.ArrayType:
							if isOpts(container.Elt) {
								t.Errorf("%s builds a slice or array of projection.Options at %s. Spell each element out as its own projection.Options literal, so that this test can check its ReadParallelism.", rel, fset.Position(x.Pos()))
							}
						case *ast.MapType:
							if isOpts(container.Value) {
								t.Errorf("%s builds a map of projection.Options at %s. Spell each value out as its own projection.Options literal, so that this test can check its ReadParallelism.", rel, fset.Position(x.Pos()))
							}
						}
						return true
					}
					s := site{file: rel, fn: fn, pos: fset.Position(x.Pos())}
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "ReadParallelism" {
							s.val = kv.Value
						}
					}
					sites = append(sites, s)
				case *ast.ValueSpec:
					// `var o projection.Options` and then o.Field = ... is a
					// construction with no literal to inspect. Forbidden
					// rather than followed.
					if x.Type != nil && isOpts(x.Type) {
						t.Errorf("%s declares a projection.Options variable at %s. Build it as a composite literal instead, so that this test can check its ReadParallelism.", rel, fset.Position(x.Pos()))
					}
				}
				return true
			})
		}

		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				record(fn.Name.Name, fn)
				continue
			}
			// Package scope: a var block, most likely. Named "" so an
			// exclusion for one has to say so.
			record("", decl)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	// A scanner that saw nothing would report everything wired. These four
	// exist today and one of them is the exclusion, so a walk that misses any
	// of them has gone wrong somewhere other than the code under test - a
	// wrong root, a prefilter that dropped a file, a skipped directory. This
	// is a floor and not a count: a fifth site is not a failure here, it is a
	// failure below unless it is wired or excused.
	found := map[string]int{}
	for _, s := range sites {
		found[s.file]++
	}
	for _, want := range []struct {
		file string
		n    int
	}{
		{"internal/command/live_plan.go", 2},
		{"internal/command/live_mode.go", 1},
		{"internal/live/mv/mv.go", 1},
		{"internal/live/projection/build.go", 1},
	} {
		if found[want.file] < want.n {
			t.Fatalf("the walk found %d projection.Options in %s, want at least %d. This test is not measuring what it claims to; check the root (%s) and the directory skips before reading anything below.", found[want.file], want.file, want.n, root)
		}
	}

	excluded := map[string]string{}
	for _, ex := range projectionOptionsExclusions {
		key := ex.file + " " + ex.fn
		if prior, dup := excluded[key]; dup {
			t.Errorf("projectionOptionsExclusions has two entries for %q (%q and %q). One would silently shadow the other, and the stale-entry check below would not see it.", key, prior, ex.reason)
		}
		excluded[key] = ex.reason
	}
	matched := map[string]bool{}

	for _, s := range sites {
		key := s.file + " " + s.fn
		reason, isExcluded := excluded[key]
		if isExcluded {
			matched[key] = true
			if s.val != nil {
				t.Errorf("the projection.Options at %s DOES set ReadParallelism, but projectionOptionsExclusions still excuses it as %q. Delete the exclusion.", s.pos, reason)
			}
			continue
		}
		if s.val == nil {
			t.Errorf("the projection.Options at %s (%s, in %s) does not set ReadParallelism, so that read pass runs at the engine's default whatever %s says.\nWire it - the value has to come from the run, not from a constant - or add it to projectionOptionsExclusions with the reason it should not honour the setting. Issue #626 left exactly one site unwired this way and it took issue #640 to find it.", s.pos, s.fn, s.file, readParallelismEnvVar)
			continue
		}
		if lit, ok := s.val.(*ast.BasicLit); ok {
			t.Errorf("the projection.Options at %s sets ReadParallelism to the constant %s. The point of %s is that the width comes from the run.", s.pos, lit.Value, readParallelismEnvVar)
		}
	}

	// Over the slice rather than the map, so that two stale entries report in
	// the order they are written rather than in Go's map order.
	for _, ex := range projectionOptionsExclusions {
		key := ex.file + " " + ex.fn
		if !matched[key] {
			t.Errorf("projectionOptionsExclusions excuses %q as %q, and the walk found no projection.Options there. A stale exclusion is a hole: delete it.", key, ex.reason)
		}
	}
}

// repoRootFromCommandTest resolves this checkout's root from this file's own
// location, the same approach internal/live/registry's own repoRoot uses, and
// then proves it is a checkout rather than trusting the arithmetic.
func repoRootFromCommandTest(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at internal/command/live_read_parallelism_test.go.
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved the repository root as %s, which has no go.mod: %v. A tree-wide test that walks the wrong tree finds nothing and passes.", root, err)
	}
	return root
}

// compositeLitField returns the expression every pkg.Name composite literal
// inside root assigns to the named field. It is [projectionOptionsField] for
// any other qualified type; that one is kept as it is because its own name is
// what its failure messages read as.
func compositeLitField(root ast.Node, pkg, name, field string) []ast.Expr {
	var found []ast.Expr
	ast.Inspect(root, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSelector(lit.Type, pkg, name) {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
				found = append(found, kv.Value)
			}
		}
		return true
	})
	return found
}

// TestReadParallelismIsDocumentedBesideTheOthers holds the documentation half
// of issue #626. Three bounds now exist on one pipeline - stock's -parallelism
// on the graph walk, the sweep's variable on the marker sweep, this one on the
// read pass - and the one place all three appear is live-plan's own help. An
// operator who sees only two of them cannot tell which phase they are turning
// down, which is the confusion the separate names exist to avoid.
func TestReadParallelismIsDocumentedBesideTheOthers(t *testing.T) {
	help := (&LivePlanCommand{}).Help()
	for _, want := range []string{
		// The entry in the "Environment variables:" list, not merely the name
		// somewhere in the help. Written as the indented "NAME=n" line the
		// list is made of, because a first draft of this test looked for the
		// bare name and PASSED with the whole entry deleted - the cross
		// reference in -parallelism's own text below still contained it.
		"\n  " + readParallelismEnvVar + "=n\n",
		"\n  " + sweepParallelismEnvVar + "=n\n",
		"  -parallelism=n ",
		// The pairing itself, not just the three names: -parallelism has to
		// say that it is not this one.
		"Nor does\n                          it bound the read pass",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("live-plan's help no longer contains %q, so nothing tells an operator which of the three parallelism bounds they are setting", want)
		}
	}

	// Issue #640's addition. live-mv honours the same variable, so its own
	// help has to say so: an operator who has turned the read pass down for a
	// migration reaches for live-mv during that same migration, and live-plan's
	// help is not where they look for what live-mv does.
	mvHelp := (&LiveMvCommand{}).Help()
	for _, want := range []string{
		"\nEnvironment variables:\n",
		"\n  " + readParallelismEnvVar + "=n\n",
	} {
		if !strings.Contains(mvHelp, want) {
			t.Errorf("live-mv's help does not contain %q. It honours %s since issue #640, and a knob nothing documents is one nobody sets.", want, readParallelismEnvVar)
		}
	}
	// The sweep's variable is deliberately NOT claimed here, and its absence
	// is asserted rather than left to chance. live-mv runs no estate-wide
	// sweep at all: internal/live/mv's own sweep lists ONE resource type
	// through listclient, and discovery.Discover - the thing
	// SweepParallelism bounds - is never called from that package. Naming a
	// bound that does nothing is worse than naming neither, so if this ever
	// starts appearing, either the sweep arrived or the help is lying.
	if strings.Contains(mvHelp, sweepParallelismEnvVar) {
		t.Errorf("live-mv's help mentions %s, which bounds discovery.Discover's estate-wide sweep - a pass live-mv does not run. Either it does now, in which case wire it, or the help is describing something that does not happen.", sweepParallelismEnvVar)
	}
}

// planCostDocRel is the operator-facing page for what a plan costs, which is
// where someone who wants a plan to cost less goes looking.
const planCostDocRel = "site/content/docs/model/plan-cost.md"

// TestBothParallelismKnobsAreDocumentedOnThePlanCostPage is GitHub issue
// #640's third half.
//
// Command help is where an operator looks once they already know a knob
// exists. The plan-cost page is where they look when they only know the plan
// is slow, and until this issue it named DefaultSweepParallelism - the engine
// constant, not the variable - and said nothing about the read pass having a
// bound at all. One of the two phases had a documented lever and the other
// had a silent one.
//
// The check is scoped to one section rather than to the whole file, because
// "the page mentions the name somewhere" is the failure mode a first draft of
// the help pin above actually had: the name appeared in a cross reference and
// the entry itself was gone. Both names have to be in the same section, and
// that section has to say which phase each bounds - which is the thing the
// separate names exist to make answerable.
func TestBothParallelismKnobsAreDocumentedOnThePlanCostPage(t *testing.T) {
	path := filepath.Join(repoRootFromCommandTest(t), filepath.FromSlash(planCostDocRel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", planCostDocRel, err)
	}
	page := string(raw)

	const heading = "\n## Turning a phase down\n"
	start := strings.Index(page, heading)
	if start < 0 {
		t.Fatalf("%s no longer has a %q section. If it was renamed, point this test at the new heading rather than widening it to the whole page - a name that appears anywhere in a long page is not documentation of the knob.", planCostDocRel, strings.TrimSpace(heading))
	}
	section := page[start+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}

	for _, want := range []string{
		// Both names, in one section, so an operator reading about one is
		// told the other exists.
		sweepParallelismEnvVar,
		readParallelismEnvVar,
		// And which phase each bounds. Two bounds of ten on one pipeline are
		// indistinguishable without this, which is the confusion the two
		// separate names exist to prevent.
		"the sweep's per-type list calls",
		"the read pass's per-instance import and read",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the %q section of %s does not contain %q", strings.TrimSpace(heading), planCostDocRel, want)
		}
	}
}

// TestLivePlan_readParallelismBoundsTheReadPass is the end-to-end half, and it
// measures the one thing a mock cloud CAN show about this knob: how many of the
// read pass's provider calls overlap.
//
// Issue #612 recorded that nothing observable differs between its settings
// against a mock cloud, which is how the sweep's missing wiring stayed green.
// Two things had to be true for that to stop being true here, and both were
// found by writing the probe and watching it read 1 at every setting:
//
//   - The calls have to be able to overlap in the DOUBLE, not only in the read
//     pass. tofu.MockProvider holds its own mutex across every callback
//     (internal/tofu/provider_mock.go's ImportResourceState, p.Lock()), and
//     newLivePlanCommand hands out one instance per provider configuration, so
//     four resources under one provider block are serialised by the test double
//     however wide the pass runs. The fixture declares four provider
//     configurations for exactly this reason - see its own comment.
//   - The probe has to hold each call. A mock answers in microseconds, so a
//     pass running ten wide can still have one call in flight at every instant
//     a probe looks. [readWidthRecorder] parks each call until the width it is
//     looking for arrives or a deadline passes, which makes both directions
//     assertions rather than races: at 1 a second caller cannot arrive however
//     long the first waits, and at the default all four do.
//
// The wall clock says the same thing without being asserted on: the default
// subtest returns in milliseconds because the four calls meet, and the
// sequential one takes four deadlines because they never do.
func TestLivePlan_readParallelismBoundsTheReadPass(t *testing.T) {
	// The four buckets the fixture declares, in the address order the read
	// pass materializes them in.
	wantImports := []string{
		"aws_s3_bucket/tofu-stateless-read-a",
		"aws_s3_bucket/tofu-stateless-read-b",
		"aws_s3_bucket/tofu-stateless-read-c",
		"aws_s3_bucket/tofu-stateless-read-d",
	}

	run := func(t *testing.T, rec *readWidthRecorder) (int, string, []string) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan-read-parallelism"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		// One region per provider block in the fixture. See its own comment
		// for why there are four of them rather than one.
		for _, region := range []string{"us-east-2", "us-west-1", "us-west-2"} {
			cloud.allowRegion(region)
		}
		for _, name := range []string{"a", "b", "c", "d"} {
			id := "tofu-stateless-read-" + name
			cloud.putMarked("aws_s3_bucket", id, "stateless-unit", fmt.Sprintf("aws_s3_bucket.%s", name), map[string]string{
				"id": id, "bucket": id,
			})
		}
		cloud.onImport = rec.hook

		c, done := newLivePlanCommand(t, cloud)
		code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
		output := done(t)
		cloud.mu.Lock()
		imports := append([]string(nil), cloud.imports...)
		cloud.mu.Unlock()
		return code, output.Stdout() + output.Stderr(), imports
	}

	t.Run("default", func(t *testing.T) {
		// No setting at all: the run takes projection.DefaultReadParallelism,
		// which is more than four, so all four reads overlap.
		t.Setenv(readParallelismEnvVar, "")
		if err := os.Unsetenv(readParallelismEnvVar); err != nil {
			t.Fatal(err)
		}
		rec := &readWidthRecorder{want: len(wantImports), wait: 10 * time.Second}
		code, out, imports := run(t, rec)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\n%s", code, out)
		}
		if peak := rec.peakWidth(); peak != len(wantImports) {
			t.Errorf("the read pass had at most %d calls in flight at once, want %d - the default is %d, so nothing should have held these four apart", peak, len(wantImports), projection.DefaultReadParallelism)
		}
		if got := sortedCopy(imports); !equalStrings(got, wantImports) {
			t.Errorf("the read pass made %v, want %v", got, wantImports)
		}
	})

	t.Run("sequential", func(t *testing.T) {
		// 1 is the case issue #626 names. Two things have to hold: the reads
		// never overlap, and the run's answer is the one the default produces
		// rather than a degraded one.
		t.Setenv(readParallelismEnvVar, "1")
		// want 2, so a second caller arriving at any point during the first
		// one's whole wait would be seen. None can, so each call pays the
		// deadline - four short waits, not one long one.
		rec := &readWidthRecorder{want: 2, wait: 200 * time.Millisecond}
		code, out, imports := run(t, rec)
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - %s=1 must reproduce the sequential read pass, not break the run\n%s", code, readParallelismEnvVar, out)
		}
		if peak := rec.peakWidth(); peak != 1 {
			t.Errorf("%s=1 still had %d read calls in flight at once; 1 means one at a time", readParallelismEnvVar, peak)
		}
		if !strings.Contains(out, "No changes.") {
			t.Errorf("the sequential read pass did not produce the same plan the default does:\n%s", out)
		}
		// Loop order, not merely one at a time - the documented meaning of 1,
		// and the half that is only assertable BECAUSE nothing overlaps.
		if !equalStrings(imports, wantImports) {
			t.Errorf("%s=1 read in the order %v, want loop order %v", readParallelismEnvVar, imports, wantImports)
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Setenv(readParallelismEnvVar, "0")
		rec := &readWidthRecorder{want: 1, wait: time.Second}
		code, out, _ := run(t, rec)
		if code == 0 {
			t.Fatalf("%s=0 planned successfully; a non-positive bound must be refused, never read as \"no limit\"\n%s", readParallelismEnvVar, out)
		}
		if !strings.Contains(out, "The parallelism must be a positive value. Not 0.") {
			t.Errorf("the run failed, but not with stock's refusal:\n%s", out)
		}
		if peak := rec.peakWidth(); peak != 0 {
			t.Errorf("a refused setting still read %d instances from the cloud; the refusal has to land before anything is read", peak)
		}
	})
}

// readWidthRecorder measures the peak number of provider read calls a run had
// in flight at once, and holds each one long enough for that measurement to
// mean something.
//
// The holding is the point. A mock provider answers in microseconds, so a read
// pass running ten wide can still have exactly one call in flight at every
// instant a probe looks, and a peak of 1 would then prove nothing about the
// bound. So a call parks until either want callers have arrived - at which
// point every parked one is released together - or wait elapses. A peak below
// want is therefore a fact about the bound rather than about scheduling.
type readWidthRecorder struct {
	// want is the width to wait for, and wait is how long a call parks when
	// that width never arrives.
	want int
	wait time.Duration

	mu       sync.Mutex
	inFlight int
	peak     int
	released chan struct{}
}

// hook is what [statelessTestCloud.onImport] is set to: true on entry to a
// provider read, false as it returns.
func (r *readWidthRecorder) hook(entering bool) {
	if !entering {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
		return
	}

	r.mu.Lock()
	r.inFlight++
	if r.inFlight > r.peak {
		r.peak = r.inFlight
	}
	if r.released == nil {
		r.released = make(chan struct{})
	}
	gate := r.released
	reached := r.inFlight >= r.want
	if reached {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}
	r.mu.Unlock()

	if reached {
		return
	}
	select {
	case <-gate:
	case <-time.After(r.wait):
	}
}

// peakWidth is the most calls this recorder ever had in flight at once.
func (r *readWidthRecorder) peakWidth() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

// parseCommandSource parses one of this package's own source files.
func parseCommandSource(t *testing.T, fset *token.FileSet, name string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	return file
}

// findAnyFuncDecl returns the named function or method, failing the test when
// it is gone rather than passing over an absent one. Unlike [findFuncDecl] it
// accepts a method, which is what PriorState is.
func findAnyFuncDecl(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("no function or method %s; this test cannot check the wiring it was written for", name)
	return nil
}

// identAssignedFromCall returns the name the first `x, ... := callee()`
// assignment in fn binds, or "" when fn makes no such call.
func identAssignedFromCall(fn *ast.FuncDecl, callee string) string {
	resolved := ""
	ast.Inspect(fn, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != callee {
			return true
		}
		if lhs, ok := assign.Lhs[0].(*ast.Ident); ok {
			resolved = lhs.Name
		}
		return false
	})
	return resolved
}

// projectionOptionsField returns the expression every projection.Options
// composite literal inside root assigns to the named field. A literal that does
// not set the field contributes nothing, so the length is how many DO. root is
// any node: a function declaration for the per-site checks, one literal for the
// completeness sweep.
func projectionOptionsField(root ast.Node, field string) []ast.Expr {
	var found []ast.Expr
	ast.Inspect(root, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSelector(lit.Type, "projection", "Options") {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
				found = append(found, kv.Value)
			}
		}
		return true
	})
	return found
}

// isSelector reports whether e is the qualified name pkg.name.
func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

// sortedCopy is in-order for a comparison that is about WHICH calls were made
// rather than in what order, which is the only thing the concurrent case can
// assert about ordering.
func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
