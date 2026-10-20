// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/gcpsweep"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The GCP leg of the estate sweep (GitHub issue #1119, under #1121's
// ruling): one Cloud Asset Inventory search per project the estate's
// google resources resolve to, for every resource carrying the estate's
// tofu-estate label.
//
// A hit is one of three things. A Compute VM or disk, or a Cloud Run
// service, that a GCP controller made from a declared resource and copied
// its labels onto is controller-held ([substrate.Google]'s
// ControllerHeld), reported and never removed. A hit whose tofu-address
// label encodes an address the configuration declares is that block's
// object. Anything else is an orphan, reported by its asset type and full
// resource name with the encoded address it carries: the label is matched,
// never decoded, so a hit naming a block the configuration no longer has
// cannot be turned back into an address here, and an orphan with no
// address is reported and never planned for removal ([OwnedResource]).
// Planning its removal is the record's job: a deleted block's record names
// its address, and the record-orphan leg proposes the destroy from it.
//
// The index is eventually consistent ([gcpsweep.Asset]): a resource
// created moments ago may be missing, and one deleted moments ago may
// still be listed. Neither changes a plan here, since nothing this leg
// files is removed on its say-so.

// SourceGCPAssets marks a [TypeScan] the GCP leg made.
const SourceGCPAssets EnumerationSource = "GCP_ASSET_INVENTORY"

// SummaryGCPSweepIncomplete is the warning for a project the GCP leg could
// not search, by name, with why: the Cloud Asset API is not enabled there,
// the credential lacks the search permission, a page failed part way, or
// no project is known to search. A gap in coverage, never a wrong plan.
const SummaryGCPSweepIncomplete = "GCP sweep incomplete"

// SummaryGCPOrphanUnaddressed is the warning naming each resource that
// carries this estate's label and an address label the configuration does
// not declare.
const SummaryGCPOrphanUnaddressed = "GCP resource carries this estate's label and no declared address"

// GCPAssetSweep is the leg ([substrate.SweepAssetSearch]) for one
// hashicorp/google provider configuration. Searcher is the client the
// provider block's credential built, and Project the provider block's own
// project, searched with every project the configuration's google
// resources name. A nil Searcher does nothing.
type GCPAssetSweep struct {
	Searcher gcpsweep.Searcher
	Project  string
	// Unlabelled names the provider's types with no labels map, read off
	// its schema by the caller that builds the leg: each one the
	// configuration declares is a TYPE_NOT_TAGGABLE gap ([unlabelledNotSwept]).
	Unlabelled map[string]bool
}

// Leg is [substrate.SweepAssetSearch].
func (GCPAssetSweep) Leg() substrate.Sweep { return substrate.SweepAssetSearch }

// ScansDeclared reports whether this leg, rather than the config-driven
// scan, finds the declared needs-discovery instances of typeName: a
// labelled google type, when there is a client to search with
// (INTENTIUS/choudoufu#2134). hashicorp/google serves no list resource the
// config-driven scan could use, so without this every such instance was
// refused as unlistable and never bound by the label it carries.
func (g GCPAssetSweep) ScansDeclared(typeName string) bool {
	return g.Searcher != nil && strings.HasPrefix(typeName, "google_") && !g.Unlabelled[typeName]
}

// Sweep runs the leg.
func (g GCPAssetSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	diags := g.sweep(ctx, in.Request, in.decl, in.Result)
	unlabelledNotSwept(in.Request, g.Unlabelled, in.Result)
	return diags
}

