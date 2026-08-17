// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/check"
)

// moduleSourceLiteral matches a literal source argument in a corpus .tf
// file. It over-matches deliberately - a provider's source argument looks
// the same - because [gitCloneURL] discards anything that is not a git
// module source anyway, and under-matching would let an unpinned repository
// through.
var moduleSourceLiteral = regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]+)"`)

// TestGitCloneURLSeparatesGoGetterFromRegistry is the trap this whole
// mechanism turns on. Both source kinds end up as a git clone, so the only
// thing that can keep a rewrite off registry traffic is the shape of the URL
// each produces. Measured against registry.opentofu.org on 2026-08-16, a
// module download answers with
// "git::https://github.com/<org>/<repo>?ref=<commit>" and no ".git", while
// go-getter's GitHub detector always appends ".git".
//
// If this ever stops holding, a rewrite keyed on the ".git" spelling starts
// capturing registry installs, and this test is what says so.
func TestGitCloneURLSeparatesGoGetterFromRegistry(t *testing.T) {
	for _, tc := range []struct {
		source string
		want   string
	}{
		// What a corpus configuration writes.
		{"github.com/alphagov/terraform-govuk-tfe-workspacer", "https://github.com/alphagov/terraform-govuk-tfe-workspacer.git"},
		{"github.com/ministryofjustice/cloud-platform-terraform-global-resources-auth0?ref=2.1.6", "https://github.com/ministryofjustice/cloud-platform-terraform-global-resources-auth0.git"},
		{"github.com/terraform-google-modules/terraform-google-cloud-storage//modules/simple_bucket?ref=v11.1.2", "https://github.com/terraform-google-modules/terraform-google-cloud-storage.git"},

		// What the OpenTofu registry hands back for a module download.
		// No ".git", so no rewrite keyed on the ".git" spelling can reach
		// it - even for a repository this manifest pins.
		{"git::https://github.com/terraform-aws-modules/terraform-aws-vpc?ref=a0307d4d1807de60b3868b96ef1b369808289157", "https://github.com/terraform-aws-modules/terraform-aws-vpc"},
		{"git::https://github.com/alphagov/terraform-govuk-tfe-workspacer?ref=b2d87762eeae6ca07a6f58288789584808b78b02", "https://github.com/alphagov/terraform-govuk-tfe-workspacer"},

		// Not git at all.
		{"terraform-aws-modules/vpc/aws", ""},
		{"./local", ""},
	} {
		if got := gitCloneURL(tc.source); got != tc.want {
			t.Errorf("gitCloneURL(%q) = %q, want %q", tc.source, got, tc.want)
		}
	}

	// The registry spelling of a PINNED repository must not equal the
	// mirror's key, or a registry install of that module would be served
	// the mirror's frozen commit instead of the version it asked for.
	pins, err := readModuleSources(filepath.Join("..", "..", "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) == 0 {
		t.Fatal("the shipped manifest pins no module sources")
	}
	for _, pin := range pins {
		if !strings.HasSuffix(pin.Repo, ".git") {
			t.Errorf("module_sources repo %q does not end in .git, so it would capture sibling repositories by prefix and miss what go-getter actually clones", pin.Repo)
		}
		bare := strings.TrimSuffix(pin.Repo, ".git")
		if strings.HasPrefix(bare, pin.Repo) {
			t.Errorf("the registry spelling of %s is captured by its own mirror key", pin.Repo)
		}
	}
}

// TestShippedModuleSourcesCoverTheCorpusGoGetterCalls is the join between
// the pin list and what the corpus actually reaches, so bumping a corpus
// source pin onto a revision that adds a go-getter repository fails here
// rather than installing a branch head.
//
// Two bounds, both deliberate. It walks from each resolved entry root
// through LOCAL module sources only, because a call inside a remote module
// cannot be seen without fetching that module - the install pass reports
// those at run time instead, as [installSummary.UnpinnedPackages], which is
// computed from the records the installer actually wrote. And it is skipped
// when .corpus has not been fetched, the same condition
// check.TestShippedManifestIsValid tolerates for var_file_layout.
//
// Scoping it to entry roots is what makes it usable at all: .corpus holds
// dozens of go-getter calls in directories no manifest glob matches, and a
// test that demanded a pin for those would demand mirrors for repositories
// nothing installs.
func TestShippedModuleSourcesCoverTheCorpusGoGetterCalls(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, ".corpus")); err != nil {
		t.Skip(".corpus is not materialized; run just corpus-fetch")
	}
	pins, err := readModuleSources(filepath.Join(root, "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[string]bool{}
	for _, pin := range pins {
		pinned[pin.Repo] = true
	}

	manifest, err := check.ReadManifest(filepath.Join(root, "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := manifest.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Skip("the manifest resolved no entries")
	}

	missing := map[string][]string{}
	for _, entry := range entries {
		for _, source := range reachableModuleSources(entry.Dir) {
			repo := gitCloneURL(source)
			if repo == "" || pinned[repo] {
				continue
			}
			missing[repo] = append(missing[repo], entry.Name)
		}
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for repo, from := range missing {
			names = append(names, repo+" (from "+from[0]+")")
		}
		sort.Strings(names)
		t.Errorf("%d go-getter repositor(ies) are reachable from a corpus entry but not pinned by live/corpus-manifest.json's module_sources, so those calls install a branch head: %s",
			len(names), strings.Join(names, ", "))
	}
}

// reachableModuleSources collects the non-local module sources an entry
// reaches, following local sources recursively the way the installer does.
func reachableModuleSources(dir string) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(string)
	walk = func(at string) {
		abs, err := filepath.Abs(at)
		if err != nil || seen[abs] {
			return
		}
		seen[abs] = true
		files, err := os.ReadDir(at)
		if err != nil {
			return
		}
		for _, file := range files {
			if file.IsDir() || !strings.HasSuffix(file.Name(), ".tf") {
				continue
			}
			text, err := os.ReadFile(filepath.Join(at, file.Name())) //nolint:gosec // reading the gitignored corpus
			if err != nil {
				continue
			}
			for _, m := range moduleSourceLiteral.FindAllStringSubmatch(string(text), -1) {
				source := m[1]
				if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
					walk(filepath.Join(at, strings.SplitN(source, "//", 2)[0]))
					continue
				}
				out = append(out, source)
			}
		}
	}
	walk(dir)
	return out
}

