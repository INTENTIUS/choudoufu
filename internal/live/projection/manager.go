// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// Manager is the state manager a stateless run uses: the roadmap's
// statemgr.Projection. It satisfies [statemgr.Full] so that the ordinary
// plan and apply operations in internal/backend/local can run unchanged,
// and it has no persistent side at all.
//
//   - The transient side is an ordinary in-memory snapshot, because the
//     operation genuinely needs one: OpenTofu Core reports progress by
//     mutating the manager's state as the apply walks, and apply writes the
//     final state to it. Refusing to hold that would break the operation
//     without removing any authority.
//   - RefreshState is a no-op. There is nothing persistent to read from, and
//     the prior state a stateless run plans against does not arrive this way:
//     it is a projection built from the live system by the run's own
//     pipeline and handed to the operation (see internal/backend/local's
//     StatelessRun seam). A projection needs the configuration and configured
//     providers to build, and the state manager interface offers neither.
//   - PersistState writes nothing authoritative, anywhere, and cannot fail
//     the operation. It is not a "write to /dev/null" adapter over a file
//     manager; there is no file manager, no state path, and no state
//     serializer reachable from this type. What it may optionally do is
//     described next.
//   - Lock and Unlock take no lock and create no lock file. A lock exists to
//     stop two processes from writing one record, and there is no record.
//
// Persists counts the PersistState calls so that a test can prove the
// persistence path was exercised and still wrote no state, which is a
// stronger claim than "no state file appeared" alone: it distinguishes "we
// never tried" from "we tried and it wrote nothing".
//
// # The optional guided-discovery hint (issue #109)
//
// PersistState is also the single place guided discovery's hint hangs off
// of: the estate's resource type roster plus a timestamp, written to the
// estate's record store at [HintKey] when [Manager.EnableHint] has been
// called (see hint_store.go). A Manager nobody calls EnableHint on writes
// nothing, ever, which is the "no record_store block -> nothing written"
// contract living at this layer rather than only in the config decoder.
//
// PersistState is called from two places in internal/backend/local: the
// apply's state hook calls it periodically while the graph walk is in
// progress (roughly every 20s, plus a forced call on interrupt), and
// [statemgr.WriteAndPersist] calls it exactly once more, always last, after
// the apply has finished and the final state has been written to this
// manager. Both call sites are stock code, and neither passes this type any
// signal that would distinguish "this is a periodic tick" from "this is the
// final call".
//
// Given that, this Manager writes the hint on every PersistState call
// rather than trying to detect the final one. The write is cheap - a type
// roster and a timestamp, a few hundred bytes, no provider calls, no cloud
// reads beyond the store put - and it is still correct: the final call is
// chronologically last by construction, so whatever a periodic tick wrote
// is unconditionally overwritten by the final hint before the apply
// returns. A periodic hint mid-apply reflecting a partial estate is not a
// bug either - staleness is already this cache's whole contract, and "was
// true a moment ago" is a specific and harmless case of "may be stale".
//
// PersistState's own contract - "cannot fail the operation" - extends to
// the hint: a write failure is recorded as a warning retrievable through
// [Manager.HintWarning] and logged, and PersistState still returns nil. A
// cache that could fail an apply would not be a cache.
//
// Manager deliberately does not implement [statemgr.PersistentMeta] or
// statemgr.Migrator. State metadata (a lineage and a serial) is exactly
// the bookkeeping that makes a stored state authoritative, and the plan-file
// staleness check that reads it has no meaning for a run whose prior state
// is rebuilt every time. The hint does not change this: it has no lineage,
// no serial, and is a JSON shape (hintRecord) that is not a statefile at
// all, exactly so that nothing could mistake it for one.
type Manager struct {
	mu       sync.Mutex
	current  *states.State
	persists int

	// hintStore and hintEstate are guided discovery's hint carrier (issue
	// #109): when hintStore is non-nil, every PersistState call writes the
	// estate's type roster and a timestamp to [HintKey](hintEstate) in it.
	// Nil disables the write entirely - the default, and the only state a
	// Manager nobody called EnableHint on is ever in.
	hintStore  staterecord.Store
	hintEstate string
	clock      func() time.Time

	// hintWarning is the diagnostic from the most recent failed hint
	// write, or nil if the last attempt succeeded or none was made. It
	// exists because PersistState's own return value cannot carry it:
	// returning an error there would turn a cache failure into an apply
	// failure.
	hintWarning tfdiags.Diagnostics

	// cachePath is where PersistState writes the state snapshot, or "" to
	// write none. Issue #685: this fork's own documentation says a state
	// file "becomes a cache rather than the record of what you own", and
	// for a long time that was implemented as writing nothing at all - so
	// there was no cache, and every plan rebuilt prior state from live
	// reads. Demoted is not deleted.
	//
	// What makes keeping one safe here, and unsafe for stock, is that
	// identity lives on the resource. A cached entry is a CANDIDATE to be
	// verified against the tag index, never a fact to be trusted, so a
	// stale or absent cache costs reads and cannot cost correctness. The
	// one-sided oracle is unchanged: a marker present proves existence, a
	// marker absent proves nothing.
	//
	// The write carries PersistState's contract exactly as the hint does -
	// a failure is a warning, never an operation failure. A cache that
	// could fail an apply would not be a cache.
	cachePath string

	// cacheWarning is the diagnostic from the most recent failed cache
	// write, kept separate from hintWarning so a reader can tell which of
	// the two side effects failed.
	cacheWarning tfdiags.Diagnostics
}

