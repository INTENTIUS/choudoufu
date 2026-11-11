// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

const tofuPkg = modulePath + "/internal/tofu"

// run2json is a small test2json stream: TestFails fails, TestPasses passes,
// TestUpstreamSkip and TestForkSkip are skipped by SkipExperimental.
const run2json = `{"Action":"run","Package":"` + tofuPkg + `","Test":"TestFails"}
{"Action":"run","Package":"` + tofuPkg + `","Test":"TestFails/sub"}
{"Action":"fail","Package":"` + tofuPkg + `","Test":"TestFails/sub"}
{"Action":"fail","Package":"` + tofuPkg + `","Test":"TestFails"}
{"Action":"run","Package":"` + tofuPkg + `","Test":"TestPasses"}
{"Action":"pass","Package":"` + tofuPkg + `","Test":"TestPasses"}
{"Action":"run","Package":"` + tofuPkg + `","Test":"TestUpstreamSkip"}
{"Action":"output","Package":"` + tofuPkg + `","Test":"TestUpstreamSkip","Output":"    x_test.go:9: New Engine: Missing Removed\n"}
{"Action":"skip","Package":"` + tofuPkg + `","Test":"TestUpstreamSkip"}
{"Action":"run","Package":"` + tofuPkg + `","Test":"TestForkSkip"}
{"Action":"output","Package":"` + tofuPkg + `","Test":"TestForkSkip","Output":"    y_test.go:9: New Engine: Missing Taint\n"}
{"Action":"skip","Package":"` + tofuPkg + `","Test":"TestForkSkip"}
{"Action":"fail","Package":"` + tofuPkg + `"}
`

const upstreamSrc = `package tofu
import "testing"
func TestUpstreamSkip(t *testing.T) { SkipExperimental(t, ExperimentalFeatureRemoved) }
func TestPasses(t *testing.T) {}
`

const forkSrc = `package tofu
import "testing"
func TestForkSkip(t *testing.T) {
	t.Run("x", func(t *testing.T) { SkipExperimental(t, ExperimentalFeatureTaint) })
}
func TestFails(t *testing.T) {}
`

// baseline is a list that matches run2json exactly.
func baseline() List {
	return List{
		Failures: []Entry{{Package: tofuPkg, Test: "TestFails", Reason: "markers are not ported yet (U4)"}},
		Skips:    []Entry{{Package: tofuPkg, Test: "TestForkSkip", Reason: "taint on the new runtime is upstream's"}},
	}
}

func checkWith(t *testing.T, list List, forkSource string) Report {
	t.Helper()
	results, err := ParseResults(strings.NewReader(run2json))
	if err != nil {
		t.Fatal(err)
	}
	head, err := FindSkipSites(map[string][]byte{
		"internal/tofu/upstream_test.go": []byte(upstreamSrc),
		"internal/tofu/fork_test.go":     []byte(forkSource),
	})
	if err != nil {
		t.Fatal(err)
	}
	up, err := FindSkipSites(map[string][]byte{"internal/tofu/upstream_test.go": []byte(upstreamSrc)})
	if err != nil {
		t.Fatal(err)
	}
	return Check(results, list, head, up)
}

func wantProblem(t *testing.T, r Report, fragment string) {
	t.Helper()
	for _, p := range r.Problems {
		if strings.Contains(p, fragment) {
			return
		}
	}
	t.Fatalf("no problem mentions %q; problems: %q", fragment, r.Problems)
}

func TestRatchetGreenOnAMatchingList(t *testing.T) {
	r := checkWith(t, baseline(), forkSrc)
	if len(r.Problems) != 0 {
		t.Fatalf("a list matching the run reported problems: %q", r.Problems)
	}
	if r.ExperimentalSkips != 2 || r.UpstreamSkipSites != 1 || r.ForkSkipSites != 1 {
		t.Errorf("counts: %d experimental skips, %d upstream sites, %d fork sites; want 2, 1, 1", r.ExperimentalSkips, r.UpstreamSkipSites, r.ForkSkipSites)
	}
}

