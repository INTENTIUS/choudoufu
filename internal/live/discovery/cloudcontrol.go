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

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// cloudControlSource decides whether typeName can be enumerated through
// Cloud Control, and if so, which CFN type to list.
//
// This is the "absent but mapped+registry-listable" leg of the three-way
// enumeration-source selection issue #47 describes: [scanType] already
// covers "native list resource exists" (unchanged) before ever reaching
// here, and the caller's existing refusal covers "neither" when this
// returns false. A request that never configured CloudControl or Roster at
// all - every caller that predates this fallback - always gets false here,
// which is what keeps the existing refusal the only outcome for them.
func cloudControlSource(req Request, typeName string) (cfnType string, ok bool) {
	if req.CloudControl == nil || req.Roster == nil {
		return "", false
	}
	return req.Roster.EnumerationSource(typeName)
}

// scanTypeCloudControl is [scanType]'s Cloud Control counterpart: same
// contract (a scan row, claims filed against decl, problems and sweep gaps
// appended to res), different transport. It is reached only when the
// provider offers typeName no native list resource and
// [cloudControlSource] found a listable CFN type for it.
//
// Tag filtering here is unconditionally client-side - Cloud Control has no
// server-side tag filter at all, unlike the EC2-style filter blocks
// [supportsTagFilter] looks for on the provider side - via whichever of the
// two paths issue #47 specifies: a listed ResourceDescription's own
// Properties.Tags when Cloud Control sent one, or one GetResource per
// candidate to refine when it did not. The refinement count rides on
// [TypeScan.Refined] rather than staying invisible, and is logged per call
// at [DEBUG] the same way [scanType]'s own client-side fallback is.
func scanTypeCloudControl(ctx context.Context, req Request, decl *declared, typeName, cfnType string, res *Result, sweep bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	scan := TypeScan{
		TypeName:  typeName,
		Declared:  len(decl.types[typeName]),
		Sweep:     sweep,
		Source:    SourceCloudControl,
		CFNType:   cfnType,
		Filtering: FilterClientSide,
		Scope:     ScopeAll,
		FilterReason: fmt.Sprintf(
			"%s has no native provider list resource; Cloud Control's ListResources on %s has no server-side tag filter at all",
			typeName, cfnType),
	}

	if sweep && !req.Roster.Taggable(cfnType) {
		res.Scans = append(res.Scans, scan)
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: typeName,
			Reason:   SweepGapNotTaggable,
			Detail: fmt.Sprintf(
				"live/registry.json records %s (Cloud Control type %s) as untaggable, so it can carry no ownership marker and the sweep has nothing to search on.",
				typeName, cfnType),
		}))
	}

	descs, err := req.CloudControl.ListResources(ctx, cfnType)
	if err != nil {
		res.Scans = append(res.Scans, scan)
		if sweep {
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapListFailed,
				Detail: fmt.Sprintf(
					"Cloud Control ListResources on %s (for %s) failed, so the sweep could not look for resources of that type which this estate owns but no longer declares: %s.",
					cfnType, typeName, err),
			}))
		}
		decl.unscanned[typeName] = true
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemListFailed,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"Cloud Control ListResources on %s failed, so nothing of %s could be discovered: %s.",
				cfnType, typeName, err),
		}))
	}
	scan.Listed = len(descs)
	if sweep {
		res.SweepCovered = append(res.SweepCovered, typeName)
	}

	log.Printf("[DEBUG] stateless/discovery: listing %s via Cloud Control (%s), %d resources, client-side tag filtering (Cloud Control offers no server-side filter)", typeName, cfnType, len(descs))

	for _, desc := range descs {
		tags, taggable, refined := cloudControlTags(ctx, req, cfnType, desc)
		if refined {
			scan.Refined++
			log.Printf("[DEBUG] stateless/discovery: %s identifier %q carried no Tags in its Cloud Control listing; refined with GetResource", typeName, desc.Identifier)
		}

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

		if !taggable {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemNoTags,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"Cloud Control listed a %s (Cloud Control type %s) with no Tags in its Properties, and refining it with GetResource found none either, so its ownership markers cannot be read.",
					typeName, cfnType),
			}))
			continue
		}

		estate := tags[TagEstate]
		switch {
		case estate == "":
			if sweep {
				continue
			}
			scan.Unclaimed++
			res.Unclaimed = append(res.Unclaimed, UnclaimedResource{
				TypeName:     typeName,
				ImportID:     importID,
				IdentityAttr: "id",
				Tags:         tags,
			})
			continue
		case estate != req.Estate:
			scan.OtherEstate++
			continue
		}

		raw := tags[TagAddress]
		escaped := EscapeAddress(raw)
		if !ValidMarkerAddress(escaped) {
			what := "carries no tofu-address tag"
			if raw != "" {
				what = fmt.Sprintf("carries the tofu-address value %q, which is not a well-formed escaped address", raw)
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				Marker:   raw,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s (via Cloud Control) claims estate %q but %s. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
					typeName, req.Estate, what),
			}))
			continue
		}

		if markerType := markerTypeOf(escaped); markerType != typeName {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				Marker:   raw,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s (via Cloud Control) claims estate %q and carries the tofu-address value %q, which names a %s rather than a %s. A marker names the resource it is written on (see live/MARKERS.md). Retag the resource with its own address, or remove the marker to disown it.",
					typeName, req.Estate, raw, markerType, typeName),
			}))
			continue
		}

		c := claimant{
			importID:     importID,
			identityAttr: "id",
			identity:     cty.NilVal,
			marker:       raw,
			escaped:      escaped,
			normalized:   escaped != raw,
			slot:         tags[TagSlot],
			tags:         tags,
			noIdentity:   importID == "",
		}

		if entry, ok := decl.types[typeName][escaped]; ok {
			entry.claimants = append(entry.claimants, c)
			continue
		}
		if decl.declares(typeName, escaped) {
			continue
		}
		if cb := countBlockFor(decl.counts[typeName], escaped); cb != nil {
			cb.extra = append(cb.extra, c)
			continue
		}
		if blk, ok := decl.blocks[typeName][escaped]; ok && blk.keyed {
			blk.claimants = append(blk.claimants, c)
			continue
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:     typeName,
			ImportID:     importID,
			IdentityAttr: "id",
			Marker:       raw,
			Normalized:   escaped,
			Slot:         tags[TagSlot],
			Tags:         tags,
			Swept:        sweep,
		})
	}

	res.Scans = append(res.Scans, scan)
	return diags
}

