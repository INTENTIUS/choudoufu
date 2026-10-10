// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1944: an instance's record is written as soon as its apply
// step returns ([WriteInstance]), and [WriteBack] still runs at the end. The
// tests below hold the version chain between the two writes, on the local
// store (whose version is a content hash, so a version can come back) and on
// the Kubernetes store (a Secret's resourceVersion, which never does), and
// the create_before_destroy tombstone the mid-apply write would otherwise
// lose.

// stores1944 is every version scheme the chain is checked on: the local
// store's content hash, which can repeat, and a counter that never does,
// which is what a Secret's resourceVersion and an S3 ETag-plus-version are
// to this code. client-go's fake clientset assigns no resourceVersion at all
// (staterecord's kubernetes_live_test.go says why that makes it useless for
// version assertions), so the counter is [monotonicStore] below.
func stores1944(t *testing.T) map[string]func(t *testing.T) staterecord.Store {
	return map[string]func(t *testing.T) staterecord.Store{
		"local": func(t *testing.T) staterecord.Store {
			s, err := staterecord.NewLocalStore(t.TempDir())
			if err != nil {
				t.Fatalf("building the local store: %s", err)
			}
			return s
		},
		"monotonic": func(t *testing.T) staterecord.Store { return &monotonicStore{} },
		// GitHub issue #1949: the real KubernetesStore over a fake
		// clientset that assigns resourceVersions and enforces conflicts
		// (writeinstance_kubernetes_1949_test.go).
		"kubernetes": func(t *testing.T) staterecord.Store { return newApiserverFake(t).store(t) },
	}
}

// monotonicStore is an in-memory [staterecord.Store] whose versions come
// from one counter, so no two writes ever share one - the resourceVersion
// shape.
type monotonicStore struct {
	mu   sync.Mutex
	next int
	objs map[string]monotonicObj
}

type monotonicObj struct {
	payload []byte
	version string
}

func (m *monotonicStore) Get(_ context.Context, key string) ([]byte, string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objs[key]
	return o.payload, o.version, ok, nil
}

func (m *monotonicStore) PutIfVersion(_ context.Context, key string, payload []byte, expected string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.objs[key].version
	if cur != expected {
		return "", &staterecord.VersionConflictError{Key: key, ExpectedVersion: expected, ActualVersion: cur}
	}
	m.next++
	v := fmt.Sprintf("rv-%d", m.next)
	if m.objs == nil {
		m.objs = map[string]monotonicObj{}
	}
	m.objs[key] = monotonicObj{payload: append([]byte(nil), payload...), version: v}
	return v, nil
}

func (m *monotonicStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return m.PutIfVersion(ctx, key, payload, "")
}

func (m *monotonicStore) Delete(_ context.Context, key string, expected string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.objs[key]
	if !ok {
		if expected == "" {
			return nil
		}
		return &staterecord.VersionConflictError{Key: key, ExpectedVersion: expected}
	}
	if cur.version != expected {
		return &staterecord.VersionConflictError{Key: key, ExpectedVersion: expected, ActualVersion: cur.version}
	}
	delete(m.objs, key)
	return nil
}

