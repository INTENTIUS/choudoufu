// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package affected

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/largeset"
)

var update = flag.Bool("update", false, "rewrite testdata golden files")

// gitEnv makes every commit in a test repository the same commit on every
// machine: no user or system configuration (signing, hooks, a default
// branch name), and fixed identities and dates.
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_AUTHOR_DATE=2026-10-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-10-01T00:00:00Z",
	)
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, rel, string(b)+body)
}

func replaceIn(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), old) {
		t.Fatalf("%s does not contain %q", rel, old)
	}
	writeFile(t, dir, rel, strings.Replace(string(b), old, new, 1))
}

func removeFile(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// moveFile renames a file in the working tree; git's rename detection
// pairs the two paths.
func moveFile(t *testing.T, dir, from, to string) {
	t.Helper()
	dst := filepath.Join(dir, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, filepath.FromSlash(from)), dst); err != nil {
		t.Fatal(err)
	}
}

func writeModuleB(t *testing.T, dir string) {
	t.Helper()
	for name, body := range largeset.ModulePackage(largeset.VersionB) {
		writeFile(t, dir, "modules/shared/"+name, string(body))
	}
}

const registry = "localhost:4890"

// localFixture is the #1750 fixture, five estates, the module called by
// relative path.
func localFixture(t *testing.T, dir string) {
	t.Helper()
	if _, err := largeset.Write(dir, largeset.Options{Estates: 5, ModuleVersion: largeset.VersionA}); err != nil {
		t.Fatal(err)
	}
}

// ociFixture is the #1750 OCI variant: every root pins
// oci://.../largeset/shared?tag=1.0.0, and the module's source lives in the
// monorepo under modules/shared, where its publisher reads it.
func ociFixture() func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		if _, err := largeset.Write(dir, largeset.Options{Estates: 5, ModuleVersion: largeset.VersionA, Source: largeset.SourceOCI, Registry: registry}); err != nil {
			t.Fatal(err)
		}
		for name, body := range largeset.ModulePackage(largeset.VersionA) {
			writeFile(t, dir, "modules/shared/"+name, string(body))
		}
	}
}

// nested adds two modules to the local fixture: modules/inner, called by
// modules/shared (so every root reaches it through a nested call), and
// modules/extra, called by e04 alone.
func nested(t *testing.T, dir string) {
	t.Helper()
	appendFile(t, dir, "modules/shared/main.tf", "\nmodule \"inner\" {\n  source = \"../inner\"\n}\n")
	writeFile(t, dir, "modules/inner/main.tf", "variable \"x\" {\n  default = 1\n}\n")
	appendFile(t, dir, "estates/e04/main.tf", "\nmodule \"extra\" {\n  source = \"../../modules/extra\"\n}\n")
	writeFile(t, dir, "modules/extra/main.tf", "variable \"y\" {\n  default = 1\n}\n")
	writeFile(t, dir, "modules/extra/more.tf", "variable \"z\" {\n  default = 1\n}\n")
	writeFile(t, dir, "modules/extra/NOTES.md", "notes\n")
}

// pinBump moves the named roots' pin from 1.0.0 to 1.1.0, as a pin-bump
// pull request would.
func pinBump(estates ...string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		for _, e := range estates {
			replaceIn(t, dir, "estates/"+e+"/main.tf", "largeset/shared?tag=1.0.0", "largeset/shared?tag=1.1.0")
		}
	}
}

func registrySource(version string) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		replaceIn(t, dir, "estates/e05/main.tf",
			`source = "oci://`+registry+`/largeset/shared?tag=1.0.0"`,
			`source  = "acme/shared/aws"`+"\n  version = \""+version+"\"")
	}
}

func steps(fs ...func(t *testing.T, dir string)) func(t *testing.T, dir string) {
	return func(t *testing.T, dir string) {
		t.Helper()
		for _, f := range fs {
			f(t, dir)
		}
	}
}

// repoAt builds a repository whose first commit is base's tree and whose
// second is head's, and returns it with the two commits.
func repoAt(t *testing.T, base, head func(t *testing.T, dir string)) (dir, baseSHA, headSHA string) {
	t.Helper()
	dir = t.TempDir()
	// The fixture's writer refuses a non-empty directory with no manifest,
	// so it goes in before git init makes .git.
	base(t, dir)
	gitT(t, dir, "init", "-q", "-b", "main")
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", "base")
	baseSHA = gitT(t, dir, "rev-parse", "HEAD")
	head(t, dir)
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "head")
	headSHA = gitT(t, dir, "rev-parse", "HEAD")
	return dir, baseSHA, headSHA
}

