// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The cone legs (GitHub issue #2260). live-plan -resource plans one
// resource and what it reads, and finds a server-assigned instance in that
// cone without the estate-wide sweep (live/PLAN-RESOURCE.md). On AWS the
// config-driven scan lists the instance's type through the provider. The
// Azure, GCP and Kubernetes providers list little or nothing, so their
// instances are found by the family's own leg, run for the cone's types
// alone, which is what the leg's Cone field asks for:
//
//   - Kubernetes lists the cone's kinds only, label-filtered to the estate
//     as the full leg lists every kind, and binds by natural key or by the
//     address annotation.
//   - GCP asks Cloud Asset Inventory for each cone instance's own
//     tofu-address label (gcpsweep.AddressSearcher), one search per
//     instance per project, where the full leg searches the estate label.
//   - Azure sends the Resource Graph query narrowed to the cone types' ARM
//     types (azsweep.TypeSearcher), one query per subscription.
//
// Every listing a cone leg makes is a [TypeScan] with Sweep false and a
// [ProgressEvent] with Sweep false, so the run's read account counts it as
// a scan of the type, and nothing the leg lists is filed as an orphan: a
// removal outside the cone is not this run's question, and the run plans
// no removal.

// coneMark is what a cone leg's Result held before the leg ran, so that
// [coneMark.settle] can keep the leg's scans and bindings and drop
// everything a cone read does not answer.
type coneMark struct {
	scans   int
	covered int
	orphans map[string]bool
}

func orphanKey(o OwnedResource) string {
	return o.TypeName + "\x00" + o.ImportID + "\x00" + o.Normalized
}

func markCone(res *Result) coneMark {
	m := coneMark{scans: len(res.Scans), covered: len(res.SweepCovered), orphans: make(map[string]bool, len(res.Orphans))}
	for _, o := range res.Orphans {
		m.orphans[orphanKey(o)] = true
	}
	return m
}

// settle turns the leg's new scans into the cone's scans of its own types,
// reports each through the request's progress, and drops the orphans and
// the sweep coverage the leg filed.
func (m coneMark) settle(in *SweepInput, cone map[string]bool) {
	res := in.Result
	kept := res.Scans[:m.scans]
	for _, scan := range res.Scans[m.scans:] {
		if !cone[scan.TypeName] {
			continue
		}
		scan.Sweep = false
		kept = append(kept, scan)
		coneProgress(in, scan.TypeName, scan.Listed)
	}
	res.Scans = kept
	m.dropFiled(res)
}

// dropFiled drops the orphans and the sweep coverage a cone leg filed.
func (m coneMark) dropFiled(res *Result) {
	res.SweepCovered = res.SweepCovered[:m.covered]
	orphans := res.Orphans[:0]
	for _, o := range res.Orphans {
		if m.orphans[orphanKey(o)] {
			orphans = append(orphans, o)
		}
	}
	res.Orphans = orphans
}

// coneProgress reports one cone listing as a scan of typeName.
func coneProgress(in *SweepInput, typeName string, listed int) {
	if in.typesScanned != nil {
		*in.typesScanned++
	}
	if in.resourcesFound != nil {
		*in.resourcesFound += listed
	}
	if in.Request.Progress == nil {
		return
	}
	ev := ProgressEvent{TypeName: typeName, Sweep: false}
	if in.typesScanned != nil {
		ev.TypesScanned = *in.typesScanned
	}
	if in.resourcesFound != nil {
		ev.ResourcesFound = *in.resourcesFound
	}
	in.Request.Progress(ev)
}