// sweep searches every project and files each hit. decl is the pass's
// declared-instance index, nil in a caller with no binding demand: a hit
// whose address label is a declared needs-discovery instance's becomes that
// instance's claimant, and one whose block part names a declared count
// block joins that block's set, so [bind] binds both the way it binds an
// AWS tag sweep's, by tofu-slot where the set carries slots and by address
// where it does not, and the slots it settles reach the stamp through
// [Result.SlotTable].
func (g GCPAssetSweep) sweep(ctx context.Context, req Request, decl *declared, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if g.Searcher == nil {
		return diags
	}

	declared := declaredGoogleAddresses(req.Resolutions)
	demand := g.googleDemand(decl)
	projects := GCPSweepProjects(g.Project, req.Resolutions)
	if len(projects) == 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPSweepIncomplete,
			fmt.Sprintf("No GCP project is known for estate %q: the google provider block names none, GOOGLE_PROJECT and its siblings are unset, and no resource in the configuration names one. Nothing was searched, so a resource whose block was deleted is not reported until a run knows where to look.", req.Estate)))
		res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: gcpGapType, Reason: SweepGapListFailed, Detail: "no GCP project to search"})
		return diags.Append(demand.unsearched(decl, res, "no GCP project is known to search"))
	}

	cov := gcpsweep.Coverage{}
	for _, p := range projects {
		cov[p] = g.Searcher
	}
	var orphans []string
	fileOrphan := func(a gcpsweep.Asset, address, withheld string) {
		display := a.AssetType + " " + a.Name
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:    a.AssetType,
			ImportID:    a.Name,
			Marker:      address,
			DisplayName: display,
			Tags:        a.Labels,
			Resource:    cty.NilVal,
			Swept:       true,
			Withheld:    withheld,
		})
		orphans = append(orphans, fmt.Sprintf("%s (%s)", display, withheld))
	}
	// pending is the hits whose block part names a declared count block
	// and whose instance part names none of its declared instances, held
	// until every project is searched: whether they join the set depends
	// on whether the set carries slots at all.
	pending := map[*countBlock][]gcpsweep.Asset{}
	var failed []string
	for _, pr := range gcpsweep.Sweep(ctx, cov, projects, req.Estate) {
		if pr.Err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPSweepIncomplete,
				fmt.Sprintf("Project %s was not searched for estate %q: %s. A resource there whose block was deleted is not reported until a run can search it.", pr.Project, req.Estate, gcpSweepReason(pr.Err))))
			res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: gcpGapType, Reason: SweepGapListFailed, Detail: fmt.Sprintf("project %s: %s", pr.Project, pr.Err)})
			failed = append(failed, pr.Project)
			continue
		}
		res.Scans = append(res.Scans, TypeScan{
			TypeName:  gcpGapType,
			Filtering: FilterServerSide,
			Scope:     ScopeEstate,
			Sweep:     true,
			Source:    SourceGCPAssets,
			Declared:  len(declared.instances),
		})
		for _, a := range pr.Assets {
			display := a.AssetType + " " + a.Name
			if hold, ok := substrate.ControllerHeld(substrate.HoldEvidence{Labels: a.Labels, AssetType: a.AssetType}); ok {
				res.ControllerHeld = append(res.ControllerHeld, ControllerHeldResource{
					TypeName:    a.AssetType,
					ImportID:    a.Name,
					DisplayName: display,
					Kind:        a.AssetType,
					Controller:  hold.Controller,
					HeldBy:      hold.HeldBy,
				})
				continue
			}
			address := a.Labels[markers.TagAddress]
			if entry, ok := demand.entries[address]; ok {
				entry.addClaimant(gcpClaimant(a, address, entry.escaped))
				continue
			}
			if _, isDeclared := declared.instances[address]; isDeclared {
				continue
			}
			block, hasBlock := markers.GoogleAddressBlock(address)
			if cb, ok := demand.counts[block]; hasBlock && ok {
				pending[cb] = append(pending[cb], a)
				continue
			}
			withheld := fmt.Sprintf("Its tofu-address label %q encodes no address this configuration declares, and a GCP address label is matched, never decoded, so nothing here says which block made it. It is reported and left alone; the estate's record for that block, when there is one, is what proposes its removal.", address)
			if address == "" {
				withheld = "It carries no tofu-address label, so nothing says which block made it. It is reported and left alone."
			} else if hasBlock {
				if blockAddr, known := declared.blocks[block]; known {
					withheld = gcpStaleInstanceWithheld(address, blockAddr.String())
				}
			}
			fileOrphan(a, address, withheld)
		}
	}

	// A count set whose members carry tofu-slot takes every hit naming its
	// block: the slot says which member each one is, so a member whose
	// label names a stale index binds by its slot and one past the count
	// is surplus, removed as a scale-down removes it on AWS, and a member
	// with no slot beside slotted ones is the mixed set [bindCountBlock]
	// refuses. A set with no slots anywhere is told apart by address
	// alone, and a hit naming an index it no longer declares stays the
	// reported, never-removed orphan above.
	for _, cb := range sortedPendingBlocks(pending) {
		hits := pending[cb]
		slotted := false
		for _, c := range cb.claimants() {
			slotted = slotted || c.slot != ""
		}
		for _, a := range hits {
			slotted = slotted || a.Labels[markers.TagSlot] != ""
		}
		for _, a := range hits {
			address := a.Labels[markers.TagAddress]
			if slotted {
				cb.extra = append(cb.extra, gcpClaimant(a, address, address))
				continue
			}
			fileOrphan(a, address, gcpStaleInstanceWithheld(address, cb.addr))
		}
	}

	if len(failed) > 0 {
		diags = diags.Append(demand.unsearched(decl, res, "project "+strings.Join(failed, ", ")+" could not be searched"))
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPOrphanUnaddressed,
			fmt.Sprintf("%d resource(s) carry estate %q's label and no address this configuration declares:\n  - %s", len(orphans), req.Estate, strings.Join(orphans, "\n  - "))))
	}
	return diags
}

