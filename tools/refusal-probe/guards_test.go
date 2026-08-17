// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
)

// These are the guards that stop the probe reporting a number about something
// other than what the reader will assume it measured. Two of them were found
// the hard way on 2026-08-16: a sweep in a fresh worktree reported "entries
// 31" against a manifest describing 250, with exit 0 and no other output; and
// -diff compared two sweeps of two different trees without a word, because
// the only tree field it could have compared defaults to "." on both sides.
//
// Each test below breaks one guard's precondition deliberately and asserts
// the refusal fires and says something a reader can act on. The pair that
// matters most are the two negative ones - shadowed sources and a symlinked
// root - which pin the cases the guards must NOT refuse, since a guard that
// refuses working configurations is its own defect class here.

// writeManifest puts a manifest at root/live/corpus-manifest.json.
func writeManifest(t *testing.T, root string, sources ...map[string]any) string {
	t.Helper()
	dir := filepath.Join(root, "live")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"sources": sources})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "corpus-manifest.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return "live/corpus-manifest.json"
}

// writeConfig makes a directory the manifest resolver will count.
func writeConfig(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("resource \"null_resource\" \"a\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readManifestAt(t *testing.T, root, rel string) (sources []corpusSource, problems []string) {
	t.Helper()
	m, err := check.ReadManifest(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return corpusState(root, m)
}

func TestCorpusStateNamesASourceThatIsNotOnDisk(t *testing.T) {
	root := t.TempDir()
	rel := writeManifest(t,
		root,
		map[string]any{"glob": "here/*", "origin": "in-repo fixture"},
		map[string]any{"glob": "gone/*", "origin": "terraform-aws-modules"},
	)
	writeConfig(t, filepath.Join(root, "here", "one"))

	sources, problems := readManifestAt(t, root, rel)
	if len(sources) != 2 {
		t.Fatalf("accounted %d sources, manifest has 2", len(sources))
	}
	if sources[0].Matched != 1 || sources[0].Entries != 1 {
		t.Errorf("present source: matched %d entries %d, want 1 and 1", sources[0].Matched, sources[0].Entries)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "gone/*") {
		t.Fatalf("problems = %v; the absent source must be named, since the whole failure is that its absence was invisible", problems)
	}

	err := corpusProblemRefusal(root, sources, problems)
	for _, want := range []string{"gone/*", "found 1 of the manifest's 2 sources", "corpus-fetch", "-allow-partial-corpus"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q; it must say what it found, what it expected and how to fix it.\ngot:\n%s", want, err)
		}
	}
}

// TestCorpusStateAcceptsAShadowedSource is the negative case. A source whose
// directories an earlier source already claimed contributes zero entries, and
// that is a manifest property, not a missing corpus - refusing it would be
// the "rule that refused working configurations" shape.
func TestCorpusStateAcceptsAShadowedSource(t *testing.T) {
	root := t.TempDir()
	rel := writeManifest(t,
		root,
		map[string]any{"glob": "shared/*", "origin": "first"},
		map[string]any{"glob": "shared/one", "origin": "second"},
	)
	writeConfig(t, filepath.Join(root, "shared", "one"))

	sources, problems := readManifestAt(t, root, rel)
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none: the second source is shadowed by the first, which is not a missing corpus", problems)
	}
	if sources[1].Matched != 1 || sources[1].Entries != 0 {
		t.Errorf("shadowed source: matched %d entries %d, want 1 and 0 - the pair is what tells shadowed apart from absent",
			sources[1].Matched, sources[1].Entries)
	}
}

func TestCorpusStateNamesACheckoutTheManifestDoesNotPin(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	root := t.TempDir()
	repo := filepath.Join(root, "corpus", "thing")
	writeConfig(t, filepath.Join(repo, "examples", "one"))
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-qm", "corpus"},
	} {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	rel := writeManifest(t, root, map[string]any{
		"glob":   "corpus/thing/examples/*",
		"origin": "terraform-aws-modules",
		"fetch": map[string]any{
			"dir":    "corpus/thing",
			"repo":   "https://example.invalid/thing",
			"commit": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		},
	})

	sources, problems := readManifestAt(t, root, rel)
	if len(problems) != 1 || !strings.Contains(problems[0], "checkout drift") {
		t.Fatalf("problems = %v, want a checkout-drift problem: the working copy is at a commit the manifest does not pin, "+
			"so the sweep is of configurations nobody wrote down", problems)
	}
	if sources[0].Checkout == "" || sources[0].Checkout == sources[0].Pin {
		t.Errorf("checkout %q pin %q: the sweep must record both, or a later reader cannot tell which corpus it was",
			sources[0].Checkout, sources[0].Pin)
	}
}

func TestSweepRefusesAPartialCorpusAndProceedsWhenTold(t *testing.T) {
	root := t.TempDir()
	rel := writeManifest(t,
		root,
		map[string]any{"glob": "here/*", "origin": "in-repo fixture"},
		map[string]any{"glob": "gone/*", "origin": "terraform-aws-modules"},
	)
	writeConfig(t, filepath.Join(root, "here", "one"))

	if _, err := sweep(sweepOptions{manifest: rel, root: root}); err == nil {
		t.Fatal("swept a partial corpus and returned a number; that is the defect, not the fix")
	}

	r, err := sweep(sweepOptions{manifest: rel, root: root, allowPartial: true})
	if err != nil {
		t.Fatalf("-allow-partial-corpus must still measure: %v", err)
	}
	if len(r.CorpusProblems) != 1 {
		t.Errorf("corpus_problems = %v, want the one missing source recorded; -diff reads this field to refuse the comparison",
			r.CorpusProblems)
	}
	if r.Totals.Entries != 1 {
		t.Errorf("entries = %d, want 1", r.Totals.Entries)
	}
	if r.ProbeVersion != probeVersion || r.RootPath == "" || r.ManifestSHA == "" {
		t.Errorf("a sweep must record its own format, tree and manifest digest; got version %d root %q sha %q",
			r.ProbeVersion, r.RootPath, r.ManifestSHA)
	}
}

