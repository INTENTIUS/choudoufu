// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/opentofu/opentofu/internal/states"
	"github.com/opentofu/opentofu/internal/states/statemgr"
	"github.com/opentofu/opentofu/internal/tfdiags"
	"github.com/opentofu/opentofu/internal/tofu"
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
// # The optional snapshot (P4.2)
//
// PersistState is also the single place the optional observational
// snapshot hangs off of: a scrubbed, metadata-only JSON
// cache of the manager's current in-memory state, carried either as a file
// at the path the "live" block's snapshot_path argument names
// ([Manager.EnableSnapshot]) or as a commit on an orphan git branch when the
// block sets snapshots = true ([Manager.EnableSnapshotBranch]); the routing
// between the two, including the file's role as the branch's fallback, lives
// in writeSnapshotCarriers. A Manager nobody calls either method on writes
// nothing, ever, which is the "no attribute -> nothing written" contract
// living at this layer rather than only in the config decoder.
//
// PersistState is called from two places in internal/backend/local: the
// apply's state hook calls it periodically while the graph walk is in
// progress (roughly every 20s, plus a forced call on interrupt), and
// [statemgr.WriteAndPersist] calls it exactly once more, always last, after
// the apply has finished and the final state has been written to this
// manager - that is the "final call" P4.1's handoff names. Both call sites
// are stock code this task does not touch, and neither passes this type any
// signal that would distinguish "this is a periodic tick" from "this is the
// final call": the schemas argument is non-nil either way.
//
// Given that, this Manager writes the snapshot on every PersistState call
// rather than trying to detect the final one. Three reasons: first, doing
// otherwise is not possible without threading a new signal through
// backend_apply.go and hook_state.go, which is out of this task's scope by
// design (see the roadmap handoff); second, the write is cheap - one
// resource per line of metadata and a hash, no provider calls, no cloud
// reads - so a periodic tick during a long apply costs a small atomic file
// write, not a meaningful slowdown; third, and most importantly, it is
// still correct: the final call is chronologically last by construction, so
// whatever a periodic tick wrote is unconditionally overwritten by the
// final snapshot before the apply returns. The file's content once the
// process exits is always the final one. A periodic snapshot mid-apply
// showing a partial estate is not a bug either - staleness is already this
// cache's whole contract, and "was true a moment ago" is a specific and
// harmless case of "may be stale".
//
// PersistState's own contract - "cannot fail the operation" - extends to
// the snapshot: a write failure (an unwritable directory, a full disk) is
// recorded as a warning retrievable through [Manager.SnapshotWarning] and
// logged, and PersistState still returns nil. A cache that could fail an
// apply would not be a cache.
//
// Manager deliberately does not implement [statemgr.PersistentMeta] or
// statemgr.Migrator. Snapshot metadata (a lineage and a serial) is exactly
// the bookkeeping that makes a stored state authoritative, and the plan-file
// staleness check that reads it has no meaning for a run whose prior state
// is rebuilt every time. The P4.2 snapshot does not change this: it has no
// lineage, no serial, and is a JSON shape ([snapshot]) that is not a
// statefile at all, exactly so that nothing could mistake it for one.
type Manager struct {
	mu       sync.Mutex
	current  *states.State
	persists int

	// snapshotPath is where the optional observational snapshot's file
	// carrier writes, or "" to disable the file entirely - the default, and
	// half of the only state a Manager nobody called EnableSnapshot or
	// EnableSnapshotBranch on is ever in. When snapshotBranchDir is also
	// set, the path is the fallback carrier rather than the primary one;
	// see PersistState.
	snapshotPath string

	// snapshotBranchDir is the module directory whose enclosing git
	// repository receives the snapshot's branch carrier (one commit per
	// persist on refs/heads/tofu-snapshots/<estate>), or "" to disable the
	// branch entirely - the other half of the default.
	snapshotBranchDir string

	snapshotEstate string
	clock          func() time.Time

	// snapshotWarning is the diagnostic from the most recent failed
	// snapshot write, or nil if the last attempt succeeded or none was
	// made. It exists because PersistState's own return value cannot carry
	// it: returning an error there would turn a cache failure into an
	// apply failure, which rule 5 of P4.2 forbids.
	snapshotWarning tfdiags.Diagnostics
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
// optional side effect is the P4.2 snapshot described on the type; see
// there for why every call attempts the write rather than only the final
// one, and why a write failure surfaces only as [Manager.SnapshotWarning],
// never as this method's return value.
//
// The schemas argument is forwarded to [buildSnapshot] rather than
// discarded. It is the only thing in this method's reach that knows which of
// a resource's attributes a provider declares sensitive, and the snapshot
// writes a few individual values (an import ID, an identity, the ownership
// markers) rather than only hashes - so throwing it away here is what let
// secrets reach the file in the clear.
func (m *Manager) PersistState(_ context.Context, schemas *tofu.Schemas) error {
	m.mu.Lock()
	m.persists++
	path := m.snapshotPath
	branchDir := m.snapshotBranchDir
	if path == "" && branchDir == "" {
		m.mu.Unlock()
		return nil
	}
	estate := m.snapshotEstate
	clock := m.clock
	if clock == nil {
		clock = time.Now
	}
	// A copy, not the live pointer: buildSnapshot walks the state after the
	// lock is released, and states.State is not safe for concurrent
	// read/write (see its own doc comment).
	state := m.current.DeepCopy()
	m.mu.Unlock()

	snap := buildSnapshot(state, estate, clock(), schemas)
	warning := m.writeSnapshotCarriers(path, branchDir, estate, snap)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotWarning = warning
	return nil
}

// writeSnapshotCarriers routes one built snapshot to the enabled carrier or
// carriers and returns the warning diagnostics the attempt earned, nil for a
// clean write. The rules, which are the config surface's fallback contract:
//
//   - File only: exactly the pre-branch behavior, a failed write warns.
//   - Branch only: the branch is written; when it cannot be, the warning says
//     why and nothing else is written, because no operator named a file.
//   - Both: the branch is the carrier and the file is the fallback. A module
//     directory with no enclosing repository (or no git on PATH) falls back
//     to the file silently - that is the fallback working as designed, not a
//     failure. A branch that exists but could not be written falls back too,
//     and still warns, because the operator asked for the branch and should
//     hear that it is not accumulating history.
//
// Every failure stays a warning and never an error, the same rule 5 the file
// carrier has always had: a cache that can fail an apply is a record.
func (m *Manager) writeSnapshotCarriers(path, branchDir, estate string, snap *snapshot) tfdiags.Diagnostics {
	if branchDir == "" {
		if err := writeSnapshot(path, snap); err != nil {
			return snapshotWriteWarning(fmt.Sprintf(
				"Writing the observational snapshot to %q failed: %s.", path, err,
			), path, err)
		}
		return nil
	}

	branchErr := writeSnapshotBranch(branchDir, estate, snap)
	if branchErr == nil {
		return nil
	}

	if path == "" {
		return snapshotWriteWarning(fmt.Sprintf(
			"Writing the observational snapshot to the tofu-snapshots/%s branch failed: %s. No snapshot_path is configured to fall back to, so nothing was written.",
			estate, branchErr,
		), "the snapshot branch", branchErr)
	}

	fileErr := writeSnapshot(path, snap)
	switch {
	case fileErr != nil:
		return snapshotWriteWarning(fmt.Sprintf(
			"Writing the observational snapshot to the tofu-snapshots/%s branch failed: %s. Writing the snapshot_path fallback at %q failed too: %s.",
			estate, branchErr, path, fileErr,
		), path, fileErr)
	case errors.Is(branchErr, errSnapshotBranchUnavailable):
		// The designed fallback: no repository (or no git) here, and the
		// operator named the file for exactly this case.
		return nil
	default:
		return snapshotWriteWarning(fmt.Sprintf(
			"Writing the observational snapshot to the tofu-snapshots/%s branch failed: %s. The snapshot was written to the snapshot_path fallback at %q instead, so this run is recorded, but the branch is not accumulating history.",
			estate, branchErr, path,
		), path, branchErr)
	}
}

// snapshotWriteWarning wraps one carrier-routing outcome in the sourceless
// warning shape every snapshot failure has always used, and logs it. detail
// already tells the whole story; the closing sentence is the contract
// reminder that the run itself is unaffected.
func snapshotWriteWarning(detail, target string, err error) tfdiags.Diagnostics {
	log.Printf("[WARN] stateless snapshot: writing %s: %s", target, err)
	return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"Could not write the observational snapshot",
		detail+" This snapshot is a cache for offline diff and audit; it is never read by any operation, so the run is unaffected.",
	))
}

