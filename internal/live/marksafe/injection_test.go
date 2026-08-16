// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package marksafe_test

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the dynamic half of issue #240's closure. The scanner next
// door proves that no call site READS a value nothing shows is unmarked;
// this runs the analysis with marks injected everywhere a configuration can
// put one and asserts the run does not crash.
//
// The two halves catch different things. The scanner sees every path,
// including ones no fixture reaches, but only as far as a syntactic proof
// goes. This sees only the paths the fixtures reach, but sees them exactly
// as a user would. Three of the four sites this work found came out of this
// sweep over fixtures written for entirely other reasons - which is the
// point of using them: they are an external source of shapes, not a test
// that agrees with its own rule.

// injectMarks makes every value a configuration can mark, marked: every
// input variable becomes sensitive, and every local value is wrapped in
// sensitive(). Returns how many injections it made.
//
// Those two are the whole config-level vocabulary for producing a mark. A
// mark propagates through every derived expression, so marking the roots
// marks everything downstream of them.
func injectMarks(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(path); ext != ".tf" && ext != ".tofu" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		file, diags := hclwrite.ParseConfig(src, path, hcl.InitialPos)
		if diags.HasErrors() || file == nil {
			// A fixture the writer cannot parse is one this sweep cannot
			// rewrite; check.Dir still gets the original text below.
			return nil
		}
		changed := false
		for _, blk := range file.Body().Blocks() {
			switch blk.Type() {
			case "variable":
				blk.Body().SetAttributeValue("sensitive", cty.True)
				changed = true
				n++
			case "locals":
				for name, attr := range blk.Body().Attributes() {
					wrapped := hclwrite.Tokens{
						{Type: hclsyntax.TokenIdent, Bytes: []byte("sensitive")},
						{Type: hclsyntax.TokenOParen, Bytes: []byte("(")},
					}
					wrapped = append(wrapped, attr.Expr().BuildTokens(nil)...)
					wrapped = append(wrapped, &hclwrite.Token{Type: hclsyntax.TokenCParen, Bytes: []byte(")")})
					blk.Body().SetAttributeRaw(name, wrapped)
					changed = true
					n++
				}
			}
		}
		if changed {
			return os.WriteFile(path, file.Bytes(), 0o644)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("injecting marks into %s: %v", dir, err)
	}
	return n
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".terraform" {
				return fs.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}

// configDirs are every directory under root holding a .tf or .tofu file.
// Module subdirectories are included as roots of their own, which costs
// nothing and analyzes a few more shapes.
func configDirs(t *testing.T, root string) []string {
	t.Helper()
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if ext := filepath.Ext(p); ext == ".tf" || ext == ".tofu" {
			seen[filepath.Dir(p)] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// analyzeRecovering runs the analysis and reports a panic instead of
// letting it out. A panic here is the bug: check.Dir is the entry point both
// front ends call, so a crash in it is a crash of "choudoufu live-plan".
func analyzeRecovering(dir string, schemas map[string]providers.Schema) (report check.Report, panicked string, stack string) {
	defer func() {
		if r := recover(); r != nil {
			panicked = fmt.Sprint(r)
			stack = string(debug.Stack())
		}
	}()
	report = check.Dir(context.Background(), dir, check.Context{Schemas: schemas})
	return
}

// TestMarkedFixturesDoNotPanic is the property: take every configuration
// fixture in internal/live, mark everything a configuration can mark, and
// assert the analysis refuses rather than crashes.
//
// The fixtures were written for other reasons, which is what makes this an
// external source of truth rather than a test agreeing with its own rule,
// and it widens by itself: a fixture added for the next expression
// decomposition path is swept the day it lands.
func TestMarkedFixturesDoNotPanic(t *testing.T) {
	dirs := configDirs(t, "..")
	tmp := t.TempDir()

	injections := 0
	crashed := 0
	for i, dir := range dirs {
		work := filepath.Join(tmp, fmt.Sprintf("c%04d", i))
		copyTree(t, dir, work)
		injections += injectMarks(t, work)

		_, panicked, stack := analyzeRecovering(work, nil)
		if panicked == "" {
			continue
		}
		crashed++
		t.Errorf("%s crashes the analysis once its variables and locals are sensitive: %s\n%s",
			dir, panicked, forkFrames(stack))
	}
	t.Logf("swept %d configuration directories with %d mark injections, %d crashed", len(dirs), injections, crashed)
	if injections == 0 {
		t.Error("no marks were injected anywhere; the injection has stopped working and this test proves nothing")
	}
}

// TestMarkedRegressionFixturesRefuse pins the eight sites this work fixed.
// Each fixture reaches one of them, and asserting the diagnostic - not
// merely the absence of a panic - is what proves the path is still reached:
// a later change that stops resolving these shapes at all would leave a
// no-panic assertion green while covering nothing.
func TestMarkedRegressionFixturesRefuse(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata", entry.Name())
			report, panicked, stack := analyzeRecovering(dir, schemaFromConfig(t, dir))
			if panicked != "" {
				t.Fatalf("crashed: %s\n%s", panicked, forkFrames(stack))
			}
			want, err := os.ReadFile(filepath.Join(dir, "want-summary"))
			if err != nil {
				// No want-summary: the fixture asserts only that the run
				// survives, because the site it covers reports "cannot
				// check" rather than a diagnostic.
				return
			}
			wanted := strings.TrimSpace(string(want))
			for _, f := range append(append([]check.Finding{}, report.Findings...), report.Warnings...) {
				if f.ID == wanted || f.Title == wanted {
					return
				}
			}
			var got []string
			for _, f := range append(append([]check.Finding{}, report.Findings...), report.Warnings...) {
				got = append(got, f.ID)
			}
			t.Errorf("no finding %q; got %v.\nThe fixture no longer reaches the site it was written for.", wanted, got)
		})
	}
}