// cloudControlTags reads a listed resource's tags, filtering client-side
// (issue #47): from the ResourceDescription's own Properties.Tags when
// Cloud Control sent one alongside the listing, or - the bounded fallback -
// from one GetResource call when it did not, using the same
// UnsupportedOperation-tolerant fallback the composite-identifier path and
// every other Cloud Control read in this fork uses
// ([cloudcontrol.GetResourceByIdentity]).
//
// refined reports whether the second path was taken, so the caller can
// count and log the cost rather than hide it. taggable is false when
// neither the listing nor the refinement produced a Tags property at all -
// [markers.TagsOf]'s "this object has no tags attribute" case, translated
// to Cloud Control's Properties map.
func cloudControlTags(ctx context.Context, req Request, cfnType string, desc cloudcontrol.ResourceDescription) (tags map[string]string, taggable, refined bool) {
	if t, ok := ccPropertiesTags(desc.Properties); ok {
		return t, true, false
	}

	full, err := cloudcontrol.GetResourceByIdentity(ctx, req.CloudControl, cfnType, desc.Identifier)
	if err != nil || full == nil {
		// A refinement that failed or came back empty is reported as
		// untaggable rather than retried or guessed at: the caller's
		// ProblemNoTags path says so to an operator, which is the same
		// honesty [markers.TagsOf] gives a native-path resource whose
		// object came back with no tags attribute at all.
		return nil, false, true
	}
	t, ok := ccPropertiesTags(full.Properties)
	return t, ok, true
}

// ccPropertiesTags reads a CFN Tags property out of a Cloud Control
// Properties map, in the shape Cloud Control actually sends it: a JSON
// array of {"Key": ..., "Value": ...} objects. ok is false only when the
// Tags key is absent entirely - a Properties map that carries Tags as an
// empty list is a real "tagged with nothing", the same distinction
// [markers.TagsOf] draws for the native path.
func ccPropertiesTags(props map[string]any) (map[string]string, bool) {
	if props == nil {
		return nil, false
	}
	raw, ok := props["Tags"]
	if !ok {
		return nil, false
	}
	list, ok := raw.([]any)
	if !ok {
		// Present but not the list-of-Key/Value shape this fork's admitted
		// types use: reported as "no tags this client can read" rather than
		// guessed at.
		return nil, false
	}
	out := make(map[string]string, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		k, _ := m["Key"].(string)
		v, _ := m["Value"].(string)
		if k == "" {
			continue
		}
		out[k] = v
	}
	return out, true
}

// resolveCloudControlImportID turns a Cloud Control ListResources
// identifier into the TF import identity, enforcing the composite rule
// issue #47 states in one sentence: Cloud Control's "|"-joined identifier is
// not a TF import string, so a multi-part identifier is always composed
// through the identity table's Components and never handed out raw.
//
// A single-part identifier (no "|" at all - every server-assigned type in
// [identity.DefaultTable] today) needs no table entry: it is already the
// whole of what a native list result's identity attribute would have been,
// and is used directly, matching [importIdentity]'s "id" convention for the
// native path.
func resolveCloudControlImportID(typeName, identifier string) (string, bool) {
	parts := strings.Split(identifier, "|")
	if len(parts) == 1 {
		if identifier == "" {
			return "", false
		}
		return identifier, true
	}
	ti, ok := identity.LookupType(typeName)
	if !ok {
		return "", false
	}
	return composeCloudControlIdentifier(ti, parts)
}

// composeCloudControlIdentifier builds a TF import identity out of a Cloud
// Control identifier's ordered parts, walking ti.Components the same way
// the identity table already composes any other multi-attribute identity:
// a [identity.Component] with a Literal contributes it verbatim (a
// separator - "_", "/", ","), one with a Cloud value cannot be filled from
// parts at all (Cloud Control does not send the account ID or region
// positionally, and guessing at their position would be exactly the kind of
// guess this package refuses to make elsewhere), and every other component
// consumes the next unconsumed part in order.
//
// ok is false - refusing to compose, never falling back to the raw
// "|"-joined string - whenever a Cloud component is in the way, or the
// parts and the components do not account for each other one for one: both
// mean this type's table entry does not describe the identity Cloud Control
// actually sent, and papering over that with a guess is the one thing
// [DefaultTable]'s whole composition scheme exists to refuse to do (see
// identity/table.go's ComposesIdentity).
func composeCloudControlIdentifier(ti identity.TypeIdentity, parts []string) (string, bool) {
	if len(ti.Components) == 0 {
		return "", false
	}
	var b strings.Builder
	next := 0
	for _, c := range ti.Components {
		switch {
		case c.Literal != "":
			b.WriteString(c.Literal)
		case c.Cloud != identity.CloudNone:
			return "", false
		default:
			if next >= len(parts) {
				return "", false
			}
			b.WriteString(parts[next])
			next++
		}
	}
	if next != len(parts) {
		return "", false
	}
	return b.String(), true
}
