// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// Lookalike is the lookalike guard's warning: a declared instance the plan
// proposes to create, beside a live resource this estate does not own that
// might be the very resource being duplicated - most often because its
// tofu-estate and tofu-address tags were stripped out of band, off a
// server-assigned resource that no marker means no other way to find. Warn,
// never block: the create may be genuinely intended, and nothing here
// changes it.
type Lookalike struct {
	// Addr is the declared instance the plan proposes to create.
	Addr addrs.AbsResourceInstance

	// TypeName is its resource type.
	TypeName string

	// LiveID is the unowned live resource's identity, the handle an operator
	// needs to go look at it. Empty when the provider sent no usable
	// identity, the same case [Resource.LiveID] covers.
	LiveID string

	// DisplayName is the provider's label for it, when it differs from
	// LiveID.
	DisplayName string

	// Matched are the identity-bearing arguments the live resource and the
	// declared instance agreed on exactly - the same evidence a
	// [Candidate] carries. Empty for the generic, no-[matchTable]-entry
	// case: there, nothing in configuration confirmed the match, only
	// cardinality did, and pretending otherwise would overstate the
	// evidence.
	Matched []AttrMatch

	// MarkerEstate and MarkerAddress are the tofu-estate and tofu-address
	// values that adopt the live resource instead of creating a duplicate
	// beside it.
	MarkerEstate  string
	MarkerAddress string

	// Hint is the one-line adoption command composed by the same machinery
	// [Candidate.Hint] uses, empty for a type this fork has no composable
	// tagging verb for.
	Hint string
}

// String renders a lookalike warning on one line, for logs and test failure
// output.
func (l Lookalike) String() string {
	id := l.LiveID
	if id == "" {
		id = "(no identity)"
	}
	return l.Addr.String() + " ~ " + l.TypeName + " " + id
}

// Lookalikes is the lookalike guard. Given the addresses a plan actually
// proposes to create, and the [Result] a [Classify] call already produced
// over the same discovery pass the plan's prior state was built from, it
// returns one warning for every create that a live resource this estate does
// not own might be the very thing being duplicated.
//
// Two paths, both as conservative as [Classify] itself, and neither one
// re-derives what [Classify] already decided:
//
//   - A type with a [matchTable] entry warns only when [Result.Candidates]
//     already offers this exact address a match - the one-to-one,
//     every-argument-equal content match [Classify] computed once. A
//     matchTable type with no confirmed candidate (a near miss, an
//     ambiguity, no unclaimed resource of that type at all) stays silent;
//     a wrong guess would point an operator at the wrong resource, and
//     [Classify]'s own doc says a missed hint is the cheaper mistake.
//   - A type absent from the table has nothing in configuration that could
//     confirm a match, so the only safe signal left is cardinality: exactly
//     one unclaimed live resource of the create's type in [Result.Foreign].
//     Zero says nothing and more than one is the same ambiguity the
//     one-to-one rule refuses to resolve by guessing, so both stay silent.
//     Like the matchTable path, a keyed (count/for_each) create is skipped
//     outright: distinguishing "the marker was stripped off one member" from
//     "the set legitimately grew" needs the slot matcher, not a cardinality
//     count, and guessing here would flag ordinary scale-out as a duplicate.
//
// Nothing here touches the plan. A create beside a genuine lookalike is
// still a create; this only makes sure whoever reads the plan sees why it
// might be the wrong one before applying it.
func Lookalikes(req Request, res *Result, creates []addrs.AbsResourceInstance) []Lookalike {
	if res == nil {
		return nil
	}

	var out []Lookalike
	for _, addr := range creates {
		typeName := addr.Resource.Resource.Type

		if c, ok := res.CandidateFor(addr); ok {
			out = append(out, Lookalike{
				Addr:          addr,
				TypeName:      c.TypeName,
				LiveID:        c.LiveID,
				DisplayName:   c.DisplayName,
				Matched:       c.Matched,
				MarkerEstate:  c.MarkerEstate,
				MarkerAddress: c.MarkerAddress,
				Hint:          c.Hint,
			})
			continue
		}
		if _, hasTable := matchTable[typeName]; hasTable {
			// Matchable in principle, but nothing confirmed it. Silence is
			// the safe direction, the same one Classify itself takes on a
			// near miss or an ambiguity.
			continue
		}
		if addr.Resource.Key != addrs.NoKey {
			// A count/for_each member: cardinality alone cannot tell a
			// stripped marker from ordinary scale-out here, and the slot
			// matcher, not this guard, is what decides that.
			continue
		}

		var only *Resource
		ambiguous := false
		for i := range res.Foreign {
			f := &res.Foreign[i]
			if f.TypeName != typeName {
				continue
			}
			if only != nil {
				ambiguous = true
				break
			}
			only = f
		}
		if only == nil || ambiguous {
			continue
		}

		out = append(out, Lookalike{
			Addr:          addr,
			TypeName:      typeName,
			LiveID:        only.LiveID,
			DisplayName:   only.DisplayName,
			MarkerEstate:  res.Estate,
			MarkerAddress: discovery.EscapeAddress(addr.String()),
			Hint:          adoptionHint(req, &discovery.UnclaimedResource{TypeName: typeName, ImportID: only.LiveID}, addr),
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}
