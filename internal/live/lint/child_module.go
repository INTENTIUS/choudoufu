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

// checkChildModules rejects every module call, wherever in the tree it is
// written - but not for the same reason. #59 narrows this rule module call by
// module call rather than all at once: every module block is still refused
// today, but the three shapes a module call can take (static, keyed
// for_each, count) are refused for three different reasons, and only one of
// them is permanent.
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
//   - a static module call - neither count nor for_each - is refused today
//     because nothing downstream of lint walks into a child module at all
//     yet (#59, phase 2, "59b"). Nothing expands here, so there is no
//     ambiguity about which instance a marker belongs to, only a traversal
//     that has not been written.
//
// Stateless mode v0 is a root-module mode: identity resolution, discovery,
// marker stamping, the projection and a rename all stop at the root, and each
// of them used to say so in its own words when it was handed a configuration
// with children. Those five refusals were the same refusal reached five ways,
// and none of them could point at the module block that caused it, because by
// the time they ran the configuration was a tree rather than a page.
//
// Lint runs before all five, in both commands, and it already walks the whole
// tree - which is what lets this one name the module call and its source
// range. The five package-level guards are still there and still refuse, but
// as internal invariants now (one line each) rather than as the user's
// explanation: a package reached with a child-moduled configuration has been
// reached out of order, and that is a bug report, not something an operator
// can act on.
//
// The walk is per-module rather than "does the root have children", so a
// configuration three deep reports all three module calls in one pass instead
// of one per run of the fix-and-rerun loop.
func checkChildModules(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		call := mod.ModuleCalls[name]
		*issues = append(*issues, Issue{
			Rule:      RuleChildModule,
			Construct: fmt.Sprintf("module %q", name),
			Module:    path,
			Detail:    childModuleDetail(call),
			Subject:   call.DeclRange,
		})
	}
}

// childModuleDetail picks the one of three Details a module call is refused
// with, matching which of the three shapes checkChildModules documents the
// call as taking. count and for_each are mutually exclusive on a module
// block - HCL itself refuses a call that sets both - so this is a strict
// three-way choice, not a priority order.
func childModuleDetail(call *configs.ModuleCall) string {
	switch {
	case call.Count != nil:
		return "count on a module block expands it positionally, renumbering every " +
			"resource address inside it on every insertion or removal above the changed " +
			"index. A tofu-address marker records an address, and a renumbering that moves " +
			"addresses out from under their markers is not a gap this mode intends to " +
			"close - count-expanded modules are refused permanently. Move the module's " +
			"resources into the root module, or split the module into an estate of its own " +
			"with its own live block"
	case call.ForEach != nil:
		return "for_each on a module block is planned (issue #59, phase 3, after the " +
			"static-module traversal lands): a keyed instance does not renumber the way a " +
			"counted one does, which is what makes it worth admitting. It is not admitted " +
			"yet - identity resolution, discovery, marker stamping and the projection do " +
			"not walk into a module's instances today. Move the module's resources into " +
			"the root module, or split the module into an estate of its own with its own " +
			"live block, until keyed module expansion ships"
	default:
		return "static module calls are planned (issue #59, phase 2, in progress): live " +
			"resource markers v0 cover the root module only, and identity resolution, " +
			"discovery, marker stamping and the projection do not yet walk into a child " +
			"module. Move the module's resources into the root module, or split the module " +
			"into an estate of its own with its own live block, until that traversal ships"
	}
}
