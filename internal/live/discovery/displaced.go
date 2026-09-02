// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
)

// This file is the discovery half of GitHub issue #244.
//
// # What was wrong
//
// Three scan loops - discovery.go's own, cloudcontrol.go's and tagging.go's -
// classify a live object this estate owns by the address its marker names.
// Each one ended a declared address at:
//
//	if decl.declares(typeName, escaped) {
//	    // ... the marker only confirms the estate still owns what it
//	    // thinks it owns.
//	    continue
//	}
//
// [declared.declares] reads [declared.all], which is populated from every
// resolution with no class filter, so the question it answers is "does the
// configuration declare this address at all". The comment's question - is
// this object the instance that address names - was never asked here, and it
// was not asked in internal/live/projection either, which deferred it back
// in a comment of its own. #244 half 1 made the projection ask it. This is
// the other side, and the two are NOT the same check over the same input:
//
//   - The projection sees only the object the CONFIGURATION's own identity
//     fetched. If that object's marker names a sibling it now refuses. It
//     never learns that a second object exists carrying the address the first
//     one should have had, because it never looks anywhere but at the one
//     identity it computed.
//   - This pass sees the objects the CLOUD lists. It is the only layer
//     holding both halves at once: the marker on the object, and the identity
//     the configuration computes for the address that marker names.
//
// So the object left behind by a renumbering - the one still carrying
// aws_x.r[1] after r[1]'s identity moved to another object - reaches this
// pass and nothing else. Before this file it was in no section of the result:
// not bound, not an orphan, not a problem, not a removal. Invisible.
//
// # What this does, and the one thing it deliberately does not do
//
// It reports. It does not act. A displaced object stays out of Orphans,
// stays out of [Result.Removals], stays out of the resolutions, and the
// `continue` that used to end its classification still ends it. The only
// difference is that a [Problem] is filed first, at WARNING severity.
//
// That is the whole safety argument and it is worth stating plainly, because
// tightening a discovery classification is the direction that loses data: a
// false orphan is a proposed destroy. This change cannot produce one. It
// cannot produce a false destroy, a false create, a false adoption or a
// changed plan of any kind, because no code path downstream of a [Problem]
// with [SeverityWarning] reads it - the same reasoning
// [ProblemUnsweepableOwnedType] carries a few lines from here in result.go,
// and for the same situation: a resource that sits outside removal coverage,
// which #107 says is tolerable, while being silent about it is not.
//
// The residual cost of a false positive is therefore one warning line. That
// is what pays for the comparison below being an inexact one.
//
// # Why the comparison is inexact, and how it is bounded
//
// The identity the configuration computes and the identity the provider
// attaches to a listed object are two different objects that usually - not
// always - spell the same thing. [identity.Resolution.IdentityValues] is
// per-attribute and comes from the configuration's arguments;
// [importIdentity] takes the first populated attribute of the type's
// [identity.TypeIdentity.IdentityAttrs] off whatever the provider sent. A
// composite import ID compared against one attribute of it would mismatch on
// every instance of the type, which would be exactly the "rule that refuses
// working configurations" this repository has been bitten by.
//
// So displacedFrom compares only where it can compare like with like, and
// every case it cannot reach returns "not displaced" - today's silence:
//
//   - Not [identity.ClassConcrete]: nothing to compare. A needs-discovery
//     address never reaches this code at all (it is bound through
//     [declared.entryFor] one branch earlier); a parent-derived one has a
//     formula, not a value.
//   - Ambiguous ([declaredAddress.ambiguous]): two instances escape to one
//     marker value, so there is no single configured identity to compare to.
//   - Per-attribute where possible. Every attribute the configuration
//     supplies AND the listed object carries is compared, which is exact for
//     a composite: {"role": "r", "policy_arn": "a"} against a provider
//     identity carrying both. If ANY such attribute is present, the verdict
//     is that comparison and the fallback below does not run.
//   - Whole import ID only for a single-component type, where the import ID
//     IS that component's one value and cannot be a join of several. This is
//     the branch that fires against a provider or emulator whose identity
//     object carries only "id" - and the branch #244's own tests exercise.
//   - Anything else: not displaced.
//
// # The fallback's exposure, counted
//
// Over the 959 types [identity.AdmittedTypes] holds today: 519 are
// ServerAssigned and 10 have no components, so neither is ever concrete from
// configuration; 148 are multi-component, where the fallback's own guard
// declines; 282 are single-component and fallback-eligible. Of those 282,
// 192 have an [identity.TypeIdentity.IdentityAttrs] whose first entry is the
// attribute a component supplies - the two strings are the same string by
// construction - and 17 list no identity attributes at all, so
// [importIdentity] has nothing to read and c.importID is empty.
//
// That leaves 73 where the first identity attribute is a bare "id" the
// entry's component does not itself supply. For almost all of them "id" and
// the import ID are the same value: aws_cloudwatch_log_group's id is its
// name, aws_dynamodb_table's is its name, and the table lists id first
// precisely because it spells what the import syntax spells. Screening the
// 73 for the one shape where they could legitimately differ - a documented
// [identity.TypeIdentity.ImportSyntax] naming an ARN against an identity
// attribute that does not, or the reverse - leaves exactly one type,
// aws_ecr_pull_time_update_exclusion (PRINCIPAL_ARN, identity attribute
// "id").
//
// And even that one only reaches the fallback where the provider serves an
// identity object carrying no attribute the configuration supplies, since
// the per-attribute path takes precedence wherever it can compare anything
// at all. The residual is one warning line on one type against an
// identity-poor provider. Recount these numbers rather than quoting them if
// the admission table has moved.
//
// The narrowing that matters most is not in this list: displacedFrom is only
// ever asked about an object that already carries THIS estate's tofu-estate
// marker AND a tofu-address the configuration declares. An object of a type
// this estate does not use, an object of another estate, and an untagged
// object never reach it.

