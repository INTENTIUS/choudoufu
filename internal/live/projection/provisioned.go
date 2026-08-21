// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
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

// provisionedNamespaceRoot is the literal segment every provisioner-taint
// key lives under. It is a different literal from [recordNamespaceRoot]
// ("tofu-records"), from hint_store.go's hintNamespaceRoot ("tofu-hints"),
// from located.go's locatedNamespaceRoot ("tofu-located"), from
// residue.go's residueNamespaceRoot ("tofu-residue") and from
// live/RECEIPTS.md's "tofu-receipts", for the reason this file's own
// comment gives: the enumeration that feeds the destroy path lists the
// RECORD root, so anything that must never be destroyable has to live
// somewhere that listing physically cannot reach.
//
// Like the located and residue roots it is NOT derived from a record_store
// block's key_prefix override, and internal/configs'
// validateRecordStoreKeyPrefix closes the other direction by refusing an
// override rooted here.
const provisionedNamespaceRoot = "tofu-provisioned"

// ProvisionedKeyPrefix is the key namespace one estate's provisioner-taint
// records live under. Exported for the same reason [RecordKeyPrefix],
// [LocatedKeyPrefix] and [ResidueKeyPrefix] are: internal/command's store
// construction and this package's own namespace-safety tests both have to
// name one definition.
func ProvisionedKeyPrefix(estate string) string {
	return provisionedNamespaceRoot + "/" + estate
}

// ProvisionedKey is the store key one resource instance's provisioner-taint
// record lives at, for estate.
//
// The address is encoded exactly as [RecordKey], [LocatedKey] and
// [ResidueKey] encode it, by [recordKeyEncoding], because the constraint is
// the same: a for_each key can carry characters SSM parameter names and S3
// object keys forbid. Sharing the encoding is safe where sharing the ROOT
// would not be.
//
// There is deliberately no reverse of this function, for [LocatedKey]'s
// reason: a reverse exists so that a LISTING can recover an address, and
// building one here would be building the first half of a destroy path this
// file exists to keep unbuilt.
func ProvisionedKey(estate string, addr addrs.AbsResourceInstance) string {
	return ProvisionedKeyPrefix(estate) + "/" + addr.Resource.Resource.Type + "/" + recordKeyEncoding.EncodeToString([]byte(addr.String()))
}

// provisionedFormatVersion identifies the JSON shape [provisionedPayload]
// writes. An unrecognized version is refused rather than guessed at - see
// [ProvisionedStore.Get] for why a refusal here stops the run instead of
// being shrugged off the way an unreadable residue record is.
const provisionedFormatVersion = "tofu-live-provisioned-v1"

// provisionedPayload is what one instance's provisioner-taint key holds.
//
// Note what is NOT in here, and that the absence is the design rather than
// a first version to be filled in later: no attribute values, no provider
// private state, no import identity, and above all no hash, digest or copy
// of the provisioner's own configuration. See this file's opening comment.
//
// It shares no field name with [recordPayload] - no value_type, no attrs,
// no private, no status - which is what makes [decodeRecordPayload] refuse
// it outright rather than half-read it, the third of the three independent
// stops between one of these keys and the destroy path.
type provisionedPayload struct {
	// FormatVersion is always [provisionedFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// Address is the instance address this record is for, written out in
	// full. Redundant with the key, and deliberately so:
	// [ProvisionedStore.Get] checks the two agree, so a key copied,
	// renamed or hand-edited into pointing at another resource is refused
	// rather than answering about another resource - which would force a
	// replace of a live object that never had a provisioner fail on it.
	Address string `json:"address"`

	// Tainted is the bit. It is always true in any record this package
	// writes: a record meaning "not tainted" would say nothing, and
	// [ProvisionedStore.Get] refuses one rather than treating it as an
	// obscure spelling of absence. Absence is how "not tainted" is spelled
	// here, and there is exactly one spelling of it.
	Tainted bool `json:"tainted"`
}

// ProvisionedStore is the point-lookup view of an estate's
// provisioner-taint records.
//
// It is NOT a [staterecord.Store] and does not embed one, for the reason
// [LocatedStore]'s doc comment sets out in full: no List, no Keys, no
// iteration of any kind, and every method keyed by an
// [addrs.AbsResourceInstance] the caller must already hold. "What else is
// in here" is not a question this type can be asked, so the enumeration
// that feeds the destroy path cannot be handed one and cannot be given one
// of these keys by one.
type ProvisionedStore struct {
	store  staterecord.Store
	estate string
}

