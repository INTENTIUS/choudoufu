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
// [Rule.DocsRef] answer from. That used to make this "the documented rules
// rather than provably all of them", because a Rule constant with no
// ruleInfo entry was absent here and nothing said so. TestEveryRuleConstantIsRegistered
// closes it: it parses this package's own declarations - const and var,
// typed and via a Rule(...) conversion - and fails if any of them is missing
// from ruleInfo, or if ruleInfo names one that no longer exists.
func Rules() []Rule {
	out := make([]Rule, 0, len(ruleInfo))
	for rule := range ruleInfo {
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