func sortedConeTypes(cone map[string]bool) []string {
	out := make([]string, 0, len(cone))
	for t, on := range cone {
		if on {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// sweepCone is the Kubernetes leg over the cone's kinds alone.
func (k KubernetesSweep) sweepCone(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	narrowed := k
	narrowed.Cone, narrowed.FieldGranular, narrowed.Types = nil, nil, nil
	for _, t := range k.Types {
		if k.Cone[t] {
			narrowed.Types = append(narrowed.Types, t)
		}
	}
	if !k.Cone[k.ManifestType] {
		narrowed.ManifestType = ""
	}
	if len(narrowed.Types) == 0 && narrowed.ManifestType == "" {
		return nil
	}
	mark := markCone(in.Result)
	bindings, gaps := len(in.Result.Bindings), len(in.Result.SweepGaps)
	diags := narrowed.sweep(ctx, in.Request, in.Result)
	mark.settle(in, k.Cone)
	if in.decl != nil {
		diags = diags.Append(k.claimCone(in, bindings, gaps))
	}
	return diags
}

// claimCone hands [bind] what the Kubernetes cone leg found for the cone's
// declared needs-discovery instances (INTENTIUS/choudoufu#2266). The leg
// [KubernetesSweep.ScansDeclared] the cone's types, so the config-driven scan
// did not list them, and an instance with no claimant would read as absent:
// each binding the leg made for such an instance (by its address annotation)
// becomes that instance's claimant instead, and bind binds it as it binds any
// other. A cone type whose kinds the leg could not all list (a gap filed from
// gaps on, or no scan at all) is left unscanned with a problem that says so,
// so the plan proposes no create for an object that may exist. bindings and
// gaps are the lengths of [Result.Bindings] and [Result.SweepGaps] before the
// leg ran.
func (k KubernetesSweep) claimCone(in *SweepInput, bindings, gaps int) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	res, decl := in.Result, in.decl
	kept := res.Bindings[:bindings]
	for _, b := range res.Bindings[bindings:] {
		entry, ok := decl.entryFor(b.TypeName, EscapeAddress(b.Addr.String()))
		if !ok || !k.Cone[b.TypeName] {
			kept = append(kept, b)
			continue
		}
		entry.addClaimant(claimant{
			importID:       b.ImportID,
			identityAttr:   b.IdentityAttr,
			identityValues: b.IdentityValues,
			displayName:    b.DisplayName,
			marker:         b.Marker,
			escaped:        entry.escaped,
		})
	}
	res.Bindings = kept

	scanned := map[string]bool{}
	for _, s := range res.Scans {
		if k.Cone[s.TypeName] && s.Source == SourceKubernetes {
			scanned[s.TypeName] = true
		}
	}
	for _, g := range res.SweepGaps[gaps:] {
		if k.Cone[g.TypeName] {
			scanned[g.TypeName] = false
		}
	}
	for _, typeName := range sortedConeTypes(k.Cone) {
		if !k.ScansDeclared(typeName) || len(decl.types[typeName]) == 0 || scanned[typeName] {
			continue
		}
		decl.unscanned[typeName] = true
		diags = diags.Append(problemDiag(res, Problem{
			Kind:     ProblemTypeNotListable,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"The %d declared instance(s) of %s are found by the Kubernetes leg's label-selected list of the kinds the type manages, and the cluster did not list all of them this run (see the sweep gaps), so this run cannot tell which exist. Fix the list and plan again.",
				len(decl.types[typeName]), typeName),
		}))
	}
	return diags
}

// sweepCone is the GCP leg for the cone's instances. A covered type the leg
// can list through its own service (Lister, INTENTIUS/choudoufu#2264) is
// listed that way, at the scopes its cone instances name; every other cone
// instance is found by one address search per project.
func (g GCPAssetSweep) sweepCone(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, decl, res := in.Request, in.decl, in.Result
	if g.Searcher == nil {
		return diags
	}
	full := g.googleDemand(decl)
	demand := gcpDemand{entries: map[string]*declaredEntry{}, counts: map[string]*countBlock{}}
	for address, entry := range full.entries {
		if g.Cone[entry.res.Type()] {
			demand.entries[address] = entry
		}
	}
	for block, cb := range full.counts {
		if g.Cone[cb.typeName] {
			demand.counts[block] = cb
		}
	}
	for _, t := range full.types {
		if g.Cone[t] {
			demand.types = append(demand.types, t)
		}
	}
	if len(demand.entries) == 0 {
		return diags
	}

	// The per-type lists first: each list's answer is the only one about
	// its type, as in the estate leg.
	lists, listDiags := g.typeLists(ctx, req, decl, demand)
	diags = diags.Append(listDiags)
	listed := map[string]int{}
	for _, a := range lists.hits {
		address := a.Labels[markers.TagAddress]
		if entry, ok := demand.entries[address]; ok {
			entry.addClaimant(gcpClaimant(a, entry.res.Type(), address, entry.escaped))
			listed[entry.res.Type()]++
		}
	}
	for _, t := range sortedConeTypes(lists.listed) {
		res.Scans = append(res.Scans, TypeScan{TypeName: t, Filtering: FilterServerSide, Scope: ScopeEstate, Source: SourceGCPAssets, Listed: listed[t]})
		coneProgress(in, t, listed[t])
	}

	var want []string
	for address, entry := range demand.entries {
		if !lists.listed[entry.res.Type()] {
			want = append(want, address)
		}
	}
	if len(want) == 0 {
		return diags
	}
	sort.Strings(want)
	searcher, narrow := g.Searcher.(gcpsweep.AddressSearcher)
	if !narrow {
		return diags.Append(demand.unsearched(decl, res, "this run's Cloud Asset client cannot search by tofu-address, which live-plan -resource needs to stay off the estate-wide search", lists.listed))
	}
	projects := GCPSweepProjectsIn(ctx, req.Config, g.Project, req.Resolutions)
	if len(projects) == 0 {
		return diags.Append(demand.unsearched(decl, res, "no GCP project is known to search", lists.listed))
	}
	var failed []string
	for _, address := range want {
		entry := demand.entries[address]
		typeName := entry.res.Type()
		for _, project := range projects {
			assets, err := searcher.SearchAddress(ctx, project, req.Estate, address)
			coneProgress(in, typeName, len(assets))
			if err != nil {
				failed = append(failed, project)
				res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: typeName, Reason: SweepGapListFailed, Detail: fmt.Sprintf("project %s, tofu-address %s: %s", project, address, err)})
				continue
			}
			res.Scans = append(res.Scans, TypeScan{
				TypeName:  typeName,
				Filtering: FilterServerSide,
				Scope:     ScopeEstate,
				Source:    SourceGCPAssets,
				Listed:    len(assets),
				Declared:  1,
			})
			for _, a := range assets {
				entry.addClaimant(gcpClaimant(a, typeName, address, entry.escaped))
			}
		}
	}
	if len(failed) > 0 {
		sort.Strings(failed)
		diags = diags.Append(demand.unsearched(decl, res, "project "+strings.Join(failed, ", ")+" could not be searched", lists.listed))
	}
	return diags
}

