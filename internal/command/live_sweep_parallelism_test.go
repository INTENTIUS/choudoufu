// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// TestSweepParallelismSetting covers the resolver itself: what each setting
// means, and which ones are refused.
func TestSweepParallelismSetting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		set     bool
		value   string
		want    int
		wantErr bool
	}{
		{name: "unset", set: false, want: discovery.DefaultSweepParallelism},
		{name: "empty", set: true, value: "", want: discovery.DefaultSweepParallelism},
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
			// Unset is the real unset: t.Setenv restores whatever the
			// process had, and the default case has to see no variable at
			// all rather than an empty one.
			// t.Setenv first either way: it is what registers the cleanup
			// that puts the process's own value back, and the unset case
			// has to see no variable at all rather than an empty one.
			t.Setenv(sweepParallelismEnvVar, tc.value)
			if !tc.set {
				if err := os.Unsetenv(sweepParallelismEnvVar); err != nil {
					t.Fatal(err)
				}
			}

			got, diags := sweepParallelismSetting()
			if diags.HasErrors() != tc.wantErr {
				t.Fatalf("%s=%q: HasErrors is %v, want %v (%s)", sweepParallelismEnvVar, tc.value, diags.HasErrors(), tc.wantErr, diags.Err())
			}
			if tc.wantErr {
				// A refused setting still hands back a usable number, so a
				// caller that drops the diagnostics cannot pass zero into
				// discovery, where zero reads as "unset".
				if got != discovery.DefaultSweepParallelism {
					t.Errorf("a refused setting returned %d; want the default %d so that no caller can pass zero to discovery", got, discovery.DefaultSweepParallelism)
				}
				return
			}
			if got != tc.want {
				t.Errorf("%s=%q resolved to %d, want %d", sweepParallelismEnvVar, tc.value, got, tc.want)
			}
		})
	}
}

// TestSweepParallelismRefusesInStocksOwnWords checks the refusal against an
// EXTERNAL source rather than against a copy of itself: it runs live-import's
// own -parallelism through [arguments.ParseLiveImport] and requires the
// sentence that refusal renders to appear verbatim in this one.
//
// GitHub issue #612 item 2 asks for stock's wording specifically because this
// fork now has two parallelism bounds, and two spellings of the same refusal
// is how they start reading as two different rules. live-import's own is the
// nearest one, restated from internal/tofu/context.go for issue #583; if it
// is ever reworded, this fails rather than letting them drift.
func TestSweepParallelismRefusesInStocksOwnWords(t *testing.T) {
	for _, bad := range []string{"0", "-1"} {
		t.Run(bad, func(t *testing.T) {
			_, liDiags := arguments.ParseLiveImport([]string{"-state=x.tfstate", "-estate=e", "-parallelism=" + bad})
			if !liDiags.HasErrors() {
				t.Fatalf("live-import accepted -parallelism=%s; this test reads its refusal, so there is nothing to compare against", bad)
			}
			// -state and -estate are both given, so the parallelism check is
			// the only one that can have fired. If that ever stops being
			// true this reads the wrong diagnostic and fails loudly rather
			// than comparing against nothing.
			if len(liDiags) != 1 {
				t.Fatalf("live-import produced %d diagnostics for -parallelism=%s, want just its parallelism refusal: %s", len(liDiags), bad, liDiags.Err())
			}
			stockSummary := liDiags[0].Description().Summary
			stockDetail := liDiags[0].Description().Detail

			t.Setenv(sweepParallelismEnvVar, bad)
			_, diags := sweepParallelismSetting()
			if !diags.HasErrors() {
				t.Fatalf("%s=%s was accepted; want the same refusal stock's own -parallelism makes", sweepParallelismEnvVar, bad)
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

// TestSweepParallelismReachesTheDiscoveryRequest is the wiring pin GitHub
// issue #612 exists for.
//
// The defect it reports was not a wrong value, it was no reference at all:
// discovery.Request.SweepParallelism was set by nothing in internal/command,
// so the engine's default was the only reachable setting and every behavioral
// test stayed green - there is no output, no diagnostic and no call count that
// differs between one setting and another against a mock cloud, which is why
// a behavioral test cannot hold this and this one reads the source.
//
// It is deliberately structural rather than a string match: it requires that
// the field is set from an identifier that statelessDiscoverOne RECEIVES, and
// that every call site passes what sweepParallelismSetting returned. A
// hard-coded constant, a dropped argument or a second local would each fail
// it, which "the file contains SweepParallelism" would not.
func TestSweepParallelismReachesTheDiscoveryRequest(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "live_plan.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing live_plan.go: %v", err)
	}

	one := findFuncDecl(t, file, "statelessDiscoverOne")
	params := paramNames(one)

	// The field is set, and it is set from one of the function's own
	// parameters.
	fieldExpr := requestFieldValue(t, one, "SweepParallelism")
	ident, ok := fieldExpr.(*ast.Ident)
	if !ok {
		t.Fatalf("discovery.Request.SweepParallelism is set from a %T, not from a parameter of statelessDiscoverOne. Issue #612 is about this value coming from the run rather than from a constant.", fieldExpr)
	}
	paramIndex := -1
	for i, name := range params {
		if name == ident.Name {
			paramIndex = i
			break
		}
	}
	if paramIndex < 0 {
		t.Fatalf("discovery.Request.SweepParallelism is set from %q, which is not a parameter of statelessDiscoverOne (parameters: %v). Issue #612's knob has to arrive from the caller.", ident.Name, params)
	}

	// The caller resolves it from the environment, once, and hands it to
	// every pass.
	discover := findFuncDecl(t, file, "statelessDiscover")
	resolved := ""
	ast.Inspect(discover, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) == 0 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "sweepParallelismSetting" {
			return true
		}
		if lhs, ok := assign.Lhs[0].(*ast.Ident); ok {
			resolved = lhs.Name
		}
		return false
	})
	if resolved == "" {
		t.Fatalf("statelessDiscover no longer calls sweepParallelismSetting. It is the single funnel every entry point that sweeps goes through - live-plan's -estate form and live_mode.go's live-block path - so nothing else can carry %s to the engine.", sweepParallelismEnvVar)
	}

	calls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn, ok := call.Fun.(*ast.Ident)
		if !ok || fn.Name != "statelessDiscoverOne" {
			return true
		}
		calls++
		if paramIndex >= len(call.Args) {
			t.Errorf("a statelessDiscoverOne call at %s passes %d arguments, too few to carry %s", fset.Position(call.Pos()), len(call.Args), params[paramIndex])
			return true
		}
		arg, ok := call.Args[paramIndex].(*ast.Ident)
		if !ok || arg.Name != resolved {
			t.Errorf("the statelessDiscoverOne call at %s passes %s for %s, not the %q that sweepParallelismSetting resolved", fset.Position(call.Pos()), exprText(call.Args[paramIndex]), params[paramIndex], resolved)
		}
		return true
	})
	if calls == 0 {
		t.Fatal("no calls to statelessDiscoverOne found in live_plan.go; this test is measuring nothing")
	}
}

