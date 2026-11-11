// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package newruntime

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

// forkPortDir is internal/engine/forkport, from this package's directory.
const forkPortDir = "../../engine/forkport"

const forkPortPrefix = "internal/engine/forkport/"

var portName = regexp.MustCompile(`^[a-z][a-z0-9]*$`)

// TestForkPortRegistry holds the fork-port registry (#2376), the port
// packages and the gap table to each other: every fork_port row names a
// registered port with its switch, every registered port has its rows, a
// package and at least one expiry issue, its expiry issues are exactly its
// rows' upstream issues (what scripts/upstream-sync.sh --report reads), and
// every package under internal/engine/forkport is registered.
func TestForkPortRegistry(t *testing.T) {
	if len(forkport.Ports) == 0 {
		t.Fatal("no fork port is registered; this test would check nothing")
	}
	for _, r := range Rows() {
		if r.ForkPort == "" {
			continue
		}
		name, ok := strings.CutPrefix(r.ForkPort, forkPortPrefix)
		if !ok || name == "" {
			t.Errorf("row %q: fork_port %q is not under %s", r.ID, r.ForkPort, forkPortPrefix)
			continue
		}
		p, ok := forkport.Lookup(name)
		if !ok {
			t.Errorf("row %q: fork_port %q is not registered in internal/engine/forkport.Ports", r.ID, r.ForkPort)
			continue
		}
		if !slices.Contains(p.Rows, r.ID) {
			t.Errorf("row %q names port %q, whose registration lists rows %v", r.ID, name, p.Rows)
		}
		if want := forkport.Switch(name) + "=1"; r.Switch != want {
			t.Errorf("row %q: switch %q, want %q", r.ID, r.Switch, want)
		}
	}

	dirs, err := os.ReadDir(forkPortDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if d.IsDir() {
			if _, ok := forkport.Lookup(d.Name()); !ok {
				t.Errorf("%s%s is not registered in internal/engine/forkport.Ports", forkPortPrefix, d.Name())
			}
		}
	}

	seen := map[string]bool{}
	for _, p := range forkport.Ports {
		if !portName.MatchString(p.Name) || seen[p.Name] {
			t.Errorf("port %q: a name is unique, lower case letters and digits", p.Name)
		}
		seen[p.Name] = true
		if gofiles, _ := filepath.Glob(filepath.Join(forkPortDir, p.Name, "*.go")); len(gofiles) == 0 {
			t.Errorf("port %q: no package at %s%s", p.Name, forkPortPrefix, p.Name)
		}
		if len(p.Expiry) == 0 {
			t.Errorf("port %q has no expiry issue; every port is removed when upstream closes one", p.Name)
		}
		if len(p.Rows) == 0 {
			t.Errorf("port %q fills no gap row", p.Name)
		}
		upstream := map[string]bool{}
		for _, id := range p.Rows {
			r, ok := RowByID(id)
			if !ok {
				t.Errorf("port %q: gap row %q does not exist", p.Name, id)
				continue
			}
			if r.ForkPort != forkPortPrefix+p.Name {
				t.Errorf("port %q: gap row %q names fork_port %q", p.Name, id, r.ForkPort)
			}
			for _, u := range r.Upstream {
				upstream[u] = true
			}
		}
		var want []string
		for u := range upstream {
			want = append(want, u)
		}
		got := slices.Clone(p.Expiry)
		sort.Strings(want)
		sort.Strings(got)
		if !slices.Equal(got, want) {
			t.Errorf("port %q: expiry issues %v, but its rows list upstream %v; they must match", p.Name, got, want)
		}
	}
}

// TestForkPortSwitches proves each switch turns a port off on its own.
func TestForkPortSwitches(t *testing.T) {
	for _, p := range forkport.Ports {
		t.Run(p.Name, func(t *testing.T) {
			t.Setenv(forkport.AllOff, "")
			t.Setenv(forkport.Switch(p.Name), "")
			if !forkport.Enabled(p.Name) {
				t.Fatal("with no switch set the port must be on")
			}
			for _, env := range []string{forkport.AllOff, forkport.Switch(p.Name)} {
				t.Setenv(forkport.AllOff, "")
				t.Setenv(forkport.Switch(p.Name), "")
				t.Setenv(env, "1")
				if forkport.Enabled(p.Name) {
					t.Errorf("with %s=1 the port is still on", env)
				}
			}
		})
	}
}
