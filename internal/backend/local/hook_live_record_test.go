// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// instanceWritingLiveRun is [replaceRecordingLiveRun] with a WriteInstance
// that records each call and can refuse one address with a write conflict,
// the way projection.WriteInstance does when another run changed the record.
type instanceWritingLiveRun struct {
	replaceRecordingLiveRun

	refuse string

	wmu     sync.Mutex
	written map[string]int
	objects map[string]bool
}

func (s *instanceWritingLiveRun) WriteInstance(_ context.Context, state *states.State, addr addrs.AbsResourceInstance, _ *tofu.Schemas, _ []addrs.AbsResourceInstance, _ []projection.DeposedDestroy) tfdiags.Diagnostics {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.written == nil {
		s.written = map[string]int{}
		s.objects = map[string]bool{}
	}
	s.written[addr.String()]++
	ri := state.ResourceInstance(addr)
	s.objects[addr.String()] = ri != nil && ri.Current != nil
	if addr.String() == s.refuse {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Record store write conflict",
			"Writing the persisted record for "+addr.String()+" failed: another writer changed it between this run's plan and apply."))
	}
	return nil
}

// chainConfig is two instances, the second depending on the first.
func chainConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "test_instance" "first" {
  ami = "one"
}

resource "test_instance" "second" {
  ami        = "two"
  depends_on = [test_instance.first]
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runChainApply(t *testing.T, refuse string) (*instanceWritingLiveRun, *backend.RunningOperation, []string, string) {
	t.Helper()
	b := TestLocal(t)
	p := TestLocalProvider(t, b, "test", applyFixtureSchema())
	var amu sync.Mutex
	var applied []string
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
		amu.Lock()
		applied = append(applied, req.PlannedState.GetAttr("ami").AsString())
		amu.Unlock()
		return providers.ApplyResourceChangeResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"id":  cty.StringVal("id-" + req.PlannedState.GetAttr("ami").AsString()),
			"ami": req.PlannedState.GetAttr("ami"),
		})}
	}
	run := &instanceWritingLiveRun{refuse: refuse}
	run.mgr = statemgr.NewFullFake(statemgr.NewTransientInMemory(nil), states.NewState())
	run.prior = states.NewState()
	b.LiveRun = run

	op, done := testOperationApply(t, chainConfig(t))
	running, err := b.Operation(context.Background(), op)
	if err != nil {
		t.Fatalf("starting the apply: %s", err)
	}
	<-running.Done()
	out := done(t)
	amu.Lock()
	defer amu.Unlock()
	sort.Strings(applied)
	return run, running, applied, out.Stderr()
}

// TestLiveApplyWritesEachRecordWhenItsInstanceReturns is GitHub issue
// #1944's seam end to end: a real apply hands every instance it applied to
// [LiveRun.WriteInstance] once, with its applied object, before the apply
// is over.
func TestLiveApplyWritesEachRecordWhenItsInstanceReturns(t *testing.T) {
	run, running, applied, stderr := runChainApply(t, "")
	if running.Result != backend.OperationSuccess {
		t.Fatalf("the apply failed:\n%s", stderr)
	}
	if strings.Join(applied, ",") != "one,two" {
		t.Fatalf("the provider applied %v, want both instances", applied)
	}
	run.wmu.Lock()
	defer run.wmu.Unlock()
	for _, addr := range []string{"test_instance.first", "test_instance.second"} {
		if run.written[addr] != 1 {
			t.Errorf("%s was written %d times mid-apply, want once", addr, run.written[addr])
		}
		if !run.objects[addr] {
			t.Errorf("%s was handed to WriteInstance with no applied object", addr)
		}
	}
}

// TestLiveApplyFailsAtTheInstanceWhoseRecordConflicts: a mid-apply write
// that loses to another run fails the apply at that instance, naming the
// conflict, and nothing that depends on it is applied.
func TestLiveApplyFailsAtTheInstanceWhoseRecordConflicts(t *testing.T) {
	run, running, applied, stderr := runChainApply(t, "test_instance.first")
	if running.Result == backend.OperationSuccess {
		t.Fatal("the apply succeeded over a record write conflict")
	}
	if !strings.Contains(stderr, "Record store write conflict") || !strings.Contains(stderr, "test_instance.first") {
		t.Errorf("the failure does not name the conflict and the instance:\n%s", stderr)
	}
	if strings.Join(applied, ",") != "one" {
		t.Errorf("the provider applied %v; test_instance.second depends on the instance whose record conflicted and must not be applied", applied)
	}
	run.wmu.Lock()
	defer run.wmu.Unlock()
	if run.written["test_instance.second"] != 0 {
		t.Errorf("test_instance.second's record was written after the conflict")
	}
}
