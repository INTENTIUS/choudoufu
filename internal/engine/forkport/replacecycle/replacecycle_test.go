// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package replacecycle

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

func TestCycles(t *testing.T) {
	for name, tc := range map[string]struct {
		edges [][]int
		want  string
	}{
		"empty":      {nil, "[]"},
		"chain":      {[][]int{{1}, {2}, nil}, "[]"},
		"diamond":    {[][]int{{1, 2}, {3}, {3}, nil}, "[]"},
		"self":       {[][]int{{0}, nil}, "[[0]]"},
		"two":        {[][]int{{1}, {0}}, "[[0 1]]"},
		"two cycles": {[][]int{{1}, {0}, {3}, {4}, {2}, {0, 2}}, "[[0 1] [2 3 4]]"},
		"tail":       {[][]int{{1}, {2}, {1}}, "[[1 2]]"},
		"long chain": {chain(100000), "[]"},
		"long cycle": {ring(100000), "[[0 ... 99999]]"},
	} {
		t.Run(name, func(t *testing.T) {
			got := Cycles(len(tc.edges), func(i int) []int { return tc.edges[i] })
			s := fmt.Sprint(got)
			if len(got) == 1 && len(got[0]) > 10 {
				s = fmt.Sprintf("[[%d ... %d]]", got[0][0], got[0][len(got[0])-1])
			}
			if s != tc.want {
				t.Errorf("got %s, want %s", s, tc.want)
			}
		})
	}
}

// chain is n nodes, each depending on the next.
func chain(n int) [][]int {
	out := make([][]int, n)
	for i := 0; i+1 < n; i++ {
		out[i] = []int{i + 1}
	}
	return out
}

// ring is a chain whose last node depends on the first.
func ring(n int) [][]int {
	out := chain(n)
	out[n-1] = []int{0}
	return out
}

func TestCycleError(t *testing.T) {
	if diags := CycleError(nil); len(diags) != 0 {
		t.Fatalf("no cycles gave diagnostics: %s", diags.Err())
	}
	diags := CycleError([][]string{{"ManagedApply(test.a)", "ManagedApply(test.b)"}})
	if !diags.HasErrors() {
		t.Fatal("a cycle is not an error")
	}
	if msg := diags.Err().Error(); !strings.Contains(msg, "test.a") || !strings.Contains(msg, "test.b") {
		t.Errorf("the error does not name the operations: %s", msg)
	}
}

func TestSwitchTurnsThePortOff(t *testing.T) {
	for name, env := range map[string]string{"all ports off": forkport.AllOff, "this port off": forkport.Switch(Name)} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(Name), "")
			if !Enabled() {
				t.Fatal("with no switch set the port must be on")
			}
			t.Setenv(env, "1")
			if Enabled() {
				t.Fatalf("with %s=1 the port must be off", env)
			}
		})
	}
}
