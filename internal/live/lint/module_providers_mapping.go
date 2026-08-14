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
// sends the module's resources to a provider configuration this mode will
// not use.
//
// GitHub issue #104. The mapping is never read anywhere in the live path:
// internal/command/live_plan.go's provider cache keys on the provider and
// its alias alone, omitting the module, and providerConfigValue reads the
// root module's provider blocks unconditionally. So a module called with
//
//	providers = { aws = aws.useast1 }
//
// has its resources planned and applied against the root's default aws
// configuration instead. A multi-account or multi-region estate built the
// standard way reads and writes in the wrong account, with no diagnostic:
// discovery lists in the wrong place, the estate reads as missing rather
// than unreachable, and under #67's undeclared_untagged = "delete" quadrant
// the blast radius is considerably worse than a bad plan.
//
// Refusing is the honest half of #104's acceptance. Honouring the mapping is
// the other, and it is a larger change than a lint rule: the provider cache,
// projection and discovery's scoping all key on an address that does not
// carry the module today. Until that lands, silence is the one outcome the
// issue rules out.
//
// # Two shapes, one refusal
//
// The parent side carrying an alias is the obvious one: the author named a
// configuration, and the run uses a different one.
//
// The child side carrying an alias is the same failure by another route, and
// the first version of this rule admitted it. `providers = { aws.primary =
// aws }` is the standard `configuration_aliases` shape, and a resource
// inside the module writes `provider = aws.primary`. Live mode resolves that
// address against the ROOT module's provider blocks, where no `aws.primary`
// exists - so providerConfigValue falls through to an empty body and the
// provider is configured entirely from the environment, rather than from the
// root `aws` block the mapping named. Found by an adversarial audit, which
// reproduced the key computation rather than reading it.
//
// So the rule fires when either side names a provider configuration this run
// will not use: an aliased parent, or a child alias the root does not
// declare. A root that does declare `provider "aws" { alias = "primary" }`
// resolves, and is admitted.
//
// # What is not refused
//
// A mapping to an unaliased configuration - `providers = { aws = aws }`, or
// `{ myaws = aws }` where the child's own local name differs but the
// parent's config is the default one - describes exactly what live mode
// already does. Refusing it would refuse a configuration that works, which
// is the cost this project's own goal weighs heaviest.
//
// This is distinct from [CheckModuleProviders], which warns about provider
// *blocks* declared inside a child module (#70). Both come from the same
// gap and neither subsumes the other: a module can declare no provider
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

			// An aliased parent is always wrong. An aliased child is wrong
			// unless the root happens to declare that same address, which is
			// what the module's resources will actually resolve against.
			aliasedChild := passed.InChild != nil && passed.InChild.Alias != ""
			if passed.InParent.Alias == "" && !(aliasedChild && mod.ProviderConfigs[child] == nil) {
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
					"module %q maps %s to %s, and live mode does not read a module call's providers mapping. "+
						"%s That is not a difference a plan shows you - the resources would simply be read, "+
						"written and swept somewhere other than where you asked. Configure the whole estate "+
						"against one provider configuration, or split it into one configuration per account or "+
						"region and run them separately.",
					name, child, parent, moduleProviderConsequence(passed, child, parent),
				),
				Subject: subject,
			})
		}
	}
}

// moduleProviderConsequence names what actually happens to the module's
// resources, which differs between the two shapes this rule refuses.
//
// An aliased parent sends them to the root's default configuration. An
// aliased child sends them nowhere: the address they resolve against does
// not exist at root, so the provider is configured from the environment with
// nothing from the configuration at all - which is the worse of the two, and
// the one the first version of this rule admitted.
func moduleProviderConsequence(passed configs.PassedProviderConfig, child, parent string) string {
	if passed.InParent.Alias != "" {
		return fmt.Sprintf(
			"This module's resources would be planned and applied against the root configuration's own default %s provider instead of %s, which may be a different account or region.",
			passed.InParent.Name, parent)
	}
	return fmt.Sprintf(
		"This module's resources name %s, which the root configuration does not declare, so live mode configures that provider from the environment alone - none of %s's settings reach it.",
		child, parent)
}
