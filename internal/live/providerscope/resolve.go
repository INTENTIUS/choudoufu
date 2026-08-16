// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package providerscope resolves a resource's true root-level provider
// configuration address by walking every ancestor module call's `providers
// = { ... }` mapping, the way stock OpenTofu's graph does across two
// separate transformers (see [Resolve]'s doc comment) - but as a pure
// function over the static configuration tree, with no graph involved.
//
// GitHub issue #104. Nothing in the live path reads a module call's
// providers mapping today: internal/command/live_plan.go's provider cache
// keys on (provider, alias) alone and reads the ROOT module's provider
// blocks unconditionally, so a module called with a remapped provider has
// its resources planned and applied against the wrong account or region,
// with no diagnostic. internal/live/lint's RuleModuleProviders refuses that
// shape outright rather than mis-plan it. This package is the resolution
// core that would let the mapping be honoured instead of refused; wiring it
// into the provider cache, discovery's scoping and projection is separate,
// unstarted work - see the package's own issue thread for the plan.
package providerscope

import (
	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// Resolve computes the absolute provider configuration address that a
// reference with local address local, made inside module cfg, ultimately
// resolves to once every ancestor module call's providers mapping between
// cfg and the root is honoured.
//
// cfg is the [configs.Config] node for the module the reference is made in
// - the resource's own module, for a resource block, found by walking the
// static tree from the root's [configs.Config] the same way
// [configs.Config.Children] is keyed (see, for example,
// internal/live/identity/modulepath.go's ConfigForModule, which does this
// for a dynamic addrs.ModuleInstance; the static addrs.Module case here is
// simpler because it needs no instance-key handling). local is ordinarily
// the resource's own [*configs.Resource.ProviderConfigAddr], the local name
// and alias a "provider" argument names, or the type-implied default when
// the resource has none.
//
// This mirrors two separate mechanisms stock OpenTofu's graph applies in
// sequence:
//
//   - internal/tofu/transform_provider.go's
//     ProviderConfigTransformer.addProxyProviders reads exactly one module
//     boundary's providers mapping and builds a proxy graph node when an
//     entry's InChild address matches. Chained across nested modules, the
//     graph ends up with one proxy per boundary, each pointing at the node
//     one level up; ProviderTransformer's target-unwrapping loop walks that
//     chain to the concrete node when a resource asks for it.
//   - addrs.AbsProviderConfig.Inherited(), which ProviderTransformer's own
//     resolution loop falls back to when a boundary's mapping has no
//     matching entry: default, unaliased same-provider-type inheritance,
//     walking module-by-module all the way to the root, where
//     MissingProviderTransformer guarantees a default node exists.
//     Inherited() refuses outright once the reference carries an alias -
//     an aliased reference only ever crosses a boundary that names it
//     explicitly.
//
// Both are graph-shaped: nodes, edges, and state (t.providers, t.proxiable)
// built by a full-tree walk before any single resource's address is asked
// for. That machinery earns its cost for stock OpenTofu's own planning
// graph, which needs every provider as a real graph node with edges for
// ordering. The live path has no such graph for provider resolution - it
// asks a direct question ("what real, configured provider does this
// resource use?") outside of any graph, so this function answers it
// directly: a loop over configs.Config.Parent and each ancestor's
// configs.ModuleCall.Providers, carrying forward the (name, alias) pair the
// same way addProxyProviders' pair.InChild/pair.InParent do, and falling
// back to Inherited()'s FQN-based default inheritance - by provider type,
// not local name, exactly as Inherited() does - when a boundary's mapping
// says nothing about this reference. Reimplementing addProxyProviders by
// copying its graph-node-building code would still need the same walk this
// function already is; there is nothing there to reuse that isn't the walk
// itself.
//
// An unaliased reference (local.Alias == "") that no ancestor's providers
// mapping intercepts inherits the root's default configuration for the same
// provider type. That is stock OpenTofu's behavior for the overwhelming
// majority of module calls, which name no providers argument at all - and
// it is already what the live path's provider cache and providerConfigValue
// do today, so this is the path that does not change.
//
// An aliased reference only crosses a module boundary where that specific
// (name, alias) pair appears on the child side of that boundary's providers
// mapping (the "configuration_aliases" shape, or an explicit re-passing of
// an alias down another level). The first boundary with no matching entry
// ends the walk there: the returned address keeps the alias, anchored at
// the root using the dead-end module's own local-name resolution. A caller
// that reads the root module's provider blocks by (Provider, Alias) alone -
// which is every provider-configuration lookup in the live path today -
// then either finds a root block with that alias (the case
// [live/e2e/limits/module-providers/aliased/main.tf] would need to admit:
// a root declaring provider "aws" { alias = "primary" } as the mapping
// says) or raises the same "provider configuration is not declared"
// diagnostic [statelessProviders.providerConfigValue] in
// internal/command/live_plan.go already raises for an unresolvable
// root-level alias (GitHub issue #123) - never a silent fall-through to the
// environment.
func Resolve(cfg *configs.Config, local addrs.LocalProviderConfig) addrs.AbsProviderConfig {
	cur := cfg
	name := local.LocalName
	alias := local.Alias

	for cur != nil && cur.Module != nil && !cur.Path.IsRoot() {
		// A module that declares its own local `provider` block for this
		// (name, alias) pair, WITH REAL CONTENT, is served by that block
		// directly - never by climbing further toward root. GitHub issue
		// #201: [checkModuleProviderBlocks] in internal/live/lint admits a
		// non-root local block only when no call in the chain down to it
		// used count, for_each, enabled or depends_on (mirroring
		// internal/configs/provider_validation.go's own condition), so this
		// is the only shape a module tree can reach here with a
		// content-bearing non-root block at all. A resource in that module
		// resolves to it directly, the same way stock OpenTofu's
		// ProviderConfigTransformer wires a resource straight to its own
		// module's provider node when one exists, without ever considering
		// a parent's mapping.
		//
		// An EMPTY block (`provider "aws" {}`, no attributes, no version
		// constraint) does not terminate the walk: it is
		// provider_validation.go's own "could be a proxy configuration"
		// shape (emptyConfigs, never added to configured - see
		// [configuredProviderBlock]'s doc comment), legal even under a
		// blocked call chain precisely because it carries no settings of
		// its own. Resolving it as if it were authoritative would silently
		// substitute an empty configuration for whatever the call chain
		// actually supplies - inherited from a parent's mapping or from
		// root - so it falls through to the ordinary walk below instead,
		// unchanged.
		if pc, ok := cur.Module.GetProviderConfig(name, alias); ok && configuredProviderBlock(pc) {
			return addrs.AbsProviderConfig{
				Module:   cur.Path,
				Provider: cur.Module.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: name}),
				Alias:    alias,
			}
		}

		parent := cur.Parent
		if parent == nil || parent.Module == nil {
			break
		}

		_, callAddr := cur.Path.Call()
		call := parent.Module.ModuleCalls[callAddr.Name]
		if call == nil {
			// A static module tree with no call for the path that reached
			// it can't happen from BuildConfig's own output; break rather
			// than resolve further on a tree that doesn't match its own
			// structure.
			break
		}

		if passed, ok := passedFor(call.Providers, name, alias); ok {
			name = passed.InParent.Name
			alias = passed.InParent.Alias
			cur = parent
			continue
		}

		if alias != "" {
			// Inherited()'s "can't inherit if we have an alias": no proxy
			// named this (name, alias) pair at this boundary, and an
			// aliased reference never climbs past a boundary that doesn't
			// name it. Stop here; the anchor-at-root return below surfaces
			// the same diagnostic root-level resolution already does for
			// an alias with no matching provider block.
			break
		}

		// No entry for this call at all: default inheritance, by provider
		// type rather than local name (Inherited() only ever compares
		// Provider and Alias, never a local name - a local name is a
		// per-module label, and nothing requires it to match across a
		// module boundary even though it almost always does in practice).
		fqn := cur.Module.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: name})
		name = parent.Module.LocalNameForProvider(fqn)
		cur = parent
	}

	if cur == nil || cur.Module == nil {
		return addrs.AbsProviderConfig{Module: addrs.RootModule, Alias: alias}
	}

	return addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: cur.Module.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: name}),
		Alias:    alias,
	}
}

