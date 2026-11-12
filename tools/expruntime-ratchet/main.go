// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command expruntime-ratchet is the experimental-runtime lane's ratchet
// (#2256 U3, #2338). It reads the `go test -json` output of a run with
// TOFU_X_EXPERIMENTAL_RUNTIME=1 and CHOUDOUFU_NEW_RUNTIME_STRICT=1 (#2397:
// refuse, not fall back to the default runtime) and checks it against the
// checked-in expected-failures list, live/experimental-runtime/expected-failures.json:
//
//   - a listed test that passes fails the job until it is taken off the list;
//   - an unlisted test that fails fails the job (a regression, or something
//     upstream broke);
//   - a SkipExperimental call in a fork-owned test fails the job unless the
//     list's "skips" names that test with a reason. Without this a skip
//     would take a test off the list without it ever passing. A call is
//     upstream-owned, and left alone, when the same test function in the
//     same directory calls SkipExperimental in the raw upstream graft.
//
// Every list entry needs a reason. Skips are counted and printed.
//
// Usage:
//
//	TOFU_X_EXPERIMENTAL_RUNTIME=1 CHOUDOUFU_NEW_RUNTIME_STRICT=1 go test -json ./internal/tofu ... > results.json
//	go run ./tools/expruntime-ratchet -results results.json
//
// The last line is the verdict, "RATCHET: PASS ..." or "RATCHET: FAIL ...".
// Read that line, not the exit code alone.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const modulePath = "github.com/intentius/choudoufu"

// skipMessagePrefix is what SkipExperimental passes to t.Skip
// (internal/tofu/context_temp_runtime_test.go).
const skipMessagePrefix = "New Engine: "

// Entry is one row of the expected-failures list.
type Entry struct {
	Package string `json:"package"`
	Test    string `json:"test"`
	Reason  string `json:"reason"`
}

func (e Entry) key() testKey { return testKey{e.Package, e.Test} }

// List is the checked-in expected-failures file.
type List struct {
	Comment  string  `json:"comment,omitempty"`
	Failures []Entry `json:"failures"`
	Skips    []Entry `json:"skips"`
}

type testKey struct {
	Package string
	Test    string
}

func (k testKey) String() string { return strings.TrimPrefix(k.Package, modulePath+"/") + "." + k.Test }

// packageAbortKey names the pseudo-test a package-level panic or timeout is
// recorded under. It never has a result, so Check reports it.
const packageAbortKey = "(package aborted)"

// Outcome is a top-level test's result in a run.
type Outcome struct {
	Result string // "pass", "fail" or "skip"
	// ExperimentalSkip is true when the test or one of its subtests was
	// skipped by SkipExperimental.
	ExperimentalSkip bool
}

// testEvent is the subset of `go test -json` (test2json) we read.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// ParseResults reads test2json output into one outcome per top-level test.
// A failing subtest fails its parent in Go's own reporting, so top-level
// granularity loses nothing.
func ParseResults(r io.Reader) (map[testKey]*Outcome, error) {
	out := map[testKey]*Outcome{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("reading test2json: %w", err)
		}
		if ev.Test == "" {
			// A panic or a timeout outside any test's own output still
			// aborts the package: record it so Check can refuse the run.
			if ev.Action == "output" && (strings.Contains(ev.Output, "panic:") || strings.Contains(ev.Output, "test timed out")) {
				k := testKey{ev.Package, packageAbortKey}
				if out[k] == nil {
					out[k] = &Outcome{}
				}
			}
			continue
		}
		top := ev.Test
		if i := strings.IndexByte(top, '/'); i >= 0 {
			top = top[:i]
		}
		k := testKey{ev.Package, top}
		o := out[k]
		if o == nil {
			o = &Outcome{}
			out[k] = o
		}
		switch ev.Action {
		case "output":
			if strings.Contains(ev.Output, skipMessagePrefix) {
				o.ExperimentalSkip = true
			}
			// A panic Go attributes to this test still aborts the whole
			// package binary: the test reads as failed, and the tests that
			// would have run after it never start, so nothing else shows
			// they are missing.
			if strings.HasPrefix(ev.Output, "panic: ") {
				ak := testKey{ev.Package, packageAbortKey}
				if out[ak] == nil {
					out[ak] = &Outcome{}
				}
			}
		case "pass", "fail", "skip":
			if ev.Test == top {
				o.Result = ev.Action
			}
		}
	}
	return out, sc.Err()
}

