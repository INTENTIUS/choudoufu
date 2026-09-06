// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staticeval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestAllowedIsPinnedByValue pins the allowlist itself rather than any use
// of it. The five roots are the whole static scope: internal/configs'
// staticScopeData answers exactly these and PANICS ("Not Available in
// Static Context") on the rest, so this table is the contract every
// pre-filter on the live path depends on, and a root silently added to or
// dropped from it is a class of expression silently gained or lost across
// five packages at once.
func TestAllowedIsPinnedByValue(t *testing.T) {
	tests := []struct {
		root        string
		allowed     bool
		evaluable   bool
		description string
	}{
		{root: "var", allowed: true, evaluable: true, description: "an input variable"},
		{root: "local", allowed: true, evaluable: true, description: "a local value"},
		{root: "path", allowed: true, evaluable: true, description: "path.module and friends"},
		{root: "terraform", allowed: true, evaluable: true, description: "terraform.workspace"},
		{root: "tofu", allowed: true, evaluable: true, description: "tofu.workspace"},

		// Evaluable but not Allowed: the static scope will not produce a
		// value, but the evaluator (or identity's own richer scope) deals
		// with each of these itself rather than leaving a caller to treat
		// it as a managed-resource reference.
		{root: "count", allowed: false, evaluable: true, description: "count.index, answered from repetition data"},
		{root: "module", allowed: false, evaluable: true, description: "a module output"},
		{root: "data", allowed: false, evaluable: true, description: "a data source, answered once the data-read phase has run"},
		{root: "self", allowed: false, evaluable: true, description: "self.*, inside a provisioner"},

		// Neither: each.* is handled structurally by identity, and any
		// other root in a resource argument is a managed resource.
		{root: "each", allowed: false, evaluable: false, description: "each.key/each.value"},
		{root: "aws_s3_bucket", allowed: false, evaluable: false, description: "a managed resource"},
		{root: "random_pet", allowed: false, evaluable: false, description: "another managed resource"},
		{root: "", allowed: false, evaluable: false, description: "no root at all"},
		{root: "Var", allowed: false, evaluable: false, description: "case matters; HCL identifiers are not folded"},
		{root: "variable", allowed: false, evaluable: false, description: "the block keyword is not the reference root"},
		{root: "locals", allowed: false, evaluable: false, description: "the block keyword is not the reference root"},
	}
	for _, tc := range tests {
		t.Run(tc.root+" ("+tc.description+")", func(t *testing.T) {
			if got := Allowed(tc.root); got != tc.allowed {
				t.Errorf("Allowed(%q) = %v, want %v", tc.root, got, tc.allowed)
			}
			if got := Evaluable(tc.root); got != tc.evaluable {
				t.Errorf("Evaluable(%q) = %v, want %v", tc.root, got, tc.evaluable)
			}
			if tc.allowed && !tc.evaluable {
				t.Errorf("Allowed(%q) is true but Evaluable(%q) is false; Evaluable is defined as the wider set", tc.root, tc.root)
			}
		})
	}
}

// TestFirstDisallowedNamesTheRoot exercises the expression-level predicates
// over real parsed HCL, including the cases the root table cannot reach: an
// expression with no traversals at all, one mixing an allowed root with a
// refused one, and the ORDER the refused root is reported in, which is what
// several callers put in their refusal text.
func TestFirstDisallowedNamesTheRoot(t *testing.T) {
	tests := []struct {
		src         string
		wantAllowed bool
		wantRoot    string
	}{
		{src: `"a literal"`, wantAllowed: true},
		{src: `var.name`, wantAllowed: true},
		{src: `"${var.a}-${local.b}"`, wantAllowed: true},
		{src: `upper(var.name)`, wantAllowed: true},
		{src: `[for x in var.xs : upper(x)]`, wantAllowed: true},
		{src: `aws_s3_bucket.b.arn`, wantAllowed: false, wantRoot: "aws_s3_bucket"},
		{src: `"${var.a}-${aws_s3_bucket.b.arn}"`, wantAllowed: false, wantRoot: "aws_s3_bucket"},
		{src: `"${aws_s3_bucket.b.arn}-${var.a}"`, wantAllowed: false, wantRoot: "aws_s3_bucket"},
		{src: `each.value`, wantAllowed: false, wantRoot: "each"},
		{src: `count.index`, wantAllowed: false, wantRoot: "count"},
		{src: `data.aws_ami.x.id`, wantAllowed: false, wantRoot: "data"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			expr := parseExpr(t, tc.src)
			if got := AllowedExpr(expr); got != tc.wantAllowed {
				t.Errorf("AllowedExpr(%s) = %v, want %v", tc.src, got, tc.wantAllowed)
			}
			root, found := FirstDisallowed(expr)
			if found == tc.wantAllowed {
				t.Fatalf("FirstDisallowed(%s) found = %v, want %v", tc.src, found, !tc.wantAllowed)
			}
			if found && root != tc.wantRoot {
				t.Errorf("FirstDisallowed(%s) = %q, want %q", tc.src, root, tc.wantRoot)
			}
		})
	}
}

