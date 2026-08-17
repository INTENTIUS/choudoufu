// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the binding leg GitHub issue #272 asks for: recognising a
// listed object by the name the configuration gave it, for the narrow
// population where AWS itself guarantees that name identifies at most one
// object in the account and region.
//
// # The rule it is an exception to, and why the exception holds
//
// internal/live/foreign matches a live object's content against a
// declaration and refuses, always, to bind on the result: a content match is
// "surfaced for explicit adoption and never bound automatically", because
// "inferring it from a content match would be exactly the guess the marker
// spec exists to forbid". Every word of that is right about content. Two
// objects can carry the same CIDR, the same policy document, the same
// anything, and choosing between them is a guess.
//
// A name AWS refuses to issue twice is not content. If the API will not
// create a second cache policy called "static-assets", then "the cache
// policy called static-assets" names exactly one object in the account, and
// reading it off a listing is not inference - it is reading the identity the
// configuration already states.
//
// # How the guarantee's absence fails closed
//
// Nothing here decides whether a type has the guarantee. That decision is
// made two layers up, out of two texts AWS wrote independently
// (tools/row-gen/uniquename.go crossing live/registry.json with
// live/import-grammar.json), and it arrives as
// [identity.TypeIdentity.UniqueName]. A type where the two sources do not
// agree carries no UniqueName, [uniqueNameIndexFor] returns nothing, and
// this leg never runs for it - it is not that the comparison fails, it is
// that no comparison is attempted.
//
// The same applies per instance. [identity.Resolve] populates
// [identity.Resolution.UniqueName] only for an instance whose name argument
// it could evaluate to a non-empty string from configuration alone; an
// instance whose name is unknown until apply carries none, is not in the
// index, and binds to nothing.
//
// # Multiple matches must never bind
//
// This is the load-bearing guard, and it is enforced on both sides of the
// comparison, because "several" can arrive from either:
//
//   - several LIVE objects carrying one declared name. AWS's guarantee says
//     this cannot happen; the guard exists because a guarantee this fork
//     read off a document is not a guarantee this fork can enforce, and
//     because the listing may span more than the guarantee's own scope if
//     the scope was read wrong. Diagnosed, never bound.
//   - several DECLARED instances stating one name. That is a configuration
//     that cannot work - the second apply would collide - but this pass is
//     not the one that says so, and binding one live object to two addresses
//     would be a much worse answer than refusing. Diagnosed, never bound.
//
// # What this leg deliberately does not do
//
// It never produces an orphan, an unclaimed resource, or a sweep candidate.
// A listed object matching no declared name is left entirely alone: it
// carries no ownership marker (the type has nowhere to put one), so nothing
// about it says this estate created it, and treating "unmarked and not
// declared" as "ours to destroy" for a whole type is the catastrophe the
// marker scheme exists to prevent. A sweep over such a type is refused
// before it lists anything, by scanTypeCloudControl's own untaggable check.
//
// # Why there is no end-to-end fixture for this leg
//
// There should be one, in the shape of live/e2e/per-element/run.sh: stand the
// estate up against floci, delete the state file, replan empty, and read the
// bound identity out of the run's own trace. It cannot be written against the
// pinned emulator, and the reason is worth recording so nobody spends another
// session finding it.
//
// live/floci-capabilities.json reports all four of this leg's types
// "implemented" with the evidence "ListResources(AWS::CloudFront::CachePolicy)
// succeeded". That is a claim about the CALL, not about what it answers.
// Measured 2026-08-17 against the pinned image
// ghcr.io/lex00/floci@sha256:a1c729f4...326d52e6bd5f:
//
//   - `aws cloudfront create-cache-policy` succeeds and returns an Id;
//   - `aws cloudfront list-cache-policies` then reports Quantity 0;
//   - `aws cloudcontrol create-resource` on AWS::CloudFront::CachePolicy
//     returns IN_PROGRESS;
//   - `aws cloudcontrol list-resources` on it returns an empty
//     ResourceDescriptions either way.
//
// AWS::Route53::CidrCollection behaves identically. So the emulator creates
// these objects and cannot enumerate them, which is precisely the one thing
// this leg needs from it: a fixture built on it would replan a create over
// the object it had just made, and would be measuring the emulator rather
// than this code.
//
// Until an emulator enumerates them, the coverage here is the unit tests in
// uniquename_test.go - which drive a fake Cloud Control endpoint through the
// real Discover path - plus TestIdentityGolden's four pinned rows. Neither is
// a cloud, and this comment is the record of that gap rather than a cover for
// it.

