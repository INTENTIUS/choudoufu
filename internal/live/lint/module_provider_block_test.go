// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"testing"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// GitHub issue #201: no buildable HCL fixture can drive
// checkModuleProviderBlocks' refusal branch through loadConfigDir, because
// internal/configs.BuildConfig's own validateProviderConfigs (the upstream
// code this fork forked verbatim) already hard-errors before
// internal/live/lint ever runs on the one combination this rule would
// refuse - a call chain using count, for_each, enabled or depends_on
// reaching a content-bearing local provider block. See
// notYetEnforcedLimits's "module-provider-block" entry in limits_test.go
// and TestCheck's admitted cases in lint_test.go for the fixtures this left
// behind. These tests exercise the rule's own logic directly instead, the
// same way stock OpenTofu's provider_validation_test.go exercises
// validateProviderConfigs's switch directly.

// TestModuleCallBlocksLocalProviders pins moduleCallBlocksLocalProviders
// against all four meta-arguments internal/configs/provider_validation.go's
// own switch (provider_validation.go:298-312) checks, in the same priority
// order, plus the call with none of them.
func TestModuleCallBlocksLocalProviders(t *testing.T) {
	rng := hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: 1}, End: hcl.Pos{Line: 1, Column: 2}}
	depRange := hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: 2}, End: hcl.Pos{Line: 2, Column: 2}}

	litExpr := &hclsyntax.LiteralValueExpr{SrcRange: rng}

	tests := []struct {
		name string
		call *configs.ModuleCall
		want *hcl.Range
	}{
		{
			name: "nil call",
			call: nil,
			want: nil,
		},
		{
			name: "no meta-arguments",
			call: &configs.ModuleCall{DeclRange: rng},
			want: nil,
		},
		{
			name: "count",
			call: &configs.ModuleCall{Count: litExpr},
			want: &rng,
		},
		{
			name: "for_each",
			call: &configs.ModuleCall{ForEach: litExpr},
			want: &rng,
		},
		{
			name: "enabled",
			call: &configs.ModuleCall{Enabled: litExpr},
			want: &rng,
		},
		{
			name: "depends_on",
			call: &configs.ModuleCall{DependsOn: []hcl.Traversal{{hcl.TraverseRoot{Name: "aws_vpc", SrcRange: depRange}}}},
			want: &depRange,
		},
		{
			name: "depends_on with an empty list",
			call: &configs.ModuleCall{DependsOn: []hcl.Traversal{}, DeclRange: rng},
			want: &rng,
		},
		{
			// Priority order matches provider_validation.go's switch
			// exactly: count is checked first, so a call carrying both
			// count and for_each (not legal HCL in practice, but the
			// function's own contract) reports count's range.
			name: "count takes priority over for_each",
			call: &configs.ModuleCall{Count: litExpr, ForEach: &hclsyntax.LiteralValueExpr{SrcRange: depRange}},
			want: &rng,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := moduleCallBlocksLocalProviders(tt.call)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("moduleCallBlocksLocalProviders() = %v, want %v", got, tt.want)
			}
			if got != nil && *got != *tt.want {
				t.Errorf("moduleCallBlocksLocalProviders() = %v, want %v", *got, *tt.want)
			}
		})
	}
}

// parseProviderBody parses src as an HCL body for a synthetic
// *configs.Provider's Config field, the same shape configs.decodeProviderBlock
// would produce from a real "provider" block's remaining body after alias
// and version are extracted.
func parseProviderBody(t *testing.T, src string) hcl.Body {
	t.Helper()
	if src == "" {
		return hcl.EmptyBody()
	}
	f, diags := hclsyntax.ParseConfig([]byte(src), "child/main.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing test HCL body: %s", diags.Error())
	}
	return f.Body
}

// TestConfiguredProviderBlock pins configuredProviderBlock against
// internal/configs/provider_validation.go:340-351's own configured/
// emptyConfigs split.
func TestConfiguredProviderBlock(t *testing.T) {
	tests := []struct {
		name string
		pc   *configs.Provider
		want bool
	}{
		{
			name: "empty block",
			pc:   &configs.Provider{Name: "aws", Config: parseProviderBody(t, "")},
			want: false,
		},
		{
			name: "block with an attribute",
			pc:   &configs.Provider{Name: "aws", Config: parseProviderBody(t, `region = "us-west-2"`+"\n")},
			want: true,
		},
		{
			name: "empty block with a version constraint",
			pc: &configs.Provider{
				Name:    "aws",
				Config:  parseProviderBody(t, ""),
				Version: configs.VersionConstraint{Required: mustVersionConstraint(t, ">= 1.0.0")},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := configuredProviderBlock(tt.pc); got != tt.want {
				t.Errorf("configuredProviderBlock() = %v, want %v", got, tt.want)
			}
		})
	}
}

