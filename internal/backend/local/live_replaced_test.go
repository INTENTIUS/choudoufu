// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestReplacedInstances is GitHub issue #854's whole seam: which addresses
// [StatelessRun.WriteBack] is told this run replaced, and therefore which
// addresses the record store is allowed to record a destroyed identity for.
//
// The write side used to derive that from the record alone - "the identity
// changed at an address the final state still has" - which is equally true
// of three shapes that destroy nothing. Two of them are configuration-side
// (an `import` block, a `live-mv` onto an occupied address) and never reach
// this function's input at all; the third does, and it is the row this test
// exists for: ForgetThenCreate is `lifecycle.destroy = false`, whose whole
// purpose is to create a replacement and LEAVE THE OLD OBJECT RUNNING. A
// filter written as "anything that creates a new object here" would let it
// through and record a live resource as destroyed.
//
// Asserted on the rendered addresses, in order, rather than on a count.
func TestReplacedInstances(t *testing.T) {
	addr := func(s string) addrs.AbsResourceInstance {
		t.Helper()
		a, diags := addrs.ParseAbsResourceInstanceStr(s)
		if diags.HasErrors() {
			t.Fatalf("parsing %q: %s", s, diags.Err())
		}
		return a
	}

	changes := &plans.Changes{Resources: []*plans.ResourceInstanceChangeSrc{
		{Addr: addr("aws_vpc.created"), ChangeSrc: plans.ChangeSrc{Action: plans.Create}},
		{Addr: addr("aws_vpc.updated"), ChangeSrc: plans.ChangeSrc{Action: plans.Update}},
		{Addr: addr("aws_vpc.noop"), ChangeSrc: plans.ChangeSrc{Action: plans.NoOp}},
		{Addr: addr("aws_vpc.deleted"), ChangeSrc: plans.ChangeSrc{Action: plans.Delete}},
		{Addr: addr("aws_vpc.forgotten"), ChangeSrc: plans.ChangeSrc{Action: plans.Forget}},
		{Addr: addr("aws_vpc.forget_then_create"), ChangeSrc: plans.ChangeSrc{Action: plans.ForgetThenCreate}},
		{Addr: addr("aws_vpc.delete_then_create"), ChangeSrc: plans.ChangeSrc{Action: plans.DeleteThenCreate}},
		{Addr: addr("aws_vpc.create_then_delete"), ChangeSrc: plans.ChangeSrc{Action: plans.CreateThenDelete}},
		{
			Addr:       addr("aws_vpc.deposed_leftover"),
			DeposedKey: states.DeposedKey("abcd1234"),
			ChangeSrc:  plans.ChangeSrc{Action: plans.Delete},
		},
	}}

	var got []string
	for _, a := range replacedInstances(&plans.Plan{Changes: changes}) {
		got = append(got, a.String())
	}
	want := []string{"aws_vpc.delete_then_create", "aws_vpc.create_then_delete"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("the plan's replace set is %v, want %v. Anything extra here becomes a live object recorded as destroyed; anything missing becomes a refusal the next plan cannot clear.", got, want)
	}

	// A run with no plan at all - and a plan carrying no changes - must say
	// "replaced nothing" rather than panicking or guessing, because the
	// write side reads an empty set as "record no destroy", which refuses.
	if got := replacedInstances(nil); got != nil {
		t.Errorf("a nil plan produced a replace set of %v, want none", got)
	}
	if got := replacedInstances(&plans.Plan{}); got != nil {
		t.Errorf("a plan with no changes produced a replace set of %v, want none", got)
	}
}

// replaceRecordingStateless is a [StatelessRun] that does nothing to a live
// system and exists only to record what opApply hands its WriteBack: the
// replace set, at the moment the real call site computes it, after a real
// lr.Core.Apply has run.
type replaceRecordingStateless struct {
	mgr statemgr.Full

	// prior is what PriorState hands back as the projection - the "live"
	// system this fake run reads.
	prior *states.State

	// writeBackCalled and gotReplaced are written from WriteBack, which
	// opApply calls on the operation goroutine, and read from the test
	// goroutine after <-run.Done(). Guarded because -race says so.
	mu              sync.Mutex
	writeBackCalled bool
	gotReplaced     []addrs.AbsResourceInstance
	finalState      *states.State
}

func (s *replaceRecordingStateless) StateMgr() statemgr.Full { return s.mgr }

func (s *replaceRecordingStateless) PriorState(_ context.Context, _ *configs.Config, _ *tofu.Context) (*states.State, tfdiags.Diagnostics) {
	return s.prior.DeepCopy(), nil
}

func (s *replaceRecordingStateless) RootOutputData() map[string]cty.Value      { return nil }
func (s *replaceRecordingStateless) RecordedRootOutputs() map[string]cty.Value { return nil }

func (s *replaceRecordingStateless) WriteBack(_ context.Context, finalState *states.State, _ *tofu.Schemas, replaced []addrs.AbsResourceInstance) tfdiags.Diagnostics {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeBackCalled = true
	s.gotReplaced = replaced
	s.finalState = finalState
	return nil
}

func (s *replaceRecordingStateless) AfterApply(_ context.Context) tfdiags.Diagnostics { return nil }

