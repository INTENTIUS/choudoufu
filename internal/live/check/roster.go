// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// InstanceRung is HANDOFF.md's own vocabulary for what varies per instance
// once a type is admitted at all ("So every type stock supports is
// admitted. What varies per instance is its rung: tag-governable, derived
// from configuration, or record-only. That is a metric that goes up, never
// a gate that refuses."), named [InstanceRung] rather than a bare "Rung"
// because this package already has two other things a comment might call a
// rung and none of the three are interchangeable:
//
//   - [OnboardingClass] (ladder.go) is an ESTATE's rung on #175's onboarding
//     ladder - unreadable, language-blocked, clean, and so on - one verdict
//     for the whole configuration.
//   - Sibling issue #788's `bound[]` sources (a separate JSON surface on
//     "choudoufu live-plan") classify where a binding's VALUE came from -
//     marker, record, derived, cache - which is a question about a plan-time
//     read, not about a type's admission tier.
//
// This is the third: a fact about one managed resource INSTANCE's type, the
// one live/e2e's own README and site/content/docs/use/resource-tiers.md
// call marker-carried / declaration-carried / record-carried (and
// tools/readiness-gen's TierMarkerCarried/TierDeclarationCarried/
// TierRecordCarried constants, the closest existing Go spelling - a
// TYPE-level readiness table computed offline from generated survey
// artifacts, not something this package can import: it lives in a
// tools/*-gen `package main`, the same reason [dataread.Analyze]'s own doc
// comment gives for not importing a sibling generator's package). GitHub
// issue #790 asks for exactly HANDOFF.md's own three words - tag-governable,
// declaration-carried, record-only - so that is what this names, rather
// than inventing a fourth spelling next to a table that already has one.
type InstanceRung string

const (
	// RungTagGovernable is HANDOFF's "tag-governable": the type's schema
	// carries a settable tags argument, so a lost record store is
	// recoverable from the marker alone - resource-tiers.md's
	// "marker-carried" tier under a different name.
	RungTagGovernable InstanceRung = "tag-governable"

	// RungDeclarationCarried is HANDOFF's "derived from configuration":
	// untaggable, but the type is admitted - its identity is fully supplied
	// by configuration, or composed from a parent's live identity plus
	// configuration data. resource-tiers.md's "declaration-carried" tier,
	// same spelling.
	RungDeclarationCarried InstanceRung = "declaration-carried"

	// RungRecordOnly is HANDOFF's "record-only": untaggable AND
	// server-minted, [identity.MarkerlessTypes]' own population -
	// resource-tiers.md's "record-carried" tier under a different name.
	RungRecordOnly InstanceRung = "record-only"
)

// Instance is one declared address in GitHub issue #790's roster: every
// instance this analysis named an address for, resolved or refused, with
// the rung its type earns. It is the per-instance restatement of what the
// text report already carries one summarized count at a time (Report.
// Instances above, and the Findings a refused instance falls under) - for a
// reader like behold (named in #790's own text) that parses no HCL of its
// own and needs a roster of addresses rather than a verdict.
type Instance struct {
	// Address is the resource instance's own address, in
	// [addrs.AbsResourceInstance.String] form for a resolved instance, or
	// whatever [Site.Address] recovered for a refused one - see that
	// field's own doc comment for which refusals carry one and which do
	// not.
	Address string

	// Type is the managed resource type, when it is known. Left empty
	// rather than guessed for a refused site whose diagnostic carried none
	// - the same rule [Site.Type] already follows.
	Type string

	// Rung is what [InstanceRung] classifies: a fact about Type, not about
	// how this particular instance's identity happened to resolve. Empty
	// when Type is empty, for the same reason.
	Rung InstanceRung

	// Refused is true when this instance is a refusal site rather than a
	// resolution - Rule and Reason are populated only then.
	Refused bool

	// Rule is the refusal's stable identity ([Refusal.ID]: a [lint.Rule]
	// string, or the Summary an identity or data-read diagnostic carries),
	// the same value the finding is grouped and ranked on. Populated only
	// when Refused.
	Rule string

	// Reason is the site's own explanation - [Site.Detail], falling back to
	// the finding's [Finding.Remedy] on the rare site that carries none
	// (projection's two offline refusals are sourceless and detail-free at
	// the site level; see TestSourcelessSiteRendersAsNothingRatherThanABlankLine
	// in internal/command/views). Populated only when Refused.
	Reason string
}

