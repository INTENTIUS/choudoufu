// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Outcome is what Approve did, or did not do, about one resource instance.
type Outcome string

const (
	// OutcomeStamped means the write landed: the provider's
	// ApplyResourceChange returned no error for a tags-only change adding
	// tofu-estate and tofu-address.
	OutcomeStamped Outcome = "STAMPED"

	// OutcomeAlreadyStamped means the live object already carried this
	// estate's markers naming this exact address. A no-op, on purpose: a
	// second live-import run over the same state is idempotent rather than
	// re-writing tags that already say the right thing.
	OutcomeAlreadyStamped Outcome = "ALREADY_STAMPED"

	// OutcomeSkipped means Ratify never made this resource eligible - its
	// Status was MISSING, UNADMITTED_TYPE or UNTAGGABLE - so Approve never
	// attempted a write. Detail repeats the ratification reason.
	OutcomeSkipped Outcome = "SKIPPED"

	// OutcomeFailed means a write was attempted and refused or failed: a
	// marker conflict with another estate or another address, a plan that
	// would replace the resource or move something besides tags, or a
	// provider error. Detail says which. A failure here is per-resource; the
	// run continues to the rest.
	OutcomeFailed Outcome = "FAILED"
)

// StampOutcome is what happened, or did not happen, to one resource
// instance during Approve.
type StampOutcome struct {
	Addr     addrs.AbsResourceInstance
	TypeName string
	Outcome  Outcome
	Detail   string
}

// StampReport is what one Approve call did, one outcome per Entry the
// Ratification carried - so a caller never has to cross-reference this
// report against the ratification report to see why a resource was not
// touched.
type StampReport struct {
	Estate   string
	Outcomes []StampOutcome
}

// Approve stamps this estate's markers - tofu-estate and tofu-address, and
// only those two - onto every resource Ratify found eligible (VERIFIED or
// DRIFTED), and reports one outcome for every resource Ratify reported at
// all.
//
// This is the only method in this package that writes to the live system.
// Each write is its own PlanResourceChange / ApplyResourceChange pair on one
// instance, the same tags-only pattern
// [github.com/intentius/choudoufu/internal/live/mv] uses for a rename: a plan
// that would replace the resource, or that would move anything besides its
// tags, is refused before ApplyResourceChange is ever called - for that
// resource. A refusal, or any other per-resource failure, does not stop the
// rest of the run: every remaining resource still gets its own attempt and
// its own outcome.
//
// Approve makes no call back into the tfstate this Ratification was built
// from: everything it needs (the live object, its private data, its
// identity, the provider connection) was already carried forward by
// [Ratify]. See the package doc, "What Ratify never does".
//
// GitHub issue #327: for every VERIFIED or DRIFTED entry, Approve also tries
// to record [projection.RecordResidueForInstance]'s residue classification,
// independent of whether the tag write itself succeeds - it is its own
// provider conversation, using the object Ratify already read rather than
// the (possibly still-unmarked) object the tag write produces. This is what
// closes the gap a migrate would otherwise leave open forever: without it, a
// residue-shaped attribute (one an SDKv2 resource's Read only ever preserves
// from its prior, never reads from the remote) has no residue record until
// a choudoufu apply first classifies it, so the FIRST live-plan after a
// clean migrate sees it null - a phantom update for an ordinary argument, a
// phantom REPLACE for a ForceNew one.
func (r *Ratification) Approve(ctx context.Context) (*StampReport, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	rep := &StampReport{Estate: r.Estate}
	for _, entry := range r.Entries {
		elig, ok := r.eligible[entry.Addr.String()]
		if !ok {
			rep.Outcomes = append(rep.Outcomes, StampOutcome{
				Addr:     entry.Addr,
				TypeName: entry.TypeName,
				Outcome:  OutcomeSkipped,
				Detail:   "Not stamped: " + entry.Detail,
			})
			continue
		}
		rep.Outcomes = append(rep.Outcomes, approveOne(ctx, r.Estate, entry.Addr, elig))
		diags = diags.Append(recordResidueFor(ctx, r.residueStore, entry.Addr, elig))
	}
	return rep, diags
}

