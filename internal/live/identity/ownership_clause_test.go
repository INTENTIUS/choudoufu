// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestForEachKeyRefusalCarriesOwnershipClause is issue #1242's guard for
// this package's half: both branches of [resolver.checkedForEachKeys] say
// what an address is FOR through [markers.OwnershipClause], the one sentence
// pair shared with internal/live/lint, and neither of them says it in its own
// words.
//
// # Why this shape and not a phrase sweep
//
// #1242 asked whether "the marker is the only record of ownership a
// live-markers run has" - what these two branches said before - could be
// added to live/no_state_absence_claims_test.go's repo-wide list. It cannot:
// of a resource that carries tags the sentence is TRUE (the state cache is
// never consulted for ownership, and the record store answers only for types
// with nowhere to hang a tag), so a sweep for it would eventually report a
// correct sentence and the guard would be teaching authors to route around
// itself.
//
// This asserts the positive instead, which has no such failure mode: the
// rendered diagnostic a user actually sees contains the shared clause. The
// clause existing in one place is what stops the drift; this is what proves
// each site still reaches it. Both were proved red before green - dropping
// the %s argument from either branch fails here, and so does reverting the
// branch to the old sentence.
func TestForEachKeyRefusalCarriesOwnershipClause(t *testing.T) {
	// The sentence itself must stay scoped. If the clause is ever rewritten
	// into an unconditional claim, every assertion below would keep passing
	// while shipping the defect #1242 exists to remove, so the clause is
	// checked here rather than trusted.
	if !strings.Contains(markers.OwnershipClause, "For a resource that carries tags") {
		t.Errorf("markers.OwnershipClause no longer scopes the marker claim to a taggable resource:\n%s", markers.OwnershipClause)
	}
	if strings.Contains(strings.ToLower(markers.OwnershipClause), "only record of ownership") {
		t.Errorf("markers.OwnershipClause has regained the \"only record of ownership\" claim #1242 removed:\n%s", markers.OwnershipClause)
	}

	t.Run("ordinary", func(t *testing.T) {
		// testdata/foreach-bad-key's "50%full": "%" is outside
		// markerkey.Valid, so the unnarrowed branch reports it.
		cfg := loadConfig(t, filepath.Join("testdata", "foreach-bad-key"), nil)
		_, diags := Resolve(context.Background(), cfg)
		assertOwnershipClause(t, renderDiags(diags))
	})

	t.Run("narrowed", func(t *testing.T) {
		// The strict branch, reached only when stampCanReadStatically says
		// no: a data-rooted for_each whose key is markerkey.Valid but needs
		// markerkey.Encode's help. Same fixture and value
		// TestForEachKeyOverDataRefusesEncodeNeedingKey uses.
		cfg := loadConfig(t, filepath.Join("testdata", "data-read-foreach"), nil)
		results := map[string]cty.Value{
			"data.aws_availability_zones.here": cty.ObjectVal(map[string]cty.Value{
				"names": cty.ListVal([]cty.Value{cty.StringVal("reports(draft).json")}),
			}),
		}
		_, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
		rendered := renderDiags(diags)
		if !strings.Contains(rendered, "cannot be re-derived from configuration alone at stamp time") {
			t.Fatalf("this fixture no longer reaches the narrowed branch, so the assertion below would measure the other one:\n%s", rendered)
		}
		assertOwnershipClause(t, rendered)
	})
}

// assertOwnershipClause is the pair of checks every subtest above makes: the
// shared clause is present verbatim, and the claim it replaced is gone.
func assertOwnershipClause(t *testing.T, rendered string) {
	t.Helper()
	if rendered == "" {
		t.Fatal("no diagnostics rendered; there is nothing here to check")
	}
	if !strings.Contains(rendered, markers.OwnershipClause) {
		t.Errorf("the refusal does not carry markers.OwnershipClause.\nwant to contain:\n%s\ngot:\n%s", markers.OwnershipClause, rendered)
	}
	if strings.Contains(strings.ToLower(rendered), "only record of ownership") {
		t.Errorf("the refusal still claims the marker is the only record of ownership (issue #1242); it is not, for a resource with nowhere to hang a tag:\n%s", rendered)
	}
}
