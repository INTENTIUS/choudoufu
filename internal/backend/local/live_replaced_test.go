// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
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
