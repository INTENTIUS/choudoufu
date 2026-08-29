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
	"github.com/intentius/choudoufu/internal/live/identity"
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

// Was empty from 2026-08-14 until issue #291's sidecar sweep regenerated
// every cohort (to add estate.chdf.hcl, see files.go's estateSidecarHCL)
// and found data.tf had drifted underneath it: the last entry was s3, whose
// recorded command (bare -cohort, following admission growth) emitted a
// newly-mapped type - aws_s3control_multi_region_access_point - that the
// acceptance tier then failed on a generator defect (the identity argument
// emitted for a Computed-only attribute; fixed in fillBlock). s3's
// GENERATED.md now pins the six-type roster, so its regeneration reproduces
// the tree exactly and pulling the new type in is a deliberate roster edit
// judged by a tier run, not a side effect of regenerating.
// Issue #292 ("estate-gen's siblingRef wires an account-scoped identity
// component (Glue catalog_id) to a sibling reference instead of a literal")
// used to carry a "data" entry here: #241 made catalog_id an identity
// component for four Glue types, and this generator wired three of them to
// aws_glue_data_catalog_encryption_settings.app's own catalog_id - a
// cross-resource reference regenerating live would have pinned - rather
// than the account-scoped literal every sibling shares. The actual bug
// turned out to live in two places, neither of them siblingRef:
// identityComponentArgs was forcing a Cloud-bearing identity component's
// Attrs into the root fill pass even though the provider documents the
// omitted case (defaulting to the run's own account/region, which
// resolve.go's cloudValueFor already reads), and identityArgName was
// reporting a Cloud-derived single-component identity
// (aws_glue_data_catalog_encryption_settings's whole identity is exactly
// this shape) as a "client-set" identity parentRef could hand out as a
// same-named parent - which is what actually produced the cross-resource
// reference, independent of siblingRef, and also independent of #241
// specifically (the same shape was live on "region" in devtools,
// ecs-eks, messaging and sagemaker as a plain generic-placeholder string,
// "region = \"placeholder\"", predating #241). Both are fixed at the rule
// level (identityComponentArgs and identityArgName both skip a component
// carrying identity.Component.Cloud), so every one of the 26 admitted
// types sharing this account/region-scoped shape regenerates correctly,
// not just the four Glue ones - see the "region"/"catalog_id"/"account_id"
// removals in devtools.tf, ecs-eks.tf, messaging.tf and sagemaker.tf
// alongside data.tf. No entry remains here for any of them.
//
// The full-corpus TF_FLOCI_TEST=1 run also surfaced "lambda" and
// "route53-cloudfront" newly drifting - each one Optional, OmitIfAbsent
// identity-component argument (aws_lambda_permission's "qualifier",
// aws_route53_zone_association's "vpc_region") gaining a generic
// "placeholder" string it did not have before. Neither carries a
// [identity.Component.Cloud] property, so #292's fix (identityComponentArgs
// and identityArgName skipping a Cloud-bearing component) does not touch
// either component, and both entries reproduce identically against the
// pre-#292 generator (confirmed by reverting tools/estate-gen/gen.go to
// its committed HEAD and regenerating both cohorts unmodified). Pre-
// existing, unrelated drift that #292's own scope does not cover -
// OmitIfAbsent's own force-fill rule is the shape to look at, its own
// issue rather than folded in here.
//
// Issue #294 ("lambdaTypes grown to 7 types, lambda.tf still covers 5")
// regenerated the lambda cohort alone to add the two missing types
// (aws_lambda_function_event_invoke_config, aws_lambda_layer_version_permission)
// and, as a side effect of that same regeneration, picked up the qualifier
// force-fill above - the committed lambda.tf now matches the generator's
// current output byte-for-byte, closing the "lambda" entry below. The
// underlying OmitIfAbsent force-fill defect is unfixed and still live in
// route53-cloudfront, unregenerated because #294 scoped to lambda only.
var knownDrift = map[string]driftEntry{
	"route53-cloudfront": {
		files:  []string{"route53-cloudfront.tf: content differs"},
		reason: "aws_route53_zone_association's OmitIfAbsent \"vpc_region\" identity component now force-fills a generic placeholder (\"vpc_region = \\\"placeholder\\\"\") that the committed tree omits; reproduces unmodified against the pre-#292 generator, unrelated to #292's Cloud-component fix - the same unfiled OmitIfAbsent force-fill defect lambda had above (#294 closed lambda's entry by regenerating that cohort; this one is still open)",
	},
	// Found triaging issue #432 (acceptance cohort "ecs-eks" fails apply
	// with "all indexes must match a defined attribute. Unmatched indexes:
	// [\"GameTitle\" \"TopScore\"]"): seedFromExample seeds aws_dynamodb_table
	// from the provider doc's example, which declares three "attribute"
	// blocks (UserId, GameTitle, TopScore), but only the first survives -
	// the doc example's GameTitle/TopScore attributes never reach the
	// rendered block even though hash_key/range_key/global_secondary_index
	// (which name them) do, leaving two GSI/range keys with no matching
	// top-level attribute, which DynamoDB's CreateTable rejects unconditionally
	// (confirmed against a live floci probe, not an emulator gap). Hand-added
	// the two missing attribute blocks directly rather than fixing
	// seedFromExample's repeated-block seeding, which is shared by every
	// cohort's regeneration and out of this unit's scope.
	"ecs-eks": {
		files:  []string{"supporting.tf: content differs"},
		reason: "seedFromExample only seeds one \"attribute\" block from aws_dynamodb_table's doc example; GameTitle (S) and TopScore (N) are hand-added so the table's global_secondary_index has attribute definitions for both of its keys, matching what DynamoDB's CreateTable requires - #432",
	},
}

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