// summary is a result as comparable lines: one per root with its reasons,
// then one per indeterminacy and unplaced path by kind.
func summary(r *Result) []string {
	var out []string
	for _, root := range r.Roots {
		var texts []string
		for _, why := range root.Reasons {
			texts = append(texts, why.Text)
		}
		out = append(out, root.Dir+": "+strings.Join(texts, "; "))
	}
	for _, i := range r.Indeterminate {
		out = append(out, "indeterminate "+i.Kind+" "+i.Path)
	}
	for _, u := range r.Unplaced {
		out = append(out, "unplaced "+u.Why+" "+u.Path)
	}
	out = append(out, "outcome "+string(r.Outcome))
	return out
}

var chain = []string{
	"estates/e02: reads ls-e01",
	"estates/e03: reads ls-e02",
}

func everyRootUses(module string) []string {
	return []string{
		"estates/e01: uses " + module,
		"estates/e02: uses " + module + "; reads ls-e01",
		"estates/e03: uses " + module + "; reads ls-e02",
		"estates/e04: uses " + module,
		"estates/e05: uses " + module,
	}
}

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func noop(*testing.T, string) {}

type row struct {
	name   string
	base   func(t *testing.T, dir string)
	head   func(t *testing.T, dir string)
	ignore []string
	want   []string
}