// gcpStaleInstanceWithheld is why a hit naming an instance its declared
// block no longer has is reported and left alone.
func gcpStaleInstanceWithheld(address, block string) string {
	return fmt.Sprintf("Its tofu-address label %q names an instance of %s that the configuration no longer declares. It is reported and left alone; the estate's record for that instance, when there is one, is what proposes its removal.", address, block)
}

// gcpDemand is the declared needs-discovery google instances the leg
// binds, by the tofu-address label each carries ([markers.GoogleAddressValue]),
// and their count blocks by the label's block part ([markers.GoogleBlockValue]).
type gcpDemand struct {
	entries map[string]*declaredEntry
	counts  map[string]*countBlock
	types   []string
}

// googleDemand indexes decl's entries of every type [GCPAssetSweep.ScansDeclared]
// claims. A record-backed entry is indexed too, as [declared.entryFor]
// finds it, so a second object claiming its address is a collision.
func (g GCPAssetSweep) googleDemand(decl *declared) gcpDemand {
	d := gcpDemand{entries: map[string]*declaredEntry{}, counts: map[string]*countBlock{}}
	if decl == nil {
		return d
	}
	for _, typeName := range decl.bindTypeNames() {
		if !g.ScansDeclared(typeName) {
			continue
		}
		if decl.types[typeName] != nil {
			d.types = append(d.types, typeName)
		}
		for _, entries := range []map[string]*declaredEntry{decl.types[typeName], decl.recordBacked[typeName]} {
			for _, entry := range entries {
				d.entries[markers.GoogleAddressValue(entry.res.Addr)] = entry
			}
		}
		for _, cb := range decl.counts[typeName] {
			d.counts[markers.GoogleBlockValue(addrs.AbsResource{Module: cb.module, Resource: cb.resource})] = cb
		}
	}
	return d
}

// unsearched marks every type the leg claimed unscanned, with the refusal
// that says why, when a project could not be searched: a declared instance
// the search never reached is not absent, and calling it unbound would
// plan a create of something that may exist. The config-driven scan's
// refusal for a type nothing lists is the same answer.
func (d gcpDemand) unsearched(decl *declared, res *Result, why string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, typeName := range d.types {
		decl.unscanned[typeName] = true
		diags = diags.Append(problemDiag(res, Problem{
			Kind:     ProblemTypeNotListable,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"The %d declared instance(s) of %s are found by their tofu-address label through Cloud Asset Inventory, and %s, so this run cannot tell which exist. Fix the search and plan again.",
				len(decl.types[typeName]), typeName, why),
		}))
	}
	return diags
}

// gcpClaimant is one hit as a claimant of a declared instance. Its import
// id is the asset's full resource name without the service host
// ("//pubsub.googleapis.com/projects/p/topics/t" is "projects/p/topics/t"),
// the projects/... form hashicorp/google's importers accept. An inference
// from the provider's import formats, not measured on every type.
func gcpClaimant(a gcpsweep.Asset, address, escaped string) claimant {
	importID := gcpImportID(a.Name)
	return claimant{
		importID:     importID,
		identityAttr: "id",
		displayName:  a.AssetType + " " + a.Name,
		marker:       address,
		escaped:      escaped,
		slot:         a.Labels[markers.TagSlot],
		tags:         a.Labels,
		noIdentity:   importID == "",
	}
}

