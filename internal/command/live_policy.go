// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/untag"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// livePolicy resolves a live block's optional policy block into a
// fully populated [policy.Policy], for the setup pipeline in live_mode.go,
// live_plan.go and live_mv.go to construct alongside the estate name.
//
// live may be nil (no live block at all) or have a nil Policy (a live block
// with no policy block); both resolve to [policy.Build]'s default preset,
// which is today's fixed behavior. Every call site calls this only after
// [lint.CheckWith] has returned no issues, so by the time this runs, any
// verb here is already known valid for its quadrant.
//
// "and a delete quadrant, if any, is already known to carry a scope block"
// used to follow, and it was too broad in the way GitHub issue #116 names.
// checkLivePolicy requires a scope block for the undeclared_untagged
// quadrant only; undeclared_tagged's delete is the ordinary orphan sweep,
// scoped by the estate's own marker, and [policy.DefaultVerb] assigns it
// there - so the preset returned two lines above carries a Delete verb and
// a nil Scope, which is the common case rather than an edge one.
func livePolicy(live *configs.Live, estate string) *policy.Policy {
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

// liveOwnershipWith is the rule the projection admits live objects by:
// this run's estate, plus whatever marker discovery already proved ownership
// of, extended with any addresses a scoped reconciliation pass already
// vetted, and the policy itself, so [projection.Build] can apply GitHub issue
// #67's declared-quadrant verbs. See internal/live/projection's
// checkOwnership.
//
// Never nil, because "this run established no estate" is a verdict about
// ownership (nothing can be verified) rather than an absence of one.
//
// It superseded a plain liveOwnership when #67's matrix landed; that one
// survived unreferenced until golangci-lint's unused check was actually
// reachable (#148).
func liveOwnershipWith(estate string, disco *discovery.Result, pol *policy.Policy, extraVerified map[string]bool) *projection.Ownership {
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

// livePolicyReconcile runs GitHub issue #67's undeclared_untagged =
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
//
// scope is this run's -target / -exclude filtering, from
// [liveTargetScope] and nil for every untargeted run - GitHub issue
// #1257, filed by #1203's audit. Both things this function produces are
// narrowed by it, through the one predicate, and the coupling is the
// point: [discovery.ReconcileResult.Proposable] is the set that becomes
// destroy proposals AND the set the threshold guard counts, so a run can
// neither be refused for a population it will not touch nor destroy a
// population no threshold checked. What is NOT narrowed is the roster
// itself, which the policy report still renders in full.
func livePolicyReconcile(ctx context.Context, estate string, pol *policy.Policy, provs *projectionProviders, discoProvider addrs.AbsProviderConfig, scope identity.Scope) (rec *discovery.ReconcileResult, extra []identity.Resolution, verified map[string]bool, diags tfdiags.Diagnostics) {
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
		Scope:    scope,
	})
	diags = diags.Append(recDiags)
	if recDiags.HasErrors() {
		return rec, nil, nil, diags
	}
	// The count is Proposable's, not the roster's, for the reason
	// [discovery.ReconcileResult.ThresholdExceeded] records: the threshold
	// bounds how many live objects THIS RUN will destroy. On a narrowed run
	// that is a smaller number than the account holds, and on an untargeted
	// one the two sets are the same, so nothing about today's behavior
	// moves.
	if rec.ThresholdExceeded {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Scoped account reconciliation roster exceeds its threshold",
			fmt.Sprintf(
				"undeclared_untagged = \"delete\" found %d resource(s) to delete, over the threshold of %d. Review the roster this run printed, and raise policy.threshold deliberately once it has been reviewed - this guard exists so a first scoped delete is never wider than the operator expected.",
				len(rec.Proposable()), rec.Threshold),
		))
		return rec, nil, nil, diags
	}

	extra, verified = reconcileResolutions(rec)
	return rec, extra, verified, diags
}