// Red case 1 (#2338): removing a failing test from the list fails the job.
func TestRatchetRedUnlistedFailure(t *testing.T) {
	list := baseline()
	list.Failures = nil
	wantProblem(t, checkWith(t, list, forkSrc), "unlisted failure internal/tofu.TestFails")
}

// Red case 2 (#2338): adding a passing test to the list fails the job.
func TestRatchetRedListedTestPasses(t *testing.T) {
	list := baseline()
	list.Failures = append(list.Failures, Entry{Package: tofuPkg, Test: "TestPasses", Reason: "r"})
	wantProblem(t, checkWith(t, list, forkSrc), "listed failure internal/tofu.TestPasses now passes")
}

// Red case 3 (#2338): an unlisted SkipExperimental in a fork test fails the
// job, while upstream's own call in an upstream-owned test does not.
func TestRatchetRedUnlistedForkSkip(t *testing.T) {
	list := baseline()
	list.Skips = nil
	r := checkWith(t, list, forkSrc)
	wantProblem(t, r, "fork-owned test internal/tofu.TestForkSkip calls SkipExperimental")
	for _, p := range r.Problems {
		if strings.Contains(p, "TestUpstreamSkip") {
			t.Errorf("upstream's own SkipExperimental call was flagged: %s", p)
		}
	}
}

func TestRatchetRedEntryWithoutReason(t *testing.T) {
	list := baseline()
	list.Failures[0].Reason = " "
	wantProblem(t, checkWith(t, list, forkSrc), "has no reason")
}

func TestRatchetRedListedFailureSkipped(t *testing.T) {
	list := baseline()
	list.Failures = append(list.Failures, Entry{Package: tofuPkg, Test: "TestForkSkip", Reason: "r"})
	wantProblem(t, checkWith(t, list, forkSrc), "a skip cannot stand in for a listed failure")
}

// Red case (#2256 end phase): a package that panics mid-run leaves its
// running test without a result and every later test unrun. The run is
// incomplete, and a list measured from it would be missing tests.
func TestRatchetRedUnfinishedTest(t *testing.T) {
	stream := run2json + `{"Action":"run","Package":"` + tofuPkg + `","Test":"TestPanics"}
{"Action":"output","Package":"` + tofuPkg + `","Test":"TestPanics","Output":"panic: runtime error: invalid memory address\n"}
{"Action":"output","Package":"` + tofuPkg + `","Output":"FAIL\n"}
`
	results, err := ParseResults(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	head, _ := FindSkipSites(map[string][]byte{"internal/tofu/upstream_test.go": []byte(upstreamSrc), "internal/tofu/fork_test.go": []byte(forkSrc)})
	up, _ := FindSkipSites(map[string][]byte{"internal/tofu/upstream_test.go": []byte(upstreamSrc)})
	r := Check(results, baseline(), head, up)
	wantProblem(t, r, "internal/tofu.TestPanics started and never finished")
	if r.Unfinished != 1 {
		t.Errorf("Unfinished = %d, want 1", r.Unfinished)
	}
}

// A timeout reported only at package level still refuses the run.
func TestRatchetRedPackageLevelTimeout(t *testing.T) {
	stream := run2json + `{"Action":"output","Package":"` + tofuPkg + `","Output":"panic: test timed out after 10m0s\n"}
`
	results, err := ParseResults(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	head, _ := FindSkipSites(map[string][]byte{"internal/tofu/upstream_test.go": []byte(upstreamSrc), "internal/tofu/fork_test.go": []byte(forkSrc)})
	up, _ := FindSkipSites(map[string][]byte{"internal/tofu/upstream_test.go": []byte(upstreamSrc)})
	wantProblem(t, Check(results, baseline(), head, up), "(package aborted) started and never finished")
}
