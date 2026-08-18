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
	"strconv"
	"strings"
	"testing"
)

// HANDOFF.md is the standing playbook: what the work is for, what makes a
// change acceptable, and how to get a task from the tracker to a merge. It is
// the first thing a session with no memory of this repository reads.
//
// Three earlier versions of it were status files. Each went stale within an
// hour, one carried two rows that were wrong at the moment they were written,
// and a figure from it was propagated into three other committed files before
// an audit caught that it predated a merge. The rewrite dropped every
// perishable number and kept the durable instructions.
//
// But instructions rot in a quieter way: they go on naming a `just` recipe
// that was renamed, a tool that moved, a test that was deleted. The reader
// finds out by running something that does not exist, which is the moment the
// whole document stops being trusted.
//
// So the playbook's citations are checked against the tree. This is the same
// shape as ci_coverage_test.go next door - a registry checked against
// reality rather than a hand-list - and it fails in both directions: a
// citation that no longer resolves, and a pinned figure that has drifted from
// the pin it was copied out of.
//
// What this test deliberately does NOT do is check the prose. Whether
// "parity is the bar" is still the maintainer's position is not decidable
// here. It checks the things a reader would try to run.

const handoffPath = "HANDOFF.md"

// TestHandoffCitationsResolve checks every path, tool, recipe and test name
// the playbook names.
func TestHandoffCitationsResolve(t *testing.T) {
	root := repoRoot(t)
	text := readHandoff(t, root)

	t.Run("paths", func(t *testing.T) {
		cited := handoffCitedPaths(root, text)
		if len(cited) < 8 {
			t.Fatalf("found only %d cited paths in %s; the extraction is broken rather than the document being sparse", len(cited), handoffPath)
		}
		for _, p := range cited {
			if reason, expected := notYetCreated[p]; expected {
				// Both directions: the entry is only valid while the file is
				// genuinely absent. Once it exists the exception is stale and
				// says so, rather than quietly excusing a real citation.
				if _, err := os.Stat(filepath.Join(root, p)); err == nil {
					t.Errorf("%s is listed in notYetCreated (%q) and now exists.\n"+
						"Delete the entry - an exception that no longer applies reads as a live one.", p, reason)
				}
				continue
			}
			if _, err := os.Stat(filepath.Join(root, p)); err != nil {
				t.Errorf("%s cites %s, which does not exist.\n"+
					"Either the path moved and the playbook needs updating, or it was deleted and the instruction around it is now wrong.", handoffPath, p)
			}
		}
	})

	t.Run("just recipes", func(t *testing.T) {
		recipes := justRecipes(t, root)
		cited := handoffCitedRecipes(text)
		if len(cited) == 0 {
			t.Fatal("found no `just <recipe>` citations; the extraction is broken - the playbook names at least `just ci`")
		}
		for _, r := range cited {
			if !recipes[r] {
				t.Errorf("%s tells the reader to run `just %s`, which is not a recipe in the justfile.\n"+
					"A reader following the playbook cold would hit an error here.", handoffPath, r)
			}
		}
	})

	t.Run("test names", func(t *testing.T) {
		defined := definedTestNames(t, root)
		cited := handoffCitedTestNames(text)
		if len(cited) < 5 {
			t.Fatalf("found only %d cited test names; the extraction is broken (the enforcement table alone names six)", len(cited))
		}
		for _, name := range cited {
			if !defined[name] {
				t.Errorf("%s cites %s, which is not defined anywhere in the tree.\n"+
					"A guard named in the playbook and absent from the tree is worse than one nobody documented: it reads as covered.", handoffPath, name)
			}
		}
	})
}