// recordResidueFor is Approve's GitHub issue #327 half: it wraps elig's
// already-configured provider connection into the read
// [projection.RecordResidueForInstance] needs, called twice per its own doc
// comment, and turns a non-nil error into the same warning
// [writeBackResidue] already raises for the apply-time write path - reusing
// its Summary rather than minting a new one, since it is the identical
// situation ("an apply could not classify or store residue") reached from a
// second call site.
//
// A nil residueStore (no record_store block, or a nil Ratification built
// without one) makes this an immediate no-op, and so does elig itself being
// nil - callers that already skip a resource for OutcomeSkipped never reach
// here in the first place.
func recordResidueFor(ctx context.Context, store *projection.ResidueStore, addr addrs.AbsResourceInstance, e *eligible) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if store == nil || e == nil {
		return diags
	}
	read := func(prior cty.Value) (cty.Value, error) {
		resp := e.provider.ReadResource(ctx, providers.ReadResourceRequest{
			TypeName:   e.typeName,
			PriorState: prior,
			Private:    e.private,
			// The same null-of-dynamic every other read in this package and
			// projection's own residue path pass, and for the same reason:
			// the plugin client marshals ProviderMeta whenever the provider
			// declares a provider_meta schema, and a value with no type at
			// all panics the conformance check.
			ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
			PriorIdentity: e.identity,
		})
		if resp.Diagnostics.HasErrors() {
			return cty.NilVal, resp.Diagnostics.Err()
		}
		return resp.NewState, nil
	}
	if _, err := projection.RecordResidueForInstance(ctx, store, addr, e.schema, e.liveVal, read); err != nil {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, projection.SummaryResidueNotClassified, fmt.Sprintf(
			"No argument values were recorded for %s's residue: %s. Any argument the provider's own read does not return on its own will be proposed for update - or, for a ForceNew argument, replacement - on the first live-plan after this migration, until a choudoufu apply classifies it. Nothing in the live system was changed.",
			addr, err,
		)))
	}
	return diags
}

