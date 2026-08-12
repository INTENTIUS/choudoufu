// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/states"
	"github.com/opentofu/opentofu/internal/states/statemgr"
)

// TestManager_writesNothing is the no-persistence claim at its smallest: a
// manager put through the exact sequence an apply puts it through - refresh,
// lock, write, persist, unlock - in a directory that is empty before and must
// be empty after.
func TestManager_writesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	m := NewManager()

	if err := m.RefreshState(context.Background()); err != nil {
		t.Fatalf("RefreshState: %s", err)
	}
	id, err := m.Lock(context.Background(), statemgr.NewLockInfo())
	if err != nil {
		t.Fatalf("Lock: %s", err)
	}
	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	if err := m.PersistState(context.Background(), nil); err != nil {
		t.Fatalf("PersistState: %s", err)
	}
	if err := m.Unlock(context.Background(), id); err != nil {
		t.Fatalf("Unlock: %s", err)
	}

	if got := m.Persists(); got != 1 {
		t.Errorf("Persists() is %d, want 1", got)
	}

	var found []string
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != dir {
			rel, _ := filepath.Rel(dir, path)
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Errorf("the state manager created files: %v", found)
	}
	if _, err := os.Stat(filepath.Join(dir, "terraform.tfstate")); !os.IsNotExist(err) {
		t.Errorf("a state file exists: %v", err)
	}
}

// TestManager_transientRoundTrip: the in-memory half does work, because the
// operation genuinely needs it - apply reports progress by mutating the
// manager's snapshot, and a manager that dropped writes would make an apply
// forget what it had just done inside its own run.
func TestManager_transientRoundTrip(t *testing.T) {
	m := NewManager()

	if err := m.WriteState(testProjectionState()); err != nil {
		t.Fatalf("WriteState: %s", err)
	}
	got := m.State()
	if got == nil || got.Empty() {
		t.Fatal("State() lost what WriteState was given")
	}

	// Each read is a distinct copy, per the statemgr.Reader contract.
	other := m.State()
	other.RootModule().SetOutputValue("greeting", cty.StringVal("mutated"), false, "")
	if again := m.State(); again.RootModule().OutputValues["greeting"].Value.AsString() != "hello" {
		t.Error("mutating a returned state changed the manager's snapshot")
	}

	outputs, err := m.GetRootOutputValues(context.Background())
	if err != nil {
		t.Fatalf("GetRootOutputValues: %s", err)
	}
	if _, ok := outputs["greeting"]; !ok {
		t.Errorf("root outputs are missing the one the state carries: %v", outputs)
	}
}

// TestManager_isNotAuthoritative: the interfaces the manager deliberately
// does not implement are the ones that make a stored state authoritative.
// Snapshot metadata is what a saved plan's staleness check compares, and
// migration is state surgery; a stateless run has no use for either, and
// implementing them would invite a caller to trust this snapshot as a record.
func TestManager_isNotAuthoritative(t *testing.T) {
	var m any = NewManager()

	if _, ok := m.(statemgr.PersistentMeta); ok {
		t.Error("the projection manager reports snapshot metadata")
	}
	if _, ok := m.(statemgr.Migrator); ok {
		t.Error("the projection manager offers state migration")
	}
	// statemgr.Export builds an in-memory statefile.File from any Reader, so
	// it does answer; what matters is that it carries no lineage and no
	// serial, because those are the bookkeeping a later run would compare
	// against to decide whether a record is stale.
	exported := statemgr.Export(NewManager())
	if exported == nil {
		t.Fatal("statemgr.Export returned nothing")
	}
	if exported.Lineage != "" || exported.Serial != 0 {
		t.Errorf("the projection carries snapshot bookkeeping: lineage %q, serial %d", exported.Lineage, exported.Serial)
	}
}

func testProjectionState() *states.State {
	state := states.NewState()
	state.RootModule().SetOutputValue("greeting", cty.StringVal("hello"), false, "")
	// A resource instance, so that the snapshot is not merely an output map.
	state.RootModule().SetResourceInstanceCurrent(
		addrs.Resource{
			Mode: addrs.ManagedResourceMode,
			Type: "aws_s3_bucket",
			Name: "data",
		}.Instance(addrs.NoKey),
		&states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(`{"id":"b","bucket":"b"}`),
			Status:    states.ObjectReady,
		},
		addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: addrs.NewDefaultProvider("aws"),
		},
		addrs.NoKey,
	)
	return state
}
