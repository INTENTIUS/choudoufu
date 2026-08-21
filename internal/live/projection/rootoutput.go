// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
)

// This file is GitHub issue #349's remaining half: a carrier for the value a
// root-level `output` block had at the end of the last apply.
//
// # Why a carrier is needed at all
//
// A stock state file holds every root output's value, written at the end of
// the apply that computed it. `tofu plan` diffs that stored value against
// the one it recomputes, and an output whose inputs did not change renders
// as nothing at all.
//
// [ApplyRootOutputValues] (issue #348) reproduces that for a stateless run by
// RECOMPUTING each output against the projection, which works for every
// output whose value is a function of things the projection can materialize.
// Two rungs of #349 widened what that reaches: [withZeroInstanceBlocks] made
// a provably-zero-instance block index to an error rather than an unknown, so
// `try()` recovers; [seedDataValues] put the live value of a data source an
// output reaches into the evaluation. Together they took
// live/e2e/corpus-lambda-simple from 23 root outputs rendering "+" to one.
//
// The one that remains is the shape neither can reach, and no amount of
// evaluation ever will: an output whose value is only knowable by RUNNING
// something. corpus-lambda-simple's `local_filename` is
// `try(data.external.archive_prepare[0].result.filename, null)` - the name of
// a deployment package, derived from a hash the module's own package.py
// computes. `internal/live/dataread`'s provider boundary refuses to read that
// source before the plan on purpose (a pre-plan phase makes read-only calls
// to APIs the projection already reads, and does not run local programs), and
// widening the boundary would mean running the program TWICE per plan, since
// the plan graph reads it again for the "after" side regardless.
//
// So the answer is not a wider read. It is the answer stock already uses:
// remember what the value was. Recomputation stays the primary path and this
// is the fallback under it.
//
// # Why this is a migration-losslessness fix and not a new feature
//
// HANDOFF.md's promise opens with "migration from a stock state file is
// lossless". A stock state file's root output values were, until this file,
// dropped on the floor by `live-import`: every other thing in that file has a
// carrier on this side and they did not. Reading them across is the fix, and
// [writeBackRootOutputs] keeps them current from then on, exactly where
// [WriteBack] persists everything else an apply settled.
//
// # The soundness rule, and why the blast radius is one-directional
//
// A recorded value is used ONLY for an output [ApplyRootOutputValues] could
// not evaluate at all. It can therefore only ever turn a "+ name = value"
// line into either nothing (the recorded value equals what the plan
// recomputes, so the two cancel) or "~ name = old -> new" (they differ, which
// is a real change and is exactly what stock renders for it). It can never
// change an output that already resolves, and it can never make a real change
// render as clean: for a change to be hidden the recorded value would have to
// EQUAL the recomputed one, which is the definition of no change.
//
// That is the same shape as HANDOFF.md's "a wrong marker outranks a missing
// one" argument, and it comes out the other way for exactly one reason: this
// value is not an identity. Nothing is adopted, displaced or destroyed on the
// strength of it. The worst a stale record here can do is show a diff that
// is not there, which is loud and self-correcting on the next apply.
//
// # Why a fifth namespace rather than a field on the record
//
// Same reason as "tofu-located", "tofu-residue" and "tofu-provisioned", in
// their own words: the only enumeration this package performs is orphan
// discovery over the RECORD root, and what it finds with no configuration
// behind it is proposed for destruction. A root output is not a resource and
// has no live object at all, so a key naming one must be somewhere that
// listing physically cannot reach. Like the other three roots it is NOT
// derived from a record_store block's key_prefix override, and
// internal/configs' validateRecordStoreKeyPrefix closes the other direction
// by refusing an override rooted here.
const rootOutputNamespaceRoot = "tofu-outputs"