func (m *monotonicStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for k := range m.objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// writeInstance is one instance's mid-apply write in run r: state holds that
// one instance as its apply step left it (absent when the step destroyed
// it).
func (e *estate1938) writeInstance(r run1938, addr addrs.AbsResourceInstance, state *states.State) tfdiags.Diagnostics {
	return WriteInstance(context.Background(), WriteBackRequest{
		Store:         r.rs,
		PriorVersions: r.versions,
		FinalState:    state,
		Schemas:       e.schemas,
		Only:          &addr,
	})
}

// only is final narrowed to addr: the state its own apply step hands the
// hook.
func only(final *states.State, addr addrs.AbsResourceInstance) *states.State {
	out := states.NewState()
	res := final.Resource(addr.ContainingResource())
	if res == nil {
		return out
	}
	if inst := res.Instance(addr.Resource.Key); inst != nil {
		out.EnsureModule(addr.Module).SetResourceInstance(addr.Resource, inst.DeepCopy(), res.ProviderConfig)
	}
	return out
}

func conflictCount(diags tfdiags.Diagnostics) int {
	n := 0
	for _, d := range diags {
		if d.Severity() == tfdiags.Error && d.Description().Summary == "Record store write conflict" {
			n++
		}
	}
	return n
}

// TestWriteBackExpectsTheMidApplyVersion is the chain's headline: the
// mid-apply write moves left's record from the plan's version to a new one,
// and the final pass - which still passes the plan's version - expects the
// new one, finds the bytes it would write already there, and sends nothing.
// Before the chain the final pass conditioned its write on the plan's
// version and lost to this run's own mid-apply write.
func TestWriteBackExpectsTheMidApplyVersion(t *testing.T) {
	for name, mk := range stores1944(t) {
		t.Run(name, func(t *testing.T) {
			counting := staterecord.NewCountingStore(mk(t), nil)
			e := newEstate1938(t, counting)
			e.seed("left-0", "right-0")
			r := e.plan()
			counting.Reset()

			final := e.finalState("left-1", "right-0")
			assertNoErrors(t, e.writeInstance(r, e.left, only(final, e.left)))
			_, mid := e.recorded(e.left)
			assertNoErrors(t, e.writeBack(r, final))

			puts := 0
			for _, trip := range counting.Trips() {
				if trip.Key == RecordKey(estate1938Prefix, e.left) && trip.Method == "PutIfVersion" {
					puts++
				}
			}
			if puts != 1 {
				t.Errorf("left's record was written %d times across the mid-apply write and the final pass, want once", puts)
			}
			if id, v := e.recorded(e.left); id != "left-1" || v != mid {
				t.Errorf("left's record is %s at %s, want left-1 at the mid-apply write's version %s", id, v, mid)
			}
		})
	}
}

// TestWriteBackAfterMidApplyStillConflictsWithAnotherRun: the chain only
// follows versions this run produced. When another run writes the record
// after this run's mid-apply write, the final pass's change still meets a
// named conflict and overwrites nothing.
func TestWriteBackAfterMidApplyStillConflictsWithAnotherRun(t *testing.T) {
	for name, mk := range stores1944(t) {
		t.Run(name, func(t *testing.T) {
			e := newEstate1938(t, mk(t))
			e.seed("left-0", "right-0")
			a := e.plan()
			assertNoErrors(t, e.writeInstance(a, e.left, only(e.finalState("left-a1", "right-0"), e.left)))

			b := e.plan()
			assertNoErrors(t, e.writeBack(b, e.finalState("left-b", "right-0")))

			// a's final pass has something new to say about left, so it
			// must write, and must lose.
			diags := e.writeBack(a, e.finalState("left-a2", "right-0"))
			if got := conflictCount(diags); got != 1 {
				t.Fatalf("a's final pass reported %d write conflicts, want 1:\n%s", got, renderDiags(diags))
			}
			if id, _ := e.recorded(e.left); id != "left-b" {
				t.Errorf("left's record is %s, want left-b: the losing run overwrote it", id)
			}
		})
	}
}

// TestMidApplyConflictIsReportedOnce: a mid-apply write that loses to
// another run fails there, naming the conflict, and the final pass of the
// same run does not report it a second time.
func TestMidApplyConflictIsReportedOnce(t *testing.T) {
	for name, mk := range stores1944(t) {
		t.Run(name, func(t *testing.T) {
			e := newEstate1938(t, mk(t))
			e.seed("left-0", "right-0")
			a := e.plan()
			b := e.plan()
			assertNoErrors(t, e.writeBack(b, e.finalState("left-b", "right-0")))

			final := e.finalState("left-a", "right-0")
			mid := e.writeInstance(a, e.left, only(final, e.left))
			if got := conflictCount(mid); got != 1 {
				t.Fatalf("the mid-apply write reported %d conflicts, want 1:\n%s", got, renderDiags(mid))
			}
			end := e.writeBack(a, final)
			if got := conflictCount(end); got != 0 {
				t.Errorf("the final pass reported the same conflict again (%d times):\n%s", got, renderDiags(end))
			}
			if id, _ := e.recorded(e.left); id != "left-b" {
				t.Errorf("left's record is %s, want left-b: the losing run overwrote it", id)
			}
		})
	}
}

// TestMidApplyChainSurvivesARepeatedVersion: the local store's version is a
// content hash, so a record written back to bytes it held earlier comes back
// at the earlier version. The chain is a list, so the plan's version still
// resolves to the latest this run left rather than to the first it moved to.
func TestMidApplyChainSurvivesARepeatedVersion(t *testing.T) {
	local, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	e := newEstate1938(t, local)
	e.seed("left-0", "right-0")
	_, v0 := e.recorded(e.left)
	r := e.plan()

	assertNoErrors(t, e.writeInstance(r, e.left, only(e.finalState("left-1", "right-0"), e.left)))
	assertNoErrors(t, e.writeInstance(r, e.left, only(e.finalState("left-0", "right-0"), e.left)))
	if _, v := e.recorded(e.left); v != v0 {
		t.Fatalf("the local store gave left-0's bytes version %s the second time and %s the first; this test needs the repeat", v, v0)
	}
	assertNoErrors(t, e.writeInstance(r, e.left, only(e.finalState("left-2", "right-0"), e.left)))
	assertNoErrors(t, e.writeBack(r, e.finalState("left-2", "right-0")))
	if id, _ := e.recorded(e.left); id != "left-2" {
		t.Errorf("left's record is %s, want left-2", id)
	}
}

// TestMidApplyDeleteThenCreate is a DeleteThenCreate replace written leg by
// leg: the destroy leg deletes the record, the create leg writes a new one
// expecting none, and the final pass expects what the create leg left.
func TestMidApplyDeleteThenCreate(t *testing.T) {
	for name, mk := range stores1944(t) {
		t.Run(name, func(t *testing.T) {
			e := newEstate1938(t, mk(t))
			e.seed("left-0", "right-0")
			r := e.plan()

			assertNoErrors(t, e.writeInstance(r, e.left, states.NewState()))
			if _, _, exists, err := e.store.Get(context.Background(), RecordKey(estate1938Prefix, e.left)); err != nil || exists {
				t.Fatalf("after the destroy leg left's record exists=%v err=%v, want it gone", exists, err)
			}
			final := e.finalState("left-1", "right-0")
			assertNoErrors(t, e.writeInstance(r, e.left, only(final, e.left)))
			assertNoErrors(t, e.writeBack(r, final))
			if id, _ := e.recorded(e.left); id != "left-1" {
				t.Errorf("left's record is %s, want left-1", id)
			}
		})
	}
}

// locatedFinal is the located test type's state at addr: currentID as the
// current object and deposed (key to id) as its deposed objects.
func locatedFinal(t *testing.T, addr addrs.AbsResourceInstance, currentID string, deposed map[string]string) *states.State {
	t.Helper()
	encode := func(id string) *states.ResourceInstanceObjectSrc {
		t.Helper()
		obj := cty.ObjectVal(map[string]cty.Value{
			"id":            cty.StringVal(id),
			"allocation_id": cty.StringVal("eipalloc-declared"),
			"instance_id":   cty.StringVal("i-declared"),
		})
		src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: obj}).
			Encode(locatedTypeSchema().Block.ImpliedType(), 0, 0)
		if err != nil {
			t.Fatalf("encoding %s: %s", id, err)
		}
		return src
	}
	final := states.NewState()
	ms := final.EnsureModule(addrs.RootModuleInstance)
	ms.SetResourceInstanceCurrent(addr.Resource, encode(currentID), locatedTestProvider, addrs.NoKey)
	for dk, id := range deposed {
		ms.SetResourceInstanceDeposed(addr.Resource, states.DeposedKey(dk), encode(id), locatedTestProvider, addrs.NoKey)
	}
	return final
}

