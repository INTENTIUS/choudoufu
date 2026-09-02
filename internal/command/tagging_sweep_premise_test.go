// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// This file is issue #255.
//
// statelessDiscoverOne used to read
//
//	req.TaggingSweep = !isEmulatorEndpoint(ep)
//
// and the comment above it stated, in the present tense, that floci's
// resourcegroupstaggingapi returns an empty list for every filter. That was
// true of the pin it was written against and stopped being true when
// lex00/floci#229 landed and live/floci-image moved to sha256:a1c729f4...,
// where all seven of the manifest's tagging-sweep recipes are implemented.
// Nothing noticed, because the only thing checking the decision was a test
// asserting the literal source line - which fails when somebody edits the
// line and at no other time, so a premise that rots on its own is exactly
// the case it cannot see. The comment also cited
// live/floci-capabilities.json by LINE RANGE, into a generated artifact;
// after a regeneration those lines held an unrelated
// aws_emr_security_configuration entry.
//
// The cost was not correctness. TaggingSweep=false still detects removals,
// through ~950 per-type List calls instead of one GetResources. The cost was
// coverage: loopback is what live/e2e/run.sh and
// internal/live/flocitest.Endpoint both use, so the gate meant the emulator
// tier - the only tier that could exercise it - never reached
// internal/live/discovery's sweepViaTagging leg at all.
//
// So the premise moved out of a comment and into this test, on the shape
// live/flociimage_test.go already uses for the same problem one level up: it
// fails in BOTH directions - unexplained staleness, and a recorded exception
// that no longer applies - and it makes every exception state what
// re-measuring would cost. Its inputs are live/floci-image and
// live/floci-capabilities.json, keyed by digest, so a pin move re-decides it
// rather than leaving a sentence behind.

// taggingSweepAssignment is the source form statelessDiscoverOne carries
// when no emulator exception is on record: the sweep on for every endpoint.
// TestCloudControlFallbackWiredIntoDiscovery pins its presence as wiring;
// TestTaggingSweepPremiseHoldsForThePinnedEmulator below decides whether it
// is the form the manifest supports.
const taggingSweepAssignment = "req.TaggingSweep = true"

// taggingSweepEmulatorExceptions records resource types whose tagging-sweep
// support the pinned emulator does NOT provide, keyed by provider-local
// type, with what re-measuring would cost. A non-empty map is a standing
// decision that statelessDiscoverOne must gate TaggingSweep again rather
// than assign it unconditionally; empty is the intended state and is what
// the current pin supports.
//
// An entry here is not free. It says the emulator tier cannot exercise
// internal/live/discovery's sweepViaTagging leg for that type, which is the
// production candidate path, so whatever replaces it in a gate has to be
// spelled out here alongside the cost of getting the evidence back.
var taggingSweepEmulatorExceptions = map[string]string{}

// liveDir is the repository's live/ directory, relative to this package.
const liveDir = "../../live"

type flociTypeRowT struct {
	Type      string `json:"type"`
	Mechanism string `json:"mechanism,omitempty"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence"`
	Source    string `json:"source"`
}

type flociImageT struct {
	Digest string          `json:"digest"`
	Ref    string          `json:"ref"`
	Types  []flociTypeRowT `json:"types"`
}

type flociCapsT struct {
	Images []flociImageT `json:"images"`
}

// pinnedFlociDigest is the bare "sha256:<hex>" live/floci-image pins. A tag
// rather than a digest is a failure: a mutable tag cannot key a finding.
func pinnedFlociDigest(t *testing.T) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(liveDir, "floci-image"))
	if err != nil {
		t.Fatalf("reading live/floci-image: %v", err)
	}
	ref := strings.TrimSpace(string(raw))
	_, digest, ok := strings.Cut(ref, "@")
	if !ok || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("live/floci-image is %q, which does not pin a content digest. "+
			"live/floci-capabilities.json is keyed by digest, so a tag-only pin means no finding can be "+
			"looked up for the image actually in use.", ref)
	}
	return digest
}

