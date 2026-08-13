// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// The overlong-address rule.
//
// A resource's canonical config address becomes the tofu-address marker on
// the live resource, escaped per live/MARKERS.md ("[" becomes ":", "]"
// and `"` are dropped; markers.EscapeAddress is that rule in code), and AWS
// caps a tag value at 256 Unicode characters (markers.MaxTagValue).
// MARKERS.md is explicit about which side gives: "An address that does not
// fit is a lint-time error, not a truncation. Silently truncating an
// ownership key is worse than refusing to admit the resource." This rule is
// that lint-time error.
//
// What is measured is the escaped instance address, one per instance the
// rule can see:
//
//   - a resource with neither count nor for_each has exactly one instance,
//     whose escaped address is the address as written (nothing to rewrite);
//   - a for_each resource is measured once per key, under the same can-see
//     boundary as checkForEachKeys: keys computable from the static scope
//     alone are measured, everything else is skipped rather than guessed
//     at, because identity resolution refuses non-static for_each outright;
//   - a count resource is measured at its highest index when the count
//     expression is statically evaluable, since the escaped address grows
//     monotonically with the index and the highest index is the longest.
//
// checkOverlongAddresses runs once per module in the tree (checkConfig calls
// it at every node), and modInst is that node's own worst-case module
// instance - unkeyed at every step reached through a static module call,
// and carrying the longest key of any for_each'd module call in between
// (59c, issue #59 phase 3; see [worstCaseChildKey]). A static module
// block's resources measure module-qualified: an instance three levels deep
// is prefixed with "module.a.module.b.module.c." before the budget is
// checked, and one reached through a keyed module call is prefixed with
// that call's longest key instead of an unkeyed step, because that prefix
// is what the marker's tofu-address value actually carries once it is
// stamped for the instance that key names - see identity.ModuleInstance for
// why an unkeyed instance shim is the right (and lossless) reading of a
// purely static path. Keys multiply length exactly the way a resource's own
// for_each or count key does, which is why this rule has to measure the
// expanded instance rather than the bare module path: a module with a long
// for_each key wrapping a resource with a long for_each key of its own
// compounds both into one escaped address. count-expanded module blocks
// never reach here at all: RuleChildModule refuses them outright, before
// any module they call is walked for its own resources.
func checkOverlongAddresses(ctx context.Context, mod *configs.Module, modInst addrs.ModuleInstance, issues *[]Issue) {
	path := modInst.Module()
	for _, resource := range mod.ManagedResources {
		switch {
		case resource.ForEach != nil:
			keys, ok := staticForEachKeys(ctx, mod, resource.ForEach)
			if !ok {
				continue
			}
			for _, key := range keys {
				inst := resource.Addr().Instance(addrs.StringKey(key))
				reportOverlongAddress(inst, modInst, resource.ForEach.Range(), path, issues)
			}
		case resource.Count != nil:
			n, ok := staticCount(ctx, mod, resource.Count)
			if !ok || n < 1 {
				continue
			}
			inst := resource.Addr().Instance(addrs.IntKey(n - 1))
			reportOverlongAddress(inst, modInst, resource.Count.Range(), path, issues)
		default:
			inst := resource.Addr().Instance(addrs.NoKey)
			reportOverlongAddress(inst, modInst, resource.DeclRange, path, issues)
		}
	}
}

