// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// This file is issue #69's fix: an estate whose managed resources span more
// than one provider configuration (aliased aws providers, most often
// multi-region) used to be refused outright by internal/command/live_plan.go
// before it ever reached this package, because the estate-wide sweep
// (Request.Sweep) can only ever list through the one provider handle a
// [Request] carries. [Merge] is what lets a caller run [Discover] once per
// distinct provider configuration and combine the results into one Result,
// usable by the rest of the pipeline exactly as a single-provider Result
// already is - see internal/command/live_plan.go's statelessDiscover for the
// caller that drives it.
//
// Discover itself is unchanged: every existing single-provider caller
// (this package's own tests, the scale benchmark, a single-provider
// live-plan) keeps calling it exactly as before and gets exactly what it got
// before. The multi-provider case is additive, built on top rather than
// threaded through Discover's own request/response shape, which is what
// keeps "a single-provider estate behaves byte-identically" a property of
// the code rather than a claim about it.
package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Pass is one single-provider [Discover] call's result, labeled with the
// provider configuration it listed through.
//
// Each call should be given the *whole* estate's resolutions
// (Request.Resolutions unfiltered) plus Request.ScopeProvider set to this
// same provider configuration - not a resolution list pre-narrowed to this
// provider's own managed resources. Request.ScopeProvider's own doc comment
// explains why: an account-global list operation (aws_s3_bucket, IAM,
// Route53) hands every pass every account's population of a type,
// including objects declared under a different provider configuration, and
// a pass needs the whole picture to tell "somebody else's declared
// resource" apart from "an orphan to remove" - it just must not *bind*
// outside its own scope.
type Pass struct {
	// Provider is the provider configuration this pass listed through.
	Provider addrs.AbsProviderConfig

	// Region is the region this pass listed in - the same value the caller
	// gave [Request.Region] for this pass, when it had one. Purely for
	// naming a cross-provider collision legibly (a reader wants "us-east-1
	// and us-west-2 disagree", not two provider-configuration addresses);
	// nothing in Merge's own logic depends on it, and an empty value falls
	// back to naming the provider configuration itself.
	Region string

	// Result is what Discover returned for it. A pass whose own diagnostics
	// carried errors must never reach Merge - the same rule Discover's own
	// doc comment states for a single-provider caller, just enforced one
	// level up.
	Result *Result
}

// label is how a pass identifies itself inside a collision message: its
// region when the caller supplied one, its provider configuration address
// otherwise.
func (p Pass) label() string {
	if p.Region != "" {
		return p.Region
	}
	return p.Provider.String()
}

