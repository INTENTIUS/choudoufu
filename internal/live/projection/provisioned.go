// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #353's namespace: the ONE BIT that says a
// create-time provisioner failed on a resource instance whose prior state
// is otherwise read back out of the cloud.
//
// # Why one bit and not a fingerprint
//
// Stock OpenTofu has exactly one piece of provisioner memory, and it is not
// a memory of the provisioner's content. When a create-time provisioner
// fails, node_resource_apply_instance.go's maybeTainted marks the object
// states.ObjectTainted, and the next plan turns a tainted prior object into
// a synthetic Replace (node_resource_abstract_instance.go, the
// currentState.Status != states.ObjectTainted branch). That is all. Stock
// does not remember what the command was, does not diff it, and never
// re-runs a provisioner because its command changed.
//
// So this namespace stores the tainted bit and nothing else. An earlier
// sketch proposed hashing the provisioner configuration and re-running on a
// change; that would be a memory stock does not have, it would violate
// "match stock and go no further", and it is by name the
// terraform_data.triggers_replace pseudo-receipt pattern live/RECEIPTS.md
// forbids. There is deliberately no field here that could grow into one.
//
// # Why a record and not a receipt
//
// site/content/model-effects.md's own test: "A record is written by
// choudoufu... A receipt is written by your configuration." Nothing in an
// operator's configuration writes this, nothing outside this binary can
// read it usefully, and its format is internal. It is a record.
//
// # Why a fifth namespace root
//
// The same reason located.go and residue.go each got one.
// builder.discoverOrphanedRecords lists [Options.RecordKeyPrefix] and
// materializes every key it can decode as an UNDECLARED prior-state entry,
// which makes the plan propose DESTROYING what it names. A provisioner note
// describes a live cloud object the estate owns and the record namespace
// has no authority over, so a note about a failed `local-exec` must never
// be able to drive a cloud deletion. It therefore lives under its own root,
// with no List of any kind, and internal/configs' validateRecordStoreKeyPrefix
// refuses an operator key_prefix override rooted here.
//
// # At-most-once, deliberately, and the window is wider than stock's
//
// The record is written by [writeBackProvisioned] AFTER the apply, from the
// final state, never speculatively before the create. The stronger
// at-least-once shape (write "pending" before the create, delete it on
// success) was considered and rejected for issue #353's decision 2: match
// stock and go no further. That decision stands.
//
// What does not stand is the sentence this comment used to carry, that a
// crash loses "the same window stock itself has between the create and its
// own state write". Stock's window is milliseconds; this one is the rest of
// the apply. Anyone reasoning about crash safety from the old sentence was
// reasoning from a false premise, so the real shape, measured against the
// code rather than assumed:
//
//   - Stock persists CONTINUOUSLY. internal/backend/local's StateHook
//     writes on every PostStateUpdate as the graph walks, and
//     [backendLocal.Local.opWait] force-persists the moment an interrupt
//     arrives. A stock apply killed partway through has already written
//     the taint for a resource that finished minutes earlier.
//   - This runs ONCE. backend_apply.go calls WriteBack after
//     lr.Core.Apply has returned for the WHOLE graph. Everything a long
//     multi-resource apply does after the failing provisioner sits inside
//     the exposure, not milliseconds of it.
//   - Two paths skip it outright rather than merely racing it: a forceful
//     cancel (a second interrupt, opWait returning canceled) returns
//     before the call is reached, and a statemgr.WriteAndPersist failure
//     returns before it too.
//
// Losing the bit costs a later plan that reads a half-provisioned object as
// healthy, which is the defect issue #353 is about. Closing the window means
// writing before the create runs, and that is the at-least-once shape
// decision 2 rejected. Accepting it and saying how wide it is are compatible;
// accepting it and understating it are not.

// GitHub issue #364 folded this file's own namespace, key encoding and
// point-lookup type into [RecordStore]: [RecordStore.getProvisioned] and
// [RecordStore.mergeEnvelope] read and write the Provisioned member of the
// same envelope [record.go] and [located.go] read and write theirs of, kept
// safe from the destroy path by the same "kind" discipline - see
// [recordKindIdentity]'s comment.

