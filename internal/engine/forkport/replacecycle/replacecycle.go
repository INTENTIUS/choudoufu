// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package replacecycle is choudoufu fork port (#2380): remove when upstream
// #4714 lands. It is unit W3 of #2256, on the fork-port framework (#2376).
//
// The new runtime replaces resource instances, but it has two gaps that
// made the gap table refuse every replace outside live mode (row
// "replace-cbd-cycle" of internal/live/newruntime/gaps.json):
//
//   - it never checks its execution graph for a cycle
//     (internal/engine/planning/execgraph_resource.go says so in a TODO), so a
//     cycle such as upstream #4714's would surface only during the apply,
//     after some of it had run;
//   - it never records create_before_destroy in state, so an orphaned
//     object is replaced or destroyed in the wrong order on the next run.
//
// The port closes both: [Cycles] finds the cycles in a dependency graph,
// and the planning engine turns any it finds in the execution graph into a
// plan error ([CycleError]); the planning and apply engines record
// create_before_destroy on the objects they write. Its call sites in
// upstream code are commented with this port's issue.
//
// Removing the port is deleting this package, those call sites and the
// row's fork_port and switch. It is registered in internal/engine/forkport
// as [Name]; switched off, the call sites do nothing and the row refuses a
// replace outside live mode again.
package replacecycle

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "replacecycle"

// Enabled reports whether the port is on.
func Enabled() bool {
	return forkport.Enabled(Name)
}

// Cycles returns every cycle among n nodes, each as its node indices in
// ascending order, ordered by their first index. deps(i) lists the nodes
// node i depends on. A node that depends on itself is a cycle of one.
//
// It is Tarjan's strongly connected components algorithm, iterative so that
// a long chain of operations cannot exhaust the stack.
func Cycles(n int, deps func(int) []int) [][]int {
	const unvisited = -1
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = unvisited
	}
	var stack []int
	var out [][]int
	next := 0

	type frame struct {
		node  int
		deps  []int
		child int
	}
	for root := 0; root < n; root++ {
		if index[root] != unvisited {
			continue
		}
		work := []frame{{node: root, deps: deps(root)}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true
		for len(work) > 0 {
			f := &work[len(work)-1]
			if f.child < len(f.deps) {
				w := f.deps[f.child]
				f.child++
				switch {
				case index[w] == unvisited:
					index[w], low[w] = next, next
					next++
					stack = append(stack, w)
					onStack[w] = true
					work = append(work, frame{node: w, deps: deps(w)})
				case onStack[w]:
					low[f.node] = min(low[f.node], index[w])
				}
				continue
			}
			v := f.node
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1].node
				low[parent] = min(low[parent], low[v])
			}
			if low[v] != index[v] {
				continue
			}
			var scc []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			if len(scc) == 1 && !dependsOn(deps(v), v) {
				continue
			}
			sort.Ints(scc)
			out = append(out, scc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

func dependsOn(deps []int, node int) bool {
	for _, d := range deps {
		if d == node {
			return true
		}
	}
	return false
}

// CycleError is the plan error for cycles in the execution graph. Each cycle
// is the descriptions of the operations in it, which name the resource
// instances involved.
func CycleError(cycles [][]string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(cycles) == 0 {
		return diags
	}
	var detail strings.Builder
	detail.WriteString("The planned changes cannot be ordered: the execution graph has a cycle, so applying this plan could stop part-way. The operations in each cycle:")
	for i, cycle := range cycles {
		fmt.Fprintf(&detail, "\n\nCycle %d:", i+1)
		for _, op := range cycle {
			fmt.Fprintf(&detail, "\n  - %s", op)
		}
	}
	detail.WriteString("\n\nThis is choudoufu's check on OpenTofu's new runtime (fork port " + Name + ", #2380), which has none of its own yet (opentofu/opentofu#4714).")
	return diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cycle in the planned changes", detail.String()))
}
