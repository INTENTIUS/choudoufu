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

	var corpus struct {
		Schemas struct {
			Version string `json:"version"`
		} `json:"schemas"`
	}
	decodeInto(t, "corpus-refusals.json", &corpus)
	if corpus.Schemas.Version != pins.AWSProviderVersion {
		t.Errorf("live/corpus-refusals.json records provider %q; pins.AWSProviderVersion is %q - rerun `just corpus`",
			corpus.Schemas.Version, pins.AWSProviderVersion)
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