// reconcileResolutions turns the candidates one scoped reconciliation pass
// will actually act on into the synthetic resolutions and verified
// addresses the caller merges in before the projection is built.
//
// It reads [discovery.ReconcileResult.Proposable] rather than the roster,
// which is GitHub issue #1257's other half and the half that must not be
// separated from the threshold guard above: a candidate this run's
// -target / -exclude withheld is shown in the report and destroyed by
// nothing. Split out from [livePolicyReconcile] so it can be tested
// without a configured provider - see TestReconcileResolutionsHonourTheTargetScope.
func reconcileResolutions(rec *discovery.ReconcileResult) ([]identity.Resolution, map[string]bool) {
	proposable := rec.Proposable()
	extra := make([]identity.Resolution, 0, len(proposable))
	verified := make(map[string]bool, len(proposable))
	for _, c := range proposable {
		extra = append(extra, identity.Resolution{
			Addr:       c.Addr,
			Class:      identity.ClassConcrete,
			ImportID:   c.ImportID,
			Identity:   c.Identity,
			Undeclared: true,
		})
		verified[c.Addr.String()] = true
	}
	return extra, verified
}

// livePolicyTagKey reads a policy's TagKey, nil-safely: a run with no
// policy at all (livePolicy never ran, or ran against a nil config)
// releases nothing, since there is no policy to have named an untag verb in
// the first place.
func livePolicyTagKey(pol *policy.Policy) string {
	if pol == nil {
		return ""
	}
	return pol.TagKey
}

// livePolicyTagValue is [livePolicyTagKey]'s value half: what the
// key must carry for a resource to count as this estate's.
func livePolicyTagValue(pol *policy.Policy) string {
	if pol == nil {
		return ""
	}
	return pol.TagValue
}

