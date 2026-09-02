// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2/hclsyntax"

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
//   - count on a module block is admitted (issue #195) under exactly the
//     same two-part test a resource's own count is already held to: the
//     count expression itself has to be statically evaluable (the same
//     var/local/path/terraform/tofu scope [staticCount] and
//     [identity.ChildModuleKeys] hold for_each to), and count.index must not
//     appear anywhere in the module call's own arguments. A plain integer
//     count is not positionally fragile the way the old comment here
//     claimed: module.name[i] is exactly as stable an address as
//     resource.name[i], and shrinking count from N to N-1 only ever retires
//     the highest index, never renumbers a survivor - see [childModuleDetail]
//     for the corpus evidence (every static module count found there is the
//     count = cond ? 1 : 0 optional-instance idiom, or a plain literal/var
//     integer, never a filtered list count relies on to derive an index).
//     The real hazard is count.index reaching into an identity-bearing
//     value - by indexing a sibling resource, or an argument passed into the
//     module - the same hazard [checkCountIndex] (count_index.go, issue
//     #192) already guards a resource's own body against; this check is
//     that same guard applied to a module call's arguments, with no
//     type-specific narrowing available (a module has no identity schema of
//     its own), so any count.index reference anywhere in the call's body
//     refuses it, matching [countIndexScope]'s walkAll default for an
//     unreviewed resource type. A count expression this pass cannot
//     statically evaluate is refused exactly as it always was, since a
//     dynamic module count is exactly as unaddressable up front as a
//     dynamic resource count (identity's countExpansion, resolve.go).
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
func checkChildModules(ctx context.Context, cfg *configs.Config, path addrs.Module, issues *[]Issue) {
	mod := cfg.Module
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		call := mod.ModuleCalls[name]
		detail, refused := childModuleDetail(ctx, cfg, call)
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
func childModuleDetail(ctx context.Context, cfg *configs.Config, call *configs.ModuleCall) (string, bool) {
	mod := cfg.Module
	switch {
	case call.Count != nil:
		if _, ok := staticCount(ctx, mod, call.Count); !ok {
			return fmt.Sprintf(
				"count on a module block is admitted only when it is statically "+
					"evaluable - a literal, or an expression built from variables, locals, "+
					"path and terraform values - the same scope a resource's own count is "+
					"evaluated in, because the instance count has to be knowable before "+
					"anything is read from the cloud. count for module %q is not. Move the "+
					"module's resources into the root module, or split the module into an "+
					"estate of its own with its own live block, until the count expression "+
					"is statically evaluable",
				call.Name,
			), true
		}
		if moduleCallHasCountIndex(call) {
			return fmt.Sprintf(
				"module %q's own arguments read count.index: the lexical index of a count "+
					"instance is not stable across scale-up, scale-down, or reordering, so a "+
					"property built from it - directly, or by indexing a sibling resource's "+
					"own count-expanded collection - cannot be recovered from the live "+
					"system with no memory, the same reason a resource's own count.index is "+
					`refused wherever it can reach identity (live/LIMITATIONS.md, `+
					`"count-index-in-tag"). Replace count.index here with a value that does `+
					"not depend on this instance's position - a for_each key, or an argument "+
					"that is the same for every instance",
				call.Name,
			), true
		}
		return "", false
	case call.ForEach != nil:
		if _, diag := identity.ChildModuleKeys(ctx, cfg, fmt.Sprintf("module %q", call.Name), call.ForEach); diag != nil {
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

// moduleCallHasCountIndex reports whether a count-carrying module call's own
// arguments reference count.index anywhere - the module-call analogue of
// [checkCountIndex]'s walk over a resource's own body. A module has no
// identity schema of its own to narrow the walk with (identity.LookupType's
// Components describe a resource type's arguments, not a module's), so this
// always walks every attribute at every depth, the same [countIndexScope]
// walkAll default [countIndexScopeForType] falls back to for a resource type
// this pass has no table row for.
//
// call.Config is the leftover body after decodeModuleBlock has already
// extracted source, version, providers, count, for_each and depends_on
// (module_call.go), so nothing more needs to be skipped at the top level the
// way checkCountIndex skips a resource's own meta-arguments.
func moduleCallHasCountIndex(call *configs.ModuleCall) bool {
	body, ok := call.Config.(*hclsyntax.Body)
	if !ok {
		// JSON-syntax module call (*.tf.json): no schema-less way to walk
		// nested blocks, the same documented gap [checkCountIndex] has (see
		// doc.go). Refusing rather than silently admitting is the safe
		// direction to fail in when this pass cannot see the body at all.
		return true
	}
	// The empty countIndexDomain deliberately withholds the value-level
	// check [unsafeCountIndexHits] would otherwise apply, leaving a
	// module call exactly the syntactic rule it has always had. Proving a
	// module call's own ARGUMENTS render distinct per instance would not
	// prove what matters here: the identities of every resource inside the
	// module, built from those arguments in ways this pass cannot see from
	// the call site, and addressed by an instance key that becomes part of
	// every one of them.
	for _, hit := range countIndexCandidates(body, false, countIndexScope{walkAll: true}, countIndexDomain{}) {
		ref, refDiags := addrs.ParseRef(hit.traversal)
		if refDiags.HasErrors() || ref == nil {
			continue
		}
		if countAttr, ok := ref.Subject.(addrs.CountAttr); ok && countAttr.Name == "index" {
			return true
		}
	}
	return false
}
