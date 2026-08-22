// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
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

	// OutcomeRecorded means this instance is record-backed - its value is
	// its identity, and no live object exists to carry a tag - and Approve
	// seeded the estate's record store with the object the state recorded.
	// This is the whole migration for such a resource, and it is what GitHub
	// issue #340 found missing: without it a migrated random_pet,
	// null_resource, terraform_data or local_file has its generated value
	// nowhere, and the first live-plan after the migration proposes creating
	// it from scratch.
	OutcomeRecorded Outcome = "RECORDED"

	// OutcomeSensitivityRecorded means the record store already held this
	// exact object, written before this fork persisted which of an object's
	// attributes are sensitive, and Approve rewrote it to carry them. The
	// value, the private bytes and the status are unchanged; only the
	// recorded sensitivity moved. [projection.SeedMarksAdded] is the case.
	//
	// It is its own outcome rather than a [OutcomeRecorded] with different
	// Detail text, which is GitHub issue #344's own stated reason for
	// SeedRecordForInstance returning a [projection.SeedResult] instead of a
	// bool: the report counts by outcome, so folding the upgrade into
	// RECORDED makes a re-migration of fifty long-standing records print
	// "50 resource(s) newly recorded" - the exact miscount the enumeration
	// was introduced to end, left in place because only the per-row Detail
	// was changed. Pinned by
	// TestSensitivityUpgradeIsNotCountedAsNewlyRecorded.
	OutcomeSensitivityRecorded Outcome = "SENSITIVITY_RECORDED"

	// OutcomeAlreadyRecorded means the record store already held exactly
	// this object for this address. A no-op, on purpose, and the same
	// idempotence [OutcomeAlreadyStamped] gives the tag write: a second
	// live-import run over the same state file writes nothing twice.
	OutcomeAlreadyRecorded Outcome = "ALREADY_RECORDED"

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

