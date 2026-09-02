// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// package moved_test, not moved, because this test imports
// internal/live/lint, which itself imports internal/live/moved - an
// internal test file here would be a compiler-rejected import cycle. The
// external test package is the ordinary way around that, and lets this test
// prove the two packages agree rather than merely assert it in a comment.
package moved_test

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
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/moved"
)

// TestMovedThroughCountedModuleClearsLintInTheSamePass is issue #330's proof.
//
// internal/live/mv's checkAddresses (issue #317) admitted a rename through a
// count-keyed module step on the strength of an argument about a DIFFERENT
// command's ordering: by the time an address reaches checkAddresses,
// internal/command/live_mv.go has already run lint.CheckWith, so
// RuleChildModule's static/no-count.index-leak proof is already in hand.
//
// [moved.Honourable] is not downstream of a separate command step the way
// checkAddresses is - it IS one of lint's own rules (checkMovedBlocks,
// RuleMovedBlock), called from the very same internal/live/lint/lint.go
// checkConfig walk that calls checkChildModules (RuleChildModule) over the
// same module, immediately before it. Both are SeverityError, both feed the
// same []Issue slice CheckWith returns, and every caller that gates on it
// (live_plan.go, live_mode.go, via lint.HasErrors) treats the two rules'
// findings as one combined verdict. So a count-keyed module call unsafe
// enough to matter - a non-static count, or one whose own arguments leak
// count.index - never produces a clean lint result regardless of what
// Honourable decides, and a moved block whose destination module IS safe
// gets no help from a rule that used to refuse it anyway on a premise issue
// #195 already retired.
//
// This test builds the shape directly: a module called with a literal,
// static count and no count.index leak (the admitted shape, issue #195),
// wrapping a resource a moved block's destination endpoint reaches through
// it. It asserts three things a defect could break independently:
//
//  1. lint.CheckContext reports nothing at all for this configuration -
//     neither RuleChildModule (the module call is safe) nor RuleMovedBlock
//     (the moved block through it is honoured).
//  2. moved.Honoured includes the statement.
//  3. moved.Origins computes the real alias discovery would index the live
//     resource under.
func TestMovedThroughCountedModuleClearsLintInTheSamePass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.tf", `
module "counted" {
  source = "./counted"
  count  = 1
}

# aws_sqs_queue.solo is deliberately NOT declared: the ordinary shape of a
# moved block's "from" endpoint is a retired address the configuration no
# longer has a block for (declaresSubject's refusal exists for the opposite
# case - a "from" address still declared - which is not this test's
# concern).
moved {
  from = aws_sqs_queue.solo
  to   = module.counted[0].aws_sqs_queue.doi
}
`)
	writeFile(t, dir, "counted/main.tf", `
resource "aws_sqs_queue" "doi" {
  name = "doi"
}
`)

	cfg := loadConfigDir(t, dir)

	if issues := lint.CheckContext(t.Context(), cfg); len(issues) != 0 {
		t.Fatalf("lint.CheckContext() reported %d issues for a statically count-keyed module and the moved block through it, want none: %v", len(issues), issues)
	}

	declared := mustAddr(t, "module.counted[0].aws_sqs_queue.doi")
	stmts := moved.Honoured(cfg)
	if len(stmts) != 1 {
		t.Fatalf("moved.Honoured() returned %d statements, want 1: %v", len(stmts), stmts)
	}

	origins := moved.Origins(stmts, declared)
	want := "aws_sqs_queue.solo"
	if len(origins) != 1 || origins[0].String() != want {
		t.Fatalf("moved.Origins(%s) = %v, want [%s]", declared, origins, want)
	}
}

// writeFile writes one file of a config tree under dir, creating parent
// directories as needed - the same helper internal/live/mv's own integration
// test (issue #317) uses for the same purpose.
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

// loadConfigDir loads a local config tree the same way this package's own
// internal tests (moved_test.go, package moved) do - local module sources
// only, walked directly rather than through a ".terraform/modules" manifest.
// Duplicated rather than exported, because moved_test.go's copy is
// unexported and lives in package moved, which this external test package
// cannot reach.
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

// mustAddr parses a resource-instance address string using the same grammar
// identity.Resolve's own addresses use.
func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q as an absolute resource instance address: %s", s, diags.Err())
	}
	return addr
}
