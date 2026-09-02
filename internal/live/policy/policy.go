// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package policy

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// Scope narrows a delete-quadrant verb's reach: the resource types, provider
// service namespaces, or regions it may consider.
//
// The invariant to rely on is narrower than the one this comment used to
// state, and the difference matters. A Policy whose **UndeclaredUntagged**
// verb is Delete has a non-nil Scope with at least one non-empty field,
// because internal/live/lint refuses that combination before a [Policy] is
// built and internal/live/discovery's reconcile refuses it again defensively.
//
// A Policy carrying a Delete verb *anywhere* has no such guarantee.
// [DefaultVerb] assigns Delete to UndeclaredTagged, so `Build(nil, "e")` -
// the preset every estate with no policy block gets - returns Delete with a
// nil Scope. That is the common case, not an edge one. The old wording said
// otherwise in three files, and a reader was entitled to rely on it (#116).
type Scope struct {
	Services []string
	Types    []string
	Regions  []string
}

// Allows reports whether typeName (with its provider service namespace,
// service - "" when the caller has none to offer, which never matches a
// populated Services list) and region are within this scope's reach.
//
// Each of the three lists narrows independently, and an empty list imposes
// no restriction of its own: a scope naming only regions still allows every
// admitted, enumerable type, restricted to those regions. That is what makes
// a region-only scope a legal (if broad) narrowing under
// internal/live/lint's "at least one of services, type, or region" rule,
// while a scope naming types or services narrows the type universe directly.
// nil is never passed here for a delete verb in practice - lint refuses that
// combination - but a nil Scope allows everything, matching "no scope block"
// reads as "unscoped" wherever this is called defensively.
func (s *Scope) Allows(typeName, service, region string) bool {
	if s == nil {
		return true
	}
	if len(s.Types) > 0 && !containsString(s.Types, typeName) {
		return false
	}
	if len(s.Services) > 0 && (service == "" || !containsString(s.Services, service)) {
		return false
	}
	if len(s.Regions) > 0 && (region == "" || !containsString(s.Regions, region)) {
		return false
	}
	return true
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// DefaultThreshold is the delete quadrant's first-run guard when a policy
// block sets no threshold of its own: issue #67 asks for "a configurable
// threshold (default modest)" and leaves choosing the number to the
// behavioral half. Ten is modest in the sense the issue means - small enough
// that a roster over it is worth a human's deliberate look before the next
// run is allowed to raise it, and large enough that an estate with a
// handful of stray resources does not trip the guard on its first legitimate
// scoped cleanup.
const DefaultThreshold = 10

// EffectiveThreshold is the threshold the delete quadrant's first-run guard
// actually enforces: the configured [Policy.Threshold] when the policy block
// set one, [DefaultThreshold] otherwise.
func (p *Policy) EffectiveThreshold() int {
	if p != nil && p.ThresholdSet {
		return p.Threshold
	}
	return DefaultThreshold
}

// TagMatches reports whether a live object's tags carry this policy's
// TagKey=TagValue - the "tagged" half of every quadrant. tags is nil-safe:
// an object the caller could read no tags from (untaggable, or a provider
// that sent none) reports false, the same "nothing says this is tagged"
// reading [Ownership] already gives an untaggable object's absence of a
// marker.
func (p *Policy) TagMatches(tags map[string]string) bool {
	if p == nil || tags == nil {
		return false
	}
	v, ok := tags[p.TagKey]
	return ok && v == p.TagValue
}

// Verb returns the verb this policy assigns the quadrant named by declared
// (the resource has a configuration address) and tagged (its live object
// carries TagKey=TagValue). A nil Policy returns the quadrant's
// [DefaultVerb], so a caller need not nil-check before asking.
func (p *Policy) Verb(declared, tagged bool) Verb {
	q := quadrantOf(declared, tagged)
	if p == nil {
		return DefaultVerb[q]
	}
	switch q {
	case DeclaredTagged:
		return p.DeclaredTagged
	case DeclaredUntagged:
		return p.DeclaredUntagged
	case UndeclaredTagged:
		return p.UndeclaredTagged
	default:
		return p.UndeclaredUntagged
	}
}

// QuadrantOf maps (declared, tagged) to the [Quadrant] the ownership matrix
// names it, the same pairing [Quadrant]'s own doc comment table draws.
//
// Exported because a caller that asks [Policy.Verb] for a verb almost always
// needs to name the quadrant it came from too - in a message to the
// operator, or to compare against that quadrant's [DefaultVerb].
// internal/live/discovery did both by hand and got it wrong: it passed
// declared=false to Verb, compared the result against
// DefaultVerb[UndeclaredTagged] whichever quadrant it was, and reported it
// under that quadrant's name. See GitHub issue #116.
func QuadrantOf(declared, tagged bool) Quadrant { return quadrantOf(declared, tagged) }

func quadrantOf(declared, tagged bool) Quadrant {
	switch {
	case declared && tagged:
		return DeclaredTagged
	case declared && !tagged:
		return DeclaredUntagged
	case !declared && tagged:
		return UndeclaredTagged
	default:
		return UndeclaredUntagged
	}
}

// Policy is the fully resolved ownership policy: one verb per quadrant, the
// tag every quadrant's "tagged" half is read against, and the delete
// quadrant's safety rails. Every quadrant field is always set - never the
// empty Verb - because [Build] fills whatever a configuration's policy block
// omitted (or the whole configuration, when it has no policy block at all)
// from [DefaultVerb].
//
// See the package doc comment for what consumes this: internal/live/
// projection (the two declared quadrants), internal/live/discovery (the
// undeclared quadrants: withholding an orphan from the sweep, and the
// scoped delete reconciliation), and internal/live/stamp (the declared+
// tagged untag verb's marker suppression).
type Policy struct {
	DeclaredTagged     Verb
	DeclaredUntagged   Verb
	UndeclaredTagged   Verb
	UndeclaredUntagged Verb

	// TagKey and TagValue name the tag every quadrant's "tagged" half is
	// read against. Both default to the estate marker: TagKey to
	// markers.TagEstate ("tofu-estate") and TagValue to the estate name
	// itself - the reading every quadrant had before this policy block
	// existed, where "tagged" meant "this resource's tofu-estate value is
	// this estate's name". A configuration names a different tag_key/
	// tag_value to use a preservation tag distinct from the estate marker
	// (issue #67's "one semantic question for the maintainer to confirm").
	TagKey   string
	TagValue string

	// Scope narrows the delete quadrant's reach. Nil unless the
	// configuration's policy block set a scope block.
	Scope *Scope

	// Threshold is the delete quadrant's first-run guard: a policy whose
	// delete quadrant would touch more resources than this refuses to
	// apply, so a lint-clean but overbroad scope is still caught before
	// anything is destroyed. ThresholdSet is false when the configuration
	// set none; enforcing the guard, and choosing the "modest" default
	// issue #67 promises for that case, is the behavioral half's job.
	Threshold    int
	ThresholdSet bool
}

// String renders the policy the way a plan is meant to show it eventually:
// one line per quadrant naming its verb, then the tag and safety rails those
// verbs read against. Nothing calls this for real output yet - see the
// package doc comment - but the shape is pinned here so the plan-rendering
// work behind #59b/#60 starts from an agreed format instead of inventing one
// from scratch.
func (p *Policy) String() string {
	if p == nil {
		return "policy: <none>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "policy (tag %s=%q):\n", p.TagKey, p.TagValue)
	fmt.Fprintf(&b, "  %s: %s\n", DeclaredTagged, p.DeclaredTagged)
	fmt.Fprintf(&b, "  %s: %s\n", DeclaredUntagged, p.DeclaredUntagged)
	fmt.Fprintf(&b, "  %s: %s\n", UndeclaredTagged, p.UndeclaredTagged)
	fmt.Fprintf(&b, "  %s: %s", UndeclaredUntagged, p.UndeclaredUntagged)
	if p.Scope != nil {
		fmt.Fprintf(&b, "\n  scope: services=%v types=%v regions=%v", p.Scope.Services, p.Scope.Types, p.Scope.Regions)
	}
	if p.ThresholdSet {
		fmt.Fprintf(&b, "\n  threshold: %d", p.Threshold)
	}
	return b.String()
}

// RawScope mirrors [github.com/intentius/choudoufu/internal/configs.LivePolicyScope]'s
// three list attributes, copied across by whichever caller has the decoded
// configuration (see [Raw]).
type RawScope struct {
	Services []string
	Types    []string
	Regions  []string
}

// Raw is the policy block's four quadrant verbs, tag key/value, scope and
// threshold, exactly as decoded from HCL: the string an author wrote for
// each attribute, or the zero value with its matching *Set flag false when
// they left it out.
//
// It is the bridge between configs.LivePolicy, which owns the HCL decode,
// and this package, which owns what a policy means. The two packages
// deliberately do not import each other (see the package doc comment), so a
// caller that has both - internal/live/lint's checkLivePolicy, and the live
// command setup that calls [Build] - copies the handful of fields across
// rather than either package reaching into the other.
type Raw struct {
	DeclaredTagged, DeclaredUntagged, UndeclaredTagged, UndeclaredUntagged             string
	DeclaredTaggedSet, DeclaredUntaggedSet, UndeclaredTaggedSet, UndeclaredUntaggedSet bool

	TagKey, TagValue       string
	TagKeySet, TagValueSet bool

	Scope *RawScope

	Threshold    int
	ThresholdSet bool
}

// Build resolves raw into a fully populated [Policy]: every quadrant an
// author omitted - or every quadrant, when raw is nil, meaning no policy
// block at all - is filled from [DefaultVerb], and an omitted tag_key/
// tag_value defaults to the estate marker (markers.TagEstate) and estate's
// name.
//
// Build does not validate. By the time a caller has a [Raw] worth building,
// internal/live/lint's checkLivePolicy has already run over the same
// configuration and would have refused an invalid verb or an unscoped
// delete quadrant as a lint issue. Build only fills gaps, the same "omitted
// means the old default" rule every other optional argument on the live
// block already follows.
func Build(raw *Raw, estate string) *Policy {
	p := &Policy{
		DeclaredTagged:     DefaultVerb[DeclaredTagged],
		DeclaredUntagged:   DefaultVerb[DeclaredUntagged],
		UndeclaredTagged:   DefaultVerb[UndeclaredTagged],
		UndeclaredUntagged: DefaultVerb[UndeclaredUntagged],
		TagKey:             markers.TagEstate,
		TagValue:           estate,
	}
	if raw == nil {
		return p
	}

	if raw.DeclaredTaggedSet {
		p.DeclaredTagged = Verb(raw.DeclaredTagged)
	}
	if raw.DeclaredUntaggedSet {
		p.DeclaredUntagged = Verb(raw.DeclaredUntagged)
	}
	if raw.UndeclaredTaggedSet {
		p.UndeclaredTagged = Verb(raw.UndeclaredTagged)
	}
	if raw.UndeclaredUntaggedSet {
		p.UndeclaredUntagged = Verb(raw.UndeclaredUntagged)
	}
	if raw.TagKeySet {
		p.TagKey = raw.TagKey
	}
	if raw.TagValueSet {
		p.TagValue = raw.TagValue
	}
	if raw.Scope != nil {
		p.Scope = &Scope{
			Services: append([]string(nil), raw.Scope.Services...),
			Types:    append([]string(nil), raw.Scope.Types...),
			Regions:  append([]string(nil), raw.Scope.Regions...),
		}
	}
	if raw.ThresholdSet {
		p.Threshold = raw.Threshold
		p.ThresholdSet = true
	}

	return p
}