func TestMirrorDirIsDerivedFromTheRepoURL(t *testing.T) {
	got, err := mirrorDir(filepath.Join("x", "_modules"), "https://github.com/alphagov/terraform-govuk-tfe-workspacer.git")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("x", "_modules", "github.com", "alphagov", "terraform-govuk-tfe-workspacer.git")
	if got != want {
		t.Errorf("mirrorDir = %q, want %q", got, want)
	}
	if _, err := mirrorDir("x", "not-a-url"); err == nil {
		t.Error("a non-URL repo was accepted")
	}
}

// TestGitRewriteEnvIsAdditive: a corpus fetch must not silently discard the
// url rewrites whoever is running it already had.
func TestGitRewriteEnvIsAdditive(t *testing.T) {
	mirrors := []gitMirror{
		{Pin: moduleSourcePin{Repo: "https://github.com/a/b.git"}, Dir: "/m/a/b.git"},
		{Pin: moduleSourcePin{Repo: "https://github.com/c/d.git"}, Dir: "/m/c/d.git"},
	}
	existing := map[string]string{"GIT_CONFIG_COUNT": "2"}
	env := gitRewriteEnv(func(k string) string { return existing[k] }, mirrors)

	if env["GIT_CONFIG_COUNT"] != "4" {
		t.Errorf("GIT_CONFIG_COUNT = %q, want 4 (2 existing plus 2 mirrors)", env["GIT_CONFIG_COUNT"])
	}
	if env["GIT_CONFIG_KEY_0"] != "" || env["GIT_CONFIG_KEY_1"] != "" {
		t.Error("the caller's own rewrite slots were overwritten")
	}
	if got := env["GIT_CONFIG_KEY_2"]; got != "url./m/a/b.git.insteadOf" {
		t.Errorf("GIT_CONFIG_KEY_2 = %q", got)
	}
	if got := env["GIT_CONFIG_VALUE_2"]; got != "https://github.com/a/b.git" {
		t.Errorf("GIT_CONFIG_VALUE_2 = %q", got)
	}

	// A mirror that could not be given a directory contributes nothing;
	// with none at all there is no environment to set.
	if env := gitRewriteEnv(func(string) string { return "" }, []gitMirror{{Pin: moduleSourcePin{Repo: "x"}}}); env != nil {
		t.Errorf("a directoryless mirror produced %v", env)
	}
}