// RootOutputKeyPrefix is the key namespace one estate's root output values
// live under. Exported for the same reason [RecordKeyPrefix],
// [LocatedKeyPrefix], [ResidueKeyPrefix] and [ProvisionedKeyPrefix] are:
// internal/command's store construction and this package's own
// namespace-safety tests both have to name one definition.
func RootOutputKeyPrefix(estate string) string {
	return rootOutputNamespaceRoot + "/" + estate
}

// RootOutputKey is the store key one root output's remembered value lives at,
// for estate.
//
// The name is encoded by [recordKeyEncoding] for the reason every other key
// in this package encodes what it names: the alphabet a store key may use is
// the intersection of what SSM parameter names, S3 object keys and filesystem
// paths all accept, and encoding is how that stops being a question. An HCL
// output name happens to be within it today; deriving the key the same way as
// its four siblings means nobody has to re-check that.
//
// There is deliberately no reverse of this function, for [ProvisionedKey]'s
// reason: a reverse exists so a LISTING can recover a name, and building one
// would be building the first half of an enumeration this namespace is
// defined by not having.
func RootOutputKey(estate, name string) string {
	return RootOutputKeyPrefix(estate) + "/" + recordKeyEncoding.EncodeToString([]byte(name))
}

// rootOutputFormatVersion identifies the JSON shape [rootOutputPayload]
// writes. An unrecognized version is skipped rather than guessed at - see
// [RootOutputStore.Get] for why a problem here never stops a run.
const rootOutputFormatVersion = "tofu-live-root-output-v1"

// rootOutputPayload is what one root output's key holds.
//
// It shares no field name with [recordPayload] - no value_type, no attrs, no
// private, no status - which is what makes [decodeRecordPayload] refuse it
// outright rather than half-read it, the same third stop between one of these
// keys and the destroy path that [provisionedPayload] relies on.
type rootOutputPayload struct {
	// FormatVersion is always [rootOutputFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// Name is the output name this record is for, written out in full.
	// Redundant with the key, and deliberately so: [RootOutputStore.Get]
	// checks the two agree, so a key copied or hand-edited into pointing at
	// another output answers about nothing rather than about that output.
	Name string `json:"name"`

	// Type is the value's own cty type, as ctyjson.MarshalType writes it -
	// what lets the value be rebuilt with no schema and no configuration in
	// hand, the same self-describing convention [recordPayload] uses and
	// internal/states/statefile's version 4 established.
	Type json.RawMessage `json:"type"`

	// Value is the value itself, ctyjson-encoded against Type.
	Value json.RawMessage `json:"value"`
}

// RootOutputStore is the point-lookup view of an estate's remembered root
// output values.
//
// It is NOT a [staterecord.Store] and does not embed one, for the reason
// [LocatedStore]'s and [ProvisionedStore]'s doc comments set out in full: no
// List, no Keys, no iteration of any kind, and every method keyed by a name
// the caller must already hold - which in practice means a name the
// CONFIGURATION declares. "What else is in here" is not a question this type
// can be asked.
//
// That is also what makes a removed `output` block cost nothing: its key sits
// inert, unread because the configuration no longer names it, and no sweep
// exists that could mistake it for something with a live object behind it.
type RootOutputStore struct {
	store  staterecord.Store
	estate string
}

// NewRootOutputStore wraps store as estate's root output store. store is
// ordinarily the same [staterecord.Store] the run's record-backed,
// record-located, residue-carrying and provisioner-tainted resources use -
// one store, five disjoint namespaces in it.
//
// A nil store yields a nil RootOutputStore, and every method on a nil
// receiver is the "nothing remembered" answer. An estate with no record_store
// block therefore keeps exactly the behaviour it had before this file
// existed: an output the projection cannot evaluate renders as newly created.
// That is visible rather than silent, which is why it is allowed to be the
// default.
func NewRootOutputStore(store staterecord.Store, estate string) *RootOutputStore {
	if store == nil || estate == "" {
		return nil
	}
	return &RootOutputStore{store: store, estate: estate}
}