// SkipSite is a test function that calls SkipExperimental.
type SkipSite struct {
	Dir  string // slash-separated, relative to the repository root
	Test string
}

// FindSkipSites parses the _test.go files given as (path, source) pairs and
// returns every Test function whose body calls SkipExperimental.
func FindSkipSites(files map[string][]byte) ([]SkipSite, error) {
	var sites []SkipSite
	fset := token.NewFileSet()
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return !found
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					found = found || fun.Name == "SkipExperimental"
				case *ast.SelectorExpr:
					found = found || fun.Sel.Name == "SkipExperimental"
				}
				return !found
			})
			if found {
				sites = append(sites, SkipSite{Dir: path.Dir(filepath.ToSlash(name)), Test: fn.Name.Name})
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Dir != sites[j].Dir {
			return sites[i].Dir < sites[j].Dir
		}
		return sites[i].Test < sites[j].Test
	})
	return sites, nil
}

// Report is what Check found.
type Report struct {
	Problems []string

	Passed, Failed, Skipped          int
	ExperimentalSkips                int
	ForkSkipSites, UpstreamSkipSites int
	// Unfinished counts tests that started and reported no result: their
	// package panicked or timed out, and every test after them in that
	// package never ran, so the run measured less than it claims.
	Unfinished int
}

