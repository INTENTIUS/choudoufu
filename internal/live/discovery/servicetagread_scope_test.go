// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/servicetags"
)

// TestCloudControlTagUnreadableTypesAreReadOrIndexed is GitHub issue
// #1131's scope, enforced rather than stated.
//
// The issue's population is every type the Cloud Control leg can enumerate
// and can never tag-read: the provider gives it a tags argument (so
// internal/live/stamp writes a marker onto it), its mapped CFN type has an
// input-free list handler, and the registry records that CFN schema as
// carrying no Tags property, so neither ListResources nor GetResource ever
// returns the marker. For such an object [scanTypeCloudControl] has exactly
// two places left to find the marker: the estate's tag index
// ([markerIndex.join], #266), and the per-service tag read
// ([serviceTagRead], #1131 itself). With neither, a marked object whose
// block was deleted is a [SweepGapMarkerUnreadable] refusal instead of the
// destroy stock proposes - #881.
//
// So every member of that population must have at least one of the two:
//
//   - a route in the service tag reader (today, [servicetags.IAMRoutes]), or
//   - a tag index that holds its resources in every region, which is what
//     [TaggingAPIIndexRegions] answering (nil, true) means - no coverage row
//     restricts it.
//
// A type with a region-restricted or "indexed nowhere" coverage row and no
// reader route is #881 again, one type over, and this fails naming it. The
// usual way to get there is a new measurement landing in
// taggingAPIServiceCoverage or taggingAPITypeCoverage for a non-IAM
// service, which is correct on its own and silently strands that service's
// Cloud-Control-only types; the fix is a reader route for them, not
// removing the measurement.
//
// It reads the three committed artifacts (live/mapping.json,
// live/registry.json, live/survey-full.json), not this package's own
// predicates, for the population; the coverage and route answers are the
// ones the run itself consults.
//
// Bound, stated so it is not read as more: an object of an indexed type that
// the index has not caught up on (#1046's lag) still has no reader route and
// still files the refusal. That is loud and safe, and it is the residue the
// per-object gate (#1162) leaves by design for every unrouted type.
//
// Not run when written (maintainer ruling for this effort: write tests, do
// not execute them).
func TestCloudControlTagUnreadableTypesAreReadOrIndexed(t *testing.T) {
	root := flocitest.RepoRoot(t)
	roster, err := registry.Load(
		filepath.Join(root, "live", "mapping.json"),
		filepath.Join(root, "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}

	var survey struct {
		Types []struct {
			Type    string `json:"type"`
			Signals struct {
				Taggable bool `json:"taggable"`
			} `json:"signals"`
		} `json:"types"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "live", "survey-full.json"))
	if err != nil {
		t.Fatalf("reading live/survey-full.json: %v", err)
	}
	if err := json.Unmarshal(raw, &survey); err != nil {
		t.Fatalf("parsing live/survey-full.json: %v", err)
	}
	if len(survey.Types) == 0 {
		t.Fatal("live/survey-full.json parsed to zero types, so this test would pass by seeing nothing")
	}

	reader := servicetags.NewIAM(nil)
	var population, routed, indexed, stranded []string
	for _, e := range survey.Types {
		if !e.Signals.Taggable {
			continue
		}
		cfnType, listable := roster.EnumerationSource(e.Type)
		if !listable {
			continue
		}
		if taggable, known := roster.TaggableKnown(cfnType); !known || taggable {
			continue
		}
		population = append(population, e.Type)

		if reader.Route(e.Type) {
			routed = append(routed, e.Type)
			continue
		}
		if regions, ok := TaggingAPIIndexRegions(e.Type); ok && len(regions) == 0 {
			indexed = append(indexed, e.Type)
			continue
		}
		regions, ok := TaggingAPIIndexRegions(e.Type)
		coverage := "indexed nowhere"
		if ok {
			coverage = "indexed only in " + strings.Join(regions, ", ")
		}
		stranded = append(stranded, e.Type+" ("+coverage+")")
	}
	sort.Strings(stranded)

	// Each half must be non-empty, or the test is not measuring what it
	// claims: the population is the issue's, the routed half is #1161's
	// product, and the indexed half is the remainder the issue named
	// beyond IAM.
	if len(population) == 0 {
		t.Fatal("no type is Cloud-Control-listable and CFN-untaggable with a taggable provider schema; check the three artifacts before trusting this test")
	}
	if len(routed) == 0 {
		t.Fatal("no member of the population has a service tag reader route, so #1131's leg reaches nothing this test can see; check servicetags.IAMRoutes")
	}
	if len(indexed) == 0 {
		t.Fatal("no member of the population is covered by the tag index alone, so the second arm of this test is unmeasured")
	}

	if len(stranded) > 0 {
		t.Fatalf("%d type(s) Cloud Control lists, cannot tag-read, the tag index does not hold everywhere, and no service tag reader route covers:\n  %s\nA marked object of one of these whose block is deleted files SweepGapMarkerUnreadable instead of a destroy (#881). Add a route in internal/live/servicetags for its service's tag API.",
			len(stranded), strings.Join(stranded, "\n  "))
	}
	t.Logf("population %d: %d read through the service tag reader, %d through the tag index", len(population), len(routed), len(indexed))
}
