// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// writeTree writes a map of relative paths to a fresh directory and returns
// it, so a fixture with a real module tree can be written inline rather than
// added to testdata.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("creating %s: %s", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("writing %s: %s", full, err)
		}
	}
	return dir
}

// TestModuleInstancesOfMatchesResolution is the guard the module-instance
// walks did not have, and it fails against the code it was written for.
//
// [analyzer.moduleInstancesOf] read call.ForEach and never call.Count, so a
// count'd module call fell through to [identity.ChildModuleKeys] with a nil
// expression - which reports the single UNKEYED instance a static call has.
// The data-read phase then addressed every result inside such a module as
// "module.sites.data.aws_ami.x" while resolution, evaluation and the live
// run all name it "module.sites[0].data.aws_ami.x", so nothing the phase
// read was ever found again and a valid estate was refused. A count = 0
// call was worse still: one module instance enumerated where none exists.
//
// The expectation is COMPUTED from [identity.Resolve], not written down.
// Resolution is what decides the module instances every address in the run
// carries, and it makes the full count/for_each/static dispatch. So this
// test fails whichever of the two walks drifts, in whichever direction -
// which is exactly what neither of the three shipped instances of this same
// defect had. Restating the keys here would only prove this file agrees
// with itself.
func TestModuleInstancesOfMatchesResolution(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.tf": `
module "counted" {
  source = "./child"
  count  = 2
  prefix = "counted-${count.index}"
}

module "keyed" {
  source   = "./child"
  for_each = toset(["a", "b"])
  prefix   = each.key
}

module "static" {
  source = "./child"
  prefix = "static"
}

module "onlyone" {
  source = "./child"
  count  = 1
  prefix = "onlyone"
}
`,
		"child/main.tf": `
variable "prefix" { type = string }

resource "aws_s3_bucket" "b" {
  bucket = "example-${var.prefix}"
}

module "leaf" {
  source = "../leaf"
  prefix = var.prefix
}
`,
		"leaf/main.tf": `
variable "prefix" { type = string }

resource "aws_s3_bucket" "leaf" {
  bucket = "example-leaf-${var.prefix}"
}
`,
	})

	cfg := loadConfigTree(t, dir, nil)

	// The external source. Every resolved address carries the module
	// instance the run will use; grouping them by static module path gives
	// the instance set moduleInstancesOf has to reproduce.
	res, diags := identity.Resolve(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("resolving the fixture: %s", diags.Err())
	}
	if res.Len() == 0 {
		t.Fatal("resolution produced no addresses; this test's external source is empty and it would prove nothing")
	}

	want := map[string]map[string]bool{}
	for _, r := range res.All() {
		modInst := r.Addr.Module
		path := modInst.Module().String()
		if want[path] == nil {
			want[path] = map[string]bool{}
		}
		want[path][modInst.String()] = true
	}

	// The premise: resolution really does key the count'd call. If a future
	// change stops it doing that, this test must fail loudly rather than
	// silently checking that two unkeyed walks agree.
	if !want["module.counted"]["module.counted[0]"] {
		t.Fatalf("resolution does not key module.counted; this test's premise is gone: %v", want["module.counted"])
	}

	an := &analyzer{ctx: t.Context(), cfg: cfg}

	for path, wantInsts := range want {
		modAddr := moduleAddrOf(t, path)
		got := map[string]bool{}
		for _, inst := range an.moduleInstancesOf(modAddr) {
			got[inst.String()] = true
		}
		if !sameSet(got, wantInsts) {
			t.Errorf("moduleInstancesOf(%s) = %v; resolution names %v", path, sortedKeys(got), sortedKeys(wantInsts))
		}
	}
}

// moduleAddrOf turns a static module path string back into an addrs.Module.
// The fixture's calls are all unkeyed in the STATIC tree, so the path is
// just its dot-separated call names.
func moduleAddrOf(t *testing.T, path string) addrs.Module {
	t.Helper()
	if path == "" {
		return addrs.RootModule
	}
	var out addrs.Module
	rest := path
	for rest != "" {
		if len(rest) < len("module.") || rest[:len("module.")] != "module." {
			t.Fatalf("unexpected module path %q", path)
		}
		rest = rest[len("module."):]
		name := rest
		if i := indexByte(rest, '.'); i >= 0 {
			name = rest[:i]
			rest = rest[i+1:]
		} else {
			rest = ""
		}
		out = append(out, name)
	}
	return out
}

func indexByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