// Get reads the value remembered for one root output. exists is false when
// nothing has been recorded, which is the ordinary answer for an estate
// migrated before this namespace existed, for an output added since the last
// apply, and for a sensitive output (see [WriteRootOutputValues]).
//
// version is the store's version for the key, for a later conditional Put.
//
// A record that is present and cannot be read reports exists=false and a
// non-nil error, and EVERY caller in this package logs that error and carries
// on. That is a deliberate difference from [ProvisionedStore.Get]'s fatal
// treatment of the same situation, and the difference is what the two records
// mean: an unreadable provisioner record silently calls an unprovisioned live
// object healthy, while an unreadable record here costs one root output its
// prior value and renders as the "+" line it rendered as before this
// namespace existed. Nothing in this file may fail a run, for
// [ApplyRootOutputValues]'s stated reason - a pre-plan probe over a
// deliberately partial state must never be the thing that refuses an estate.
func (s *RootOutputStore) Get(ctx context.Context, name string) (val cty.Value, version string, exists bool, err error) {
	if s == nil {
		return cty.NilVal, "", false, nil
	}
	payload, version, exists, err := s.store.Get(ctx, RootOutputKey(s.estate, name))
	if err != nil {
		return cty.NilVal, "", false, fmt.Errorf("reading the remembered value of the root output %q: %w", name, err)
	}
	if !exists {
		return cty.NilVal, "", false, nil
	}
	var stored rootOutputPayload
	if err := json.Unmarshal(payload, &stored); err != nil {
		return cty.NilVal, "", false, fmt.Errorf("decoding the remembered value of the root output %q: %w", name, err)
	}
	if stored.FormatVersion != rootOutputFormatVersion {
		return cty.NilVal, "", false, fmt.Errorf("the remembered value of the root output %q names format %q, which this version of choudoufu does not understand", name, stored.FormatVersion)
	}
	if stored.Name != name {
		return cty.NilVal, "", false, fmt.Errorf("the record stored for the root output %q says it is for %q; refusing to answer about one output from another output's value", name, stored.Name)
	}
	ty, err := ctyjson.UnmarshalType(stored.Type)
	if err != nil {
		return cty.NilVal, "", false, fmt.Errorf("the remembered type of the root output %q could not be read: %w", name, err)
	}
	val, err = ctyjson.Unmarshal(stored.Value, ty)
	if err != nil {
		return cty.NilVal, "", false, fmt.Errorf("the remembered value of the root output %q could not be read: %w", name, err)
	}
	return val, version, true, nil
}

// Put records val as the value of one root output, conditional on the key's
// current version - expectedVersion "" asserting that nothing is recorded
// yet, the same convention [staterecord.Store.PutIfVersion] uses. It returns
// the new version.
//
// val must be wholly known and carry no marks. Both are properties of a value
// that has been through a state file, which is where both callers get theirs
// ([WriteRootOutputValues] takes a [states.Module]'s OutputValues), and
// neither is something this function can repair: ctyjson.Marshal panics on a
// marked leaf and encodes an unknown as null, so a value that fails either
// test is refused here rather than persisted as a lie about itself.
func (s *RootOutputStore) Put(ctx context.Context, name string, val cty.Value, expectedVersion string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("no record store is configured, so the value of the root output %q cannot be remembered", name)
	}
	if val == cty.NilVal {
		return "", fmt.Errorf("the root output %q has no value to remember", name)
	}
	if !val.IsWhollyKnown() {
		return "", fmt.Errorf("the value of the root output %q is not wholly known, so there is nothing settled to remember", name)
	}
	if val.ContainsMarked() {
		return "", fmt.Errorf("the value of the root output %q carries marks, which cannot be encoded; unmark it before recording", name)
	}
	ty, err := ctyjson.MarshalType(val.Type())
	if err != nil {
		return "", fmt.Errorf("encoding the type of the root output %q: %w", name, err)
	}
	encoded, err := ctyjson.Marshal(val, val.Type())
	if err != nil {
		return "", fmt.Errorf("encoding the value of the root output %q: %w", name, err)
	}
	payload, err := json.Marshal(rootOutputPayload{
		FormatVersion: rootOutputFormatVersion,
		Name:          name,
		Type:          ty,
		Value:         encoded,
	})
	if err != nil {
		return "", fmt.Errorf("encoding the record for the root output %q: %w", name, err)
	}
	return s.store.PutIfVersion(ctx, RootOutputKey(s.estate, name), payload, expectedVersion)
}