func locatedSchemas() *tofu.Schemas {
	return &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{locatedTestType: locatedTypeSchema()}},
	}}
}

// cbdReplace1944 runs a create_before_destroy replace of a record-carried
// instance the way an apply does since #1944: the create leg's mid-apply
// write (new current, old deposed), then the destroy leg's (deposed gone,
// or still there when destroyLegFailed), then the final pass.
func cbdReplace1944(t *testing.T, destroyLegFailed bool) (*RecordStore, addrs.AbsResourceInstance) {
	t.Helper()
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	const deposedKey = "deadbeef"
	located := newTestLocatedStore(localHintStore(t), "test-estate")
	version := supersedeApplyReplacing(t, located.rs, addr, "eipassoc-old", "", nil)

	req := func(state *states.State) WriteBackRequest {
		return WriteBackRequest{
			Store:            located.rs,
			EnvelopeVersions: []RecordVersion{{Addr: addr, Version: version}},
			FinalState:       state,
			Schemas:          locatedSchemas(),
			ReplacedAddrs:    []addrs.AbsResourceInstance{addr},
			Only:             &addr,
		}
	}
	createLeg := locatedFinal(t, addr, "eipassoc-new", map[string]string{deposedKey: "eipassoc-old"})
	assertNoErrors(t, WriteInstance(ctx, req(createLeg)))
	if got := tombstonedIDs(t, located.rs, addr); len(got) != 0 {
		t.Fatalf("the create leg recorded %v as destroyed while the old object is deposed and alive (#901)", got)
	}

	destroyLeg := locatedFinal(t, addr, "eipassoc-new", nil)
	if destroyLegFailed {
		destroyLeg = createLeg
	}
	assertNoErrors(t, WriteInstance(ctx, req(destroyLeg)))
	final := req(destroyLeg)
	final.Only = nil
	assertNoErrors(t, WriteBack(ctx, final))
	return located.rs, addr
}

