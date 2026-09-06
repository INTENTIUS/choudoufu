// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file is issue #695's guard, and #694 is the case it was written for.
//
// tools/ratification-queue-gen wrote live/ratification-queue.json once, 179KB
// of it, and nothing ever read it. Nobody noticed for months because nothing
// looks: a generator that runs and writes a file looks exactly like a
// generator doing its job. #694 deleted the tool and the artifact after a
// hand grep established it had zero consumers, and that grep is what this
// file makes standing.
//
// The claim: a committed artifact a tools/ generator writes must be read by
// something outside that generator. Not "must be useful" - that is a
// judgment - but "some file elsewhere in this repository names it", which is
// the cheapest possible evidence that anything at all depends on the write.
//
// # How the write set is found
//
// Two mechanical signals, unioned, neither of them a hand list.
//
// The first is the artifact's own word. Most committed artifacts here carry
// a top-level generated_by naming the command that wrote them, and one that
// names a tools/ generator is a self-declared generator output.
//
// The second is the generators' own path constants. Every non-test Go file
// under tools/ is parsed, every os.WriteFile call is found, and its first
// argument is resolved back to a repo-relative path through the spellings
// this repository actually uses: a string literal, an identifier naming a
// package-level const or var, filepath.Join(root, X), filepath.FromSlash(X),
// and an identifier assigned from flag.String(name, default, usage) - how
// tools/mapping-gen and tools/survey-gen spell their -out.
//
// Neither signal is complete on its own and the union is not complete
// either: a generator that carries its write path into a helper as a
// parameter and writes an artifact with no generated_by (tools/corpus-fetch
// and tools/corpus-gen are the ones that do both) is invisible to both. That
// is the honest limit, and it is why the discovered set is PINNED below
// rather than only checked. The pin is what makes the covered surface a
// number in a diff: an artifact that leaves the set, because a generator was
// refactored past both signals, fails this file instead of quietly shrinking
// what it guards.
//
// # What counts as a reader
//
// Any file in the checkout that names the path and is not: the artifact
// itself, .gitignore, live/fork-surface.json (the fork inventory lists every
// changed path, so it "reads" everything and proves nothing - it was one of
// the two false consumers #694 had to look past), THIS FILE, or a file
// inside the directory of the generator that writes it.
//
// The generator-directory exclusion is the point of the check: a generator
// citing its own output in its own doc comment is not a consumer. Excluding
// this file is the point too, and it was found the way this repository says
// to find things - by trying to make the guard fail. The pin below names
// every artifact, so without the exclusion every artifact had a reader
// (this file) and the check could not go red for anything, ever.

// writtenArtifacts is every committed artifact a tools/ generator writes, as
// discovered by the scan described above. It is pinned so that a new
// generator artifact, or an existing one that stops being discoverable, is a
// line in a diff next to this comment.
//
// Measured on this list when it was written: every entry has a reader, and
// the guard below is what keeps that true.
var writtenArtifacts = []string{
	"live/behaviors.json",
	"live/composite-import-roster.json",
	"live/credential-sweep.json",
	"live/estate-types.json",
	"live/floci-capabilities.json",
	"live/fork-surface.json",
	"live/gauntlet.json",
	"live/gauntlet/estates.json",
	"live/iam-reference.json",
	"live/identity-sources.json",
	"live/import-grammar.json",
	"live/logical-schemas.json",
	"live/mapping.json",
	"live/readiness.json",
	"live/registry-schema-facts.json",
	"live/registry.json",
	"live/rowgen-buckets.json",
	"live/rowgen-mismatches.json",
	"live/schema-precedence.json",
	"live/survey-full.json",
	"live/survey.json",
	"live/tag-verbs.json",
	"tools/mapping-gen/former2-rows.json",
	"tools/mapping-gen/namesdata-generated.json",
	"tools/row-gen/evidence-schema-gap.json",
}

// insideConsumer is one artifact whose consumer is not a file elsewhere in
// the tree - either the writing generator's own second stage, or a person.
// Both are real consumers; neither is visible to the scan above, so each is
// registered here by name with the consumer it actually has.
//
// This is the derivation-guard idiom, for the same reason that file gives:
// the guard cannot judge whether a consumer is good enough, so it makes the
// exception surface explicit, bounded and countable. The count is pinned
// below. Removing an entry is as much a claim as adding one, so
// TestInsideConsumersAreStillNeeded fails an entry whose artifact has since
// gained an ordinary reader.
type insideConsumer struct {
	Artifact string

	// Reader is a file inside the writing generator that actually reads the
	// artifact. Empty means the consumer is a person and Why has to say so.
	Reader string

	Why string
}

