// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// checkModuleProviderMapping rejects a module call whose providers mapping
// names a child-side alias the root module does not declare a matching
// provider configuration for.
//
// GitHub issue #104 opened this as a refusal of BOTH shapes a providers
// mapping's alias can appear in - the parent side (`providers = { aws =
// aws.useast1 }`) and the child side (`providers = { aws.primary = aws }`,
// the `configuration_aliases` shape, matched inside the module by `provider
// = aws.primary`) - because nothing in the live path read the mapping at
// all: the provider cache keyed on the provider and its alias alone, and
// providerConfigValue read the root module's provider blocks
// unconditionally, so both shapes silently fell through to the root's
// default (unaliased) configuration or the environment.
//
// GitHub issue #188 closed that gap for the parent-side shape:
// internal/live/providerscope.Resolve now walks every ancestor module
// call's providers mapping - the same walk stock OpenTofu's own
// [transform_provider.go]'s addProxyProviders and
// [addrs.AbsProviderConfig.Inherited] perform, reimplemented as a pure
// function rather than a graph - and internal/command/live_plan.go,
// internal/live/discovery's inScope and internal/live/projection/build.go
// all resolve a resource's provider configuration through it. A module
// called with an aliased parent mapping is planned, applied and swept
// against the account or region the mapping actually names, exactly as
// stock OpenTofu resolves it; refusing it would refuse a configuration that
// works. Every site the corpus had ever produced for this rule (110 of 110
// sites, live/corpus-refusals.json's "module-providers" entry as of #188's
// scoping) was this shape, and if the mapping names an alias the root
// genuinely does not declare, GitHub issue #123's own diagnostic catches
// that at plan time instead of here.
//
// The child-side shape stays refused, and unchanged from the first version
// of this rule: it is wrong unless the root happens to declare a
// configuration under the CHILD side's own local alias name, which is what
// [statelessProviders.providerConfigValue] (internal/command/live_plan.go)
// looks up today for a resource whose own local reference already carries
// that alias, independent of any mapping walk.
//
// KNOWN GAP, found and left unresolved by this same change's corpus
// re-measurement (`just corpus` after #188's four-site wiring landed):
// that name-based check is not what a providers mapping actually promises.
// The mapping's CHILD side name is purely internal to the module that
// declares configuration_aliases, and the mapping is exactly what is
// allowed to rename it into something else entirely on the way up - see
// providerscope.Resolve's own doc comment for the two mechanisms this
// mirrors. .corpus/cool-assessment's real module calls map `aws.users =
// aws.provisionassessment`; the root declares `provisionassessment`, not
// `users`, so this rule still refuses those 2 sites even though
// providerscope.Resolve(the call's own module, {aws, "users"}) walks the
// same mapping and lands on `provisionassessment` correctly - the mapping
// resolves, and the plan would succeed. A fix built and hand-verified while
// writing this change replaces the name lookup with exactly that walk
// (see this function's git history on branch wall/modprov for the
// discarded version), but it also flips the admitted/refused verdict for
// live/e2e/limits/module-providers/aliased/main.tf's `providers = {
// aws.primary = aws }` fixture: the walk correctly resolves that mapping to
// the root's plain default aws configuration (a completely valid,
// resolvable stock-OpenTofu construct - the fixture's own "worse of the two
// failures" framing was written against the OLD, pre-#188 resolution
// algorithm, not against what a providers mapping actually promises), which
// leaves that whole hand fixture with zero refusable constructs left in it.
// Landing the general fix would require rewriting that fixture to a
// genuinely unresolvable mapping (e.g. `aws.primary = aws.ghost`, naming an
// alias the root never declares), and live/ is out of this change's
// surface. Left as scoped follow-up rather than forced through.
//
// # What is not refused
//
// An aliased parent (#188 now resolves it), a mapping to an unaliased
// configuration - `providers = { aws = aws }`, or `{ myaws = aws }` where
// the child's own local name differs but the parent's config is the
// default one - and a child alias the root does declare by that same name.
// Refusing any of these would refuse a configuration that works, which is
// the cost this project's own goal weighs heaviest.
//
// This is distinct from [checkModuleProviderBlocks], which refuses provider
// *blocks* declared inside a child module (#70's ruling). Both come from the
// same gap and neither subsumes the other: a module can declare no provider
// block of its own and still be called with a mapping.
func checkModuleProviderMapping(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		call := mod.ModuleCalls[name]
		if call == nil {
			continue
		}
		for _, passed := range call.Providers {
			if passed.InParent == nil {
				continue
			}

			parent := passed.InParent.Name
			if passed.InParent.Alias != "" {
				parent += "." + passed.InParent.Alias
			}
			child := passed.InParent.Name
			if passed.InChild != nil {
				child = passed.InChild.Name
				if passed.InChild.Alias != "" {
					child += "." + passed.InChild.Alias
				}
			}

			// #188 retired the aliased-parent arm: providerscope.Resolve
			// now walks this mapping the same way live_plan.go, discovery's
			// inScope and projection/build.go all resolve a resource's
			// provider configuration, so an aliased parent is planned,
			// applied and swept correctly rather than silently falling
			// through to the root's default. Only the aliased-child arm
			// remains: it is wrong unless the root happens to declare that
			// same address, which is what the module's resources will
			// actually resolve against - see this function's doc comment
			// for why that shape was never blocked on #188's fix, and for
			// the KNOWN GAP in how it is checked.
			aliasedChild := passed.InChild != nil && passed.InChild.Alias != ""
			if !(aliasedChild && mod.ProviderConfigs[child] == nil) {
				continue
			}

			subject := passed.InParent.NameRange
			if passed.InParent.AliasRange != nil {
				subject = *passed.InParent.AliasRange
			}

			*issues = append(*issues, Issue{
				Rule:      RuleModuleProviders,
				Construct: fmt.Sprintf("module %q, providers = { %s = %s }", name, child, parent),
				Module:    path,
				Detail: fmt.Sprintf(
					"module %q maps %s to %s, and the root configuration declares no provider configuration "+
						"named %s. %s That is not a difference a plan shows you - the resources would simply be "+
						"read, written and swept through whatever the environment supplies, rather than through "+
						"%s's settings. Declare %s at root, or change the mapping to name a configuration the "+
						"root does declare.",
					name, child, parent, parent, moduleProviderConsequence(child, parent), parent, parent,
				),
				Subject: subject,
			})
		}
	}
}

// moduleProviderConsequence names what actually happens to the module's
// resources when the root declares no provider configuration matching the
// mapping's child-side alias: the address they resolve against does not
// exist at root, so the provider configures from the environment with
// nothing from the configuration at all. This is the only shape this rule
// still refuses as of GitHub issue #188 - see [checkModuleProviderMapping]'s
// doc comment for the aliased-parent shape #188 retired this refusal for.
func moduleProviderConsequence(child, parent string) string {
	return fmt.Sprintf(
		"This module's resources name %s, which the root configuration does not declare, so live mode configures that provider from the environment alone - none of %s's settings reach it.",
		child, parent)
}