// TestEvaluateRecoversAPanic is the load-bearing half of [Evaluate]: the
// static scope panics rather than erroring for the reference classes it
// does not serve, and a crash there would take a whole run down over one
// expression that was always going to be refused.
//
// The control below is what makes this a check rather than a green light:
// it asserts the same expression really does panic when handed straight to
// the evaluator, so this test cannot pass by evaluating something harmless.
func TestEvaluateRecoversAPanic(t *testing.T) {
	mod := loadModule(t, `locals { a = "x" }`)
	expr := panicExpr{msg: "Not Available in Static Context"}
	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: "test", DeclRange: hcl.Range{}}

	t.Run("control: the evaluator really panics on it", func(t *testing.T) {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("evaluating panicExpr directly did not panic; the recover test below would pass vacuously")
			}
		}()
		//nolint:errcheck // the call is expected to panic; nothing returns.
		mod.StaticEvaluator.Evaluate(t.Context(), expr, ident)
	})

	t.Run("Evaluate turns it into a refusal", func(t *testing.T) {
		val, diags, recovered := Evaluate(t.Context(), mod.StaticEvaluator, expr, ident)
		if recovered == nil {
			t.Fatal("Evaluate returned recovered == nil for an expression that panics")
		}
		if got := val; got != cty.NilVal {
			t.Errorf("Evaluate returned val = %#v on a panic, want cty.NilVal", got)
		}
		if len(diags) != 0 {
			t.Errorf("Evaluate returned %d diagnostics on a panic, want none", len(diags))
		}
		if s, ok := recovered.(string); !ok || !strings.Contains(s, "Not Available in Static Context") {
			t.Errorf("Evaluate returned recovered = %#v, want the panic value itself", recovered)
		}
	})

	t.Run("EvaluateOK reports not-ok rather than crashing", func(t *testing.T) {
		if _, ok := EvaluateOK(t.Context(), mod.StaticEvaluator, expr, "test"); ok {
			t.Error("EvaluateOK reported ok for an expression that panics")
		}
	})
}

// TestEvaluateValuesAndRefusals pins what [Evaluate] and [EvaluateOK] do
// with the two ordinary outcomes: a value, and a diagnostic. A value is
// returned only when the diagnostics carry no error, and an error never
// comes back with a value beside it.
func TestEvaluateValuesAndRefusals(t *testing.T) {
	mod := loadModule(t, `
variable "name" {
  type    = string
  default = "from-a-variable"
}

locals {
  joined = "${var.name}-suffix"
}
`)
	ident := configs.StaticIdentifier{Module: addrs.RootModule, Subject: "test", DeclRange: hcl.Range{}}

	t.Run("a value", func(t *testing.T) {
		val, diags, recovered := Evaluate(t.Context(), mod.StaticEvaluator, parseExpr(t, `local.joined`), ident)
		if recovered != nil || diags.HasErrors() {
			t.Fatalf("Evaluate refused a static expression: recovered=%v diags=%s", recovered, diags.Error())
		}
		if val.AsString() != "from-a-variable-suffix" {
			t.Errorf("Evaluate = %#v, want \"from-a-variable-suffix\"", val)
		}
	})

	t.Run("an undeclared local is a diagnostic, not a panic and not a value", func(t *testing.T) {
		val, diags, recovered := Evaluate(t.Context(), mod.StaticEvaluator, parseExpr(t, `local.nope`), ident)
		if recovered != nil {
			t.Fatalf("Evaluate recovered a panic for an undeclared local: %v", recovered)
		}
		if !diags.HasErrors() {
			t.Fatal("Evaluate accepted an undeclared local")
		}
		if val != cty.NilVal {
			t.Errorf("Evaluate returned val = %#v beside an error, want cty.NilVal", val)
		}
	})
}