// livePolicyReport assembles GitHub issue #67's policy report for the
// stateless plan view, from every stage that touched a non-default verb:
// the projection's declared-quadrant outcomes, discovery's withheld
// undeclared_tagged orphans, and the scoped reconciliation pass's roster.
//
// The fourth source is the node writer's own record of which instances a
// declared_tagged = "untag" verb actually released a marker key from
// ([projection.NodeResolver.UntagReleases], GitHub issue #1002), which fills
// [views.LivePolicyReport.Untagged]. It arrives separately from the
// other three because it does not exist yet when they do: the projection,
// the sweep and the reconciliation pass all finish before the plan walk,
// and the release happens inside it, one
// [projection.NodeResolver.AdjustConfigValue] call per instance. So each
// pipeline calls this function twice - once before the walk with released
// nil, once after it with released alone - and the view prints the untag
// section directly above the plan it describes. A caller that passes
// released before the walk gets an empty list for every estate, which is
// what this section rendered from 2026-08-25 (when the node writer took over
// from internal/live/stamp, whose Result.Untagged used to feed it) until
// #1002: GitHub issue #644 deleted the unreachable stamp implementation and
// GitHub issue #949 ported the suppression without the record.
//
// Declared and Untagged answer different questions and are expected to
// differ. Declared names every instance the verb governs. Untagged names the
// ones a key was really withheld from, so an instance whose configuration
// hand-writes the key, or that a -target kept out of the walk, is in the
// first list and not the second.
func livePolicyReport(projResult *projection.Result, disco *discovery.Result, rec *discovery.ReconcileResult, released []projection.UntagRelease) views.LivePolicyReport {
	var rep views.LivePolicyReport

	if projResult != nil {
		for _, p := range projResult.Policy {
			rep.Declared = append(rep.Declared, views.LivePolicyDeclared{
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
			rep.Withheld = append(rep.Withheld, views.LivePolicyWithheld{
				TypeName:    o.TypeName,
				LiveID:      o.ImportID,
				DisplayName: o.DisplayName,
				Marker:      o.Normalized,
				Verb:        string(o.PolicyVerb),
				Withheld:    o.Withheld,
			})
		}
	}

	for _, u := range released {
		rep.Untagged = append(rep.Untagged, views.LiveUntagged{
			Addr:         u.Addr.String(),
			Key:          u.Key,
			EstateMarker: u.EstateMarker(),
		})
	}

	if rec != nil {
		rep.Reconcile.Ran = true
		rep.Reconcile.Threshold = rec.Threshold
		rep.Reconcile.ThresholdExceeded = rec.ThresholdExceeded
		for _, c := range rec.Roster {
			rep.Reconcile.Roster = append(rep.Reconcile.Roster, views.LiveReconcileCandidate{
				TypeName:    c.TypeName,
				LiveID:      c.ImportID,
				DisplayName: c.DisplayName,
				Withheld:    c.Withheld,
			})
		}
		for _, g := range rec.Gaps {
			rep.Reconcile.Gaps = append(rep.Reconcile.Gaps, views.LiveReconcileGap{
				TypeName: g.TypeName,
				Reason:   g.Reason,
				Detail:   g.Detail,
			})
		}
	}

	return rep
}

// untagGroup is the untag verb's work for one provider configuration: the
// targets whose sweep pass listed through it, which is the only
// configuration that can reach them again (GitHub issue #1657).
type untagGroup struct {
	Provider addrs.AbsProviderConfig
	Targets  []untag.Target
}

// liveUntagTargets narrows discovery's withheld undeclared_tagged
// orphans to the ones this run's policy actually named "untag" - not
// "keep" or "report", which are also withheld from the sweep but have
// nothing for [untag.Release] to do - and turns each into the identity
// evidence [untag.Release] needs to import and read it fresh, grouped by
// the provider configuration that found it ([discovery.OwnedResource.
// Provider]). Groups are in provider-address order, targets in discovery's
// own order. See [liveRunner.AfterApply] for why this runs during
// PriorState and the result is only acted on later, from AfterApply.
func liveUntagTargets(disco *discovery.Result) []untagGroup {
	if disco == nil {
		return nil
	}
	byKey := make(map[string]int)
	var out []untagGroup
	for _, o := range disco.Orphans {
		if o.PolicyVerb != policy.Untag {
			continue
		}
		key := untagGroupKey(o.Provider)
		i, ok := byKey[key]
		if !ok {
			i = len(out)
			byKey[key] = i
			out = append(out, untagGroup{Provider: o.Provider})
		}
		out[i].Targets = append(out[i].Targets, untag.Target{
			TypeName:    o.TypeName,
			ImportID:    o.ImportID,
			Identity:    o.Identity,
			Marker:      o.Normalized,
			DisplayName: o.DisplayName,
			// The configuration that found it (GitHub issue #1742).
			ProviderType: o.Provider.Provider.Type,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return untagGroupKey(out[i].Provider) < untagGroupKey(out[j].Provider)
	})
	return out
}

// untagGroupKey is a provider configuration's address, or "" for the zero
// value: an orphan no caller attributed to a pass.
func untagGroupKey(p addrs.AbsProviderConfig) string {
	if p.Provider.Type == "" {
		return ""
	}
	return p.String()
}

// untagTargetList names targets on one line, for a diagnostic.
func untagTargetList(targets []untag.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.String()
	}
	return strings.Join(names, ", ")
}

// liveUntagCluster is the cluster client [untag.Release] releases a
// manifest-shape orphan through (GitHub issue #1656): the one the marker
// sweep built for the provider configuration that found the orphan, which
// is the one its group releases through (GitHub issue #1657), or nil when
// that configuration built none - not a Kubernetes configuration, or one
// this run could not connect with - in which case
// the release refuses such a target by name and touches nothing. It never
// borrows another configuration's client: a label release sent to the
// wrong cluster is a write on an object this run never read.
func liveUntagCluster(sweepers map[string]kubesweep.Sweeper, provider addrs.AbsProviderConfig) kubesweep.LabelReleaser {
	sweeper := sweepers[providerCacheKey(provider)]
	if sweeper == nil {
		return nil
	}
	releaser, _ := sweeper.(kubesweep.LabelReleaser)
	if c, isClient := releaser.(*kubesweep.Client); isClient && c == nil {
		// A typed nil in an interface is not a nil interface; see
		// live_import_kubernetes.go's LabelPatcher for the same guard.
		return nil
	}
	return releaser
}

// liveReleasedReport turns one [untag.Result] - the apply-time record
// of what [liveRunner.AfterApply] actually did to every
// undeclared_tagged = "untag" target - into the stateless view's own
// shape. Nil-safe: AfterApply skips calling [untag.Release] at all when
// there was nothing to release, and this function mirrors that by
// returning an empty report.
func liveReleasedReport(result *untag.Result) views.LivePolicyReport {
	var rep views.LivePolicyReport
	if result == nil {
		return rep
	}
	for _, o := range result.Outcomes {
		rep.Released = append(rep.Released, views.LiveReleased{
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
