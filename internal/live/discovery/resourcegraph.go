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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/armtypes"
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
// Four things are Azure's own, three of them because ARM is
// case-insensitive where AWS is not:
//
//   - The tags are read through markers.CanonicalARMMarkers, the arm-tags
//     surface's reader, so a resource whose configuration set Tofu-Estate
//     or Tofu-Address binds exactly as one tagged by the stamp does.
//   - The resource's ARM id is respelled to the declared instance's
//     spelling when the two differ only by case: Resource Graph returns ids
//     in the casing its index holds (resource group names lowercased, for
//     one). [configIdentityMismatch] and the record comparison treat two
//     ARM ids equal but for case as one object (#2136), so the respelling
//     is for the import id the declared instance reads, not for them.
//   - An object whose marker names a declared address but whose ARM id is
//     not the one that address resolves to (an identity argument renamed,
//     the old object still standing) is a displaced marker, reported as AWS
//     reports it and vouching for nothing (#2136), so the projection reads
//     and judges the tags of whatever sits at the new id.
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
// A controller-held resource (its ARM managedBy naming a lifecycle owner,
// or a tag AKS writes on its node resource group) is the family's answer
// ([substrate.ControllerHeld] over [substrate.HoldEvidence]'s ManagedBy and
// ARMTags) and is reported held before a marker binding is tried: AKS
// copies the cluster's tags, the estate's marker among them, onto the node
// resource group it creates, so that group carries a declared address that
// is not its own. A declared instance's own ARM id still binds first: an
// object the configuration declares is never read as held (#2133).

// SourceResourceGraph marks a [TypeScan] the Resource Graph leg made.
const SourceResourceGraph EnumerationSource = "RESOURCE_GRAPH"

// ResourceGraphSweep is the leg ([substrate.SweepResourceGraph]) for an
// azurerm or azapi provider configuration (GitHub issue #1120).
//
// A run with several Azure provider configurations (azapi beside azurerm,
// aliases, other subscriptions) runs one discovery pass per configuration,
// and every pass is handed the same Coverage. Each subscription has one
// owning pass, the first in pass order whose block names it:
// Subscriptions are the ones this pass owns, Elsewhere the ones another
// pass owns, and the one pass with TakesUnnamed set also owns every
// subscription a declared resource's ARM id names that no provider
// configuration names. Such a subscription has no client in Coverage, so
// it is reported uncovered by name: an incomplete sweep, never an empty
// one. The owner reports a subscription's failures, and live-ls lists a
// subscription through its owner alone ([ResourceGraphSweep.SweptSubscriptions]).
//
// # Which pass files a resource (GitHub issue #1120, the lane's fourth run)
//
// A subscription's answer holds every resource carrying the estate's
// marker, azurerm's and azapi's alike, since both providers write the same
// ARM tags. Filed wholesale by the owning pass, an azapi pass that sorted
// first sighted the azurerm objects outside its scope, and the azurerm
// pass, handed that subscription as Elsewhere, queried nothing: [Merge]
// refused every azurerm object as unreachable by its own configuration
// (outofscope.go), and an azurerm orphan would have been destroyed through
// the azapi provider.
//
// So with Self and Family set, every resource is filed by exactly one pass
// ([ResourceGraphSweep.filer]), and every pass queries each subscription
// its own block names, not only the ones it owns. The query itself still
// runs once per subscription per plan: the command layer hands every pass
// one memoized client per subscription. A resource is filed by:
//
//   - the provider configuration that declares the instance its marker
//     names, when that configuration's block names the resource's
//     subscription. It binds or vouches for its own object, so no pass of
//     another provider type or alias ever sights it out of scope;
//   - otherwise, the first configuration naming the subscription whose
//     provider serves the marker's type (azurerm for an azurerm_ type,
//     azapi for azapi_resource), so an orphan is read and destroyed
//     through a provider that has its schema;
//   - otherwise, and for a resource whose marker names no address, the
//     subscription's owner, as before.
//
// A marked object in a subscription no configuration that declares its
// address names is still sighted only out of scope, and still refused.
//
// With Family empty (a single configuration, and this package's own
// tests) the leg files everything it lists.
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
	// Untaggable is every declared Azure type the run knows can carry no
	// ownership marker, each reported as a [SweepGapNotTaggable] gap
	// (GitHub issue #2119). internal/command classifies it, where the leg
	// is built: this package's Sweep methods share one name, so a surface
	// question asked here would read as asked by every leg.
	Untaggable []UntaggableType
	// Self is the provider configuration this pass runs through, and
	// Family every Azure provider configuration the run has a pass for,
	// Self among them, in pass order, each with the subscription its block
	// names. See the type's doc comment for how they choose which pass
	// files a resource.
	Self   addrs.AbsProviderConfig
	Family []FamilyConfig
}

