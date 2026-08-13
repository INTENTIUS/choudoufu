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
// service namespaces, or regions it may consider. A Policy that carries a
// Delete verb always has a non-nil Scope with at least one non-empty field -
// internal/live/lint refuses a delete quadrant with no scope block before a
// [Policy] is ever built from it.
type Scope struct {
	Services []string
	Types    []string
	Regions  []string
}

// Policy is the fully resolved ownership policy: one verb per quadrant, the
// tag every quadrant's "tagged" half is read against, and the delete
// quadrant's safety rails. Every quadrant field is always set - never the
// empty Verb - because [Build] fills whatever a configuration's policy block
// omitted (or the whole configuration, when it has no policy block at all)
// from [DefaultVerb].
//
// See the package doc comment for what consumes this today: nothing. It is
// constructed and threaded as far as the live commands' setup and stops
// there until #59b and #60 land.
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