// TestCountAndForEachKeys pins the two expansion derivations against a real
// module: what each computes, and every shape each refuses. The refusals
// matter as much as the answers - a guessed instance key becomes a guessed
// marker, so "not computable here" is the only other answer allowed.
func TestCountAndForEachKeys(t *testing.T) {
	mod := loadModule(t, `
variable "secret" {
  type      = string
  default   = "s3cret"
  sensitive = true
}

locals {
  three     = 3
  names     = ["b", "a", "c"]
  keyed     = { z = 1, a = 2 }
  fractional = 1.5
  numbers   = [1, 2]
  secrets   = [var.secret]
}
`)

	t.Run("count", func(t *testing.T) {
		tests := []struct {
			src    string
			want   int
			wantOK bool
		}{
			{src: `3`, want: 3, wantOK: true},
			{src: `local.three`, want: 3, wantOK: true},
			{src: `length(local.names)`, want: 3, wantOK: true},
			{src: `local.fractional`, wantOK: false},
			{src: `local.names`, wantOK: false},
			{src: `var.secret`, wantOK: false},
			{src: `aws_s3_bucket.b.count`, wantOK: false},
			{src: `count.index`, wantOK: false},
			{src: `local.nope`, wantOK: false},
		}
		for _, tc := range tests {
			t.Run(tc.src, func(t *testing.T) {
				got, ok := Count(t.Context(), mod, parseExpr(t, tc.src))
				if ok != tc.wantOK {
					t.Fatalf("Count(%s) ok = %v, want %v (got %d)", tc.src, ok, tc.wantOK, got)
				}
				if ok && got != tc.want {
					t.Errorf("Count(%s) = %d, want %d", tc.src, got, tc.want)
				}
			})
		}
	})

	t.Run("for_each", func(t *testing.T) {
		tests := []struct {
			src    string
			want   []string
			wantOK bool
		}{
			{src: `local.names`, want: []string{"a", "b", "c"}, wantOK: true},
			{src: `toset(local.names)`, want: []string{"a", "b", "c"}, wantOK: true},
			{src: `local.keyed`, want: []string{"a", "z"}, wantOK: true},
			{src: `local.numbers`, wantOK: false},
			{src: `local.three`, wantOK: false},
			{src: `local.secrets`, wantOK: false},
			{src: `var.secret`, wantOK: false},
			{src: `aws_s3_bucket.b.tags`, wantOK: false},
			{src: `each.value`, wantOK: false},
		}
		for _, tc := range tests {
			t.Run(tc.src, func(t *testing.T) {
				got, ok := ForEachKeys(t.Context(), mod, parseExpr(t, tc.src))
				if ok != tc.wantOK {
					t.Fatalf("ForEachKeys(%s) ok = %v, want %v (got %v)", tc.src, ok, tc.wantOK, got)
				}
				if !ok {
					return
				}
				if strings.Join(got, ",") != strings.Join(tc.want, ",") {
					t.Errorf("ForEachKeys(%s) = %v, want %v", tc.src, got, tc.want)
				}
			})
		}
	})

	t.Run("a module with no static evaluator answers neither", func(t *testing.T) {
		if _, ok := Count(t.Context(), nil, parseExpr(t, `3`)); ok {
			t.Error("Count answered for a nil module")
		}
		if _, ok := ForEachKeys(t.Context(), &configs.Module{}, parseExpr(t, `["a"]`)); ok {
			t.Error("ForEachKeys answered for a module with no static evaluator")
		}
	})
}

