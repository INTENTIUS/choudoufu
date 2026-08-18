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

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCountBlockWalkNamesTheSameModuleInstancesResolutionDoes is the guard
// for a divergence that has now happened twice: a walk over the module tree
// that reads a call's for_each and not its count.
//
// [identity.resolver.walkModule] is the source of truth for which module
// instances a configuration has and what each one is called. Four other
// walks exist - stamp's moduleResourcesFrom, stamp's childExpansion,
// dataread's moduleInstancesOf, and this package's walkCountBlocks - and
// each one has to name the instances the same way, because the names ARE the
// first half of every block address a marker carries. stamp's copy read
// for_each alone and wrote one literal tofu-address onto every instance of a
// count'd module (fixed in de7c0ae3ef); this package's copy read for_each
// alone and indexed a count'd module's count blocks under the unkeyed path,
// so [declared.countBlockFor] missed every marker naming one and
// [countBlock.instanceAddr] named an address no instance has.
//
// The assertion is on the STRINGS the two walks produce, and the expected
// set is not written down here: it is computed from identity.Resolve over
// the same configuration. A future edit that reintroduces the divergence
// makes the two sets differ, whichever direction it drifts in, and a test
// that agreed with its own derivation rule would not have caught either
// defect.
func TestCountBlockWalkNamesTheSameModuleInstancesResolutionDoes(t *testing.T) {
	dir := filepath.Join("testdata", "count-module-walk")
	cfg := loadModuleConfig(t, dir)

	// What resolution calls each count'd block: the instance addresses it
	// produces, with the instance key dropped, deduplicated.
	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed: %s", diags.Err())
	}
	want := map[string]bool{}
	for _, r := range res.All() {
		if r.Addr.Resource.Resource.Type != "aws_eip" {
			continue
		}
		want[r.Addr.ContainingResource().String()] = true
	}
	if len(want) == 0 {
		t.Fatalf("the fixture resolved no aws_eip instances at all; it can prove nothing")
	}

	// What the count-block walk calls them. d.types must name the type or
	// walkCountBlocks indexes nothing; the entry map's contents are not read
	// by the walk, only its presence.
	d := &declared{
		types:  map[string]map[string]*declaredEntry{"aws_eip": {}},
		counts: map[string]map[string]*countBlock{},
	}
	d.walkCountBlocks(context.Background(), cfg, addrs.RootModuleInstance, addrs.AbsProviderConfig{})

	got := map[string]bool{}
	for _, cb := range d.counts["aws_eip"] {
		got[addrs.AbsResource{Module: cb.module, Resource: cb.resource}.String()] = true
	}

	if !sameStringSet(got, want) {
		t.Errorf("walkCountBlocks indexes\n  %v\nbut identity resolution names the same blocks\n  %v",
			sortedSetKeys(got), sortedSetKeys(want))
	}

	// And the escaped key each block is filed under is the escaped form of
	// that same address, since countBlockFor looks a marker's value up by it.
	for addr, cb := range d.counts["aws_eip"] {
		full := addrs.AbsResource{Module: cb.module, Resource: cb.resource}.String()
		if addr != EscapeAddress(full) {
			t.Errorf("count block for %s is filed under %q, want %q", full, addr, EscapeAddress(full))
		}
	}
}

func sameStringSet(a, b map[string]bool) bool {
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

func sortedSetKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// loadModuleConfig is [loadConfig] for a fixture with real child modules:
// the walker loads the called directory instead of failing the test.
func loadModuleConfig(t *testing.T, dir string) *configs.Config {
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
			child, childDiags := parser.LoadConfigDir(filepath.Join(dir, req.SourceAddr.String()), req.Call)
			if childDiags.HasErrors() {
				t.Fatalf("loading child module %q: %s", req.Name, childDiags.Error())
			}
			return child, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}
