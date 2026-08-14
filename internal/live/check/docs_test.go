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

// TestCorpusArtifactHasNoUnregisteredRefusals holds the committed #102
// artifact to zero unregistered refusals.
//
// This is the completeness argument for the half of internal/live/passthrough
// that no source scan can reach: HCL's own expression-evaluation diagnostics
// and internal/addrs' reference parser are third-party and upstream surfaces,
// so the evidence that the registry covers them is that a run over the corpus
// finds nothing it cannot name.
//
// It was three when #110 opened, and those three were the top three rows of
// the ranking - the largest blockers this repository has measured, in no
// table, in no document. A test rather than a note because the artifact is
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
