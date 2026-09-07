// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is issue #272's "admission path 4 (list and content match)",
// internal/live/listclient/doc.go's own name for the mechanism it has
// always documented and never implemented until now - see that file's
// package doc for where this sits beside the marker path.
//
// It exists for exactly the population [identity.ContentMatchBinding]'s own
// doc comment names: a type the provider mints the identity of, server-side,
// and which carries no tags argument at all - so [MarkerlessTypes] would
// ordinarily veto it, because a marker is the only handle discovery's other
// legs have to find an object again after creating it. A type reaches
// [identity.ContentMatchTypes] only when its identity-bearing argument is
// proven unique within the account by two independent sources agreeing (see
// tools/row-gen's contentMatchRoster), which is what makes matching a live
// object's own property against that argument's declared value a read of
// the identity the configuration already states, rather than the guess
// internal/live/foreign's own content matcher deliberately refuses to make
// automatically.
//
// # The one absolute rule
//
// Zero matches leaves a declared instance unbound - the ordinary "nothing
// found yet, propose a create" outcome every other leg already gives. Ex
// exactly one match binds it, by appending a claimant to the declared
// entry and letting [bind] do what it already does with any other single
// claimant. Two or more matches is never resolved by picking one: it is
// reported as [ProblemAmbiguousContentMatch] and the instance is left with
// no claimant at all, the same shape [ProblemAmbiguousTagJoin] already
// gives the tag-based leg's own version of this question.
//
// # What this leg does not attempt
//
// A declared instance with a count or for_each key is left alone entirely -
// no claimant, no diagnostic - the same restriction
// internal/live/foreign/classify.go's matchTable holds itself to and for
// the same reason: [staticeval.Argument] evaluates one expression against
// the module's static scope, which has no per-instance each.key/each.value
// or count.index binding to evaluate against. Such an instance is left
// unbound, which a plan reads as "create" - correct for a genuinely new
// instance, and a known gap (not a guess) for one whose live object already
// exists under a for_each key this leg cannot resolve.

// scanTypeContentMatch is [scanType]'s content-match leg: reached only when
// [identity.ContentMatchTypes] carries a binding for typeName. It lists
// binding.CFNType through Cloud Control - the same transport
// [scanTypeCloudControl] uses, and [TypeScan.Source] is [SourceCloudControl]
// for the same reason: the wire call is identical, only what this function
// does with the results differs - and matches each NoKey declared instance
// by content instead of by ownership marker.
// collectUnclaimed is accepted only to keep this leg's signature identical
// to [scanTypeCloudControl]'s and [scanType]'s own dispatch to it; it
// changes nothing below. A content-match type carries no tags argument at
// all (that is the whole reason this leg exists - see the package doc
// comment), so "unclaimed" has no meaning for it regardless of why sweep is
// true: GitHub issue #388 edge 3's record-backed exception widens what a
// sweep call collects, never what a type with no marker surface can answer.
func scanTypeContentMatch(ctx context.Context, req Request, decl *declared, typeName string, binding identity.ContentMatchBinding, res *Result, sweep, collectUnclaimed bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	pathLabel := strings.Join(binding.PropertyPath, ".")
	scan := TypeScan{
		TypeName:  typeName,
		Declared:  len(decl.types[typeName]),
		Sweep:     sweep,
		Source:    SourceCloudControl,
		CFNType:   binding.CFNType,
		Filtering: FilterClientSide,
		Scope:     ScopeAll,
		FilterReason: fmt.Sprintf(
			"%s carries no tags argument at all, so no ownership marker can ever be read off it; each declared instance's own %s is matched against every live candidate's %s instead (issue #272)",
			typeName, binding.Argument, pathLabel),
	}

	if sweep {
		// A sweep asks "does the estate own a live object this
		// configuration no longer declares", which needs a marker to
		// answer - the same evidence every other untaggable type's sweep
		// refuses on. Content match only ever answers a different
		// question, "which DECLARED instance is this", so it has nothing
		// to contribute to a sweep and is refused the same way.
		res.Scans = append(res.Scans, scan)
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: typeName,
			Reason:   SweepGapNotTaggable,
			Detail: fmt.Sprintf(
				"A %s carries no tags, so it can carry no ownership marker and the sweep has nothing to search on. Content match (issue #272) only identifies a declared instance's own live object, never an undeclared one, so it does not help here either. Destroy a resource of this type before removing its block, or delete it out of band.",
				typeName),
		}))
	}

	descs, err := req.CloudControl.ListResources(ctx, binding.CFNType)
	if err != nil {
		res.Scans = append(res.Scans, scan)
		decl.unscanned[typeName] = true
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemListFailed,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"Cloud Control ListResources on %s failed, so nothing of %s could be discovered: %s.",
				binding.CFNType, typeName, err),
		}))
	}
	scan.Listed = len(descs)
	log.Printf("[DEBUG] stateless/discovery: listing %s via Cloud Control (%s) for content match on %s, %d resources",
		typeName, binding.CFNType, binding.Argument, len(descs))

	// Index every candidate by its own matched property value. A candidate
	// whose property is absent, not a string, or empty never matches
	// anything - the same "cannot read it, so it disqualifies rather than
	// wildcards" rule [staticeval.Argument] holds the declared side to.
	byValue := make(map[string][]cloudControlCandidate, len(descs))
	for _, d := range descs {
		v, ok := propertyPathValue(d.Properties, binding.PropertyPath)
		if !ok || v == "" {
			continue
		}
		byValue[v] = append(byValue[v], cloudControlCandidate{identifier: d.Identifier, value: v})
	}

	for _, escaped := range decl.order[typeName] {
		entry := decl.types[typeName][escaped]
		if entry.inCount {
			continue // the set matcher's business, not content match's - see doc comment
		}
		if entry.res.Addr.Resource.Key != addrs.NoKey {
			continue // count/for_each instance: no per-instance scope to evaluate the argument against, see doc comment
		}

		rc, ok := req.Config.Module.ManagedResources[entry.res.Addr.Resource.Resource.String()]
		if !ok {
			continue // discovery already refuses a resolutions/configuration mismatch before this runs
		}

		val, why := staticeval.Argument(ctx, req.Config.Module, rc, binding.Argument)
		if why != "" {
			log.Printf("[DEBUG] stateless/discovery: %s cannot be content-matched: %s", entry.res.Addr, why)
			continue
		}

		outcome := decideContentMatch(typeName, entry.res.Addr, escaped, binding.Argument, pathLabel, val, byValue[val])
		switch {
		case outcome.Claimant != nil:
			entry.claimants = append(entry.claimants, *outcome.Claimant)
		case outcome.Problem != nil:
			diags = diags.Append(problemDiag(res, *outcome.Problem))
		}
	}

	res.Scans = append(res.Scans, scan)
	return diags
}