// FamilyConfig is one Azure provider configuration of the run and the
// subscription its block names ("" when it names none).
type FamilyConfig struct {
	Provider     addrs.AbsProviderConfig
	Subscription string
}

// UntaggableType is one declared Azure type that can carry no marker:
// TypeName is how the gap names it (an azurerm type, or an azapi type with
// the ARM type and API version its block names), and Why finishes the
// sentence that begins with it ("carries no tags").
type UntaggableType struct {
	TypeName string
	Why      string
}

// Leg is [substrate.SweepResourceGraph].
func (ResourceGraphSweep) Leg() substrate.Sweep { return substrate.SweepResourceGraph }

// Sweep runs the leg.
func (g ResourceGraphSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, res, decl := in.Request, in.Result, in.decl
	untaggable := g.recordUntaggable(res)
	owned := g.subscriptions(req.Resolutions)
	subs := g.queried(owned)
	if len(subs) == 0 {
		return diags
	}
	ownedSet := make(map[string]bool, len(owned))
	for _, s := range owned {
		ownedSet[strings.ToLower(s)] = true
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
			if !ownedSet[strings.ToLower(sr.Subscription)] {
				// The owning pass asked the same memoized query and reports
				// its failure; a second report here would say it twice.
				continue
			}
			diags = diags.Append(resourceGraphFailureDiag(req.Estate, sr))
			res.SweepGaps = append(res.SweepGaps, SweepGap{
				TypeName: substrate.Azure.Name(),
				Reason:   SweepGapListFailed,
				Detail:   fmt.Sprintf("subscription %s was not swept: %s", sr.Subscription, sr.Err),
			})
			continue
		}
		var mine []azsweep.Resource
		for _, hit := range sr.Resources {
			if len(g.Family) == 0 || g.filer(req, decl, sr.Subscription, hit) == g.Self.String() {
				mine = append(mine, hit)
			}
		}
		res.Scans = append(res.Scans, TypeScan{
			TypeName:  substrate.Azure.Name(),
			Filtering: FilterServerSide,
			Scope:     ScopeEstate,
			Sweep:     true,
			Source:    SourceResourceGraph,
			Listed:    len(mine),
		})
		log.Printf("[DEBUG] live/discovery: Resource Graph found %d resource(s) of estate %q in subscription %s; this pass files %d", len(sr.Resources), req.Estate, sr.Subscription, len(mine))
		for _, hit := range mine {
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
			if !untaggable[t] {
				types = append(types, t)
			}
		}
		sort.Strings(types)
		res.SweepCovered = append(res.SweepCovered, types...)
	}
	return diags
}

// recordUntaggable files [ResourceGraphSweep.Untaggable] as
// [SweepGapNotTaggable] gaps, which [sweepGapDiag] keeps off the per-run
// warnings as it does AWS's: it is a standing fact about the type, true on
// nearly every Azure estate (an azurerm_subnet is enough), and a warning on
// every plan would bury the one about this run. It returns the gaps' type
// names, which the covered list then leaves out: the sweep finds nothing of
// a type that can hold no marker, so an empty answer for it says nothing.
//
// The issue #2119 ruling (D3): while the estate's record of such a resource
// survives, removing its block still plans the destroy, through the record
// leg (recordorphan_read.go). With the record lost the resource is left running
// unowned and no run can see it, which live/RECOVERY.md documents with its
// recovery.
func (g ResourceGraphSweep) recordUntaggable(res *Result) map[string]bool {
	names := map[string]bool{}
	for _, u := range g.Untaggable {
		if u.TypeName == "" || names[u.TypeName] {
			continue
		}
		names[u.TypeName] = true
		sweepGapDiag(res, SweepGap{
			TypeName: u.TypeName,
			Reason:   SweepGapNotTaggable,
			Detail: fmt.Sprintf(
				"%s %s, so it can carry no ownership marker and the Azure Resource Graph sweep has nothing to search on. Removing its block still plans the destroy while the estate's record of it survives; with that record lost the resource is left running unowned, and live/RECOVERY.md says how to restore the record's previous version.",
				u.TypeName, u.Why),
		})
	}
	return names
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

	// A declared instance's own ARM id binds before any hold is asked
	// (#2133 finding 2): an object the configuration declares is that
	// instance's, whatever its managedBy or tags say, and reading it as held
	// would drop it from every plan.
	d, declaredID := byID[strings.ToLower(hit.ID)]
	if !declaredID {
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
	}

	importID := hit.ID
	var refusedAt string
	if declaredID {
		importID = d.ImportID
		if named && declaredAddrs[addr.String()] && addr.String() != d.Addr.String() {
			// The object a declared instance names, carrying this estate's
			// marker for ANOTHER declared address: AKS's copy of a
			// cluster's markers onto the route table or public IP it was
			// given, most often. When d reads it at its own id it refuses
			// it by name (GitHub issue #2135), so the displaced marker
			// [fileCandidate] would file for the address the marker names
			// waits on that read (see [taggedCandidate.refusedAt]).
			//
			// Waits, rather than being dropped here: the refusal is likely,
			// not certain (GitHub issue #2140, finding 4). Nothing in
			// discovery vouches for d (the only object at d's id carries
			// another address, and one carrying d's address at any other
			// id is compared by ARM id and reported displaced, #2136), but
			// under -refresh=false the projection serves d from the cache
			// on its record envelope's word and reads no tags, and then
			// nothing refuses. The projection alone knows which happened,
			// and gives the warning when it did not refuse.
			refusedAt = d.Addr.String()
		}
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
		refusedAt:    refusedAt,
		objectType:   azureObjectType(hit.Type),
	}, res, " (via Azure Resource Graph)", true))
	for i := before; i < len(res.Orphans); i++ {
		if res.Orphans[i].DisplayName == "" {
			res.Orphans[i].DisplayName = hit.Type + " " + hit.Name
		}
	}
	return typeName, named
}

