// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"bytes"
	"context"
	"encoding/json"
	"log"

	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #1239: a provider's private state, the fourth carrier.
//
// A provider's private is opaque bytes core carries from one operation to
// the next: ApplyResourceChange returns it, the state file stores it, and
// the next PlanResourceChange receives it as PriorPrivate. The live path
// has no state file to carry it in. Every plan re-derives its prior through
// [importAndRead], so the private a live plan holds is whatever the import
// stub and the read produced, never what the last apply wrote.
//
// # The two halves
//
// Part of a private re-derives from configuration and is rebuilt on every
// plan, never recorded: SDKv2's timeout meta (#1185, [withConfiguredTimeouts])
// and its "schema_version", which helper/schema's own import and read write
// themselves. [applyTimePrivate] strips exactly those two keys.
//
// Everything else is, by construction, something the provider learned at
// apply time and wants handed back, with no configuration expression to
// rebuild it from. That part is recorded beside the instance's residue
// ([residueFields.ProviderPrivate]) by [WriteBack] after an apply and by
// [RecordResidueForInstance] at migration, and put back by
// [builder.restoreProviderPrivate] on every later plan.
//
// # The population, measured
//
// Step 1 of the issue, run offline against hashicorp/terraform-provider-aws's
// own source on 2026-10-03 (GitHub code search over the repository for
// Private.SetKey, Private.GetKey, NewWriteOnlyValueStore, RequiresReplaceWO,
// resp.Private, response.Private and request.Private): the provider writes
// private state of its own in exactly one place,
// internal/framework/privatestate/write_only.go's WriteOnlyValueStore,
// which stores the sha256 of a write-only argument under a key of the
// resource's choosing. Its one user is aws_transfer_host_key
// (host_key_body_wo), and its one reader is the
// stringplanmodifier.RequiresReplaceWO plan modifier, which proposes a
// REPLACE whenever the configured value is set and the private holds no
// hash for it. On the live path before this file that was every plan: the
// import stub's private is empty, so the host key was destroyed and
// re-imported on every apply, where stock's state-backed plan reads the
// hash the create wrote and proposes nothing.
//
// Seeding the hash from configuration instead would be wrong in the way
// residue.go's "What is stored" section describes: the plan modifier
// exists to notice that the configured value CHANGED, and a hash of the
// current configuration agrees with the current configuration by
// construction. The record holds what was sent.
//
// Nothing in this file names that type or that key. The rule is the
// private's own shape, so a framework provider of any other kind that
// writes resp.Private.SetKey is covered the day it is admitted.

// sdkv2SchemaVersionKey is the second key helper/schema writes into an
// instance's meta, beside [sdkv2TimeoutMetaKey]. Its import and read write
// it from the resource's own declared SchemaVersion, so it is never
// apply-time data.
const sdkv2SchemaVersionKey = "schema_version"

// recordsProviderPrivate reports whether an estate's `strict { secrets }`
// setting lets the record store hold a provider's private.
//
// Only under the default, [strict.Store]. A private is opaque: nothing here
// can tell a hash from the secret it was taken of, and the founding member
// IS taken of a write-only - by definition secret - argument. So an
// operator who said "refuse" has said the record holds no secret material,
// and one who said "ssm" has said a secret goes to Parameter Store rather
// than into the record; this mechanism has no Parameter Store route, and
// guessing that a blob is not a secret would be overriding either answer.
// Under both the private is not recorded and the plan is the one the live
// path proposed before #1239.
func recordsProviderPrivate(secrets strict.Secrets) bool {
	return secrets == strict.Store
}

// applyTimePrivate returns the part of private that an import and read
// cannot rebuild, or nil when there is none.
//
// The shapes, each decided on what the bytes are rather than on which SDK
// wrote them:
//
//   - empty, or the JSON literal null: nothing.
//   - a JSON object: every key but [sdkv2TimeoutMetaKey] and
//     [sdkv2SchemaVersionKey], re-encoded; nothing if no key is left. Both
//     SDKs write a JSON object (helper/schema's metaEncode, and
//     terraform-plugin-framework's Data.Bytes over a map[string][]byte),
//     so this is the ordinary case.
//   - anything else: the whole of it, unchanged. A private this package
//     cannot read is a private it cannot prove re-derivable, and recording
//     bytes nothing will look inside costs less than dropping them.
func applyTimePrivate(private []byte) []byte {
	trimmed := bytes.TrimSpace(private)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &meta); err != nil {
		return append([]byte(nil), private...)
	}
	delete(meta, sdkv2TimeoutMetaKey)
	delete(meta, sdkv2SchemaVersionKey)
	if len(meta) == 0 {
		return nil
	}
	out, err := json.Marshal(meta)
	if err != nil {
		return append([]byte(nil), private...)
	}
	return out
}