// uniqueNameIndex is one type's name-binding state for a single scan: where
// to read the name off a live object, which declared instances are waiting
// for one, and what was found.
type uniqueNameIndex struct {
	// path is [identity.UniqueName.Property] split into the segments a
	// reader descends through a Cloud Control Properties map.
	path []string

	// property is the same path unsplit, for messages.
	property string

	// declared maps a declared name to the instances stating it. A name with
	// more than one entry is a configuration this leg refuses to bind, and
	// keeping the slice rather than the first arrival is what lets it say
	// so.
	declared map[string][]*declaredEntry

	// found maps a declared name to the live objects carrying it, in listing
	// order. Only names some declaration is waiting for are recorded: a
	// listing of an account's cache policies is mostly other people's, and
	// remembering all of them would make the ambiguity check quadratic in
	// the account rather than in the configuration.
	found map[string][]uniqueNameCandidate
}

// uniqueNameCandidate is one listed object that carried a declared name.
type uniqueNameCandidate struct {
	importID   string
	identifier string
}

// uniqueNameIndexFor builds the index for one type, or reports that this
// type is not name-bindable and the ordinary marker path applies.
//
// ok tracks the TYPE, not the configuration: it is true for every type whose
// table entry carries a UniqueName, including one no declared instance
// resolved a name for. That distinction is what keeps such a type off the
// marker path, where it has nothing to carry a marker in and every listed
// object would raise ProblemNoTags - a diagnostic blaming the provider for
// the absence of something this fork already knew was absent. An index with
// no declared names binds nothing and diagnoses nothing, which is the honest
// outcome: this run cannot say what any of those instances is called.
func uniqueNameIndexFor(decl *declared, typeName string) (*uniqueNameIndex, bool) {
	ti, ok := identity.LookupType(typeName)
	if !ok || !ti.UniqueName.Set() {
		return nil, false
	}
	idx := &uniqueNameIndex{
		path:     ti.UniqueName.PropertyPath(),
		property: ti.UniqueName.Property,
		declared: map[string][]*declaredEntry{},
		found:    map[string][]uniqueNameCandidate{},
	}
	for _, entry := range decl.types[typeName] {
		if entry.res.UniqueName == "" {
			continue
		}
		idx.declared[entry.res.UniqueName] = append(idx.declared[entry.res.UniqueName], entry)
	}
	// decl.types is a map, so the arrival order of two instances sharing one
	// name is Go's, not the configuration's. Sorting by address makes the
	// ambiguity diagnostic below name them in a stable order, which is the
	// difference between a message a user can compare across runs and one
	// that shuffles.
	for name := range idx.declared {
		sort.Slice(idx.declared[name], func(i, j int) bool {
			return idx.declared[name][i].escaped < idx.declared[name][j].escaped
		})
	}
	return idx, true
}

// scanUniqueName is [scanTypeCloudControl]'s whole body for a name-bound
// type: read each listed object's name, then bind. It replaces the marker
// path for such a type rather than running beside it, because the type has
// no tags argument at all - see [uniqueNameIndexFor]'s doc for why running
// both would report the same absence twice and blame the wrong thing.
func scanUniqueName(req Request, decl *declared, typeName, cfnType string, descs []cloudcontrol.ResourceDescription, idx *uniqueNameIndex, scan *TypeScan, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	for _, desc := range descs {
		importID, idOK := resolveCloudControlImportID(typeName, desc.Identifier)
		if !idOK {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemUncomposableIdentifier,
				TypeName: typeName,
				LiveIDs:  liveIDs(desc.Identifier),
				Detail: fmt.Sprintf(
					"Cloud Control returned the multi-part identifier %q for a %s, and no entry in the identity table (internal/live/identity) can compose its parts into a TF import identity. The raw \"|\"-joined string is never used as a substitute; see live/MARKERS.md and internal/live/identity's Components.",
					desc.Identifier, typeName),
			}))
			continue
		}
		if _, ok := idx.observe(desc, importID); !ok {
			// Zero: this object carries nothing at the path the registry
			// said its name lives at. It is diagnosed rather than skipped
			// because a listing this leg cannot read is a listing that
			// proves nothing - the declared object may be sitting right
			// there, and reporting the instance as merely absent would let
			// the plan propose creating a second one.
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemUnreadableUniqueName,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"Cloud Control listed a %s (Cloud Control type %s, identifier %q) with no string at %s, which is where live/registry.json's own schema for that type says its name is. This resource type is recognised by that name and has no tags argument to carry an ownership marker, so nothing about this object can be compared against the configuration.",
					typeName, cfnType, desc.Identifier, idx.property),
			}))
			continue
		}
	}
	return diags.Append(idx.bind(req, typeName, scan, res))
}

