// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
// that lives in a helper. A hard-coded constant, a second local, a dropped
// argument or a fourth construction site added later would each fail it, and
// "the file contains ReadParallelism" would catch none of them.
func TestReadParallelismReachesEveryProjectionOptions(t *testing.T) {
	fset := token.NewFileSet()
	planFile := parseCommandSource(t, fset, "live_plan.go")
	modeFile := parseCommandSource(t, fset, "live_mode.go")

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

	// Completeness, which is the half a per-site check cannot hold: a FOURTH
	// projection.Options built in this package later would be unbounded, and
	// nothing above would notice. This counts every one of them, set or not.
	total := 0
	for _, file := range []*ast.File{planFile, modeFile} {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok || !isSelector(lit.Type, "projection", "Options") {
				return true
			}
			total++
			if len(projectionOptionsField(lit, "ReadParallelism")) == 0 {
				t.Errorf("the projection.Options at %s does not set ReadParallelism, so that read pass runs at the engine's default whatever %s says", fset.Position(lit.Pos()), readParallelismEnvVar)
			}
			return true
		})
	}
	if total != 3 {
		t.Errorf("found %d projection.Options constructions in live_plan.go and live_mode.go, want the 3 GitHub issue #626 names. A new one is not a failure in itself - wire it and update this count.", total)
	}
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