// schemaFromConfig builds a provider schema out of the configuration's own
// resource blocks: every argument a block writes becomes an optional
// attribute of that type.
//
// Two of the fixtures need one, because resolver.siblingLiteralExpr only
// applies to an attribute the schema knows and does not mark Computed. It
// is derived from the fixture rather than hand-written per type so that
// adding a fixture needs no second edit here.
func schemaFromConfig(t *testing.T, dir string) map[string]providers.Schema {
	t.Helper()
	load := check.Load(context.Background(), dir)
	if load.Config == nil {
		return nil
	}
	out := map[string]providers.Schema{}
	var walk func(cfg *configs.Config)
	walk = func(cfg *configs.Config) {
		if cfg == nil || cfg.Module == nil {
			return
		}
		for _, rc := range cfg.Module.ManagedResources {
			body, ok := rc.Config.(*hclsyntax.Body)
			if !ok {
				continue
			}
			schema, seen := out[rc.Type]
			if !seen {
				schema = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{}}}
			}
			for name := range body.Attributes {
				if _, have := schema.Block.Attributes[name]; have {
					continue
				}
				schema.Block.Attributes[name] = &configschema.Attribute{
					Type:     cty.DynamicPseudoType,
					Optional: true,
				}
			}
			out[rc.Type] = schema
		}
		for _, child := range cfg.Children {
			walk(child)
		}
	}
	walk(load.Config)
	return out
}

// forkFrames trims a stack to the frames a reader needs: this fork's own
// and cty's.
func forkFrames(stack string) string {
	var keep []string
	for _, line := range strings.Split(stack, "\n") {
		if strings.Contains(line, "choudoufu/internal") || strings.Contains(line, "go-cty") {
			keep = append(keep, "    "+strings.TrimSpace(line))
		}
		if len(keep) >= 16 {
			break
		}
	}
	return strings.Join(keep, "\n")
}
