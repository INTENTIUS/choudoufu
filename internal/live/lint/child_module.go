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

// checkChildModules rejects the two module-call shapes whose expansion is
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
//   - for_each on a module block is refused today because nothing downstream
//     of lint walks into a module's instances yet (#59, phase 3, "59c").
//     Its keys do not shift under insertion or removal the way count's
//     positions do, which is what makes it worth admitting - it is not
//     admitted yet.
//
// Before 59b landed, identity resolution, discovery, marker stamping, the
// projection and a rename all stopped at the root, and each of them used to
// say so in its own words when handed a configuration with children. Those
// five refusals were the same refusal reached five ways, and none of them
// could point at the module block that caused it, because by the time they
// ran the configuration was a tree rather than a page. Lint runs before all
// five, in both commands, and it already walks the whole tree - which is
// what lets this one name the module call and its source range - so a
// count- or for_each-expanded module is still stopped here, at the one
// place that can say why in the operator's own configuration.
//
// The walk is per-module rather than "does the root have children", so a
// configuration three deep reports every count- or for_each-expanded call in
// one pass instead of one per run of the fix-and-rerun loop.
func checkChildModules(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		call := mod.ModuleCalls[name]
		detail, refused := childModuleDetail(call)
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

// childModuleDetail is the refusal for a count- or for_each-expanded module
// call, and reports false for a static one: count and for_each are mutually
// exclusive on a module block - HCL itself refuses a call that sets both -
// so this is a strict three-way choice (count, for_each, or admitted), not
// a priority order.
func childModuleDetail(call *configs.ModuleCall) (string, bool) {
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
		return "for_each on a module block is planned (issue #59, phase 3, after the " +
			"static-module traversal lands): a keyed instance does not renumber the way a " +
			"counted one does, which is what makes it worth admitting. It is not admitted " +
			"yet - identity resolution, discovery, marker stamping and the projection do " +
			"not walk into a module's instances today. Move the module's resources into " +
			"the root module, or split the module into an estate of its own with its own " +
			"live block, until keyed module expansion ships", true
	default:
		return "", false
	}
}