// sweepCone is the Azure leg over the cone's ARM types: the Resource Graph
// query each subscription is sent, narrowed by type.
func (g ResourceGraphSweep) sweepCone(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, res, decl := in.Request, in.Result, in.decl

	var armTypes []string
	for _, t := range sortedConeTypes(g.Cone) {
		arm, ok := armtypes.AzurermARMType(t)
		if !ok {
			res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapNotListable,
				Detail: "no ARM type is known for this type, so the Resource Graph query cannot be narrowed to it; live-plan -resource does not widen it to the whole estate"})
			continue
		}
		if strings.EqualFold(arm, "Microsoft.Resources/resourceGroups") {
			// Resource Graph lists a resource group under its container
			// type, which is how [azsweep.Query]'s union names it.
			arm = "microsoft.resources/subscriptions/resourcegroups"
		}
		armTypes = append(armTypes, strings.ToLower(arm))
	}
	if len(armTypes) == 0 {
		return diags
	}
	owned := g.subscriptions(req.Resolutions)
	subs := g.queried(owned)
	if len(subs) == 0 {
		return diags
	}

	byID := declaredARMIDs(req.Resolutions)
	declaredAddrs := make(map[string]bool, len(req.Resolutions))
	for _, r := range req.Resolutions {
		declaredAddrs[r.Addr.String()] = true
	}

	mark := markCone(res)
	for _, sub := range subs {
		searcher := azureSearcherFor(g.Coverage, sub)
		narrow, ok := searcher.(azsweep.TypeSearcher)
		if !ok {
			for _, t := range sortedConeTypes(g.Cone) {
				res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapListFailed,
					Detail: fmt.Sprintf("subscription %s has no Resource Graph client that can narrow its query by type", sub)})
			}
			continue
		}
		hits, err := narrow.SearchTypes(ctx, sub, req.Estate, armTypes)
		if err != nil {
			diags = diags.Append(resourceGraphFailureDiag(req.Estate, azsweep.SubscriptionResult{Subscription: sub, Err: err}))
			for _, t := range sortedConeTypes(g.Cone) {
				res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapListFailed,
					Detail: fmt.Sprintf("subscription %s was not queried: %s", sub, err)})
				coneProgress(in, t, 0)
			}
			continue
		}
		listed := map[string]int{}
		for _, hit := range hits {
			if len(g.Family) != 0 && g.filer(req, decl, sub, hit) != g.Self.String() {
				continue
			}
			typeName, _ := g.fileHit(ctx, req, decl, res, hit, byID, declaredAddrs, &diags)
			listed[typeName]++
		}
		for _, t := range sortedConeTypes(g.Cone) {
			res.Scans = append(res.Scans, TypeScan{
				TypeName:  t,
				Filtering: FilterServerSide,
				Scope:     ScopeEstate,
				Source:    SourceResourceGraph,
				Listed:    listed[t],
			})
			coneProgress(in, t, listed[t])
		}
	}
	// fileHit files a hit no cone instance declares as an orphan; the cone
	// plans no removal, so those are dropped. The scans above are already
	// the cone's own.
	mark.dropFiled(res)
	return diags
}

// azureSearcherFor is the client cov assigns subscription, compared
// case-insensitively as [azsweep.Sweep] compares it.
func azureSearcherFor(cov azsweep.Coverage, subscription string) azsweep.Searcher {
	if s, ok := cov[subscription]; ok {
		return s
	}
	for k, s := range cov {
		if strings.EqualFold(k, subscription) {
			return s
		}
	}
	return nil
}
