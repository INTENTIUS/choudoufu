// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// checkChildModules rejects the module-call shapes whose expansion is
// incompatible with address-as-identity, wherever in the tree they are
// written. A static module call - neither count nor for_each - is not one
// of them and is not reported at all: 59b gave the five walkers a real
// traversal into a static module tree, so a marker binds to
// "module.a.aws_x.y" exactly as soundly as it binds to a root address, and
// there is nothing left to refuse.
//
//   - count on a module block is refused for keeps. Expansion by count
//     renumbers every instance below it positionally, and a tofu-address
//     marker records an address, not a position - a renumbering that leaves
//     every marker pointing at the wrong instance is not a gap this mode
//     intends to close.
//   - for_each on a module block is admitted (59c, issue #59 phase 3) when
//     its keys are statically evaluable - a literal collection, or one
//     built from variables, locals, path and terraform values, exactly the
//     scope a resource's own for_each is evaluated in - because a keyed
//     instance's key does not shift under insertion or removal the way
//     count's position does, which is what makes it worth admitting at all.
//     A for_each whose keys this pass cannot enumerate is still refused:
//     the five walkers need every instance key before anything is read
//     from the cloud, the same reason identity resolution refuses a
//     resource's own non-static for_each (resolve.go's forEachExpansion).
//     Whether each individual key survives the trip through a
//     tofu-address marker is a separate question, asked by
//     [checkForEachKeys] with the same rule a resource's own for_each key
//     is held to (live/MARKERS.md, RuleForEachKey) - a module whose keys
//     are enumerable but contain a bad character is admitted here and
//     reported there instead, exactly as a resource is.
//
// Before 59b landed, identity resolution, discovery, marker stamping, the
// projection and a rename all stopped at the root, and each of them used to
// say so in its own words when handed a configuration with children. Those
// five refusals were the same refusal reached five ways, and none of them
// could point at the module block that caused it, because by the time they
// ran the configuration was a tree rather than a page. Lint runs before all
// five, in both commands, and it already walks the whole tree - which is
// what lets this one name the module call and its source range - so a
// count-expanded or non-statically-keyed module is still stopped here, at
// the one place that can say why in the operator's own configuration.
//
// The walk is per-module rather than "does the root have children", so a
// configuration three deep reports every refused call in one pass instead
// of one per run of the fix-and-rerun loop.
func checkChildModules(ctx context.Context, mod *configs.Module, path addrs.Module, issues *[]Issue) {
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		call := mod.ModuleCalls[name]
		detail, refused := childModuleDetail(ctx, mod, call)
		if !refused {
			continue
		}
		*issues = append(*issues, Issue{
			Rule:      RuleChildModule,
			Construct: fmt.Sprintf("module %q", name),
			Module:    path,
			Detail:    detail,
			Subject:   call.DeclRange,
		})
	}
}

// childModuleDetail is the refusal for a count-expanded or non-statically-
// keyed for_each module call, and reports false for a static call or a
// for_each call whose keys this pass can enumerate: count and for_each are
// mutually exclusive on a module block - HCL itself refuses a call that
// sets both - so this is a strict three-way choice (count, for_each, or
// admitted), not a priority order.
func childModuleDetail(ctx context.Context, mod *configs.Module, call *configs.ModuleCall) (string, bool) {
	switch {
	case call.Count != nil:
		return "count on a module block expands it positionally, renumbering every " +
			"resource address inside it on every insertion or removal above the changed " +
			"index. A tofu-address marker records an address, and a renumbering that moves " +
			"addresses out from under their markers is not a gap this mode intends to " +
			"close - count-expanded modules are refused permanently. Move the module's " +
			"resources into the root module, or split the module into an estate of its own " +
			"with its own live block", true
	case call.ForEach != nil:
		if _, diag := identity.ChildModuleKeys(ctx, mod, fmt.Sprintf("module %q", call.Name), call.ForEach); diag != nil {
			return fmt.Sprintf(
				"for_each on a module block is admitted only when its keys are statically "+
					"evaluable - a literal collection, or one built from variables, locals, "+
					"path and terraform values - because every one of them becomes part of an "+
					"address inside the module, and the five walkers need every instance key "+
					"before anything is read from the cloud. %s Move the module's resources "+
					"into the root module, or split the module into an estate of its own with "+
					"its own live block, until the for_each expression is statically evaluable",
				diag.Detail,
			), true
		}
		return "", false
	default:
		return "", false
	}
}