// displacedFrom reports whether c is positively NOT the instance the address
// it is filed under names, and if so the identity the configuration computes
// for that instance, for the message.
//
// The asymmetry is deliberate: it answers "displaced" only on evidence, and
// answers "not displaced" for every kind of doubt. See this file's doc
// comment for the five ways it declines to answer.
func (d *declared) displacedFrom(typeName, escaped string, c claimant) (want string, displaced bool) {
	entry := d.all[typeName][escaped]
	if entry == nil || entry.ambiguous {
		return "", false
	}
	res := entry.res
	if res.Class != identity.ClassConcrete {
		return "", false
	}

	// The precise path: identity attributes both sides name. Sorted so that
	// a resolution with two mismatching attributes always reports the same
	// one - a Go map range here would make the diagnostic nondeterministic.
	if len(res.IdentityValues) > 0 {
		names := make([]string, 0, len(res.IdentityValues))
		for name := range res.IdentityValues {
			names = append(names, name)
		}
		sort.Strings(names)

		compared := false
		var firstMismatch string
		for _, name := range names {
			wantVal := res.IdentityValues[name]
			if wantVal == "" {
				continue
			}
			// Read through listclient's own accessor rather than reaching
			// into the cty.Value here: it is the one place that checks the
			// value is a known, unmarked string, and going through it means
			// this file adds no cty surface of its own for marksafe to
			// police.
			got, ok := listclient.Result{Identity: c.identity}.IdentityAttr(name)
			if !ok || got == "" {
				continue
			}
			compared = true
			if got != wantVal && firstMismatch == "" {
				firstMismatch = fmt.Sprintf("%s=%s", name, wantVal)
			}
		}
		if compared {
			return firstMismatch, firstMismatch != ""
		}
	}

	// The fallback: the whole import ID, and only where it cannot be a join.
	if res.ImportID == "" || c.importID == "" {
		return "", false
	}
	ti, ok := identity.LookupType(typeName)
	if !ok || len(ti.Components) != 1 {
		return "", false
	}
	if c.importID == res.ImportID {
		return "", false
	}
	return res.ImportID, true
}

// displacedProblem is the finding [declared.displacedFrom] produces.
//
// It names both identities, because the operator's next question is always
// "which of these two objects did I mean", and neither string alone answers
// it. It proposes nothing: see this file's doc comment.
func displacedProblem(req Request, typeName, escaped, want string, c claimant) Problem {
	return Problem{
		Kind:     ProblemDisplacedMarker,
		TypeName: typeName,
		Marker:   escaped,
		LiveIDs:  liveIDs(c.importID),
		Detail: fmt.Sprintf(
			"A live %s with identity %q carries estate %q and the address %q, but the identity this configuration computes for %s is %q. Two different live resources therefore answer to one address: this one by its marker, and another by the identity the configuration names. That is most often what a renumbering leaves behind - deleting a middle element of a count list moves every later instance's identity down one while the live resources keep the markers they were stamped with. Nothing is proposed for this resource: it is not read, not changed and not destroyed, and it will stay in the account until a human says which resource is which. Use choudoufu live-mv, or a moved block, to say that a resource moved; remove this resource's markers to disown it.",
			typeName, c.displayID(), req.Estate, escaped, escaped, want),
	}
}
