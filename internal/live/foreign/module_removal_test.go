// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

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
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// moduleRemovalRoot declares one resource block at the root and calls a child
// module that declares a different one. Neither block exists on both sides,
// which is what makes the two directions of the lookup distinguishable: a
// root-only lookup gets each of them exactly backwards for an orphan inside
// the module.
const moduleRemovalRoot = `
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

module "net" {
  source = "./child"
}

resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`

const moduleRemovalChild = `
resource "aws_subnet" "this" {
  for_each = toset(["b"])

  vpc_id     = "vpc-00000000000000000"
  cidr_block = "10.0.0.0/24"
}
`

func moduleRemovalDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(moduleRemovalRoot), 0o600); err != nil {
		t.Fatalf("writing the root fixture: %v", err)
	}
	child := filepath.Join(dir, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatalf("creating the child module directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(child, "main.tf"), []byte(moduleRemovalChild), 0o600); err != nil {
		t.Fatalf("writing the child fixture: %v", err)
	}
	return dir
}

// loadModuleConfig is [loadConfig] with a module walker that actually loads
// the child, which the package's own loader refuses on purpose.
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

func classifyModuleFixture(t *testing.T, dir string, res discovery.Result) *Result {
	t.Helper()

	out, diags := Classify(context.Background(), Request{
		Estate: estateName,
		Config: loadModuleConfig(t, dir),
		Report: &res.Report, Orphans: res.Orphans,
	})
	if diags.HasErrors() {
		t.Fatalf("classification failed:\n%s", renderDiags(diags))
	}
	return out
}

func removalOrphan(t *testing.T, typeName, id, marker string) discovery.OwnedResource {
	t.Helper()
	o := orphan(typeName, id, "", marker)
	o.Addr, o.Addressable = discovery.UnescapeAddress(o.Normalized)
	if !o.Addressable {
		t.Fatalf("the test's own marker %q does not unescape to an address", marker)
	}
	o.Removal = true
	o.Swept = true
	return o
}

// TestBlockGoneIsReadFromTheOrphansOwnModule is issue #316's foreign-side
// twin. [classifier.removals] asked the ROOT module's ManagedResources
// whether an orphan's block still exists, with the module path stripped off
// the address it looked up - so for anything inside a module it answered a
// question about a different block, and BlockGone (and the sentence built
// from it, [removalWhy]) came out exactly backwards in both directions.
//
// The fixture is built so that the two directions cannot both be right by
// accident: aws_vpc.main exists only at the root, aws_subnet.this only in the
// child, and every orphan below sits inside module.net.
func TestBlockGoneIsReadFromTheOrphansOwnModule(t *testing.T) {
	dir := moduleRemovalDir(t)

	t.Run("block still declared in the child module", func(t *testing.T) {
		// module.net's aws_subnet.this block is still there; only the key "c"
		// is gone. The root module has no aws_subnet.this at all.
		res := classifyModuleFixture(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{removalOrphan(t, "aws_subnet", "subnet-c", "module.net.aws_subnet.this:c")}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}}})

		if len(res.Removals) != 1 {
			t.Fatalf("want one removal, got:\n%s", res)
		}
		rm := res.Removals[0]
		if rm.BlockGone {
			t.Errorf("the removal of %s is reported as a deleted block, but module.net still declares aws_subnet.this.\nWhy: %q",
				rm.Addr, rm.Why)
		}
	})

	t.Run("block declared only at the root", func(t *testing.T) {
		// The mirror image: aws_vpc.main exists at the root and NOT in the
		// child, so an orphan of module.net.aws_vpc.main is a deleted block.
		res := classifyModuleFixture(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{removalOrphan(t, "aws_vpc", "vpc-1", "module.net.aws_vpc.main")}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_vpc", 1)}}})

		if len(res.Removals) != 1 {
			t.Fatalf("want one removal, got:\n%s", res)
		}
		rm := res.Removals[0]
		if !rm.BlockGone {
			t.Errorf("the removal of %s is not reported as a deleted block, but module.net declares no aws_vpc.main - only the root does.\nWhy: %q",
				rm.Addr, rm.Why)
		}
	})

	t.Run("module call itself gone", func(t *testing.T) {
		// A module the configuration no longer calls at all. There is no
		// module config to consult, so the block is gone by construction.
		res := classifyModuleFixture(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{removalOrphan(t, "aws_subnet", "subnet-x", "module.deleted.aws_subnet.this:b")}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}}})

		if len(res.Removals) != 1 {
			t.Fatalf("want one removal, got:\n%s", res)
		}
		if !res.Removals[0].BlockGone {
			t.Errorf("the removal of %s is not reported as a deleted block, but nothing calls module.deleted",
				res.Removals[0].Addr)
		}
	})

	t.Run("root orphans are unchanged", func(t *testing.T) {
		res := classifyModuleFixture(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{
			removalOrphan(t, "aws_vpc", "vpc-root", "aws_vpc.main"),
			removalOrphan(t, "aws_subnet", "subnet-root", "aws_subnet.this:c"),
		}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_vpc", 1), scan("aws_subnet", 1)}}})

		if len(res.Removals) != 2 {
			t.Fatalf("want two removals, got:\n%s", res)
		}
		for _, rm := range res.Removals {
			switch rm.TypeName {
			case "aws_vpc":
				if rm.BlockGone {
					t.Errorf("the root's own aws_vpc.main block is declared, but its removal is reported as a deleted block")
				}
			case "aws_subnet":
				if !rm.BlockGone {
					t.Errorf("the root declares no aws_subnet.this, but its removal is not reported as a deleted block")
				}
			}
		}
	})
}
