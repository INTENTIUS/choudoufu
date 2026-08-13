// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// This file (package residue, colocated with residue.go for the reason
// tagverbs.go already documents: a go:embed directive can only name files
// in its own package's directory, and live/survey.json lives here) is
// issue #63's evidence-version source.
//
// Every admitted row's import grammar, identity schema, and taggability
// judgment was verified against one provider version at the time
// tools/survey-gen last ran, recorded once in live/survey.json's header
// rather than per row. [EvidenceVersion]'s doc comment is the record of
// why a single table-wide basis, not a per-row range, is the right answer.
//
// survey.json is the artifact read here, of the three that carry a copy of
// this version (survey.json's header, import-grammar.json's header, and
// registry.json's pin), because it is the one this package already parses
// end to end for [Lookup] and the residue-cohort accessors: reading it a
// second time for one more field costs one more struct, not a second embed
// and a second JSON parser. import-grammar.json's provider_version header
// is produced by the same tools/survey-gen run and carries the identical
// value; registry.json's pin is a digest and an acceptance date, not a
// provider semver, and answers a different question ("is the CFN registry
// snapshot stale") rather than this one.
package residue

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed survey.json
var surveyJSONBytes []byte

// surveyHeader is the slice of live/survey.json's shape this file reads:
// tools/survey-gen/main.go's own header fields, re-read here rather than
// imported for the same reason mappingRow and registryType are - that
// package is main and cannot be imported.
type surveyHeader struct {
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`
}

var (
	surveyHeaderOnce sync.Once
	surveyHeaderVal  surveyHeader
)

func loadSurveyHeader() surveyHeader {
	surveyHeaderOnce.Do(func() {
		if err := json.Unmarshal(surveyJSONBytes, &surveyHeaderVal); err != nil {
			panic(fmt.Sprintf("residue: decoding embedded survey.json: %v", err))
		}
	})
	return surveyHeaderVal
}

// EvidenceProvider is the provider source live/survey.json's admission
// table was verified against - "hashicorp/aws" today, since choudoufu
// supports one provider (live/COVERAGE.md).
func EvidenceProvider() string {
	return loadSurveyHeader().Provider
}

// EvidenceVersion is the provider version every admitted type's import
// grammar, identity schema, and taggability judgment were verified
// against: live/survey.json's provider_version header.
//
// This is a single table-wide basis, not a per-row version range, by
// deliberate choice (issue #63's design question). Two things make a
// table-wide basis correct rather than merely convenient. First,
// tools/admission-pipeline re-verifies the whole table on every provider
// pin bump (see its own doc comment), so the table has never actually held
// two provider versions' evidence at once; a per-row range would record a
// distinction that has never existed in how this table is built or kept
// current. Second, a per-row range would need its own drift test to stay
// honest as rows come and go with every mapping-gen and survey-gen run,
// which is exactly the kind of second copy of a fact this codebase's own
// conventions (see residue.go's doc comment on why cohorts are computed
// from artifacts rather than hand-copied) argue against introducing. A
// single version is also the only basis simple enough for a warning read
// once, at plan time, without a table lookup of its own.
func EvidenceVersion() string {
	return loadSurveyHeader().ProviderVersion
}
