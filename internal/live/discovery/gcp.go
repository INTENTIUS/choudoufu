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

// Sweep runs the leg.
func (g GCPAssetSweep) Sweep(ctx context.Context, in *SweepInput) tfdiags.Diagnostics {
	// The collision refusal runs whether or not there is a searcher: it is
	// a fact about the configuration, not about what the index answers.
	if _, collisions := declaredGoogleAddresses(in.Request.Resolutions); collisions.HasErrors() {
		return collisions
	}
	diags := g.sweep(ctx, in.Request, in.Result)
	unlabelledNotSwept(in.Request, g.Unlabelled, in.Result)
	return diags
}

func (g GCPAssetSweep) sweep(ctx context.Context, req Request, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if g.Searcher == nil {
		return diags
	}

	declared, _ := declaredGoogleAddresses(req.Resolutions)
	projects := GCPSweepProjects(g.Project, req.Resolutions)
	if len(projects) == 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPSweepIncomplete,
			fmt.Sprintf("No GCP project is known for estate %q: the google provider block names none, GOOGLE_PROJECT and its siblings are unset, and no resource in the configuration names one. Nothing was searched, so a resource whose block was deleted is not reported until a run knows where to look.", req.Estate)))
		res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: gcpGapType, Reason: SweepGapListFailed, Detail: "no GCP project to search"})
		return diags
	}

	cov := gcpsweep.Coverage{}
	for _, p := range projects {
		cov[p] = g.Searcher
	}
	var orphans []string
	for _, pr := range gcpsweep.Sweep(ctx, cov, projects, req.Estate) {
		if pr.Err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPSweepIncomplete,
				fmt.Sprintf("Project %s was not searched for estate %q: %s. A resource there whose block was deleted is not reported until a run can search it.", pr.Project, req.Estate, gcpSweepReason(pr.Err))))
			res.SweepGaps = append(res.SweepGaps, SweepGap{TypeName: gcpGapType, Reason: SweepGapListFailed, Detail: fmt.Sprintf("project %s: %s", pr.Project, pr.Err)})
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
			if _, isDeclared := declared.instances[address]; isDeclared {
				continue
			}
			withheld := fmt.Sprintf("Its tofu-address label %q encodes no address this configuration declares, and a GCP address label is matched, never decoded, so nothing here says which block made it. It is reported and left alone; the estate's record for that block, when there is one, is what proposes its removal.", address)
			if address == "" {
				withheld = "It carries no tofu-address label, so nothing says which block made it. It is reported and left alone."
			} else if block, ok := markers.GoogleAddressBlock(address); ok {
				if blockAddr, known := declared.blocks[block]; known {
					withheld = fmt.Sprintf("Its tofu-address label %q names an instance of %s that the configuration no longer declares. It is reported and left alone; the estate's record for that instance, when there is one, is what proposes its removal.", address, blockAddr)
				}
			}
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
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryGCPOrphanUnaddressed,
			fmt.Sprintf("%d resource(s) carry estate %q's label and no address this configuration declares:\n  - %s", len(orphans), req.Estate, strings.Join(orphans, "\n  - "))))
	}
	return diags
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
// way the stamp writes it ([markers.GoogleAddressValue]), and refuses by
// name two instances whose labels would be equal, or two blocks whose block
// parts would be (GitHub issue #1121's address ruling; #2134 item 3): the
// label is matched and never decoded, so a collision would bind one
// block's object to the other. At 130 bits a part this never fires in
// practice, and it is checked anyway.
func declaredGoogleAddresses(resolutions []identity.Resolution) (googleDeclared, tfdiags.Diagnostics) {
	return declaredGoogleAddressesWith(resolutions, markers.GoogleAddressValue, markers.GoogleBlockValue)
}

// declaredGoogleAddressesWith is [declaredGoogleAddresses] over the two
// encoders, so a test can force the collision a real hash never produces.
func declaredGoogleAddressesWith(resolutions []identity.Resolution, instanceValue func(addrs.AbsResourceInstance) string, blockValue func(addrs.AbsResource) string) (googleDeclared, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	out := googleDeclared{instances: map[string]addrs.AbsResourceInstance{}, blocks: map[string]addrs.AbsResource{}}
	for _, r := range resolutions {
		if !strings.HasPrefix(r.Type(), "google_") {
			continue
		}
		v := instanceValue(r.Addr)
		if prev, dup := out.instances[v]; dup && !prev.Equal(r.Addr) {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryGCPAddressCollision,
				fmt.Sprintf("%s and %s encode to the same tofu-address label value %q, so neither can be told from the other on a live object. Rename one of the two blocks.", prev, r.Addr, v)))
			continue
		}
		out.instances[v] = r.Addr
		block := r.Addr.ContainingResource()
		b := blockValue(block)
		if prev, dup := out.blocks[b]; dup && !prev.Equal(block) {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryGCPAddressCollision,
				fmt.Sprintf("Blocks %s and %s encode to the same tofu-address block part %q, so a count member's slot could bind to the wrong one. Rename one of the two blocks.", prev, block, b)))
			continue
		}
		out.blocks[b] = block
	}
	return out, diags
}

// SummaryGCPAddressCollision is the refusal of two declared google
// addresses, or two blocks, that encode to one tofu-address label value.
const SummaryGCPAddressCollision = "Two addresses encode to one GCP address label"

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

// dropGCPHitsTheRecordsRemove drops a GCP leg's withheld orphan when a
// record leg proposes the destroy of the same object (GitHub issue #2134,
// item 1): the Asset search found a labelled object whose tofu-address
// encodes nothing declared, and the deleted block's record then named that
// address. The record's destroy is the answer, so the hit is not reported a
// second time as left alone. It runs after the removal legs.
func dropGCPHitsTheRecordsRemove(res *Result) {
	removed := map[string]bool{}
	for _, o := range res.Orphans {
		if o.Removal && strings.HasPrefix(o.Addr.Resource.Resource.Type, "google_") {
			removed[markers.GoogleAddressValue(o.Addr)] = true
		}
	}
	if len(removed) == 0 {
		return
	}
	kept := res.Orphans[:0]
	for _, o := range res.Orphans {
		if !o.Removal && o.Withheld != "" && o.Marker != "" && removed[o.Marker] {
			continue
		}
		kept = append(kept, o)
	}
	res.Orphans = kept
}
