// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ── why the guard is a per-script ratchet (issue #1214) ──────────────────
//
// #1042's guard banned one literal idiom outright, which it could: one
// call site had it. #1214's class is the whole of "a reducing --query on an
// operation botocore says can page", and when it was first measured over
// this tree it found 786 call sites in 42 scripts. A guard that fails on
// all of them cannot land, and pruning the class until it fits is exactly
// the judgement call #1214's ruling removed.
//
// So the guard is the repo's own ratchet shape (TestLegacyScriptsOnlyGoDown
// in tools/gauntlet is the precedent), tightened from one global number to
// a per-script count. That distinction matters: a single total lets a new
// broken call site hide behind an unrelated fix somewhere else, which is
// how a ratchet stops being a guard. Per script:
//
//   - measured above the recorded count fails, naming the new call sites;
//   - measured below it ALSO fails, telling you to lower the number, so
//     progress is recorded rather than quietly banked as future cover;
//   - a script not in the baseline has an implicit count of zero, so a
//     newly written script cannot start out dirty;
//   - a baseline entry whose script is gone fails, so the file cannot
//     accumulate dead credit.
//
// The numbers are MEASURED, never typed: `go run ./tools/aws-paginators-gen
// -baseline` rewrites the file, and the guard's failure message says so.

// PageQueryBaselinePath is the committed baseline, relative to the
// repository root.
const PageQueryBaselinePath = "live/aws-page-query-baseline.json"

// PageQueryBaseline is the baseline file's schema.
type PageQueryBaseline struct {
	Generated string `json:"_generated"`
	Comment   string `json:"_comment"`

	// Scripts maps a script path relative to live/ ("e2e/<estate>/run.sh")
	// to how many call sites in #1214's class it carries today.
	Scripts map[string]int `json:"scripts"`
}

const pageQueryBaselineComment = "Per-script count of AWS CLI call sites carrying a reducing --query on a paginating operation (issues #1042, #1206, #1214). Measured, not typed: go run ./tools/aws-paginators-gen -baseline. A count may only go down."

// CrossingScriptSources enumerates every shell script this guard scans,
// keyed by its path relative to liveDir, with its bytes.
//
// The population is wider than the one #1139's
// TestGauntletCrossingScriptsCoverEveryCorpusCopy and #1216's
// TestGauntletCrossingScriptsCarryNoVersionLiteral share
// (gauntletCrossingScriptsThatDeclareAWS in pins_drift_test.go, which
// narrows e2e/*/run.sh to the scripts that copy a corpus module AND
// declare hashicorp/aws): the per-page --query defect does not care
// whether a script copies a corpus module or which provider it pins, only
// whether it reads the cloud through the AWS CLI. e2e/lib/*.sh is in for
// the same reason - gauntlet_tagged_count lives there, and a helper that
// reintroduced the idiom would poison every caller at once.
//
// Files are read with os.ReadFile, not grep: e2e/corpus-mastino-dns/run.sh
// carries an embedded NUL byte and grep reports nothing for it (#1219,
// which is how PR #1157 counted 23 scripts out of 24). That script carries
// call sites in this class, so the blind spot would have been load-bearing
// here.
func CrossingScriptSources(liveDir string) (map[string]string, error) {
	globs := []string{
		filepath.Join(liveDir, "e2e", "*", "run.sh"),
		filepath.Join(liveDir, "e2e", "lib", "*.sh"),
	}
	out := map[string]string{}
	for _, g := range globs {
		paths, err := filepath.Glob(g)
		if err != nil {
			return nil, err
		}
		for _, p := range paths {
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(liveDir, p)
			if err != nil {
				return nil, err
			}
			out[filepath.ToSlash(rel)] = string(data)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scripts matched %v - the guard would silently check nothing", globs)
	}
	return out, nil
}

// MeasurePageQueryBaseline counts, per script, the call sites in #1214's
// class. Scripts with no call sites are omitted, so the file lists only
// what is actually outstanding.
func MeasurePageQueryBaseline(liveDir string, snap *PaginatingOperations) (map[string]int, error) {
	sources, err := CrossingScriptSources(liveDir)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for rel, src := range sources {
		findings, unknown, _ := snap.PageQueryFindings(src)
		if len(unknown) > 0 {
			return nil, fmt.Errorf("live/%s: unresolved AWS CLI service token(s) %v - the measurement cannot classify their calls, so it refuses to record a count", rel, unknown)
		}
		if len(findings) > 0 {
			counts[rel] = len(findings)
		}
	}
	return counts, nil
}

// MarshalPageQueryBaseline renders the baseline in its one canonical form.
func MarshalPageQueryBaseline(counts map[string]int) ([]byte, error) {
	b := PageQueryBaseline{
		Generated: "go run ./tools/aws-paginators-gen -baseline  -  DO NOT EDIT  -  " + time.Now().UTC().Format("2006-01-02"),
		Comment:   pageQueryBaselineComment,
		Scripts:   counts,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(b); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadPageQueryBaseline reads the committed baseline.
func LoadPageQueryBaseline(path string) (*PageQueryBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b PageQueryBaseline
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if b.Scripts == nil {
		return nil, fmt.Errorf("%s: no scripts map", path)
	}
	return &b, nil
}

// PageQueryRatchetViolation is one way the ratchet fails.
type PageQueryRatchetViolation struct {
	Script   string
	Recorded int
	Measured int
	Message  string
}

// CheckPageQueryRatchet compares a fresh measurement against the recorded
// baseline and returns every violation, sorted by script.
func CheckPageQueryRatchet(recorded map[string]int, measured map[string]int, present map[string]bool) []PageQueryRatchetViolation {
	var out []PageQueryRatchetViolation
	seen := map[string]bool{}
	for script, n := range measured {
		seen[script] = true
		want := recorded[script]
		switch {
		case n > want:
			out = append(out, PageQueryRatchetViolation{
				Script: script, Recorded: want, Measured: n,
				Message: fmt.Sprintf("live/%s carries %d call site(s) with a reducing --query on a paginating operation; the baseline records %d. A NEW one was added (issues #1042, #1206, #1214). Route it through live/e2e/lib/gauntlet.sh's gauntlet_first_match or gauntlet_tagged_count, which merge the pages before filtering", script, n, want),
			})
		case n < want:
			out = append(out, PageQueryRatchetViolation{
				Script: script, Recorded: want, Measured: n,
				Message: fmt.Sprintf("live/%s is down to %d call site(s) from the recorded %d - record the progress: go run ./tools/aws-paginators-gen -baseline (unspent credit is cover for the next broken call site somebody adds)", script, n, want),
			})
		}
	}
	for script, n := range recorded {
		if seen[script] {
			continue
		}
		if !present[script] {
			out = append(out, PageQueryRatchetViolation{
				Script: script, Recorded: n, Measured: 0,
				Message: fmt.Sprintf("the baseline records %d call site(s) for live/%s, which no longer exists - prune it: go run ./tools/aws-paginators-gen -baseline", n, script),
			})
			continue
		}
		out = append(out, PageQueryRatchetViolation{
			Script: script, Recorded: n, Measured: 0,
			Message: fmt.Sprintf("live/%s now carries none, down from the recorded %d - record it: go run ./tools/aws-paginators-gen -baseline", script, n),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Script < out[j].Script })
	return out
}
