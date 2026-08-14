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

// checkModuleProviderBlocks refuses every provider block declared inside a
// child module.
//
// GitHub issue #70's ruling. The interim state was a once-per-run tfdiags
// warning (CheckModuleProviders, retired by the change that added this rule):
// live mode reads provider configurations from the root module only, so an
// in-module provider block is never consulted and the module's resources are
// silently served by the root configuration's own provider config instead -
// possibly a different account, region, or set of credentials than the block
// asked for. Warning rather than refusing was deliberate while the design
// question (support per-module resolution, or refuse the block) stayed open.
//
// The ruling closed it with a measurement: across the ten most-installed
// terraform-aws-modules repositories - 740 module-source .tf files - not one
// declares a provider block inside module source, and upstream's own
// documentation calls in-module provider configurations legacy practice
// (a module declaring one cannot be used with count, for_each or depends_on,
// and removing the module call orphans its resources). Rare, and already
// discouraged upstream, so this fork sides with upstream and refuses. The
// full per-module resolution design stays available if a real estate ever
// needs it; the corpus will show the day that stops being rare.
//
// Root module only in the other direction: the root's own provider blocks are
// exactly what live mode does consult, so [checkConfig]'s walk reaches this
// function for every module node and it reports nothing at root. This is
// distinct from [checkModuleProviderMapping] (#104), which polices the
// providers mapping on the module *call*: a module can declare no provider
// block of its own and still be called with a mapping, and vice versa.
func checkModuleProviderBlocks(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if path.IsRoot() {
		return
	}

	names := make([]string, 0, len(mod.ProviderConfigs))
	for name := range mod.ProviderConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		pc := mod.ProviderConfigs[name]
		block := fmt.Sprintf("provider %q", pc.Name)
		if pc.Alias != "" {
			block = fmt.Sprintf("provider %q, alias %q", pc.Name, pc.Alias)
		}
		*issues = append(*issues, Issue{
			Rule:      RuleModuleProviderBlock,
			Construct: block,
			Module:    path,
			Detail: fmt.Sprintf(
				"%s declares %s. Live mode reads provider configurations from the root module only, "+
					"so this block would never be consulted: the module's resources would be read, written "+
					"and swept against whatever the root configuration's own provider config names, which "+
					"may be a different account or region than this block asks for, with nothing said about "+
					"it. Declare the provider configuration at root and let the module receive it "+
					"implicitly. A providers mapping on the module call stays subject to the "+
					"module-providers rule's admitted shapes: an unaliased mapping such as "+
					"providers = { aws = aws } is admitted, an aliased one is refused",
				path.String(), block,
			),
			Subject: pc.DeclRange,
		})
	}
}