// TestHandoffFiguresMatchTheirPins is the second direction.
//
// The playbook quotes two numbers about the identity golden, because they are
// what tell a reader whether the instrument is big enough to be worth
// trusting. Those are copied out of identityGoldenPin, and a copy is exactly
// the thing that goes stale - it is how a site total ended up propagated into
// three committed files while being off by a whole refusal class.
func TestHandoffFiguresMatchTheirPins(t *testing.T) {
	text := readHandoff(t, repoRoot(t))

	var want int
	for _, n := range identityGoldenPin {
		want += n
	}
	if want != identityGoldenPinInstances {
		t.Fatalf("identityGoldenPin sums to %d but identityGoldenPinInstances is %d; fix the pin before trusting the playbook against it",
			want, identityGoldenPinInstances)
	}

	for _, c := range []struct {
		what string
		n    int
	}{
		{"rendered identities", identityGoldenPinInstances},
		{"configuration directories", identityGoldenPinDirs},
	} {
		if !figureAnchored(text, c.n, c.what) {
			t.Errorf("%s does not attach %d to %q anywhere.\n"+
				"A bare digit run is not enough: %d also matches inside a longer number (1%d, %d0) and inside an "+
				"unrelated PR or issue reference, and a strings.Contains check over the whole document cannot tell "+
				"those apart from the sentence that actually names the figure.\n"+
				"Either the pin moved and the playbook still quotes the old figure, or the sentence describing the "+
				"golden was dropped while a decoy digit run elsewhere kept the old check quiet.",
				handoffPath, c.n, c.what, c.n, c.n, c.n)
		}
	}
}

// digitRun matches a maximal run of ASCII digits, so figureAnchored can
// compare it against the pinned number as a whole rather than as a
// substring - the difference between "400" and "4001" or "14000".
var digitRun = regexp.MustCompile(`[0-9]+`)

// figureAnchored reports whether n appears in text as a standalone digit
// run - never as part of a longer one - immediately next to (across
// whitespace) the literal phrase describing what it counts.
//
// This is deliberately stronger than "the digit string occurs somewhere in
// the document": a PR number, an issue number, or a line reference satisfies
// a bare strings.Contains identically to the sentence that actually names
// the figure, which is exactly how a deleted sentence left this check green
// with the digits still present elsewhere as unrelated numbers.
func figureAnchored(text string, n int, phrase string) bool {
	want := strconv.Itoa(n)
	for _, loc := range digitRun.FindAllStringIndex(text, -1) {
		if text[loc[0]:loc[1]] != want {
			continue
		}
		before := strings.TrimRight(text[:loc[0]], " \t")
		after := strings.TrimLeft(text[loc[1]:], " \t")
		if strings.HasPrefix(after, phrase) || strings.HasSuffix(before, phrase) {
			return true
		}
	}
	return false
}

// TestHandoffCarriesNoConflictMarkers is the cheapest guard here and it exists
// because the omission was live on main for two merges.
//
// Resolving two golden re-pins in a row, I ran a regex over HANDOFF.md that
// collapsed all three conflict variants to identical text and left the <<<<<<<
// / ======= / >>>>>>> lines in place. The citation legs above were all green -
// every path, recipe and test name still resolved, and the figure was correct
// three times over - so nothing failed, and the file was pushed. A subagent
// reading it for instructions found it.
//
// The lesson generalises past this file: a guard that checks only what a
// document CITES will not notice that the document is malformed. This is the
// well-formedness half.
//
// Repository-wide rather than HANDOFF-only, because the same edit shape
// reaches any hand-owned markdown, and a generated artifact carrying these
// would be worse still.
func TestHandoffCarriesNoConflictMarkers(t *testing.T) {
	root := repoRoot(t)

	// Deliberately not a single regexp over the three: ======= alone appears
	// as a legitimate markdown setext underline, so it only counts when the
	// other two are present in the same file.
	starts := regexp.MustCompile(`(?m)^<{7} `)
	ends := regexp.MustCompile(`(?m)^>{7} `)
	middles := regexp.MustCompile(`(?m)^={7}$`)

	var checked int
	for _, rel := range conflictScannedFiles {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("%s is listed for conflict scanning and could not be read: %s\n"+
				"Remove it from conflictScannedFiles or restore it; a scan over a file that is not there passes forever.", rel, err)
			continue
		}
		checked++
		text := string(b)
		nStart, nEnd := len(starts.FindAllString(text, -1)), len(ends.FindAllString(text, -1))
		if nStart == 0 && nEnd == 0 {
			continue
		}
		t.Errorf("%s carries %d unresolved merge-conflict start marker(s), %d end marker(s) and %d bare ======= line(s).\n"+
			"Every citation in it can still resolve while it says the same thing three times, which is how this shipped: "+
			"the checks above pass and the document is unreadable.",
			rel, nStart, nEnd, len(middles.FindAllString(text, -1)))
	}
	if checked < len(conflictScannedFiles) {
		t.Errorf("scanned %d of %d listed files; the rest are missing and this guard is weaker than it looks", checked, len(conflictScannedFiles))
	}
}