func TestSweepRefusesAnEntryFilterThatMatchesNothing(t *testing.T) {
	root := t.TempDir()
	rel := writeManifest(t, root, map[string]any{"glob": "here/*", "origin": "in-repo fixture"})
	writeConfig(t, filepath.Join(root, "here", "one"))

	_, err := sweep(sweepOptions{manifest: rel, root: root, only: "not-a-thing"})
	if err == nil {
		t.Fatal("an -entry filter matching nothing produced a summary of nothing, which reads as a configuration that refuses nothing")
	}
	if !strings.Contains(err.Error(), "not-a-thing") {
		t.Errorf("the refusal must quote the filter that matched nothing; got: %v", err)
	}
}

// TestRealPathResolvesSymlinks is the other negative case, and the reason the
// root guard compares [run.RootPath] rather than the raw -root string.
// /Users/alex/checkouts is a symlink to /Users/alex/Documents/checkouts on
// the machine this project is developed on - the same trap behind `env -u
// PWD` - so two sweeps of one tree can spell its root two ways. Refusing
// those would be a guard refusing working configurations.
func TestRealPathResolvesSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if realPath(link) != realPath(target) {
		t.Errorf("realPath(%q) = %q and realPath(%q) = %q; two spellings of one tree must compare equal, "+
			"or the root guard refuses a legitimate before/after pair", link, realPath(link), target, realPath(target))
	}
}

// full is a sweep that passes every precondition, as the base for mutating
// exactly one input per case below.
func full() *run {
	return &run{
		ProbeVersion: probeVersion,
		Manifest:     "live/corpus-manifest.json",
		Root:         ".",
		RootPath:     "/tree",
		Commit:       "abc123",
		ManifestSHA:  "aaaa",
		Entries: []entry{
			{Name: "a", Sites: 3},
			{Name: "b", Sites: 4},
		},
		Totals: totals{Entries: 2, Sites: 7},
	}
}

func TestDiffPreconditions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*run)
		wantErr string
	}{
		{"identical", func(*run) {}, ""},
		{"older format", func(r *run) { r.ProbeVersion = 1 }, "format"},
		{"another manifest path", func(r *run) { r.Manifest = "live/other.json" }, "different manifests"},
		{"the same manifest with other contents", func(r *run) { r.ManifestSHA = "bbbb" }, "contents differ"},
		{"schemas on one side", func(r *run) { r.Schemas = true }, "schemas and the other does not"},
		{"another tree", func(r *run) { r.RootPath = "/other-tree" }, "two different trees"},
		{"a corpus problem on one side", func(r *run) { r.CorpusProblems = []string{"no configurations: x"} }, "different corpora"},
		{"another set of entries", func(r *run) {
			r.Entries = append(r.Entries, entry{Name: "c"})
			r.Totals.Entries = 3
		}, "different sets of configurations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := full(), full()
			tc.mutate(after)
			err := diffPreconditions("before.json", before, "after.json", after)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused a comparison that is sound: %v", err)
			case tc.wantErr == "":
				return
			case err == nil:
				t.Fatalf("compared two sweeps differing in %s and said nothing; the delta would carry that difference "+
					"wearing the change's name", tc.name)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("refusal does not mention %q, so a reader cannot tell what to re-run:\n%v", tc.wantErr, err)
			}
		})
	}
}

// TestDiffPreconditionsOnSchemaBackedRuns keeps the provider release out of
// the delta. Two sweeps taken against two AWS provider versions differ by the
// provider, whatever else changed between them.
func TestDiffPreconditionsOnSchemaBackedRuns(t *testing.T) {
	schemaRun := func() *run {
		r := full()
		r.Schemas = true
		r.PinSource = "hashicorp/aws"
		r.PinVersion = "6.59.0"
		return r
	}
	for _, tc := range []struct {
		name    string
		mutate  func(*run)
		wantErr string
	}{
		{"identical", func(*run) {}, ""},
		{"another provider version", func(r *run) { r.PinVersion = "6.58.0" }, "different versions"},
		{"another provider", func(r *run) { r.PinSource = "hashicorp/google" }, "different providers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := schemaRun(), schemaRun()
			tc.mutate(after)
			err := diffPreconditions("before.json", before, "after.json", after)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused a sound comparison: %v", err)
			case tc.wantErr == "":
			case err == nil:
				t.Fatalf("compared sweeps differing in %s and said nothing", tc.name)
			case !strings.Contains(err.Error(), tc.wantErr):
				t.Fatalf("refusal does not mention %q:\n%v", tc.wantErr, err)
			}
		})
	}
}

// TestEntryDiffNamesBothSides matters because -diff's per-entry section
// silently skips a name it cannot find on both sides: a 250-entry sweep
// against a 31-entry one used to print a delta over the intersection and
// totals over the union, which reads as an enormous win.
func TestEntryDiffNamesBothSides(t *testing.T) {
	before, after := full(), full()
	after.Entries = []entry{{Name: "b"}, {Name: "c"}}
	onlyBefore, onlyAfter := entryDiff(before, after)
	if len(onlyBefore) != 1 || onlyBefore[0] != "a" {
		t.Errorf("onlyBefore = %v, want [a]", onlyBefore)
	}
	if len(onlyAfter) != 1 || onlyAfter[0] != "c" {
		t.Errorf("onlyAfter = %v, want [c]", onlyAfter)
	}
}