// TestWriteBackSeesTheReplaceSetAfterApply is GitHub issue #908's guard, and
// it is deliberately not a unit test of replacedInstances.
//
// #854 plumbed the plan's replace set into [StatelessRun.WriteBack] and
// TestReplacedInstances above proved the function correct - against a
// synthetic plan, which nothing ever drains. The live call site read
// `replacedInstances(plan)` in the WriteBack argument list, AFTER
// lr.Core.Apply had already removed every applied change from plan.Changes
// (NodeAbstractResourceInstance.writeChange with a nil change calls
// Changes.RemoveResourceInstanceChange). So on every real run the set was
// empty, no replace recorded a tombstone, #670's mechanism was inert and
// one ForceNew replace blocked the next plan on a collision with its own
// corpse. Measured on the live path before the fix, with a log line either
// side of the Apply call:
//
//	DBG908 BEFORE-APPLY replacedInstances=[aws_instance.web] (of 4 changes)
//	DBG908 AFTER-APPLY  replacedInstances=[] (of 3 changes)
//
// This test runs a real apply - real plan, real graph walk, real
// Changes.RemoveResourceInstanceChange - of a configuration whose one
// instance the provider requires a replace for, and asserts on the rendered
// addresses WriteBack was actually handed. Written against the seam and not
// against the implementation: it does not care where in opApply the set is
// computed, only that what arrives names the replaced instance.
//
// It fails if anyone moves the computation back below the apply, and it
// fails for the same reason if a future change drains plan.Changes earlier.
func TestWriteBackSeesTheReplaceSetAfterApply(t *testing.T) {
	b := TestLocal(t)

	p := TestLocalProvider(t, b, "test", applyFixtureSchema())
	// The fixture's declared ami is "bar" and the prior object below holds
	// "old", so the ordinary diff is an update. Requiring a replace on that
	// one attribute is what turns it into a DeleteThenCreate at the same
	// declared address, which is the shape a ForceNew argument produces in
	// production.
	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		if req.ProposedNewState.IsNull() {
			// The destroy half of the replace.
			return providers.PlanResourceChangeResponse{
				PlannedState:   req.ProposedNewState,
				PlannedPrivate: req.PriorPrivate,
			}
		}
		return providers.PlanResourceChangeResponse{
			PlannedState: cty.ObjectVal(map[string]cty.Value{
				"id":  cty.UnknownVal(cty.String),
				"ami": req.ProposedNewState.GetAttr("ami"),
			}),
			RequiresReplace: []cty.Path{cty.GetAttrPath("ami")},
		}
	}
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		if req.PlannedState.IsNull() {
			return providers.ApplyResourceChangeResponse{NewState: req.PlannedState}
		}
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.StringVal("new"),
			"ami": req.PlannedState.GetAttr("ami"),
		})}
	}

	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("test_instance.foo")
	if addrDiags.HasErrors() {
		t.Fatalf("parsing the fixture's address: %s", addrDiags.Err())
	}

	prior := states.BuildState(func(ss *states.SyncState) {
		ss.SetResourceInstanceCurrent(
			addr,
			&states.ResourceInstanceObjectSrc{
				Status:    states.ObjectReady,
				AttrsJSON: []byte(`{"id":"old","ami":"old"}`),
			},
			addrs.AbsProviderConfig{
				Provider: addrs.NewDefaultProvider("test"),
				Module:   addrs.RootModule,
			},
			addrs.NoKey,
		)
	})

	stateless := &replaceRecordingStateless{
		mgr:   statemgr.NewFullFake(statemgr.NewTransientInMemory(nil), prior.DeepCopy()),
		prior: prior,
	}
	b.Stateless = stateless

	op, done := testOperationApply(t, "./testdata/apply")
	op.PlanRefresh = false

	run, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-run.Done()

	output := done(t)
	if run.Result != backend.OperationSuccess {
		t.Fatalf("the apply failed, so this test measured nothing:\nstdout:\n%s\nstderr:\n%s", output.Stdout(), output.Stderr())
	}

	stateless.mu.Lock()
	called, got, final := stateless.writeBackCalled, stateless.gotReplaced, stateless.finalState
	stateless.mu.Unlock()

	if !called {
		t.Fatal("WriteBack was never called, so this test measured nothing about the replace set")
	}

	// The apply really did replace: the object at the address is the new
	// one, not the prior "old". Asserted by value, so a run that quietly
	// planned an update instead of a replace cannot pass the check below by
	// having produced no replace to see.
	if final == nil {
		t.Fatal("WriteBack was handed a nil final state")
	}
	is := final.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatalf("%s is not in the final state at all; this apply did not do what the test needs", addr)
	}
	if id := string(is.Current.AttrsJSON); !strings.Contains(id, `"id":"new"`) {
		t.Fatalf("after the apply %s reads %s, want the replacement object's id - no replace happened, so the assertion below proves nothing", addr, id)
	}

	var gotStr []string
	for _, a := range got {
		gotStr = append(gotStr, a.String())
	}
	want := []string{"test_instance.foo"}
	if fmt.Sprint(gotStr) != fmt.Sprint(want) {
		t.Errorf("WriteBack was handed the replace set %v, want %v.\n"+
			"An empty set here is GitHub issue #908: the set is being read after lr.Core.Apply drained plan.Changes, "+
			"so no replace records a tombstone, the superseded-claimant prune is unreachable, and the next plan "+
			"refuses the estate over the replaced object's own corpse.", gotStr, want)
	}
}
