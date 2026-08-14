// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/docsref"
	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestEveryRefusalDocsRefIsResolvable is GitHub issue #110's third acceptance
// criterion, applied to all three registries at once.
//
// The criterion was written as "a test fails when a refusal exists in code
// with no documented entry". The lint half of that already existed as a
// count: identity's registry carried a ratchet on how many refusals had an
// empty DocsRef, and shrinking it was the goal. Counting empties turned out
// to be the weaker check of the two available, because it passed for a
// refusal citing a heading nobody had written - which is indistinguishable,
// from where the user stands, from citing nothing.
//
// So this resolves instead of counting. Every refusal in the catalog names a
// document under live/ that exists and a heading inside it that exists.
//
// What it does NOT do is worth stating, because an audit read more into it
// than it says. For the refusals whose entry tools/limits-gen writes, the
// reference and the heading are derived from the same Summary, so this
// cannot fail once the generator has run - and TestSpansAreCurrent forces
// that. Its teeth are on the hand-written references: lint's rule table and
// the three identity overrides, where a human chose the target and can
// choose a wrong one. The check that a generated entry says anything is
// tools/limits-gen's TestEveryGeneratedEntryHasContent, and the check that a
// refusal exists at all is each package's own refusalscan test.
//
// The chain is what makes the criterion hold, not this test alone.
func TestEveryRefusalDocsRefIsResolvable(t *testing.T) {
	root := flocitest.RepoRoot(t)

	// A Summary registered in two packages would give one heading two
	// entries, and every check downstream would pass on the duplicate.
	seen := map[string]string{}
	for _, refusal := range AllRefusals() {
		key := string(refusal.Layer) + "/" + refusal.ID
		if where, dup := seen[key]; dup {
			t.Errorf("%s is catalogued twice, from %s and %s. One heading would be written for both, and the reader would not learn there were two.", key, where, refusal.RaisedBy)
		}
		seen[key] = refusal.RaisedBy
	}

	// AllRefusals, not Catalog: the criterion is every hard refusal in the
	// live path, and stamping and discovery are the two passes this
	// instrument cannot run. A refusal it cannot measure is not a refusal a
	// user cannot hit.
	for _, refusal := range AllRefusals() {
		if refusal.DocsRef == "" {
			t.Errorf("%s/%s has no DocsRef at all", refusal.Layer, refusal.ID)
			continue
		}
		ref, err := docsref.Parse(refusal.DocsRef)
		if err != nil {
			t.Errorf("%s/%s: %s", refusal.Layer, refusal.ID, err)
			continue
		}
		if err := ref.Resolve(root); err != nil {
			t.Errorf("%s/%s cites %s: %s.\nIf the registry changed, run `just limits` to regenerate live/LIMITATIONS.md.",
				refusal.Layer, refusal.ID, refusal.DocsRef, err)
		}
	}
}

// TestEveryLayerHasARegistry is what would have caught projection.
//
// #110's first criterion is every hard refusal in the live path. The work
// added registries to lint, identity, stamp and discovery, described those
// last two in a commit message as "the other two", and stopped - while
// [UncheckedLayers] had always returned three. Projection's twenty-six
// diagnostics stayed in no table, [AllRefusals] was smaller than its own doc
// comment claimed, and every test passed. An audit found it by reading.
//
// So the layer list checks itself against the registry list. There is no
// clever mechanism here; the point is that the omission becomes a failure
// instead of a sentence somebody has to notice is wrong.
func TestEveryLayerHasARegistry(t *testing.T) {
	withRegistry := map[Layer]bool{}
	for _, layer := range LayersWithRegistries() {
		withRegistry[layer] = true
	}

	for _, layer := range append(CheckedLayers(), UncheckedLayers()...) {
		if !withRegistry[layer] {
			t.Errorf("the %s layer has no registry in AllRefusals, so its refusals are in no table and in no document", layer)
		}
	}

	// And the other direction: a registry for a layer nothing lists.
	classified := map[Layer]bool{}
	for _, layer := range append(CheckedLayers(), UncheckedLayers()...) {
		classified[layer] = true
	}
	for _, layer := range LayersWithRegistries() {
		if !classified[layer] {
			t.Errorf("AllRefusals draws from the %s layer, which is neither checked nor unchecked", layer)
		}
	}

	// Every layer with a registry actually contributes. A registry wired in
	// but returning nothing would satisfy the lists above and document
	// nothing.
	byLayer := map[Layer]int{}
	for _, refusal := range AllRefusals() {
		byLayer[refusal.Layer]++
	}
	for _, layer := range LayersWithRegistries() {
		if byLayer[layer] == 0 {
			t.Errorf("the %s layer has a registry that contributes no refusals to AllRefusals", layer)
		}
	}
}

