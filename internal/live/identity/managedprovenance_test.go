// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
)

// TestSelectReferencedValueRefusesAMarkedAggregate is the regression this
// package's own bug demanded: internal/live/marksafe's TestNoUnprovenUnsafeCallSite
// caught selectReferencedValue's cur.LengthInt() call reading a value
// nothing had proven unmarked at that identifier - val's own IsMarked test,
// a few lines up, does not carry over to cur by VALUE, because marksafe
// compares receivers as rendered text and "cur" is a different name from
// "val" even though the two hold the same cty.Value at that point in the
// function. This is the dynamic half of that proof: an actually-marked
// aggregate must refuse, not panic, and an unmarked one must still resolve.
//
// Sensitivity does not have to originate on the aggregate itself to reach
// here - a sensitive input variable propagates its mark onto anything it is
// interpolated into, which is exactly how a real
// [identity.Context.ManagedResults] entry could arrive marked: a managed
// resource argument built in part from a sensitive variable.
func TestSelectReferencedValueRefusesAMarkedAggregate(t *testing.T) {
	elem := cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("example"),
	})
	unmarked := cty.TupleVal([]cty.Value{elem})
	marked := unmarked.Mark("sensitive-test-mark")

	if _, ok := selectReferencedValue(marked, addrs.IntKey(0), hcl.Traversal{}); ok {
		t.Fatal("selectReferencedValue resolved a marked aggregate; it must refuse, never read through the mark")
	}

	got, ok := selectReferencedValue(unmarked, addrs.IntKey(0), hcl.Traversal{})
	if !ok {
		t.Fatal("selectReferencedValue declined an ordinary unmarked tuple, which the marked case above must not have been the only thing distinguishing")
	}
	if !got.RawEquals(elem) {
		t.Errorf("selectReferencedValue returned %#v, want %#v", got, elem)
	}
}