// gcpImportID is an asset's full resource name without its leading
// "//host/".
func gcpImportID(name string) string {
	rest, ok := strings.CutPrefix(name, "//")
	if !ok {
		return name
	}
	_, path, ok := strings.Cut(rest, "/")
	if !ok {
		return ""
	}
	return path
}

// sortedPendingBlocks is pending's blocks in address order.
func sortedPendingBlocks(pending map[*countBlock][]gcpsweep.Asset) []*countBlock {
	out := make([]*countBlock, 0, len(pending))
	for cb := range pending {
		out = append(out, cb)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].addr < out[j].addr })
	return out
}

// unlabelledNotSwept files a [SweepGapNotTaggable] gap for every google
// type the configuration declares that unlabelled names (GitHub issue
// #2119, D3): with no labels map it carries no marker, so the Asset
// Inventory search never finds its objects, and a removed block of such a
// type is found again only from its record. The gaps render in the plan's
// "Not swept for removal" list, as AWS's untaggable types do. Losing a
// record is the record-store-loss case, documented with its recovery in
// live/RECOVERY.md.
func unlabelledNotSwept(req Request, unlabelled map[string]bool, res *Result) {
	seen := map[string]bool{}
	for _, r := range req.Resolutions {
		t := r.Type()
		if seen[t] || !unlabelled[t] {
			continue
		}
		seen[t] = true
		res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: t, Reason: SweepGapNotTaggable,
			Detail: "a google type with no labels map carries no marker, so the Cloud Asset Inventory search cannot list it; a removed block of this type is found again only from its record"})
	}
}

// gcpGapType is the name a GCP sweep gap and scan are filed under: the
// search is per project, not per resource type.
const gcpGapType = "google"

// gcpSweepReason is the clause naming why a project was not searched.
func gcpSweepReason(err error) string {
	var disabled *gcpsweep.ServiceDisabledError
	var denied *gcpsweep.PermissionDeniedError
	var uncovered *gcpsweep.UncoveredProjectError
	switch {
	case errors.As(err, &disabled):
		return fmt.Sprintf("the Cloud Asset API (%s) is not enabled in it; enable it, or the estate cannot be swept there", gcpsweep.Service)
	case errors.As(err, &denied):
		return fmt.Sprintf("the provider's credential lacks %s there", gcpsweep.Permission)
	case errors.As(err, &uncovered):
		return "no google provider configuration's credential covers it"
	}
	return err.Error()
}

// googleDeclared is the google instances a configuration declares, by the
// tofu-address label value each carries, and its blocks by the block part.
type googleDeclared struct {
	instances map[string]addrs.AbsResourceInstance
	blocks    map[string]addrs.AbsResource
}

// declaredGoogleAddresses encodes every google resolution's address the
// way the stamp writes it ([markers.GoogleAddressValue]).
func declaredGoogleAddresses(resolutions []identity.Resolution) googleDeclared {
	out := googleDeclared{instances: map[string]addrs.AbsResourceInstance{}, blocks: map[string]addrs.AbsResource{}}
	for _, r := range resolutions {
		if !strings.HasPrefix(r.Type(), "google_") {
			continue
		}
		out.instances[markers.GoogleAddressValue(r.Addr)] = r.Addr
		out.blocks[markers.GoogleBlockValue(r.Addr.ContainingResource())] = r.Addr.ContainingResource()
	}
	return out
}

// gcpImportProject finds the project in a google import id, which every
// project-scoped type spells projects/PROJECT/....
var gcpImportProject = regexp.MustCompile(`(?:^|/)projects/([^/]+)/`)

// GCPSweepProjects is every project the GCP leg searches: the provider
// block's own, and every project a concrete google resolution's import id
// names, each once, sorted. A resource that names its own project
// (`project = "..."`) is placed there, not in the provider's.
func GCPSweepProjects(providerProject string, resolutions []identity.Resolution) []string {
	seen := map[string]bool{}
	if providerProject != "" {
		seen[providerProject] = true
	}
	for _, r := range resolutions {
		if r.Class != identity.ClassConcrete || !strings.HasPrefix(r.Type(), "google_") {
			continue
		}
		if m := gcpImportProject.FindStringSubmatch(r.ImportID); m != nil && m[1] != "" {
			seen[m[1]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
