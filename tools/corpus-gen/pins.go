// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// providerPin is one (provider, constraint) pair's checked-in acquisition
// outcome: what "tofu init" actually resolved that requirement to, the last
// time this program locked it. It exists to close issue #222 - before it,
// every provider except the one flag-pinned by -provider-source/
// -provider-version (default hashicorp/aws, #117) was acquired at whatever
// its requiring entry's own constraint says, which for an unconstrained or
// ranged requirement means "whatever the registry calls latest today", so
// live/corpus-refusals.json could move with zero code change and zero test
// failure whenever a provider published a release.
//
// This generalizes the SAME mechanism the AWS flag pin already uses - an
// exact version handed to [pluginschema.Request.Version] instead of letting
// [pluginschema.Request.Constraint] float - to every other provider, via a
// lookup table keyed by (provider, constraint) rather than by a name in any
// control flow. [schemaAcquirer.acquire] consults it before resolving a
// requirement; [schemaAcquirer] never overwrites an existing entry, only
// adds one the first time a (provider, constraint) pair is seen, so a
// version bump for an already-locked provider is a deliberate edit to this
// file (delete or hand-update the entry, then regenerate), the same
// deliberateness a bump to pins.AWSProviderVersion already requires.
//
// A provider whose acquisition failed (no build for this platform, an
// unpublished version) is locked too, by its error text rather than a
// version: [Acquire] does not report the version it tried and failed to
// install, and every failure actually seen in this corpus (an old,
// abandoned provider's last release predating the darwin_arm64 platform)
// resolves to the exact same "latest" release, and therefore the exact
// same error, on every run - so a changed error is real news, not noise
// to filter out.
type providerPin struct {
	// Provider and Constraint are copied verbatim from
	// [ProviderSchemaResult], not re-derived, so this file's keys are the
	// same strings a reader of the corpus artifact already sees.
	Provider   string `json:"provider"`
	Constraint string `json:"constraint,omitempty"`

	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// providerPins is the file's shape: a map keyed by [pinKey], so
// encoding/json's own map-key sort keeps the file's row order stable across
// runs with no sort.Slice call of this program's own to keep in sync with
// [pinKey]'s definition.
type providerPins map[string]providerPin

// pinKey is the lookup key both [loadProviderPins]'s caller and
// [schemaAcquirer.acquire] use, built from exactly the two fields that
// distinguish one requirement from another in [providerNeed.key] - a
// provider's display name and the constraint text the requiring
// configuration declared (empty for "no constraint").
func pinKey(provider, constraint string) string {
	return provider + "@" + constraint
}

// loadProviderPins reads path, or returns an empty table when it does not
// exist yet - the state before this program has ever locked anything, e.g.
// the first run after a fresh corpus-manifest.json entry adds a provider
// nothing has needed before.
func loadProviderPins(path string) (providerPins, error) {
	data, err := os.ReadFile(path) //nolint:gosec // fixed path under live/, passed by flag
	if errors.Is(err, os.ErrNotExist) {
		return providerPins{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var pins providerPins
	if err := json.Unmarshal(data, &pins); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	if pins == nil {
		pins = providerPins{}
	}
	return pins, nil
}

// writeProviderPins writes pins as indented, key-sorted JSON (encoding/json
// sorts string map keys), matching [writeArtifact]'s own formatting so both
// generated files read the same way in a diff.
func writeProviderPins(path string, pins providerPins) error {
	encoded, err := json.MarshalIndent(pins, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o600) //nolint:gosec // generated artifact, not a secret
}
