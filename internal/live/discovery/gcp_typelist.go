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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
)

// The GCP leg's per-type lists (INTENTIUS/choudoufu#2264). A declared
// instance of a covered type ([gcpsweep.Covered]: the server-assigned types
// written by hand, and every type derived from its Asset name and service)
// is bound from its type's own list,
// read from the service's own store, rather than from the Cloud Asset
// Inventory index, which lags a create. Two applies that each created one
// such object are then both seen by the next plan, which names the
// collision instead of binding whichever object the index happened to
// hold. A type whose list cannot be made this run (a scope configuration
// cannot name, a list call that fails) is bound from the index as before,
// with a warning saying the collision check did not cover it.

// SummaryGCPTypeListIncomplete is the warning for a covered type whose own
// list could not be made, so its declared instances were bound from the
// Asset Inventory index, which may lag a concurrent create.
const SummaryGCPTypeListIncomplete = "GCP type list incomplete"

// gcpTypeLists is what the per-type lists returned: the covered types whose
// every scope was listed, and the hits those lists returned that name a
// declared instance or count block of the type.
type gcpTypeLists struct {
	listed map[string]bool
	hits   []gcpsweep.Asset
}

// covers reports whether a hit the Asset search returned is one a per-type
// list read instead: of a listed type, by its asset type, naming an entry or
// count block of that type. Such a hit is dropped, so the list's answer is
// the only one about the type.
func (l gcpTypeLists) covers(demand gcpDemand, a gcpsweep.Asset) bool {
	if len(l.listed) == 0 {
		return false
	}
	address := a.Labels[markers.TagAddress]
	typeName := ""
	if entry, ok := demand.entries[address]; ok {
		typeName = entry.res.Type()
	} else if block, hasBlock := markers.GoogleAddressBlock(address); hasBlock {
		if cb, ok := demand.counts[block]; ok {
			typeName = cb.typeName
		}
	}
	if typeName == "" || !l.listed[typeName] {
		return false
	}
	t, _ := gcpsweep.Covered(typeName)
	return t.Names(a)
}

// typeLists lists every covered type the demand holds, at each scope its
// declared instances name. A type is listed only when every one of its
// instances names a scope, since a list that misses one scope would read
// as "no second object" for the instance there.
func (g GCPAssetSweep) typeLists(ctx context.Context, req Request, decl *declared, demand gcpDemand) (gcpTypeLists, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	out := gcpTypeLists{listed: map[string]bool{}}
	if g.Lister == nil || decl == nil {
		return out, diags
	}
	for _, typeName := range demand.coveredTypes() {
		t, _ := gcpsweep.Covered(typeName)
		scopes, why := g.typeScopes(ctx, req, decl, demand, t)
		if why != "" {
			diags = diags.Append(gcpTypeListWarning(req.Estate, typeName, why))
			continue
		}
		var hits []gcpsweep.Asset
		failed := ""
		for _, scope := range scopes {
			assets, err := g.Lister.ListType(ctx, typeName, scope, req.Estate)
			if err != nil {
				failed = fmt.Sprintf("listing it in %s failed: %s", scope, gcpTypeListReason(err, t))
				break
			}
			hits = append(hits, assets...)
		}
		if failed != "" {
			diags = diags.Append(gcpTypeListWarning(req.Estate, typeName, failed))
			continue
		}
		out.listed[typeName] = true
		for _, a := range hits {
			address := a.Labels[markers.TagAddress]
			if entry, ok := demand.entries[address]; ok && entry.res.Type() == typeName {
				out.hits = append(out.hits, a)
				continue
			}
			if block, hasBlock := markers.GoogleAddressBlock(address); hasBlock {
				if cb, ok := demand.counts[block]; ok && cb.typeName == typeName {
					out.hits = append(out.hits, a)
				}
			}
			// Any other hit is the Asset search's to report: an orphan, a
			// displaced marker or a declared instance not waiting on
			// discovery, each filed once, from the index, as before.
		}
	}
	return out, diags
}

