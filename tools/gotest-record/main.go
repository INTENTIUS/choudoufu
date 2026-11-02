// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Command gotest-record writes the dated runs of the Go test proofs in
// live/smoke/claims.json (GitHub issue #2288). It is the only writer of
// live/smoke/evidence/go-test/: a run there is measured, never typed.
//
// A proof with no gate runs in CI's fast tier, and its run is a CI run at a
// commit:
//
//	go run ./tools/gotest-record -ci-run <GitHub Actions run id>
//
// reads the run with gh, requires it to be a successful push run of the CI
// workflow on main at a commit HEAD descends from, and records every
// ungated proof whose package the run's log reports ok and whose test
// exists at that commit.
//
// A proof behind a gate (a kind cluster, real AWS) is run by the maintainer,
// and its run is that run's `go test -v` log:
//
//	go run ./tools/gotest-record -log <file>
//
// records every gated proof the log shows passing (a skip is not a pass),
// at HEAD of a clean tree, and keeps the log beside the records.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/intentius/choudoufu/tools/statpages"
)

const (
	module = "github.com/intentius/choudoufu"
	repo   = "INTENTIUS/choudoufu"
)

// proof is a Go test proof as claims.json states it.
type proof struct {
	Test    string
	Package string
	Gate    string
}

func main() {
	ciRun := flag.String("ci-run", "", "record the ungated Go test proofs from this GitHub Actions run of the CI workflow")
	logPath := flag.String("log", "", "record the gated Go test proofs this maintainer `go test -v` log shows passing")
	root := flag.String("root", ".", "the repository root")
	flag.Parse()
	var err error
	switch {
	case *ciRun != "" && *logPath == "":
		err = recordCI(*root, *ciRun)
	case *logPath != "" && *ciRun == "":
		err = recordLog(*root, *logPath)
	default:
		err = errors.New("give exactly one of -ci-run or -log")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gotest-record:", err)
		os.Exit(1)
	}
}

