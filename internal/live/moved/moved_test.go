// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package moved

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// TestOriginsCoversEveryCorpusShape drives the four shapes the 105-config
// corpus actually contains through one estate: a plain rename, a
// root-to-module refactor, a module rename, and a cross-module move. The
// point is that none of them needs a case in this package - they are all one
// structural rewrite through addrs.
func TestOriginsCoversEveryCorpusShape(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "estate"))
	stmts := Honoured(cfg)
	if len(stmts) != 5 {
		t.Fatalf("Honoured() returned %d statements, want all 5: %s", len(stmts), statementStrings(stmts))
	}

	tests := []struct {
		name     string
		declared string
		want     []string
	}{
		{
			name:     "plain rename",
			declared: "aws_s3_bucket.new",
			want:     []string{"aws_s3_bucket.old"},
		},
		{
			name:     "count-expanded destination keeps its index",
			declared: "aws_s3_bucket_versioning.this[0]",
			want:     []string{"aws_s3_bucket_versioning.legacy[0]"},
		},
		{
			name:     "root to module",
			declared: "module.queues.aws_sqs_queue.doi",
			want:     []string{"aws_sqs_queue.doi"},
		},
		{
			// The module rename reaches every resource beneath it with no
			// per-resource statement, and the cross-module move reaches one
			// of them by a second route. Both origins are real: a live queue
			// could be carrying either.
			name:     "module rename and cross-module move compose",
			declared: "module.renamed.aws_sqs_queue.stray",
			want:     []string{"module.gone.aws_sqs_queue.stray", "module.old_name.aws_sqs_queue.stray"},
		},
		{
			name:     "module rename alone",
			declared: "module.renamed.aws_sqs_queue.doi",
			want:     []string{"module.old_name.aws_sqs_queue.doi"},
		},
		{
			name:     "an address no statement mentions has no origins",
			declared: "aws_s3_bucket.untouched",
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := originStrings(Origins(stmts, mustAddr(t, test.declared)))
			sort.Strings(got)
			want := append([]string(nil), test.want...)
			sort.Strings(want)
			if !equal(got, want) {
				t.Errorf("Origins(%s) = %v, want %v", test.declared, got, want)
			}
		})
	}
}

// TestOriginsFollowsChains is the case that makes a published module's
// permanently-shipped moved blocks work across more than one upgrade: an
// estate that has not run since before the first rename still binds.
func TestOriginsFollowsChains(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "chain"))
	stmts := Honoured(cfg)

	got := originStrings(Origins(stmts, mustAddr(t, "aws_s3_bucket.c")))
	sort.Strings(got)
	want := []string{"aws_s3_bucket.a", "aws_s3_bucket.b"}
	if !equal(got, want) {
		t.Errorf("Origins() = %v, want %v", got, want)
	}
}

// TestOriginsIsIdempotentOnAnAppliedMove is the requirement a moved block
// living permanently inside a published module turns on: once the marker has
// been rewritten, the block must be a no-op rather than an error or a second
// rewrite. Nothing here has to check for that explicitly - the destination's
// own address is never in its own origin set, so a live resource already
// carrying it binds canonically and the alias matches nothing.
func TestOriginsIsIdempotentOnAnAppliedMove(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "chain"))
	stmts := Honoured(cfg)

	declared := mustAddr(t, "aws_s3_bucket.c")
	for _, origin := range Origins(stmts, declared) {
		if origin.Equal(declared) {
			t.Fatalf("Origins() included the declared address %s itself, which would make an applied move re-apply", declared)
		}
	}
}

// TestHonourableRefusals pins the residual refusals, and - the direction that
// matters - that they are the only ones.
func TestHonourableRefusals(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "refused"))
	stmts := Statements(cfg)
	if len(stmts) != 2 {
		t.Fatalf("Statements() returned %d, want 2", len(stmts))
	}
	if got := Honoured(cfg); len(got) != 0 {
		t.Fatalf("Honoured() returned %d statements, want none: %s", len(got), statementStrings(got))
	}

	wantReasons := []string{
		"the address it moves from is still declared",
		"its two endpoints name different resource types",
	}
	for i, stmt := range stmts {
		reason, ok := Honourable(cfg, stmt)
		if ok {
			t.Errorf("statement %d (%s -> %s) was honoured, want refused", i, stmt.From, stmt.To)
			continue
		}
		if !strings.Contains(reason, wantReasons[i]) {
			t.Errorf("statement %d reason = %q, want it to mention %q", i, reason, wantReasons[i])
		}
	}
}

// TestOriginsIgnoresUnhonouredStatements is the safety direction stated as a
// test: a refused statement must contribute no alias, or lint would be
// refusing a block whose old address discovery had nonetheless indexed - the
// harmless disagreement - while the reverse would leave an object claimed by
// nobody.
func TestOriginsIgnoresUnhonouredStatements(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "refused"))
	stmts := Honoured(cfg)

	for _, declared := range []string{"aws_s3_bucket.target", "aws_s3_bucket_versioning.other_type"} {
		if got := Origins(stmts, mustAddr(t, declared)); len(got) != 0 {
			t.Errorf("Origins(%s) = %v, want none", declared, originStrings(got))
		}
	}
}

// TestStatementsIsDeterministic guards the one thing a map-backed module tree
// threatens: two runs over one configuration must produce the same order, or
// the alias index and every diagnostic built from it shuffle.
func TestStatementsIsDeterministic(t *testing.T) {
	cfg := loadConfigDir(t, filepath.Join("testdata", "estate"))
	first := statementStrings(Statements(cfg))
	for i := 0; i < 20; i++ {
		if got := statementStrings(Statements(cfg)); !equal(got, first) {
			t.Fatalf("iteration %d gave %v, want %v", i, got, first)
		}
	}
}

// TestStatementsInSurvivesAnUndecodableBlock is the difference from
// refactoring.FindMoveStatements, which panics: lint runs on configurations
// that have not been accepted, so a block whose endpoints the decoder rejected
// has to be skipped rather than crash the run.
func TestStatementsInSurvivesAnUndecodableBlock(t *testing.T) {
	mod := &configs.Module{Moved: []*configs.Moved{
		nil,
		{},
		{From: &addrs.MoveEndpoint{}},
	}}
	if got := StatementsIn(mod, addrs.RootModule); len(got) != 0 {
		t.Fatalf("StatementsIn() = %d statements, want 0", len(got))
	}
}

// ---------------------------------------------------------------------------

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", s, diags.Err())
	}
	return addr
}

func originStrings(in []addrs.AbsResourceInstance) []string {
	out := make([]string, 0, len(in))
	for _, addr := range in {
		out = append(out, addr.String())
	}
	return out
}

func statementStrings(in []Statement) []string {
	out := make([]string, 0, len(in))
	for _, stmt := range in {
		out = append(out, stmt.From.String()+" -> "+stmt.To.String())
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