// Merge combines the results of several single-provider [Discover] passes
// run against one estate - one per distinct provider configuration among
// the estate's managed resources - into one Result, safe to hand to
// [projection.BuildWith] and [foreign.Classify] exactly as a
// single-provider Result already is.
//
// The second return value attributes every merged resolution marked
// [identity.Resolution.Undeclared] to the pass that found it, keyed by its
// address string - the account and region a sweep-discovered resource with
// no configuration of its own has to be read back through, since nothing
// else says which one. It is [projection.Options.UndeclaredProviders]'
// input; a single-pass merge still populates it, though a caller with only
// one provider configuration is free to use the simpler
// [projection.Options.UndeclaredProvider] instead, as every caller did
// before this existed.
//
// Every field a pass fills in purely from its own account and region -
// Bindings, Unbound, Unclaimed, Slots, Surplus, and (after issue #69's
// Request.ScopeProvider) ParentReads - is simply concatenated. That is
// sound because Request.ScopeProvider ([Pass]'s doc comment) keeps each of
// those fields scoped to the one pass responsible for it: a needs-discovery
// instance's resource block names exactly one provider configuration, so
// at most one pass's Bindings or Unbound could ever name it, and the same
// is true of a count block's Slots/Surplus and of which pass's
// ParentReads leg processes a given declared parent. Scans, SweepGaps,
// SweepCovered and each pass's own Problems are concatenated too, and
// deliberately may repeat across passes - each entry is a true, distinct
// fact about that one pass's own account and region, not a duplicate to
// collapse.
//
// Resolutions is not simply concatenated, because every pass was handed
// the whole estate's resolutions (not a filtered subset - see [Pass]), so
// naive concatenation would repeat every declared resolution once per
// pass. Merge instead keeps, for each address with a resource block
// ([identity.Resolution.Undeclared] false), whichever pass's copy is the
// more resolved of the (at most two distinct) values it can ever take -
// see the ScopeProvider reasoning above for why only one pass could ever
// have bound it. An address with no resource block (Undeclared true) has
// no such owner to deduplicate against and is unique to the pass that
// found it, subject to the collision handling below.
//
// Orphans are the one field that is not simply concatenated, because an
// orphan carries no configuration to say which provider it "belongs" to -
// only a marker, and per live/MARKERS.md's Ownership semantics a marker is
// scoped to an estate and an address with no notion of region: "at most one
// live resource per address per estate", full stop, not "per address per
// estate per provider configuration". Two passes each independently
// proposing to remove the same address is therefore not two resources
// coexisting because they happen to sit in different regions - it is
// exactly the collision MARKERS.md already names, one neither pass could
// see on its own because each only ever lists its own account. Merge is the
// first point with the whole estate in view, so it is where that collision
// is caught: every address more than one pass's Orphans agrees on becomes a
// [ProblemCollision], every one of the agreeing entries loses its Removal,
// and none of their synthetic resolutions survive into the merged
// Resolutions. A single pass's own address collisions (two live resources
// in the *same* account claiming one address) are unaffected - Discover
// already resolved those, via [collisionOrphanProblem], before Merge ever
// sees the result.
func Merge(estate string, passes []Pass) (*Result, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	providerOf := make(map[string]addrs.AbsProviderConfig)
	res := &Result{Estate: estate}

	if len(passes) == 0 {
		return res, providerOf, diags
	}
	if len(passes) == 1 {
		// Nothing to reconcile across passes when there is only one: what it
		// found is the answer, unchanged, and it is handed back rather than
		// rebuilt field by field so a single-provider caller gets exactly
		// what a direct call to Discover would have given it.
		p := passes[0]
		for _, r := range p.Result.Resolutions {
			if r.Undeclared {
				providerOf[r.Addr.String()] = p.Provider
			}
		}
		return p.Result, providerOf, diags
	}

	skip := crossProviderOrphanCollisions(estate, passes, res, &diags)

	// base holds the merged answer for every resolution that came out of
	// the configuration (r.Undeclared == false: a needs-discovery instance
	// or an already-concrete, client-named one - see [Request.ScopeProvider]
	// on why every pass is handed the *whole* estate's resolutions rather
	// than a filtered subset). Every pass's own copy of such an address is
	// either byte-identical to every other pass's (nothing here bound it,
	// or it never needed binding at all) or, for the one pass whose
	// ScopeProvider owns it, rewritten to a bound Concrete resolution - and
	// at most one pass can ever be that pass, because a needs-discovery
	// instance's resource block names exactly one provider configuration.
	// pickBase keeps whichever copy is more resolved, which is the correct
	// one whichever pass produced it and a no-op when every pass agrees.
	base := make(map[string]identity.Resolution)
	baseOrder := make([]string, 0)
	pickBase := func(key string, r identity.Resolution) {
		existing, ok := base[key]
		if !ok {
			baseOrder = append(baseOrder, key)
			base[key] = r
			return
		}
		if existing.Class != identity.ClassConcrete && r.Class == identity.ClassConcrete {
			base[key] = r
		}
	}

	// sweepGapSeen and sweepCoveredSeen dedupe [Result.SweepGaps] and
	// [Result.SweepCovered] across passes by (type, reason) and type
	// respectively. Unlike Problems (each one is about a specific live
	// resource in one pass's own account) or Scans (each pass's own
	// measured counts), whether a type is listable or taggable at all is a
	// fact about the provider's schema, identical for every provider
	// configuration built from the same provider version - so reporting it
	// once per pass would repeat the same 298-type list verbatim per
	// provider configuration rather than adding information.
	sweepGapSeen := make(map[string]bool)
	sweepCoveredSeen := make(map[string]bool)

	for pi, p := range passes {
		res.Bindings = append(res.Bindings, p.Result.Bindings...)
		res.Unbound = append(res.Unbound, p.Result.Unbound...)
		res.Unclaimed = append(res.Unclaimed, p.Result.Unclaimed...)
		res.Orphans = append(res.Orphans, p.Result.Orphans...)
		for _, g := range p.Result.SweepGaps {
			key := g.TypeName + "\x00" + string(g.Reason)
			if sweepGapSeen[key] {
				continue
			}
			sweepGapSeen[key] = true
			res.SweepGaps = append(res.SweepGaps, g)
		}
		for _, typeName := range p.Result.SweepCovered {
			if sweepCoveredSeen[typeName] {
				continue
			}
			sweepCoveredSeen[typeName] = true
			res.SweepCovered = append(res.SweepCovered, typeName)
		}
		res.Surplus = append(res.Surplus, p.Result.Surplus...)
		res.Slots = append(res.Slots, p.Result.Slots...)
		res.Problems = append(res.Problems, p.Result.Problems...)
		res.Scans = append(res.Scans, p.Result.Scans...)
		res.ParentReads = append(res.ParentReads, p.Result.ParentReads...)

		for _, r := range p.Result.Resolutions {
			if !r.Undeclared {
				pickBase(r.Addr.String(), r)
				continue
			}
			if skip[pi][r.Addr.String()] {
				// A removal this pass proposed for an address a collision
				// downgraded below - see crossProviderOrphanCollisions. The
				// live resource is still reported (it is still in
				// res.Orphans, Withheld rather than silently dropped); only
				// the resolution that would have told the projection to
				// destroy it is held back.
				continue
			}
			// Undeclared resolutions (orphan removals, parent-read
			// removals) have no config-declared owner to deduplicate
			// against: each one is unique to the pass that found it.
			res.Resolutions = append(res.Resolutions, r)
			providerOf[r.Addr.String()] = p.Provider
		}
	}

	for _, key := range baseOrder {
		res.Resolutions = append(res.Resolutions, base[key])
	}

	res.sortEverything()
	return res, providerOf, diags
}