// approveOne is the tags-only write for one resource, from the object
// Ratify already read. See tags.go's package comment for why this mirrors
// mv's rewrite.go rather than calling it.
func approveOne(ctx context.Context, estate string, addr addrs.AbsResourceInstance, e *eligible) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: e.typeName}

	wantAddress := discovery.EscapeAddress(addr.String())
	if len([]rune(wantAddress)) > discovery.MaxAddressLen || !discovery.ValidMarkerAddress(wantAddress) {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The address %s does not escape to a well-formed tofu-address marker, so it can never be carried as a tag value (or set of continuation tag values). Nothing was written.", addr)
		return out
	}
	chunks := discovery.SplitAddress(wantAddress)

	tags, ok := tagsFromObject(e.schema, e.liveVal)
	if !ok {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("%s has no settable tags argument in the provider's schema. Nothing was written.", e.typeName)
		return out
	}

	gotEstate := tags[discovery.TagEstate]
	gotRaw, corrupt := discovery.GatherAddress(tags)
	gotAddress := discovery.EscapeAddress(gotRaw)
	// discovery.AddressMatches rather than a bare equality: a resource
	// already adopted under a marker a prior run wrote before issue #178
	// widened the for_each key grammar (only possible for a key containing
	// "@") still carries an address that names this same instance, and a
	// re-run of live-import over it has to see "already stamped" rather
	// than "carries a different address" - see that function's doc comment.
	addressMatches := gotAddress != "" && discovery.AddressMatches(gotAddress, addr.String())

	switch {
	case gotEstate == estate && addressMatches && !corrupt:
		out.Outcome = OutcomeAlreadyStamped
		out.Detail = "Already carries this estate's markers; nothing written."
		return out
	case gotEstate != "" && gotEstate != estate:
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Carries tofu-estate = %q, owned by another estate. A migration never adopts another estate's resource; nothing was written.", gotEstate)
		return out
	case corrupt:
		out.Outcome = OutcomeFailed
		out.Detail = "Already carries a tofu-address marker whose continuation tags (tofu-address-2, tofu-address-3, ...) have a gap in them, so this run cannot tell what address it names. See live/MARKERS.md, \"tofu-address continuation tags\"; a human has to resolve this before it can be adopted."
		return out
	case gotAddress != "" && !addressMatches:
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Already carries tofu-address = %q. Rewriting it here would be a rename, which is choudoufu live-mv's job, not a side effect of an import; nothing was written.", gotRaw)
		return out
	}

	desiredTags := make(map[string]string, len(tags)+1+len(chunks))
	for k, v := range tags {
		desiredTags[k] = v
	}
	desiredTags[discovery.TagEstate] = estate
	for i, chunk := range chunks {
		desiredTags[discovery.AddressTagKey(i)] = chunk
	}

	desired, err := withTags(e.schema.Block, e.liveVal, desiredTags)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The tags of this %s could not be replaced: %s.", e.typeName, err)
		return out
	}

	configVal := configValue(e.schema.Block, desired)
	proposed := objchange.ProposedNew(e.schema.Block, e.liveVal, configVal)

	planResp := e.provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
		TypeName:         e.typeName,
		PriorState:       e.liveVal,
		ProposedNewState: proposed,
		Config:           configVal,
		PriorPrivate:     e.private,
		// A null of the dynamic pseudo-type, not the zero cty.Value: the
		// plugin client marshals ProviderMeta whenever the provider declares
		// a provider_meta schema, and a value with no type at all panics the
		// conformance check. See mv's rewrite.go, same call.
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: e.identity,
	})
	if planResp.Diagnostics.HasErrors() {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The provider failed while planning the tag write: %s. Nothing was written.", planResp.Diagnostics.Err())
		return out
	}

	if len(planResp.RequiresReplace) > 0 {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Stamping this %s would require replacing it, according to the provider. A migration never destroys anything; nothing was written.", e.typeName)
		return out
	}
	planned := planResp.PlannedState
	if planned == cty.NilVal || planned.IsNull() {
		out.Outcome = OutcomeFailed
		out.Detail = "Planning the tag write produced no object at all. This is a provider bug; nothing was written."
		return out
	}
	if extra := changedOutsideTags(e.schema.Block, e.liveVal, planned); len(extra) > 0 {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("Stamping this %s would also change %s. Approve is a tags-only write; nothing was written. Run live-plan to see what else has drifted and resolve that first.", e.typeName, strings.Join(extra, ", "))
		return out
	}

	applyResp := e.provider.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:        e.typeName,
		PriorState:      e.liveVal,
		PlannedState:    planResp.PlannedState,
		Config:          configVal,
		PlannedPrivate:  planResp.PlannedPrivate,
		ProviderMeta:    cty.NullVal(cty.DynamicPseudoType),
		PlannedIdentity: planResp.PlannedIdentity,
	})
	if applyResp.Diagnostics.HasErrors() {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The provider failed while writing the tags: %s. The write may have partly landed; read the resource's tofu-address tag before deciding what to do next.", applyResp.Diagnostics.Err())
		return out
	}

	out.Outcome = OutcomeStamped
	out.Detail = "Wrote tofu-estate and tofu-address."
	newTags, newOK := tagsFromObject(e.schema, applyResp.NewState)
	newRaw, newCorrupt := discovery.GatherAddress(newTags)
	if !newOK || newCorrupt || discovery.EscapeAddress(newRaw) != wantAddress || newTags[discovery.TagEstate] != estate {
		// A mismatch here is a warning, not a failure: the write itself
		// reported no error, and some providers do not serve tags back on
		// the read that follows an apply (mv's e2e notes call this #5).
		out.Detail = "The write reported no error, but the object read back afterwards does not carry the new markers. Some providers do not serve tags back on a post-apply read; verify with the cloud's own API before relying on this."
	}
	return out
}
