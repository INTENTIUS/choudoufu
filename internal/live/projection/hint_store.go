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
	"log"
	"sort"
	"time"

	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is guided discovery's hint riding the estate's record store
// (issue #109): a set of resource type names plus a timestamp, written after
// an apply's final state persist and read back by internal/live/discovery's
// guided mode to decide what the estate-wide sweep can skip on a routine
// pass. It is a plan-cost cache and never authority: any problem writing it
// is a warning, any problem reading it is "fall back to full enumeration",
// and TestGuided_equivalence in internal/live/discovery is the proof that a
// missing or broken hint changes only what a pass costs, never what it does.

// hintNamespaceRoot is the literal segment the hint's key lives under. It is
// a different literal from recordNamespaceRoot ("tofu-records") and from
// live/RECEIPTS.md's "tofu-receipts" segment on purpose: the same
// disjoint-by-construction namespace safety those two keep between records
// and receipts, extended to the hint, so that orphan discovery
// (builder.discoverOrphanedRecords), which lists the record namespace and
// treats what it finds as resource records, can never see a hint key.
// internal/configs' validateRecordStoreKeyPrefix enforces the same
// disjointness against an operator-supplied key_prefix override, exactly as
// it already does for "tofu-receipts".
const hintNamespaceRoot = "tofu-hints"

// hintFormatVersion identifies the JSON shape [hintRecord] writes. It is
// this fork's own cache shape, free to change: readHintPayload treats an
// unrecognized version as "no hint", which costs guided discovery one cold
// full-enumeration pass and can never produce a wrong plan.
const hintFormatVersion = "tofu-live-hint-v1"

// HintKey is the record-store key one estate's guided-discovery hint lives
// at. Exported for the same reason [RecordKeyPrefix] is: internal/command
// and the namespace-safety tests both need to name the one definition.
func HintKey(estate string) string {
	return hintNamespaceRoot + "/" + estate + "/guided"
}

// hintRecord is the hint's wire shape: the whole of what survives issue
// #109's removal of the observational snapshot. Everything here is a type
// name or a timestamp; there is no field an attribute value, an identifier
// or a marker could travel in, so the redaction machinery the snapshot
// needed has nothing to do here.
type hintRecord struct {
	// FormatVersion is always [hintFormatVersion].
	FormatVersion string `json:"formatVersion"`

	// WrittenAt is when the hint was built, RFC3339. It is the staleness
	// signal guided discovery's GuidedMaxAge / GuidedVerifyAge rules gate
	// on.
	WrittenAt string `json:"writtenAt"`

	// Estate is the estate name this run resolved.
	Estate string `json:"estate"`

	// Types is the sorted set of resource types the run's final state
	// spanned. A type's absence means "this run recorded nothing of it",
	// not "this estate has none" - the same staleness contract the
	// snapshot's own type roster carried.
	Types []string `json:"types"`
}

// encodeHintRecord renders state into [hintRecord] bytes, attributed to
// estate and timestamped writtenAt. A nil or empty state yields an empty
// type roster, which is a legitimate answer (an empty estate, or a persist
// before anything was written).
func encodeHintRecord(state *states.State, estate string, writtenAt time.Time) ([]byte, error) {
	rec := hintRecord{
		FormatVersion: hintFormatVersion,
		WrittenAt:     writtenAt.UTC().Format(time.RFC3339),
		Estate:        estate,
		Types:         hintTypes(state),
	}
	return json.Marshal(rec)
}

