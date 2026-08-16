// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// WriteBackRequest is what [WriteBack] needs: the store and namespace a
// projection was built against, the versions it read at plan time, and the
// state an apply finished with.
type WriteBackRequest struct {
	// Store is where record-backed instances persist. A nil Store makes
	// [WriteBack] a no-op: no live block configured one, so there is
	// nothing to write back to.
	Store staterecord.Store

	// KeyPrefix is the same prefix the projection that read PriorVersions
	// was built with - ordinarily [RecordKeyPrefix](estate), or a
	// record_store block's key_prefix override.
	KeyPrefix string

	// PriorVersions is the projection's own record of what it read at plan
	// time - [Result.RecordVersions]. An address with no entry here had no
	// prior record.
	PriorVersions []RecordVersion

	// FinalState is the state an apply (or destroy) finished with.
	FinalState *states.State

	// Schemas resolves a resource type's current schema, needed to decode
	// FinalState's stored objects before they can be re-encoded as a
	// record payload.
	Schemas *tofu.Schemas
}

// WriteBack persists every record-backed resource instance's post-apply
// state to req.Store, and deletes the record for every record-backed
// instance that req.PriorVersions saw but req.FinalState no longer has -
// GitHub issue #73's write-back half of the record lifecycle, called once,
// after a successful apply (or destroy), never mid-apply.
//
// Every write and delete is conditional on the version [Result] read at
// plan time (staterecord.Store.PutIfVersion / Delete), so a second writer
// that changed the same record between this run's plan and apply produces a
// *staterecord.VersionConflictError, which this function turns into a loud,
// run-stopping error diagnostic naming both versions - the same philosophy
// live/MARKERS.md's marker-collision handling already uses, never a silent
// last-writer-wins. A conflict on one instance does not stop this function
// from attempting every other instance: an operator fixing one conflict at
// a time should see every conflict in the estate, not just the first one
// alphabetically.
func WriteBack(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.Store == nil {
		return diags
	}

	seen := make(map[string]bool, len(req.PriorVersions))

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				// A deposed object is a mid-replace leftover the graph is
				// about to discard; the record for this address is the
				// current object's job, materialized in the next iteration
				// (or, for a purely-deposed address with no current object
				// at all, by nothing - which is correct, since that shape
				// means the replace's create side failed and there is
				// nothing new to persist).
				continue
			}
			addr := entry.Instance
			typeName := addr.Resource.Resource.Type

			ti, ok := identity.LookupType(typeName)
			if !ok || !ti.RecordBacked {
				continue
			}
			seen[addr.String()] = true

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}

			schema, _ := req.Schemas.ResourceTypeConfig(res.ProviderConfig.Provider, addrs.ManagedResourceMode, typeName)
			if schema == nil || schema.Block == nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: no schema is available for %s to decode its final state with.", addr, typeName),
				))
				continue
			}
			obj, err := ri.Current.Decode(schema.Block.ImpliedType())
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: its final state could not be decoded: %s.", addr, err),
				))
				continue
			}

			payload, err := encodeRecordPayload(obj.Value, obj.Private, obj.Status)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: %s.", addr, err),
				))
				continue
			}

			key := RecordKey(req.KeyPrefix, addr)
			expected := priorVersion(req.PriorVersions, addr)
			if _, err := req.Store.PutIfVersion(ctx, key, payload, expected); err != nil {
				diags = diags.Append(writeBackConflictDiag(addr, "Writing", err))
			}
		}
	}

	for _, rv := range req.PriorVersions {
		if seen[rv.Addr.String()] {
			continue
		}
		key := RecordKey(req.KeyPrefix, rv.Addr)
		if err := req.Store.Delete(ctx, key, rv.Version); err != nil {
			diags = diags.Append(writeBackConflictDiag(rv.Addr, "Deleting", err))
		}
	}

	return diags
}

// priorVersion looks up addr's plan-time version in versions, "" (the
// store's own create/absence convention) when there is no entry.
func priorVersion(versions []RecordVersion, addr addrs.AbsResourceInstance) string {
	want := addr.String()
	for _, rv := range versions {
		if rv.Addr.String() == want {
			return rv.Version
		}
	}
	return ""
}

// writeBackConflictDiag turns a write-back failure into a run-stopping
// error diagnostic, naming both versions when it is a
// *staterecord.VersionConflictError so an operator sees exactly what
// changed underneath this run rather than a bare "conflict" - the same
// naming-both-sides discipline live/MARKERS.md's marker-collision handling
// already uses. verb is "Writing" or "Deleting", matching the operation
// that failed.
func writeBackConflictDiag(addr addrs.AbsResourceInstance, verb string, err error) tfdiags.Diagnostics {
	var vErr *staterecord.VersionConflictError
	if errors.As(err, &vErr) {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Record store write conflict", fmt.Sprintf(
			"%s the persisted record for %s failed: another writer changed it between this run's plan and apply. This run expected version %q and the store now holds version %q. Nothing was overwritten. Re-plan and re-apply to reconcile with the other writer's change.",
			verb, addr, displayVersion(vErr.ExpectedVersion), displayVersion(vErr.ActualVersion),
		)))
	}
	return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record", fmt.Sprintf(
		"%s the persisted record for %s failed: %s.", verb, addr, err,
	)))
}

// displayVersion renders staterecord's "" (no record) sentinel as an
// operator-facing word rather than an empty, easy-to-miss string.
func displayVersion(v string) string {
	if v == "" {
		return "(no record)"
	}
	return v
}