// TestMidApplyCBDReplaceTombstonesTheOldIdentity: by the time the destroy
// leg returns, the create leg's write has already pointed the record at the
// new object, so the identity no longer looks superseded. The old object's
// identity has to be tombstoned from its deposed entry instead
// ([destroyedDeposedFor]), or its lingering tag reads as a second claimant
// (#670).
func TestMidApplyCBDReplaceTombstonesTheOldIdentity(t *testing.T) {
	rs, addr := cbdReplace1944(t, false)
	if got := tombstonedIDs(t, rs, addr); len(got) != 1 || got[0] != "eipassoc-old" {
		t.Fatalf("after the replace the record names %v as destroyed, want exactly [eipassoc-old]", got)
	}
	if got := deposedIDs(t, rs, addr); len(got) != 0 {
		t.Errorf("the record still holds deposed objects %v after their destroy", got)
	}
	rec, _, _, _, err := rs.GetIdentity(context.Background(), addr)
	if err != nil || rec.ImportID != "eipassoc-new" {
		t.Errorf("the current identity is %q (err %v), want eipassoc-new", rec.ImportID, err)
	}
}

// TestMidApplyCBDReplaceWithAFailedDestroyLegRecordsNothing is #901 under
// mid-apply writes: the destroy leg failed, the old object is deposed and
// alive, and nothing may say it was destroyed.
func TestMidApplyCBDReplaceWithAFailedDestroyLegRecordsNothing(t *testing.T) {
	rs, addr := cbdReplace1944(t, true)
	if got := tombstonedIDs(t, rs, addr); len(got) != 0 {
		t.Fatalf("a replace whose destroy leg failed recorded %v as destroyed; the object is alive (#901)", got)
	}
	if got := deposedIDs(t, rs, addr); len(got) != 1 || got["deadbeef"] != "eipassoc-old" {
		t.Errorf("the record holds deposed objects %v, want {deadbeef: eipassoc-old}", got)
	}
}