// crossProviderOrphanCollisions finds every address more than one pass's
// Orphans claims, downgrades every one of the agreeing entries so none of
// them is proposed as a removal, and records a [ProblemCollision] plus an
// error diagnostic for each - see [Merge]'s own doc comment for why this is
// the honest reading of live/MARKERS.md rather than a bug to paper over.
//
// The returned map says which (pass index, address string) pairs must not
// contribute their synthetic removal resolution to the merged Resolutions
// list.
func crossProviderOrphanCollisions(estate string, passes []Pass, res *Result, diags *tfdiags.Diagnostics) map[int]map[string]bool {
	type orphanLoc struct {
		pass  int
		index int
	}

	byAddr := make(map[string][]orphanLoc)
	for pi, p := range passes {
		for oi, o := range p.Result.Orphans {
			if !o.Addressable {
				continue
			}
			key := o.Addr.String()
			byAddr[key] = append(byAddr[key], orphanLoc{pass: pi, index: oi})
		}
	}

	// Sorted so that when more than one address collides, the problems and
	// diagnostics this produces come out in the same order on every run
	// regardless of Go's map iteration order.
	keys := make([]string, 0, len(byAddr))
	for k := range byAddr {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	skip := make(map[int]map[string]bool)
	for _, key := range keys {
		locs := byAddr[key]

		distinctProviders := make(map[string]bool, len(locs))
		for _, loc := range locs {
			distinctProviders[passes[loc.pass].Provider.String()] = true
		}
		if len(distinctProviders) < 2 {
			// Every claimant of this address came from the same pass, which
			// means Discover's own collisionOrphanProblem already resolved
			// it (at most one survivor, if any) before Merge ever saw it.
			continue
		}

		ids := make([]string, 0, len(locs))
		regions := make([]string, 0, len(locs))
		for _, loc := range locs {
			o := &passes[loc.pass].Result.Orphans[loc.index]
			id := o.ImportID
			if id == "" {
				id = "(no identity)"
			}
			label := passes[loc.pass].label()
			ids = append(ids, fmt.Sprintf("%s in %s", id, label))
			regions = append(regions, label)
		}
		sort.Strings(ids)
		sort.Strings(regions)

		for _, loc := range locs {
			o := &passes[loc.pass].Result.Orphans[loc.index]
			o.Removal = false
			o.Withheld = fmt.Sprintf(
				"%d live %s resources across %s carry estate %q and this address; markers are address-unique estate-wide with no notion of region (live/MARKERS.md, \"Ownership semantics\"), so this is a collision needing a human, not two resources that happen to sit in different places",
				len(locs), o.TypeName, strings.Join(regions, " and "), estate)

			if skip[loc.pass] == nil {
				skip[loc.pass] = make(map[string]bool)
			}
			skip[loc.pass][o.Addr.String()] = true
		}

		first := passes[locs[0].pass].Result.Orphans[locs[0].index]
		detail := fmt.Sprintf(
			"%d live %s resources across %s carry estate %q and the address %q: %s. A tofu-address marker names one resource per estate regardless of region or account; a human has to resolve which is the real owner before this estate can be planned. See live/MARKERS.md, \"Ownership semantics\".",
			len(locs), first.TypeName, strings.Join(regions, " and "), estate, first.Normalized, strings.Join(ids, ", "))
		res.Problems = append(res.Problems, Problem{
			Kind:     ProblemCollision,
			TypeName: first.TypeName,
			Addr:     first.Addr,
			Marker:   first.Normalized,
			LiveIDs:  ids,
			Detail:   detail,
		})
		*diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, problemSummaries[ProblemCollision], detail))
	}

	return skip
}
