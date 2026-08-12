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
// written.
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
			Detail: "live resource markers v0 cover the root module only. Identity resolution, " +
				"discovery, marker stamping and the projection all stop at the root, and " +
				"module expansion - count or for_each on a module block - changes every " +
				"resource address inside the module, which is what a tofu-address marker " +
				"records. Move the module's resources into the root module, or split the " +
				"module into an estate of its own with its own live block",
			Subject: call.DeclRange,
		})
	}
}
