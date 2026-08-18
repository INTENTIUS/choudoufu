// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestDeclaredEstateNamesReachesEveryModule is the guard neither copy of
// this walk had.
//
// [DeclaredEstateNames] was written out twice, body for body: once in
// internal/command as statelessEstateFromModule and once in
// internal/live/check as declaredEstateNamesFrom, whose own doc said it
// mirrored the other. Between them there was no test at all - stopping
// check's copy recursing into child modules left the whole tree green - so
// the instrument and the run could have disagreed about which estate a
// configuration declares without anything noticing. Issue #285.
//
// The expected set is COMPUTED from the module tree rather than written
// down: the fixture gives each module exactly one estate name, spelled
// "estate-" plus the module call's own name ("estate-root" at the root), so
// walking cfg.Children says how many distinct names a complete walk has to
// find and what each one is. A walk that stops at any depth returns fewer,
// and the test names the missing one.
//
// Each level reaches its value differently - a root variable, a child
// local, a bare string literal - so the static-evaluator leg is exercised
// per module rather than only at the root, where a single-module test would
// leave it.
func TestDeclaredEstateNamesReachesEveryModule(t *testing.T) {
	cfg := loadNestedModuleConfig(t, filepath.Join("testdata", "estate-names"))

	want := estateNamesFromTree(cfg)
	sort.Strings(want)
	if len(want) < 3 {
		t.Fatalf("the fixture has %d module(s); this test cannot show a recursion failure with fewer than 3", len(want))
	}

	got := DeclaredEstateNames(context.Background(), cfg)

	if len(got) != len(want) {
		t.Fatalf("DeclaredEstateNames found %v; the module tree declares %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("DeclaredEstateNames found %v; the module tree declares %v", got, want)
			break
		}
	}
}

// loadNestedModuleConfig is [loadModuleConfig] resolving each module call's
// source relative to the CALLING module's own directory, which is what the
// CLI does and what a fixture more than one level deep needs.
// loadModuleConfig joins every source onto the root directory instead, so a
// grandchild sitting inside its own parent's directory is invisible to it.
func loadNestedModuleConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}

	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			childDir := filepath.Join(req.Parent.Module.SourceDir, req.SourceAddr.String())
			child, childDiags := parser.LoadConfigDir(childDir, req.Call)
			if childDiags.HasErrors() {
				t.Fatalf("loading child module %q from %s: %s", req.Name, childDir, childDiags.Error())
			}
			return child, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// estateNamesFromTree names the estate each module in the tree declares,
// from the shape of the tree itself. The fixture holds up its half of the
// convention; this function holds up the other.
func estateNamesFromTree(cfg *configs.Config) []string {
	if cfg == nil || cfg.Module == nil {
		return nil
	}
	name := "root"
	if len(cfg.Path) > 0 {
		name = cfg.Path[len(cfg.Path)-1]
	}
	out := []string{"estate-" + name}
	for _, child := range cfg.Children {
		out = append(out, estateNamesFromTree(child)...)
	}
	return out
}