// insideConsumers was measured, not guessed: it is exactly the set the guard
// below reported on its first red run over this tree.
var insideConsumers = []insideConsumer{
	{
		Artifact: "tools/mapping-gen/former2-rows.json",
		Reader:   "tools/mapping-gen/former2_source.go",
		Why: "A committed download checkpoint: mapping-gen fetches the former2 rows in one run and " +
			"reads them back on every later one, which is the whole reason it is committed rather " +
			"than cached. The consumer is the same generator's next run, not another package.",
	},
	{
		Artifact: "tools/mapping-gen/namesdata-generated.json",
		Reader:   "tools/mapping-gen/namesdata_source.go",
		Why:      "The same shape as former2-rows.json: mapping-gen's own committed copy of an upstream source.",
	},
	{
		Artifact: "tools/row-gen/evidence-schema-gap.json",
		Why: "Issue #428's remainder ledger. It exists to be read by a person deciding what evidence " +
			"would close each family's gap - the same standing tools/row-gen/separator-evidence.json " +
			"has, and the reason both are review indexes rather than 300 rejected.json entries. If a " +
			"year passes and no ratification batch has cited it, that is a reason to delete it, and " +
			"this entry is where that question gets asked.",
	},
}

// TestEveryGeneratorArtifactHasAReader is the guard itself.
func TestEveryGeneratorArtifactHasAReader(t *testing.T) {
	root := repoRoot(t)
	writers := discoverGeneratorArtifacts(t, root)
	excused := map[string]bool{}
	for _, e := range insideConsumers {
		excused[e.Artifact] = true
	}

	for _, rel := range writtenArtifacts {
		writerDir, ok := writers[rel]
		if !ok {
			continue // the pin check below reports it; do not double-report
		}
		if excused[rel] {
			continue
		}
		readers := readersOf(t, root, rel, writerDir)
		if len(readers) == 0 {
			t.Errorf("%s is written by tools/%s and read by nothing outside it.\n"+
				"An artifact nothing reads is a generator burning time and a reviewer's attention on a\n"+
				"file that cannot be wrong, because nothing would notice. Delete the artifact and its\n"+
				"generator (that is what #694 did), give it a reader, or - if its consumer is the same\n"+
				"generator's next stage or a person - add it to insideConsumers with the reason.",
				rel, writerDir)
		}
	}
}

// TestInsideConsumersAreStillNeeded holds the exception list honest in both
// directions: an entry naming a reader must name one that really reads the
// artifact, an entry naming none must say who does, and an entry whose
// artifact has since gained an ordinary reader must go.
func TestInsideConsumersAreStillNeeded(t *testing.T) {
	root := repoRoot(t)
	writers := discoverGeneratorArtifacts(t, root)

	const wantEntries = 3
	if len(insideConsumers) != wantEntries {
		t.Errorf("insideConsumers has %d entries, pinned at %d. Adding one is a standing exception to "+
			"the rule this file exists for; removing one is a claim the artifact now has an ordinary "+
			"reader. Either way, update the count here in the same diff.", len(insideConsumers), wantEntries)
	}

	for _, e := range insideConsumers {
		if e.Why == "" {
			t.Errorf("%s: an entry with no reason is a hole, not an exception", e.Artifact)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(e.Artifact))); err != nil {
			t.Errorf("%s: no such artifact; delete the entry", e.Artifact)
			continue
		}
		if e.Reader != "" {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Reader))) //nolint:gosec // a path inside the checkout
			if err != nil {
				t.Errorf("%s: its named reader %s does not exist", e.Artifact, e.Reader)
			} else if !strings.Contains(string(data), e.Artifact) {
				t.Errorf("%s: its named reader %s does not mention it, so the entry is claiming a "+
					"consumer that is not there", e.Artifact, e.Reader)
			}
		}
		if writerDir, ok := writers[e.Artifact]; ok {
			if readers := readersOf(t, root, e.Artifact, writerDir); len(readers) > 0 {
				t.Errorf("%s now has an ordinary reader (%s), so it no longer needs an entry here; "+
					"delete it and lower the pinned count", e.Artifact, strings.Join(readers, ", "))
			}
		}
	}
}

// TestGeneratorArtifactPinIsCurrent is the completeness half. Without it a
// generator whose -out this scan cannot resolve would drop out of the write
// set with nothing said, and the guard above would pass by covering less.
func TestGeneratorArtifactPinIsCurrent(t *testing.T) {
	root := repoRoot(t)
	writers := discoverGeneratorArtifacts(t, root)

	found := make([]string, 0, len(writers))
	for rel := range writers {
		found = append(found, rel)
	}
	sort.Strings(found)

	pinned := append([]string(nil), writtenArtifacts...)
	sort.Strings(pinned)

	if strings.Join(found, "\n") != strings.Join(pinned, "\n") {
		t.Errorf("the committed artifacts tools/ generators write have changed.\n"+
			"Update writtenArtifacts in this file, and make sure every addition has a reader.\n\ndiscovered:\n  %s\n\npinned:\n  %s",
			strings.Join(found, "\n  "), strings.Join(pinned, "\n  "))
	}
}

// discoverGeneratorArtifacts returns each committed artifact path a tools/
// generator writes, mapped to the generator directory that writes it (the
// path relative to tools/, e.g. "row-gen").
func discoverGeneratorArtifacts(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}

	toolsDir := filepath.Join(root, "tools")
	err := filepath.WalkDir(toolsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Errorf("parsing %s: %v", path, parseErr)
			return nil
		}
		dir, relErr := filepath.Rel(toolsDir, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		for _, p := range writtenPathsIn(f, packageStrings(t, filepath.Dir(path))) {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				continue // not a committed file: a cache, a temp fixture
			}
			out[p] = filepath.ToSlash(dir)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking tools/: %v", err)
	}

	for rel, dir := range selfDeclaredArtifacts(t, root) {
		if _, ok := out[rel]; !ok {
			out[rel] = dir
		}
	}
	return out
}

