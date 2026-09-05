// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file is issue #145's remaining half.
//
// The consolidation the issue asks for has landed: live/floci-image is the
// single source, and the Makefile, live/e2e/run.sh, internal/live/flocitest
// and live/flocicap_test.go all read it rather than carrying a copy.
// make test-floci-clean uses that pin too, so it no longer cleans an image
// nobody runs.
//
// What was missing is the other half of the same request - "add whatever
// assertion makes a future drift fail rather than no-op" - and it was
// missing in a way that had already bitten. Several artifacts under live/
// record the image they were MEASURED against, and one of them,
// live/plan-budget.json, records a digest the pin has since moved past.
// Nothing failed, nothing mentioned it, and a budget measured on one
// emulator reads as a budget for the one in use.
//
// Lagging is legitimate and expected: re-measuring an artifact is a
// deliberate act, and tools/estate-gen makes the same distinction with its
// own fixture pin. So this does not demand equality. It demands that a
// lagging artifact be a recorded decision rather than an accident, which is
// the same shape as ciExcludedPackages and estate-gen's knownDrift.

// flociImageFields names each artifact under live/ that records the emulator
// image it was measured against, and the field holding it. Reads a flat
// top-level string field only - no nested-path support, so an artifact that
// carries a per-image ref (cohort-triage.json's own detail, below) still
// needs a plain top-level field this map can name.
//
// Derived from a walk rather than assumed complete: TestEveryFlociImageRef
// IsAccountedFor scans every artifact for a floci ref, so a new one is a
// failure here rather than an omission nobody sees.
//
// cohort-triage.json (issue #432) genuinely carries two refs: the stale
// pin its source artifact (cohort-acceptance.json) was measured against,
// kept under measured_at for provenance, and the current pin its own
// findings were live-verified against. Only the second is what this guard
// means by "measured against" - it is what the table's family/issue calls
// actually rest on - so that is the top-level "image" field registered
// here, not measured_at's nested pair.
var flociImageFields = map[string]string{
	"plan-budget.json":       "measured_against",
	"cohort-acceptance.json": "image",
	"gauntlet.json":          "emulator",
	"cohort-triage.json":     "image",
}

// staleFlociMeasurements are artifacts knowingly measured against an older
// image than the current pin, with the reason. An entry is a standing
// decision that says what re-measuring would cost; empty is the intended
// state.
//
// 2026-09-05 repin (issue #672, lex00/floci#190 - CreateSubnet CIDR-conflict
// check): the maintainer ruling that ordered this repin scoped the
// re-measurement to corpus-vpc-complete, the estate the emulator defect was
// found on (its greenfield stage's stock-oracle subnet count is the ratchet
// this fix targets, and it re-ran clear against the new pin - see
// live/gauntlet.json's own row). Re-measuring the three artifacts below is
// each its own multi-estate sweep, not a rerun of the one estate this repin
// was about, so each is recorded here rather than attempted in the same
// pass:
var staleFlociMeasurements = map[string]string{
	// bench-estate (`make bench-estate`, internal/live/discovery's
	// TestScaleAgainstFloci) plans a synthetic N=200 and N=1000 resource
	// estate and counts every API call; re-measuring costs that benchmark's
	// own run time (single-digit minutes per N, but it is a call-count
	// ratchet unrelated to EC2 subnet allocation - this fix touches no path
	// bench-estate's synthetic fixture exercises).
	"plan-budget.json": "measured against the pre-#672 pin; re-measuring costs a full `make bench-estate` run at N=200 and N=1000, and the fix (EC2 CreateSubnet CIDR-conflict rejection) touches no call this benchmark's synthetic fixture makes",
	// TestCohortAcceptance (internal/live/acceptance) applies, deletes the
	// state of, and replans all 31 estate-gen cohorts under
	// live/e2e/estates/ against a live floci container; re-measuring costs
	// that whole sweep, not the one estate this repin's ruling named.
	"cohort-acceptance.json": "measured against the pre-#672 pin; re-measuring costs a full `TF_FLOCI_TEST=1 TF_FLOCI_ACCEPTANCE_ARTIFACT=1 go test ./internal/live/acceptance -run TestCohortAcceptance` sweep across all 31 cohorts, out of scope for a repin ruling that named corpus-vpc-complete specifically",
	// cohort-triage.json is hand triage reconciled against
	// cohort-acceptance.json's own re-measurement (its own generated_by
	// field says so); it cannot be re-measured independently of that file.
	"cohort-triage.json": "reconciled by hand against cohort-acceptance.json (see this file's own generated_by field); re-measuring depends on that artifact's own re-measurement, which is the entry above",
}

// flociPinRef is live/floci-image's full ref.
func flociPinRef(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("floci-image")
	if err != nil {
		t.Fatalf("reading live/floci-image: %v", err)
	}
	return strings.TrimSpace(string(data))
}

// TestFlociMeasurementsMatchThePinOrSayWhyNot is the guard.
func TestFlociMeasurementsMatchThePinOrSayWhyNot(t *testing.T) {
	pin := flociPinRef(t)

	for file, field := range flociImageFields {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s is listed in flociImageFields but cannot be read: %v", file, err)
			continue
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Errorf("decoding %s: %v", file, err)
			continue
		}
		got, ok := doc[field].(string)
		if !ok {
			t.Errorf("%s has no string %q field; flociImageFields is stale", file, field)
			continue
		}

		reason, stale := staleFlociMeasurements[file]
		switch {
		case got == pin && stale:
			t.Errorf("%s now matches the pin, but is still listed as stale (%q).\n"+
				"Delete the entry: a recorded exception that no longer applies reads as a live one.", file, reason)
		case got != pin && !stale:
			t.Errorf("%s was measured against %s, but live/floci-image pins %s.\n"+
				"An artifact measured on one emulator reads as a measurement of the one in use. Either "+
				"re-measure it, or add it to staleFlociMeasurements with what re-measuring would cost.",
				file, got, pin)
		}
	}
}