// rows are #1751's acceptance table over the #1750 fixture.
var rows = []row{
	{
		name: "one estate's own file names exactly that estate",
		base: localFixture,
		head: func(t *testing.T, dir string) { appendFile(t, dir, "estates/e05/main.tf", "\n# edited\n") },
		want: []string{"estates/e05: changed", "outcome determinate"},
	},
	{
		name: "a producer's change names its readers down the depth-2 chain",
		base: localFixture,
		head: func(t *testing.T, dir string) { appendFile(t, dir, "estates/e01/main.tf", "\n# edited\n") },
		want: cat([]string{"estates/e01: changed"}, chain, []string{"outcome determinate"}),
	},
	{
		name: "a reader's change names it and what reads it, not what it reads",
		base: localFixture,
		head: func(t *testing.T, dir string) { appendFile(t, dir, "estates/e02/main.tf", "\n# edited\n") },
		want: []string{"estates/e02: changed", "estates/e03: reads ls-e02", "outcome determinate"},
	},
	{
		name: "a producer's estate rename names what read the old name",
		base: localFixture,
		head: func(t *testing.T, dir string) {
			replaceIn(t, dir, "estates/e01/estate.chdf.hcl", `estate = "ls-e01"`, `estate = "ls-e01b"`)
		},
		want: cat([]string{"estates/e01: changed"}, chain, []string{"outcome determinate"}),
	},
	{
		name: "the shared local module names every estate that uses it",
		base: localFixture,
		head: writeModuleB,
		want: cat(everyRootUses("modules/shared"), []string{"outcome determinate"}),
	},
	{
		name: "a module reached through a nested call names every estate reaching it",
		base: steps(localFixture, nested),
		head: func(t *testing.T, dir string) { appendFile(t, dir, "modules/inner/main.tf", "\n# edited\n") },
		want: cat(everyRootUses("modules/inner"), []string{"outcome determinate"}),
	},
	{
		name: "a module one estate uses names that estate and no other",
		base: steps(localFixture, nested),
		head: func(t *testing.T, dir string) { appendFile(t, dir, "modules/extra/main.tf", "\n# edited\n") },
		want: []string{"estates/e04: uses modules/extra", "outcome determinate"},
	},
	{
		name: "a file deleted from a module names the module's users",
		base: steps(localFixture, nested),
		head: func(t *testing.T, dir string) { removeFile(t, dir, "modules/extra/more.tf") },
		want: []string{"estates/e04: uses modules/extra", "outcome determinate"},
	},
	{
		name: "a file deleted from a root names that root",
		base: steps(localFixture, func(t *testing.T, dir string) {
			writeFile(t, dir, "estates/e05/extra.tf", "variable \"w\" {\n  default = 1\n}\n")
		}),
		head: func(t *testing.T, dir string) { removeFile(t, dir, "estates/e05/extra.tf") },
		want: []string{"estates/e05: changed", "outcome determinate"},
	},
	{
		name: "a file renamed out of a module names the module's users",
		base: steps(localFixture, nested),
		head: func(t *testing.T, dir string) {
			moveFile(t, dir, "modules/extra/more.tf", "attic/more.tf")
		},
		want: []string{"estates/e04: uses modules/extra", "unplaced unread-module attic/more.tf", "outcome determinate"},
	},
	{
		name: "a document renamed out of a module names the module's users and nothing for its new path",
		base: steps(localFixture, nested),
		head: func(t *testing.T, dir string) {
			moveFile(t, dir, "modules/extra/NOTES.md", "docs/NOTES.md")
		},
		want: []string{"estates/e04: uses modules/extra", "unplaced documentation docs/NOTES.md", "outcome determinate"},
	},
	{
		name: "a provider version bump is indeterminate",
		base: localFixture,
		head: func(t *testing.T, dir string) {
			replaceIn(t, dir, "estates/e05/main.tf", `version = "= 6.59.0"`, `version = "= 6.60.0"`)
		},
		want: []string{"estates/e05: changed", "indeterminate provider-version estates/e05", "outcome indeterminate"},
	},
	{
		name: "a lock file bump is indeterminate",
		base: steps(localFixture, func(t *testing.T, dir string) {
			writeFile(t, dir, "estates/e05/.terraform.lock.hcl", "# lock 6.59.0\n")
		}),
		head: func(t *testing.T, dir string) {
			writeFile(t, dir, "estates/e05/.terraform.lock.hcl", "# lock 6.60.0\n")
		},
		want: []string{"estates/e05: changed", "indeterminate lock-file estates/e05/.terraform.lock.hcl", "outcome indeterminate"},
	},
	{
		name: "a documentation file names nothing and does not force planning everything",
		base: localFixture,
		head: func(t *testing.T, dir string) { writeFile(t, dir, "README.md", "about\n") },
		want: []string{"unplaced documentation README.md", "outcome determinate"},
	},
	{
		name: "a loose file outside every module is indeterminate",
		base: localFixture,
		head: func(t *testing.T, dir string) { writeFile(t, dir, "common.tfvars", "region = \"us-east-1\"\n") },
		want: []string{"indeterminate unplaced-file common.tfvars", "outcome indeterminate"},
	},
	{
		name:   "an ignored path names nothing",
		base:   localFixture,
		head:   func(t *testing.T, dir string) { writeFile(t, dir, ".gitlab-ci.yml", "stages: [plan]\n") },
		ignore: []string{".gitlab-ci.yml"},
		want:   []string{"unplaced ignored .gitlab-ci.yml", "outcome determinate"},
	},
	{
		name: "module code no root calls by a local path names nothing",
		base: localFixture,
		head: func(t *testing.T, dir string) { writeFile(t, dir, "modules/unused/main.tf", "variable \"v\" {}\n") },
		want: []string{"unplaced unread-module modules/unused/main.tf", "outcome determinate"},
	},
	{
		name: "a root that does not load at head is indeterminate",
		base: localFixture,
		head: func(t *testing.T, dir string) {
			appendFile(t, dir, "estates/e05/main.tf", "\nresource \"aws_vpc\" {\n")
		},
		want: []string{"estates/e05: changed", "indeterminate load-error estates/e05", "outcome indeterminate"},
	},
	{
		name: "oci: a change under modules/ with every root pinned names no root",
		base: ociFixture(),
		head: writeModuleB,
		want: []string{"unplaced unread-module modules/shared/main.tf", "outcome determinate"},
	},
	{
		name: "oci: a pin bump names the bumped roots with the pin",
		base: ociFixture(),
		head: pinBump("e02", "e04"),
		want: []string{
			"estates/e02: pin shared 1.0.0 -> 1.1.0; changed",
			"estates/e03: reads ls-e02",
			"estates/e04: pin shared 1.0.0 -> 1.1.0; changed",
			"outcome determinate",
		},
	},
	{
		name: "registry: an exact version is pinned, so a module change names no root",
		base: steps(ociFixture(), registrySource("1.4.0")),
		head: writeModuleB,
		want: []string{"unplaced unread-module modules/shared/main.tf", "outcome determinate"},
	},
	{
		name: "registry: an exact version bump names the root with the pin",
		base: steps(ociFixture(), registrySource("1.4.0")),
		head: func(t *testing.T, dir string) {
			replaceIn(t, dir, "estates/e05/main.tf", `version = "1.4.0"`, `version = "1.5.0"`)
		},
		want: []string{"estates/e05: pin shared 1.4.0 -> 1.5.0; changed", "outcome determinate"},
	},
	{
		name: "registry: a floating constraint makes a module change indeterminate",
		base: steps(ociFixture(), registrySource("~> 1.4")),
		head: writeModuleB,
		want: []string{"indeterminate floating-module modules/shared/main.tf", "outcome indeterminate"},
	},
	{
		name: "registry: a floating constraint does not taint an unrelated change",
		base: steps(ociFixture(), registrySource("~> 1.4")),
		head: func(t *testing.T, dir string) { appendFile(t, dir, "estates/e04/main.tf", "\n# edited\n") },
		want: []string{"estates/e04: changed", "outcome determinate"},
	},
}

