// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "corpus-manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestManifestRequiresAnOrigin is the validation that keeps a corpus honest.
//
// An origin-less source would land in the artifact as an unlabelled row, and
// the whole purpose of the field is that a fixture this project wrote can
// never later be mistaken for a third-party configuration. Defaulting it
// would be worse than refusing it: the default would be a claim nobody made.
func TestManifestRequiresAnOrigin(t *testing.T) {
	path := writeManifest(t, `{"sources": [{"glob": "live/e2e/estates/*"}]}`)

	_, err := ReadManifest(path)
	if err == nil {
		t.Fatal("a source with no origin was accepted")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("error does not name the missing field: %s", err)
	}
}

// TestManifestRequiresACompletePin covers the other half: a fetched source
// has to carry everything needed to reproduce it, including both the tag and
// the commit that tag must resolve to.
func TestManifestRequiresACompletePin(t *testing.T) {
	path := writeManifest(t, `{"sources": [{
		"glob": ".corpus/vpc/examples/*",
		"origin": "terraform-aws-modules",
		"fetch": {"dir": ".corpus/vpc", "repo": "https://example.invalid/x.git", "tag": "v1.0.0"}
	}]}`)

	_, err := ReadManifest(path)
	if err == nil {
		t.Fatal("a fetch block with no commit was accepted, so the corpus could move under the artifact")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("error does not name what is missing: %s", err)
	}
}

// TestResolveSkipsDirectoriesWithNoConfiguration keeps empty directories out
// of the ranking. One counted as a configuration would read as something
// nothing refused, which overstates coverage in the denominator.
func TestResolveSkipsDirectoriesWithNoConfiguration(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"corpus/real", "corpus/empty", "corpus/docs"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "corpus/real/main.tf"), []byte("# config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "corpus/docs/README.md"), []byte("# not config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{Sources: []ManifestSource{{Glob: "corpus/*", Origin: "test"}}}
	entries, err := manifest.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 {
		t.Fatalf("resolved %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].Name != "corpus/real" {
		t.Errorf("resolved %q, want corpus/real", entries[0].Name)
	}
	if entries[0].Origin != "test" {
		t.Errorf("origin is %q, want test", entries[0].Origin)
	}
}

// TestHasConfigFilesMatchesTheLoaderSuffixes is issue #256 item 2: an
// earlier HasConfigFiles matched only ".tf" and ".tf.json" by hand, so a
// ".tofu"-only directory was invisible to the corpus - not refused, not
// counted, simply missing from every denominator. HasConfigFiles now defers
// to configs.IsEmptyDir instead of restating the suffix list, but that only
// guards against drift if something checks the two against an oracle
// neither of them is built from.
//
// The oracle here is configs.NewParser(nil).LoadConfigDir actually parsing a
// resource out of the file - the loader's real parse path, not a copy of its
// suffix constants (which are unexported and cannot be imported to compare
// against). If HasConfigFiles and the real loader ever disagree on whether a
// filename is configuration, this fails without anyone having to notice the
// mismatch by reading corpus numbers.
func TestHasConfigFilesMatchesTheLoaderSuffixes(t *testing.T) {
	const resourceHCL = `resource "null_resource" "x" {}`
	const resourceJSON = `{"resource": {"null_resource": {"x": {}}}}`

	cases := []struct {
		name     string
		filename string
		content  string
		loadable bool
	}{
		{"tf", "main.tf", resourceHCL, true},
		{"tf_json", "main.tf.json", resourceJSON, true},
		{"tofu", "main.tofu", resourceHCL, true},
		{"tofu_json", "main.tofu.json", resourceJSON, true},
		{"txt", "main.txt", resourceHCL, false},
		{"markdown", "README.md", "# not config\n", false},
		{"hidden_tf", ".main.tf", resourceHCL, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}

			parser := configs.NewParser(nil)
			mod, diags := parser.LoadConfigDir(dir, configs.RootModuleCallForTesting())
			if tc.loadable && diags.HasErrors() {
				t.Fatalf("test fixture is wrong: loader rejected %s: %s", tc.filename, diags)
			}
			loaderSees := mod != nil && len(mod.ManagedResources) == 1
			if loaderSees != tc.loadable {
				t.Fatalf("test fixture is wrong: loader recognizes %s = %v, want %v", tc.filename, loaderSees, tc.loadable)
			}

			if got := HasConfigFiles(dir); got != tc.loadable {
				t.Errorf("HasConfigFiles(%s) = %v, want %v (the real loader recognizes it: %v)",
					tc.filename, got, tc.loadable, loaderSees)
			}
		})
	}
}

// TestShippedManifestIsValid checks the manifest this repository actually
// ships, so a hand edit to it fails here rather than at the next corpus run.
func TestShippedManifestIsValid(t *testing.T) {
	manifest, err := ReadManifest(filepath.Join("..", "..", "..", "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatalf("the shipped corpus manifest is invalid: %s", err)
	}

	var thirdParty int
	root := filepath.Join("..", "..", "..")
	for _, source := range manifest.Sources {
		for _, vf := range source.VarFiles {
			// #183: a var_files entry that does not resolve to a real file
			// would silently measure the estate bare while the artifact
			// claimed otherwise - readVarsFile fails open (it skips what it
			// cannot read), so nothing else in the pipeline would catch a
			// typo here.
			if _, err := os.Stat(filepath.Join(root, vf)); err != nil {
				t.Errorf("%s: var_files %q does not exist: %s", source.Glob, vf, err)
			}
		}
		if l := source.VarFileLayout; l != nil {
			// VariablesDir itself is not required to exist here: it
			// typically sits under .corpus/, materialized only after "just
			// corpus-fetch" runs, which this test does not require. What
			// must hold unconditionally is that the rule names both of its
			// fields - see [ManifestSource.VarFileLayout]'s doc comment.
			if l.VariablesDir == "" || l.Env == "" {
				t.Errorf("%s: var_file_layout is missing variables_dir or env", source.Glob)
			}
		}
		if source.Fetch == nil {
			continue
		}
		thirdParty++
		if len(source.Fetch.Commit) != 40 {
			t.Errorf("%s: commit %q is not a full SHA; a short or partial pin is not a pin",
				source.Glob, source.Fetch.Commit)
		}
		if !strings.HasPrefix(source.Fetch.Repo, "https://") {
			t.Errorf("%s: repo %q is not an https URL", source.Glob, source.Fetch.Repo)
		}
	}

	// The corpus exists to measure configurations this project did not
	// write. A manifest that pins none of them has quietly reverted to
	// measuring the fixtures, which is the state GitHub issue #102 was
	// filed to get out of.
	if thirdParty == 0 {
		t.Error("the shipped manifest pins no third-party sources, so the corpus measures only this project's own fixtures")
	}
}