// NewProvisionedStore wraps store as estate's provisioner-taint store.
// store is ordinarily the same [staterecord.Store] the run's record-backed,
// record-located and residue-carrying resources use - one store, four
// disjoint namespaces in it.
//
// A nil store yields a nil ProvisionedStore, so a run with no record_store
// declared has nowhere to keep a taint bit - which is exactly why
// internal/live/lint still refuses a provisioner for such a run. The gate
// and this constructor answer the same question, and they must keep
// answering it the same way.
func NewProvisionedStore(store staterecord.Store, estate string) *ProvisionedStore {
	if store == nil || estate == "" {
		return nil
	}
	return &ProvisionedStore{store: store, estate: estate}
}

// Get reports whether addr's last apply left a failed create-time
// provisioner behind. exists is false when nothing has been recorded, which
// is the ordinary "the provisioner has never failed, or last succeeded"
// answer and is not an error.
//
// version is the store's version for the key, for a later conditional Put
// or Delete.
//
// A record that is present and cannot be read is an ERROR, and that is a
// deliberate difference from [ResidueStore.Get]'s warning-and-continue. An
// unreadable residue record costs a visible, repeated update proposal; an
// unreadable record HERE would be silently read as "no provisioner ever
// failed", which is the exact silent under-run - a live, fully-marked,
// unprovisioned object that every later plan calls healthy - this whole
// namespace exists to prevent. Loud and one deletion away from fixed beats
// silent and invisible.
func (s *ProvisionedStore) Get(ctx context.Context, addr addrs.AbsResourceInstance) (tainted bool, version string, exists bool, err error) {
	if s == nil {
		return false, "", false, nil
	}
	key := ProvisionedKey(s.estate, addr)
	payload, version, exists, err := s.store.Get(ctx, key)
	if err != nil {
		return false, "", false, fmt.Errorf("reading the provisioner record for %s: %w", addr, err)
	}
	if !exists {
		return false, "", false, nil
	}
	var stored provisionedPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return false, "", false, fmt.Errorf("decoding the provisioner record for %s: %w", addr, err)
	}
	if stored.FormatVersion != provisionedFormatVersion {
		return false, "", false, fmt.Errorf("the provisioner record for %s names format %q, which this version of choudoufu does not understand", addr, stored.FormatVersion)
	}
	if stored.Address != addr.String() {
		return false, "", false, fmt.Errorf("the provisioner record stored for %s says it is for %s; refusing to force a replacement of one resource from another resource's failed provisioner", addr, stored.Address)
	}
	if !stored.Tainted {
		return false, "", false, fmt.Errorf("the provisioner record for %s says nothing: a record exists only to record a failed create-time provisioner, and absence is the only spelling of \"no failure\"", addr)
	}
	return true, version, true, nil
}

// Put records that addr's create-time provisioner failed, conditional on
// the key's current version - expectedVersion "" asserting that nothing is
// recorded yet, the same convention [staterecord.Store.PutIfVersion] uses.
// It returns the new version.
func (s *ProvisionedStore) Put(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so the failed create-time provisioner on %s cannot be recorded", addr)
	}
	payload, err := json.Marshal(provisionedPayload{
		FormatVersion: provisionedFormatVersion,
		Address:       addr.String(),
		Tainted:       true,
	})
	if err != nil {
		return "", fmt.Errorf("encoding the provisioner record for %s: %w", addr, err)
	}
	return s.store.PutIfVersion(ctx, ProvisionedKey(s.estate, addr), payload, expectedVersion)
}

