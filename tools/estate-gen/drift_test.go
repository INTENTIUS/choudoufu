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
// shows against the committed tree today, plus why. Measured 2026-08-14 by
// running this test; every reason states what the measurement showed, not
// what an issue predicted. Twelve of the thirteen differ in README.md alone
// - readmeMD's format changed after those cohorts were committed - so a
// single regeneration commit clears them.
//
// The files list is exact, not a mask: a listed cohort whose drift GROWS -
// a .tf file joining a README-only entry - fails the same as an unlisted
// cohort would. The first version keyed on the cohort name alone, and an
// audit pointed out that made every listed cohort a hole through which any
// new drift passed silently.
type driftEntry struct {
	files  []string
	reason string
}

var readmeOnlyDrift = driftEntry{
	files:  []string{"README.md: content differs"},
	reason: "readmeMD's format changed after the cohort was committed",
}

var knownDrift = map[string]driftEntry{
	// The one content drift: a type admitted since the last regen maps
	// into the cohort, so regeneration emits one more resource than the
	// tree holds (issue #108's own finding, confirmed).
	"s3": {
		files:  []string{"README.md: content differs", "s3.tf: content differs"},
		reason: "a type admitted since the last regen maps into the cohort",
	},

	"apigateway":    readmeOnlyDrift,
	"aps":           readmeOnlyDrift,
	"data-movement": readmeOnlyDrift,
	"databases":     readmeOnlyDrift,
	"devtools":      readmeOnlyDrift,
	"iot":           readmeOnlyDrift,
	"media":         readmeOnlyDrift,
	// Issue #89 recorded this cohort as "no longer regenerable" after a
	// merge lost 38 override entries. Measured: its .tf surface
	// regenerates byte-identical; only the README differs. The overrides
	// were evidently restored, and #89's claim is stale.
	"networking-advanced": readmeOnlyDrift,
	"observability":       readmeOnlyDrift,
	"sagemaker":           readmeOnlyDrift,
	"security":            readmeOnlyDrift,
	"stragglers":          readmeOnlyDrift,
}

// regenGaps: cohort -> why no working one-command regeneration exists yet.
// A cohort listed here is skipped outright: either its README records no
// command, or the recorded command is broken or would destroy hand-written
// content the generator does not know how to emit.
var regenGaps = map[string]string{
	"iam-ecr":   "fully hand-written cohort (coverage lives in iam.tf/ecr.tf); its ratification evidence is in file comments the generator does not emit",
	"identity":  "hand-written iam.tf outside the emit set; the README's recorded command would regenerate around it",
	"lambda":    "hand-written iam.tf outside the emit set (the function's execution role)",
	"messaging": "hand-written iam.tf outside the emit set (streams.metrics.cloudwatch principal, not derivable from the cohort name)",

	// The README records `-cohort data` with no -types, and
	// defaultCohortTypes cannot derive a "data" CFN service - the recorded
	// command does not run. Measured by this test's first version.
	"data": "the README's recorded regeneration command is broken: no -types, and no CFN service lowercases to \"data\"",

	"ai-location":          "README records no regeneration command",
	"compute-platforms":    "README records no regeneration command",
	"connect-euc":          "README records no regeneration command",
	"dynamodb-elasticache": "README records no regeneration command",
	"ec2-core":             "README records no regeneration command",
	"ec2-networking":       "README records no regeneration command",
	"ecs-eks":              "README records no regeneration command",
	"governance":           "README records no regeneration command",
	"rds":                  "README records no regeneration command",
	"remainder":            "README records no regeneration command",
	"route53-cloudfront":   "README records no regeneration command",
	"storage":              "README records no regeneration command",
	"streaming":            "README records no regeneration command",
}

// recordedRegenTypes reads the command out of the cohort README's
// "Regenerate with" fenced block and returns the -types roster it names
// (nil, true when the command exists but carries no -types flag - the
// defaultCohortTypes shape). Only that block counts: several READMEs
// mention estate-gen invocations in prose, and the first version of this
// parser read one of those as a regeneration command for a cohort that has
// none.
var regenCommandLine = regexp.MustCompile(`go run \./tools/estate-gen [^\n]*`)
var typesFlagArg = regexp.MustCompile(`-types[= ]([^ \n]+)`)

func recordedRegenTypes(t *testing.T, cohortDir string) ([]string, bool) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(cohortDir, "README.md")) //nolint:gosec // fixture paths
	if err != nil {
		return nil, false
	}
	text := string(data)
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
		if tfs, _ := filepath.Glob(filepath.Join(committed, "*.tf")); len(tfs) == 0 {
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

// diffDirs compares the configuration and README.md surface of two cohort
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
			if !isConfigFile(d.Name()) && d.Name() != "README.md" {
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
