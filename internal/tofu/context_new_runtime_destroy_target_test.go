// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/newruntime"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// destroyTargetState holds test_object.a and test_object.b, with b recorded
// as depending on a, as TestApply_targetedDestroy's state does.
func destroyTargetState() *states.State {
	provider := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}
	return states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(
			mustResourceInstanceAddr("test_object.a"),
			&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"id":"i-a","name":"a"}`)},
			provider, addrs.NoKey,
		)
		s.SetResourceInstanceCurrent(
			mustResourceInstanceAddr("test_object.b"),
			&states.ResourceInstanceObjectSrc{
				Status:       states.ObjectReady,
				AttrsJSON:    []byte(`{"id":"i-b","name":"i-a"}`),
				Dependencies: []addrs.ConfigResource{mustConfigResourceAddr("test_object.a")},
			},
			provider, addrs.NoKey,
		)
	})
}

const destroyTargetConfig = `
resource "test_object" "a" {
  name = "a"
}
resource "test_object" "b" {
  name = test_object.a.id
}
`

func refusedUnder(diags tfdiags.Diagnostics, id string) bool {
	for _, d := range diags {
		desc := d.Description()
		if d.Severity() == tfdiags.Error && desc.Summary == newruntime.RefusalSummary && strings.Contains(desc.Detail, `gap "`+id+`"`) {
			return true
		}
	}
	return false
}

func deletes(plan *plans.Plan) []string {
	var out []string
	if plan == nil || plan.Changes == nil {
		return nil
	}
	for _, rc := range plan.Changes.Resources {
		if rc.Action == plans.Delete {
			out = append(out, rc.Addr.String())
		}
	}
	return out
}

// TestNewRuntimeToggle_destroyTargetRefusesOverDestroy: on the new runtime,
// a destroy with -target of a dependent also plans the deletion of what it
// depends on (TestApply_targetedDestroy, subtest "Targeted Destroy"). The
// post-plan check refuses such a plan under gap "destroy-target", before it
// is shown or saved. A targeted destroy whose deletes are only the target
// and its dependents is not refused.
func TestNewRuntimeToggle_destroyTargetRefusesOverDestroy(t *testing.T) {
	toggleOn(t)
	m := testModuleInline(t, map[string]string{"main.tf": destroyTargetConfig})

	t.Run("target a dependent", func(t *testing.T) {
		opts := SimplePlanOpts(plans.DestroyMode, nil)
		opts.Targets = []addrs.Targetable{mustResourceInstanceAddr("test_object.b")}
		plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, destroyTargetState(), opts)
		if !refusedUnder(diags, "destroy-target") {
			t.Fatalf("destroy -target test_object.b was not refused under gap %q; it deletes %v; diagnostics:\n%s", "destroy-target", deletes(plan), diags.ErrWithWarnings())
		}
		if plan != nil {
			t.Errorf("a refused plan was returned")
		}
	})

	t.Run("target what others depend on", func(t *testing.T) {
		opts := SimplePlanOpts(plans.DestroyMode, nil)
		opts.Targets = []addrs.Targetable{mustResourceInstanceAddr("test_object.a")}
		plan, diags := toggleContext(t, toggleProvider()).Plan(context.Background(), m, destroyTargetState(), opts)
		if diags.HasErrors() {
			t.Fatalf("destroy -target test_object.a, which deletes only the target and its dependent, failed: %s", diags.Err())
		}
		got := strings.Join(deletes(plan), " ")
		if !strings.Contains(got, "test_object.a") || !strings.Contains(got, "test_object.b") {
			t.Errorf("destroy -target test_object.a deletes %q, want test_object.a and test_object.b", got)
		}
	})
}