func readProofs(root string) ([]proof, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(statpages.ClaimsRel)))
	if err != nil {
		return nil, err
	}
	var f struct {
		Claims []struct {
			Providers map[string]struct {
				Proofs []struct {
					GoTest    string `json:"go_test"`
					GoPackage string `json:"go_package"`
					Gate      string `json:"gate"`
				} `json:"proofs"`
			} `json:"providers"`
		} `json:"claims"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	var out []proof
	seen := map[string]bool{}
	for _, c := range f.Claims {
		for _, cell := range c.Providers {
			for _, p := range cell.Proofs {
				if p.GoTest == "" || seen[p.GoTest] {
					continue
				}
				seen[p.GoTest] = true
				out = append(out, proof{Test: p.GoTest, Package: p.GoPackage, Gate: p.Gate})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s lists no Go test proofs", statpages.ClaimsRel)
	}
	return out, nil
}

// importPath is a proof's "./pkg/x" package as go test prints it.
func importPath(pkg string) string {
	return path.Join(module, strings.TrimPrefix(pkg, "./"))
}

// packageOK reports whether a go test log says pkg passed.
func packageOK(log, pkg string) bool {
	re := regexp.MustCompile(`(?m)\bok\s+` + regexp.QuoteMeta(importPath(pkg)) + `\s`)
	return re.MatchString(log)
}

// testPassed reports why a `go test -v` log does not show test passing, or
// nil when it does: a `--- PASS` line for the test (each level of a
// subtest), no `--- SKIP` or `--- FAIL` for it, and its package ok.
func testPassed(log string, p proof) error {
	parts := strings.Split(p.Test, "/")
	for i := range parts {
		name := strings.Join(parts[:i+1], "/")
		q := regexp.QuoteMeta(name)
		if regexp.MustCompile(`(?m)--- (SKIP|FAIL): ` + q + ` \(`).MatchString(log) {
			return fmt.Errorf("the log shows %s skipped or failed", name)
		}
		if !regexp.MustCompile(`(?m)--- PASS: ` + q + ` \(`).MatchString(log) {
			return fmt.Errorf("the log has no --- PASS line for %s", name)
		}
	}
	if !packageOK(log, p.Package) {
		return fmt.Errorf("the log has no ok line for %s", importPath(p.Package))
	}
	return nil
}

func git(root string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// testExistsAt reports whether p's top-level test function is in its
// package at commit.
func testExistsAt(root, commit string, p proof) bool {
	top := strings.Split(p.Test, "/")[0]
	dir := strings.TrimPrefix(p.Package, "./")
	err := exec.Command("git", "-C", root, "grep", "-q", "-E", `^func `+regexp.QuoteMeta(top)+`\(`, commit, "--", dir+"/*_test.go").Run()
	return err == nil
}

func write(root string, rec statpages.GoTestRecord) error {
	rel := statpages.GoTestEvidenceRel(rec.Test)
	full := filepath.Join(root, filepath.FromSlash(rel))
	if old, err := statpages.ReadGoTestRecord(root, rec.Test); err == nil && old != nil && old.Date > rec.Date {
		fmt.Printf("kept %s: it records a newer run (%s) than this one (%s)\n", rel, old.Date, rec.Date)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(full, append(b, '\n'), 0o644); err != nil { //nolint:gosec // a committed record
		return err
	}
	fmt.Printf("recorded %s: %s at %s on %s\n", rel, rec.Test, rec.Commit[:10], rec.Date)
	return nil
}

func recordCI(root, id string) error {
	proofs, err := readProofs(root)
	if err != nil {
		return err
	}
	raw, err := exec.Command("gh", "run", "view", id, "-R", repo, "--json", "headSha,conclusion,createdAt,url,workflowName,event,headBranch").Output()
	if err != nil {
		return fmt.Errorf("gh run view %s: %w", id, err)
	}
	var run struct {
		HeadSha, Conclusion, CreatedAt, URL, WorkflowName, Event, HeadBranch string
	}
	if err := json.Unmarshal(raw, &run); err != nil {
		return err
	}
	if run.WorkflowName != "CI" || run.Event != "push" || run.HeadBranch != "main" || run.Conclusion != "success" {
		return fmt.Errorf("run %s is %q on %s %s and reads %q; a CI test's dated run is a successful push run of the CI workflow on main", id, run.WorkflowName, run.Event, run.HeadBranch, run.Conclusion)
	}
	if err := exec.Command("git", "-C", root, "merge-base", "--is-ancestor", run.HeadSha, "HEAD").Run(); err != nil {
		return fmt.Errorf("run %s tested %s, which is not an ancestor of HEAD (fetch, or record from a run this branch descends from)", id, run.HeadSha)
	}
	log, err := exec.Command("gh", "run", "view", id, "-R", repo, "--log").Output()
	if err != nil {
		return fmt.Errorf("gh run view %s --log: %w", id, err)
	}
	recorded := 0
	for _, p := range proofs {
		if p.Gate != "" {
			continue
		}
		switch {
		case !testExistsAt(root, run.HeadSha, p):
			fmt.Printf("waiting %s: it is not in %s at %s, so run %s did not run it\n", p.Test, p.Package, run.HeadSha[:10], id)
		case !packageOK(string(log), p.Package):
			return fmt.Errorf("run %s's log has no ok line for %s, so it does not show %s passing", id, importPath(p.Package), p.Test)
		default:
			if err := write(root, statpages.GoTestRecord{
				Test: p.Test, Package: p.Package, Status: "pass",
				Commit: run.HeadSha, Date: run.CreatedAt[:10], CIRun: run.URL,
			}); err != nil {
				return err
			}
			recorded++
		}
	}
	fmt.Printf("%d CI test proof(s) recorded from %s\n", recorded, run.URL)
	return nil
}

func recordLog(root, logPath string) error {
	proofs, err := readProofs(root)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	if dirty, err := git(root, "status", "--porcelain", "--untracked-files=no", "--", ".", ":!"+statpages.GoTestEvidenceDir); err != nil {
		return err
	} else if dirty != "" {
		return fmt.Errorf("the tree has uncommitted changes, so HEAD is not what the log ran:\n%s", dirty)
	}
	head, err := git(root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	date := time.Now().UTC().Format("2006-01-02")
	logRel := statpages.GoTestEvidenceDir + "/" + date + "." + head[:10] + ".log"
	var recs []statpages.GoTestRecord
	for _, p := range proofs {
		if p.Gate == "" {
			continue
		}
		if err := testPassed(string(raw), p); err != nil {
			fmt.Printf("not recorded %s: %v\n", p.Test, err)
			continue
		}
		recs = append(recs, statpages.GoTestRecord{
			Test: p.Test, Package: p.Package, Status: "pass", Commit: head, Date: date, Log: logRel,
		})
	}
	if len(recs) == 0 {
		return fmt.Errorf("%s shows no gated Go test proof passing", logPath)
	}
	full := filepath.Join(root, filepath.FromSlash(logRel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(full, raw, 0o644); err != nil { //nolint:gosec // a committed log
		return err
	}
	for _, rec := range recs {
		if err := write(root, rec); err != nil {
			return err
		}
	}
	return nil
}