// TestCorpusArtifactHasNoUnregisteredRefusals holds the committed #102
// artifact to zero unregistered refusals.
//
// This is the completeness argument for the half of internal/live/passthrough
// that no source scan can reach: HCL's own expression-evaluation diagnostics
// and internal/addrs' reference parser are third-party and upstream surfaces,
// so the evidence that the registry covers them is that a run over the corpus
// finds nothing it cannot name.
//
// It was three when #110 opened, and one of those three was the top row of
// the ranking: the largest single blocker this repository has measured, in
// no table and in no document. A test rather than a note because the artifact is
// regenerated whenever the corpus or the registries change, and a new
// unregistered refusal appearing there is exactly the event that should stop
// someone.
func TestCorpusArtifactHasNoUnregisteredRefusals(t *testing.T) {
	path := filepath.Join(flocitest.RepoRoot(t), "live", "corpus-refusals.json")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the corpus artifact: %s", err)
	}

	var artifact struct {
		Refusals []struct {
			Layer      string `json:"layer"`
			ID         string `json:"id"`
			Configs    int    `json:"configs"`
			Registered bool   `json:"registered"`
		} `json:"refusals"`
		Totals struct {
			Unregistered int `json:"refusals_unregistered"`
		} `json:"totals"`
	}
	if err := json.Unmarshal(src, &artifact); err != nil {
		t.Fatalf("parsing the corpus artifact: %s", err)
	}
	if len(artifact.Refusals) == 0 {
		t.Fatal("the corpus artifact lists no refusals; it is empty or its shape changed")
	}

	for _, r := range artifact.Refusals {
		if r.Configs > 0 && !r.Registered {
			t.Errorf("%s/%s fired in %d configurations and is in none of the three registries.\n"+
				"A user hit it and has nowhere to look it up. Add it to internal/live/passthrough (or to the live package that raises it) and run `just limits`.",
				r.Layer, r.ID, r.Configs)
		}
	}
	if artifact.Totals.Unregistered != 0 {
		t.Errorf("the artifact's totals.refusals_unregistered is %d, want 0", artifact.Totals.Unregistered)
	}
}

// TestPopulationsClaimNoRate is issue #118's line: every population in the
// committed corpus artifact reads as a ranking, because none of them is
// estate-shaped. The day one is, add its origin to rateCapableOrigins with
// its provenance in the manifest, and this test starts allowing exactly
// that row to say "rate".
func TestPopulationsClaimNoRate(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "live", "corpus-refusals.json"))
	if err != nil {
		t.Fatalf("reading the corpus artifact: %s", err)
	}
	var artifact struct {
		Populations []PopulationTotals `json:"populations"`
		Totals      map[string]any     `json:"totals"`
	}
	if err := json.Unmarshal(src, &artifact); err != nil {
		t.Fatalf("parsing the corpus artifact: %s", err)
	}
	if len(artifact.Populations) == 0 {
		t.Fatal("the committed corpus artifact carries no populations; regenerate it with `just corpus`")
	}
	for _, pop := range artifact.Populations {
		if pop.ReadsAs != ReadsAsRanking && !rateCapableOrigins[pop.Origin] {
			t.Errorf("population %q claims reads_as=%q without being in rateCapableOrigins - no current population supports a compatibility rate (#118)", pop.Origin, pop.ReadsAs)
		}
	}
	if _, ok := artifact.Totals["blocked"]; ok {
		t.Error("totals carries a corpus-wide blocked count again; that figure reads as a compatibility rate and was removed under #118")
	}
}

