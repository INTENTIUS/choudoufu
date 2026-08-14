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
// # What is not refused
//
// A mapping to an unaliased configuration - `providers = { aws = aws }`, or
// `{ aws = aws.this }` where the child's own name differs but the parent's
// config is the default one - describes exactly what live mode already does.
// Refusing it would refuse a configuration that works, which is the cost
// this project's own goal weighs heaviest.
//
// So the rule fires on the parent side carrying an alias. That is the case
// where what the author asked for and what the run does are different
// things.
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
			if passed.InParent == nil || passed.InParent.Alias == "" {
				continue
			}

			parent := passed.InParent.Name + "." + passed.InParent.Alias
			child := passed.InParent.Name
			if passed.InChild != nil {
				child = passed.InChild.Name
				if passed.InChild.Alias != "" {
					child += "." + passed.InChild.Alias
				}
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
					"module %q maps %s to %s, and live mode does not read a module call's providers mapping: "+
						"this module's resources would be planned and applied against the root configuration's own "+
						"default %s provider instead, which may be a different account or region. That is not a "+
						"difference a plan shows you - the resources would simply be read, written and swept "+
						"somewhere other than where you asked. Configure the whole estate against one provider "+
						"configuration, or split it into one configuration per account or region and run them "+
						"separately.",
					name, child, parent, passed.InParent.Name,
				),
				Subject: subject,
			})
		}
	}
}
