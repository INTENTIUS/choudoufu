// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Issue #1596, the Kubernetes-side instance of the #591/#623/#691 class:
// every CHOUDOUFU_K8S_*-gated test read its own environment variable
// directly and skipped, so nothing said whether the tier ran at all -
// internal/live/projection/fenced_live_test.go:135's
// TestFencedIdentityOnACluster in particular was invoked by nothing, on any
// schedule, anywhere. internal/live/k8stest gives every such test a shared
// switch (k8stest.Gate, beside internal/live/flocitest.Gate for the AWS
// side); this file is what live/floci_tier_gate_test.go is for that switch.
//
// Issue #1741: the first version of this guard counted strings. Deleting
// `CHOUDOUFU_K8S_TEST: "1"` from kind-tier.yml still passed, because the
// header comment names the variable twice; and renaming
// TestKubernetesTwoWritersOneRecord still passed, because only the
// package was checked, so the nightly would have printed `testing:
// warning: no tests to run` and PASS forever. It now reads the workflow as
// YAML and each `go test` as shell words, and holds three things to it:
//
//   - every step that runs `go test` carries CHOUDOUFU_K8S_TEST="1" in its
//     effective env (workflow, job and step env merged), as a value, not a
//     mention;
//   - every alternative in every `-run` regex matches a real `func TestX`
//     in the package pattern that step names;
//   - every Test function that reaches k8stest.Gate, directly or through a
//     helper in its own package, is selected by some such step. The roster
//     is derived from the Go source, never hand-listed.
//
// The rules live in kindTierFindings so the violations #1741 names are fed
// to it as fixtures below, and stay provably red.
//
// live/nightly_watch_test.go separately holds nightly-watch.yml to every
// workflow file that carries a cron, kind-tier.yml included, so a red
// kind-tier night cannot go unnoticed the way seventeen red floci-tier
// nights did (#1316).

const kindTierWorkflow = ".github/workflows/kind-tier.yml"

// ktWorkflow is the slice of a GitHub workflow this guard reads.
type ktWorkflow struct {
	On   map[string]any `yaml:"on"`
	Env  map[string]string
	Jobs map[string]struct {
		Env   map[string]string
		Steps []struct {
			Name string
			Run  string
			Env  map[string]string
		}
	}
}

// ktGoTest is one `go test` invocation found in a step's run script.
type ktGoTest struct {
	where string   // job/step, for messages
	pkgs  []string // package patterns, as written (./x/y or ./x/...)
	run   string   // the -run value, "" when absent
	env   map[string]string
}

// ktPackage is what the guard knows about one Go package directory: the
// Test functions it declares and which of them reach k8stest.Gate.
type ktPackage struct {
	tests []string
	gated []string
}

func TestKindTierCoversEveryGatedPackage(t *testing.T) {
	root := repoRoot(t)

	wf, readErr := os.ReadFile(filepath.Join(root, kindTierWorkflow))
	if readErr != nil {
		t.Fatalf("%s is missing (%v); the Kubernetes live tier gates nothing without a workflow that sets k8stest.EnvVar and runs it (issue #1596)", kindTierWorkflow, readErr)
	}

	// The pattern requires the call's open paren, so this file - whose prose
	// names k8stest.Gate without calling it - cannot match itself. The
	// runtime string is a single backslash before the dot, but the source
	// that produces it needs two, so this file's own bytes never contain the
	// literal sequence being searched for.
	out, err := exec.Command("git", "-C", root, "grep", "-l", "k8stest\\.Gate(", "--", "*_test.go").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("enumerating k8stest.Gate call sites: %v", err)
		}
	}
	gatedDirs := map[string]bool{}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" {
			gatedDirs[filepath.ToSlash(filepath.Dir(f))] = true
		}
	}
	if len(gatedDirs) < 2 {
		t.Fatalf("found only %d packages calling k8stest.Gate; want at least internal/live/staterecord and internal/live/projection (issue #1596 asks that every CHOUDOUFU_K8S_*-gated test use it)", len(gatedDirs))
	}

	pkgs := ktLoadPackages(t, root)
	gatedCount := 0
	for dir := range gatedDirs {
		p, ok := pkgs[dir]
		if !ok || len(p.gated) == 0 {
			t.Errorf("%s calls k8stest.Gate, but no Test function in it reaches the call; the guard's call-graph reading is broken", dir)
			continue
		}
		gatedCount += len(p.gated)
	}
	if _, ok := pkgs["internal/live/projection"]; !ok || !containsString(pkgs["internal/live/projection"].gated, "TestFencedIdentityOnACluster") {
		t.Errorf("TestFencedIdentityOnACluster is not among the gated tests the guard derived; issue #1596 exists because that test is invoked by nothing")
	}

	findings := kindTierFindings(wf, pkgs)
	for _, f := range findings {
		t.Error(f)
	}
	t.Logf("checked %d gated test function(s) in %d package(s) against %s", gatedCount, len(gatedDirs), kindTierWorkflow)
}