func TestWithEnvRestoresUnsetAndSetAlike(t *testing.T) {
	t.Setenv("CHOUDOUFU_TEST_SET", "before")
	os.Unsetenv("CHOUDOUFU_TEST_UNSET") //nolint:errcheck // best effort in a test

	restore := withEnv(map[string]string{"CHOUDOUFU_TEST_SET": "during", "CHOUDOUFU_TEST_UNSET": "during"})
	if os.Getenv("CHOUDOUFU_TEST_SET") != "during" || os.Getenv("CHOUDOUFU_TEST_UNSET") != "during" {
		t.Fatal("withEnv did not set")
	}
	restore()
	if got := os.Getenv("CHOUDOUFU_TEST_SET"); got != "before" {
		t.Errorf("a previously set var restored to %q, want before", got)
	}
	if _, set := os.LookupEnv("CHOUDOUFU_TEST_UNSET"); set {
		t.Error("a previously unset var was left set")
	}
}

// TestMirrorServesThePinnedCommitThroughInsteadOf is the end-to-end
// mechanism with no network: a local upstream repository stands in for
// github, a mirror is built from a commit that is NOT its branch head, and a
// clone of the github URL is checked to land on the pinned commit.
//
// The mutation is the point. The upstream's default branch is moved on after
// the pin is taken, exactly as a third-party repository does between two
// corpus runs, and the clone must still produce the pinned tree.
func TestMirrorServesThePinnedCommitThroughInsteadOf(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	upstream := filepath.Join(dir, "upstream")
	mustGit(t, "", "init", "--quiet", "-b", "trunk", upstream)
	write(t, filepath.Join(upstream, "main.tf"), "# pinned\n")
	mustGit(t, upstream, "add", ".")
	commitAll(t, upstream, "one")
	pinnedAt := gitMust(t, upstream, "rev-parse", "HEAD")
	mustGit(t, upstream, "tag", "v1.0.0")

	// The drift: upstream moves after the pin is taken.
	write(t, filepath.Join(upstream, "main.tf"), "# moved on\n")
	mustGit(t, upstream, "add", ".")
	commitAll(t, upstream, "two")
	if gitMust(t, upstream, "rev-parse", "HEAD") == pinnedAt {
		t.Fatal("the upstream did not move")
	}

	pin := moduleSourcePin{Repo: upstream, Commit: pinnedAt}
	lock := modulePins{Packages: map[string][]string{}}
	mirror := filepath.Join(dir, "_modules", "mirror.git")
	built, err := prepareMirror(ctx, pin, mirror, &lock, nil)
	if err != nil {
		t.Fatalf("preparing the mirror: %s", err)
	}
	if !built {
		t.Error("a first prepare reported the mirror was already current")
	}

	refs := lock.GitMirrors[upstream]
	if refs.DefaultBranch != "trunk" {
		t.Errorf("recorded default branch %q, want trunk", refs.DefaultBranch)
	}
	if refs.Tags["v1.0.0"] != pinnedAt {
		t.Errorf("recorded tag v1.0.0 at %q, want %q", refs.Tags["v1.0.0"], pinnedAt)
	}

	// Clone through the rewrite, the way go-getter does.
	restore := withEnv(gitRewriteEnv(os.Getenv, []gitMirror{{Pin: pin, Dir: mirror}}))
	defer restore()

	clone := filepath.Join(dir, "clone")
	mustGit(t, "", "clone", "--quiet", "--", upstream, clone)
	if got := gitMust(t, clone, "rev-parse", "HEAD"); got != pinnedAt {
		t.Errorf("the clone landed on %s, want the pinned %s - the rewrite did not take", got, pinnedAt)
	}
	if got := readFile(t, filepath.Join(clone, "main.tf")); got != "# pinned\n" {
		t.Errorf("clone content = %q, want the pinned tree", got)
	}
	// A real branch, not a detached HEAD: whatever git does with a
	// branchless source, a clone of this one is on the recorded branch.
	if got := gitMust(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "trunk" {
		t.Errorf("clone is on %q, want the recorded default branch", got)
	}
	// And the tag a "?ref=" call would ask for resolves, which is the
	// whole reason the lock records tags at all.
	if got := gitMust(t, clone, "rev-parse", "refs/tags/v1.0.0^{commit}"); got != pinnedAt {
		t.Errorf("refs/tags/v1.0.0 resolved to %s, want %s", got, pinnedAt)
	}

	// Warm: a second prepare does nothing and leaves the same commit.
	built, err = prepareMirror(ctx, pin, mirror, &lock, nil)
	if err != nil {
		t.Fatalf("re-preparing the mirror: %s", err)
	}
	if built {
		t.Error("a warm prepare rebuilt the mirror, which would evict every installed module for no reason")
	}
	if got := gitMust(t, mirror, "rev-parse", "refs/heads/trunk"); got != pinnedAt {
		t.Errorf("a warm prepare moved the mirror to %s", got)
	}
}

// TestMirrorFollowsAChangedPin is the mutation the lock has to survive: edit
// the manifest's commit and the checkout must follow it, not the stale
// mirror that is already on disk.
func TestMirrorFollowsAChangedPin(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	upstream := filepath.Join(dir, "upstream")
	mustGit(t, "", "init", "--quiet", "-b", "trunk", upstream)
	write(t, filepath.Join(upstream, "main.tf"), "# first\n")
	mustGit(t, upstream, "add", ".")
	commitAll(t, upstream, "one")
	first := gitMust(t, upstream, "rev-parse", "HEAD")
	write(t, filepath.Join(upstream, "main.tf"), "# second\n")
	mustGit(t, upstream, "add", ".")
	commitAll(t, upstream, "two")
	second := gitMust(t, upstream, "rev-parse", "HEAD")

	lock := modulePins{Packages: map[string][]string{}}
	mirror := filepath.Join(dir, "_modules", "mirror.git")
	if _, err := prepareMirror(ctx, moduleSourcePin{Repo: upstream, Commit: first}, mirror, &lock, nil); err != nil {
		t.Fatal(err)
	}
	if got := gitMust(t, mirror, "rev-parse", "refs/heads/trunk"); got != first {
		t.Fatalf("mirror is at %s, want %s", got, first)
	}

	built, err := prepareMirror(ctx, moduleSourcePin{Repo: upstream, Commit: second}, mirror, &lock, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !built {
		t.Error("a changed pin did not rebuild the mirror, so nothing installed from it would be evicted")
	}
	if got := gitMust(t, mirror, "rev-parse", "refs/heads/trunk"); got != second {
		t.Errorf("after the pin changed the mirror is still at %s, want %s - the pin is not consulted", got, second)
	}
}

// TestAnUnsatisfiablePinFailsThatMirrorOnly: a commit that does not exist
// must cost that one repository, be reported, and leave the rest of the run
// alone. It must NOT fall back to the network, which is why the rewrite is
// still registered for a failed mirror.
func TestAnUnsatisfiablePinFailsThatMirrorOnly(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	dir := t.TempDir()

	good := filepath.Join(dir, "good")
	mustGit(t, "", "init", "--quiet", "-b", "trunk", good)
	write(t, filepath.Join(good, "main.tf"), "# ok\n")
	mustGit(t, good, "add", ".")
	commitAll(t, good, "one")
	goodAt := gitMust(t, good, "rev-parse", "HEAD")

	bad := filepath.Join(dir, "bad")
	mustGit(t, "", "init", "--quiet", "-b", "trunk", bad)
	write(t, filepath.Join(bad, "main.tf"), "# ok\n")
	mustGit(t, bad, "add", ".")
	commitAll(t, bad, "one")

	// file:// URLs, because prepareMirrors derives each mirror's directory
	// from the clone URL and a bare filesystem path names no repository.
	goodURL, badURL := "file://"+good, "file://"+bad

	lock := modulePins{Packages: map[string][]string{}}
	mirrors := prepareMirrors(ctx, []moduleSourcePin{
		{Repo: goodURL, Commit: goodAt},
		{Repo: badURL, Commit: "0000000000000000000000000000000000000000"},
	}, filepath.Join(dir, "_modules"), &lock, nil)

	if len(mirrors) != 2 {
		t.Fatalf("got %d mirrors, want 2", len(mirrors))
	}
	byRepo := map[string]gitMirror{}
	for _, m := range mirrors {
		byRepo[m.Pin.Repo] = m
	}
	if byRepo[goodURL].Err != nil {
		t.Errorf("the satisfiable pin failed too: %s", byRepo[goodURL].Err)
	}
	if byRepo[badURL].Err == nil {
		t.Fatal("an unsatisfiable pin reported success")
	}
	if byRepo[badURL].Dir == "" {
		t.Error("a failed mirror has no directory, so no rewrite is registered and its calls would reach the network")
	}

	// The rewrite for the failed mirror is registered, so a clone of it
	// fails rather than silently succeeding against the real remote.
	restore := withEnv(gitRewriteEnv(os.Getenv, mirrors))
	defer restore()
	cmd := exec.Command("git", "clone", "--quiet", "--", badURL, filepath.Join(dir, "clone-bad"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		// Including the empty-clone case: a half-built mirror clones
		// successfully and empty, and an empty module directory is
		// measured as a module with no resources in it.
		t.Errorf("cloning through a failed mirror succeeded:\n%s", out)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "clone-bad", ".git")); statErr == nil {
		t.Error("a clone through the failed mirror produced a repository")
	}
}

func TestReadModuleSourcesRefusesAPartialPin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "m.json")
	write(t, path, `{"sources":[],"module_sources":[{"repo":"https://x/y.git","commit":"abc"}]}`)
	if _, err := readModuleSources(path); err == nil || !strings.Contains(err.Error(), "full SHA") {
		t.Errorf("a short commit was accepted: %v", err)
	}

	write(t, path, `{"sources":[],"module_sources":[{"commit":"`+strings.Repeat("a", 40)+`"}]}`)
	if _, err := readModuleSources(path); err == nil {
		t.Error("a pin with no repo was accepted")
	}

	write(t, path, `{"sources":[]}`)
	got, err := readModuleSources(path)
	if err != nil || len(got) != 0 {
		t.Errorf("a manifest with no module_sources gave %v, %v", got, err)
	}
}