// Delete removes addr's provisioner-taint record, conditional on
// expectedVersion.
//
// Deleting this record is not deleting anything in the cloud, and cannot
// be: it is a note that a command failed, and the object it describes is
// created and destroyed by the ordinary apply through the provider.
//
// The reverse case - a key with no configuration behind it, which is what
// an operator removing the provisioner block while a taint stood leaves -
// is not swept, because nothing enumerates this namespace. It sits inert
// for as long as the block declares no create-time provisioner, since
// [declaresCreateProvisioners] gates the read as well as the write, and it
// applies again the moment the block comes back. Stock behaves the same
// way with its own tainted bit: editing the provisioner block does not
// clear it, and the next plan still forces a replace.
//
// An earlier version of this comment went on to say such a key "is never
// read again" and "is overwritten if the block ever comes back and fails
// again". Both halves were wrong, and the second was wrong in the direction
// that destroys a live resource. The block coming back and SUCCEEDING is
// the ordinary case, not the edge case, and it used to leave the record
// standing, so every plan afterwards proposed replacing a healthy object
// and named a provisioner failure that had already been fixed.
// [writeBackProvisioned] now clears from the CONFIGURATION rather than from
// whatever this run's plan happened to read; see its comment for why that
// is the only gate that closes the case.
func (s *ProvisionedStore) Delete(ctx context.Context, addr addrs.AbsResourceInstance, expectedVersion string) error {
	if s == nil {
		return nil
	}
	return s.store.Delete(ctx, ProvisionedKey(s.estate, addr), expectedVersion)
}

// currentVersion reports the version the store holds for addr's key right
// now, "" when the key does not exist - the same convention
// [staterecord.Store.PutIfVersion] and [staterecord.Store.Delete] use for
// "nothing is there".
//
// It exists because a conditional write needs a version and this run's plan
// may never have read the key. [builder.applyProvisionedTaint] only reads
// for an instance the projection actually materialized, and the two cases
// [writeBackProvisioned] has to clean up after are exactly the ones where
// it did not: the provider reported the object ABSENT (build.go's
// statusAbsent returns before the taint is consulted), or the resource
// block was gone from the configuration.
//
// It deliberately does not decode the payload, and so cannot fail the way
// [ProvisionedStore.Get] fails on a record that names another address or an
// unknown format version. The caller is about to overwrite or delete the
// key on the strength of what the FINAL STATE says about the instance, and
// what the bytes at the key say has no bearing on that: a payload too
// broken to read is still a payload that must not be left behind to force a
// replacement of a healthy object.
func (s *ProvisionedStore) currentVersion(ctx context.Context, addr addrs.AbsResourceInstance) (string, error) {
	if s == nil {
		return "", nil
	}
	_, version, exists, err := s.store.Get(ctx, ProvisionedKey(s.estate, addr))
	if err != nil {
		return "", fmt.Errorf("reading the provisioner record for %s: %w", addr, err)
	}
	if !exists {
		return "", nil
	}
	return version, nil
}

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
	if b.opts.ProvisionedStore == nil || obj == nil {
		return true
	}
	if !resourceDeclaresCreateProvisioners(rc) {
		return true
	}
	tainted, version, exists, err := b.opts.ProvisionedStore.Get(ctx, addr)
	if err != nil {
		detail := fmt.Sprintf(
			"The provisioner record for %s could not be read: %s. That record is the only place a failed create-time provisioner is remembered for a resource whose state lives in the cloud, so continuing would report a half-provisioned object as healthy. Delete the record from the store to have the resource read back as healthy, or fix the store, then re-plan.",
			addr, err,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryProvisionedUnreadable, detail))
		b.omitFailed(addr, detail)
		return false
	}
	if !exists || !tainted {
		return true
	}
	b.provisionedVersions = append(b.provisionedVersions, RecordVersion{Addr: addr, Version: version})
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