// ReadRootOutputValues reads back what this estate remembers for every root
// output the CONFIGURATION declares, for [ApplyRootOutputValues] to fall back
// on. The configuration is what bounds the read: this namespace has no
// listing, so a name nothing declares is never asked for and a key left
// behind by a deleted `output` block is never seen.
//
// Cost: one point lookup per DECLARED root output, per plan or apply, and
// none at all for a configuration that declares none - which is every estate
// tools/estate-gen generates and therefore everything the plan-call budget
// ratchet (live/plan-budget.json) measures. The lookups are store reads, not
// provider calls, so nothing here reaches that ratchet or the cloud APIs it
// counts. A batched read was not built: the namespace has no listing on
// purpose, and a batch API keyed by a caller-supplied name list would be the
// first half of one.
//
// A store that is nil, a configuration with no outputs, and a record that
// will not decode all produce the same thing - an absent entry - and none of
// them raises anything. See [RootOutputStore.Get] for why every failure here
// is logged and skipped rather than reported: the cost of one is a single
// output rendering as newly created, which is the honest gap this whole
// namespace narrows rather than a problem with the estate.
func ReadRootOutputValues(ctx context.Context, store *RootOutputStore, config *configs.Config) map[string]cty.Value {
	if store == nil || config == nil || config.Module == nil || len(config.Module.Outputs) == 0 {
		return nil
	}
	names := make([]string, 0, len(config.Module.Outputs))
	for name := range config.Module.Outputs {
		names = append(names, name)
	}
	sort.Strings(names)

	var out map[string]cty.Value
	for _, name := range names {
		val, _, exists, err := store.Get(ctx, name)
		if err != nil {
			log.Printf("[WARN] live: the remembered value of the root output %q could not be read, so it has no prior value this run: %s", name, err)
			continue
		}
		if !exists {
			continue
		}
		if out == nil {
			out = make(map[string]cty.Value, len(names))
		}
		out[name] = val
	}
	return out
}