// selfDeclaredArtifacts reads every committed .json under live/ and tools/
// and keeps the ones whose own generated_by names a tools/ generator.
func selfDeclaredArtifacts(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, glob := range []string{"live/*.json", "live/*/*.json", "tools/*/*.json"} {
		paths, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			t.Fatalf("globbing %s: %v", glob, err)
		}
		for _, p := range paths {
			rel, relErr := filepath.Rel(root, p)
			if relErr != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if strings.Contains(rel, "/testdata/") || strings.Contains(rel, "/overlay.d/") || strings.HasPrefix(rel, "live/history/") {
				continue
			}
			data, readErr := os.ReadFile(p) //nolint:gosec // a path inside the checkout
			if readErr != nil {
				continue
			}
			var head struct {
				GeneratedBy string `json:"generated_by"`
			}
			if json.Unmarshal(data, &head) != nil || head.GeneratedBy == "" {
				continue
			}
			i := strings.Index(head.GeneratedBy, "tools/")
			if i < 0 {
				continue
			}
			name := head.GeneratedBy[i+len("tools/"):]
			if j := strings.IndexAny(name, " \t)/"); j >= 0 {
				name = name[:j]
			}
			if name == "" {
				continue
			}
			if _, err := os.Stat(filepath.Join(root, "tools", name)); err != nil {
				continue
			}
			out[rel] = name
		}
	}
	return out
}

// packageStrings collects every package-level string value in dir that a
// write target could name: consts, vars, and flag.String defaults.
func packageStrings(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				if s, ok := stringValue(vs.Values[i], nil); ok {
					out[name.Name] = s
				}
			}
			return true
		})
		// A second pass for `out := flag.String(...)` and
		// `out = filepath.Join(root, X)` inside functions: the assignment
		// forms mapping-gen and survey-gen use for their -out.
		ast.Inspect(f, func(n ast.Node) bool {
			as, ok := n.(*ast.AssignStmt)
			if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			id, ok := as.Lhs[0].(*ast.Ident)
			if !ok {
				return true
			}
			if s, ok := stringValue(as.Rhs[0], out); ok {
				out[id.Name] = s
			}
			return true
		})
	}
	return out
}

// writtenPathsIn returns the resolved first argument of every os.WriteFile
// call in f.
func writtenPathsIn(f *ast.File, known map[string]string) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "WriteFile" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "os" {
			return true
		}
		if s, ok := stringValue(call.Args[0], known); ok {
			s = strings.TrimPrefix(filepath.ToSlash(s), "./")
			if strings.HasSuffix(s, ".json") && (strings.HasPrefix(s, "live/") || strings.HasPrefix(s, "tools/")) {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// stringValue resolves an expression to a string when it is one of the four
// spellings this repository uses for a write target. known carries the
// package's own string names; a nil map means literals only.
func stringValue(e ast.Expr, known map[string]string) (string, bool) {
	switch v := e.(type) {
	case *ast.BasicLit:
		if v.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(v.Value)
		return s, err == nil
	case *ast.Ident:
		if known == nil {
			return "", false
		}
		s, ok := known[v.Name]
		return s, ok
	case *ast.StarExpr: // *out, for a flag.String result
		return stringValue(v.X, known)
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if !ok {
			return "", false
		}
		pkg, _ := sel.X.(*ast.Ident)
		switch {
		case pkg != nil && pkg.Name == "flag" && sel.Sel.Name == "String" && len(v.Args) == 3:
			return stringValue(v.Args[1], known)
		case pkg != nil && pkg.Name == "filepath" && sel.Sel.Name == "FromSlash" && len(v.Args) == 1:
			return stringValue(v.Args[0], known)
		case pkg != nil && pkg.Name == "filepath" && sel.Sel.Name == "Join" && len(v.Args) == 2:
			// Join(root, rel): the first argument is the checkout root,
			// which is what makes the second one repo-relative.
			return stringValue(v.Args[1], known)
		}
		return "", false
	}
	return "", false
}

// readersOf returns every file naming rel, excluding the four things that do
// not count as a reader - see this file's own doc comment.
func readersOf(t *testing.T, root, rel, writerDir string) []string {
	t.Helper()
	var out []string
	writerPrefix := "tools/" + writerDir + "/"
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "vendor" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		r, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		r = filepath.ToSlash(r)
		switch {
		case r == rel, r == ".gitignore", r == "live/fork-surface.json", r == "live/artifact_readers_test.go":
			return nil
		case strings.HasPrefix(r, writerPrefix):
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil || info.Size() > 8<<20 {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec // a path inside the checkout
		if readErr != nil {
			return nil
		}
		if strings.Contains(string(data), rel) {
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the checkout for readers of %s: %v", rel, err)
	}
	return out
}
