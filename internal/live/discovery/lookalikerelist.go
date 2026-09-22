// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// relistForLookalikes is GitHub issue #1480: the one list call a plain plan
// makes so that the lookalike guard can still fire.
//
// The guard (b32bb7d5dd; live/MARKERS.md, "The residual risk, and the last
// line of defense") exists for a single scenario - somebody strips
// tofu-estate and tofu-address off a live, server-assigned resource, and the
// next plan proposes a silent CREATE of a duplicate beside it. Since
// 09d180f921 (2026-08-30, the CollectUnclaimed ruling on #604) an ordinary
// plan could not see that resource at all for any declared type whose list
// schema carries a filter block: [scanType]'s filterOK branch sets
// scan.Scope = [ScopeEstate] and sends tag:tofu-estate = this estate, and a
// group whose markers were just stripped is precisely what that filter
// drops. [Result.Unclaimed] came back empty, internal/live/foreign had no
// candidate, and the warning could only be had through
// TOFU_LIVE_COLLECT_UNCLAIMED=1 or -adoption-only.
//
// # What this does instead, and why it is not the one-line alternative
//
// The one-line alternative - passing true for the config-driven loop's
// collectUnclaimed - restores the guard by paging every declared type's
// whole regional population on every plan, which is the #622 shape the
// CollectUnclaimed ruling settled against: cost that scales with the
// customer's account rather than with the estate, measured at 710 API calls
// against 157 for a migrated 79-instance terralith.
//
// So this pays the wider list only where it buys something, which is the
// charter's own rule. After [bind] has run, a declared instance of a
// needs-discovery type left in [Result.Unbound] IS the pending create the
// guard is for - there is no other way for an instance to reach that list -
// and every other declared type is bound, which means the plan proposes no
// create of it and the guard would have nothing to say about it anyway. So:
//
//   - A steady-state "No changes" plan has an empty Unbound and makes no
//     extra call at all. TestLookalikeRelistCosts pins that by count,
//     because the 157-call figure is what the ruling was measured against.
//   - A plan with a pending create of such a type pays exactly one extra
//     list for that type, and nothing for any other.
//
// # What it deliberately does not do
//
// It does not re-run [scanType]. Binding, claiming, orphan collection and
// slot bookkeeping have all already happened for this type against the
// estate-scoped listing, and running them a second time over a superset
// would double-count every one of them. This walks the wider listing for
// one product only - the objects carrying no tofu-estate tag - and appends
// them to [Result.Unclaimed], which is the input internal/live/foreign
// classifies and the guard reads.
//
// It also declines to speak about an object whose tags this listing did not
// carry: no tag-index join and no per-service tag read happen here, so an
// object with an unreadable marker is skipped rather than called unclaimed.
// That is #1136's rule ("not ours" and "ours, unreadable" are different
// facts) applied to a leg that has no second route to the marker. In
// practice the population is narrow enough that the question barely arises:
// the only types reaching here are the ones whose list schema offers a
// server-side tag filter, which is the provider saying it reads their tags.
//
// An object carrying ANOTHER estate's marker is skipped too, and not
// counted into scan.OtherEstate. The plain plan's other-estate tally is
// measured over the estate-scoped listings every plan already makes;
// letting a type's tally silently widen whenever that type happened to have
// a create pending would make the number depend on the plan rather than on
// the account.
func relistForLookalikes(ctx context.Context, req Request, schemas listclient.Schemas, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if len(res.Unbound) == 0 {
		return diags
	}

	// The types with a create pending. A keyed instance is excluded because
	// the guard itself refuses to speak for one: [foreign.Lookalikes] skips
	// a count/for_each member outright on its generic path, and
	// foreign's buildSlots skips it on the matchTable path, since
	// cardinality cannot tell a stripped marker from ordinary scale-out.
	// Listing the account for a warning that can never be emitted would be
	// cost with nothing on the other side of it.
	pending := make(map[string]bool, len(res.Unbound))
	for _, addr := range res.Unbound {
		if addr.Resource.Key != addrs.NoKey {
			continue
		}
		pending[addr.Resource.Resource.Type] = true
	}
	if len(pending) == 0 {
		return diags
	}

	for i := range res.Scans {
		scan := &res.Scans[i]
		switch {
		case !pending[scan.TypeName]:
			// Nothing of this type is about to be created.
			continue
		case scan.Sweep || scan.CacheVouch:
			// Not the config-driven scan. A sweep row's type is one the
			// configuration declares nothing of, and a cache-vouch row is
			// hermetic by construction (#692).
			continue
		case scan.Scope == ScopeAll:
			// Already wide: either CollectUnclaimed was set for this run
			// (-adoption-only, TOFU_LIVE_COLLECT_UNCLAIMED=1) or the type's
			// list schema offers no tag filter, so the unmarked population
			// already crossed the wire and Result.Unclaimed already holds
			// it. This is what keeps the opted-in paths costing exactly
			// what they cost today.
			continue
		case scan.Source != SourceProvider:
			// The Cloud Control, tagging, service-API and record-store legs
			// reach their own scopes by their own routes; this is the
			// native list call's branch and only that one.
			continue
		case scan.Filtering != FilterServerSide:
			continue
		}
		diags = diags.Append(relistOneForLookalikes(ctx, req, schemas, scan, res))
	}

	return diags
}

