// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package execgraph

// choudoufu fork port (#2380): remove when upstream #4714 lands. The whole
// file is the execution graph's side of package replacecycle; the graph's
// tables are unexported, so the walk lives here.

import (
	"github.com/intentius/choudoufu/internal/engine/forkport/replacecycle"
)

// ForkCycles returns the operations in each cycle of the graph, as their
// debug summaries, which name the resource instances involved. It is nil
// for an acyclic graph.
//
// The nodes are the operations and the waiters, and an edge runs from each
// to the operation or waiter results it depends on. Operands can only refer
// to results built before them, so a cycle needs a waiter that was added to
// after it was referred to ([Builder.MutableWaiter]); the walk covers both
// kinds anyway.
func (g *Graph) ForkCycles() [][]string {
	nOps := len(g.ops)
	node := func(ref AnyResultRef) (int, bool) {
		switch ref := ref.(type) {
		case anyOperationResultRef:
			return ref.operationResultIndex(), true
		case waiterResultRef:
			return nOps + ref.index, true
		}
		return 0, false
	}
	deps := func(i int) []int {
		var refs []AnyResultRef
		if i < nOps {
			refs = g.ops[i].operands
		} else {
			refs = g.waiters[i-nOps]
		}
		var out []int
		for _, ref := range refs {
			if n, ok := node(ref); ok {
				out = append(out, n)
			}
		}
		return out
	}
	var ret [][]string
	for _, cycle := range replacecycle.Cycles(nOps+len(g.waiters), deps) {
		var ops []string
		for _, i := range cycle {
			if i < nOps {
				ops = append(ops, g.operationDebugSummary(g.ops[i]))
			}
		}
		if len(ops) == 0 {
			ops = append(ops, "(waiters only, no operation)")
		}
		ret = append(ret, ops)
	}
	return ret
}
