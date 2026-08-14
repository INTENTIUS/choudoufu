// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This file is issue #108's fourth criterion made a test: the committed
// cohort tree must be the generator's own output, and where it is not, the
// gap is a named table entry rather than a silence. Two tables, both
// ratchets:
//
//   - knownDrift names the cohorts whose recorded regeneration command no
//     longer reproduces the committed tree byte-for-byte, with the reason.
//     An UNLISTED drift fails (something changed the generator or the tree
//     without regenerating); a listed cohort coming back clean fails too
//     (stale entry - delete it).
//   - regenGaps names the cohorts with no recorded regeneration command at
//     all, or whose tree holds hand-written .tf files the generator refuses
//     to regenerate around. Closing a gap means folding the content into
//     the generator and recording the command, then deleting the entry.
//
// Shrinking both tables to empty is the criterion's finish line; this test
// keeps them exact along the way.

// knownDrift: cohort -> the exact drift lines the recorded command's output
// shows against the committed tree, plus why. Measured 2026-08-14, twice:
// the first tables listed 13 drifting cohorts and 18 regeneration gaps;
// the GENERATED.md ownership split (generator owns the facts file outright,
// README.md is hand-owned and never rewritten) plus a regeneration sweep
// with rosters derived from the committed coverage files cleared all of
// them but the entries below. The sweep also surfaced ecs-eks's documented
// hand edit - a supporting aws_ecs_cluster - which is now the generator's
// own NeedsSupporting mechanism rather than a hand block regeneration kept
// reverting.
//
// The files list is exact, not a mask: a listed cohort whose drift GROWS -
// a .tf file joining a GENERATED.md-only entry - fails the same as an
// unlisted cohort would. The first version keyed on the cohort name alone,
// and an audit pointed out that made every listed cohort a hole through
// which any new drift passed silently.
type driftEntry struct {
	files  []string
	reason string
}

// Empty since 2026-08-14: the last entry was s3, whose recorded command
// (bare -cohort, following admission growth) emitted a newly-mapped type -
// aws_s3control_multi_region_access_point - that the acceptance tier then
// failed on a generator defect (the identity argument emitted for a
// Computed-only attribute; fixed in fillBlock). s3's GENERATED.md now pins
// the six-type roster, so its regeneration reproduces the tree exactly and
// pulling the new type in is a deliberate roster edit judged by a tier
// run, not a side effect of regenerating.
var knownDrift = map[string]driftEntry{}

// regenGaps: cohort -> why no working one-command regeneration exists yet.
// A cohort listed here is skipped outright. Empty since the four
// hand-written cohorts were folded on 2026-08-14: their supporting
// resources became NeedsSupporting/NeedsIAMRole overrides, their
// hand-tuned values became override Apply rules citing the hand evidence,
// and every displaced comment block was relocated verbatim into the
// cohort's hand-owned README before the regeneration - the messaging fold
// initially missed the coverage file's own evidence comments and the
// value knowledge in dashboard_body/firehose_arn/alarm_rule, which is why
// the relocation step now precedes every fold rather than only covering
// the file being deleted.
var regenGaps = map[string]string{}

// recordedRegenTypes reads the command out of the cohort's "Regenerate
// with" fenced block - GENERATED.md first, README.md as the pre-split
// fallback - and returns the -types roster it names
// (nil, true when the command exists but carries no -types flag - the
// defaultCohortTypes shape). Only that block counts: several READMEs
// mention estate-gen invocations in prose, and the first version of this
// parser read one of those as a regeneration command for a cohort that has
// none.
var regenCommandLine = regexp.MustCompile(`go run \./tools/estate-gen [^\n]*`)
var typesFlagArg = regexp.MustCompile(`-types[= ]([^ \n]+)`)

func recordedRegenTypes(t *testing.T, cohortDir string) ([]string, bool) {
	t.Helper()

	// GENERATED.md is the generator-owned home of the command; README.md is
	// the fallback for cohorts whose hand READMEs still carry one from
	// before the ownership split.
	var text string
	for _, name := range []string{"GENERATED.md", "README.md"} {
		data, err := os.ReadFile(filepath.Join(cohortDir, name)) //nolint:gosec // fixture paths
		if err == nil && strings.Contains(string(data), "Regenerate with") {
			text = string(data)
			break
		}
	}
	if text == "" {
		return nil, false
	}
	i := strings.Index(text, "Regenerate with")
	if i < 0 {
		return nil, false
	}
	rest := text[i:]
	open := strings.Index(rest, "```")
	if open < 0 {
		return nil, false
	}
	rest = rest[open+3:]
	if close := strings.Index(rest, "```"); close >= 0 {
		rest = rest[:close]
	}
	// A shell continuation would otherwise cut the command at the
	// backslash and silently substitute the default roster for a recorded
	// -types on the next line.
	rest = strings.ReplaceAll(rest, "\\\n", " ")
	cmd := regenCommandLine.FindString(rest)
	if cmd == "" || strings.Contains(cmd, "-count") {
		return nil, false
	}
	if m := typesFlagArg.FindStringSubmatch(cmd); m != nil {
		return strings.Split(m[1], ","), true
	}
	return nil, true
}