// conflictScannedFiles are the hand-owned documents an agent or a fresh
// session reads for instructions, plus the artifacts a bad merge would
// corrupt silently. Generated markdown is deliberately included: a conflict
// marker inside a generated span means somebody hand-merged instead of
// regenerating, which is its own standing rule.
var conflictScannedFiles = []string{
	"HANDOFF.md",
	".claude/agents/live-markers.md",
	".claude/skills/measuring-the-wall/SKILL.md",
	"live/HARNESS.md",
	"live/LIMITATIONS.md",
	"live/MARKERS.md",
	"README.md",
}

func readHandoff(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, handoffPath))
	if err != nil {
		t.Fatalf("reading %s: %s\n"+
			"This test guards that file; with it absent there is nothing to guard, so this is a failure rather than a skip.", handoffPath, err)
	}
	return string(b)
}

// backticked matches every `code span` in the document. The playbook's
// convention is that anything a reader would type or open is in one.
var backticked = regexp.MustCompile("`([^`\n]+)`")

// fencedBlock matches the body of a ``` fenced code block: everything
// between the line that opens the fence and the line that closes it. The
// playbook puts every runnable command line - `go run`, `go test`, `just`
// invocations with real arguments - in one of these rather than in an
// inline span, so a checker that only reads inline spans never sees them.
var fencedBlock = regexp.MustCompile("(?s)```[^\n]*\n(.*?)\n```")

// pathish recognises a repo-relative path: at least one slash, and a
// component that looks like a file or a directory rather than prose.
var pathish = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_./-]*/[A-Za-z0-9_./-]+$`)

// handoffCitedPaths finds every repo-relative path the playbook names,
// whether it sits alone in an inline `code span` or as an operand inside a
// command line - `go run ./tools/x -flag y`, `env -u PWD go test
// ./internal/live/check/ -run Foo` - in either an inline span or a fenced
// block. Both sources are tokenised and validated the same way: this is the
// "tool leg" the package doc comment already promises, folded into the path
// check rather than built as a parallel extractor.
func handoffCitedPaths(root, text string) []string {
	seen := map[string]bool{}
	for _, m := range backticked.FindAllStringSubmatch(text, -1) {
		addPathTokens(root, m[1], seen)
	}
	for _, m := range fencedBlock.FindAllStringSubmatch(text, -1) {
		addPathTokens(root, m[1], seen)
	}
	return sortedSet(seen)
}