// queried is every subscription this pass sends the query for: the ones it
// owns, then each one its own block names that another pass owns, for the
// resources [ResourceGraphSweep.filer] gives this pass there. With no
// Family it is the owned ones alone.
func (g ResourceGraphSweep) queried(owned []string) []string {
	seen := make(map[string]bool, len(owned))
	for _, s := range owned {
		seen[strings.ToLower(s)] = true
	}
	out := append([]string(nil), owned...)
	for _, c := range g.Family {
		if c.Provider.String() != g.Self.String() || c.Subscription == "" || seen[strings.ToLower(c.Subscription)] {
			continue
		}
		seen[strings.ToLower(c.Subscription)] = true
		out = append(out, c.Subscription)
	}
	return out
}

// filer is the provider configuration, by its string, whose pass files hit,
// listed in subscription sub; see the type's doc comment for the rule.
// Every pass computes the same answer: Family and the declared set are the
// run's, not the pass's. "" is no pass, for a subscription no
// configuration names when this pass does not take those.
func (g ResourceGraphSweep) filer(req Request, decl *declared, sub string, hit azsweep.Resource) string {
	if hit.SubscriptionID != "" {
		sub = hit.SubscriptionID
	} else if s, ok := ARMSubscription(hit.ID); ok {
		sub = s
	}
	var namers []addrs.AbsProviderConfig
	for _, c := range g.Family {
		if c.Subscription != "" && strings.EqualFold(c.Subscription, sub) {
			namers = append(namers, c.Provider)
		}
	}
	if len(namers) == 0 {
		if g.TakesUnnamed {
			return g.Self.String()
		}
		return ""
	}

	raw, corrupt := GatherAddress(markers.CanonicalARMMarkers(hit.Tags))
	escaped := EscapeAddress(raw)
	addr, addressable := UnescapeAddress(escaped)
	if corrupt || !ValidMarkerAddress(escaped) || !addressable {
		return namers[0].String()
	}
	typeName := addr.Resource.Resource.Type
	if _, declaredBy, ok := declaredProvider(req, decl, typeName, escaped); ok {
		for _, n := range namers {
			if n.String() == declaredBy.String() {
				return n.String()
			}
		}
	}
	serves := addr.Resource.Resource.ImpliedProvider()
	for _, n := range namers {
		if n.Provider.Type == serves {
			return n.String()
		}
	}
	return namers[0].String()
}

// SweptSubscriptions is the subscriptions this pass owns, in order: its
// own, then, for the pass that takes them, every subscription a declared
// resource's ARM id names that no provider configuration does, sorted.
// Compared case-insensitively, as ARM compares subscription ids. live-ls
// lists through it so that the inventory reads the subscriptions the plan
// does, each once.
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

// azureObjectType names a listed object by its own type, for a message
// about the object rather than the address its marker names (GitHub issue
// #2138): the azurerm type that creates its ARM type, with the ARM type
// beside it, where exactly one azurerm type does
// ([armtypes.AzurermTypesForARMType]), and otherwise the ARM type alone, as
// Resource Graph listed it. An azapi_resource can create any ARM type, so
// the azurerm name is a name for the kind of object, never a claim about
// which block made it.
func azureObjectType(armType string) string {
	if armType == "" {
		return ""
	}
	tfTypes, spelled := armtypes.AzurermTypesForARMType(armType)
	if len(tfTypes) == 1 {
		return fmt.Sprintf("%s (%s)", tfTypes[0], spelled)
	}
	if spelled != "" {
		return spelled
	}
	return armType
}