// TestCommittedCohortsMatchGenerator regenerates every cohort with a
// recorded command and diffs the result against the committed tree.
//
//	TF_FLOCI_TEST=1 go test ./tools/estate-gen -run TestCommittedCohortsMatchGenerator -v
func TestCommittedCohortsMatchGenerator(t *testing.T) {
	flocitest.Gate(t, "estate-gen drift")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}

	estates := filepath.Join(root, "live", "e2e", "estates")
	entries, err := os.ReadDir(estates)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cohort := e.Name()
		committed := filepath.Join(estates, cohort)
		if !holdsConfig(t, committed) {
			continue // live/e2e/estates/example holds only a README
		}
		seen[cohort] = true

		if reason, gap := regenGaps[cohort]; gap {
			// A gap can close two ways this loop can see: a README that
			// records no command gains one, or a hand-written tree loses
			// its foreign files. Either way the entry is stale and the
			// cohort must graduate into the regeneration diff - a gap that
			// closes silently would stay skipped forever, which an audit
			// called out of the first version's "both tables are ratchets"
			// claim.
			_, hasCommand := recordedRegenTypes(t, committed)
			if strings.Contains(reason, "README records no regeneration command") && hasCommand {
				t.Errorf("%s: regenGaps says its README records no command, but it records one now - stale entry, move the cohort into the diff", cohort)
				continue
			}
			if strings.Contains(reason, "hand-written") && hasCommand && checkForeignTF(committed, cohort) == nil {
				t.Errorf("%s: regenGaps says it carries hand-written files, but none remain and a command is recorded - stale entry", cohort)
				continue
			}
			t.Logf("%s: regeneration gap, skipped (%s)", cohort, reason)
			continue
		}
		types, hasCommand := recordedRegenTypes(t, committed)
		if !hasCommand {
			t.Errorf("%s: no recorded regeneration command and not in regenGaps - record the command in its README or name the gap", cohort)
			continue
		}

		t.Run(cohort, func(t *testing.T) {
			if types == nil {
				types, err = defaultCohortTypes(root, cohort)
				if err != nil {
					t.Fatalf("defaultCohortTypes(%s): %v", cohort, err)
				}
			}
			out := filepath.Join(t.TempDir(), cohort)
			g, err := planCohort(cohort, schemas, types)
			if err != nil {
				t.Fatalf("planCohort: %v", err)
			}
			if err := writeCohort(out, cohort, types, g, false, nil); err != nil {
				t.Fatalf("writeCohort: %v", err)
			}
			if _, err := exec.LookPath(defaultFmtBin); err == nil {
				if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
					t.Fatalf("formatting: %v", err)
				}
			}

			drift := diffDirs(t, committed, out)
			sort.Strings(drift)
			entry, listed := knownDrift[cohort]
			expected := append([]string{}, entry.files...)
			sort.Strings(expected)
			switch {
			case len(drift) > 0 && !listed:
				t.Errorf("%s drifted from its recorded regeneration and is not in knownDrift:\n  %s", cohort, strings.Join(drift, "\n  "))
			case len(drift) == 0 && listed:
				t.Errorf("%s regenerates byte-identical but is still listed in knownDrift (%q) - stale entry", cohort, entry.reason)
			case listed && !reflect.DeepEqual(drift, expected):
				t.Errorf("%s: the drift is not the drift knownDrift records (%q).\n  recorded: %s\n  measured: %s",
					cohort, entry.reason, strings.Join(expected, ", "), strings.Join(drift, ", "))
			case len(drift) > 0:
				t.Logf("%s: known drift (%s):\n  %s", cohort, entry.reason, strings.Join(drift, "\n  "))
			}
		})
	}

	for cohort := range regenGaps {
		if !seen[cohort] {
			t.Errorf("regenGaps names %s, which is not a cohort directory - stale entry", cohort)
		}
	}
	for cohort := range knownDrift {
		if !seen[cohort] {
			t.Errorf("knownDrift names %s, which is not a cohort directory - stale entry", cohort)
		}
	}
}

// diffDirs compares the configuration and GENERATED.md surface of two cohort
// trees, recursively, and returns one line per difference. Recursive and
// extension-complete on purpose: the first version read the top level's
// *.tf only, which would have hidden both a -module-wrap cohort's wrapped/
// tree and a resource-declaring iam.tf.json (audit findings, both).
func diffDirs(t *testing.T, committed, generated string) []string {
	t.Helper()

	collect := func(root string) map[string]bool {
		out := map[string]bool{}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if !isConfigFile(d.Name()) && d.Name() != "GENERATED.md" {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = true
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	files := collect(committed)
	for name := range collect(generated) {
		files[name] = true
	}

	var drift []string
	for name := range files {
		a, errA := os.ReadFile(filepath.Join(committed, filepath.FromSlash(name))) //nolint:gosec // fixture paths
		b, errB := os.ReadFile(filepath.Join(generated, filepath.FromSlash(name))) //nolint:gosec // fixture paths
		switch {
		case errA != nil:
			drift = append(drift, name+": only in the regeneration")
		case errB != nil:
			drift = append(drift, name+": only in the committed tree")
		case string(a) != string(b):
			drift = append(drift, name+": content differs")
		}
	}
	return drift
}

// holdsConfig reports whether the cohort directory contains any loadable
// configuration file, recursively. The first version globbed *.tf at the
// top level only, so a cohort declaring resources in a .tf.json file or a
// wrapped/ subdirectory was invisible to BOTH ratchet tables - the same
// filter-narrower-than-the-loader shape the wave-2 audit demonstrated by
// planting exactly those two cohorts and watching nothing fail.
func holdsConfig(t *testing.T, dir string) bool {
	t.Helper()

	found := false
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if isConfigFile(d.Name()) {
			found = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}
