// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The demo trees under examples/ (demo-terragrunt, demo-atmos) claim that a
// Terragrunt or Atmos user switches the binary to choudoufu and changes
// nothing else; a live block per unit is the one accepted addition
// (choudoufu#2231). Each tree has needed workarounds before, and each was
// really added and later removed. This lint fails if one comes back
// (choudoufu#2262, #2263, ruled: a workaround lint).
//
// What it cannot see: a new kind of workaround nobody has needed yet. It
// catches the regressions seen so far, and the README's "what differs" list
// and review stay the guard for the rest.

// demoWorkaround is one workaround a demo tree once carried: a pattern over
// the non-comment lines of the files whose extension is listed.
type demoWorkaround struct {
	name string
	exts []string
	re   *regexp.Regexp
}

// demoConfigExts are the files the lint reads. READMEs are left out: they
// name the workarounds in prose to say they are gone.
var demoConfigExts = []string{".hcl", ".tf", ".yaml", ".yml", ".json", ".mjs", ".sh"}

// demoCommonWorkarounds are the ones both demos carried: a composed root is
// checked apart (demoComposedRoots), since it is a module call rather than a
// word.
var demoCommonWorkarounds = []demoWorkaround{
	{
		name: "terraform_estate_outputs (units read each other through the tool's own wiring)",
		exts: []string{".tf", ".hcl", ".yaml", ".yml"},
		re:   regexp.MustCompile(`\bterraform_estate_outputs\b`),
	},
}

// terragruntDemoWorkarounds are the ones demo-terragrunt carried before
// choudoufu#2231: a generated backend in place of remote_state, and turning
// off Terragrunt's dependency reads from state.
var terragruntDemoWorkarounds = []demoWorkaround{
	{
		name: `generate "backend" in place of a remote_state block`,
		exts: []string{".hcl"},
		re:   regexp.MustCompile(`\bgenerate\s+"backend"`),
	},
	{
		name: "--no-dependency-fetch-output-from-state",
		exts: demoConfigExts,
		re:   regexp.MustCompile(`no-dependency-fetch-output-from-state`),
	},
	{
		name: "TG_NO_DEPENDENCY_FETCH_OUTPUT_FROM_STATE",
		exts: demoConfigExts,
		re:   regexp.MustCompile(`TG_NO_DEPENDENCY_FETCH_OUTPUT_FROM_STATE`),
	},
	{
		name: "dependency_fetch_output_from_state = false",
		exts: []string{".hcl"},
		re:   regexp.MustCompile(`\bdependency_fetch_output_from_state\s*=\s*false\b`),
	},
}

// demoCommentLine matches a line that is only a comment in HCL, YAML or JS.
var demoCommentLine = regexp.MustCompile(`^\s*(#|//)`)

// demoModuleSource matches a module call's local source.
var demoModuleSource = regexp.MustCompile(`(?s)module\s+"([^"]+)"\s*\{[^}]*?\bsource\s*=\s*"(\.{1,2}/[^"]*)"`)

// demoLiveBlock matches a live block inside a terraform block.
var demoLiveBlock = regexp.MustCompile(`(?m)^\s*live\s*\{`)

