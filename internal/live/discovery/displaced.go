// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/projection"
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
//   - The estate's own record names this very object as the one the address
//     owns right now ([recordOwners]). The identities disagree because the
//     address is about to be replaced, not because a second object holds it.
//     This one is [verdictIdentityChanging] rather than [verdictOwnObject]:
//     silent, and not a vouch either. GitHub issue #885.
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

// addressVerdict is [declared.displacedFrom]'s answer about one live object
// wearing a declared address's marker. Three answers rather than two because
// GitHub issue #885 found a third case, and folding it into either of the
// existing ones is wrong in a different direction each way: reporting it
// produces the false warning #885 is about, and treating it as an ordinary
// confirmation hands out issue #692's vouch - permission to admit the
// address's object without reading its tags - on the strength of a sighting
// of a DIFFERENT object than the one the projection will read.
type addressVerdict int

const (
	// verdictOwnObject: nothing here contradicts this object being the
	// instance the address names. Either the identities agree, or nothing
	// comparable was available (see this file's doc comment). This is the
	// answer that vouches.
	verdictOwnObject addressVerdict = iota

	// verdictDisplaced: the configuration computes an identity this object
	// does not have, and no record says this object is the address's. The
	// finding [displacedProblem] renders.
	verdictDisplaced

	// verdictIdentityChanging: the configuration computes an identity this
	// object does not have, and this estate's own record still names THIS
	// object as the one the address owns right now. The address is on its
	// way to a replace and this object is the one being replaced, so there
	// is nothing to report - and nothing to vouch for either, because the
	// object the projection reads at that address is the one the
	// configuration names, which this sighting says nothing about.
	verdictIdentityChanging
)

// displacedFrom classifies one live object wearing a declared address's
// marker, and returns the identity the configuration computes for that
// address alongside, for the message.
//
// The asymmetry is deliberate: it answers [verdictDisplaced] only on
// evidence, and answers [verdictOwnObject] for every kind of doubt. See this
// file's doc comment for the six ways it declines to answer.
func (d *declared) displacedFrom(ctx context.Context, typeName, escaped string, c claimant) (want string, verdict addressVerdict) {
	entry := d.all[typeName][escaped]
	if entry == nil || entry.ambiguous {
		return "", verdictOwnObject
	}
	res := entry.res
	if res.Class != identity.ClassConcrete {
		return "", verdictOwnObject
	}

	want, mismatched := configIdentityMismatch(typeName, res, c)
	if !mismatched {
		return "", verdictOwnObject
	}
	// GitHub issue #885, and the sixth way this function declines to
	// report. A mismatch alone does not distinguish the two objects from
	// the one: see [recordOwners.namesClaimant].
	if d.records.namesClaimant(ctx, res.Addr, c) {
		return want, verdictIdentityChanging
	}
	return want, verdictDisplaced
}