// cloudControlCandidate is one listed candidate's own identifier and the
// value at [identity.ContentMatchBinding.PropertyPath] it carries.
type cloudControlCandidate struct {
	identifier string
	value      string
}

// contentMatchOutcome is what deciding one declared instance's content
// match produced: Claimant and Problem are mutually exclusive, and both nil
// means "not found yet" - the ordinary, unremarkable outcome that lets
// [bind] propose a create the same way it would for any other type.
type contentMatchOutcome struct {
	// Claimant is scanTypeContentMatch's own claimant to append to the
	// declared entry, set only when exactly one live candidate matched.
	Claimant *claimant

	// Problem is set on either refusal this leg can produce: an
	// uncomposable multi-part identifier, or two-or-more candidates
	// sharing the declared value. Never set alongside Claimant.
	Problem *Problem
}

// decideContentMatch is [scanTypeContentMatch]'s own binding decision,
// pulled out as a pure function - no Cloud Control client, no declared
// index, nothing but the values already read - so it can be tested
// directly against the shape issue #272's own verification asks for: zero
// matches (both fields nil), exactly one (Claimant set), two or more
// (Problem set, never a guess).
//
// matches is byValue[val] from [scanTypeContentMatch]'s own index -
// already narrowed to the candidates carrying exactly val, so this
// function only ever has to decide by count.
func decideContentMatch(typeName string, addr addrs.AbsResourceInstance, escaped, argument, pathLabel, val string, matches []cloudControlCandidate) contentMatchOutcome {
	switch len(matches) {
	case 0:
		return contentMatchOutcome{}
	case 1:
		m := matches[0]
		importID, idOK := resolveCloudControlImportID(typeName, m.identifier)
		if !idOK {
			return contentMatchOutcome{Problem: &Problem{
				Kind:     ProblemUncomposableIdentifier,
				TypeName: typeName,
				Addr:     addr,
				LiveIDs:  liveIDs(m.identifier),
				Detail: fmt.Sprintf(
					"Cloud Control returned the multi-part identifier %q for a %s content-matched by %s=%q, and no entry in the identity table can compose its parts into a TF import identity.",
					m.identifier, typeName, argument, val),
			}}
		}
		return contentMatchOutcome{Claimant: &claimant{
			importID:     importID,
			identityAttr: "id",
			identity:     cty.NilVal,
			escaped:      escaped,
			noIdentity:   importID == "",
		}}
	default:
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.identifier
		}
		return contentMatchOutcome{Problem: &Problem{
			Kind:     ProblemAmbiguousContentMatch,
			TypeName: typeName,
			Addr:     addr,
			LiveIDs:  liveIDs(ids...),
			Detail: fmt.Sprintf(
				"%d live %s resources all carry %s = %q, matching %s's own declared %s. Content match cannot tell which live object is %s's, so none was bound; nothing in this leg guesses.",
				len(matches), typeName, pathLabel, val, typeName, argument, addr),
		}}
	}
}

// propertyPathValue walks a Cloud Control Properties map along path,
// returning the string value at the end of it. Cloud Control decodes a
// resource's own JSON model into nested map[string]any the same way
// encoding/json always decodes an object, which is the shape
// [identity.ContentMatchBinding.PropertyPath] is written against - a
// one-element path for a top-level property, a two-element path for one
// wrapped in the resource's single mutable-config object (see that type's
// own doc comment).
//
// ok is false whenever the path does not resolve to a plain string: absent
// at any segment, a non-object in the way of a longer path, or a leaf that
// is not a string. None of those are guessed at - see [scanTypeContentMatch]
// for what an unresolved candidate becomes (excluded from matching, never a
// wildcard).
func propertyPathValue(props map[string]any, path []string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	var cur any = props
	for _, seg := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", false
		}
		cur, ok = m[seg]
		if !ok {
			return "", false
		}
	}
	s, ok := cur.(string)
	if !ok {
		return "", false
	}
	return s, true
}
