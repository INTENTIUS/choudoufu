// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package pins holds the provider release the measurement instruments pin,
// as one constant instead of one per tool. GitHub issue #117: survey-gen
// pinned 6.59.0 while the corpus artifact recorded 6.58.0, so the
// instrument that ranks admission failures was measured against a
// different provider than the artifacts that define admission, and reading
// the two against each other required remembering that. Both tools now
// derive from this constant, and live/pins_drift_test.go fails when a
// committed artifact records a different version than the pin - the same
// discipline #63 applies to the user-facing path, one layer up.
package pins

// AWSProviderVersion is the hashicorp/aws release every measurement
// instrument surveys and analyzes against: tools/survey-gen (and everything
// downstream - live/survey-full.json, live/SURVEY.md, the admission
// evidence), and tools/corpus-gen's provider-schema pass
// (live/corpus-refusals.json).
//
// tools/estate-gen carries its own, deliberately separate fixture pin
// (currently 6.58.0): the cohort estates are applied artifacts whose
// verdicts live/cohort-acceptance.json records, so bumping THAT pin is a
// mass fixture regeneration plus an acceptance-tier re-run - a deliberate
// event, not something a shared constant should trigger as a side effect.
const AWSProviderVersion = "6.59.0"
