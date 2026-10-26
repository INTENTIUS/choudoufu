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
	"github.com/intentius/choudoufu/internal/live/azsweep"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
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
	diags := narrowed.sweep(ctx, in.Request, in.Result)
	mark.settle(in, k.Cone)
	return diags
}

// sweepCone is the GCP leg for the cone's instances: one address search per
// declared instance of a cone type, per project.
func (g GCPAssetSweep) sweepCone(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	req, decl, res := in.Request, in.decl, in.Result
	if g.Searcher == nil {
		return diags
	}
	demand := g.googleDemand(decl)
	var want []string
	for address, entry := range demand.entries {
		if g.Cone[entry.res.Addr.Resource.Resource.Type] {
			want = append(want, address)
		}
	}
	if len(want) == 0 {
		return diags
	}
	sort.Strings(want)
	// Only the cone's types are refused when the search cannot run: the
	// demand's other types are not this run's to find.
	coneDemand := demand
	coneDemand.types = nil
	for _, t := range demand.types {
		if g.Cone[t] {
			coneDemand.types = append(coneDemand.types, t)
		}
	}

	searcher, narrow := g.Searcher.(gcpsweep.AddressSearcher)
	if !narrow {
		return diags.Append(coneDemand.unsearched(decl, res, "this run's Cloud Asset client cannot search by tofu-address, which live-plan -resource needs to stay off the estate-wide search"))
	}
	projects := GCPSweepProjectsIn(ctx, req.Config, g.Project, req.Resolutions)
	if len(projects) == 0 {
		return diags.Append(coneDemand.unsearched(decl, res, "no GCP project is known to search"))
	}
	var failed []string
	for _, address := range want {
		entry := demand.entries[address]
		typeName := entry.res.Addr.Resource.Resource.Type
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
		diags = diags.Append(coneDemand.unsearched(decl, res, "project "+strings.Join(failed, ", ")+" could not be searched"))
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
