// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// referenceCounts pins issue #42's measured reference values for the
// bundle it names ("bundle as of 2026-08"): 1,653 schemas; 1,014 taggable;
// 1,098 list-free, 309 list-with-required-input, 246 no-list-handler (168
// of those with no handlers at all); 1,479 with read.
//
// The one figure this does not copy verbatim from the issue is the
// primaryIdentifier arity: the issue states 1,654 types with a
// primaryIdentifier (1,365 single, 232 double, rest 3+) against a total of
// 1,653 schemas, which is one more than the schema total and therefore
// cannot be a per-type count over that same 1,653 - either two measurements
// from different moments were pasted together, or a stray off-by-one crept
// into the issue text. This tool's own count is unambiguous and internally
// consistent (every one of the 1,653 pinned schemas carries a
// primaryIdentifier: 1,364 single, 232 double, 57 with three or more), and
// it is what's pinned here and in pinned_spec.go/pinned-types.json. Every
// other figure below matches the issue exactly, which is the real
// cross-check: this tool's taggable/list/read logic reproduces five
// independent published numbers precisely, and the sixth is off by exactly
// the delta a one-type difference between measurement moments would
// produce - consistent with the volatility issue #42 itself documents (AWS
// shipping several distinct digests a day).
//
// issue #66 moved this pin: the CFN registry drift admission-pipeline found
// on the provider 6.58.0 -> 6.59.0 bump added 30 types with none removed
// (tools/registry-gen/pinned_spec.go's Digest/Resources/Accepted and
// pinned-types.json moved together, in the same commit, per this file's own
// AssertPinned discipline). The values below are what live/registry.json's
// own counts were measured at against the new pinned bundle, following the
// same "moving only with an accepted pin bump" rule the test below enforces
// - not re-derived from a published issue text the way the original 1,653
// set was.
var referenceCounts = RegistryCounts{
	Types:                  1683,
	WithPrimaryIdentifier:  1683,
	PrimaryIdentifierArity: PrimaryIdentifierArity{Single: 1386, Double: 239, ThreeOrMore: 58},
	Taggable:               1035,
	ListFree:               1121,
	ListWithRequiredInput:  322,
	NoListHandler:          240,
	NoHandlersAtAll:        161,
	WithRead:               1516,

	// Issue #151 added these three. relationshipRef coverage is thin in the
	// pinned bundle (26 types, 85 annotations) and grows as AWS populates
	// it, so a pin bump is expected to move the first two. The third is the
	// one to watch: a non-zero unattributed count means the walk met a
	// nesting shape it cannot tie to a property, which is a
	// relationships.go gap rather than an upstream change.
	WithRelationships:         26,
	Relationships:             85,
	RelationshipsUnattributed: 0,
}

// realZipOrSkip returns the cached real CloudFormation Registry bundle, or
// skips the test cleanly - issue #42 requires no network access in tests,
// and CI has no reason to have this ~3MB archive cached, so a machine that
// hasn't run `go run ./tools/registry-gen -zip <path>` (or a real
// generation) sees these tests skip rather than fail.
func realZipOrSkip(t *testing.T) []byte {
	t.Helper()
	path, err := defaultZipPath()
	if err != nil {
		t.Fatalf("resolving the registry zip cache path: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // a fixed cache path this tool itself wrote
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("real CloudFormation Registry bundle not cached at %s; run `go run ./tools/registry-gen -zip <path-to-CloudformationSchema.zip>` with network access (or an existing copy) first", path)
		}
		t.Fatalf("reading the cached registry bundle at %s: %v", path, err)
	}
	return data
}

// TestRealBundleMatchesPin is the pin's own self-check: the cached real
// bundle must extract to exactly the committed PinnedSpec, with no drift at
// all. A failure here means either the local cache is stale against
// pinned_spec.go (delete it and re-seed with a fresh download) or the pin
// itself needs a reviewed bump.
func TestRealBundleMatchesPin(t *testing.T) {
	zipData := realZipOrSkip(t)

	schemas, err := extractSchemas(zipData)
	if err != nil {
		t.Fatalf("extractSchemas: %v", err)
	}
	if drift := ComputeDrift(schemas, PinnedTypeNames, PinnedSpec); drift != nil {
		t.Fatalf("the cached real bundle no longer matches the committed pin:\n%s", DriftMessage(drift, PinnedSpec, drift.ResourceSetMoved()))
	}
}

// TestRegistryJSONMatchesRealBundle regenerates live/registry.json from the
// cached real bundle and diffs it against the committed artifact, the
// registry-gen sibling of survey-gen's TestSurveyJSONMatchesProviderSchemas.
// A mismatch means the committed file is stale: rerun `go run
// ./tools/registry-gen` and review the diff.
func TestRegistryJSONMatchesRealBundle(t *testing.T) {
	zipData := realZipOrSkip(t)

	schemas, err := extractSchemas(zipData)
	if err != nil {
		t.Fatalf("extractSchemas: %v", err)
	}
	if err := AssertPinned(schemas, PinnedTypeNames, PinnedSpec, false, func(m string) { t.Log(m) }); err != nil {
		t.Fatalf("the cached real bundle drifted from the pin: %v", err)
	}

	art, err := buildRegistry(PinnedSpec, schemas)
	if err != nil {
		t.Fatalf("buildRegistry: %v", err)
	}
	got, err := art.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated registry: %v", err)
	}

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(root, registryJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", registryJSONRel, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale against the cached real bundle; rerun `go run ./tools/registry-gen` and review the diff", registryJSONRel)
	}
}

// TestRegistryCounts_MatchIssue42ReferenceValues asserts the committed
// artifact's headline counts against issue #42's measured reference values
// (referenceCounts above). This is the one test in this package that reads
// the committed live/registry.json rather than regenerating it, so it also
// runs (and is meaningful) on a machine with the real bundle cached but no
// network to re-verify a live download against - the committed artifact is
// what ships, and this is the check that its counts are the ones a human
// reviewed the pin bump against.
func TestRegistryCounts_MatchIssue42ReferenceValues(t *testing.T) {
	realZipOrSkip(t) // gate: skip cleanly where the real bundle isn't cached

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, registryJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/registry-gen`): %v", registryJSONRel, err)
	}
	var art RegistryArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", registryJSONRel, err)
	}

	if art.Counts != referenceCounts {
		t.Errorf("%s's counts are %+v, want the pinned reference values %+v (moving only with an accepted pin bump)",
			registryJSONRel, art.Counts, referenceCounts)
	}

	// The counts must also be what the rows themselves say, so the pinned
	// reference values and the artifact's own rows cannot silently drift
	// apart from each other.
	var recount RegistryCounts
	for _, e := range art.Types {
		tallyEntry(&recount, e)
	}
	if recount != art.Counts {
		t.Errorf("%s's counts %+v do not match its own rows %+v", registryJSONRel, art.Counts, recount)
	}
}
