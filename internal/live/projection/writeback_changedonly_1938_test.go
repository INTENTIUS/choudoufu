// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1938: an apply writes only the records whose content
// changed. Before the fix every apply rewrote every record-backed
// instance's record under If-Match, so two applies on one estate that
// changed different resources collided on a record neither changed - the
// shape terragucci's resource-level concurrency claim (#418) measured with
// terraform_data.left and terraform_data.right.

const estate1938Prefix = "tofu-records/estate-1938"

// estate1938 is one estate with two record-backed instances, left and
// right, over one shared local store.
type estate1938 struct {
	t       *testing.T
	store   staterecord.Store
	dir     string
	left    addrs.AbsResourceInstance
	right   addrs.AbsResourceInstance
	schemas *tofu.Schemas
}

func newEstate1938(t *testing.T, store staterecord.Store) *estate1938 {
	t.Helper()
	dir := t.TempDir()
	const src = `
resource "null_resource" "left" {}
resource "null_resource" "right" {}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return &estate1938{
		t:     t,
		store: store,
		dir:   dir,
		left:  mustAddr(t, `null_resource.left`),
		right: mustAddr(t, `null_resource.right`),
		schemas: &tofu.Schemas{
			Providers: map[addrs.Provider]providers.ProviderSchema{
				nullProvider.Provider: {
					Provider:      providers.Schema{Block: &configschema.Block{}},
					ResourceTypes: map[string]providers.Schema{"null_resource": nullResourceSchema()},
				},
			},
		},
	}
}

func nullVal1938(id string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	})
}

// finalState is the state an apply finished with: left and right holding
// the given ids.
func (e *estate1938) finalState(leftID, rightID string) *states.State {
	e.t.Helper()
	schema := nullResourceSchema()
	st := states.NewState()
	for addr, id := range map[string]string{e.left.String(): leftID, e.right.String(): rightID} {
		a := mustAddr(e.t, addr)
		obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: nullVal1938(id)}
		src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
		if err != nil {
			e.t.Fatalf("encoding %s: %s", a, err)
		}
		st.EnsureModule(a.Module).SetResourceInstanceCurrent(a.Resource, src, nullProvider, addrs.NoKey)
	}
	return st
}

// run1938 is one run's plan half: a projection built against the store,
// through its own RecordStore, the way a separate process would.
type run1938 struct {
	rs       *RecordStore
	versions []RecordVersion
}

func (e *estate1938) plan() run1938 {
	e.t.Helper()
	rs := NewRecordEnvelopeStore(e.store, estate1938Prefix)
	res, diags := BuildWith(context.Background(), loadConfig(e.t, e.dir), []identity.Resolution{
		{Addr: e.left, Class: identity.ClassRecordBacked},
		{Addr: e.right, Class: identity.ClassRecordBacked},
	}, SingleProvider(nullProvider, nullResourceProvider()), Options{RecordStore: rs})
	assertNoErrors(e.t, diags)
	if len(res.RecordVersions) != 2 {
		e.t.Fatalf("the plan read %d record versions, want 2: %v", len(res.RecordVersions), res.RecordVersions)
	}
	return run1938{rs: rs, versions: res.RecordVersions}
}

func (e *estate1938) writeBack(r run1938, final *states.State) tfdiags.Diagnostics {
	return WriteBack(context.Background(), WriteBackRequest{
		Store:         r.rs,
		PriorVersions: r.versions,
		FinalState:    final,
		Schemas:       e.schemas,
	})
}

// seed writes both records the way an earlier apply would have, so their
// bytes are exactly what a write-back produces.
func (e *estate1938) seed(leftID, rightID string) {
	e.t.Helper()
	diags := WriteBack(context.Background(), WriteBackRequest{
		Store:      NewRecordEnvelopeStore(e.store, estate1938Prefix),
		FinalState: e.finalState(leftID, rightID),
		Schemas:    e.schemas,
	})
	assertNoErrors(e.t, diags)
}

func (e *estate1938) recorded(addr addrs.AbsResourceInstance) (id, version string) {
	e.t.Helper()
	payload, version, exists, err := e.store.Get(context.Background(), RecordKey(estate1938Prefix, addr))
	if err != nil || !exists {
		e.t.Fatalf("reading %s's record: exists=%v err=%v", addr, exists, err)
	}
	val, _, _, err := decodeRecordPayload(payload)
	if err != nil {
		e.t.Fatalf("decoding %s's record: %s", addr, err)
	}
	return val.GetAttr("id").AsString(), version
}

// TestWriteBackLeavesUnchangedRecordUntouched: an apply that changes left
// and leaves right as it was writes left's record and sends nothing at all
// for right, whose version stays what it was.
func TestWriteBackLeavesUnchangedRecordUntouched(t *testing.T) {
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	counting := staterecord.NewCountingStore(local, nil)
	e := newEstate1938(t, counting)
	e.seed("left-0", "right-0")
	_, rightBefore := e.recorded(e.right)
	_, leftBefore := e.recorded(e.left)

	r := e.plan()
	counting.Reset()
	assertNoErrors(t, e.writeBack(r, e.finalState("left-1", "right-0")))

	rightKey := RecordKey(estate1938Prefix, e.right)
	leftKey := RecordKey(estate1938Prefix, e.left)
	leftWrites := 0
	for _, trip := range counting.Trips() {
		if trip.Key == rightKey {
			t.Errorf("the write-back touched right's record, which this apply did not change: %s", trip)
		}
		if trip.Key == leftKey && trip.Method == "PutIfVersion" {
			leftWrites++
		}
	}
	if leftWrites != 1 {
		t.Errorf("left's record was written %d times, want once", leftWrites)
	}
	if id, v := e.recorded(e.right); id != "right-0" || v != rightBefore {
		t.Errorf("right's record is %s at %s, want right-0 at %s (untouched)", id, v, rightBefore)
	}
	if id, v := e.recorded(e.left); id != "left-1" || v == leftBefore {
		t.Errorf("left's record is %s at %s, want left-1 at a new version (was %s)", id, v, leftBefore)
	}
}

// TestWriteBackDisjointAppliesBothSucceed is the issue's done-when: two
// applies on one estate, both planned before either writes, one changing
// left and the other right. Both write-backs succeed, in either order and
// at once, and the store ends with each run's change.
func TestWriteBackDisjointAppliesBothSucceed(t *testing.T) {
	for _, order := range []string{"a-then-b", "b-then-a", "concurrent"} {
		t.Run(order, func(t *testing.T) {
			local, err := staterecord.NewLocalStore(t.TempDir())
			if err != nil {
				t.Fatalf("building the local store: %s", err)
			}
			e := newEstate1938(t, local)
			e.seed("left-0", "right-0")

			a := e.plan() // changes left only
			b := e.plan() // changes right only
			finalA := e.finalState("left-a", "right-0")
			finalB := e.finalState("left-0", "right-b")

			var diagsA, diagsB tfdiags.Diagnostics
			switch order {
			case "a-then-b":
				diagsA = e.writeBack(a, finalA)
				diagsB = e.writeBack(b, finalB)
			case "b-then-a":
				diagsB = e.writeBack(b, finalB)
				diagsA = e.writeBack(a, finalA)
			case "concurrent":
				var wg sync.WaitGroup
				start := make(chan struct{})
				wg.Add(2)
				go func() { defer wg.Done(); <-start; diagsA = e.writeBack(a, finalA) }()
				go func() { defer wg.Done(); <-start; diagsB = e.writeBack(b, finalB) }()
				close(start)
				wg.Wait()
			}
			if diagsA.HasErrors() {
				t.Errorf("apply a (left) failed:\n%s", renderDiags(diagsA))
			}
			if diagsB.HasErrors() {
				t.Errorf("apply b (right) failed:\n%s", renderDiags(diagsB))
			}
			if id, _ := e.recorded(e.left); id != "left-a" {
				t.Errorf("left's record is %s, want left-a (apply a's change)", id)
			}
			if id, _ := e.recorded(e.right); id != "right-b" {
				t.Errorf("right's record is %s, want right-b (apply b's change)", id)
			}
		})
	}
}

// TestWriteBackSameRecordStillConflicts keeps the safety property: two
// applies that BOTH change left still get exactly one winner and one named
// conflict, and the loser overwrites nothing.
func TestWriteBackSameRecordStillConflicts(t *testing.T) {
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	e := newEstate1938(t, local)
	e.seed("left-0", "right-0")

	a := e.plan()
	b := e.plan()
	assertNoErrors(t, e.writeBack(a, e.finalState("left-a", "right-0")))
	diags := e.writeBack(b, e.finalState("left-b", "right-0"))
	if !hasDiagSummary(diags, "Record store write conflict") {
		t.Fatalf("want a write conflict for the second apply that changed left, got:\n%s", renderDiags(diags))
	}
	if id, _ := e.recorded(e.left); id != "left-a" {
		t.Errorf("left's record is %s, want left-a: the losing apply overwrote it", id)
	}
}

// TestWriteBackDeleteStillConditional: an apply that destroyed right still
// deletes right's record conditionally, and fails rather than deleting a
// record another run changed after this one planned.
func TestWriteBackDeleteStillConditional(t *testing.T) {
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	e := newEstate1938(t, local)
	e.seed("left-0", "right-0")

	a := e.plan() // changes right
	b := e.plan() // destroys right
	assertNoErrors(t, e.writeBack(a, e.finalState("left-0", "right-a")))

	onlyLeft := e.finalState("left-0", "right-0")
	onlyLeft.Module(addrs.RootModuleInstance).RemoveResource(e.right.Resource.Resource)
	diags := e.writeBack(b, onlyLeft)
	if !diags.HasErrors() {
		t.Fatal("a delete of a record another apply changed since the plan succeeded; want a conflict")
	}
	if id, _ := e.recorded(e.right); id != "right-a" {
		t.Errorf("right's record is %s, want right-a: the conflicting delete removed or overwrote it", id)
	}
}