// flociRef is what "records a floci image ref" means to the scan below, and
// it is a regexp rather than a substring test so the same pattern can pull
// the distinct digests out of an exempted artifact.
//
// It is deliberately the emulator repository's own name and not a general
// digest pattern: a `sha256:` in an artifact is usually a provider or module
// checksum and has nothing to do with the pin.
var flociRef = regexp.MustCompile(`floci@sha256:[0-9a-f]{8,}`)

// multiRefArtifacts are the live/ artifacts the scan below skips because
// carrying several different refs is their shape rather than drift, with why.
//
// This was two hardcoded `if e.Name() == ...` arms. Named here instead so
// TestEveryFlociRefExemptionIsStillEarned can ask whether each one is still
// true, which is the reverse direction staleFlociMeasurements already gets
// ("a recorded exception that no longer applies reads as a live one") and
// these two did not.
//
// An entry that is also in flociImageFields needs no separate check: it would
// be skipped here, so the floor at the end of the scan would not see it
// matched, and that fires.
var multiRefArtifacts = map[string]string{
	"floci-capabilities.json": "a per-image capability manifest keyed BY digest; one ref per entry is what it is",
	"corpus-crossing-manifest.json": "per-estate historical narrative rather than a `measured against` declaration: " +
		"each estate's notes field is orchestrator-written prose accumulated over many crossings, and " +
		"legitimately quotes whatever digest was pinned the day that note was written",
}

// TestEveryFlociImageRefIsAccountedFor keeps the list above from being
// narrower than the tree it describes.
//
// A hand list of files is the thing this repository keeps finding at the
// bottom of its silent failures: the check exists, and does not reach the
// thing it was meant to protect. So the files are found by scanning, and
// every ref must be either the pin, a listed measurement field, or one of
// the multiRefArtifacts above.
//
// The scan's own reach is asserted at the end. Every file in flociImageFields
// is known to carry a ref - that is what putting it there means - so every
// one of them has to come back out of the scan, and a scan that finds none of
// them has stopped recognising refs rather than found a clean tree. That is
// not hypothetical: the pattern is the emulator's repository path, so moving
// the image, or pinning it by tag instead of digest, makes every artifact in
// live/ fall through the `continue` and the test pass having read nothing.
// The sibling above does not cover it, because it compares each registered
// artifact to the pin and a pin that moved with them still matches.
func TestEveryFlociImageRefIsAccountedFor(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading live/: %v", err)
	}

	matched := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if _, exempt := multiRefArtifacts[e.Name()]; exempt {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("reading %s: %v", e.Name(), err)
			continue
		}
		if !flociRef.MatchString(string(raw)) {
			continue
		}
		matched[e.Name()] = true
		if _, listed := flociImageFields[e.Name()]; !listed {
			t.Errorf("%s records a floci image ref but is not in flociImageFields, so nothing checks it "+
				"against live/floci-image.\nAdd it with the field name holding the ref.",
				filepath.Base(e.Name()))
		}
	}

	for file := range flociImageFields {
		if matched[file] {
			continue
		}
		t.Errorf("%s is registered in flociImageFields but the scan did not find %s in it\n"+
			"The scan is what keeps that list from going narrower than live/, and it just failed to "+
			"recognise a ref in a file registered for carrying one. Either the artifact stopped "+
			"recording the emulator it was measured against - in which case drop it from "+
			"flociImageFields - or the ref is written some way flociRef no longer matches, in which "+
			"case every other artifact under live/ is now falling through this scan unread.",
			file, flociRef)
	}
}

// TestEveryFlociRefExemptionIsStillEarned is the reverse direction on
// multiRefArtifacts.
//
// An exemption is a claim about a file: that it carries several floci refs on
// purpose. Nothing rechecked that claim, so the two entries could outlive it
// - the artifact renamed, emptied, or settled down to a single ref that ought
// to be registered in flociImageFields and compared against the pin like
// every other. A skipped file is a file nothing checks, and an exemption that
// no longer applies reads as a live one.
func TestEveryFlociRefExemptionIsStillEarned(t *testing.T) {
	for file, why := range multiRefArtifacts {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s is exempted from the floci ref scan (%s) but cannot be read: %v\n"+
				"Delete the entry if the artifact is gone: it is currently excusing a file that is not "+
				"there, and would silently excuse a new one that took its name.", file, why, err)
			continue
		}
		refs := map[string]bool{}
		for _, m := range flociRef.FindAllString(string(raw), -1) {
			refs[m] = true
		}
		if len(refs) < 2 {
			t.Errorf("%s is exempted from the floci ref scan for carrying several refs on purpose (%s), "+
				"but it carries %d distinct ref(s)\n"+
				"The exemption has outlived its reason. Drop it from multiRefArtifacts, and if the file "+
				"still records one ref, register it in flociImageFields so it is compared against "+
				"live/floci-image like every other artifact.", file, why, len(refs))
		}
	}
}
