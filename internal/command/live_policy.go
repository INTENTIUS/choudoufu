// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/untag"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// statelessPolicy resolves a live block's optional policy block into a
// fully populated [policy.Policy], for the setup pipeline in live_mode.go,
// live_plan.go and live_mv.go to construct alongside the estate name.
//
// live may be nil (no live block at all) or have a nil Policy (a live block
// with no policy block); both resolve to [policy.Build]'s default preset,
// which is today's fixed behavior. Every call site calls this only after
// [lint.CheckWith] has returned no issues, so by the time this runs, any
// verb here is already known valid for its quadrant, and a delete quadrant,
// if any, is already known to carry a scope block - see
// internal/live/lint's checkLivePolicy.
func statelessPolicy(live *configs.Live, estate string) *policy.Policy {
	if live == nil || live.Policy == nil {
		return policy.Build(nil, estate)
	}

	lp := live.Policy
	raw := &policy.Raw{
		DeclaredTagged:        lp.DeclaredTagged,
		DeclaredTaggedSet:     lp.DeclaredTaggedSet,
		DeclaredUntagged:      lp.DeclaredUntagged,
		DeclaredUntaggedSet:   lp.DeclaredUntaggedSet,
		UndeclaredTagged:      lp.UndeclaredTagged,
		UndeclaredTaggedSet:   lp.UndeclaredTaggedSet,
		UndeclaredUntagged:    lp.UndeclaredUntagged,
		UndeclaredUntaggedSet: lp.UndeclaredUntaggedSet,
		TagKey:                lp.TagKey,
		TagKeySet:             lp.TagKeySet,
		TagValue:              lp.TagValue,
		TagValueSet:           lp.TagValueSet,
		Threshold:             lp.Threshold,
		ThresholdSet:          lp.ThresholdSet,
	}
	if lp.Scope != nil {
		raw.Scope = &policy.RawScope{
			Services: lp.Scope.Services,
			Types:    lp.Scope.Types,
			Regions:  lp.Scope.Regions,
		}
	}
	return policy.Build(raw, estate)
}

// statelessOwnershipWith is [statelessOwnership] plus policy: the same
// verified set, extended with any addresses a scoped reconciliation pass
// already vetted, and the policy itself, so [projection.Build] can apply
// GitHub issue #67's declared-quadrant verbs. See
// internal/live/projection's checkOwnership.
func statelessOwnershipWith(estate string, disco *discovery.Result, pol *policy.Policy, extraVerified map[string]bool) *projection.Ownership {
	verified := disco.MarkerVerified()
	if len(extraVerified) > 0 {
		if verified == nil {
			verified = make(map[string]bool, len(extraVerified))
		}
		for k := range extraVerified {
			verified[k] = true
		}
	}
	return &projection.Ownership{
		Estate:   estate,
		Verified: verified,
		Policy:   pol,
	}
}