func TestAffectedTable(t *testing.T) {
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, head := repoAt(t, tc.base, tc.head)
			res, err := Compute(context.Background(), Options{RepoDir: dir, Spec: base + ".." + head, Ignore: tc.ignore, TempDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			got := summary(res)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Errorf("got:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tc.want, "\n  "))
			}
		})
	}
}

// TestRangeForms reads one change through every range spelling.
func TestRangeForms(t *testing.T) {
	dir, base, head := repoAt(t, localFixture, func(t *testing.T, dir string) {
		appendFile(t, dir, "estates/e05/main.tf", "\n# edited\n")
	})
	for _, spec := range []string{base + ".." + head, base + "..", base, base + "..." + head, "HEAD~1..HEAD"} {
		res, err := Compute(context.Background(), Options{RepoDir: filepath.Join(dir, "estates"), Spec: spec, TempDir: t.TempDir()})
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if res.Range.Base != base || res.Range.Head != head {
			t.Errorf("%s resolved to %s..%s, want %s..%s", spec, res.Range.Base, res.Range.Head, base, head)
		}
		if got := strings.Join(summary(res), "|"); got != "estates/e05: changed|outcome determinate" {
			t.Errorf("%s: %s", spec, got)
		}
	}
	if _, err := Compute(context.Background(), Options{RepoDir: dir, Spec: "nope..HEAD"}); err == nil {
		t.Error("an unknown revision was accepted")
	}
}

// TestRootSelection restricts the run to -root directories: a change to a
// root left out names nothing.
func TestRootSelection(t *testing.T) {
	dir, base, head := repoAt(t, localFixture, func(t *testing.T, dir string) {
		appendFile(t, dir, "estates/e01/main.tf", "\n# edited\n")
	})
	res, err := Compute(context.Background(), Options{RepoDir: dir, Spec: base + ".." + head, Roots: []string{"estates/e01", "estates/e03"}, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(summary(res), "|"); got != "estates/e01: changed|outcome determinate" {
		t.Errorf("got %s", got)
	}
	if res.RootsTotal != 2 {
		t.Errorf("roots_total %d, want 2", res.RootsTotal)
	}
	if _, err := Compute(context.Background(), Options{RepoDir: dir, Spec: base + ".." + head, Roots: []string{"estates/e09"}, TempDir: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "-root estates/e09 holds no configuration") {
		t.Errorf("a -root naming nothing was accepted: %v", err)
	}
}

// TestJSONGolden pins the -json document: field names, nesting, empty
// arrays rather than null, and the reason objects. The two commit hashes
// are replaced, since they are the only part of it a test repository does
// not fix. Regenerate with -update after reading the diff.
func TestJSONGolden(t *testing.T) {
	dir, base, head := repoAt(t, ociFixture(), steps(pinBump("e02", "e04"), func(t *testing.T, dir string) {
		writeFile(t, dir, "README.md", "about\n")
		replaceIn(t, dir, "estates/e05/main.tf", `version = "= 6.59.0"`, `version = "= 6.60.0"`)
	}))
	res, err := Compute(context.Background(), Options{RepoDir: dir, Spec: base + ".." + head, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	doc = strings.NewReplacer(base, "<base>", head, "<head>").Replace(doc)
	golden := filepath.Join("testdata", "result.golden.json")
	if *update {
		if err := os.WriteFile(golden, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if doc != string(want) {
		t.Errorf("the -json document moved from %s:\n%s", golden, doc)
	}
}

// TestEmptySlicesAreArrays holds the contract that a consumer never reads
// null for a list.
func TestEmptySlicesAreArrays(t *testing.T) {
	dir, base, head := repoAt(t, localFixture, noop)
	res, err := Compute(context.Background(), Options{RepoDir: dir, Spec: base + ".." + head, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := res.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"roots": []`, `"indeterminate": []`, `"unplaced": []`} {
		if !strings.Contains(doc, field) {
			t.Errorf("an empty range's document lacks %s:\n%s", field, doc)
		}
	}
	if res.RootsTotal != 5 {
		t.Errorf("roots_total %d, want 5", res.RootsTotal)
	}
}
