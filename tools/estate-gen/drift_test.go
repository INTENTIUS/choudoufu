// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cohorts"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is what is left of issue #108's fourth criterion once the cohort
// trees stopped being committed (issue #699).
//
// The criterion was "the committed cohort tree must be the generator's own
// output, and where it is not, the gap is a named table entry rather than a
// silence", and it was enforced by regenerating every cohort and diffing it
// against the checkout. Two ratchet tables carried the exceptions: knownDrift
// (cohorts whose recorded command no longer reproduced the tree) and
// regenGaps (cohorts with no recorded command at all).
//
// There is no committed tree to drift from any more, so the criterion is met
// by construction rather than by a table, and both tables are gone. Their
// closing state, measured on 2026-09-06 against a full `estate-gen -all`
// render before the deletion: regenGaps was already empty; knownDrift held
// two entries, of which "ecs-eks" was stale (the #432 DynamoDB attribute
// blocks it recorded as a hand edit had since been folded into
// overrides_cohort_ecs_eks.go and regenerate byte-identical) and
// "route53-cloudfront" was real - one line, aws_route53_zone_association's
// OmitIfAbsent "vpc_region" force-filling a generic placeholder that the
// committed copy predated. That ruling moved to
// live/e2e/estates/route53-cloudfront/README.md, where the cohort's other
// hand findings live; the drift itself resolved in the generator's favour,
// since the committed copy was the stale side.
//
// What replaces the diff is TestGeneratedCohortsMatchTheRecordedRoster below:
// the generator's real output has to agree with what internal/live/cohorts
// records, in both directions, so the roster cannot quietly describe a set of
// resources the generator does not render.
//
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

	for typeName := range fixtureGaps {
		if !admitted[typeName] {
			t.Errorf("fixtureGaps lists %s, which the identity table does not admit; the entry records a gap for nothing", typeName)
		}
		// Reading the roster rather than globbing live/e2e/estates/*/*.tf:
		// the cohorts are rendered at run time now (issue #699), and the
		// roster records exactly what each render declares - its pinned
		// -types plus the supporting resources the generator adds, held to
		// the generator's real output by
		// TestGeneratedCohortsMatchTheRecordedRoster.
		if declaring := cohorts.CohortsDeclaring(typeName); len(declaring) > 0 {
			t.Errorf("fixtureGaps lists %s but the %s cohort declares it; the gap has closed - delete the entry", typeName, declaring[0])
		}
	}
}

// TestGeneratedCohortsMatchTheRecordedRoster renders every cohort and holds
// internal/live/cohorts to what came out.
//
// It is the successor to TestCommittedCohortsMatchGenerator (see this file's
// header): that test existed because a committed tree can disagree with the
// generator that wrote it, and this one exists because a committed ROSTER
// can. The roster is not decoration - the ungated union pins in
// internal/live/lint and internal/live/identity read their whole cohort
// universe out of it, and tools/estate-types answers "does any cohort declare
// this type" from it - so a Supporting list that drifts from what the
// supporting pass actually emits would silence those three quietly.
//
// Both directions, per cohort: every type the render declares must be in
// Types or Supporting, and every type in Types or Supporting must be
// declared.
//
//	TF_FLOCI_TEST=1 go test ./tools/estate-gen -run TestGeneratedCohortsMatchTheRecordedRoster -v
func TestGeneratedCohortsMatchTheRecordedRoster(t *testing.T) {
	flocitest.Gate(t, "estate-gen roster")
	flocitest.RequireBinary(t, defaultInitBin)

	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring provider schemas: %v", err)
	}

	haveFmt := false
	if _, err := exec.LookPath(defaultFmtBin); err == nil {
		haveFmt = true
	}

	for _, c := range cohorts.All() {
		t.Run(c.Name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), c.Name)
			g, err := planCohort(c.Name, schemas, c.Types)
			if err != nil {
				t.Fatalf("planCohort: %v", err)
			}
			if err := writeCohort(out, c.Name, c.Types, g, false, nil); err != nil {
				t.Fatalf("writeCohort: %v", err)
			}
			if haveFmt {
				if err := formatWithBinary(defaultFmtBin, out, runCombined); err != nil {
					t.Fatalf("formatting: %v", err)
				}
			}

			declared, err := declaredTypesInDir(out)
			if err != nil {
				t.Fatal(err)
			}
			recorded := map[string]bool{}
			for _, typ := range c.DeclaredTypes() {
				recorded[typ] = true
			}
			var unrecorded, unrendered []string
			for typ := range declared {
				if !recorded[typ] {
					unrecorded = append(unrecorded, typ)
				}
			}
			for typ := range recorded {
				if !declared[typ] {
					unrendered = append(unrendered, typ)
				}
			}
			sort.Strings(unrecorded)
			sort.Strings(unrendered)
			if len(unrecorded) > 0 {
				t.Errorf("%s renders %s, which internal/live/cohorts records in neither Types nor Supporting; add them to Supporting (or to Types if the roster grew)",
					c.Name, strings.Join(unrecorded, ", "))
			}
			if len(unrendered) > 0 {
				t.Errorf("%s: internal/live/cohorts records %s, which the render does not declare; a stale roster entry claims coverage this cohort does not have",
					c.Name, strings.Join(unrendered, ", "))
			}
		})
	}
}

// declaredTypesInDir returns every provider-local resource type declared by a
// rendered cohort, recursively and over every configuration form the loader
// accepts - the same completeness [isConfigFile] exists for, so a
// resource-declaring .tf.json or a wrapped/ subdirectory cannot pass unseen.
func declaredTypesInDir(dir string) (map[string]bool, error) {
	types := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !isConfigFile(d.Name()) {
			return err
		}
		content, readErr := os.ReadFile(path) //nolint:gosec // paths under the test's own temp dir
		if readErr != nil {
			return readErr
		}
		for _, m := range resourceDeclRe.FindAllStringSubmatch(string(content), -1) {
			types[m[1]] = true
		}
		return nil
	})
	return types, err
}

var resourceDeclRe = regexp.MustCompile(`(?m)^resource "(aws_[a-z0-9_]+)"`)
