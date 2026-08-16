// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/intentius/choudoufu/internal/live/pins"
)

// TestMeasurementArtifactsShareTheProviderPin is issue #117's check: the two
// committed measurement artifacts must record the release
// pins.AWSProviderVersion names, so the instrument that ranks admission
// failures (the corpus) and the artifacts that define admission (the
// survey) can never again silently describe different providers. A pin
// bump fails here until both are regenerated:
//
//	go run ./tools/survey-gen        (and its downstream, see the tool's doc)
//	just corpus                      (after just corpus-fetch)
func TestMeasurementArtifactsShareTheProviderPin(t *testing.T) {
	var survey struct {
		ProviderVersion string `json:"provider_version"`
	}
	decodeInto(t, "survey-full.json", &survey)
	if survey.ProviderVersion != pins.AWSProviderVersion {
		t.Errorf("live/survey-full.json records provider %q; pins.AWSProviderVersion is %q - regenerate the survey or fix the pin",
			survey.ProviderVersion, pins.AWSProviderVersion)
	}

	// #211: the artifact now records one row per provider any corpus entry
	// actually needed, not a single global provider - so the pin check
	// looks for the one row marked "pinned" (see
	// tools/corpus-gen's schemaAcquirer and its package doc comment for why
	// exactly one provider, matched by -provider-source/-provider-version
	// rather than by name in any control flow, still keeps an exact pin)
	// instead of reading a single top-level version string.
	var corpus struct {
		Schemas struct {
			Providers []struct {
				Provider string `json:"provider"`
				Version  string `json:"version"`
				Pinned   bool   `json:"pinned"`
			} `json:"providers"`
		} `json:"schemas"`
	}
	decodeInto(t, "corpus-refusals.json", &corpus)
	var pinnedVersion string
	var found bool
	for _, p := range corpus.Schemas.Providers {
		if p.Pinned {
			pinnedVersion = p.Version
			found = true
			break
		}
	}
	if !found {
		t.Errorf("live/corpus-refusals.json records no pinned provider - rerun `just corpus`")
	} else if pinnedVersion != pins.AWSProviderVersion {
		t.Errorf("live/corpus-refusals.json records the pinned provider at %q; pins.AWSProviderVersion is %q - rerun `just corpus`",
			pinnedVersion, pins.AWSProviderVersion)
	}
}

// providerRow mirrors tools/corpus-gen's ProviderSchemaResult, decoding
// only the fields this test needs.
type providerRow struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint"`
	Version    string `json:"version"`
	Available  bool   `json:"available"`
	Error      string `json:"error"`
	Locked     bool   `json:"locked"`
}

// providerPinRow mirrors tools/corpus-gen's providerPin.
type providerPinRow struct {
	Provider   string `json:"provider"`
	Constraint string `json:"constraint"`
	Available  bool   `json:"available"`
	Version    string `json:"version"`
	Error      string `json:"error"`
}

func pinRowKey(provider, constraint string) string {
	return provider + "@" + constraint
}

// TestEveryAcquiredProviderIsLocked is issue #222's check. #211 gave every
// corpus entry schemas for its own declared or implied providers instead of
// a single global hashicorp/aws map, which was the right fix at that layer,
// but left every provider except the one flag-pinned by
// -provider-source/-provider-version free to float to whatever "latest"
// (or a ranged constraint's matching release) resolved to on the day of the
// run: live/corpus-refusals.json could move with zero code change and zero
// test failure. TestMeasurementArtifactsShareTheProviderPin above only ever
// checked the one row marked Pinned - by construction exactly one provider.
//
// The external anchor here is live/corpus-provider-pins.json
// (tools/corpus-gen's providerPin/[loadProviderPins]): a checked-in file a
// regeneration run reads BEFORE resolving any unlocked requirement, so an
// available row's version, or an unavailable row's error text, are not
// being compared against a copy of themselves produced by the same run -
// they are compared against what a PRIOR, independently committed run
// recorded, and [schemaAcquirer.acquire] is what makes that comparison
// meaningful: it forces the exact locked version onto every already-seen
// (provider, constraint) pair, so agreement here means the lock actually
// took effect, not merely that nothing changed by chance.
//
// Every row in the artifact's schema block is checked, not only the flag
// pin: a provider absent from the pins file entirely fails loudly instead
// of being skipped (the shape of bug #222 itself - a completeness check
// that can only see what it already expects). A row with Available false
// (the corpus's 8 platform-incompatible providers) is checked too, against
// the pin's recorded error text rather than a version it never resolved,
// so "skip what has no version" cannot quietly recreate the same hole one
// field over.
func TestEveryAcquiredProviderIsLocked(t *testing.T) {
	var corpus struct {
		Schemas struct {
			Providers []providerRow `json:"providers"`
		} `json:"schemas"`
	}
	decodeInto(t, "corpus-refusals.json", &corpus)

	var pins map[string]providerPinRow
	decodeInto(t, "corpus-provider-pins.json", &pins)

	seen := make(map[string]bool, len(pins))

	for _, row := range corpus.Schemas.Providers {
		key := pinRowKey(row.Provider, row.Constraint)
		pin, ok := pins[key]
		if !ok {
			t.Errorf("corpus-refusals.json: %s (constraint %q) has no entry in live/corpus-provider-pins.json - rerun `just corpus` to lock it, then commit both files",
				row.Provider, row.Constraint)
			continue
		}
		seen[key] = true

		if pin.Available != row.Available {
			t.Errorf("%s (constraint %q): corpus-refusals.json records available=%v, corpus-provider-pins.json records available=%v - the lock did not hold; rerun `just corpus` and inspect the diff before committing",
				row.Provider, row.Constraint, row.Available, pin.Available)
			continue
		}

		if row.Available {
			if row.Version != pin.Version {
				t.Errorf("%s (constraint %q) drifted from %q (locked in corpus-provider-pins.json) to %q (corpus-refusals.json) - a provider version pin moved with no deliberate edit to the pins file",
					row.Provider, row.Constraint, pin.Version, row.Version)
			}
		} else {
			if row.Error != pin.Error {
				t.Errorf("%s (constraint %q): unavailable, but the recorded failure changed.\nlocked:\n%s\nnow:\n%s",
					row.Provider, row.Constraint, pin.Error, row.Error)
			}
		}
	}

	for key, pin := range pins {
		if !seen[key] {
			t.Errorf("corpus-provider-pins.json: %s (constraint %q) is locked but no row in corpus-refusals.json matches it - prune it, or the manifest entry that once needed it is gone",
				pin.Provider, pin.Constraint)
		}
	}
}

func decodeInto(t *testing.T, rel string, v any) {
	t.Helper()
	data, err := os.ReadFile(rel) //nolint:gosec // fixed paths inside the checkout
	if err != nil {
		t.Fatalf("reading live/%s: %v", rel, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decoding live/%s: %v", rel, err)
	}
}
