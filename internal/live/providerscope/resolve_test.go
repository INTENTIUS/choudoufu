// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package providerscope

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// loadConfigDir builds a configuration tree from a directory the same way
// the live path's own tests do (internal/live/lint/lint_test.go's helper of
// the same name): configs.Parser plus configs.BuildConfig, with a module
// walker that resolves local source addresses straight from disk. Kept as
// its own copy rather than imported, since lint_test.go's version is
// unexported and this package must not depend on internal/live/lint for a
// dozen lines of test scaffolding.
func loadConfigDir(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	rootMod, diags := parser.LoadConfigDir(dir, configs.RootModuleCallForTesting())
	if diags.HasErrors() {
		t.Fatalf("failed to load %s: %s", dir, diags.Error())
	}

	walker := configs.ModuleWalkerFunc(func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
		sourceAddr, ok := req.SourceAddr.(addrs.ModuleSourceLocal)
		if !ok {
			return nil, nil, hcl.Diagnostics{{
				Severity: hcl.DiagError,
				Summary:  "Unsupported module source in test fixture",
				Detail:   "Only local module sources are supported by these tests.",
				Subject:  req.SourceAddrRange.Ptr(),
			}}
		}
		childDir := filepath.Join(req.Parent.Module.SourceDir, string(sourceAddr))
		mod, diags := parser.LoadConfigDir(childDir, req.Call)
		return mod, nil, diags
	})

	cfg, diags := configs.BuildConfig(t.Context(), rootMod, walker)
	if diags.HasErrors() {
		t.Fatalf("failed to build config from %s: %s", dir, diags.Error())
	}
	return cfg
}

// childConfig finds the [configs.Config] node for a dotted path of module
// call names, e.g. "mid/leaf" for module.mid.module.leaf, the same way
// [configs.Config.Children] is keyed at every level.
func childConfig(t *testing.T, root *configs.Config, names ...string) *configs.Config {
	t.Helper()

	cur := root
	for _, name := range names {
		child, ok := cur.Children[name]
		if !ok {
			t.Fatalf("no child module %q under %s", name, cur.Path)
		}
		cur = child
	}
	return cur
}

// resourceIn finds a managed resource block by type.name inside a module's
// own configuration, the same lookup providerConfigAddr's caller in
// internal/command/live_plan.go does via configs.Module.ManagedResources.
func resourceIn(t *testing.T, cfg *configs.Config, typeName string) *configs.Resource {
	t.Helper()

	rc, ok := cfg.Module.ManagedResources[typeName]
	if !ok {
		t.Fatalf("no managed resource %q in module %s", typeName, cfg.Path)
	}
	return rc
}

func awsProvider() addrs.Provider {
	return addrs.NewDefaultProvider("aws")
}

// sameAddr compares two [addrs.AbsProviderConfig] values field by field:
// addrs.Module is a slice, so the struct itself is not comparable with ==.
func sameAddr(a, b addrs.AbsProviderConfig) bool {
	return a.Provider == b.Provider && a.Alias == b.Alias && a.Module.String() == b.Module.String()
}

