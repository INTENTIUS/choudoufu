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

	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The Azure leg of the estate sweep (GitHub issue #1120, unit 11, under
// #1122's ruling of 2026-10-19). One Azure Resource Graph query per
// subscription the estate's resources resolve to (internal/live/azsweep),
// matching the tofu-estate tag case-insensitively; every resource that
// comes back carries this estate's marker, and is filed through the same
// per-resource rules the AWS tag sweep applies to a tagged candidate
// ([fileCandidate]): its tofu-address names a declared instance, which
// binds or vouches for it, or names none, which makes it an orphan that
// [classifyOrphans] and [applyOrphanPolicy] decide with no Azure rule of
// their own.
//
// Three things are Azure's own, and each is where it is because ARM is
// case-insensitive where AWS is not:
//
//   - The tags are read through markers.CanonicalARMMarkers, the arm-tags
//     surface's reader, so a resource whose configuration set Tofu-Estate
//     or Tofu-Address binds exactly as one tagged by the stamp does.
//   - The resource's ARM id is respelled to the declared instance's
//     spelling when the two differ only by case: Resource Graph returns ids
//     in the casing its index holds (resource group names lowercased, for
//     one), and [configIdentityMismatch] compares import ids exactly, so an
//     unrespelled id would read as a displaced object.
//   - A resource whose ARM id is a declared instance's, but whose marker
//     names an address the configuration does not declare, is that
//     instance's object carrying a stale marker, never an orphan: filing it
//     would propose destroying what a block still declares.
//
// Tombstones need nothing here. The family answers MarkerOutlivesObject
// true (Resource Graph lists a deleted resource for a while), so a destroy
// writes one, and [classifyOrphans] reads it for every orphan whose
// address another claimant shares, by import id: the ARM id this leg files.
//
// A controller-held resource (its ARM managedBy set, or a tag AKS writes
// on its node resource group) is the family's answer
// ([substrate.ControllerHeld] over [substrate.HoldEvidence]'s ManagedBy and
// ARMTags) and is reported held before any binding is tried: AKS copies the
// cluster's tags, the estate's marker among them, onto the node resource
// group it creates, so that group carries a declared address that is not
// its own.

// SourceResourceGraph marks a [TypeScan] the Resource Graph leg made.
const SourceResourceGraph EnumerationSource = "RESOURCE_GRAPH"

// ResourceGraphSweep is the leg ([substrate.SweepResourceGraph]) for an
// azurerm or azapi provider configuration (GitHub issue #1120).
//
// A run with several Azure provider configurations (aliases, other
// subscriptions) runs one discovery pass per configuration, and every pass
// is handed the same Coverage. Each subscription is swept by exactly one
// pass, because two passes filing the same orphan is a cross-provider
// collision to [Merge]: Subscriptions are the ones this pass owns,
// Elsewhere the ones another pass owns, and the one pass with
// TakesUnnamed set also sweeps every subscription a declared resource's
// ARM id names that no provider configuration names. Such a subscription
// has no client in Coverage, so it is reported uncovered by name: an
// incomplete sweep, never an empty one.
type ResourceGraphSweep struct {
	// Coverage is the client that queries each subscription, keyed by the
	// subscription id its provider block names.
	Coverage azsweep.Coverage
	// Subscriptions are the subscriptions this pass sweeps.
	Subscriptions []string
	// Elsewhere are subscriptions another pass of the same run sweeps.
	Elsewhere []string
	// TakesUnnamed: this pass also sweeps the subscriptions declared ARM ids
	// name and no provider configuration does.
	TakesUnnamed bool
}

// Leg is [substrate.SweepResourceGraph].
func (ResourceGraphSweep) Leg() substrate.Sweep { return substrate.SweepResourceGraph }

// Sweep runs the leg.
func (g ResourceGraphSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, res, decl := in.Request, in.Result, in.decl
	subs := g.subscriptions(req.Resolutions)
	if len(subs) == 0 {
		return diags
	}

	byID := declaredARMIDs(req.Resolutions)
	declaredAddrs := make(map[string]bool, len(req.Resolutions))
	for _, r := range req.Resolutions {
		declaredAddrs[r.Addr.String()] = true
	}

	complete := true
	covered := map[string]bool{}
	for _, sr := range azsweep.Sweep(ctx, g.Coverage, subs, req.Estate) {
		if sr.Err != nil {
			complete = false
			diags = diags.Append(resourceGraphFailureDiag(req.Estate, sr))
			res.SweepGaps = append(res.SweepGaps, SweepGap{
				TypeName: substrate.Azure.Name(),
				Reason:   SweepGapListFailed,
				Detail:   fmt.Sprintf("subscription %s was not swept: %s", sr.Subscription, sr.Err),
			})
			continue
		}
		res.Scans = append(res.Scans, TypeScan{
			TypeName:  substrate.Azure.Name(),
			Filtering: FilterServerSide,
			Scope:     ScopeEstate,
			Sweep:     true,
			Source:    SourceResourceGraph,
			Listed:    len(sr.Resources),
		})
		log.Printf("[DEBUG] live/discovery: Resource Graph found %d resource(s) of estate %q in subscription %s", len(sr.Resources), req.Estate, sr.Subscription)
		for _, hit := range sr.Resources {
			typeName, filed := g.fileHit(ctx, req, decl, res, hit, byID, declaredAddrs, &diags)
			if filed {
				covered[typeName] = true
			}
		}
	}

	// What an empty removal list is a statement about: every Azure type the
	// configuration declares, plus every type a marker named, and only when
	// every subscription answered.
	if complete {
		for _, r := range req.Resolutions {
			if sub, ok := substrate.ForProvider(r.Addr.Resource.Resource.ImpliedProvider()); ok && sub.Sweep() == substrate.SweepResourceGraph {
				covered[r.Addr.Resource.Resource.Type] = true
			}
		}
		types := make([]string, 0, len(covered))
		for t := range covered {
			types = append(types, t)
		}
		sort.Strings(types)
		res.SweepCovered = append(res.SweepCovered, types...)
	}
	return diags
}

// fileHit is one listed resource: held, bound by its ARM id under a stale
// marker, or handed to [fileCandidate]. It returns the type it filed the
// resource under, and false for a held resource or one with no marker
// address to name a type by.
func (g ResourceGraphSweep) fileHit(ctx context.Context, req Request, decl *declared, res *Result, hit azsweep.Resource, byID map[string]identity.Resolution, declaredAddrs map[string]bool, diags *tfdiags.Diagnostics) (string, bool) {
	tags := markers.CanonicalARMMarkers(hit.Tags)
	raw, _ := GatherAddress(tags)
	escaped := EscapeAddress(raw)
	addr, addressable := UnescapeAddress(escaped)
	typeName, named := hit.Type, false
	if ValidMarkerAddress(escaped) && addressable {
		typeName, named = addr.Resource.Resource.Type, true
	}

	if hold, held := substrate.ControllerHeld(substrate.HoldEvidence{ManagedBy: hit.ManagedBy, ARMTags: hit.Tags}); held {
		h := ControllerHeldResource{
			TypeName:    typeName,
			ImportID:    hit.ID,
			DisplayName: hit.Name,
			Controller:  hold.Controller,
			HeldBy:      hold.HeldBy,
		}
		if named && !declaredAddrs[addr.String()] {
			h.Marked, h.Addr = true, addr
		}
		res.ControllerHeld = append(res.ControllerHeld, h)
		sortControllerHeld(res.ControllerHeld)
		return typeName, false
	}

	importID := hit.ID
	if d, ok := byID[strings.ToLower(hit.ID)]; ok {
		importID = d.ImportID
		if named && !declaredAddrs[addr.String()] {
			// The object a declared instance names, carrying the marker of
			// an address the configuration no longer declares: a block
			// renamed without a moved block, or two blocks that once
			// swapped. Its own instance reads it at its id, and the stamp
			// rewrites the marker on apply; an orphan here would be a
			// destroy of a declared object.
			log.Printf("[DEBUG] live/discovery: %s carries tofu-address %q but is %s's object by its ARM id; not an orphan", hit.ID, raw, d.Addr)
			return typeName, false
		}
	}

	before := len(res.Orphans)
	*diags = diags.Append(fileCandidate(ctx, req, decl, typeName, taggedCandidate{
		importID:     importID,
		identityAttr: "id",
		tags:         tags,
	}, res, " (via Azure Resource Graph)", true))
	for i := before; i < len(res.Orphans); i++ {
		if res.Orphans[i].DisplayName == "" {
			res.Orphans[i].DisplayName = hit.Type + " " + hit.Name
		}
	}
	return typeName, named
}

// SweptSubscriptions is what this pass queries, in order: its own, then,
// for the pass that takes them, every subscription a declared resource's
// ARM id names that no provider configuration does, sorted. Compared
// case-insensitively, as ARM compares subscription ids. live-ls lists
// through it so that the inventory reads the subscriptions the plan does.
func (g ResourceGraphSweep) SweptSubscriptions(resolutions []identity.Resolution) []string {
	return g.subscriptions(resolutions)
}

func (g ResourceGraphSweep) subscriptions(resolutions []identity.Resolution) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range g.Subscriptions {
		if k := strings.ToLower(s); s != "" && !seen[k] {
			seen[k] = true
			out = append(out, s)
		}
	}
	if !g.TakesUnnamed {
		return out
	}
	for _, s := range g.Elsewhere {
		seen[strings.ToLower(s)] = true
	}
	var unnamed []string
	for _, r := range resolutions {
		s, ok := ARMSubscription(r.ImportID)
		if !ok || seen[strings.ToLower(s)] {
			continue
		}
		seen[strings.ToLower(s)] = true
		unnamed = append(unnamed, s)
	}
	sort.Strings(unnamed)
	return append(out, unnamed...)
}