// Check applies the ratchet's rules. headSites are the SkipExperimental call
// sites in the tree under test; upstreamSites are those in the raw upstream
// graft.
func Check(results map[testKey]*Outcome, list List, headSites, upstreamSites []SkipSite) Report {
	var r Report
	problem := func(format string, args ...any) { r.Problems = append(r.Problems, fmt.Sprintf(format, args...)) }

	for _, o := range results {
		switch o.Result {
		case "pass":
			r.Passed++
		case "fail":
			r.Failed++
		case "skip":
			r.Skipped++
		default:
			r.Unfinished++
		}
		if o.ExperimentalSkip {
			r.ExperimentalSkips++
		}
	}

	listedFail := map[testKey]bool{}
	for _, e := range list.Failures {
		if strings.TrimSpace(e.Reason) == "" {
			problem("list entry %s has no reason", e.key())
		}
		if listedFail[e.key()] {
			problem("list names %s twice", e.key())
		}
		listedFail[e.key()] = true
		o := results[e.key()]
		switch {
		case o == nil:
			problem("listed failure %s did not run: take it off the list or fix its name", e.key())
		case o.Result == "pass":
			problem("listed failure %s now passes: take it off the list", e.key())
		case o.Result == "skip":
			problem("listed failure %s was skipped, so it neither failed nor passed: a skip cannot stand in for a listed failure", e.key())
		}
	}

	var keys []testKey
	for k := range results {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, k := range keys {
		if results[k].Result == "" {
			problem("%s started and never finished: its package panicked or timed out, so the tests after it never ran and this run is incomplete", k)
			continue
		}
		if results[k].Result == "fail" && !listedFail[k] {
			problem("unlisted failure %s: a regression, or something upstream broke", k)
		}
	}

	upstream := map[SkipSite]bool{}
	for _, s := range upstreamSites {
		upstream[s] = true
	}
	listedSkip := map[testKey]bool{}
	listedSkipByDir := map[SkipSite]bool{}
	for _, e := range list.Skips {
		if strings.TrimSpace(e.Reason) == "" {
			problem("skip entry %s has no reason", e.key())
		}
		listedSkip[e.key()] = true
		listedSkipByDir[SkipSite{Dir: strings.TrimPrefix(e.Package, modulePath+"/"), Test: e.Test}] = true
	}
	headSet := map[SkipSite]bool{}
	for _, s := range headSites {
		headSet[s] = true
		if upstream[s] {
			r.UpstreamSkipSites++
			continue
		}
		r.ForkSkipSites++
		if !listedSkipByDir[s] {
			problem("fork-owned test %s.%s calls SkipExperimental and the list's skips do not name it with a reason", s.Dir, s.Test)
		}
	}
	for _, e := range list.Skips {
		s := SkipSite{Dir: strings.TrimPrefix(e.Package, modulePath+"/"), Test: e.Test}
		if !headSet[s] {
			problem("skip entry %s names a test that no longer calls SkipExperimental: take it off the list", e.key())
		}
	}
	return r
}

func main() {
	resultsPath := flag.String("results", "", "test2json output of the experimental run (required)")
	listPath := flag.String("list", "live/experimental-runtime/expected-failures.json", "expected-failures list")
	upstreamRef := flag.String("upstream-ref", "", "raw upstream graft to tell upstream-owned SkipExperimental calls from the fork's (default: the newest commit whose subject starts \"graft:\")")
	flag.Parse()

	if err := run(*resultsPath, *listPath, *upstreamRef); err != nil {
		fmt.Println("RATCHET: FAIL", err)
		os.Exit(1)
	}
}

func run(resultsPath, listPath, upstreamRef string) error {
	if resultsPath == "" {
		return fmt.Errorf("-results is required")
	}
	rf, err := os.Open(resultsPath)
	if err != nil {
		return err
	}
	defer rf.Close()
	results, err := ParseResults(rf)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("%s holds no test results: a run that measured nothing is not a pass", resultsPath)
	}

	raw, err := os.ReadFile(listPath)
	if err != nil {
		return err
	}
	var list List
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&list); err != nil {
		return fmt.Errorf("reading %s: %w", listPath, err)
	}

	dirs := map[string]bool{}
	for k := range results {
		if dir, ok := strings.CutPrefix(k.Package, modulePath+"/"); ok {
			dirs[dir] = true
		}
	}

	if upstreamRef == "" {
		out, err := exec.Command("git", "log", "-1", "--format=%H", "--grep=^graft:", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return fmt.Errorf("no raw upstream graft found from HEAD (pass -upstream-ref): %v", err)
		}
		upstreamRef = strings.TrimSpace(string(out))
	}

	headFiles, err := testFilesOnDisk(dirs)
	if err != nil {
		return err
	}
	upstreamFiles, err := testFilesAtRef(upstreamRef, dirs)
	if err != nil {
		return err
	}
	headSites, err := FindSkipSites(headFiles)
	if err != nil {
		return err
	}
	upstreamSites, err := FindSkipSites(upstreamFiles)
	if err != nil {
		return err
	}

	r := Check(results, list, headSites, upstreamSites)
	fmt.Printf("tests: %d passed, %d failed, %d skipped (%d by SkipExperimental), %d unfinished\n", r.Passed, r.Failed, r.Skipped, r.ExperimentalSkips, r.Unfinished)
	fmt.Printf("SkipExperimental call sites: %d upstream-owned, %d fork-owned\n", r.UpstreamSkipSites, r.ForkSkipSites)
	fmt.Printf("list: %d expected failure(s), %d listed skip(s)\n", len(list.Failures), len(list.Skips))
	for _, p := range r.Problems {
		fmt.Println("problem:", p)
	}
	if len(r.Problems) > 0 {
		return fmt.Errorf("%d problem(s)", len(r.Problems))
	}
	fmt.Printf("RATCHET: PASS %d expected failure(s), %d skipped by SkipExperimental\n", len(list.Failures), r.ExperimentalSkips)
	return nil
}

func testFilesOnDisk(dirs map[string]bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	for dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(filepath.FromSlash(dir), "*_test.go"))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			b, err := os.ReadFile(m)
			if err != nil {
				return nil, err
			}
			files[filepath.ToSlash(m)] = b
		}
	}
	return files, nil
}

func testFilesAtRef(ref string, dirs map[string]bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	for dir := range dirs {
		out, err := exec.Command("git", "ls-tree", "--name-only", ref, dir+"/").Output()
		if err != nil {
			return nil, fmt.Errorf("listing %s at %s: %w", dir, ref, err)
		}
		for _, name := range strings.Fields(string(out)) {
			if !strings.HasSuffix(name, "_test.go") {
				continue
			}
			b, err := exec.Command("git", "show", ref+":"+name).Output()
			if err != nil {
				return nil, fmt.Errorf("reading %s at %s: %w", name, ref, err)
			}
			files[name] = b
		}
	}
	return files, nil
}
