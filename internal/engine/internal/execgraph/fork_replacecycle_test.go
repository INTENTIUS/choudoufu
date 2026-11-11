// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package execgraph

// choudoufu fork port (#2380): remove when upstream #4714 lands.

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/internal/exec"
	"github.com/intentius/choudoufu/internal/lang/eval"
)

// TestForkCycles builds the shape the planning engine builds for two
// deletes, whose ordering edges go through mutable waiters, once acyclic
// and once with each delete waiting on the other.
func TestForkCycles(t *testing.T) {
	build := func(cyclic bool) *Graph {
		b := NewBuilder()
		applies := map[string]ResourceInstanceResultRef{}
		register := map[string]func(AnyResultRef){}
		for _, name := range []string{"a", "b"} {
			addr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "test", Name: name}.Absolute(addrs.RootModuleInstance).Instance(addrs.NoKey)
			addrRef := b.ConstantResourceInstAddr(addr)
			prior := b.ResourceInstancePrior(addrRef)
			meta := b.ResourceInstanceCurrentMeta(addrRef, prior)
			plan := b.ManagedFinalPlan(meta, NilResultRef[*eval.DesiredResourceInstance](), prior, b.ConstantValue(cty.NullVal(cty.DynamicPseudoType)))
			waiter, reg := b.MutableWaiter()
			applies[name] = b.ManagedApply(plan, NilResultRef[*exec.ResourceInstanceObject](), waiter)
			register[name] = reg
		}
		register["a"](applies["b"])
		if cyclic {
			register["b"](applies["a"])
		}
		return b.Finish()
	}

	if got := build(false).ForkCycles(); got != nil {
		t.Fatalf("an acyclic graph reported cycles: %q", got)
	}

	got := build(true).ForkCycles()
	if len(got) != 1 {
		t.Fatalf("got %d cycles, want 1: %q", len(got), got)
	}
	joined := strings.Join(got[0], "\n")
	for _, want := range []string{"ManagedApply", "test.a", "test.b"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the cycle does not mention %s:\n%s", want, joined)
		}
	}
}
