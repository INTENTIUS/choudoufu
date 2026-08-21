// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCheckAddressesAndAnchorAdmitARealCountedModuleRename is issue #317's
// integration proof, over a config tree loaded and resolved the same way
// live-mv's caller does it - rather than over addrs.AbsResourceInstance
// values this test built by hand, the way TestCheckAddresses above does.
//
// The fixture mirrors live/e2e/limits/child-module/counted: a module call
// named "counted" with a literal count = 1 and no count.index leak in its
// own arguments, wrapping one aws_vpc.main. Since issue #195, that is the
// admitted shape - RuleChildModule reports nothing for it, and
// identity.Resolve's walkModule dispatches through ChildModuleCountKeys to
// produce a Resolution addressed "module.counted[0].aws_vpc.main". This
// test exercises exactly the two things #317's scoping comment traced by
// reading rather than running:
//
//  1. identity.Resolve actually produces that address for this fixture (the
//     scoping comment's claim that resolve.go's walkModule was never the
//     bug - only stamp's, discovery's, dataread's and lint's OWN copies of
//     this dispatch had drifted from it).
//  2. checkAddresses admits a rename whose destination is that real,
//     resolved address, and anchorAddr recognizes it as declared - so the
//     two pieces of machinery a live-mv rename actually calls agree with
//     each other on this shape, not just with a hand-built fixture.
//
// What this test does NOT cover: an actual marker rewrite (mover.find and
// mover.rewrite need a provider), and lint.CheckWith running first
// (internal/command/live_mv.go's own responsibility, covered by
// TestLimitsEnforced's "counted" row and the floci e2e test). Both are
// out of scope for a package with no provider access in its unit tests.
func TestCheckAddressesAndAnchorAdmitARealCountedModuleRename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "counted" {
  source = "./counted"
  count  = 1
}
`)
	writeFile(t, dir, "counted/main.tf", `
resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
`)

	cfg := loadConfigDir(t, dir)

	result, diags := identity.Resolve(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity.Resolve on the counted-module fixture: %s", diags.Err())
	}

	const wantAddr = "module.counted[0].aws_vpc.main"
	newRes, ok := result.Get(mustAddr(t, wantAddr))
	if !ok {
		t.Fatalf("identity.Resolve produced no resolution for %s; all resolutions: %v", wantAddr, result.All())
	}

	// The old address: a plain root aws_vpc.main, standing in for "this
	// resource used to be a root resource before the operator wrapped it in
	// the counted module" - the same shape live-mv's own rename-across-a-
	// module-boundary case documents. It need not be declared: anchorAddr
	// only requires the destination to be, since the destination is what
	// says which provider configuration and resource block own the live
	// object going forward.
	oldAddr := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_vpc", Name: "main"}.
		Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	req := Request{
		Estate:      "counted-test",
		Old:         oldAddr,
		New:         newRes.Addr,
		Config:      cfg,
		Resolutions: result.All(),
	}

	if diags := checkAddresses(req); diags.HasErrors() {
		t.Fatalf("checkAddresses refused a rename onto %s, a real count-keyed module address RuleChildModule admits: %s", newRes.Addr, diags.Err())
	}

	anchor, diags := anchorAddr(req)
	if diags.HasErrors() {
		t.Fatalf("anchorAddr refused %s -> %s: %s", req.Old, req.New, diags.Err())
	}
	if anchor.String() != newRes.Addr.String() {
		t.Errorf("anchorAddr returned %s, want the declared destination %s", anchor, newRes.Addr)
	}
}

// writeFile is loadConfigDir's fixture-building half: it writes one file of
// a config tree under dir, creating parent directories as needed.
func writeFile(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// loadConfigDir loads a local config tree the same way
// internal/live/moved's own test helper of the same name does: local module
// sources only, resolved by walking the directory tree directly rather than
// through a ".terraform/modules" manifest, which is what lets this run with
// no "choudoufu get" step first.
func loadConfigDir(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	rootMod, diags := parser.LoadConfigDir(dir, testModuleCall(dir))
	if diags.HasErrors() {
		t.Fatalf("failed to load %s: %s", dir, diags.Error())
	}

	walker := configs.ModuleWalkerFunc(func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
		sourceAddr, ok := req.SourceAddr.(addrs.ModuleSourceLocal)
		if !ok {
			return nil, nil, hcl.Diagnostics{{
				Severity: hcl.DiagError,
				Summary:  "Unsupported module source in test fixture",
				Detail:   "Only local module sources are supported by these tests.",
				Subject:  req.SourceAddrRange.Ptr(),
			}}
		}
		childDir := filepath.Join(req.Parent.Module.SourceDir, string(sourceAddr))
		mod, diags := parser.LoadConfigDir(childDir, req.Call)
		return mod, nil, diags
	})

	cfg, diags := configs.BuildConfig(t.Context(), rootMod, walker)
	if diags.HasErrors() {
		t.Fatalf("failed to build config from %s: %s", dir, diags.Error())
	}
	return cfg
}

func testModuleCall(dir string) configs.StaticModuleCall {
	return configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if v.Default != cty.NilVal {
				return v.Default, nil
			}
			if v.ConstraintType != cty.NilType {
				return cty.UnknownVal(v.ConstraintType), nil
			}
			return cty.DynamicVal, nil
		},
		dir,
		"default",
	)
}

// mustAddr parses a resource-instance address string using the same
// grammar discovery.UnescapeAddress and identity.Resolve's own addresses
// use: addrs.ParseAbsResourceInstanceStr, via configs' own addrs package.
func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q as an absolute resource instance address: %s", s, diags.Err())
	}
	return addr
}