// withRecordedPrivate puts recorded - an earlier [applyTimePrivate] result -
// back into private, the private this run's import and read produced, and
// reports whether anything changed.
//
// It fills, it never overwrites, for [fillResidue]'s reason: a key the
// read itself produced is the provider speaking this run, and the record
// is only ever the answer for what the read could not know.
//
//   - nothing recorded: private unchanged.
//   - private empty (or null): recorded, whole.
//   - both JSON objects: every recorded key private lacks is added.
//   - otherwise: private unchanged. A non-empty private this package cannot
//     read as an object is the provider's, in a shape nothing here may
//     rewrite (the same stance [withConfiguredTimeouts] takes), and an
//     opaque recording cannot be merged into anything.
func withRecordedPrivate(private, recorded []byte) ([]byte, bool) {
	if len(bytes.TrimSpace(recorded)) == 0 {
		return private, false
	}
	trimmed := bytes.TrimSpace(private)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return append([]byte(nil), recorded...), true
	}
	var have, want map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &have); err != nil || have == nil {
		return private, false
	}
	if err := json.Unmarshal(recorded, &want); err != nil {
		return private, false
	}
	changed := false
	for k, v := range want {
		if _, ok := have[k]; ok {
			continue
		}
		have[k] = v
		changed = true
	}
	if !changed {
		return private, false
	}
	out, err := json.Marshal(have)
	if err != nil {
		return private, false
	}
	return out, true
}

// restoreProviderPrivate is [builder.materialize]'s GitHub issue #1239
// step: it reads w's recorded [residueFields.ProviderPrivate], if any, and
// fills it into obj.Private, the private the plan will hand the provider
// as PriorPrivate.
//
// It runs after the ownership check, for [builder.fillResidueFor]'s
// reason: a record never argues about what the cloud said.
//
// A record whose captured identity disagrees with this attempt's identity
// is not used - [builder.residueIdentityStale], the same guard the residue
// seed takes. That matters more here than anywhere else: the founding
// member's private is a hash of what was sent to ONE object, and handing
// another object's hash to a plan modifier whose whole job is "has the
// configured value changed since it was sent" would answer "no" for an
// object that was never sent it.
//
// No failure here stops the run, for fillResidueFor's reason: the cost of
// an unrestored private is the plan the live path proposed before #1239
// existed, which is visible, and stopping the run over it would trade a
// visible nuisance for an outage.
func (b *builder) restoreProviderPrivate(ctx context.Context, w wanted, obj *states.ResourceInstanceObject) {
	if b.opts.RecordStore == nil || obj == nil {
		return
	}
	recorded, found, err := b.opts.RecordStore.GetProviderPrivate(ctx, w.addr)
	if err != nil {
		// Swallowed, for [builder.manifestDeclaredKeysFor]'s reason: this
		// is the same envelope [builder.fillResidueFor] reads next, and
		// that read's own [SummaryResidueUnreadable] warning already
		// names the failure. A second warning over the one read would
		// say nothing the first does not.
		log.Printf("[WARN] projection: %s's recorded provider private could not be read (%s); continuing with the private the import and read produced", w.addr, err)
		return
	}
	if !found {
		return
	}
	if b.residueIdentityStale(ctx, w) {
		log.Printf("[INFO] projection: %s has a recorded provider private, but the record names a different object than this run resolved; it is not restored, because it describes what was sent to that other object", w.addr)
		return
	}
	if updated, changed := withRecordedPrivate(obj.Private, recorded); changed {
		log.Printf("[TRACE] projection: %s restored the apply-time part of its provider private from the record store", w.addr)
		obj.Private = updated
	}
}
