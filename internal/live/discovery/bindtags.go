// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// This file is GitHub issue #266: the tag join that keeps a run from
// proposing to create a resource the estate already owns.
//
// # What goes wrong without it
//
// A needs-discovery instance is one nothing in the configuration names, so
// the only way to find its live object is to read an ownership marker off a
// listed object. Some list operations do not return tags. ec2:DescribeVpcs
// returns a TagSet; iam:ListRoles does not, and the AWS provider issues no
// per-member GetRole to fill the gap. The listed object therefore arrives
// with an empty tag map, its marker cannot be read, and the instance stays
// unbound - so the plan proposes creating it, apply creates a second live
// object carrying the first one's marker, and the next run does it again.
// One duplicate per run, no error, no convergence.
//
// # Why the fix is free
//
// The data is already on the wire. The estate-wide sweep makes one
// GetResources call against the Resource Groups Tagging API, filtered to
// this estate's tofu-estate tag, and that answer carries every owned
// resource's ARN together with its tags - including the tags the list call
// dropped. [sweepViaTagging] then discarded any result whose type the
// config-driven scan already covers, which is exactly the population that
// needed it.
//
// So: fetch that answer once, share it, and join its tags onto the listed
// objects the scan already has, by identifier.
//
// # Why by identifier, and not through the ARN join table
//
// [arnJoinTable] answers "which TF type is this ARN?" - a question with 876
// possible answers and a hand-curated table covering 26 of them. This join
// does not have to ask it. The scan is looking at objects of a type it
// already knows, because it just listed that type; all it needs is which
// tagged ARN is the same object as which listed one. That is a string
// comparison between the ARN's resource-id segment and the object's own
// import ID, and it is bounded by nothing.
//
// # Why it cannot bind the wrong object
//
// A wrong bind adopts somebody else's resource, which is worse than the
// defect this fixes, so the join is gated three ways and refuses whenever
// any of them is unsettled:
//
//   - The tagged resource has to carry a well-formed tofu-address whose own
//     type is the type being listed. A marker names the resource it is
//     written on (live/MARKERS.md), so a role's marker can never attach to a
//     user with the same name. This is [markerTypeOf], the same check
//     [scanTypeCloudControl] and [fileTaggingCandidate] already make.
//   - It has to carry this estate's tofu-estate. GetResources was filtered
//     on it, so this is belt and braces.
//   - Exactly one tagged resource may match. Two is ambiguity, and ambiguity
//     is reported rather than resolved by picking one.
//
// Identifiers are unique per type per account per region, and one
// [Discover] pass runs against one provider configuration with a Tagging
// client scoped to the same region, so a match that clears those three
// gates is the same object.
//
// # When it cannot fire
//
// TOFU_LIVE_CLOUDCONTROL=off leaves [Request.Tagging] nil, and a real
// account's tag index lags behind a write by minutes. Either way the join
// finds nothing and the run degrades to exactly what it did before, plus
// the finding [unreadableMarkerProblem] files when an instance goes unbound
// with unreadable objects of its type on the table. It never guesses.

// markerIndex is the estate-filtered Tagging API answer, fetched at most
// once per [Discover] and shared by the config-driven scan's tag join and
// by the estate-wide sweep.
//
// The fetch is lazy, and there is exactly one of it per pass. Every command
// in this fork runs discovery with Sweep set, so the call was already being
// made and this only moved it earlier and gave it a second reader - the
// sense in which #266's fix is free. A caller that sweeps not at all and
// lists an object with no readable marker does pay for a call that did not
// happen before, which is the one case where "no extra API call" is a claim
// about this fork's commands rather than about the package.
type markerIndex struct {
	client *cloudcontrol.Client
	estate string

	once   sync.Once
	tagged []cloudcontrol.TaggedResource
	err    error

	// objs is every fetched resource with its marker already read, in the
	// order GetResources returned them, and byKey indexes the same objects
	// by every string a listed object's import ID could equal. Both are
	// built by the same sync.Once as the fetch.
	objs  []markerObject
	byKey map[string][]markerObject
}

// markerObject is one tagged resource, with its marker already read.
type markerObject struct {
	arn  string
	tags map[string]string

	// markerType is the resource type the tofu-address names, and escaped
	// the address itself; both are empty when the resource carries no
	// well-formed marker. markerType is what makes a join type-safe without
	// an ARN join table.
	markerType string
	escaped    string
}

// newMarkerIndex builds the shared index for one discovery pass, or returns
// nil when this run has no Tagging client to fill it from - a nil index is
// usable and answers "not available" to everything, so no call site needs a
// nil check of its own.
func newMarkerIndex(req Request) *markerIndex {
	if req.Tagging == nil {
		return nil
	}
	return &markerIndex{client: req.Tagging, estate: req.Estate}
}

