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
		if source.VarFile != "" {
			// #183: a var_file that does not resolve to a real file would
			// silently measure the estate bare while the artifact claimed
			// otherwise - readVarsFile fails open (it skips what it cannot
			// read), so nothing else in the pipeline would catch a typo
			// here.
			if _, err := os.Stat(filepath.Join(root, source.VarFile)); err != nil {
				t.Errorf("%s: var_file %q does not exist: %s", source.Glob, source.VarFile, err)
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
