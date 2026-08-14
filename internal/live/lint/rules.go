// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import "sort"

// Rules returns every rule this package can report, sorted.
//
// It exists for the same reason [identity.Refusals] does: a program that
// measures which refusals fire has to be able to ask what the whole set is,
// including the rules that fired nowhere. A rule with no configuration
// tripping it is the interesting end of that table, and a set assembled by
// watching output can never contain one.
//
// The set is [ruleInfo]'s keys, which is the same table [Rule.Summary] and
// [Rule.DocsRef] answer from. One consequence is worth stating rather than
// discovering: a Rule constant declared with no ruleInfo entry is absent
// here, and nothing in this package asserts there is no such constant.
// Closing that is GitHub issue #110's half of the work; until it is closed,
// this returns the documented rules rather than provably all of them.
func Rules() []Rule {
	out := make([]Rule, 0, len(ruleInfo))
	for rule := range ruleInfo {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