var _ statemgr.Full = (*Manager)(nil)

// NewManager returns a state manager holding an empty snapshot.
func NewManager() *Manager {
	return &Manager{current: states.NewState()}
}

// State implements [statemgr.Reader].
func (m *Manager) State() *states.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.current.DeepCopy()
}

// WriteState implements [statemgr.Writer].
func (m *Manager) WriteState(state *states.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = state.DeepCopy()
	return nil
}

// MutateState implements [statemgr.Writer].
func (m *Manager) MutateState(fn func(*states.State) *states.State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = fn(m.current)
	return nil
}

// RefreshState implements [statemgr.Refresher] by doing nothing. See the
// type documentation for why the projection does not arrive through here.
func (m *Manager) RefreshState(context.Context) error {
	return nil
}

// PersistState implements [statemgr.Persister] by writing no state,
// authoritative or otherwise capable of affecting the operation. Its only
// optional side effect is the guided-discovery hint described on the type;
// see there for why every call attempts the write rather than only the
// final one, and why a write failure surfaces only as
// [Manager.HintWarning], never as this method's return value.
//
// The schemas argument is unused: the hint carries type names and a
// timestamp, nothing a sensitivity mark could apply to.
func (m *Manager) PersistState(ctx context.Context, _ *tofu.Schemas) error {
	m.mu.Lock()
	m.persists++
	cachePath := m.cachePath
	if cachePath != "" {
		// Snapshot under the lock, write outside it, for the same reason
		// the hint does: states.State is not safe for concurrent
		// read/write.
		snapshot := m.current.DeepCopy()
		m.mu.Unlock()
		warning := writeStateCache(cachePath, snapshot)
		m.mu.Lock()
		m.cacheWarning = warning
	}
	store := m.hintStore
	if store == nil {
		m.mu.Unlock()
		return nil
	}
	estate := m.hintEstate
	clock := m.clock
	if clock == nil {
		clock = time.Now
	}
	// A copy, not the live pointer: writeHint walks the state after the
	// lock is released, and states.State is not safe for concurrent
	// read/write (see its own doc comment).
	state := m.current.DeepCopy()
	m.mu.Unlock()

	warning := writeHint(ctx, store, estate, state, clock())

	m.mu.Lock()
	defer m.mu.Unlock()
	m.hintWarning = warning
	return nil
}

// Persists is the number of times PersistState has been called. Every call
// wrote no authoritative state; see the type documentation for the one
// optional exception (the guided-discovery hint) and why it does not count
// as state either.
func (m *Manager) Persists() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persists
}

