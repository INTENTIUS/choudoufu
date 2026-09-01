// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"sort"

	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// planRejectAdoptionOnly refuses "-adoption-only" on a run that is not under
// live resource markers.
//
// Ignoring it would be worse than refusing it: the operator asked for a
// different report and would get the ordinary one, with no sign that the
// flag did nothing. The stock plan surface is unchanged in behaviour, which
// is what -estate's own placement argument (arguments/live_plan.go) is
// protecting; what changes is that a wrong invocation is now answered with
// a sentence instead of "flag provided but not defined".
func planRejectAdoptionOnly(adoptionOnly, live bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if !adoptionOnly || live {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"-adoption-only needs a configuration under live resource markers",
		"Adoption is the live-markers question of which live resources this estate's ownership markers can claim, and this configuration has no live block, so this run reads a state file instead and owns everything in it already. Add a live block naming the estate, or drop -adoption-only.",
	))
}

// GitHub issue #587's adoption ledger, built from what the run already
// decided.
//
// The design question the issue poses is whether to filter the plan or to
// render live-import's ratification report from the plan. It is settled by
// [github.com/intentius/choudoufu/internal/live/liveimport.Ratify]'s own
// precondition: it refuses without a parsed tfstate ("live-import needs a
// parsed tfstate to read, and none was given"), and a configuration under a
// live block has no AUTHORITATIVE state file to hand it - the cache under
// the data dir is not a migration source, and treating it as one would
// ratify stale identities.
// The ratification report is therefore unreachable from a plan, and this is
// the filtered view.
//
// So the anti-duplication rule is honoured a rung lower down, where it
// actually bites. Every verdict below is READ, never recomputed:
//
//   - adoptable / in the way comes from [statelessUnownedReport], the same
//     value the "Unowned" section renders;
//   - the content-matched adoptions come from [statelessForeignReport], the
//     same value the "Adoptable" section renders, hint and all;
//   - every other class comes from the projection's own omission reason;
//   - "can this carry a marker" is [markers.Taggable] over the schema this
//     run's provider served, which is the single implementation of
//     taggability in this repository. live-import's UNTAGGABLE verdict is
//     the same call: internal/live/liveimport/tags.go's taggable delegates
//     to markers.Taggable and nothing else, and so do
//     internal/live/stamp, internal/live/untag, internal/live/mv,
//     internal/live/discovery and internal/live/lint.
//
// Nothing here decides anything about a resource that some other stage has
// not already decided.

// statelessPlanView picks the renderer for a stateless run: the ordinary one,
// or GitHub issue #587's adoption-only one. Both satisfy
// [views.StatelessPlan], so this is the only branch either mode needs.
func statelessPlanView(view *views.View, adoptionOnly bool) views.StatelessPlan {
	if adoptionOnly {
		return views.NewStatelessAdoption(view)
	}
	return views.NewStatelessPlan(view)
}

// statelessAdoptionReport builds the adoption ledger for one run.
//
// projResult is the projection; foreignRep and unowned are the already-built
// view values for the two sections that carry adoption information today;
// schemas is the run's own managed-resource schema map, consulted only
// through [markers.Taggable]; estate is the settled estate name, empty when
// the run has none.
func statelessAdoptionReport(
	projResult *projection.Result,
	foreignRep views.StatelessForeign,
	unowned []views.StatelessUnowned,
	schemas map[string]providers.Schema,
	estate string,
	swept bool,
) views.StatelessAdoption {
	rep := views.StatelessAdoption{Estate: estate, Swept: swept}
	if projResult == nil {
		return rep
	}

	// The two indexes of already-decided adoption verdicts, keyed by the
	// declared instance address each one is about.
	unownedByAddr := make(map[string]views.StatelessUnowned, len(unowned))
	for _, u := range unowned {
		unownedByAddr[u.Addr] = u
	}
	candidateByAddr := make(map[string]views.StatelessBindCandidate, len(foreignRep.Candidates))
	for _, c := range foreignRep.Candidates {
		candidateByAddr[c.Addr] = c
	}

	// canCarryMarker answers the one question this file asks the schema, and
	// asks it through the single implementation. A type whose schema this
	// run never read answers false, which is the safe direction here: it
	// costs the row a marker-half tally line it might have earned, and never
	// claims a marker can be written where it cannot.
	canCarryMarker := func(typeName string) bool {
		schema, ok := schemas[typeName]
		return ok && markers.Taggable(schema.Block)
	}

	// Materialized first: the projection read it and this estate owns it.
	for _, addr := range projResult.Materialized {
		typeName := addr.Resource.Resource.Type
		rep.Rows = append(rep.Rows, views.StatelessAdoptionRow{
			Addr:           addr.String(),
			TypeName:       typeName,
			Class:          views.AdoptionMarked,
			CanCarryMarker: canCarryMarker(typeName),
		})
	}

	for _, om := range projResult.Omitted {
		addr := om.Addr.String()
		typeName := om.Addr.Resource.Resource.Type
		row := views.StatelessAdoptionRow{
			Addr:           addr,
			TypeName:       typeName,
			CanCarryMarker: canCarryMarker(typeName),
			Detail:         om.Detail,
		}

		// A content match by the foreign classifier is the strongest
		// adoption answer there is - it names the live object AND carries
		// the command - so it wins over whatever the omission said about
		// why the instance was not read. Checked first for that reason.
		if c, ok := candidateByAddr[addr]; ok {
			row.Class = views.AdoptionAdoptable
			row.LiveID = c.LiveID
			row.DisplayName = c.DisplayName
			row.Matched = c.Matched
			row.MarkerEstate = c.MarkerEstate
			row.MarkerAddress = c.MarkerAddress
			row.Hint = c.Hint
			row.Detail = ""
			rep.Rows = append(rep.Rows, row)
			continue
		}

		switch om.Reason {
		case projection.ReasonUnowned:
			// Every UNOWNED omission also appears in Unowned, which is
			// where the live resource's own details and the adoption
			// decision are. Fall back to IN_THE_WAY if that invariant ever
			// breaks, rather than reporting an unowned resource as absent.
			u, ok := unownedByAddr[addr]
			if !ok {
				row.Class = views.AdoptionInTheWay
				break
			}
			row.LiveID = u.LiveID
			row.HeldBy = u.HeldBy
			row.MarkerEstate = u.MarkerEstate
			row.MarkerAddress = u.MarkerAddress
			if u.MarkerEstate != "" {
				row.Class = views.AdoptionAdoptable
				row.Detail = ""
			} else {
				row.Class = views.AdoptionInTheWay
			}
		case projection.ReasonNeedsDiscovery:
			row.Class = views.AdoptionNoPath
		case projection.ReasonParentUnavailable:
			row.Class = views.AdoptionWaitsOnParent
		case projection.ReasonAbsent, projection.ReasonSuperseded:
			row.Class = views.AdoptionAbsent
			row.Detail = ""
		default:
			row.Class = views.AdoptionUnreadable
		}
		rep.Rows = append(rep.Rows, row)
	}

	sortAdoptionRows(rep.Rows)
	return rep
}

// sortAdoptionRows puts the ledger in address order, the order every other
// section in this package uses, so two runs over the same estate produce the
// same report.
func sortAdoptionRows(rows []views.StatelessAdoptionRow) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Addr < rows[j].Addr })
}