// Persists is the number of times PersistState has been called. Every call
// wrote no authoritative state; see the type documentation for the one
// optional exception (the snapshot cache) and why it does not count as
// state either.
func (m *Manager) Persists() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persists
}

// EnableSnapshot turns on the optional observational snapshot: from the
// next PersistState call onward, a [snapshot] of the manager's current
// state is written to path. clock supplies the "writtenAt" timestamp; a nil
// clock defaults to time.Now, and tests inject a fixed one for
// reproducible output.
//
// Not calling this method at all is the manager's half of "no attribute in
// the live block -> no file, ever": there is no default path a Manager
// falls back to, and nothing else in this package can turn the snapshot on.
func (m *Manager) EnableSnapshot(path, estate string, clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotPath = path
	m.snapshotEstate = estate
	m.clock = clock
}

// EnableSnapshotBranch turns on the observational snapshot's branch carrier:
// from the next PersistState call onward, a [snapshot] is committed to the
// refs/heads/tofu-snapshots/<estate> branch of the git repository enclosing
// moduleDir (see writeSnapshotBranch for the mechanics and what is never
// touched). It may be combined with EnableSnapshot, in which case the path
// becomes the fallback carrier; see PersistState for the routing rules.
//
// Not calling this method is the branch's half of "no attribute in the live
// block -> nothing written, ever", exactly as EnableSnapshot is the file's.
func (m *Manager) EnableSnapshotBranch(moduleDir, estate string, clock func() time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotBranchDir = moduleDir
	m.snapshotEstate = estate
	m.clock = clock
}

// SetSnapshotEstate updates the estate name the snapshot records. It is
// separate from EnableSnapshot because the estate name a stateless run
// settles on - possibly derived from the configuration's own tofu-estate
// tags rather than the live block's estate argument - is not known
// until later in the run's pipeline than where the manager is constructed.
// Calling it before EnableSnapshot or EnableSnapshotBranch, or when the
// snapshot is disabled, is harmless: the value is only ever read from inside
// PersistState, and only once a carrier is enabled.
func (m *Manager) SetSnapshotEstate(estate string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshotEstate = estate
}

// SnapshotWarning returns the diagnostic from the most recent failed
// snapshot write, or nil diagnostics if the last attempt succeeded or the
// snapshot was never enabled. It is how a write failure becomes observable
// without PersistState ever returning an error for it - rule 5 of P4.2: a
// cache that can fail an apply is a record, not a cache.
func (m *Manager) SnapshotWarning() tfdiags.Diagnostics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotWarning
}

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