// EnableHint turns on guided discovery's hint write (issue #109): from the
// next PersistState call onward, the estate's resource type roster and a
// timestamp are written to [HintKey](estate) in store. clock supplies the
// "writtenAt" timestamp; a nil clock defaults to time.Now, and tests inject
// a fixed one for reproducible output.
//
// Not calling this method at all is the manager's half of "no record_store
// in the live block -> nothing written, ever": there is no default store a
// Manager falls back to, and nothing else in this package can turn the
// hint write on. The caller (internal/command's stateless runner) calls it
// once the estate name is settled and the live block's record store is
// open, which is why the estate here is always the settled name, never a
// placeholder filled in later.
func (m *Manager) EnableHint(store staterecord.Store, estate string, clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hintStore = store
	m.hintEstate = estate
	m.clock = clock
}

// HintWarning returns the diagnostic from the most recent failed hint
// write, or nil diagnostics if the last attempt succeeded or the hint was
// never enabled. It is how a write failure becomes observable without
// PersistState ever returning an error for it: a cache that can fail an
// apply is a record, not a cache.
func (m *Manager) HintWarning() tfdiags.Diagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hintWarning
}

// EnableStateCache makes PersistState write the state snapshot to path.
// Passing "" disables it, which is the state a Manager nobody called this on
// is in. Issue #685.
func (m *Manager) EnableStateCache(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cachePath = path
}

// CacheWarning returns the diagnostic from the most recent failed cache
// write, or nil. Separate from [Manager.HintWarning] so a reader can tell
// which of PersistState's two side effects failed.
func (m *Manager) CacheWarning() tfdiags.Diagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cacheWarning
}

// writeStateCache writes snapshot to path as an ordinary statefile, via a
// temporary file and a rename so a reader never sees a half-written cache and
// an interrupted write leaves the previous one intact.
//
// It returns a warning rather than an error, and every caller must treat it
// that way: this is PersistState's contract, and a cache that could fail an
// apply would not be a cache.
//
// The file carries a lineage and serial because statefile.Write requires
// them, but nothing reads them for authority. The cache is a candidate set to
// be verified against the tag index, so a lineage mismatch is not a conflict
// to refuse - it is a cache to ignore.
func writeStateCache(path string, snapshot *states.State) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	warn := func(err error) tfdiags.Diagnostics {
		log.Printf("[WARN] projection: could not write the state cache to %s: %s", path, err)
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Could not write the state cache",
			fmt.Sprintf("choudoufu could not write %s: %s.\n\nThis does not affect the result of this run. The next plan will rebuild prior state from live reads instead of starting from the cache, which costs API calls and not correctness.", path, err),
		))
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return warn(err)
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return warn(err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	sf := &statefile.File{
		Lineage: stateCacheLineage,
		Serial:  1,
		State:   snapshot,
	}
	if err := statefile.Write(sf, f, encryption.StateEncryptionDisabled()); err != nil {
		_ = f.Close()
		return warn(err)
	}
	if err := f.Close(); err != nil {
		return warn(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return warn(err)
	}
	log.Printf("[DEBUG] projection: wrote the state cache to %s (%d managed resource(s))", path, len(snapshot.RootModule().Resources))
	return diags
}

// stateCacheLineage is a fixed, recognisable lineage. It is deliberately not
// random: nothing treats this file as authoritative, so a lineage exists only
// because the statefile format carries one, and a constant makes it obvious in
// a file that this is a cache rather than a state of record.
const stateCacheLineage = "choudoufu-state-cache"

// GetRootOutputValues implements [statemgr.OutputReader] from the in-memory
// snapshot.
func (m *Manager) GetRootOutputValues(context.Context) (map[string]*states.OutputValue, error) {
	state := m.State()
	if state == nil {
		return map[string]*states.OutputValue{}, nil
	}
	root := state.RootModule()
	if root == nil {
		return map[string]*states.OutputValue{}, nil
	}
	return root.OutputValues, nil
}

// Lock implements [statemgr.Locker] without taking a lock.
//
// The returned id is a constant rather than a random one so that a stray
// Unlock with the wrong id is impossible to construct: there is only one
// possible id, and it unlocks nothing.
func (m *Manager) Lock(context.Context, *statemgr.LockInfo) (string, error) {
	return "stateless", nil
}

// Unlock implements [statemgr.Locker] without releasing anything.
func (m *Manager) Unlock(context.Context, string) error {
	return nil
}