// TestKindTierGateIsRedOnTheViolationsIssue1741Names feeds kindTierFindings
// the two mutations #1741 proved green against the old string counter.
func TestKindTierGateIsRedOnTheViolationsIssue1741Names(t *testing.T) {
	root := repoRoot(t)
	wf, err := os.ReadFile(filepath.Join(root, kindTierWorkflow))
	if err != nil {
		t.Fatal(err)
	}
	pkgs := ktLoadPackages(t, root)
	// Each mutant must add a finding the real file does not already have;
	// the real file's own findings are TestKindTierCoversEveryGatedPackage's
	// to report.
	base := map[string]bool{}
	for _, f := range kindTierFindings(wf, pkgs) {
		base[f] = true
	}
	newFinding := func(got []string, s string) bool {
		for _, f := range got {
			if !base[f] && strings.Contains(f, s) {
				t.Logf("red, as it must be: %s", f)
				return true
			}
		}
		return false
	}

	envLine := regexp.MustCompile(`(?m)^\s*CHOUDOUFU_K8S_TEST: "1"\s*\n`)
	if !envLine.Match(wf) {
		t.Fatal(`kind-tier.yml carries no CHOUDOUFU_K8S_TEST: "1" line to delete; the mutant would be vacuous`)
	}
	noEnv := envLine.ReplaceAll(wf, nil)
	if !strings.Contains(string(noEnv), "CHOUDOUFU_K8S_TEST") {
		t.Fatal("the mutant no longer mentions CHOUDOUFU_K8S_TEST anywhere; it must, to prove a comment cannot satisfy the guard")
	}
	if !newFinding(kindTierFindings(noEnv, pkgs), "CHOUDOUFU_K8S_TEST") {
		t.Error(`deleting CHOUDOUFU_K8S_TEST: "1" from kind-tier.yml, with the variable still named in its comments, produced no finding`)
	}

	const name = "TestKubernetesTwoWritersOneRecord"
	if !strings.Contains(string(wf), name) {
		t.Fatalf("kind-tier.yml no longer names %s; pick another name for the rename mutant", name)
	}
	renamed := pkgs["internal/live/staterecord"]
	renamed.tests = ktReplace(renamed.tests, name, "TestRecordRaceTwoWriters")
	renamed.gated = ktReplace(renamed.gated, name, "TestRecordRaceTwoWriters")
	mut := map[string]ktPackage{}
	for k, v := range pkgs {
		mut[k] = v
	}
	mut["internal/live/staterecord"] = renamed
	got := kindTierFindings(wf, mut)
	if !newFinding(got, name) {
		t.Errorf("renaming %s produced no finding naming it: %v", name, got)
	}

	// A `-run` in a comment, or a regex whose one alternative names no
	// test, must not count either.
	orphan := strings.Replace(string(wf), name, "TestNoSuchTestAnywhere", 1)
	if !newFinding(kindTierFindings([]byte(orphan), pkgs), "TestNoSuchTestAnywhere") {
		t.Error("a -run alternative naming no real test produced no finding")
	}
}

func ktReplace(xs []string, from, to string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		if x == from {
			x = to
		}
		out[i] = x
	}
	return out
}