// statelessPolicyReconcile runs GitHub issue #67's undeclared_untagged =
// "delete" scoped account reconciliation, when the resolved policy asks for
// one, and turns its roster into the resolutions and verified addresses the
// caller must merge in before the projection is built - reusing the same
// "no configuration, no destroy needed" mechanism a swept orphan already
// uses (see [discovery.Result.MarkerVerified] and the sweep's own
// classifyOrphans): a resolution with no matching declared address is an
// ordinary orphan to the plan engine, and needs no synthetic configuration
// to be proposed for destruction.
//
// Diagnostics with errors mean the run must stop before anything is
// materialized: either the reconciliation pass itself failed, or its
// roster exceeded the policy's threshold, in which case rec is still
// returned (with rec.ThresholdExceeded set) so the caller can render the
// roster in the same report that explains the refusal.
func statelessPolicyReconcile(ctx context.Context, estate string, pol *policy.Policy, provs *statelessProviders, discoProvider addrs.AbsProviderConfig) (rec *discovery.ReconcileResult, extra []identity.Resolution, verified map[string]bool, diags tfdiags.Diagnostics) {
	if pol == nil || pol.UndeclaredUntagged != policy.Delete {
		return nil, nil, nil, diags
	}
	if estate == "" || discoProvider.Provider.Type == "" {
		// Nothing settled to reconcile against - the same graceful
		// degradation discovery itself makes when it has no estate name or
		// no provider to list through.
		return nil, nil, nil, diags
	}

	provider, err := provs.ConfiguredProvider(ctx, discoProvider)
	if err != nil {
		return nil, nil, nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Provider unavailable for scoped account reconciliation",
			fmt.Sprintf("undeclared_untagged = \"delete\" needs provider %s to list live resources with, which could not be used: %s.", discoProvider, err),
		))
	}

	rec, recDiags := discovery.Reconcile(ctx, discovery.ReconcileRequest{
		Estate:   estate,
		Provider: provider,
		Region:   provs.region(discoProvider),
		Policy:   pol,
	})
	diags = diags.Append(recDiags)
	if recDiags.HasErrors() {
		return rec, nil, nil, diags
	}
	if rec.ThresholdExceeded {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Scoped account reconciliation roster exceeds its threshold",
			fmt.Sprintf(
				"undeclared_untagged = \"delete\" found %d resource(s) to delete, over the threshold of %d. Review the roster this run printed, and raise policy.threshold deliberately once it has been reviewed - this guard exists so a first scoped delete is never wider than the operator expected.",
				len(rec.Roster), rec.Threshold),
		))
		return rec, nil, nil, diags
	}

	extra = make([]identity.Resolution, 0, len(rec.Roster))
	verified = make(map[string]bool, len(rec.Roster))
	for _, c := range rec.Roster {
		extra = append(extra, identity.Resolution{
			Addr:       c.Addr,
			Class:      identity.ClassConcrete,
			ImportID:   c.ImportID,
			Identity:   c.Identity,
			Undeclared: true,
		})
		verified[c.Addr.String()] = true
	}
	return rec, extra, verified, diags
}

// statelessPolicyTagKey reads a policy's TagKey, nil-safely: a run with no
// policy at all (statelessPolicy never ran, or ran against a nil config)
// releases nothing, since there is no policy to have named an untag verb in
// the first place.
func statelessPolicyTagKey(pol *policy.Policy) string {
	if pol == nil {
		return ""
	}
	return pol.TagKey
}