// demoFiles lists the tree's files with one of exts, skipping the provider
// cache and generated files a local run leaves behind.
func demoFiles(t *testing.T, root string, exts []string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".terraform", ".terragrunt-cache", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		for _, ext := range exts {
			if strings.HasSuffix(d.Name(), ext) {
				out = append(out, path)
				break
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	sort.Strings(out)
	return out
}

// demoCode returns a file's text with whole-line comments blanked, so a
// comment explaining why a workaround is gone does not trip the lint.
func demoCode(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if demoCommentLine.MatchString(l) {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// demoComposedRoots finds each module call whose local source is a directory
// holding a live block: a root that calls an estate as a module is a
// composed root, which the stock layouts do not have.
func demoComposedRoots(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	for _, f := range demoFiles(t, root, []string{".tf"}) {
		for _, m := range demoModuleSource.FindAllStringSubmatch(demoCode(t, f), -1) {
			target := filepath.Join(filepath.Dir(f), m[2])
			if st, err := os.Stat(target); err != nil || !st.IsDir() {
				continue
			}
			for _, tf := range demoFiles(t, target, []string{".tf"}) {
				if filepath.Dir(tf) != filepath.Clean(target) {
					continue
				}
				if demoLiveBlock.MatchString(demoCode(t, tf)) {
					rel, _ := filepath.Rel(root, f)
					found = append(found, rel+": module \""+m[1]+"\" calls the estate at "+m[2]+" (a composed root)")
					break
				}
			}
		}
	}
	return found
}

// lintDemoTree returns one finding per workaround found, naming the file.
func lintDemoTree(t *testing.T, root string, rules []demoWorkaround) []string {
	t.Helper()
	var found []string
	for _, r := range rules {
		for _, f := range demoFiles(t, root, r.exts) {
			if r.re.MatchString(demoCode(t, f)) {
				rel, _ := filepath.Rel(root, f)
				found = append(found, rel+": "+r.name)
			}
		}
	}
	return append(found, demoComposedRoots(t, root)...)
}

// demoMustContain fails unless the file's non-comment text matches re: the
// binary switch the README claims is the one change.
func demoMustContain(t *testing.T, path string, re *regexp.Regexp, what string) {
	t.Helper()
	if !re.MatchString(demoCode(t, path)) {
		t.Errorf("%s: %s is missing", path, what)
	}
}

const terragruntDemoDir = "../examples/demo-terragrunt"

// TestTerragruntDemoCarriesNoWorkaround is the lint over
// examples/demo-terragrunt, its scenario trees included.
func TestTerragruntDemoCarriesNoWorkaround(t *testing.T) {
	rules := append(append([]demoWorkaround{}, demoCommonWorkarounds...), terragruntDemoWorkarounds...)
	for _, f := range lintDemoTree(t, terragruntDemoDir, rules) {
		t.Errorf("examples/demo-terragrunt: %s", f)
	}

	rootHCL := filepath.Join(terragruntDemoDir, "root.hcl")
	demoMustContain(t, rootHCL, regexp.MustCompile(`(?m)^\s*terraform_binary\s*=\s*"choudoufu"`), `terraform_binary = "choudoufu"`)
	demoMustContain(t, rootHCL, regexp.MustCompile(`(?m)^\s*remote_state\s*\{`), "the stock remote_state block")

	// Three units, each with a terragrunt.hcl and a live block.
	for _, u := range []string{"network", "app", "edge"} {
		dir := filepath.Join(terragruntDemoDir, u)
		if _, err := os.Stat(filepath.Join(dir, "terragrunt.hcl")); err != nil {
			t.Errorf("unit %s: %v", u, err)
		}
		live := false
		for _, f := range demoFiles(t, dir, []string{".tf"}) {
			if filepath.Dir(f) == dir && demoLiveBlock.MatchString(demoCode(t, f)) {
				live = true
			}
		}
		if !live {
			t.Errorf("unit %s has no live block", u)
		}
	}
}

// TestDemoWorkaroundLintCatchesEachWorkaround proves each rule can fail: a
// tree holding one workaround yields a finding for it, and the same text in a
// comment yields none.
func TestDemoWorkaroundLintCatchesEachWorkaround(t *testing.T) {
	samples := map[string]struct {
		file, text string
		rules      []demoWorkaround
	}{
		"estate outputs":    {"main.tf", "data \"terraform_estate_outputs\" \"net\" {\n  estate = \"net\"\n}\n", demoCommonWorkarounds},
		"generated backend": {"root.hcl", "generate \"backend\" {\n  path = \"backend.tf\"\n}\n", terragruntDemoWorkarounds},
		"no-fetch flag":     {"run.sh", "terragrunt run --all --no-dependency-fetch-output-from-state -- plan\n", terragruntDemoWorkarounds},
		"no-fetch env":      {"ci.yml", "env:\n  TG_NO_DEPENDENCY_FETCH_OUTPUT_FROM_STATE: \"true\"\n", terragruntDemoWorkarounds},
		"no-fetch attr":     {"terragrunt.hcl", "dependency \"n\" {\n  config_path = \"../n\"\n  dependency_fetch_output_from_state = false\n}\n", terragruntDemoWorkarounds},
	}
	for name, s := range samples {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, s.file), []byte(s.text), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := lintDemoTree(t, dir, s.rules); len(got) != 1 {
				t.Errorf("want one finding, got %q", got)
			}
			var commented []string
			for _, l := range strings.Split(s.text, "\n") {
				commented = append(commented, "# "+l)
			}
			if err := os.WriteFile(filepath.Join(dir, s.file), []byte(strings.Join(commented, "\n")), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := lintDemoTree(t, dir, s.rules); len(got) != 0 {
				t.Errorf("a comment is not a workaround, got %q", got)
			}
		})
	}

	t.Run("composed root", func(t *testing.T) {
		dir := t.TempDir()
		for path, text := range map[string]string{
			"net/live.tf":   "terraform {\n  live {\n    estate = \"net\"\n  }\n}\n",
			"lib/main.tf":   "variable \"x\" {}\n",
			"root/main.tf":  "module \"net\" {\n  source = \"../net\"\n}\n",
			"other/main.tf": "module \"lib\" {\n  source = \"../lib\"\n}\n",
		} {
			p := filepath.Join(dir, path)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		got := lintDemoTree(t, dir, nil)
		if len(got) != 1 || !strings.HasPrefix(got[0], filepath.Join("root", "main.tf")) {
			t.Errorf("want one composed-root finding in root/main.tf (a plain local module is fine), got %q", got)
		}
	})
}