// observe records one listed object against the index. ok is false when the
// object carried no readable name at the property path, which is the zero
// case the caller diagnoses: a listing this leg cannot read is a listing
// that proves nothing about whether the declared object exists.
func (idx *uniqueNameIndex) observe(desc cloudcontrol.ResourceDescription, importID string) (name string, ok bool) {
	name, ok = ccPropertyString(desc.Properties, idx.path)
	if !ok || name == "" {
		return "", false
	}
	if _, declared := idx.declared[name]; declared {
		idx.found[name] = append(idx.found[name], uniqueNameCandidate{importID: importID, identifier: desc.Identifier})
	}
	return name, true
}

// bind files the claims this index earned, once every listed object has been
// observed, and diagnoses everything it refuses to claim.
//
// A declared name with exactly one live object is bound. A name with more
// than one live object, or more than one declared instance, is diagnosed and
// bound to nothing. A name with no live object at all is left silent: that
// is an object this run has to create, which is the ordinary state of a
// first apply and not a refusal.
func (idx *uniqueNameIndex) bind(req Request, typeName string, scan *TypeScan, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	names := make([]string, 0, len(idx.declared))
	for name := range idx.declared {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		entries := idx.declared[name]
		found := idx.found[name]

		if len(entries) > 1 {
			var addrs []string
			for _, e := range entries {
				addrs = append(addrs, e.res.Addr.String())
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemAmbiguousUniqueName,
				TypeName: typeName,
				LiveIDs:  uniqueNameLiveIDs(found),
				Detail: fmt.Sprintf(
					"%s all set %s = %q, and AWS documents that name as identifying at most one %s in the account, so the configuration is asking for one live object to be several resources. Nothing was bound to any of them. Give each instance its own name.",
					strings.Join(addrs, ", "), uniqueNameArgOf(typeName), name, typeName),
			}))
			continue
		}

		switch len(found) {
		case 0:
			// Nothing in the account carries this name, so there is nothing
			// to bind and the plan will propose creating it. This is the
			// ordinary first-apply state; see this file's own doc comment
			// for why it is not a refusal.
			continue
		case 1:
			c := claimant{
				importID:     found[0].importID,
				identityAttr: "id",
				identity:     cty.NilVal,
				noIdentity:   found[0].importID == "",
			}
			entries[0].claimants = append(entries[0].claimants, c)
			scan.NameBound++
			log.Printf("[DEBUG] stateless/discovery: bound %s to the live %s named %q (Cloud Control identifier %q), read from %s; the name is documented unique per account and region",
				entries[0].res.Addr, typeName, name, found[0].identifier, idx.property)
		default:
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemAmbiguousUniqueName,
				TypeName: typeName,
				Addr:     entries[0].res.Addr,
				LiveIDs:  uniqueNameLiveIDs(found),
				Detail: fmt.Sprintf(
					"%d live resources of type %s carry the name %q at %s, and %s declares that name. AWS documents the name as identifying at most one such resource in an account and region, so this listing contradicts the guarantee this fork read it off - either the uniqueness is scoped more narrowly than the documentation says, or the listing spans more than one scope. Binding either object would be a guess, so nothing was bound.",
					len(found), typeName, name, idx.property, entries[0].res.Addr),
			}))
		}
	}
	_ = req
	return diags
}

// uniqueNameLiveIDs is the sorted identity list a problem reports.
func uniqueNameLiveIDs(found []uniqueNameCandidate) []string {
	if len(found) == 0 {
		return nil
	}
	out := make([]string, 0, len(found))
	for _, c := range found {
		if c.importID != "" {
			out = append(out, c.importID)
			continue
		}
		out = append(out, c.identifier)
	}
	sort.Strings(out)
	return out
}

// uniqueNameArgOf is the configuration argument a type's name-bind reads, for
// a message. It reads the table rather than assuming "name", so a type whose
// unique argument is spelled differently is described accurately.
func uniqueNameArgOf(typeName string) string {
	ti, ok := identity.LookupType(typeName)
	if !ok || len(ti.UniqueName.Attrs) == 0 {
		return "the name argument"
	}
	return ti.UniqueName.Attrs[0]
}

// ccPropertyString descends a Cloud Control Properties map along path and
// returns the string at the end of it.
//
// It is [ccPropertiesTags]' shape for a nested scalar rather than for the
// Tags list, and it is deliberately as strict: ok is false for an absent
// segment, for a segment that is not an object when the path continues
// through it, and for a leaf that is not a JSON string. Every one of those
// means "this object does not carry the property the registry said it
// carries", and a leg that guessed at a near-miss - stringifying a number,
// descending into a list - would be inventing the very value the bind is
// about.
func ccPropertyString(props map[string]any, path []string) (string, bool) {
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
	return s, ok
}
