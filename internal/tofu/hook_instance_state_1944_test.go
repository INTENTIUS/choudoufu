// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// instanceStateRecorder is an [InstanceStateHook] that records every call,
// by address.
type instanceStateRecorder struct {
	NilHook

	mu    sync.Mutex
	calls map[string][]*states.ResourceInstance
	provs map[string]*addrs.AbsProviderConfig

	// postApplied holds the addresses PostApply has reported and no
	// PostInstanceStateUpdate has consumed yet, and early counts the calls
	// that arrived for an address PostApply had not reported. A live run's
	// record hook (internal/backend/local) writes only for an instance
	// PostApply has marked, so on either runtime the order is PostApply,
	// then the instance's state.
	postApplied map[string]bool
	early       []string
}

func (h *instanceStateRecorder) PostApply(addr addrs.AbsResourceInstance, _ states.Generation, _ cty.Value, _ error) (HookAction, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.postApplied == nil {
		h.postApplied = map[string]bool{}
	}
	h.postApplied[addr.String()] = true
	return HookActionContinue, nil
}

func (h *instanceStateRecorder) PostInstanceStateUpdate(addr addrs.AbsResourceInstance, inst *states.ResourceInstance, provider *addrs.AbsProviderConfig) (HookAction, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.calls == nil {
		h.calls = map[string][]*states.ResourceInstance{}
		h.provs = map[string]*addrs.AbsProviderConfig{}
	}
	h.calls[addr.String()] = append(h.calls[addr.String()], inst)
	h.provs[addr.String()] = provider
	if !h.postApplied[addr.String()] {
		h.early = append(h.early, addr.String())
	}
	delete(h.postApplied, addr.String())
	return HookActionContinue, nil
}

func (h *instanceStateRecorder) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = nil
	h.provs = nil
	h.postApplied = nil
	h.early = nil
}

// TestInstanceStateHookFiresOncePerInstance is GitHub issue #1944's fork
// hook: every instance an apply step writes is handed to
// PostInstanceStateUpdate once, under its own address, with the object that
// step wrote - here across seven instances the graph applies in parallel,
// and then their destroys, where the instance is gone.
func TestInstanceStateHookFiresOncePerInstance(t *testing.T) {
	testInstanceStateHookFiresOncePerInstance(t)
}

// TestInstanceStateHookFiresOncePerInstance_newRuntime is the same test on
// the new runtime (#2256 U5, #2341), where the call comes from
// internal/engine/applying's ManagedApply through ApplyOpts.InstanceStateUpdate
// rather than from updateStateHook. Without that wiring the new runtime
// makes no PostInstanceStateUpdate call at all and this fails on its first
// assertion.
func TestInstanceStateHookFiresOncePerInstance_newRuntime(t *testing.T) {
	forceExperimentalRuntimeForTesting(t)
	testInstanceStateHookFiresOncePerInstance(t)
}

func testInstanceStateHookFiresOncePerInstance(t *testing.T) {
	t.Helper()
	m := testModuleInline(t, map[string]string{
		"main.tf": `
resource "aws_instance" "pool" {
  count = 6
  foo   = "pool-${count.index}"
}

resource "aws_instance" "solo" {
  foo = "solo"
}
`,
	})
	p := testProvider("aws")
	p.PlanResourceChangeFn = testDiffFn
	p.ApplyResourceChangeFn = testApplyFn
	hook := &instanceStateRecorder{}
	ctx := testContext2(t, &ContextOpts{
		Hooks:       []Hook{hook},
		Parallelism: 7,
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			addrs.NewDefaultProvider("aws"): testProviderFuncFixed(p),
		}, nil),
	})

	want := []string{"aws_instance.solo"}
	for i := 0; i < 6; i++ {
		want = append(want, "aws_instance.pool["+string(rune('0'+i))+"]")
	}
	sort.Strings(want)

	plan, diags := ctx.Plan(context.Background(), m, states.NewState(), DefaultPlanOpts)
	assertNoErrors(t, diags)
	hook.reset()
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	hook.mu.Lock()
	var got []string
	for addr, calls := range hook.calls {
		got = append(got, addr)
		if len(calls) != 1 {
			t.Errorf("%s: PostInstanceStateUpdate called %d times by the create, want once", addr, len(calls))
			continue
		}
		inst := calls[0]
		if inst == nil || inst.Current == nil {
			t.Errorf("%s: handed no current object after its create", addr)
			continue
		}
		wantFoo := strings.TrimPrefix(strings.TrimSuffix(strings.Replace(addr, "aws_instance.pool[", "pool-", 1), "]"), "aws_instance.")
		if !strings.Contains(string(inst.Current.AttrsJSON), `"foo":"`+wantFoo+`"`) {
			t.Errorf("%s: handed %s, want the object with foo = %q, its own", addr, inst.Current.AttrsJSON, wantFoo)
		}
		if prov := hook.provs[addr]; prov == nil || prov.Provider != addrs.NewDefaultProvider("aws") {
			t.Errorf("%s: handed provider %v, want the aws provider", addr, prov)
		}
	}
	early := hook.early
	hook.mu.Unlock()
	if len(early) != 0 {
		t.Errorf("on create PostInstanceStateUpdate came before PostApply for %v", early)
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("PostInstanceStateUpdate saw %v, want exactly %v", got, want)
	}

	destroyPlan, diags := ctx.Plan(context.Background(), m, state, &PlanOpts{Mode: plans.DestroyMode})
	assertNoErrors(t, diags)
	hook.reset()
	_, diags = ctx.Apply(context.Background(), destroyPlan, m, nil)
	assertNoErrors(t, diags)

	hook.mu.Lock()
	defer hook.mu.Unlock()
	if len(hook.early) != 0 {
		t.Errorf("on destroy PostInstanceStateUpdate came before PostApply for %v", hook.early)
	}
	got = got[:0]
	for addr, calls := range hook.calls {
		got = append(got, addr)
		if len(calls) != 1 {
			t.Errorf("%s: PostInstanceStateUpdate called %d times by the destroy, want once", addr, len(calls))
			continue
		}
		if calls[0] != nil && calls[0].Current != nil {
			t.Errorf("%s: handed a current object after its destroy, want none", addr)
		}
	}
	sort.Strings(got)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("on destroy PostInstanceStateUpdate saw %v, want exactly %v", got, want)
	}
}
