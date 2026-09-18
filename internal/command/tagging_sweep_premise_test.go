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
// alwaysNativeSweepTypes below is one of the two other, narrower ways a row
// here can be something other than "implemented" without owing
// statelessDiscoverOne a gate (taggingSweepEmulatorDefects is the third, for
// a gap that is the emulator's own defect and expires with it): a type internal/live/discovery's own per-type routing
// (typeNeedsResourceObjectToRecompose, issue #394) sends through the
// native per-type sweep unconditionally, never through sweepViaTagging, no
// matter what Request.TaggingSweep says. For those, an unimplemented
// tagging-sweep row costs the production path nothing to leave ungated,
// because the path never reads it.
//
// An entry here is not free. It says the emulator tier cannot exercise
// internal/live/discovery's sweepViaTagging leg for that type, which is the
// production candidate path, so whatever replaces it in a gate has to be
// spelled out here alongside the cost of getting the evidence back.
var taggingSweepEmulatorExceptions = map[string]string{}

// alwaysNativeSweepTypes names the types this package can independently
// verify internal/live/discovery routes through the native per-type sweep
// unconditionally (issue #394's typeNeedsResourceObjectToRecompose is
// unexported, so this list is hand-kept rather than imported - see each
// entry's own citation into that package for what to re-check if
// discovery.go's routing ever changes). Unlike taggingSweepEmulatorExceptions,
// listing a type here is not a standing decision about the emulator: it is
// a fact about where statelessDiscoverOne's candidates for the type come
// from, true or false regardless of any floci pin, and it does not toggle
// case 5's unconditional/exception coupling below - Request.TaggingSweep
// stays correctly unconditional either way.
//
// issue #1045 (lex00/floci PR #202) is what first made this matter: floci
// stopped serving IAM through GetResources, so aws_iam_role's tagging-sweep
// row went from implemented to unimplemented, and this premise test would
// otherwise demand either a re-pin (impossible - the new pin is the correct
// one; the old pin's "implemented" was the divergence from real AWS, per
// issue #692) or a TaggingSweep gate that would regress the sweep for every
// other type to buy nothing, since aws_iam_role never took that leg anyway.
var alwaysNativeSweepTypes = map[string]string{
	"aws_iam_role": "internal/live/discovery.typeNeedsResourceObjectToRecompose returns true for aws_iam_role unconditionally (its aws_iam_service_linked_role sibling pair, issue #302/#394), so partitionSweepTypes always sends it through the native per-type leg (scanTypeReporting) and sweepViaTagging never sees it - see discovery.go's own doc comment on partitionSweepTypes and typeNeedsResourceObjectToRecompose",
}

// trackedEmulatorGap explains a tagging-sweep row that is unimplemented
// because the EMULATOR is wrong, not because AWS is.
//
// Why is what makes the row harmless to the production path today, and it
// has to be a routing fact this package can cite, the same standing
// alwaysNativeSweepTypes entries carry. Tracker is the issue that fixing
// the emulator closes, and it is the half alwaysNativeSweepTypes has no
// room for.
type trackedEmulatorGap struct {
	Tracker string
	Why     string
}