// notATagsOnlyPlan is the whole of what stands between a planned tag write
// and an apply: the sentence to report if this plan must not be applied, or
// "" if it may be.
//
// The three refusals are the ones approveOne has always made inline, lifted
// into one function because a stamp now plans more than once - see
// [syntheticConfigs] - and a second candidate configuration must be judged by
// exactly the same rules as the first, not by a copy of them that drifts.
// Nothing here is new but the named attribute paths on the replacement
// refusal, which internal/live/mv's checkPlan has always printed.
func notATagsOnlyPlan(block *configschema.Block, prior cty.Value, typeName string, resp providers.PlanResourceChangeResponse) string {
	if resp.Diagnostics.HasErrors() {
		return fmt.Sprintf("The provider failed while planning the tag write: %s. Nothing was written.", resp.Diagnostics.Err())
	}
	if len(resp.RequiresReplace) > 0 {
		paths := make([]string, 0, len(resp.RequiresReplace))
		for _, p := range resp.RequiresReplace {
			paths = append(paths, tfdiags.FormatCtyPath(p))
		}
		sort.Strings(paths)
		return fmt.Sprintf("Stamping this %s would require replacing it, according to the provider (%s). A migration never destroys anything; nothing was written.", typeName, strings.Join(paths, ", "))
	}
	if resp.PlannedState == cty.NilVal || resp.PlannedState.IsNull() {
		return "Planning the tag write produced no object at all. This is a provider bug; nothing was written."
	}
	if extra := changedOutsideTags(block, prior, resp.PlannedState); len(extra) > 0 {
		return fmt.Sprintf("Stamping this %s would also change %s. Approve is a tags-only write; nothing was written. Run live-plan to see what else has drifted and resolve that first.", typeName, strings.Join(extra, ", "))
	}
	return ""
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
//
// GitHub issue #340: and for every RECORD-BACKED entry, which by definition
// has no live object and therefore never reaches a tag write at all, Approve
// seeds the estate's record store from the state's own object instead - see
// [recordOne]. That is the whole of what migrating such a resource means,
// and while it did not happen a migrated estate lost every generated value
// it had: the run reported success, and the next live-plan proposed creating
// random_pet, null_resource, terraform_data and local_file from nothing.
//
// GitHub issue #341: and the residue write above is NOT limited to the
// entries that reach a tag write. An admitted resource whose provider schema
// has no tags argument is still a real cloud object with real arguments, and
// while its residue was unreachable, a clean migrate of one left an estate
// proposing the same phantom update forever - measured on
// aws_route53_record.allow_overwrite, over an untaggable population of 342 of
// [identity.DefaultTable]'s 1040 rows. Such an entry keeps
// [OutcomeSkipped], because what was skipped is the marker write and that is
// what this axis reports; no count this command prints moves as a result.
func (r *Ratification) Approve(ctx context.Context) (*StampReport, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	rep := &StampReport{Estate: r.Estate}
	for _, entry := range r.Entries {
		if rec, ok := r.recordable[entry.Addr.String()]; ok {
			// Issue #340. A record-backed instance is never also eligible
			// (see [recordable]), so this branch and the one below are
			// alternatives, not a sequence.
			rep.Outcomes = append(rep.Outcomes, recordOne(ctx, r.recordStore, r.recordKeyPrefix, entry.Addr, rec))
			continue
		}
		elig, ok := r.eligible[entry.Addr.String()]
		if !ok {
			rep.Outcomes = append(rep.Outcomes, StampOutcome{
				Addr:     entry.Addr,
				TypeName: entry.TypeName,
				Outcome:  OutcomeSkipped,
				Detail:   "Not stamped: " + entry.Detail,
			})
			// GitHub issue #341. Not stamped is not the same as nothing to
			// do. An admitted, untaggable resource is a real cloud object
			// with real arguments, and a migration is the only moment
			// anything can classify the ones its provider never reads back.
			// The outcome stays SKIPPED on purpose - the marker write is what
			// was skipped, and that is what this axis reports - so no count
			// this run prints moves.
			diags = diags.Append(recordResidueFor(ctx, r.residueStore, entry.Addr, r.residuable[entry.Addr.String()]))
			continue
		}
		rep.Outcomes = append(rep.Outcomes, approveOne(ctx, r.Estate, entry.Addr, elig))
		diags = diags.Append(recordResidueFor(ctx, r.residueStore, entry.Addr, &elig.residuable))
	}

	// GitHub issue #349: carry the state file's root output values across
	// too. They are the one thing in a stock state file that had no carrier
	// on this side, so every migrated estate's next stateless plan called
	// every output new. Nothing about it is per-entry, which is why it sits
	// after the loop rather than in it, and nothing about it can fail a
	// migration - see [projection.WriteRootOutputValues], which reports
	// nothing and logs what it could not write.
	projection.WriteRootOutputValues(ctx, r.rootOutputStore, r.rootOutputs)

	return rep, diags
}

// recordOne is Approve's GitHub issue #340 half: the migration of one
// record-backed instance, which is a single write to the estate's record
// store and no cloud call at all.
//
// It is the exact counterpart of [approveOne] - one carrier per resource,
// one write per resource, one outcome per resource - and the reason a
// migration needs both is that "which live object is this" and "what value
// is this" are answered by different carriers. A tag answers the first. For
// the fifteen types [recordBackedType] covers today there is no first
// question to answer, and the record is the only answer to the second.
//
// A nil store cannot be reached from Approve (Ratify never builds a
// *recordable without one), but it is handled anyway rather than trusted:
// [projection.SeedRecordForInstance] treats it as a no-op, which would
// report ALREADY_RECORDED, so the guard here says the true thing instead.
func recordOne(ctx context.Context, store staterecord.Store, keyPrefix string, addr addrs.AbsResourceInstance, rec *recordable) StampOutcome {
	out := StampOutcome{Addr: addr, TypeName: rec.typeName}
	if store == nil {
		out.Outcome = OutcomeSkipped
		out.Detail = fmt.Sprintf("Not recorded: %s is record-backed and this configuration declares no record_store, so there is nowhere to keep its value.", rec.typeName)
		return out
	}

	seeded, err := projection.SeedRecordForInstance(ctx, store, keyPrefix, addr, rec.value, rec.private, rec.status)
	switch {
	case err != nil:
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The record store could not be seeded for this %s: %s. Nothing was written, and the first live-plan after this migration will propose creating it.", rec.typeName, err)
	case seeded == projection.SeedMarksAdded:
		// GitHub issue #344's case, and its own outcome rather than a
		// RECORDED with different Detail: the report the operator reads
		// counts by Outcome and prints those counts in one summary line, so
		// an upgrade filed under RECORDED is counted as a newly recorded
		// resource no matter what the row beside it says. A store write, and
		// not a new record.
		out.Outcome = OutcomeSensitivityRecorded
		out.Detail = "The record store already held this exact object, recorded before choudoufu persisted which of its attributes are sensitive; rewrote it to carry them. The value itself is unchanged."
	case seeded.Wrote():
		out.Outcome = OutcomeRecorded
		out.Detail = "Wrote the state's own object into this estate's record store; there is no live object to tag."
	default:
		out.Outcome = OutcomeAlreadyRecorded
		out.Detail = "The record store already holds exactly this object; nothing written."
	}
	return out
}

// recordResidueFor is Approve's GitHub issue #327 half: it wraps the
// carrier's already-configured provider connection into the read
// [projection.RecordResidueForInstance] needs, called twice per its own doc
// comment, and turns a non-nil error into the same warning
// [writeBackResidue] already raises for the apply-time write path - reusing
// its Summary rather than minting a new one, since it is the identical
// situation ("an apply could not classify or store residue") reached from a
// second call site.
//
// It takes a [residuable] and not an [eligible], which is GitHub issue #341's
// whole fix in one signature: recording what an estate sent has nothing to do
// with whether the thing it sent it to has a tags argument. Both call sites
// in [Ratification.Approve] reach here now - the stamped one through the
// [residuable] its *eligible embeds, and the untaggable one through its own.
//
// A nil residueStore (no record_store block, or a nil Ratification built
// without one) makes this an immediate no-op, and so does e itself being nil
// - an instance with no carrier at all, such as one whose type is unadmitted
// or whose state holds no current object, has nothing to classify from.
func recordResidueFor(ctx context.Context, store *projection.ResidueStore, addr addrs.AbsResourceInstance, e *residuable) tfdiags.Diagnostics {
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
	if _, err := projection.RecordResidueForInstance(ctx, store, addr, e.schema, e.applied, read); err != nil {
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

	tags, ok := tagsFromObject(e.schema, e.applied)
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

	desired, err := withTags(e.schema.Block, e.applied, desiredTags)
	if err != nil {
		out.Outcome = OutcomeFailed
		out.Detail = fmt.Sprintf("The tags of this %s could not be replaced: %s.", e.typeName, err)
		return out
	}

	// The resource being stamped has no configuration - that is what a
	// migration is - so one is synthesized, and there is more than one honest
	// answer to "what would the HCL have said about the arguments the
	// provider fills in for itself". [syntheticConfigs] offers them least
	// claim first; each is planned in turn and the first plan that is a
	// clean tags-only change wins. [notATagsOnlyPlan] is what "clean" means,
	// and it is the same three guards that already stood between a plan and
	// an apply - so trying a second configuration widens what can be written
	// without widening what may be written. See tags.go's configClaim.
	var (
		configVal cty.Value
		planResp  providers.PlanResourceChangeResponse
		refusal   string
	)
	for _, candidate := range syntheticConfigs(e.schema.Block, desired) {
		resp := e.provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
			TypeName:         e.typeName,
			PriorState:       e.applied,
			ProposedNewState: objchange.ProposedNew(e.schema.Block, e.applied, candidate),
			Config:           candidate,
			PriorPrivate:     e.private,
			// A null of the dynamic pseudo-type, not the zero cty.Value: the
			// plugin client marshals ProviderMeta whenever the provider declares
			// a provider_meta schema, and a value with no type at all panics the
			// conformance check. See mv's rewrite.go, same call.
			ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
			PriorIdentity: e.identity,
		})
		if why := notATagsOnlyPlan(e.schema.Block, e.applied, e.typeName, resp); why != "" {
			// Kept, not returned: if no candidate produces a clean plan, the
			// LAST refusal is the one reported, so the message an operator
			// reads is the one for the configuration that asserts most - the
			// only one this path used to send at all.
			refusal = why
			continue
		}
		configVal, planResp, refusal = candidate, resp, ""
		break
	}
	if refusal != "" {
		out.Outcome = OutcomeFailed
		out.Detail = refusal
		return out
	}

	applyResp := e.provider.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:        e.typeName,
		PriorState:      e.applied,
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