// kindTierFindings applies the three rules above to a kind-tier workflow
// and the package facts ktLoadPackages read. Each finding is one sentence
// naming what failed.
func kindTierFindings(wfSrc []byte, pkgs map[string]ktPackage) []string {
	var wf ktWorkflow
	if err := yaml.Unmarshal(wfSrc, &wf); err != nil {
		return []string{fmt.Sprintf("%s does not parse as YAML: %v", kindTierWorkflow, err)}
	}
	var findings []string
	if _, ok := wf.On["schedule"]; !ok {
		findings = append(findings, "kind-tier.yml has no schedule trigger; a workflow that only runs on workflow_dispatch still depends on a human remembering to type it")
	}

	var runs []ktGoTest
	jobNames := make([]string, 0, len(wf.Jobs))
	for n := range wf.Jobs {
		jobNames = append(jobNames, n)
	}
	sort.Strings(jobNames)
	for _, jn := range jobNames {
		job := wf.Jobs[jn]
		for i, st := range job.Steps {
			env := map[string]string{}
			for _, m := range []map[string]string{wf.Env, job.Env, st.Env} {
				for k, v := range m {
					env[k] = v
				}
			}
			where := fmt.Sprintf("job %s step %d (%s)", jn, i+1, st.Name)
			for _, gt := range ktGoTests(st.Run) {
				gt.where, gt.env = where, env
				runs = append(runs, gt)
			}
		}
	}
	if len(runs) == 0 {
		return append(findings, "kind-tier.yml never runs `go test`; it does not run the gated roster at all")
	}

	for _, r := range runs {
		if r.env["CHOUDOUFU_K8S_TEST"] != "1" {
			findings = append(findings, fmt.Sprintf("%s runs `go test %s` without CHOUDOUFU_K8S_TEST=\"1\" in its env (workflow, job or step); every k8stest.Gate test in it skips and the step measures nothing (issue #1596). A mention in a comment is not the value", r.where, strings.Join(r.pkgs, " ")))
		}
		if len(r.pkgs) == 0 {
			findings = append(findings, fmt.Sprintf("%s runs `go test` with no ./package argument the guard can read", r.where))
			continue
		}
		if r.run == "" {
			continue
		}
		var names []string
		for _, p := range r.pkgs {
			for dir, pk := range pkgs {
				if ktPatternCovers(p, dir) {
					names = append(names, pk.tests...)
				}
			}
		}
		alts, err := ktRunAlternatives(r.run)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: -run %q does not parse: %v", r.where, r.run, err))
			continue
		}
		for _, alt := range alts {
			re, err := regexp.Compile(alt)
			if err != nil {
				findings = append(findings, fmt.Sprintf("%s: -run alternative %q does not compile: %v", r.where, alt, err))
				continue
			}
			hit := false
			for _, n := range names {
				if re.MatchString(n) {
					hit = true
					break
				}
			}
			if !hit {
				findings = append(findings, fmt.Sprintf("%s: -run alternative %q matches no func Test in %s; the step would print `testing: warning: no tests to run` and PASS", r.where, alt, strings.Join(r.pkgs, " ")))
			}
		}
	}

	dirs := make([]string, 0, len(pkgs))
	for d := range pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		for _, test := range pkgs[dir].gated {
			if !ktSelected(runs, dir, test) {
				findings = append(findings, fmt.Sprintf("%s.%s reaches k8stest.Gate but no `go test` step in kind-tier.yml with CHOUDOUFU_K8S_TEST=\"1\" selects it (package pattern and -run); it runs nowhere", dir, test))
			}
		}
	}
	return findings
}

func ktSelected(runs []ktGoTest, dir, test string) bool {
	for _, r := range runs {
		if r.env["CHOUDOUFU_K8S_TEST"] != "1" {
			continue
		}
		covered := false
		for _, p := range r.pkgs {
			if ktPatternCovers(p, dir) {
				covered = true
			}
		}
		if !covered {
			continue
		}
		if r.run == "" {
			return true
		}
		top := ktTopLevel(r.run)
		if re, err := regexp.Compile(top); err == nil && re.MatchString(test) {
			return true
		}
	}
	return false
}

// ktPatternCovers reports whether a `go test` package argument selects the
// package in dir (slash-separated, relative to the repository root).
func ktPatternCovers(pattern, dir string) bool {
	p := strings.TrimPrefix(pattern, "./")
	if base, ok := strings.CutSuffix(p, "/..."); ok {
		return dir == base || strings.HasPrefix(dir, base+"/")
	}
	if p == "..." {
		return true
	}
	return dir == strings.TrimSuffix(p, "/")
}

// ktTopLevel is the part of a -run value that selects top-level tests:
// `go test` splits the value on unbracketed slashes, one regex per level.
func ktTopLevel(run string) string {
	depth := 0
	for i, c := range run {
		switch c {
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		case '/':
			if depth == 0 {
				return run[:i]
			}
		}
	}
	return run
}

// ktRunAlternatives expands a -run value's top level into one regex per
// alternative, through groups: `^(A|B)$` yields `^A$` and `^B$`, so a
// renamed B cannot hide behind a still-matching A.
func ktRunAlternatives(run string) ([]string, error) {
	re, err := syntax.Parse(ktTopLevel(run), syntax.Perl)
	if err != nil {
		return nil, err
	}
	var expand func(*syntax.Regexp) []string
	expand = func(r *syntax.Regexp) []string {
		switch r.Op {
		case syntax.OpAlternate:
			var out []string
			for _, s := range r.Sub {
				out = append(out, expand(s)...)
			}
			return out
		case syntax.OpCapture:
			return expand(r.Sub[0])
		case syntax.OpConcat:
			out := []string{""}
			for _, s := range r.Sub {
				var next []string
				for _, prefix := range out {
					for _, b := range expand(s) {
						next = append(next, prefix+b)
					}
				}
				out = next
				if len(out) > 256 {
					return []string{r.String()}
				}
			}
			return out
		default:
			return []string{r.String()}
		}
	}
	return expand(re), nil
}