// TestShippedManifestModuleSourcesParse keeps the checked-in list honest
// without needing .corpus.
func TestShippedManifestModuleSourcesParse(t *testing.T) {
	pins, err := readModuleSources(filepath.Join("..", "..", "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) == 0 {
		t.Fatal("no module sources are pinned")
	}
	seen := map[string]bool{}
	for _, pin := range pins {
		if seen[pin.Repo] {
			t.Errorf("%s is pinned twice", pin.Repo)
		}
		seen[pin.Repo] = true
		if !strings.HasPrefix(pin.Repo, "https://") {
			t.Errorf("repo %q is not an https URL", pin.Repo)
		}
	}

	// The manifest still decodes as the shared definition, which is the
	// property that lets corpus-fetch add a key internal/live/check does
	// not know about.
	var shared struct {
		Sources []struct {
			Glob string `json:"glob"`
		} `json:"sources"`
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "live", "corpus-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &shared); err != nil || len(shared.Sources) == 0 {
		t.Fatalf("the manifest no longer decodes as a corpus manifest: %v", err)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s\n%s", strings.Join(args, " "), err, out)
	}
}

func commitAll(t *testing.T, dir, message string) {
	t.Helper()
	mustGit(t, dir, "-c", "user.email=corpus@example.invalid", "-c", "user.name=corpus", "commit", "--quiet", "-m", message)
}

func gitMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOutput(context.Background(), dir, args...)
	if err != nil {
		t.Fatalf("git %s: %s", strings.Join(args, " "), err)
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a path this test just wrote
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
