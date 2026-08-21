// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// scopeFixture is GitHub issue #352's own shape: an aws_budgets_budget whose
// account_id nothing in the configuration states, beside two resources that
// resolve. The refusal it raises is the reported one, word for word -
// "Identity argument not set ... has no value for \"account_id\", so its
// import identity (ACCOUNT_ID:NAME) cannot be built" - and the estate that
// found it scopes that resource out with -target precisely because floci does
// not implement AWS Budgets at all.
const scopeFixture = `
resource "aws_cloudwatch_log_group" "wanted" {
  name = "/wanted"
}

resource "aws_cloudwatch_log_group" "also_fine" {
  name = "/also-fine"
}

resource "aws_budgets_budget" "rotten" {
  name = "monthly-limit"
}
`

// TestScopeWithholdsAnOutOfScopeRefusal is GitHub issue #352's fix, stated as
// the two runs it has to tell apart.
//
// A targeted run whose scope leaves the unresolvable resource out resolves
// cleanly, because stock OpenTofu's plan graph drops that resource before
// anything evaluates it and this fork's own passes have to agree.
//
// The same configuration with the same resource IN scope still refuses, which
// is the mutation check on the fix: remove only the stated obstacle - the
// scope - and confirm the case goes back to refusing. Without this half the
// test would pass just as well against a resolver that had stopped refusing
// altogether.
func TestScopeWithholdsAnOutOfScopeRefusal(t *testing.T) {
	cfg := writeScopeFixture(t, scopeFixture, "")

	t.Run("out of scope", func(t *testing.T) {
		res, diags := ResolveWith(t.Context(), cfg, Context{
			Scope: scopeExcluding("aws_budgets_budget.rotten"),
		})
		if diags.HasErrors() {
			t.Fatalf("a targeted run refused on a resource outside its own scope: %s", diags.Err())
		}
		assertResolved(t, res, []string{
			"aws_cloudwatch_log_group.also_fine",
			"aws_cloudwatch_log_group.wanted",
		})
	})

	t.Run("in scope", func(t *testing.T) {
		if _, diags := ResolveWith(t.Context(), cfg, Context{}); !diags.HasErrors() {
			t.Fatal("an untargeted run resolved the budget's account_id; the fixture no longer refuses, so the case above proves nothing")
		}
		if _, diags := ResolveWith(t.Context(), cfg, Context{Scope: func(addrs.ConfigResource) bool { return true }}); !diags.HasErrors() {
			t.Fatal("a scope that includes everything withheld a refusal; only an out-of-scope block's refusal may be dropped")
		}
	})
}

// TestScopeStillDeclaresWhatItDoesNotEvaluate pins the half of the fix that
// is not about refusing: an out-of-scope resource that resolves normally KEEPS
// its resolution.
//
// internal/live/discovery builds its "this address is declared" set from the
// resolutions it is handed (declared.all), and that set is what stops the
// estate-wide marker sweep reading a live object as an orphan to remove. A
// scope that dropped every out-of-scope resource outright would turn every
// marked object outside the target set into an undeclared one, which - under
// a policy with undeclared_untagged = "delete" - is a threshold away from
// being acted on. Skipping evaluation is not the fix; withholding the
// refusal is.
func TestScopeStillDeclaresWhatItDoesNotEvaluate(t *testing.T) {
	cfg := writeScopeFixture(t, scopeFixture, "")

	res, diags := ResolveWith(t.Context(), cfg, Context{
		Scope: scopeOnly("aws_cloudwatch_log_group.wanted"),
	})
	if diags.HasErrors() {
		t.Fatalf("unexpected refusal: %s", diags.Err())
	}
	// also_fine is out of scope and resolves anyway; rotten is out of scope
	// and cannot, so it is absent exactly as the plan's own targeting would
	// have made it.
	assertResolved(t, res, []string{
		"aws_cloudwatch_log_group.also_fine",
		"aws_cloudwatch_log_group.wanted",
	})
}