// ARMSubscription is the subscription an ARM id names: the segment after
// its leading /subscriptions/, matched case-insensitively. False for an id
// that is not an ARM id.
func ARMSubscription(id string) (string, bool) {
	const prefix = "/subscriptions/"
	if len(id) <= len(prefix) || !strings.EqualFold(id[:len(prefix)], prefix) {
		return "", false
	}
	rest := id[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// declaredARMIDs is every resolution whose import id is an ARM id, keyed by
// that id lowercased. An id two resolutions share is left out: which
// instance it belongs to is the identity layer's duplicate-identity
// question, not this leg's to answer.
func declaredARMIDs(resolutions []identity.Resolution) map[string]identity.Resolution {
	out := map[string]identity.Resolution{}
	dup := map[string]bool{}
	for _, r := range resolutions {
		if r.Undeclared || r.ImportID == "" {
			continue
		}
		if _, ok := ARMSubscription(r.ImportID); !ok {
			continue
		}
		k := strings.ToLower(r.ImportID)
		if prev, ok := out[k]; ok && prev.Addr.String() != r.Addr.String() {
			dup[k] = true
		}
		out[k] = r
	}
	for k := range dup {
		delete(out, k)
	}
	return out
}

// resourceGraphSummaries is every summary a failed subscription is reported
// under, keyed by azsweep's own constant and valued by the same words
// written out, so that internal/live/refusalscan sees each one here, where
// the diagnostic is raised. TestResourceGraphSummariesMatchAzsweep holds
// each key to its value.
var resourceGraphSummaries = map[string]string{
	azsweep.SummaryResourceGraphNotRegistered: "Microsoft.ResourceGraph not registered",
	azsweep.SummarySubscriptionUnreadable:     "Azure subscription not readable by the sweep",
	azsweep.SummaryResourceGraphThrottled:     "Azure Resource Graph throttled the sweep",
	azsweep.SummarySubscriptionUncovered:      "Azure subscription not covered by any provider block",
	azsweep.SummarySweepUnavailable:           "Azure sweep unavailable",
}

// ResourceGraphListingDiag is [resourceGraphFailureDiag] for a listing
// rather than a plan (live-ls): always a warning, since a listing is what
// could be read and proposes nothing, under the same summary and words.
func ResourceGraphListingDiag(estate string, sr azsweep.SubscriptionResult) tfdiags.Diagnostics {
	return resourceGraphDiag(estate, sr, true)
}

// resourceGraphFailureDiag is one failed subscription's diagnostic, its
// severity [SeverityForRefusal]'s: refused when the subscription has not
// registered Microsoft.ResourceGraph or Resource Graph throttled the query
// past what its headers allow, and an incomplete-sweep warning naming the
// subscription otherwise.
func resourceGraphFailureDiag(estate string, sr azsweep.SubscriptionResult) tfdiags.Diagnostics {
	return resourceGraphDiag(estate, sr, false)
}

func resourceGraphDiag(estate string, sr azsweep.SubscriptionResult, listing bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	summary := azsweep.Summary(sr.Err)
	var detail string
	switch summary {
	case azsweep.SummaryResourceGraphNotRegistered:
		detail = fmt.Sprintf("Subscription %s, which estate %q's resources resolve to, has not registered the %s resource provider, so the estate sweep cannot list what the estate marks there, and a plan made without it would propose no removal in that subscription however many blocks were deleted. Register it (az provider register --namespace %s --subscription %s) and plan again. %s.", sr.Subscription, estate, azsweep.ResourceProvider, azsweep.ResourceProvider, sr.Subscription, sr.Err)
	case azsweep.SummaryResourceGraphThrottled:
		detail = fmt.Sprintf("Azure Resource Graph kept throttling the query for subscription %s past the waits its answers allow, so what estate %q marks there is unknown this run. A plan made without it would propose no removal in that subscription, so none is made: plan again once the caller's Resource Graph quota has recovered. %s.", sr.Subscription, estate, sr.Err)
	case azsweep.SummarySubscriptionUnreadable:
		detail = fmt.Sprintf("The sweep's credential cannot read subscription %s, so this is an incomplete sweep and not an empty one: a resource estate %q marks there whose block was deleted is not proposed for removal until a run can read it. Grant the credential Reader on the subscription, or the resource groups the estate owns. %s.", sr.Subscription, estate, sr.Err)
	case azsweep.SummarySubscriptionUncovered:
		detail = fmt.Sprintf("A resource of estate %q names subscription %s in its ARM id, and no azurerm or azapi provider block of this configuration names it, so no credential was set to query it: an incomplete sweep, not an empty one. A resource the estate marks there whose block was deleted is not proposed for removal. Add a provider block (an alias will do) whose subscription_id is %s.", estate, sr.Subscription, sr.Subscription)
	default:
		detail = fmt.Sprintf("Querying Azure Resource Graph for subscription %s failed, so this is an incomplete sweep and not an empty one: a resource estate %q marks there whose block was deleted is not proposed for removal until a run can read it. %s.", sr.Subscription, estate, sr.Err)
	}
	sev := tfdiags.Warning
	if !listing && SeverityForRefusal(resourceGraphSummaries[summary]) == SeverityError {
		sev = tfdiags.Error
	}
	return diags.Append(tfdiags.Sourceless(sev, resourceGraphSummaries[summary], detail))
}