// TestArgument exercises every shape the select-by-path read has to tell
// apart - a literal, a value read through a local, an argument the block
// never sets, an empty string, a value that depends on another resource's
// own attribute, and one that is sensitive. The reason text is asserted,
// not only the refusal: two callers put it verbatim in what a user reads.
func TestArgument(t *testing.T) {
	mod := loadModule(t, `
variable "secret" {
  type      = string
  default   = "s3cret"
  sensitive = true
}

locals {
  policy_name = "from-a-local"
}

resource "aws_cloudfront_cache_policy" "literal" {
  name = "literal-name"
}

resource "aws_cloudfront_cache_policy" "from_local" {
  name = local.policy_name
}

resource "aws_cloudfront_cache_policy" "computed" {
  name = upper(local.policy_name)
}

resource "aws_cloudfront_cache_policy" "no_name" {
  min_ttl = 1
}

resource "aws_cloudfront_cache_policy" "empty" {
  name = ""
}

resource "aws_cloudfront_cache_policy" "sensitive" {
  name = var.secret
}

resource "aws_s3_bucket" "other" {
  bucket = "irrelevant"
}

resource "aws_cloudfront_cache_policy" "dynamic" {
  name = aws_s3_bucket.other.arn
}

resource "aws_cloudfront_cache_policy" "each" {
  name = each.value
}
`)

	tests := []struct {
		addr    string
		want    string
		wantWhy string // substring expected in the failure reason, or "" for success
	}{
		{addr: "aws_cloudfront_cache_policy.literal", want: "literal-name"},
		{addr: "aws_cloudfront_cache_policy.from_local", want: "from-a-local"},
		{addr: "aws_cloudfront_cache_policy.computed", want: "FROM-A-LOCAL"},
		{addr: "aws_cloudfront_cache_policy.no_name", wantWhy: "sets no name argument"},
		{addr: "aws_cloudfront_cache_policy.empty", wantWhy: "is empty, which matches nothing"},
		{addr: "aws_cloudfront_cache_policy.sensitive", wantWhy: "not a plain known value"},
		{addr: "aws_cloudfront_cache_policy.dynamic", wantWhy: "refers to aws_s3_bucket, which is not known until the run is under way"},
		{addr: "aws_cloudfront_cache_policy.each", wantWhy: "refers to each, which is not known until the run is under way"},
	}
	for _, tc := range tests {
		t.Run(tc.addr, func(t *testing.T) {
			rc, ok := mod.ManagedResources[tc.addr]
			if !ok {
				t.Fatalf("fixture does not declare %s", tc.addr)
			}
			got, why := Argument(t.Context(), mod, rc, "name")
			if tc.wantWhy != "" {
				if why == "" {
					t.Fatalf("Argument succeeded with %q, want a refusal containing %q", got, tc.wantWhy)
				}
				if !strings.Contains(why, tc.wantWhy) {
					t.Errorf("Argument reason = %q, want it to contain %q", why, tc.wantWhy)
				}
				if got != "" {
					t.Errorf("Argument returned %q beside a refusal, want the empty string", got)
				}
				return
			}
			if why != "" {
				t.Fatalf("Argument refused: %s", why)
			}
			if got != tc.want {
				t.Errorf("Argument = %q, want %q", got, tc.want)
			}
		})
	}
}

// panicExpr is an expression whose evaluation panics, standing in for the
// static scope's own "Not Available in Static Context" panic without having
// to build the several-module-deep configuration that produces it for real.
// Variables() returns nothing, so it passes every pre-filter in this package
// and reaches the evaluator exactly as the real shape does.
type panicExpr struct {
	msg string
}

var _ hcl.Expression = panicExpr{}

func (e panicExpr) Value(*hcl.EvalContext) (cty.Value, hcl.Diagnostics) { panic(e.msg) }
func (e panicExpr) Variables() []hcl.Traversal                          { return nil }
func (e panicExpr) Range() hcl.Range                                    { return hcl.Range{Filename: "panic.tf"} }
func (e panicExpr) StartRange() hcl.Range                               { return e.Range() }

// parseExpr parses one HCL expression from source.
func parseExpr(t *testing.T, src string) hcl.Expression {
	t.Helper()
	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", src, diags.Error())
	}
	return expr
}

// loadModule writes src to a temporary directory and loads it as a root
// module, so that mod.StaticEvaluator is the real one internal/configs
// builds rather than a hand-assembled stand-in.
//
// The fixture is written at run time on purpose. internal/live is one of
// the two trees internal/live/check's TestIdentityGolden sweeps, so a
// checked-in testdata/*.tf directory here would add lines to a golden that
// exists to make a MOVED line an alarm - fixture bookkeeping in the one
// file whose diff has to stay readable.
func loadModule(t *testing.T, src string) *configs.Module {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading fixture: %s", diags.Error())
	}
	return mod
}