// declaresCreateProvisioners reports whether cfg declares at least one
// create-time provisioner on the resource block addr belongs to.
//
// It is the gate on BOTH halves of this mechanism - the read in
// [builder.applyProvisionedTaint] and the write in
// [writeBackProvisioned] - and asking one function the same question in
// both places is what keeps the set that gets written identical to the set
// that gets read. A second, looser rule on either side is how an instance
// would come to be tainted from a record nothing writes, or written to a
// record nothing reads.
//
// Destroy-time provisioners are deliberately not counted. Stock only runs
// one when it is also calling the provider's delete, strictly before it; on
// failure the delete never happens, nothing is written, and the live object
// survives WITH ITS MARKER INTACT - so the marker's continued existence
// already is the "still needs destroying" signal, and the next plan
// re-proposes the destroy and re-runs the provisioner. That is
// at-least-once for free, through a mechanism this fork already has, and it
// needs no storage at all.
func declaresCreateProvisioners(cfg *configs.Config, addr addrs.AbsResourceInstance) bool {
	if cfg == nil {
		return false
	}
	modCfg, ok := identity.ConfigForModule(cfg, addr.Module)
	if !ok || modCfg.Module == nil {
		return false
	}
	rc := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	return resourceDeclaresCreateProvisioners(rc)
}

// resourceDeclaresCreateProvisioners is [declaresCreateProvisioners] for a
// caller that already holds the resource block, which
// [builder.applyProvisionedTaint] does.
func resourceDeclaresCreateProvisioners(rc *configs.Resource) bool {
	if rc == nil || rc.Managed == nil {
		return false
	}
	for _, p := range rc.Managed.Provisioners {
		if p.When == configs.ProvisionerWhenCreate {
			return true
		}
	}
	return false
}

// applyProvisionedTaint is [builder.materialize]'s front door for issue
// #353: after the live object has been read and its ownership checked, it
// consults the provisioner-taint store and, if the last apply left a failed
// create-time provisioner behind, marks the projected prior object
// [states.ObjectTainted].
//
// That one field is the whole mechanism downstream. Nothing else changes:
// the stock plan graph reads it (node_resource_abstract_instance.go treats
// a tainted prior object as if the object did not exist, then rewrites the
// change into a synthetic Replace at the end) and the stock apply graph
// re-runs the create-time provisioner on the replacement, because a replace
// IS a create. There is no new execution machinery anywhere in this fork
// for that, and there deliberately is not going to be.
//
// It is called AFTER the ownership check, which is a rule and not an
// ordering accident, for exactly [builder.fillResidueFor]'s reason: whether
// this estate owns the object is decided on what the CLOUD said, and a
// stored note must never be in a position to answer it.
//
// rc is the instance's resource block (nil for an undeclared instance the
// marker sweep found, which by definition has no provisioner block left to
// have failed). The store is only consulted when that block declares a
// create-time provisioner, which keeps an estate with no provisioners
// anywhere paying nothing - not one extra store round trip per instance -
// and keeps the read gate identical to the write gate.
//
// The bool is false when the instance was omitted rather than projected,
// which is the one outcome the caller has to stop for: an unreadable record
// leaves this function unable to say whether the object is healthy, and
// continuing would say "healthy" by default.
func (b *builder) applyProvisionedTaint(ctx context.Context, addr addrs.AbsResourceInstance, rc *configs.Resource, obj *states.ResourceInstanceObject) bool {
	if b.opts.RecordStore == nil || obj == nil {
		return true
	}
	if !resourceDeclaresCreateProvisioners(rc) {
		return true
	}
	tainted, version, keyExists, err := b.opts.RecordStore.getProvisioned(ctx, addr)
	if err != nil {
		detail := fmt.Sprintf(
			"The provisioner record for %s could not be read: %s. That record is the only place a failed create-time provisioner is remembered for a resource whose state lives in the cloud, so continuing would report a half-provisioned object as healthy. Delete the record from the store to have the resource read back as healthy, or fix the store, then re-plan.",
			addr, err,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryProvisionedUnreadable, detail))
		b.omitFailed(addr, detail)
		return false
	}
	if keyExists {
		b.recordEnvelopeVersion(addr, version)
	}
	if !tainted {
		return true
	}
	obj.Status = states.ObjectTainted
	return true
}

// SummaryProvisionedUnreadable is the summary of the refusal
// [builder.applyProvisionedTaint] raises when a provisioner-taint record
// exists and cannot be used. Named for [SummaryLocatedNoStore]'s reason:
// internal/live/refusalscan requires every diagnostic this fork raises to
// have a registry entry, and the entry and the diagnostic have to name one
// string.
const SummaryProvisionedUnreadable = "Provisioner record could not be read"