// TestScopeReachesIntoModules pins that the scope is asked about a resource's
// module-qualified configuration address, not its module-relative one. A
// scope keyed on the bare block address would answer for
// module.child.aws_cloudwatch_log_group.rotten whatever it answered for the
// root's - which is the whole of a multi-module estate scoped by one -target.
func TestScopeReachesIntoModules(t *testing.T) {
	cfg := writeScopeFixture(t, `
resource "aws_cloudwatch_log_group" "root" {
  name = "/root-is-fine"
}

module "child" {
  source = "./child"
}
`, `
resource "aws_budgets_budget" "rotten" {
  name = "monthly-limit"
}
`)

	if _, diags := ResolveWith(t.Context(), cfg, Context{}); !diags.HasErrors() {
		t.Fatal("the child module's budget no longer refuses; the case below proves nothing")
	}

	res, diags := ResolveWith(t.Context(), cfg, Context{
		Scope: scopeExcluding("module.child.aws_budgets_budget.rotten"),
	})
	if diags.HasErrors() {
		t.Fatalf("excluding the child module's resource did not withhold its refusal: %s", diags.Err())
	}
	assertResolved(t, res, []string{"aws_cloudwatch_log_group.root"})
}

// TestNilScopeIsEveryBlock is the common case, and the one that must not have
// moved: every caller that has no plan graph to ask - live-check, live-mv,
// the whole offline corpus, and every untargeted plan - passes no scope and
// must get exactly the answers this package always gave.
func TestNilScopeIsEveryBlock(t *testing.T) {
	cfg := writeScopeFixture(t, `
resource "aws_cloudwatch_log_group" "a" {
  name = "/a"
}

resource "aws_cloudwatch_log_group" "b" {
  name = "/b"
}
`, "")

	plain, plainDiags := Resolve(t.Context(), cfg)
	scoped, scopedDiags := ResolveWith(t.Context(), cfg, Context{Scope: nil})
	if plainDiags.HasErrors() || scopedDiags.HasErrors() {
		t.Fatalf("unexpected refusals: %s / %s", plainDiags.Err(), scopedDiags.Err())
	}
	assertResolved(t, plain, []string{
		"aws_cloudwatch_log_group.a",
		"aws_cloudwatch_log_group.b",
	})
	assertResolved(t, scoped, []string{
		"aws_cloudwatch_log_group.a",
		"aws_cloudwatch_log_group.b",
	})
	for i, r := range plain.All() {
		if got := scoped.All()[i]; got.ImportID != r.ImportID || got.Class != r.Class {
			t.Errorf("a nil scope changed %s: %s/%s, want %s/%s", r.Addr, got.Class, got.ImportID, r.Class, r.ImportID)
		}
	}
}

func scopeExcluding(addrStrs ...string) Scope {
	out := make(map[string]bool, len(addrStrs))
	for _, s := range addrStrs {
		out[s] = true
	}
	return func(addr addrs.ConfigResource) bool { return !out[addr.String()] }
}

func scopeOnly(addrStrs ...string) Scope {
	in := make(map[string]bool, len(addrStrs))
	for _, s := range addrStrs {
		in[s] = true
	}
	return func(addr addrs.ConfigResource) bool { return in[addr.String()] }
}

func assertResolved(t *testing.T, res *Result, want []string) {
	t.Helper()
	var got []string
	for _, r := range res.All() {
		got = append(got, r.Addr.String())
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("resolved\n  %v\nwant\n  %v", got, want)
	}
}

func writeScopeFixture(t *testing.T, root, child string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(root), 0o600); err != nil {
		t.Fatal(err)
	}
	if child != "" {
		if err := os.Mkdir(filepath.Join(dir, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child", "main.tf"), []byte(child), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return loadConfigTree(t, dir, map[string]cty.Value{})
}