// ResolveResource is [Resolve] for the common case: a managed or data
// resource block, using the local provider address it names (or the
// type-implied default when it names none), in the module cfg represents.
func ResolveResource(cfg *configs.Config, rc *configs.Resource) addrs.AbsProviderConfig {
	return Resolve(cfg, rc.ProviderConfigAddr())
}

// configuredProviderBlock reports whether pc carries real configuration - at
// least one attribute or nested block, or a version constraint - rather than
// being an empty declaration such as `provider "aws" {}`. This is exactly
// internal/configs/provider_validation.go:340-351's own configured/
// emptyConfigs split, duplicated here (and in
// internal/live/lint/module_provider_block.go, which has the identical copy
// and the identical reason for it) because that function's sets are
// unexported and local to one validation pass.
func configuredProviderBlock(pc *configs.Provider) bool {
	_, contentDiags := pc.Config.Content(&hcl.BodySchema{})
	return contentDiags.HasErrors() || pc.Version.Required != nil
}

// passedFor finds the entry in a module call's providers mapping whose
// child-side address is exactly (name, alias), the same match
// internal/tofu/transform_provider.go's addProxyProviders performs when
// deciding whether a child module's provider node should become a proxy.
func passedFor(providers []configs.PassedProviderConfig, name, alias string) (configs.PassedProviderConfig, bool) {
	for _, p := range providers {
		if p.InChild == nil || p.InParent == nil {
			continue
		}
		if p.InChild.Name == name && p.InChild.Alias == alias {
			return p, true
		}
	}
	return configs.PassedProviderConfig{}, false
}
