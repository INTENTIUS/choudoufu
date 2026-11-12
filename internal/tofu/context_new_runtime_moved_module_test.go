// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/states"
)

// TestNewRuntimeToggle_movedModuleApplies: a moved block that renames a
// module call, applied on the new runtime, moves every resource instance in
// the module. The module holds several resources and a counted one, some
// updating in place and some not, like the terraform-aws-modules/lambda
// simple example that panicked with "invalid move": the plan's prior state
// held each moved object at both its old and its new address, so the
// apply's move found its destination taken. A root-level moved block took
// the same path.
func TestNewRuntimeToggle_movedModuleApplies(t *testing.T) {
	toggleOn(t)
	strictOn(t)
	m := testModuleInline(t, map[string]string{
		"main.tf": `
module "new" {
  source = "./mod"
}
moved {
  from = module.old
  to   = module.new
}
resource "test_object" "r" {
  name = "r"
}
moved {
  from = test_object.q
  to   = test_object.r
}
`,
		"mod/main.tf": `
resource "test_object" "a" {
  name = "a"
  tag  = "new"
}
resource "test_object" "b" {
  count = 2
  name  = "b"
}
resource "test_object" "c" {
  name = "c"
}
`,
	})
	provider := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("test"), Module: addrs.RootModule}
	prior := states.BuildState(func(s *states.SyncState) {
		for addr, name := range map[string]string{
			"module.old.test_object.a":    "a",
			"module.old.test_object.b[0]": "b",
			"module.old.test_object.b[1]": "b",
			"module.old.test_object.c":    "c",
			"test_object.q":               "r",
		} {
			s.SetResourceInstanceCurrent(
				mustResourceInstanceAddr(addr),
				&states.ResourceInstanceObjectSrc{Status: states.ObjectReady, AttrsJSON: []byte(`{"id":"i-` + name + `","name":"` + name + `","tag":"old"}`)},
				provider,
				addrs.NoKey,
			)
		}
	})
	p := toggleProvider()
	p.GetProviderSchemaResponse.ResourceTypes["test_object"].Block.Attributes["tag"] = &configschema.Attribute{Type: cty.String, Optional: true}
	ctx := toggleContext(t, p)
	plan, diags := ctx.Plan(context.Background(), m, prior, DefaultPlanOpts)
	if diags.HasErrors() {
		t.Fatalf("plan failed:\n%s", diags.ErrWithWarnings())
	}
	for _, addr := range []string{"module.old.test_object.a", "test_object.q"} {
		if plan.PriorState.ResourceInstance(mustResourceInstanceAddr(addr)) == nil {
			t.Errorf("the plan's prior state has no %s to move", addr)
		}
	}
	for _, addr := range []string{"module.new.test_object.a", "test_object.r"} {
		if plan.PriorState.ResourceInstance(mustResourceInstanceAddr(addr)) != nil {
			t.Errorf("the plan's prior state already has %s before the move is applied", addr)
		}
	}
	state, diags := ctx.Apply(context.Background(), plan, m, nil)
	if diags.HasErrors() {
		t.Fatalf("apply failed:\n%s", diags.ErrWithWarnings())
	}
	for _, addr := range []string{"module.new.test_object.a", "module.new.test_object.b[0]", "module.new.test_object.b[1]", "module.new.test_object.c", "test_object.r"} {
		if state.ResourceInstance(mustResourceInstanceAddr(addr)) == nil {
			t.Errorf("%s is not in the applied state", addr)
		}
	}
	if a := state.ResourceInstance(mustResourceInstanceAddr("module.new.test_object.a")); a != nil && a.Current != nil {
		if got := string(a.Current.AttrsJSON); !strings.Contains(got, `"tag":"new"`) {
			t.Errorf("module.new.test_object.a was not updated in place: %s", got)
		}
	}
	if state.ResourceInstance(mustResourceInstanceAddr("test_object.q")) != nil {
		t.Errorf("test_object.q is still in the applied state")
	}
	if state.Module(addrs.RootModuleInstance.Child("old", addrs.NoKey)) != nil {
		t.Errorf("module.old is still in the applied state:\n%s", state)
	}
}