// fixtureGaps: admitted type -> why no cohort fixture exercises it. The
// third gap table, added with the #175 ratification batch under the ruling
// that an unbuildable fixture does not exempt a type from parity: the row
// lands in the identity and admission tables, and the missing fixture is
// recorded here by name with the physical prerequisite that blocks it,
// rather than silently joining the many types no cohort happens to wire.
// TestFixtureGapsAreRealAndCurrent holds each entry to both halves: the
// type must actually be admitted, and no committed fixture may use it - an
// entry whose fixture appears is stale and must be deleted.
var fixtureGaps = map[string]string{
	"aws_s3control_bucket_policy":                                "the bucket argument must be an S3 on Outposts bucket ARN, and creating one requires a physical AWS Outpost (#175, ratified 2026-08-15)",
	"aws_ssoadmin_customer_managed_policy_attachments_exclusive": "requires an IAM Identity Center instance, which cannot be created by configuration - the instance_arn only exists after console/org-level enablement (#175, ratified 2026-08-15)",
	"aws_ssoadmin_managed_policy_attachments_exclusive":          "requires an IAM Identity Center instance, same prerequisite as the row above (#175, ratified 2026-08-15)",
	"aws_ssoadmin_permission_set_inline_policy":                  "requires an IAM Identity Center instance, same prerequisite as the row above (#175, ratified 2026-08-15)",
	"aws_ssoadmin_permissions_boundary_attachment":               "requires an IAM Identity Center instance, same prerequisite as the row above (#175, ratified 2026-08-15)",
}

// TestFixtureGapsAreRealAndCurrent keeps fixtureGaps exact in both
// directions its doc comment promises: every listed type is really
// admitted (a typo here would record a gap for nothing), and no committed
// cohort fixture declares it (a fixture landing retires the entry, and a
// stale entry fails the same way knownDrift's stale entries do).
func TestFixtureGapsAreRealAndCurrent(t *testing.T) {
	admitted := map[string]bool{}
	for _, typeName := range identity.AdmittedTypes() {
		admitted[typeName] = true
	}

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	usedBy := map[string]string{}
	estates := filepath.Join(root, "live", "e2e", "estates")
	entries, err := os.ReadDir(estates)
	if err != nil {
		t.Fatal(err)
	}
	resourceDecl := regexp.MustCompile(`(?m)^resource "(aws_[a-z0-9_]+)"`)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		tfs, err := filepath.Glob(filepath.Join(estates, e.Name(), "*.tf"))
		if err != nil {
			t.Fatal(err)
		}
		for _, tf := range tfs {
			content, err := os.ReadFile(tf) //nolint:gosec // fixture paths inside the checkout
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range resourceDecl.FindAllStringSubmatch(string(content), -1) {
				usedBy[m[1]] = e.Name()
			}
		}
	}

	for typeName := range fixtureGaps {
		if !admitted[typeName] {
			t.Errorf("fixtureGaps lists %s, which the identity table does not admit; the entry records a gap for nothing", typeName)
		}
		if cohort, ok := usedBy[typeName]; ok {
			t.Errorf("fixtureGaps lists %s but the %s cohort's fixture declares it; the gap has closed - delete the entry", typeName, cohort)
		}
	}
}

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