// reportOverlongAddress escapes one instance address exactly the way the
// stamped marker would be escaped and appends an issue if the result does
// not fit in a tag value.
//
// inst and modInst are handed separately, rather than as the one already-
// joined address string the rest of this file used to pass around, because
// the refusal below breaks the budget down into the module path's share and
// the resource's own share - see budgetBreakdown - and that split needs the
// join point, not just the joined result.
func reportOverlongAddress(inst addrs.ResourceInstance, modInst addrs.ModuleInstance, subject hcl.Range, path addrs.Module, issues *[]Issue) {
	addr := inst.Absolute(modInst).String()
	length := utf8.RuneCountInString(markers.EscapeAddress(addr))
	if length <= markers.MaxTagValue {
		return
	}
	*issues = append(*issues, Issue{
		Rule:      RuleOverlongAddress,
		Construct: addr,
		Module:    path,
		Detail: fmt.Sprintf(
			"the escaped tofu-address for this instance is %d characters, %d over the "+
				"256-character budget (the AWS hard cap on a tag value, live/MARKERS.md). "+
				"%s The address becomes the tofu-address marker on the live resource, and "+
				"that marker - together with the separate tofu-estate tag, which has its own "+
				"independent 128-character budget and is not part of this one - is the only "+
				"record of ownership a stateless run has. An address that does not fit is "+
				"refused here rather than truncated: silently truncating an ownership key is "+
				"worse than refusing to admit the resource. To fit: shorten the module names "+
				"in the path, flatten a level of module nesting, shorten the resource label, "+
				"pick a shorter for_each key, or move the resource somewhere its address is "+
				"shorter and carry its marker over with \"choudoufu live-mv\" rather than "+
				"re-adopting it from scratch. See live/LIMITATIONS.md, \"overlong-address\", "+
				"for the full budget math and a worst-case example.",
			length, length-markers.MaxTagValue, budgetBreakdown(inst, modInst),
		),
		Subject: subject,
	})
}

// budgetBreakdown is the one sentence that turns "256 characters" into
// something a reader can act on: how much of this instance's escaped address
// the module path spends, versus how much is left for the resource type,
// its label and its instance key combined. Module nesting is the part that
// grows without the author writing a longer resource label - each level
// costs a literal "module." (7 characters) plus the module's own name and,
// for a keyed module call, an escaped instance key - so it is the module
// path's share that most often explains why an address that looked
// reasonable in a flat root module stopped fitting once it moved three
// levels deep.
func budgetBreakdown(inst addrs.ResourceInstance, modInst addrs.ModuleInstance) string {
	total := utf8.RuneCountInString(markers.EscapeAddress(inst.Absolute(modInst).String()))
	resourceOnly := utf8.RuneCountInString(markers.EscapeAddress(inst.String()))
	modulePath := total - resourceOnly
	if modulePath <= 0 {
		return fmt.Sprintf(
			"This instance declares no module path, so the resource type, label and instance "+
				"key alone account for all %d characters.",
			resourceOnly,
		)
	}
	remaining := markers.MaxTagValue - modulePath
	if remaining <= 0 {
		return fmt.Sprintf(
			"Of that, the module path (\"%s\", including the separator before the resource "+
				"address) alone spends %d characters - already past the 256-character budget "+
				"before the resource type, label or instance key are counted at all.",
			modInst.String(), modulePath,
		)
	}
	return fmt.Sprintf(
		"Of that, the module path (\"%s\", including the separator before the resource "+
			"address) spends %d characters, leaving %d for the resource type, label and "+
			"instance key combined - the budget that shrinks every time this instance moves "+
			"one level deeper into the module tree.",
		modInst.String(), modulePath, remaining,
	)
}

// staticCount computes the value of a count expression, or reports that it
// is not computable here. It mirrors staticForEachKeys: the same traversal
// pre-filter keeps the static scope's panic classes out of the evaluator,
// and anything it cannot evaluate is skipped rather than guessed at.
func staticCount(ctx context.Context, mod *configs.Module, expr hcl.Expression) (int, bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return 0, false
	}
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "var", "local", "path", "terraform":
			// Evaluable in a static scope.
		default:
			return 0, false
		}
	}

	val, ok := evalStatic(ctx, mod.StaticEvaluator, expr, "count")
	if !ok || val == cty.NilVal || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() {
		return 0, false
	}

	var n int
	if err := gocty.FromCtyValue(val, &n); err != nil {
		// Not a whole number: not a legal count at all, and identity
		// resolution says so with its own message.
		return 0, false
	}
	return n, true
}