// writeBackProvisioned is [WriteBack]'s issue #353 half: after an apply,
// every instance whose configuration declares a create-time provisioner and
// whose final object came out [states.ObjectTainted] gets a record, and
// every record this run's plan read that is no longer warranted is deleted.
//
// This is the half that makes the mechanism true rather than hypothetical,
// and its absence is the exact defect issue #353 is about.
// internal/live/stamp marks a resource's configuration BEFORE the create
// request goes out, so a live object is fully marked the instant it exists
// - before any provisioner has run. Stock sets states.ObjectTainted when a
// create-time provisioner fails; without this function that bit lands only
// in an in-memory state nothing persists for a marker-tracked type, and the
// next plan reads a fully-marked, live, unprovisioned object back as
// healthy. A silent under-run, and one no verdict-level check can see.
//
// # Both directions, and why BOTH are gated on the configuration
//
// The write side is gated on the CONFIGURATION declaring a create-time
// provisioner, not merely on the object being tainted. That is
// deliberately narrower than "persist every tainted object": a create that
// the PROVIDER failed halfway through is a real thing stock also taints,
// and widening this to cover it would be a second, larger parity change
// (every partially-created cloud resource forcing a replace on the next
// plan) that issue #353 did not scope and did not measure. One question at
// a time.
//
// The clear side is gated on the same question, and that is a FIX. It used
// to walk req.ProvisionedVersions instead - the records this run's own plan
// happened to read - on the assumption that a plan always reads the record
// for any instance that has one. It does not, and there are two ordinary
// ways it does not:
//
//   - The provider reports the object ABSENT. build.go's statusAbsent
//     branch returns from materialize before applyProvisionedTaint is ever
//     reached, so nothing is read and nothing is recorded in
//     ProvisionedVersions.
//   - The resource block is not in the configuration, so
//     resourceDeclaresCreateProvisioners is false on a nil block and the
//     read is skipped for the gate's own reason.
//
// Which produces this, reproduced end to end in
// TestStaleTaintDoesNotSurviveAHealthyRecreate: an apply fails on the
// provisioner and the record is written, correctly. The operator deletes
// the half-built object by hand, which is the natural response. The next
// apply reads it as absent, creates it, and the provisioner SUCCEEDS. The
// old code took the healthy branch and did not delete, and the versions
// loop had nothing in it to delete, so the record survived a successful
// provision. Every plan after that read a live, healthy, correctly
// provisioned object PLUS a stale record, called it tainted, and said "must
// be replaced" - destroying a healthy resource carrying real data, and
// naming a provisioner failure that had already been fixed as the reason.
// That is a wrong marker rather than a missing one, which HANDOFF.md ranks
// as the worse of the two for exactly this reason.
//
// So the clear side now walks the final state under the same gate as the
// write side, and asks the store for the key's current version when this
// run's plan has none ([ProvisionedStore.currentVersion]). The round trip
// that argument avoided is one Get per PROVISIONER-BEARING instance per
// apply, not one per resource in the estate: the gate that makes the write
// side cheap makes the clear side cheap in the same breath, and an estate
// with no provisioners anywhere still pays nothing at all.
//
// A key whose instance declares no create-time provisioner is still left
// inert, exactly as located.go and residue.go leave theirs. See
// [ProvisionedStore.Delete] for why that case is parity rather than a leak.
//
// # Failure is an ERROR here, matching the record-backed half
//
// This side used to raise a warning where [writeBackConflictDiag] raises an
// error for the identical *staterecord.VersionConflictError, and the
// argument for the difference was that the apply had already succeeded so a
// run-stopping error would report a failure for work that succeeded. That
// argument does not survive looking at what each failure costs:
//
//   - A taint that cannot be WRITTEN is a lost bit, and the run it happens
//     in has already failed anyway - a create-time provisioner failed,
//     which is what put the object in this state. There is no successful
//     work to misreport.
//   - A stale record that cannot be CLEARED is the wrong-marker case above.
//     It does not lose information, it fabricates it, and what it
//     fabricates gets a healthy live resource destroyed on the next plan.
//
// Both are the silent-under-run this namespace exists to prevent, one by
// omission and one by invention, and a warning at the tail of a long apply
// is exactly the kind of thing that gets scrolled past. Loud and one
// deletion away from fixed beats silent and invisible, which is the same
// sentence [ProvisionedStore.Get] already justifies the read side with.
func writeBackProvisioned(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.ProvisionedStore == nil {
		return diags
	}

	settled := make(map[string]bool, len(req.ProvisionedVersions))

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				// A deposed object is a mid-replace leftover the graph is
				// about to discard; the current object is what this
				// address's provisioner outcome should be read from.
				continue
			}
			addr := entry.Instance

			if ti, ok := identity.LookupType(addr.Resource.Resource.Type); ok && ti.RecordBacked {
				// A record-backed instance already carries its own tainted
				// bit inside its record (recordPayload.Status, issue
				// #216). Writing a second copy here would be two sources
				// for one fact, and the two could disagree.
				continue
			}

			if !declaresCreateProvisioners(req.Config, addr) {
				continue
			}

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}

			// From here on this instance's record is this loop's
			// responsibility whichever way it goes, so the versions loop
			// below must not touch it again.
			settled[addr.String()] = true

			version, err := provisionedVersionFor(ctx, req, addr)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryProvisionedNotRecorded, fmt.Sprintf(
					"The provisioner record for %s could not be read to settle it after this apply: %s. Whether the next plan re-runs the create-time provisioner on this resource, or proposes replacing it for a failure that is already fixed, now depends on whatever the store still holds. Fix the store and re-plan.",
					addr, err,
				)))
				continue
			}

			if ri.Current.Status == states.ObjectTainted {
				if _, err := req.ProvisionedStore.Put(ctx, addr, version); err != nil {
					diags = diags.Append(provisionedWriteDiag(addr, err, fmt.Sprintf(
						"A create-time provisioner failed on %s and that could not be recorded: %%s. The live object exists and carries this estate's ownership marker, but the next plan will read it back as healthy and will not re-run the provisioner. Taint it by hand, or destroy and re-create it, to have the provisioner run again.",
						addr,
					)))
				}
				continue
			}

			// Healthy, and that is the whole of the question: this run
			// finished with a live object whose create-time provisioner did
			// not fail, so no record for it can be warranted, whether this
			// run's plan read one or not.
			if version == "" {
				continue
			}
			if err := req.ProvisionedStore.Delete(ctx, addr, version); err != nil {
				diags = diags.Append(provisionedWriteDiag(addr, err, fmt.Sprintf(
					"The provisioner record for %s, whose create-time provisioner this run ran successfully, could not be deleted: %%s. The resource is healthy; the stale record will make the next plan propose destroying and replacing it, and will name a provisioner failure that has already been fixed as the reason. Delete the key from the record store to stop that.",
					addr,
				)))
			}
		}
	}

	// Whatever this run's plan read that the final state no longer has at
	// all: the ordinary destroy, where there is no instance left for the
	// loop above to reach.
	for _, rv := range req.ProvisionedVersions {
		if settled[rv.Addr.String()] {
			continue
		}
		if err := req.ProvisionedStore.Delete(ctx, rv.Addr, rv.Version); err != nil {
			diags = diags.Append(provisionedWriteDiag(rv.Addr, err, fmt.Sprintf(
				"The provisioner record for %s, which this run destroyed, could not be deleted: %%s. Nothing is left for it to describe; the stale record will make the next plan that re-creates this address propose replacing it immediately. Delete the key from the record store to stop that.",
				rv.Addr,
			)))
		}
	}

	return diags
}

