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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staticeval"
)

// The overlong-address rule.
//
// A resource's canonical config address becomes the tofu-address marker on
// the live resource, escaped per live/MARKERS.md ("[" becomes ":", "]"
// and `"` are dropped; markers.EscapeAddress is that rule in code). AWS
// caps a single tag value at 256 Unicode characters (markers.MaxTagValue),
// but an address that does not fit in one tag is not refused outright any
// more: it is split across up to markers.MaxContinuations tags -
// tofu-address, tofu-address-2, ... - concatenated back into one value on
// read (markers.GatherAddress). See live/MARKERS.md, "tofu-address
// continuation tags". The budget this rule enforces is therefore
// markers.MaxAddressLen (MaxContinuations x MaxTagValue), not MaxTagValue
// alone. MARKERS.md is explicit about which side gives past that wider
// ceiling: "An address that does not fit is a lint-time error, not a
// truncation. Silently truncating an ownership key is worse than refusing
// to admit the resource." This rule is that lint-time error.
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
// and carrying the longest-escaping key of any expanded module call in
// between, whether for_each'd (59c, issue #59 phase 3) or count'd (issue
// #195); see [worstCaseChildKey]. A static module
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
// compounds both into one escaped address. A count-expanded module block
// compounds the same way, through its highest index: this comment used to
// say such a block never reached here at all, because RuleChildModule
// refused it outright, and that stopped being true when issue #195 admitted
// a statically-evaluable module count with no count.index leak.
func checkOverlongAddresses(ctx context.Context, mod *configs.Module, modInst addrs.ModuleInstance, issues *[]Issue) {
	path := modInst.Module()
	for _, resource := range mod.ManagedResources {
		switch {
		case resource.ForEach != nil:
			keys, ok := staticeval.ForEachKeys(ctx, mod, resource.ForEach)
			if !ok {
				continue
			}
			for _, key := range keys {
				inst := resource.Addr().Instance(addrs.StringKey(key))
				reportOverlongAddress(inst, modInst, resource.ForEach.Range(), path, issues)
			}
		case resource.Count != nil:
			n, ok := staticeval.Count(ctx, mod, resource.Count)
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
// not fit in the continuation-tag budget.
//
// inst and modInst are handed separately, rather than as the one already-
// joined address string the rest of this file used to pass around, so the
// join point (inst.Absolute(modInst)) stays local to this function instead
// of being recomputed by every caller.
func reportOverlongAddress(inst addrs.ResourceInstance, modInst addrs.ModuleInstance, subject hcl.Range, path addrs.Module, issues *[]Issue) {
	addr := inst.Absolute(modInst).String()
	length := utf8.RuneCountInString(markers.EscapeAddress(addr))
	if length <= markers.MaxAddressLen {
		return
	}
	*issues = append(*issues, Issue{
		Rule:      RuleOverlongAddress,
		Construct: addr,
		Module:    path,
		Detail: fmt.Sprintf(
			"the escaped tofu-address for this instance is %d characters, and this fork carries "+
				"an address across at most %d tag values of %d characters each (live/MARKERS.md, "+
				"\"tofu-address continuation tags\"), a ceiling of %d characters in total. The "+
				"address becomes the tofu-address marker (and its continuation tags) on the live "+
				"resource, and that marker is the only record of ownership a stateless run has, so "+
				"an address that does not fit is refused here rather than truncated: silently "+
				"truncating an ownership key is worse than refusing to admit the resource. Shorten "+
				"the resource label, the instance key, or the module nesting",
			length, markers.MaxContinuations, markers.MaxTagValue, markers.MaxAddressLen,
		),
		Subject: subject,
	})
}