// WriteRootOutputValues remembers the root output values a state settled, so
// the next stateless plan has the "before" side stock reads out of its state
// file. Both writers go through it: `live-import`, whose state is the stock
// tfstate a migration was pointed at, and [WriteBack], whose state is the one
// an apply just produced.
//
// It is best-effort in every direction and returns nothing. An output that
// cannot be encoded, a store that will not take the write, a conflicting
// concurrent write - each costs that one output its prior value on the next
// run, which is the "+" line it renders today. Neither a migration nor an
// apply may fail over one, and the two callers are respectively a command
// whose whole contract is that it stamps and records what it can and reports
// what it could not, and a write-back that runs after the cloud has already
// changed.
//
// # What is deliberately not written
//
// A SENSITIVE output is skipped, and the skip is scope rather than
// squeamishness. HANDOFF.md's default is that secrets are stored the way
// stock stores them, with refusal as the strict toggle - and that toggle
// ("no secrets stored by the tool") has no wiring that reaches this file yet.
// Writing sensitive output values here first and adding the toggle afterwards
// would put material into a store that an operator who had turned the toggle
// on believed was free of it. So the strict answer is the one taken until the
// toggle reaches here, and the cost of it is small and visible: a sensitive
// output renders as "+ name = (sensitive value)", which is what it rendered
// as before this namespace existed and says nothing an onlooker did not
// already know.
//
// An output whose value is not wholly known is skipped too. A state file's
// output values always are, so this is a guard on the type rather than a case
// with a name.
//
// # The one corner this leaves, stated rather than left to be rediscovered
//
// Nothing here DELETES. After a destroy the final state carries no output
// values, so every key this estate wrote stays where it is, and the plan that
// follows a destroy diffs a recomputed "after" against a remembered "before"
// where stock - whose state file the destroy emptied - would show the output
// as new. The result is one output line reading "~ old -> (known after
// apply)" instead of "+ name = (known after apply)", on a plan that is
// already proposing to rebuild the whole estate.
//
// Deleting instead was considered and not taken, because the two cases are
// not distinguishable from here: a final state missing an output because a
// destroy removed it looks exactly like one missing it because this apply was
// scoped and never evaluated it, and deleting on the second would throw away
// a value that is still correct. A cosmetic line on a plan that is rebuilding
// everything is the cheaper of the two errors.
func WriteRootOutputValues(ctx context.Context, store *RootOutputStore, state *states.State) {
	if store == nil || state == nil {
		return
	}
	root := state.RootModule()
	if root == nil || len(root.OutputValues) == 0 {
		return
	}
	names := make([]string, 0, len(root.OutputValues))
	for name := range root.OutputValues {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		ov := root.OutputValues[name]
		if ov == nil || ov.Sensitive {
			continue
		}
		val := ov.Value
		if val == cty.NilVal || !val.IsWhollyKnown() {
			continue
		}
		// A state file carries no marks, but a caller handing us a state it
		// built in memory can, and Put refuses a marked value rather than
		// panicking inside ctyjson. Take them off here so that the one
		// in-memory caller ([WriteBack], whose FinalState came off an apply
		// walk) is not the odd one out.
		val, _ = val.UnmarkDeep()

		// The expected version is read immediately before the write rather
		// than carried from a plan-time read, because there is no plan-time
		// read of this namespace to carry: [ReadRootOutputValues] runs on a
		// projection that may not be this run's at all, and `live-import`
		// never read the key. A conflict means another writer got there
		// first with a value from a state at least as new as this one, which
		// is the case where losing is correct.
		_, version, _, err := store.Get(ctx, name)
		if err != nil {
			// An unreadable record is still a record, and its version is
			// what a conditional write needs. Get does not return one for a
			// payload it could not decode, so ask the store directly rather
			// than leaving a corrupt key standing forever.
			version = store.rawVersion(ctx, name)
		}
		if _, err := store.Put(ctx, name, val, version); err != nil {
			log.Printf("[WARN] live: the value of the root output %q could not be remembered, so it will render as newly created on the next stateless plan: %s", name, err)
		}
	}
}

// rawVersion reports the version the store holds for name's key right now,
// "" when the key does not exist, without decoding the payload.
//
// It exists for [ProvisionedStore.currentVersion]'s reason: a payload too
// broken for [RootOutputStore.Get] to read is still a payload occupying the
// key, and the caller is about to overwrite it on the strength of a state
// that has already settled. What the unreadable bytes say has no bearing on
// that.
func (s *RootOutputStore) rawVersion(ctx context.Context, name string) string {
	if s == nil {
		return ""
	}
	_, version, exists, err := s.store.Get(ctx, RootOutputKey(s.estate, name))
	if err != nil || !exists {
		return ""
	}
	return version
}

// writeBackRootOutputs is [WriteBack]'s root-output half: the apply that just
// finished settled these values, so the next stateless plan should diff
// against them rather than call every one of them new.
//
// It is the sibling of writeBackLocated, writeBackResidue and
// writeBackProvisioned, and the shortest of the four because a root output
// has no live object, no identity, no provider and nothing to reconcile: the
// final state says what the value is, and that is the whole of it.
func writeBackRootOutputs(ctx context.Context, req WriteBackRequest) {
	WriteRootOutputValues(ctx, req.RootOutputStore, req.FinalState)
}