// relistOneForLookalikes makes the one widened list call for a single type
// and harvests its unmarked population. See [relistForLookalikes].
//
// A failure here is a log line and nothing else, never a diagnostic: the
// estate-scoped listing this run already made is what every binding, every
// removal and every create in the plan rests on, and it succeeded. This
// call only adds a warning the plan would otherwise not carry, so a plan
// that would have completed must still complete. The scan row is left
// saying [ScopeEstate] in that case, which is exactly true - nothing wider
// was seen - and internal/live/foreign's coverage report will say so.
func relistOneForLookalikes(ctx context.Context, req Request, schemas listclient.Schemas, scan *TypeScan, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	typeName := scan.TypeName
	ts, ok := schemas.Get(typeName)
	if !ok {
		// Unreachable: this row exists because the type WAS listed through
		// the same schema set a moment ago.
		return diags
	}

	vals := make(map[string]cty.Value)
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}
	config, cfgDiags := ts.BuildConfig(vals)
	if cfgDiags.HasErrors() {
		log.Printf("[WARN] stateless/discovery: the lookalike guard's widened list configuration for %s could not be built (%s); the plan's create of it goes unchecked against unmarked live resources", typeName, cfgDiags.Err())
		return diags
	}

	log.Printf("[DEBUG] stateless/discovery: %s has a declared instance nothing claimed, so the plan proposes creating one; listing the type unfiltered once so the lookalike guard can see a stripped marker (issue #1480)", typeName)

	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	if listDiags.HasErrors() {
		log.Printf("[WARN] stateless/discovery: the lookalike guard's widened list of %s failed (%s); the plan's create of it goes unchecked against unmarked live resources", typeName, listDiags.Err())
		return diags
	}

	scan.Filtering = FilterClientSide
	scan.Scope = ScopeAll
	scan.FilterReason = fmt.Sprintf(
		"a declared %s is unbound, so the plan proposes creating one; the type was listed a second time without the estate filter so the lookalike guard could see a live resource whose markers were stripped",
		typeName)
	scan.LookalikeRelist = true
	scan.RelistListed = len(results)

	for _, r := range results {
		if r.Diagnostics.HasErrors() {
			// #531's shape: the provider delivered the object but could not
			// fully read it. Nothing may be concluded from its tags.
			continue
		}
		tags, taggable := markers.TagsOf(r.Resource)
		if !taggable {
			continue
		}
		if tags[TagEstate] != "" {
			// Ours (already bound off the estate-scoped listing) or another
			// estate's. Either way not unclaimed - see the doc comment.
			continue
		}
		importID, idAttr, _ := importIdentity(typeName, r)
		scan.Unclaimed++
		res.Unclaimed = append(res.Unclaimed, UnclaimedResource{
			TypeName:     typeName,
			ImportID:     importID,
			IdentityAttr: idAttr,
			Identity:     r.Identity,
			DisplayName:  r.DisplayName,
			Tags:         tags,
			Resource:     r.Resource,
		})
	}

	return diags
}
