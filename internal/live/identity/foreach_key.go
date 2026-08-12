// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// checkedForEachKeys refuses an expansion whose instance keys cannot survive
// the trip through a tofu-address marker, and passes it through otherwise.
//
// This is the second enforcement point for [lint.RuleForEachKey], not a
// duplicate of it. Lint checks the for_each expressions it can evaluate from
// the configuration text; the resolver is where an expression that lint
// declined to guess at has actually been evaluated, and it is the last place
// before a key becomes an address, an address becomes a marker, and the
// marker becomes something no later run can read. The rule is defined once,
// in the lint package, so the two points cannot drift.
//
// Only the statically-expanded for_each branch calls this. A block whose
// for_each iterates over another resource inherits that resource's keys,
// which were checked where they were declared, and reporting them twice
// would make one bad key look like two problems.
func (r *resolver) checkedForEachKeys(rc *configs.Resource, exp *expansion) (*expansion, bool) {
	ok := true
	for _, key := range exp.keys {
		strKey, isString := key.(addrs.StringKey)
		if !isString {
			continue
		}
		bad, isBad := lint.InvalidForEachKeyRune(string(strKey))
		if !isBad {
			continue
		}
		ok = false
		r.errorf(rc.ForEach.Range(), "for_each key cannot be recorded as a marker",
			"%s expands to an instance keyed %q, and that key contains %s. "+
				"The key becomes part of the resource's address, the address becomes the tofu-address marker on the live resource, "+
				"and that marker is the only record of ownership a live-markers run has (live/MARKERS.md). "+
				"A key may contain letters, digits, space, and the characters + - = _ / @: the AWS tag-value character set, "+
				"less \".\" and \":\", which separate the segments of an escaped address. Rename the key.",
			rc.Addr().String(), string(strKey), lint.DescribeForEachKeyRune(bad))
	}
	if !ok {
		return nil, false
	}
	return exp, true
}