// resources returns the estate's tagged resources, making the one
// GetResources call the first time it is asked and reusing the answer after
// that. The error is remembered too: a failed call is not retried per type.
func (m *markerIndex) resources(ctx context.Context) ([]cloudcontrol.TaggedResource, error) {
	if m == nil {
		return nil, nil
	}
	m.once.Do(func() { m.fetch(ctx) })
	return m.tagged, m.err
}

func (m *markerIndex) fetch(ctx context.Context) {
	m.tagged, m.err = m.client.GetResources(ctx, nil, []cloudcontrol.TagFilter{
		{Key: TagEstate, Values: []string{m.estate}},
	})
	if m.err != nil {
		return
	}
	log.Printf("[DEBUG] stateless/discovery: tag index for estate %q holds %d resources", m.estate, len(m.tagged))
	m.objs, m.byKey = indexTagged(m.tagged)
}

// indexTagged reads each tagged resource's marker and indexes it by every
// string a listed object's import ID could equal. Separate from the fetch so
// that the whole of the join's decision-making can be tested against a
// literal answer, with no HTTP server standing in for the one thing this
// function does not do.
func indexTagged(tagged []cloudcontrol.TaggedResource) ([]markerObject, map[string][]markerObject) {
	objs := make([]markerObject, 0, len(tagged))
	byKey := make(map[string][]markerObject, len(tagged)*2)
	for _, tr := range tagged {
		obj := markerObject{arn: tr.ResourceARN, tags: tr.Tags}
		if raw, corrupt := GatherAddress(tr.Tags); !corrupt {
			if escaped := EscapeAddress(raw); ValidMarkerAddress(escaped) {
				obj.markerType, obj.escaped = markerTypeOf(escaped), escaped
			}
		}
		objs = append(objs, obj)
		for _, key := range markerJoinKeys(tr.ResourceARN) {
			byKey[key] = append(byKey[key], obj)
		}
	}
	return objs, byKey
}

// available reports whether the index was fetched successfully and can
// therefore answer a join at all. False means the run must fall back to
// what the list call said, which is the pre-#266 behavior.
func (m *markerIndex) available(ctx context.Context) bool {
	if m == nil {
		return false
	}
	_, err := m.resources(ctx)
	return err == nil
}

// settled reports whether the one GetResources call has already been made
// and answered, without making it. It is what a diagnostic asks after the
// scan is over, where triggering a network call to word a sentence would be
// absurd - and where the answer is already in: any object the scan could
// not read a marker off drove a [markerIndex.join], which fetches.
func (m *markerIndex) settled() bool {
	return m != nil && m.byKey != nil
}

// joinOutcome is what [markerIndex.join] decided.
type joinOutcome int

const (
	// joinUnavailable means there was no index to ask: no Tagging client,
	// or its one call failed. Not an answer about the object.
	joinUnavailable joinOutcome = iota

	// joinNone means the index was asked and holds no resource of this type
	// with this identifier. The object is genuinely not this estate's, as
	// far as the tag index can say.
	joinNone

	// joinAmbiguous means more than one tagged resource of this type
	// matched the identifier. Nothing in the data says which, so nothing is
	// bound.
	joinAmbiguous

	// joinBound means exactly one tagged resource matched and its tags are
	// the object's tags.
	joinBound
)

// join looks up the tags a listed object of typeName with import ID
// importID carries in the estate's tag index.
//
// It is only ever worth calling for an object whose own tags carry no
// tofu-estate: an object that already told the truth about itself needs no
// second opinion, and asking for one would let a stale index overrule a
// fresh list.
func (m *markerIndex) join(ctx context.Context, typeName, importID string) (map[string]string, joinOutcome) {
	if importID == "" || !m.available(ctx) {
		if importID == "" {
			// An object with no identity cannot be matched to an ARN. That
			// is a fact about the object, not about the index, but there is
			// nothing to report differently: it has its own problem
			// already (ProblemNoIdentity at bind time).
			return nil, joinNone
		}
		return nil, joinUnavailable
	}

	var matched []markerObject
	for _, obj := range m.byKey[markerJoinKey(importID)] {
		if obj.markerType != typeName {
			// Either no readable marker at all, or a marker naming another
			// type - which means the identifier collision is across types
			// and this is not the same object.
			continue
		}
		if obj.tags[TagEstate] != m.estate {
			continue
		}
		if len(matched) > 0 && matched[0].arn == obj.arn {
			// The same resource reached under two keys (its ARN and its
			// resource-id segment). One object, not an ambiguity.
			continue
		}
		matched = append(matched, obj)
	}

	switch len(matched) {
	case 0:
		return nil, joinNone
	case 1:
		return matched[0].tags, joinBound
	default:
		return nil, joinAmbiguous
	}
}

