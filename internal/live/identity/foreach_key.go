// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markerkey"
	"github.com/intentius/choudoufu/internal/live/staticeval"
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
// in [markerkey], so the two points cannot drift.
//
// Only the statically-expanded for_each branch calls this. A block whose
// for_each iterates over another resource inherits that resource's keys,
// which were checked where they were declared, and reporting them twice
// would make one bad key look like two problems.
//
// The set of runes a key may use here is not fixed: it narrows to
// legalRune (issue #227) whenever [stampCanReadStatically] says no, on top
// of the [markerkey.Valid] check that always applies. See that function's
// doc comment for why - the short version is that this resolver can
// compute a for_each key identity resolution alone can see (a data source,
// another resource, anything only known once the cloud is read), but
// stamp cannot re-derive the same for_each expression later to build the
// per-key escape table [markerkey.Encode] needs, and falls back silently
// to a narrower template that only reproduces legalRune's own doubling
// rule.
func (r *resolver) checkedForEachKeys(rc *configs.Resource, exp *expansion) (*expansion, bool) {
	strict := !stampCanReadStatically(rc.ForEach)
	ok := true
	for _, key := range exp.keys {
		strKey, isString := key.(addrs.StringKey)
		if !isString {
			continue
		}
		s := string(strKey)
		bad, isBad := markerkey.InvalidRune(s)
		narrowed := false
		if !isBad && strict {
			bad, isBad = markerkey.FirstEncodeRune(s)
			narrowed = isBad
		}
		if !isBad {
			continue
		}
		ok = false
		if narrowed {
			r.errorf(rc.ForEach.Range(), "for_each key cannot be recorded as a marker",
				"%s expands to an instance keyed %q, and that key contains %s. "+
					"The key becomes part of the resource's address, the address becomes the tofu-address marker on the live resource, "+
					"and that marker is the only record of ownership a live-markers run has (live/MARKERS.md). "+
					"This for_each expression cannot be re-derived from configuration alone at stamp time (it depends on a data source, another resource, or something else only known once the cloud is read), so the escaping this character would otherwise need cannot be computed for it. "+
					"A key may contain letters, digits, space, and the characters + - = . _ : / @ here: the AWS tag-value character set. Rename the key.",
				rc.Addr().String(), s, markerkey.DescribeRune(bad))
			continue
		}
		r.errorf(rc.ForEach.Range(), "for_each key cannot be recorded as a marker",
			"%s expands to an instance keyed %q, and that key contains %s. "+
				"The key becomes part of the resource's address, the address becomes the tofu-address marker on the live resource, "+
				"and that marker is the only record of ownership a live-markers run has (live/MARKERS.md). "+
				"A key may contain letters, digits, space, and the characters + - = . _ : / @: the AWS tag-value character set. Rename the key.",
			rc.Addr().String(), s, markerkey.DescribeRune(bad))
	}
	if !ok {
		return nil, false
	}
	return exp, true
}

// stampCanReadStatically reports whether stamp and lint's own evaluator -
// which never sees a data result, a resource attribute, or anything else
// only known once the cloud has been read - could compute this for_each
// expression's keys the same way this resolver just did.
//
// It is the exact traversal pre-filter both [lint]'s and [stamp]'s own
// staticForEachKeys apply before ever calling their evaluator: every
// variable referenced ANYWHERE in the expression - including inside a
// comprehension's value clause or an object constructor's value, not just
// the part that decides the key set - must be rooted at var, local, path,
// terraform or tofu, or their static evaluator refuses to run at all
// (staticScopeData panics by contract on anything else). A for_each this
// returns false for is exactly the shape internal/live/stamp's addressExpr
// doc comment names as the one case its ordinary each.key template is not
// proven correct for: "a block ... whose keys this pass cannot read
// statically at all".
//
// The predicate itself is [staticeval.AllowedExpr] (issue #826). It used to
// be reproduced here, with a comment saying that keeping the two
// static-read boundaries in lockstep was worth a few duplicated lines
// rather than a dependency between packages that otherwise have none;
// internal/live/staticeval is a leaf under both, so the lockstep is now
// structural rather than a promise. The nil case stays here: "no for_each
// at all" is this caller's question to answer, not the allowlist's.
func stampCanReadStatically(expr hcl.Expression) bool {
	if expr == nil {
		return true
	}
	return staticeval.AllowedExpr(expr)
}