// mustVersionConstraint builds a real version.Constraints value, so
// TestConfiguredProviderBlock's "version constraint, otherwise empty" case
// exercises configuredProviderBlock's pc.Version.Required != nil branch
// with the same type internal/configs actually populates that field with,
// rather than a stand-in.
func mustVersionConstraint(t *testing.T, raw string) version.Constraints {
	t.Helper()
	c, err := version.NewConstraint(raw)
	if err != nil {
		t.Fatalf("version.NewConstraint(%q): %s", raw, err)
	}
	return c
}

// TestCheckModuleProviderBlocksAdmitsEmptyEvenWhenBlocked calls
// checkModuleProviderBlocks directly - bypassing the config loader entirely,
// since no loadable *configs.Config can put it in this state (see this
// file's own package doc comment) - to prove the configured/empty split
// GitHub issue #201 added: a content-bearing block is refused only when
// noProviderConfigRange is non-nil, and an empty (proxy) block is never
// refused, blocked chain or not.
func TestCheckModuleProviderBlocksAdmitsEmptyEvenWhenBlocked(t *testing.T) {
	blockedRange := hcl.Range{Filename: "main.tf", Start: hcl.Pos{Line: 5}, End: hcl.Pos{Line: 5, Column: 2}}
	declRange := hcl.Range{Filename: "child/main.tf", Start: hcl.Pos{Line: 1}, End: hcl.Pos{Line: 1, Column: 1}}

	configured := &configs.Provider{Name: "aws", DeclRange: declRange, Config: parseProviderBody(t, `region = "us-west-2"`+"\n")}
	empty := &configs.Provider{Name: "aws", DeclRange: declRange, Config: parseProviderBody(t, "")}
	path := addrs.Module{"compute"}

	tests := []struct {
		name       string
		mod        *configs.Module
		blockRange *hcl.Range
		wantIssues int
	}{
		{
			name:       "configured block, no blocking ancestor",
			mod:        &configs.Module{ProviderConfigs: map[string]*configs.Provider{"aws": configured}},
			blockRange: nil,
			wantIssues: 0,
		},
		{
			name:       "configured block, blocked chain",
			mod:        &configs.Module{ProviderConfigs: map[string]*configs.Provider{"aws": configured}},
			blockRange: &blockedRange,
			wantIssues: 1,
		},
		{
			name:       "empty block, no blocking ancestor",
			mod:        &configs.Module{ProviderConfigs: map[string]*configs.Provider{"aws": empty}},
			blockRange: nil,
			wantIssues: 0,
		},
		{
			name:       "empty block, blocked chain - still admitted",
			mod:        &configs.Module{ProviderConfigs: map[string]*configs.Provider{"aws": empty}},
			blockRange: &blockedRange,
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var issues []Issue
			checkModuleProviderBlocks(tt.mod, path, tt.blockRange, &issues)
			if len(issues) != tt.wantIssues {
				t.Errorf("checkModuleProviderBlocks() produced %d issues, want %d: %v", len(issues), tt.wantIssues, issues)
			}
			for _, issue := range issues {
				if issue.Rule != RuleModuleProviderBlock {
					t.Errorf("got rule %q, want %q", issue.Rule, RuleModuleProviderBlock)
				}
			}
		})
	}

	// Root is never refused regardless of content or blocking, matching
	// [checkModuleProviderBlocks]'s own root guard.
	t.Run("root module, configured block, blocked chain", func(t *testing.T) {
		var issues []Issue
		checkModuleProviderBlocks(&configs.Module{ProviderConfigs: map[string]*configs.Provider{"aws": configured}}, addrs.RootModule, &blockedRange, &issues)
		if len(issues) != 0 {
			t.Errorf("checkModuleProviderBlocks() at root produced %d issues, want 0: %v", len(issues), issues)
		}
	})
}