// provisionedVersionFor is the version a conditional Put or Delete for addr
// has to be made against: the one this run's plan read, when it read one,
// and otherwise whatever the store holds right now.
//
// The second half is the whole of Finding 1's fix. priorVersion returns ""
// both for "this run read no record" and for "there is no record", and the
// two are not the same thing: a Delete against "" is a documented no-op
// when the key is absent, but a CONFLICT when the key is there, so a blind
// Delete would have reported the stale record rather than removing it.
func provisionedVersionFor(ctx context.Context, req WriteBackRequest, addr addrs.AbsResourceInstance) (string, error) {
	if v := priorVersion(req.ProvisionedVersions, addr); v != "" {
		return v, nil
	}
	return req.ProvisionedStore.currentVersion(ctx, addr)
}

// provisionedWriteDiag turns a failed taint write or clear into a
// run-stopping error, naming both versions when the store rejected a
// conditional write because another run got there first - the same
// naming-both-sides discipline [writeBackConflictDiag] uses for the
// record-backed half, and now the same severity.
//
// detailFmt carries exactly one %s, for the error itself, so each caller
// can say what was being settled and what the operator is left with.
func provisionedWriteDiag(addr addrs.AbsResourceInstance, err error, detailFmt string) tfdiags.Diagnostics {
	detail := fmt.Sprintf(detailFmt, err)
	var vErr *staterecord.VersionConflictError
	if errors.As(err, &vErr) {
		detail += fmt.Sprintf(
			"\n\nAnother writer changed this record between this run's plan and apply: this run expected version %s and the store now holds version %s. Nothing was overwritten.",
			displayVersion(vErr.ExpectedVersion), displayVersion(vErr.ActualVersion),
		)
	}
	return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, SummaryProvisionedNotRecorded, detail))
}

// SummaryProvisionedNotRecorded is the summary of the error
// [writeBackProvisioned] raises when an apply could not record, or could not
// clear, a create-time provisioner's outcome. Named for
// [SummaryLocatedNoStore]'s reason.
const SummaryProvisionedNotRecorded = "Provisioner outcome could not be recorded"