// buildRoster assembles GitHub issue #790's roster from the same two
// results the text report already reads: [Report.Identities] (every
// instance that resolved) and the ranked [Report.Findings] (every refusal,
// with the sites it fired at). Running after both are final, rather than
// threaded through [Analyze]'s own diagnostic loop, is what lets #790's own
// "Done when" hold by construction: the roster and the text summary read
// the same two slices, so a count in one can never drift from the other.
//
// An address is never listed twice. A resolved instance and a refused site
// are disjoint in practice - identity.ResolveWith either produces a
// Resolution for an instance or raises an error diagnostic about it, never
// both - but the dedupe guards it structurally rather than trusting that
// invariant to hold forever.
func buildRoster(schemas map[string]providers.Schema, identities []identity.Resolution, findings []Finding) []Instance {
	seen := make(map[string]bool, len(identities))
	var out []Instance

	for _, res := range identities {
		addr := res.Addr.String()
		if seen[addr] {
			continue
		}
		seen[addr] = true
		resType := res.Addr.Resource.Resource.Type
		out = append(out, Instance{
			Address: addr,
			Type:    resType,
			Rung:    rungForType(schemas, resType),
		})
	}

	for _, f := range findings {
		for _, site := range f.Sites {
			if site.Address == "" || seen[site.Address] {
				continue
			}
			seen[site.Address] = true
			reason := site.Detail
			if reason == "" {
				reason = f.Remedy()
			}
			out = append(out, Instance{
				Address: site.Address,
				Type:    site.Type,
				Rung:    rungForType(schemas, site.Type),
				Refused: true,
				Rule:    f.ID,
				Reason:  reason,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// rungForType is the per-instance rung HANDOFF.md's own "So every type
// stock supports is admitted. What varies per instance is its rung" line
// names. It is a fact about the resource TYPE, not about how this
// particular instance's identity happened to resolve: an aws_s3_bucket
// with a literal bucket name in configuration still recovers from a lost
// record store by its tag, so it is tag-governable even though its own
// resolution is [identity.ClassConcrete] rather than
// [identity.ClassNeedsDiscovery] - the two axes answer different
// questions, and this function only answers the type one.
//
// [identity.MarkerlessTypes] decides record-only outright: it is generated
// from the same taggability signal this function's own schema check reads,
// for exactly the types that have neither a tags argument nor a
// client-suppliable identity. A type that is not in it, and whose schema
// (when one was read - see [Context.Schemas]) carries a settable tags
// argument via [markers.Taggable], is tag-governable; every other admitted
// type is declaration-carried by elimination, matching the middle tier
// site/content/docs/use/resource-tiers.md and tools/readiness-gen's own
// TierDeclarationCarried both name "declaration-carried" for.
//
// Without schemas (an uninitialized directory - see [Context.Schemas]),
// taggability cannot be read for a type outside [identity.MarkerlessTypes]
// at all, and this returns declaration-carried rather than guessing tag-
// governable - the same direction every other schema-less degradation in
// this package takes: it costs precision, never a wrong claim of the
// stronger recovery path. resourceType == "" (a site with no recovered
// type - see [Site.Type]) returns "" rather than guessing either.
func rungForType(schemas map[string]providers.Schema, resourceType string) InstanceRung {
	if resourceType == "" {
		return ""
	}
	if _, markerless := identity.MarkerlessTypes[resourceType]; markerless {
		return RungRecordOnly
	}
	if schema, ok := schemas[resourceType]; ok && markers.Taggable(schema.Block) {
		return RungTagGovernable
	}
	return RungDeclarationCarried
}
