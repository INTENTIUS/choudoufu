// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"path/filepath"
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
// image it was measured against, and the field holding it.
//
// Derived from a walk rather than assumed complete: TestEveryFlociImageRef
// IsAccountedFor scans every artifact for a floci ref, so a new one is a
// failure here rather than an omission nobody sees.
var flociImageFields = map[string]string{
	"plan-budget.json":       "measured_against",
	"cohort-acceptance.json": "image",
}

// staleFlociMeasurements are artifacts knowingly measured against an older
// image than the current pin, with the reason. An entry is a standing
// decision that says what re-measuring would cost; empty is the intended
// state.
var staleFlociMeasurements = map[string]string{
	"plan-budget.json": "measured against sha256:4753246c, several pin moves back (sha256:488f4d6d, sha256:da6298c1, " +
		"sha256:1362e856, sha256:a1c729f4, sha256:94c5a40e, sha256:62476090); re-measuring means re-running issue " +
		"#64's estate benchmark against the current emulator, which is an acceptance-tier run rather than a " +
		"regeneration",
	"cohort-acceptance.json": "measured against sha256:1362e856, three pin moves back (sha256:a1c729f4 for the " +
		"resourcegroupstaggingapi union-index fix, sha256:94c5a40e for the security-group rule identity and Lambda " +
		"VpcConfig, sha256:62476090 for private hosted zones with a VPC and ELBv2 attribute defaults). Each was " +
		"verified live against the calls it changed. Re-measuring means re-running all 31 cohorts' apply/replan " +
		"round-trip against one shared emulator, which is an acceptance-tier run rather than a regeneration - and " +
		"the expectation is that it does not move the 4/27 split, only that it makes the 4 passes' removal leg " +
		"non-vacuous (a blind tagging index answered \"nothing extra to destroy\" for the same reason a working one " +
		"does)",
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

// TestEveryFlociImageRefIsAccountedFor keeps the list above from being
// narrower than the tree it describes.
//
// A hand list of files is the thing this repository keeps finding at the
// bottom of its silent failures: the check exists, and does not reach the
// thing it was meant to protect. So the files are found by scanning, and
// every ref must be either the pin, a listed measurement field, or part of
// live/floci-capabilities.json's per-image manifest, which is keyed BY
// digest and therefore holds several on purpose.
func TestEveryFlociImageRefIsAccountedFor(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading live/: %v", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if e.Name() == "floci-capabilities.json" {
			continue // a manifest keyed by digest; many refs is its shape
		}
		if e.Name() == "corpus-crossing-manifest.json" {
			// Per-estate historical narrative, not a "measured against"
			// declaration: each estate's notes field is orchestrator-
			// written prose accumulated over many crossings, and legitimately
			// quotes whatever digest was pinned the day that note was
			// written - several different ones across the file's history,
			// on purpose, the same shape floci-capabilities.json is
			// exempted for above.
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("reading %s: %v", e.Name(), err)
			continue
		}
		if !strings.Contains(string(raw), "floci@sha256:") {
			continue
		}
		if _, listed := flociImageFields[e.Name()]; !listed {
			t.Errorf("%s records a floci image ref but is not in flociImageFields, so nothing checks it "+
				"against live/floci-image.\nAdd it with the field name holding the ref.",
				filepath.Base(e.Name()))
		}
	}
}