// hintTypes is the set of resource types state's current (non-deposed)
// resource instance objects span, sorted. The same instances the snapshot's
// resource roster walked, reduced to nothing but their type names.
func hintTypes(state *states.State) []string {
	if state == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, rec := range state.AllResourceInstanceObjectAddrs() {
		if rec.DeposedKey != states.NotDeposed {
			continue
		}
		seen[rec.Instance.Resource.Resource.Type] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// ReadHintStore reads the guided-discovery hint for estate out of store and
// returns it as a [Hint]. Every failure - no store, no settled estate name,
// a store that cannot be read, no hint recorded yet, a payload that does not
// parse or names a format this build does not recognize - comes back as a
// non-nil error and a nil Hint; there is no partial or best-effort result.
// The caller's contract (internal/live/discovery's guided.go) is to treat
// every such error as "fall back to today's full enumeration", never to
// surface it as a run failure.
func ReadHintStore(ctx context.Context, store staterecord.Store, estate string) (*Hint, error) {
	if store == nil {
		return nil, errors.New("no record store is configured")
	}
	if estate == "" {
		return nil, errors.New("the estate name is not settled, so there is no hint key to read")
	}
	payload, _, exists, err := store.Get(ctx, HintKey(estate))
	if err != nil {
		return nil, fmt.Errorf("reading the hint record: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("no hint has been recorded for estate %q yet", estate)
	}
	return readHintPayload(payload)
}

// readHintPayload parses payload as a [hintRecord] and reduces it to a
// [Hint].
func readHintPayload(payload []byte) (*Hint, error) {
	var rec hintRecord
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, fmt.Errorf("decoding the hint record: %w", err)
	}
	if rec.FormatVersion != hintFormatVersion {
		return nil, fmt.Errorf("unrecognized hint format %q", rec.FormatVersion)
	}
	h := &Hint{
		Estate: rec.Estate,
		Types:  make(map[string]bool, len(rec.Types)),
	}
	if when, err := time.Parse(time.RFC3339, rec.WrittenAt); err == nil {
		h.WrittenAt = when
	}
	for _, t := range rec.Types {
		h.Types[t] = true
	}
	return h, nil
}

// writeHint writes the guided-discovery hint for estate to store, built
// from state as of writtenAt, and returns the warning diagnostics the
// attempt earned - nil for a clean write. It never returns an error: a
// cache that could fail an apply would not be a cache, the same rule 5 the
// snapshot write always had.
//
// The write is a read-then-conditional-put against the hint's current
// version. A version conflict means a concurrent run wrote a hint of its
// own between this run's read and its write; that hint is as fresh as this
// one and records the same estate, so losing the race is logged and not
// even a warning.
func writeHint(ctx context.Context, store staterecord.Store, estate string, state *states.State, writtenAt time.Time) tfdiags.Diagnostics {
	if estate == "" {
		return hintWriteWarning(estate, errors.New("the estate name is not settled, so there is no hint key to write"))
	}
	payload, err := encodeHintRecord(state, estate, writtenAt)
	if err != nil {
		return hintWriteWarning(estate, err)
	}
	key := HintKey(estate)
	_, version, _, err := store.Get(ctx, key)
	if err != nil {
		return hintWriteWarning(estate, err)
	}
	if _, err := store.PutIfVersion(ctx, key, payload, version); err != nil {
		var conflict *staterecord.VersionConflictError
		if errors.As(err, &conflict) {
			log.Printf("[TRACE] projection: a concurrent run wrote the guided-discovery hint for estate %q first; keeping theirs", estate)
			return nil
		}
		return hintWriteWarning(estate, err)
	}
	return nil
}

// hintWriteWarning wraps one failed hint write in the sourceless warning
// shape the manager's persistence side effects report through, and logs it.
func hintWriteWarning(estate string, err error) tfdiags.Diagnostics {
	log.Printf("[WARN] stateless hint: writing the guided-discovery hint for estate %q: %s", estate, err)
	return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Could not write the discovery hint",
		fmt.Sprintf(
			"Writing guided discovery's hint for estate %q to the record store failed: %s. The hint is a plan-cost cache: without a fresh one, the next run's estate-wide sweep enumerates every admitted type instead of skipping the quiet ones, which costs more and changes nothing else.",
			estate, err,
		),
	))
}