// TestSweepParallelismIsDocumentedBesideTheFlagItIsNot holds the other half
// of issue #612 item 3. Two parallelism bounds now exist on one pipeline, and
// the place they are most likely to be confused is the one place both appear:
// live-plan's own help, where -parallelism has always been listed. Losing
// either half of that pairing is what turns two names into one ambiguity.
func TestSweepParallelismIsDocumentedBesideTheFlagItIsNot(t *testing.T) {
	help := (&LivePlanCommand{}).Help()
	for _, want := range []string{
		sweepParallelismEnvVar,
		"It does not bound the\n                          marker sweep",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("live-plan's help no longer contains %q, so nothing tells an operator which of the two parallelism bounds they are setting", want)
		}
	}
}

// TestLivePlan_sweepParallelismIsReachableFromTheCommandLine is the end-to-end
// half: a whole live-plan run under the setting, through the same command an
// operator runs.
//
// 1 is the case issue #612 names - it reproduces the sequential sweep, and the
// run's output has to be the one the default produces, not a degraded one.
func TestLivePlan_sweepParallelismIsReachableFromTheCommandLine(t *testing.T) {
	run := func(t *testing.T) (int, string) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
			"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
		})

		c, done := newLivePlanCommand(t, cloud)
		code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data"})
		output := done(t)
		return code, output.Stdout() + output.Stderr()
	}

	t.Run("sequential", func(t *testing.T) {
		t.Setenv(sweepParallelismEnvVar, "1")
		code, out := run(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - %s=1 must reproduce the sequential sweep, not break the run\n%s", code, sweepParallelismEnvVar, out)
		}
		if !strings.Contains(out, "No changes.") {
			t.Errorf("the sequential sweep did not produce the same plan the default does:\n%s", out)
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Setenv(sweepParallelismEnvVar, "0")
		code, out := run(t)
		if code == 0 {
			t.Fatalf("%s=0 planned successfully; a non-positive bound must be refused, never read as \"no limit\"\n%s", sweepParallelismEnvVar, out)
		}
		if !strings.Contains(out, "The parallelism must be a positive value. Not 0.") {
			t.Errorf("the run failed, but not with stock's refusal:\n%s", out)
		}
	})
}

// findFuncDecl returns the named top-level function, failing the test when it
// is gone rather than passing over an absent one.
func findFuncDecl(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("live_plan.go has no function %s; this test cannot check the wiring it was written for", name)
	return nil
}

// paramNames flattens a function's parameter list into one name per position,
// expanding the grouped form ("a, b int") the way a call's argument list sees
// it.
func paramNames(fn *ast.FuncDecl) []string {
	var names []string
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			names = append(names, name.Name)
		}
	}
	return names
}

// requestFieldValue returns the expression a discovery.Request composite
// literal inside fn assigns to the named field.
func requestFieldValue(t *testing.T, fn *ast.FuncDecl, field string) ast.Expr {
	t.Helper()
	var found ast.Expr
	ast.Inspect(fn, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Request" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "discovery" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == field {
				found = kv.Value
			}
		}
		return false
	})
	if found == nil {
		t.Fatalf("the discovery.Request built in %s does not set %s. A request field internal/command never references leaves the engine's default as the only setting a run can have - the defect shape GitHub issues #612 and #745 both report.", fn.Name.Name, field)
	}
	return found
}

// exprText renders an expression for a failure message, well enough to name
// what was passed instead.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.BasicLit:
		return v.Value
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	default:
		return "a non-identifier expression"
	}
}
