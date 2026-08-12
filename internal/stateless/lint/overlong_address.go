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

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/stateless/markers"
)

// The overlong-address rule.
//
// A resource's canonical config address becomes the tofu-address marker on
// the live resource, escaped per stateless/MARKERS.md ("[" becomes ":", "]"
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
// Only root-module addresses are ever stamped: child modules are refused
// wholesale by RuleChildModule, so a module path never reaches a marker,
// and the address measured here is the whole value the marker would hold.

// checkOverlongAddresses reports every resource instance whose escaped
// tofu-address would exceed the AWS tag-value cap.
func checkOverlongAddresses(ctx context.Context, mod *configs.Module, path addrs.Module, issues *[]Issue) {
	for _, resource := range mod.ManagedResources {
		switch {
		case resource.ForEach != nil:
			keys, ok := staticForEachKeys(ctx, mod, resource.ForEach)
			if !ok {
				continue
			}
			for _, key := range keys {
				addr := resource.Addr().Instance(addrs.StringKey(key))
				reportOverlongAddress(addr.String(), resource.ForEach.Range(), path, issues)
			}
		case resource.Count != nil:
			n, ok := staticCount(ctx, mod, resource.Count)
			if !ok || n < 1 {
				continue
			}
			addr := resource.Addr().Instance(addrs.IntKey(n - 1))
			reportOverlongAddress(addr.String(), resource.Count.Range(), path, issues)
		default:
			reportOverlongAddress(resource.Addr().String(), resource.DeclRange, path, issues)
		}
	}
}

// reportOverlongAddress escapes one instance address exactly the way the
// stamped marker would be escaped and appends an issue if the result does
// not fit in a tag value.
func reportOverlongAddress(addr string, subject hcl.Range, path addrs.Module, issues *[]Issue) {
	length := utf8.RuneCountInString(markers.EscapeAddress(addr))
	if length <= markers.MaxTagValue {
		return
	}
	*issues = append(*issues, Issue{
		Rule:      RuleOverlongAddress,
		Construct: addr,
		Module:    path,
		Detail: fmt.Sprintf(
			"the escaped tofu-address for this instance is %d characters, and a tag value "+
				"holds at most %d (the AWS hard cap, stateless/MARKERS.md). The address becomes "+
				"the tofu-address marker on the live resource, and that marker is the only record "+
				"of ownership a stateless run has, so an address that does not fit is refused here "+
				"rather than truncated: silently truncating an ownership key is worse than refusing "+
				"to admit the resource. Shorten the resource label or the instance key",
			length, markers.MaxTagValue,
		),
		Subject: subject,
	})
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