// configIdentityMismatch is displacedFrom's comparison proper, split out so
// that the record's second opinion above reads as the separate question it
// is: this answers "does the configuration compute a different identity than
// the one this live object carries", which is true of a displaced object and
// equally true of an object a ForceNew replace is about to supersede.
//
// want is the identity the configuration computes, for the message.
func configIdentityMismatch(typeName string, res identity.Resolution, c claimant) (want string, mismatched bool) {
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

// recordOwners answers, for one declared address, whether a live object is
// the object this estate's own record says that address owns RIGHT NOW.
//
// # Why displacedFrom needs a second opinion at all (GitHub issue #885)
//
// The comparison above sees two strings: the identity the configuration
// computes for an address, and the identity the cloud attached to the object
// wearing that address's marker. They disagree in two situations that are
// indistinguishable from those two strings alone:
//
//   - the object is a leftover - a renumbering moved this address's identity
//     onto a different live object and this one kept the marker. Two objects
//     answer to one address, and that is #244's finding.
//   - the address is about to be REPLACED. A ForceNew argument changed, so
//     the configuration now computes the identity of the object the next
//     apply will create, while the live object still carries the identity -
//     and the marker - of the object that apply will destroy. Exactly one
//     live object exists, the plan for that address is correct, and there is
//     nothing to report.
//
// Measured on corpus-giantswarm-crossplane's day2_replace: an ordinary
// single-claimant replace of aws_iam_role produced this warning claiming
// "two different live resources answer to one address" while the same plan,
// correctly, proposed the replace.
//
// # What settles it
//
// The foundation-order ruling (#388) item 1 makes the estate's own record
// authoritative for "which live object does this address own right now", and
// [projection.WriteBack] writes it on every apply for ordinary taggable
// instances as well as record-backed ones. So a record naming THIS object is
// the estate's own statement that this object is the address's, whatever the
// configuration has since been changed to compute. The mismatch is then a
// pending change to the address, which the ordinary diff owns, and not a
// second claimant, which is the only thing this pass reports.
//
// A record naming a DIFFERENT object leaves the finding exactly as it was:
// the record agrees this object is not the address's. So does no record at
// all, a record that cannot be read, and a store that was never configured -
// every doubt keeps today's behaviour, which is the asymmetry this file's
// doc comment describes and the reason a false positive here still costs one
// warning line.
//
// This is [pruneSupersededClaimants]'s record-first discipline over the
// population that pass cannot reach - a single claimant, at a concrete
// client-named address - and it shares that pass's comparison
// ([claimantMatchesRecord]) rather than growing a second one, so #879's
// SecondaryID and the composite-component path apply here unchanged. No
// resource type name appears in it.
type recordOwners struct {
	store *projection.RecordStore
	// memo is one entry per address consulted, nil where the store holds no
	// readable current identity. The reads are lazy and rare by
	// construction: only a positive identity mismatch asks a question here.
	memo map[string]*projection.LocatedRecord
}

// newRecordOwners opens req's record store for [declared.displacedFrom]'s
// use, or returns nil when the run has no record store - in which case every
// question below is answered "the record says nothing", the pre-#885
// behaviour.
func newRecordOwners(req Request) *recordOwners {
	if req.HintStore == nil {
		return nil
	}
	prefix := req.KeyPrefix
	if prefix == "" {
		prefix = projection.RecordKeyPrefix(req.Estate)
	}
	store := projection.NewRecordEnvelopeStore(req.HintStore, prefix)
	if store == nil {
		return nil
	}
	return &recordOwners{store: store, memo: make(map[string]*projection.LocatedRecord)}
}

// namesClaimant reports whether the estate's current-identity record for
// addr names c. False for a nil receiver, an address with no record, a
// record that cannot be read, and a record naming another object: see this
// type's doc comment for why every one of those is "not proved, so report".
func (r *recordOwners) namesClaimant(ctx context.Context, addr addrs.AbsResourceInstance, c claimant) bool {
	if r == nil || r.store == nil {
		return false
	}
	key := addr.String()
	rec, consulted := r.memo[key]
	if !consulted {
		got, _, _, found, err := r.store.GetIdentity(ctx, addr)
		if err == nil && found {
			cp := got
			rec = &cp
		}
		r.memo[key] = rec
	}
	if rec == nil {
		return false
	}
	return claimantMatchesRecord(*rec, c)
}

// displacedProblem is the finding [declared.displacedFrom] produces.
//
// It names both identities, because the operator's next question is always
// "which of these two objects did I mean", and neither string alone answers
// it. It proposes nothing: see this file's doc comment.
//
// # What the text may and may not claim (GitHub issue #885)
//
// This function is called from a scan, before any plan for the address
// exists, so it cannot see what the run proposes for that ADDRESS and must
// not describe it. The text it used to carry did both: it asserted "two
// different live resources therefore answer to one address" on evidence that
// only ever showed one, and it asserted "Nothing is proposed for this
// resource", which an operator reads as the address and which was false in
// exactly the case #885 measured - a replace was proposed and applied.
//
// So the claims are now scoped to what this pass actually establishes: the
// live OBJECT in hand is not the one the address's plan will be computed
// against, and this run binds, reads, changes and destroys nothing of it.
// Whether a second live object exists at the identity the configuration
// names is left as the open question it is - [declared.displacedFrom] does
// not look, and TestOwnershipAddress_displacedObjectNoOtherClaimant is the
// case where nothing is there at all. What the address itself is planned for
// is the ordinary diff's answer and is left to the plan output beside this
// warning.
func displacedProblem(req Request, typeName, escaped, want string, c claimant) Problem {
	return Problem{
		Kind:     ProblemDisplacedMarker,
		TypeName: typeName,
		Marker:   escaped,
		LiveIDs:  liveIDs(c.importID),
		Detail: fmt.Sprintf(
			"A live %s with identity %q carries estate %q and the address %q, but the identity this configuration computes for %s is %q, and this estate's own record does not name this object as the one that address owns. This live object is therefore not the one the address is planned against: the plan for %s is computed from the identity the configuration names, which is either a different live resource or nothing yet. That is most often what a renumbering leaves behind - deleting a middle element of a count list moves every later instance's identity down one while the live resources keep the markers they were stamped with. This run does nothing to the object named here: it is bound to no address, it is not read, not changed and not destroyed, and it will stay in the account until a human says which resource is which. Read the plan for %s beside this warning for what that address itself is proposed for. Use choudoufu live-mv, or a moved block, to say that a resource moved; remove this resource's markers to disown it.",
			typeName, c.displayID(), req.Estate, escaped, escaped, want, escaped, escaped),
	}
}