// taggingSweepEmulatorDefects is the third way a non-"implemented" row can
// be accounted for, and the only one of the three that is temporary.
//
// The other two say something permanent. taggingSweepEmulatorExceptions
// says "the emulator cannot serve this, so statelessDiscoverOne owes the
// run a gate" - case 5 below enforces exactly that, so an entry there
// conditions TaggingSweep for every type to buy coverage for one.
// alwaysNativeSweepTypes says "discovery routes this type through the
// native leg unconditionally, true or false regardless of any floci pin",
// and it short-circuits with no reverse direction at all, which is right
// for a fact that cannot stop being true.
//
// Neither fits an emulator defect. Writing one into
// alwaysNativeSweepTypes would record a falsehood to get green: that map's
// entries are #394 routing facts, and it never fails when a row turns
// implemented, so a floci fix would leave the entry sitting there reading
// as a routing fact it never was - the "recorded exception that no longer
// applies reads as a live one" shape this file's own case 4 exists to
// catch. Writing one into taggingSweepEmulatorExceptions would demand a
// TaggingSweep gate that regresses every type's sweep for two IAM types
// that never take that leg anyway.
//
// So this map short-circuits the gate question the way alwaysNativeSweepTypes
// does - on a stated routing fact - and, unlike it, fails in the reverse
// direction when the pinned row turns implemented. That is the entry's
// expiry: the day the emulator is fixed, this test goes red and names the
// tracker, instead of the entry rotting.
//
// EMPTY since 2026-09-18, and the emptying is the mechanism working rather
// than the mechanism becoming unnecessary.
//
// Its two entries were aws_iam_instance_profile and aws_iam_policy, tracked
// as #1152 (lex00/floci#205). Both said the same thing: real AWS returns 500
// of each through GetResources in us-east-1 (#1134) and the emulator
// returned an empty ResourceTagMappingList for every IAM type, so those two
// rows were floci diverging from AWS rather than matching it. lex00/floci
// fixed that, live/floci-image moved to sha256:74ffd40e..., the capability
// manifest's rows for both turned implemented, and case 6 below went red
// naming the tracker - which is exactly what the entries were written to do
// and the reason they could not live in alwaysNativeSweepTypes, where a
// fixed emulator would have left them reading as routing facts they never
// were.
//
// aws_iam_role did NOT move and must not: its entry belongs in
// alwaysNativeSweepTypes above, GetResources returns nothing for iam:role in
// any region on real AWS (#1134), and the pinned emulator is faithful to
// that - re-probed directly on this digest, us-east-1 returns the instance
// profile and the policy and not the role.
//
// What the retirement unblocked, so the next reader does not have to
// reconstruct it: #1144 keyed the unserved set by type and region, and
// internal/live/discovery's TestPerRegionTaggingRoutingAgainstFloci now
// drives a correct narrowing and two broken ones against this emulator and
// records that they produce different results. That comparison is what
// #1152 said was impossible, and it was right until this pin.
var taggingSweepEmulatorDefects = map[string]trackedEmulatorGap{}

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
// Six ways it fails, and all six are the point:
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
//  6. A row explained by taggingSweepEmulatorDefects is now implemented -
//     the emulator was fixed and the entry outlived it. Case 4's shape for
//     the third bucket, and the reason a temporary explanation may not live
//     in alwaysNativeSweepTypes, which has no such direction.
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

	// 3 and 4: the two directions, on the same rows. alwaysNativeSweepTypes
	// is checked first and short-circuits both: a type it names never takes
	// the tagging leg regardless of this row's status, so neither direction
	// says anything about whether statelessDiscoverOne needs a gate.
	//
	// taggingSweepEmulatorDefects is checked next and short-circuits case 3
	// only. It carries the same routing fact, so an unimplemented row is
	// explained the same way - but its entries are temporary, so the
	// implemented direction is a failure naming the tracker rather than a
	// skip. See case 6 below, which is that direction.
	for _, typeName := range sortedKeys(rows) {
		row := rows[typeName]
		if _, native := alwaysNativeSweepTypes[typeName]; native {
			continue
		}
		if defect, tracked := taggingSweepEmulatorDefects[typeName]; tracked {
			// 6. The emulator was fixed and the entry outlived it.
			if row.Status == "implemented" {
				t.Errorf("%s is recorded in taggingSweepEmulatorDefects as an emulator defect tracked by %s, but the "+
					"pinned emulator %s now records its tagging sweep as implemented (%s).\n"+
					"Delete the entry - and do not stop there: %s was the thing standing between issue #881's "+
					"tagging-leg repair and an emulator that could prove it. Re-read %s before closing it.",
					typeName, defect.Tracker, digest, row.Evidence, defect.Tracker, defect.Tracker)
			}
			continue
		}
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