// ktGoTests finds each `go test` command in a run script and reads its
// package arguments and -run value as shell words, so a quoted regex with
// `|` in it is one argument, and a `# comment` is not a command.
func ktGoTests(script string) []ktGoTest {
	var out []ktGoTest
	src := strings.ReplaceAll(script, "\\\n", " ")
	for _, line := range strings.Split(src, "\n") {
		words := ktShellWords(line)
		for i := 0; i+1 < len(words); i++ {
			if words[i] != "go" || words[i+1] != "test" {
				continue
			}
			var gt ktGoTest
			for j := i + 2; j < len(words); j++ {
				w := words[j]
				if w == ";" || w == "&&" || w == "||" || w == "|" {
					break
				}
				switch {
				case w == "-run" && j+1 < len(words):
					gt.run = words[j+1]
					j++
				case strings.HasPrefix(w, "-run="):
					gt.run = strings.TrimPrefix(w, "-run=")
				case strings.HasPrefix(w, "./"):
					gt.pkgs = append(gt.pkgs, w)
				}
			}
			out = append(out, gt)
		}
	}
	return out
}

// ktShellWords splits one line of shell into words, honouring single and
// double quotes, stopping at an unquoted `#` that starts a word, and
// emitting ; && || | as words of their own.
func ktShellWords(line string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(line[i+1:], '\'')
			if j < 0 {
				j = len(line) - i - 1
			}
			cur.WriteString(line[i+1 : i+1+j])
			inWord = true
			i += j + 1
		case c == '"':
			i++
			for i < len(line) && line[i] != '"' {
				if line[i] == '\\' && i+1 < len(line) {
					i++
				}
				cur.WriteByte(line[i])
				i++
			}
			inWord = true
		case c == ' ' || c == '\t':
			flush()
		case c == '#' && !inWord:
			flush()
			return words
		case c == ';' || c == '|' || c == '&':
			flush()
			op := string(c)
			if i+1 < len(line) && (line[i+1] == '|' || line[i+1] == '&') && line[i+1] == c {
				op += string(c)
				i++
			}
			words = append(words, op)
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return words
}

// ktLoadPackages reads every hand-written _test.go under internal/ and
// cmd/-shaped roots once: the Test functions each package declares, and
// which of them reach k8stest.Gate through calls inside their own package.
func ktLoadPackages(t *testing.T, root string) map[string]ktPackage {
	t.Helper()
	files := map[string][]*ast.File{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, filepath.Dir(path))
		rel = filepath.ToSlash(rel)
		files[rel] = append(files[rel], f)
		return nil
	})
	if err != nil {
		t.Fatalf("reading test files: %v", err)
	}
	out := map[string]ktPackage{}
	for dir, fs := range files {
		out[dir] = ktPackageFacts(fs)
	}
	return out
}

func ktPackageFacts(files []*ast.File) ktPackage {
	calls := map[string]map[string]bool{} // func name -> names it calls
	direct := map[string]bool{}           // funcs calling k8stest.Gate
	var tests []string
	for _, f := range files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv == nil && strings.HasPrefix(name, "Test") && ktTakesTestingT(fd) {
				tests = append(tests, name)
			}
			if calls[name] == nil {
				calls[name] = map[string]bool{}
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fn := ce.Fun.(type) {
				case *ast.Ident:
					calls[name][fn.Name] = true
				case *ast.SelectorExpr:
					if x, ok := fn.X.(*ast.Ident); ok && x.Name == "k8stest" && fn.Sel.Name == "Gate" {
						direct[name] = true
					} else {
						calls[name][fn.Sel.Name] = true
					}
				}
				return true
			})
		}
	}
	gated := map[string]bool{}
	for n := range direct {
		gated[n] = true
	}
	for changed := true; changed; {
		changed = false
		for fn, callees := range calls {
			if gated[fn] {
				continue
			}
			for c := range callees {
				if gated[c] {
					gated[fn] = true
					changed = true
					break
				}
			}
		}
	}
	p := ktPackage{tests: tests}
	for _, tn := range tests {
		if gated[tn] {
			p.gated = append(p.gated, tn)
		}
	}
	sort.Strings(p.tests)
	sort.Strings(p.gated)
	return p
}

func ktTakesTestingT(fd *ast.FuncDecl) bool {
	ps := fd.Type.Params.List
	if len(ps) != 1 {
		return false
	}
	star, ok := ps[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}