func loadFlociCaps(t *testing.T) flociCapsT {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(liveDir, "floci-capabilities.json"))
	if err != nil {
		t.Fatalf("reading live/floci-capabilities.json: %v", err)
	}
	var caps flociCapsT
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatalf("decoding live/floci-capabilities.json: %v", err)
	}
	return caps
}

// taggingSweepRows indexes one image block's tagging-sweep rows by type.
func taggingSweepRows(img flociImageT) map[string]flociTypeRowT {
	out := make(map[string]flociTypeRowT)
	for _, row := range img.Types {
		if row.Mechanism == "tagging-sweep" {
			out[row.Type] = row
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestTaggingSweepPremiseHoldsForThePinnedEmulator decides, from the
// committed capability manifest rather than from a sentence, whether
// statelessDiscoverOne is entitled to enable the estate-wide tagging sweep
// unconditionally.
//
// Five ways it fails, and all five are the point:
//
//  1. The pin moved and nobody re-probed - no image block for the pinned
//     digest, or one with no tagging-sweep rows in it. Silence is "not yet
//     investigated", never a clean bill of health (see live/flocicap.go).
//  2. The pin's probe is narrower than an earlier pin's. Whatever types were
//     probed before must be probed again, or listed as exceptions; otherwise
//     a one-recipe re-probe would read as broad evidence.
//  3. A pinned row is not "implemented" and no exception explains it - the
//     regression case, which is what re-introduces the need for a gate.
//  4. An exception is recorded for a type the pin implements - the case that
//     actually bit, a standing decision outliving its reason.
//  5. The manifest and the source disagree about whether a gate exists at
//     all, in either direction.
func TestTaggingSweepPremiseHoldsForThePinnedEmulator(t *testing.T) {
	digest := pinnedFlociDigest(t)
	caps := loadFlociCaps(t)

	var pinned *flociImageT
	priorTypes := map[string]string{} // type -> the digest that recorded it
	for i := range caps.Images {
		img := &caps.Images[i]
		if img.Digest == digest {
			pinned = img
			continue
		}
		for typeName := range taggingSweepRows(*img) {
			priorTypes[typeName] = img.Digest
		}
	}

	if pinned == nil {
		t.Fatalf("live/floci-image pins %s, and live/floci-capabilities.json has no findings for that digest at all.\n"+
			"An unprobed emulator is not a working one: internal/command/live_plan.go enables the estate-wide "+
			"tagging sweep (%s) on the strength of this manifest. Re-probe the new image with\n"+
			"    go run ./tools/floci-capability-gen -mode=tagging\n"+
			"before the pin move lands, or record the gap in taggingSweepEmulatorExceptions.",
			digest, taggingSweepAssignment)
	}

	rows := taggingSweepRows(*pinned)
	if len(rows) == 0 {
		t.Fatalf("live/floci-capabilities.json has findings for the pinned digest %s but not one tagging-sweep row.\n"+
			"Other mechanisms' rows say nothing about whether GetResources answers from a populated index - that is "+
			"the exact gap issue #229 fixed and issue #255 found still gated. Run\n"+
			"    go run ./tools/floci-capability-gen -mode=tagging",
			digest)
	}

	// 2. Breadth may not shrink silently. Derived from the artifact itself
	// rather than from a hand-written list of recipes, so retiring a recipe
	// is a decision recorded here and not an omission nobody sees.
	for _, typeName := range sortedKeys(priorTypes) {
		if _, ok := rows[typeName]; ok {
			continue
		}
		if _, excepted := taggingSweepEmulatorExceptions[typeName]; excepted {
			continue
		}
		t.Errorf("%s has a tagging-sweep finding under digest %s but none under the pinned %s.\n"+
			"The new pin's probe is narrower than an older one's, so the evidence behind %s is thinner than it "+
			"looks. Re-probe that type, or record why it was dropped in taggingSweepEmulatorExceptions.",
			typeName, priorTypes[typeName], digest, taggingSweepAssignment)
	}

	// 3 and 4: the two directions, on the same rows.
	for _, typeName := range sortedKeys(rows) {
		row := rows[typeName]
		reason, excepted := taggingSweepEmulatorExceptions[typeName]
		implemented := row.Status == "implemented"
		switch {
		case !implemented && !excepted:
			t.Errorf("the pinned emulator %s records %s's tagging sweep as %q (%s; source: %s), and nothing explains it.\n"+
				"internal/command/live_plan.go carries %s, so every emulator run gathers this type's removal "+
				"candidates from an index that does not hold it - zero candidates, no diagnostic. Either re-pin to an "+
				"image that serves it, or add %s to taggingSweepEmulatorExceptions with what re-measuring would cost "+
				"and gate TaggingSweep again.",
				digest, typeName, row.Status, row.Evidence, row.Source, taggingSweepAssignment, typeName)
		case implemented && excepted:
			t.Errorf("%s is listed in taggingSweepEmulatorExceptions (%q), but the pinned emulator %s records its "+
				"tagging sweep as implemented (%s).\n"+
				"Delete the entry: a recorded exception that no longer applies reads as a live one, which is the "+
				"exact shape issue #255 found in the gate this test replaced.",
				typeName, reason, digest, row.Evidence)
		}
	}

	// 5. The manifest and the code have to agree about whether a gate
	// exists. This is the coupling the old test lacked: it asserted the
	// gate's text with no reference to whether the gate was still needed.
	src, err := os.ReadFile("live_plan.go")
	if err != nil {
		t.Fatalf("reading live_plan.go: %v", err)
	}
	unconditional := strings.Contains(string(src), taggingSweepAssignment)
	switch {
	case len(taggingSweepEmulatorExceptions) == 0 && !unconditional:
		t.Errorf("every tagging-sweep row under the pinned %s is implemented and taggingSweepEmulatorExceptions is "+
			"empty, but live_plan.go does not contain %q.\n"+
			"A gate here is a premise about an emulator. The manifest is the evidence, and it says there is nothing "+
			"to gate: either drop the condition, or name the type it exists for in taggingSweepEmulatorExceptions.",
			digest, taggingSweepAssignment)
	case len(taggingSweepEmulatorExceptions) > 0 && unconditional:
		t.Errorf("taggingSweepEmulatorExceptions records %d emulator gap(s) (%s), but live_plan.go still contains %q.\n"+
			"An exception that changes nothing about the run is a note, not a gate. Condition TaggingSweep on the "+
			"gap, or delete the exception.",
			len(taggingSweepEmulatorExceptions), strings.Join(sortedKeys(taggingSweepEmulatorExceptions), ", "),
			taggingSweepAssignment)
	}
}

// generatedArtifactLineCite matches a citation into a generated JSON
// artifact under live/ by line number, in either the single-line or the
// range form.
var generatedArtifactLineCite = regexp.MustCompile(`live/[A-Za-z0-9_.-]+\.json:[0-9]+`)

// TestNoLineRangeCitationsIntoGeneratedArtifacts is issue #255's other half,
// and it is cheap enough to be worth having on its own.
//
// The gate's comment cited a four-line range of
// live/floci-capabilities.json as its evidence. That artifact is generated;
// the next regeneration moved the finding and left an unrelated
// aws_emr_security_configuration entry at those lines,
// so a reader following the citation was pointed at something unrelated with
// nothing to tell them so. A line number into a file whose generator decides
// the line numbers cannot survive its generator - cite the digest and the
// type, which are stable keys the artifact is indexed by.
func TestNoLineRangeCitationsIntoGeneratedArtifacts(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading internal/command/: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Errorf("reading %s: %v", e.Name(), err)
			continue
		}
		for _, hit := range generatedArtifactLineCite.FindAllString(string(raw), -1) {
			t.Errorf("%s cites %s by line number.\n"+
				"Artifacts under live/ are generated, and their line numbers belong to whichever generator ran "+
				"last; the citation this replaced pointed at an unrelated entry within one regeneration. Cite the "+
				"key the artifact is indexed by (the image digest, the resource type) instead.",
				e.Name(), hit)
		}
	}
}