// addPathTokens splits s - an inline code span or a fenced block's body -
// on whitespace and adds every token that resolves to a real path under
// root to seen. A bare path (no whitespace) is a no-op split of one, so an
// inline `tools/refusal-probe` citation goes through the identical checks a
// `go run ./tools/refusal-probe -out x.json` command line's operand does.
func addPathTokens(root, s string, seen map[string]bool) {
	for _, tok := range strings.Fields(s) {
		// Glob and wildcard forms name a family, not a file, and a
		// placeholder segment like <path>, <name> or <sha> names a value
		// the reader supplies, not a repository artifact - this is what
		// keeps `git worktree add ../wt/<name> -b wall/<name> main` from
		// being checked against a literal directory named "<name>".
		if strings.ContainsAny(tok, "*<>${}|") {
			continue
		}
		tok = strings.TrimSuffix(tok, "/")
		if !pathish.MatchString(tok) {
			continue
		}
		// ./tools/x is how a go command spells it; the repo path is the same.
		tok = strings.TrimPrefix(tok, "./")
		// A GitHub repo slug (opentofu/opentofu, INTENTIUS/choudoufu) is
		// spelled exactly like a two-segment path. Recognition is therefore
		// "the first segment is a real entry at the repo root" rather than a
		// deny-list of slugs, which would need a new entry every time the
		// playbook mentioned another repository.
		//
		// The cost is that a typo in the FIRST segment reads as prose and is
		// skipped rather than reported. A typo in any later segment - which
		// is the common case, since the first segment is a short familiar
		// word - still fails.
		first, _, _ := strings.Cut(tok, "/")
		if _, err := os.Stat(filepath.Join(root, first)); err != nil {
			continue
		}
		seen[tok] = true
	}
}

// notYetCreated are paths the playbook names on purpose while they do not yet
// exist, because telling a fresh session which file a step will produce is the
// whole value of the sentence.
//
// Each entry must say what creates it. The check runs in both directions, so
// an entry outlives its file by exactly one commit.
var notYetCreated = map[string]string{
	"live/corpus-module-pins.json": "written by the first `just corpus-fetch` run after the module-install and go-getter-mirror work landed; the playbook names it so the next session commits it with the regenerated artifact instead of leaving the corpus unpinned",
}

var justCall = regexp.MustCompile(`\bjust ([a-z][a-z0-9-]*)`)

func handoffCitedRecipes(text string) []string {
	seen := map[string]bool{}
	for _, m := range justCall.FindAllStringSubmatch(text, -1) {
		seen[m[1]] = true
	}
	return sortedSet(seen)
}

var testName = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9_]*`)

func handoffCitedTestNames(text string) []string {
	seen := map[string]bool{}
	for _, m := range testName.FindAllString(text, -1) {
		seen[m] = true
	}
	return sortedSet(seen)
}

// justRecipes reads recipe names out of the justfile.
//
// A recipe line is `name:` or `name arg="default":` at column zero. Parsing
// it here rather than shelling out to `just --summary` keeps the test running
// on a machine that does not have just installed, which CI's container does
// not guarantee.
var justRecipeDecl = regexp.MustCompile(`(?m)^([a-z][a-z0-9-]*)(?:\s+[^:\n]*)?:`)

func justRecipes(t *testing.T, root string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("reading the justfile: %s", err)
	}
	out := map[string]bool{}
	for _, m := range justRecipeDecl.FindAllStringSubmatch(string(b), -1) {
		out[m[1]] = true
	}
	if len(out) < 5 {
		t.Fatalf("parsed only %d recipes out of the justfile; the pattern has stopped matching and every citation would pass vacuously", len(out))
	}
	return out
}

var testDecl = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

// definedTestNames walks the fork's own trees for test declarations.
//
// internal/ at large is included because the golden and the marksafe guards
// live under internal/live, but upstream OpenTofu's own packages are in scope
// too and that is fine: this leg only ever asks whether a cited name exists.
func definedTestNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, dir := range []string{"live", "internal", "tools", "cmd"} {
		walkTestFiles(t, filepath.Join(root, dir), func(b []byte) {
			for _, m := range testDecl.FindAllSubmatch(b, -1) {
				out[string(m[1])] = true
			}
		})
	}
	if len(out) < 100 {
		t.Fatalf("found only %d test declarations in the tree; the walk is broken and every citation would pass vacuously", len(out))
	}
	return out
}

func walkTestFiles(t *testing.T, dir string, fn func([]byte)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir():
			if e.Name() == "testdata" || e.Name() == ".terraform" {
				continue
			}
			walkTestFiles(t, p, fn)
		case strings.HasSuffix(e.Name(), "_test.go"):
			b, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("reading %s: %s", p, err)
				continue
			}
			fn(b)
		}
	}
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