// coveredTypes is every type in the demand a per-type lister covers, sorted.
func (d gcpDemand) coveredTypes() []string {
	seen := map[string]bool{}
	for _, entry := range d.entries {
		if _, ok := gcpsweep.Covered(entry.res.Type()); ok {
			seen[entry.res.Type()] = true
		}
	}
	for _, cb := range d.counts {
		if _, ok := gcpsweep.Covered(cb.typeName); ok {
			seen[cb.typeName] = true
		}
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// typeScopes is every scope t's declared instances name, each once and
// sorted, or why one of them names none. An instance's project is its
// block's own `project`, else the provider block's; its location is its
// block's own location argument, else the provider block's region
// (gcpsweep.ProviderScope.Default, GitHub issue #2273).
func (g GCPAssetSweep) typeScopes(ctx context.Context, req Request, decl *declared, demand gcpDemand, t gcpsweep.TypeList) ([]gcpsweep.ListScope, string) {
	for _, cb := range demand.counts {
		if cb.typeName == t.Type && len(cb.entries) == 0 {
			return nil, fmt.Sprintf("count block %s declares no instance to read a project or location from", cb.addr)
		}
	}
	seen := map[gcpsweep.ListScope]bool{}
	for _, entries := range []map[string]*declaredEntry{decl.types[t.Type], decl.recordBacked[t.Type]} {
		for _, entry := range entries {
			addr := entry.res.Addr
			if t.Derived {
				scope, why := g.derivedScope(ctx, req, addr, t)
				if why != "" {
					return nil, why
				}
				seen[scope] = true
				continue
			}
			project, set, ok := configuredGoogleArg(ctx, req.Config, addr, "project")
			switch {
			case set && !ok:
				return nil, fmt.Sprintf("%s's project is not known from configuration alone", addr)
			case !set:
				project = g.Project
			}
			if project == "" {
				return nil, fmt.Sprintf("no project is known for %s: neither its block nor the provider block names one", addr)
			}
			location := ""
			if t.Location != "" {
				var lset, lok bool
				location, lset, lok = configuredGoogleArg(ctx, req.Config, addr, t.Location)
				switch {
				case lset && !lok:
					return nil, fmt.Sprintf("%s's %s is not known from configuration alone", addr, t.Location)
				case !lset:
					// GitHub issue #2273: a block with no location is
					// listed in the provider's region, one rule for both
					// engines (gcpsweep.ProviderScope.Default).
					location = g.providerScope().Default(t.Location)
				}
				if location == "" {
					return nil, fmt.Sprintf("no %s is known for %s: neither its block nor the provider block names one", t.Location, addr)
				}
			}
			seen[gcpsweep.ListScope{Project: project, Location: location}] = true
		}
	}
	out := make([]gcpsweep.ListScope, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		if out[i].Location != out[j].Location {
			return out[i].Location < out[j].Location
		}
		return out[i].Collection+"?"+out[i].Query < out[j].Collection+"?"+out[j].Query
	})
	return out, ""
}

// derivedScope is where one declared instance of a derived type
// ([gcpsweep.TypeList.Derived]) is listed: its type's collection with every
// variable filled from the instance's block, or why one cannot be.
//
// project, region and zone are the block's own argument, else the provider
// block's, as hashicorp/google defaults them; a self link or full name counts
// by its last segment. location is the block's own `location`, else the
// provider block's region (GitHub issue #2273, ruled 2026-10-26: plunge's
// rule, so both engines list a block with no location under one scope;
// gcpsweep.ProviderScope.Default is that one rule). Any other variable is the parent
// object's argument of the same name (a crypto key's key_ring, a map entry's
// map), which has to be known from configuration alone; one that refers to
// another resource's attribute is not, and the type is bound from the index
// with a warning, as before.
func (g GCPAssetSweep) derivedScope(ctx context.Context, req Request, addr addrs.AbsResourceInstance, t gcpsweep.TypeList) (gcpsweep.ListScope, string) {
	why := ""
	vars := func(name string) (string, bool) {
		v, set, ok := configuredGoogleArg(ctx, req.Config, addr, name)
		if set && !ok {
			if why == "" {
				why = fmt.Sprintf("%s's %s is not known from configuration alone", addr, name)
			}
			return "", false
		}
		if set && gcpsweep.ScopeVars[name] {
			v = v[strings.LastIndex(v, "/")+1:]
		}
		if !set {
			v = g.providerScope().Default(name)
		}
		return v, v != ""
	}
	scope, err := t.Scope(vars)
	if why != "" {
		return gcpsweep.ListScope{}, why
	}
	if err != nil {
		return gcpsweep.ListScope{}, fmt.Sprintf("%s's list path %s cannot be filled for %s: %s", t.Type, t.Path, addr, err)
	}
	return scope, ""
}

// providerScope is the provider block's project, region and zone, the
// defaults a list scope falls back to.
func (g GCPAssetSweep) providerScope() gcpsweep.ProviderScope {
	return gcpsweep.ProviderScope{Project: g.Project, Region: g.Region, Zone: g.Zone}
}

// gcpTypeListWarning is the warning for a covered type bound from the
// index this run.
func gcpTypeListWarning(estate, typeName, why string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(tfdiags.Warning, SummaryGCPTypeListIncomplete,
		fmt.Sprintf("%s is listed through its own service for estate %q so that two objects one address claims are both seen, and this run could not list it: %s. Its declared instances are bound from the Cloud Asset Inventory index instead, which can miss an object created moments ago, so a second object from a concurrent apply may not be reported as a collision until a later plan.", typeName, estate, why))
}

// gcpTypeListReason is the clause naming why a type's list failed.
func gcpTypeListReason(err error, t gcpsweep.TypeList) string {
	msg := gcpSweepReason(err)
	if strings.Contains(msg, gcpsweep.Service) || strings.Contains(msg, gcpsweep.Permission) {
		// gcpSweepReason names the Asset API; the list is on t's service.
		msg = err.Error()
	}
	return msg + " (needs " + t.Service + " enabled and " + t.Permission + ")"
}