// matchedARNs is what [markerIndex.join] matched, for a diagnostic that has
// to name the resources it would not choose between.
func (m *markerIndex) matchedARNs(typeName, importID string) []string {
	if m == nil || importID == "" {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, obj := range m.byKey[markerJoinKey(importID)] {
		if obj.markerType != typeName || obj.tags[TagEstate] != m.estate || seen[obj.arn] {
			continue
		}
		seen[obj.arn] = true
		out = append(out, obj.arn)
	}
	return out
}

// marksAddress reports whether the index holds a resource of typeName
// carrying exactly this escaped marker address - the estate's own record
// that the resource exists, independent of what any list call returned.
//
// It is what separates "this address has no live resource, so create it" -
// the correct answer for a greenfield instance - from "this address has a
// live resource the run could not match to anything it listed", which is
// #266's shape and has to be said out loud rather than planned over.
func (m *markerIndex) marksAddress(typeName, escaped string) []string {
	if m == nil || escaped == "" {
		return nil
	}
	var out []string
	for _, obj := range m.objs {
		if obj.markerType == typeName && obj.escaped == escaped && obj.tags[TagEstate] == m.estate {
			out = append(out, obj.arn)
		}
	}
	sort.Strings(out)
	return out
}

// unreadableMarkerProblem is the finding [bind] files for a declared
// instance that went unbound while the run listed live resources of its
// type whose ownership markers it could not read.
//
// Two shapes, and the second is the stronger claim:
//
//   - The tag index holds a resource of this type marked for this exact
//     address, and no listed object matched it. The estate's own record
//     says the resource exists, so proposing to create it will produce a
//     second one carrying the same marker. Nothing here is a guess.
//   - The index could not settle it - it was unavailable (no Tagging
//     client, or its call failed), or it holds nothing for this address -
//     but N listed resources of this type came back unreadable. Either one
//     of them is this instance or none is, and the run cannot tell.
//
// Returns false when the type had nothing unreadable, which is the ordinary
// case and the one where a create is unambiguously right.
func unreadableMarkerProblem(req Request, decl *declared, typeName, escaped string, addr addrs.AbsResourceInstance) (Problem, bool) {
	if marked := req.markers.marksAddress(typeName, escaped); len(marked) > 0 {
		return Problem{
			Kind:     ProblemUnreadableMarker,
			TypeName: typeName,
			Addr:     addr,
			Marker:   escaped,
			LiveIDs:  marked,
			Detail: fmt.Sprintf(
				"Estate %q's tag index says a live %s already carries the ownership marker for %s, and no resource this run listed could be matched to it - so nothing bound to that address and the plan below proposes creating it. Applying that plan produces a second live resource carrying the same marker, which is the collision live/MARKERS.md describes. Either the list call for %s returns no tags and the identifier could not be joined (see internal/live/discovery/bindtags.go), or the resource was retagged out of band. Reconcile it with live-import before applying.",
				req.Estate, typeName, addr, typeName),
		}, true
	}

	n := decl.unreadable[typeName]
	if n == 0 {
		return Problem{}, false
	}
	how := "and this run's tag index could not be consulted to fill them in"
	if req.markers.settled() {
		how = "and the estate's tag index holds no marker for this address either"
	}
	return Problem{
		Kind:     ProblemUnreadableMarker,
		TypeName: typeName,
		Addr:     addr,
		Marker:   escaped,
		Detail: fmt.Sprintf(
			"Nothing bound to %s, so the plan below proposes creating it - but %d live %s resource(s) this run listed came back with no readable ownership marker, %s. If one of them is in fact this estate's %s, applying will create a second resource carrying the same marker rather than adopting the first. Check the resources reported as foreign below before applying; live-import binds one by hand.",
			addr, n, typeName, how, addr),
	}, true
}

// markerJoinKeys is every string a listed object's import ID could equal
// for this ARN to be the same object: the ARN itself, for the types whose
// identity IS their ARN, and the ARN's resource-id segment for every other
// type - which is what [resolveCloudControlImportID] composes an import ID
// from, and for a single-part identifier is the import ID unchanged.
func markerJoinKeys(arn string) []string {
	keys := []string{markerJoinKey(arn)}
	if a, ok := cloudcontrol.ParseARN(arn); ok && a.ResourceID != "" {
		if k := markerJoinKey(a.ResourceID); k != keys[0] {
			keys = append(keys, k)
		}
	}
	return keys
}

// markerJoinKey normalizes one identifier so that the two spellings AWS
// uses for the same thing land on one key.
//
// The only divergence that exists is the leading slash on a hierarchical
// name. An SSM parameter at /db/password has the ARN
// arn:aws:ssm:REGION:ACCOUNT:parameter/db/password, whose resource-id
// segment is "db/password", while its TF import ID is "/db/password" -
// the ARN's own "parameter/" prefix has eaten the separator that is part of
// the name. Trimming the prefix from both sides is symmetric, so it can
// only ever conflate two identifiers that differ by a leading slash, which
// for every AWS naming scheme is the same resource written two ways.
func markerJoinKey(id string) string {
	return strings.TrimPrefix(id, "/")
}