// statelessPolicyUntagMap turns the declared-quadrant policy outcomes a
// projection recorded into the block-address-to-tag-key map
// [stamp.Request.PolicyUntag] needs: every instance whose verb was
// [policy.Untag], keyed by its resource block's address (stamping works at
// block granularity - see [stamp.Request.PolicyUntag]'s own doc comment for
// why).
//
// The key is built as [addrs.ConfigResource], module-qualified, to match
// the address stamping's own PolicyUntag lookup uses (see
// internal/live/stamp's markerObject caller): 59b's static-module
// traversal means a declared_tagged instance's block can be inside a
// module, and collapsing that to the bare resource address the way a
// root-only build once could would either miscount two same-named blocks
// in different modules as one, or simply never match stamp's own key at
// all.
func statelessPolicyUntagMap(outcomes []projection.PolicyOutcome, tagKey string) map[string]string {
	if len(outcomes) == 0 {
		return nil
	}
	out := make(map[string]string)
	for _, o := range outcomes {
		if o.Verb != policy.Untag {
			continue
		}
		cr := addrs.ConfigResource{Module: o.Addr.Module.Module(), Resource: o.Addr.Resource.Resource}
		out[cr.String()] = tagKey
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// statelessPolicyReport assembles GitHub issue #67's policy report for the
// stateless plan view, from every stage that touched a non-default verb:
// the projection's declared-quadrant outcomes, discovery's withheld
// undeclared_tagged orphans, marker stamping's released tag keys, and the
// scoped reconciliation pass's roster.
func statelessPolicyReport(projResult *projection.Result, disco *discovery.Result, stampRes *stamp.Result, rec *discovery.ReconcileResult) views.StatelessPolicyReport {
	var rep views.StatelessPolicyReport

	if projResult != nil {
		for _, p := range projResult.Policy {
			rep.Declared = append(rep.Declared, views.StatelessPolicyDeclared{
				Addr:     p.Addr.String(),
				TypeName: p.TypeName,
				Tagged:   p.Tagged,
				Verb:     string(p.Verb),
			})
		}
	}

	if disco != nil {
		for _, o := range disco.Orphans {
			if o.PolicyVerb == "" || o.Removal {
				continue
			}
			rep.Withheld = append(rep.Withheld, views.StatelessPolicyWithheld{
				TypeName:    o.TypeName,
				LiveID:      o.ImportID,
				DisplayName: o.DisplayName,
				Marker:      o.Normalized,
				Verb:        string(o.PolicyVerb),
				Withheld:    o.Withheld,
			})
		}
	}

	if stampRes != nil {
		for _, u := range stampRes.Untagged {
			rep.Untagged = append(rep.Untagged, views.StatelessUntagged{
				Addr:         u.Addr.String(),
				Key:          u.Key,
				EstateMarker: u.EstateMarker,
			})
		}
	}

	if rec != nil {
		rep.Reconcile.Ran = true
		rep.Reconcile.Threshold = rec.Threshold
		rep.Reconcile.ThresholdExceeded = rec.ThresholdExceeded
		for _, c := range rec.Roster {
			rep.Reconcile.Roster = append(rep.Reconcile.Roster, views.StatelessReconcileCandidate{
				TypeName:    c.TypeName,
				LiveID:      c.ImportID,
				DisplayName: c.DisplayName,
			})
		}
		for _, g := range rec.Gaps {
			rep.Reconcile.Gaps = append(rep.Reconcile.Gaps, views.StatelessReconcileGap{
				TypeName: g.TypeName,
				Reason:   g.Reason,
				Detail:   g.Detail,
			})
		}
	}

	return rep
}

// statelessUntagTargets narrows discovery's withheld undeclared_tagged
// orphans to the ones this run's policy actually named "untag" - not
// "keep" or "report", which are also withheld from the sweep but have
// nothing for [untag.Release] to do - and turns each into the identity
// evidence [untag.Release] needs to import and read it fresh. See
// [statelessRunner.AfterApply] for why this runs during PriorState and the
// result is only acted on later, from AfterApply.
func statelessUntagTargets(disco *discovery.Result) []untag.Target {
	if disco == nil {
		return nil
	}
	var out []untag.Target
	for _, o := range disco.Orphans {
		if o.PolicyVerb != policy.Untag {
			continue
		}
		out = append(out, untag.Target{
			TypeName:    o.TypeName,
			ImportID:    o.ImportID,
			Identity:    o.Identity,
			Marker:      o.Normalized,
			DisplayName: o.DisplayName,
		})
	}
	return out
}

// statelessReleasedReport turns one [untag.Result] - the apply-time record
// of what [statelessRunner.AfterApply] actually did to every
// undeclared_tagged = "untag" target - into the stateless view's own
// shape. Nil-safe: AfterApply skips calling [untag.Release] at all when
// there was nothing to release, and this function mirrors that by
// returning an empty report.
func statelessReleasedReport(result *untag.Result) views.StatelessPolicyReport {
	var rep views.StatelessPolicyReport
	if result == nil {
		return rep
	}
	for _, o := range result.Outcomes {
		rep.Released = append(rep.Released, views.StatelessReleased{
			TypeName:     o.TypeName,
			LiveID:       o.ImportID,
			DisplayName:  o.DisplayName,
			Marker:       o.Marker,
			Key:          result.Key,
			EstateMarker: result.Key == markers.TagEstate,
			OK:           o.OK,
			Detail:       o.Detail,
		})
	}
	return rep
}