// TestResolveAliasedParentAdmitsTheDefaultMapping is the corpus's actual
// shape (live/corpus-refusals.json's "module-providers" entry: 110 of 110
// sites are this one) alongside its already-admitted twin in the same
// configuration - the "mixed" case, one estate carrying both an aliased and
// a default mapping the way live/e2e/limits/module-providers/main.tf does.
func TestResolveAliasedParentAdmitsTheDefaultMapping(t *testing.T) {
	root := loadConfigDir(t, "testdata/aliased-parent")

	east := childConfig(t, root, "east")
	got := ResolveResource(east, resourceIn(t, east, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: "useast1"}
	if !sameAddr(got, want) {
		t.Errorf("east module: got %s, want %s (today's behavior resolves this to the root's DEFAULT us-west-2 config instead, which is exactly the wrong-account failure RuleModuleProviders exists to refuse)", got, want)
	}
}

// TestResolveDefaultMappingIsUnchanged is the admitted twin in the same
// fixture: an explicit `providers = { aws = aws }` names exactly what live
// mode already does, and Resolve must leave it alone. This is the "did any
// warning become silence" check pointed the other way: nothing here should
// move at all.
func TestResolveDefaultMappingIsUnchanged(t *testing.T) {
	root := loadConfigDir(t, "testdata/aliased-parent")

	west := childConfig(t, root, "west")
	got := ResolveResource(west, resourceIn(t, west, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: ""}
	if !sameAddr(got, want) {
		t.Errorf("west module: got %s, want %s", got, want)
	}
}

// TestResolveConfigurationAliasesShape is the rule's other registered shape
// - the alias on the CHILD side, matched by a root block of the same name -
// the admitted form of the fixture at
// live/e2e/limits/module-providers/aliased/main.tf.
func TestResolveConfigurationAliasesShape(t *testing.T) {
	root := loadConfigDir(t, "testdata/config-alias")

	aliased := childConfig(t, root, "aliased")
	got := ResolveResource(aliased, resourceIn(t, aliased, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: "primary"}
	if !sameAddr(got, want) {
		t.Errorf("aliased module: got %s, want %s", got, want)
	}
}

// TestResolveNestedPassthroughCarriesTheAliasThroughAnUnnamedBoundary is a
// two-level nesting depth where only the OUTER boundary's mapping names the
// alias; the inner boundary re-passes it with a plain `providers = { aws =
// aws }` that, read on its own, looks exactly like the always-admitted
// default case. The walk has to carry the alias through a boundary whose
// own text never mentions it.
func TestResolveNestedPassthroughCarriesTheAliasThroughAnUnnamedBoundary(t *testing.T) {
	root := loadConfigDir(t, "testdata/nested-passthrough")

	leaf := childConfig(t, root, "mid", "leaf")
	got := ResolveResource(leaf, resourceIn(t, leaf, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: "east"}
	if !sameAddr(got, want) {
		t.Errorf("leaf module: got %s, want %s", got, want)
	}
}

// TestResolveNestedDefaultInheritanceIsUnchanged is two levels of nesting
// with no providers argument at either boundary - the overwhelmingly common
// shape, and the one that must resolve exactly where it already does today.
func TestResolveNestedDefaultInheritanceIsUnchanged(t *testing.T) {
	root := loadConfigDir(t, "testdata/nested-default")

	leaf := childConfig(t, root, "mid", "leaf")
	got := ResolveResource(leaf, resourceIn(t, leaf, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: ""}
	if !sameAddr(got, want) {
		t.Errorf("leaf module: got %s, want %s", got, want)
	}
}

// TestResolveDeadEndAliasStopsAtTheChildRatherThanGuessing is an aliased
// reference with no proxy anywhere in the tree: the call passes nothing, so
// stock OpenTofu's own Inherited() never climbs past it either. Resolve
// must anchor the address using the child's own scope rather than silently
// walking past the missing boundary on the assumption that the name still
// means the same thing higher up.
//
// The fixture's child module declares no `configuration_aliases`: adding
// one makes OpenTofu's own config loader require an explicit providers
// entry for it at the call site, which turned this into a config that
// fails to build at all rather than one that reaches Resolve with the
// alias genuinely unpassed (confirmed by hand while writing this test).
// This ad hoc `provider = aws.primary` reference, with no formal
// requirement anywhere, is the shape that stays loadable with nothing
// passed.
func TestResolveDeadEndAliasStopsAtTheChildRatherThanGuessing(t *testing.T) {
	root := loadConfigDir(t, "testdata/dead-end-alias")

	child := childConfig(t, root, "child")
	got := ResolveResource(child, resourceIn(t, child, "aws_s3_bucket.data"))
	// The alias survives verbatim, anchored at root: no dead end this
	// fixture can reach carries its own local provider block (GitHub issue
	// #201 gave Resolve's return value a second possible anchor, a
	// non-root module with a content-bearing block of its own - see
	// TestResolveLocalProviderBlockTerminatesAtItsOwnModule - but that
	// never applies to an alias with no matching entry anywhere in the
	// tree, which is this case). No root block named "primary" exists in
	// this fixture, so a
	// caller that resolves the returned address against the root module's
	// blocks - every caller in the live path today - gets the same
	// "provider configuration is not declared" diagnostic issue #123
	// already raises for a root-level unresolvable alias, never a silent
	// fall-through to the environment.
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: "primary"}
	if !sameAddr(got, want) {
		t.Errorf("child module: got %s, want %s", got, want)
	}
}

// TestResolveLocalProviderBlockTerminatesAtItsOwnModule is GitHub issue
// #201's actual corpus shape: a child module declares its own,
// content-bearing provider block (a live one reads a var, mirroring
// simpleinfra/terraform/shared/modules/gha-iam-user/main.tf:10's
// `provider "github" { owner = var.org }`), reached by a call with none of
// count, for_each, enabled or depends_on - the shape
// internal/live/lint.checkModuleProviderBlocks now admits. Before this
// change, Resolve had no path that terminated at a non-root module's own
// block at all: every return climbed to addrs.RootModule regardless, which
// would have pointed this resource at root's us-west-2 configuration
// instead of the child's own us-east-1 one - a live resource read, written
// and swept against the wrong account with nothing said about it, the
// exact failure mode issue #104's own package doc comment describes for an
// unhonoured providers mapping.
func TestResolveLocalProviderBlockTerminatesAtItsOwnModule(t *testing.T) {
	root := loadConfigDir(t, "testdata/local-provider-block")

	child := childConfig(t, root, "compute")
	got := ResolveResource(child, resourceIn(t, child, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.Module{"compute"}, Provider: awsProvider(), Alias: ""}
	if !sameAddr(got, want) {
		t.Errorf("compute module: got %s, want %s (module.compute's own provider block, not root's)", got, want)
	}
}

// TestResolveLocalProviderBlockAliasedTerminatesAtItsOwnModule is the same
// shape with an alias on both the block and the resource's own provider
// reference - proving the terminate-locally case carries the alias through
// rather than only handling the always-unaliased default name.
func TestResolveLocalProviderBlockAliasedTerminatesAtItsOwnModule(t *testing.T) {
	root := loadConfigDir(t, "testdata/local-provider-block-aliased")

	child := childConfig(t, root, "compute")
	got := ResolveResource(child, resourceIn(t, child, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.Module{"compute"}, Provider: awsProvider(), Alias: "east"}
	if !sameAddr(got, want) {
		t.Errorf("compute module: got %s, want %s", got, want)
	}
}

// TestResolveEmptyLocalProviderBlockFallsThrough is the other half of issue
// #201's local-block work: an EMPTY block (`provider "aws" {}`) is
// internal/configs/provider_validation.go's own "could be a proxy
// configuration" shape, legal even under a call using count precisely
// because it carries no settings of its own. A naive "any matching local
// declaration terminates the walk" rule would resolve this to the child's
// own (empty) block instead of root's real one - silently trading a
// configured provider for an unconfigured one. Resolve must fall through it
// exactly as if the child had declared no block at all.
func TestResolveEmptyLocalProviderBlockFallsThrough(t *testing.T) {
	root := loadConfigDir(t, "testdata/empty-proxy-block")

	child := childConfig(t, root, "compute")
	got := ResolveResource(child, resourceIn(t, child, "aws_s3_bucket.data"))
	want := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: awsProvider(), Alias: ""}
	if !sameAddr(got, want) {
		t.Errorf("compute module: got %s, want %s (root's real configuration, not the child's empty proxy block)", got, want)
	}
}
