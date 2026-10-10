// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"sync"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestMidApplyDisjointAppliesBothLand is #1938's done-when with GitHub
// issue #1944's mid-apply writes in it (cdf-concurrency's shape): two
// applies of one estate, both planned before either writes, one changing
// left and the other right. Each writes its instance's record as the
// instance returns and then runs the final pass, in either order,
// interleaved and at once, and both land.
func TestMidApplyDisjointAppliesBothLand(t *testing.T) {
	for name, mk := range stores1944(t) {
		for _, order := range []string{"a-then-b", "b-then-a", "interleaved", "concurrent"} {
			t.Run(name+"/"+order, func(t *testing.T) {
				e := newEstate1938(t, mk(t))
				e.seed("left-0", "right-0")
				a := e.plan()
				b := e.plan()
				finalA := e.finalState("left-a", "right-0")
				finalB := e.finalState("left-0", "right-b")
				applyA := func() tfdiags.Diagnostics {
					return e.writeInstance(a, e.left, only(finalA, e.left)).Append(e.writeBack(a, finalA))
				}
				applyB := func() tfdiags.Diagnostics {
					return e.writeInstance(b, e.right, only(finalB, e.right)).Append(e.writeBack(b, finalB))
				}

				var diagsA, diagsB tfdiags.Diagnostics
				switch order {
				case "a-then-b":
					diagsA = applyA()
					diagsB = applyB()
				case "b-then-a":
					diagsB = applyB()
					diagsA = applyA()
				case "interleaved":
					diagsA = e.writeInstance(a, e.left, only(finalA, e.left))
					diagsB = e.writeInstance(b, e.right, only(finalB, e.right))
					diagsA = diagsA.Append(e.writeBack(a, finalA))
					diagsB = diagsB.Append(e.writeBack(b, finalB))
				case "concurrent":
					var wg sync.WaitGroup
					start := make(chan struct{})
					wg.Add(2)
					go func() { defer wg.Done(); <-start; diagsA = applyA() }()
					go func() { defer wg.Done(); <-start; diagsB = applyB() }()
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
					t.Errorf("left's record is %s, want left-a", id)
				}
				if id, _ := e.recorded(e.right); id != "right-b" {
					t.Errorf("right's record is %s, want right-b", id)
				}
			})
		}
	}
}
