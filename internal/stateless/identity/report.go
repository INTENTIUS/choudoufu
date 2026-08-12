// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/opentofu/opentofu/internal/providers"
)

// DerivabilityReport is the answer to "which rows would the identity table
// get for free", in a shape another program can read.
//
// It exists because the answer is now worth automating rather than reading
// out of a log line. Every hand-written batch of table rows (#19, #20, #21)
// was four files of editing per type, and the whole point of plumbing the
// provider's identity schemas was to stop paying that. A report naming the
// types that need no editing at all - and, for the cohort the schemas
// cannot decide alone, naming the arguments a configuration would have to
// set to decide them - is what turns "the table should shrink" into
// something a generator can act on.
//
// The JSON field names follow tools/survey-gen's, which is the program that
// consumes it: snake_case, lists sorted, no timestamps, so that
// regenerating against one provider release is byte-identical.
type DerivabilityReport struct {
	// Candidates has one row per type that some evidence admits or could
	// admit, sorted by type name. A type whose identity attributes are not
	// configuration arguments at all is not a candidate and is absent.
	Candidates []AdmissionCandidate `json:"candidates"`

	// Counts are the roster-wide totals, one per [Admission] plus the split
	// that matters to a wiring batch: how many are not in the table yet.
	Counts AdmissionCounts `json:"counts"`
}

// AdmissionCounts are the report's totals.
type AdmissionCounts struct {
	// Schema, ConfigSignal, NeedsConfigSignal and ConfigDeclined count the
	// candidates by what decided them. See [Admission].
	Schema            int `json:"schema"`
	ConfigSignal      int `json:"config_signal"`
	NeedsConfigSignal int `json:"needs_config_signal"`
	ConfigDeclined    int `json:"config_declined"`

	// NewToTable is how many of the admitted candidates - schema or
	// config-signal, not the undecided ones - [DefaultTable] does not
	// already carry. It is the size of the batch nobody has to write.
	NewToTable int `json:"new_to_table"`
}

// AdmissionCandidate is one type's row.
type AdmissionCandidate struct {
	// Type is the resource type name.
	Type string `json:"type"`

	// Admits is what decided it. See [Admission].
	Admits Admission `json:"admits"`

	// IdentityAttrs are the attributes the provider requires for import,
	// sorted. For a config-signal row they are also the arguments a
	// configuration has to set, which is the whole of what the row asks of
	// one.
	IdentityAttrs []string `json:"identity_attrs"`

	// Context are the identity attributes the provider marks optional for
	// import and fills in itself: account_id and region, in the AWS
	// provider.
	Context []string `json:"context,omitempty"`

	// InTable is true when [DefaultTable] already carries a hand-written
	// row for this type.
	InTable bool `json:"in_table"`
}

// Report builds the derivability report for one provider's schemas,
// optionally read against one configuration.
//
// With a nil signal - a survey of a provider's whole type set, with no
// configuration in the picture - the cohort the schemas cannot decide comes
// back [AdmitNeedsConfigSignal], naming the arguments that would decide it.
// With a signal, those rows become [AdmitConfigSignal] or
// [AdmitConfigDeclined] according to what the configuration says.
func Report(resourceTypes map[string]providers.Schema, signal *ConfigSignal) DerivabilityReport {
	var report DerivabilityReport

	strict := make(map[string]bool)
	for _, d := range Derivable(resourceTypes) {
		strict[d.Type] = true
		report.Candidates = append(report.Candidates, AdmissionCandidate{
			Type:          d.Type,
			Admits:        AdmitSchema,
			IdentityAttrs: d.IdentityAttrs,
			Context:       d.Context,
			InTable:       d.InTable,
		})
	}

	for typeName, schema := range resourceTypes {
		if strict[typeName] {
			continue
		}
		required, ok := cohortAttrs(schema)
		if !ok {
			continue
		}
		_, context := identityAttrs(schema.IdentitySchema)
		_, inTable := DefaultTable[typeName]

		admits := AdmitNeedsConfigSignal
		switch verdict, _ := signal.NamingOfType(typeName, required); verdict {
		case NamingClientNamed:
			admits = AdmitConfigSignal
		case NamingServerAssigned, NamingPartial:
			admits = AdmitConfigDeclined
		}

		report.Candidates = append(report.Candidates, AdmissionCandidate{
			Type:          typeName,
			Admits:        admits,
			IdentityAttrs: required,
			Context:       context,
			InTable:       inTable,
		})
	}

	sort.Slice(report.Candidates, func(i, j int) bool {
		return report.Candidates[i].Type < report.Candidates[j].Type
	})

	for _, c := range report.Candidates {
		switch c.Admits {
		case AdmitSchema:
			report.Counts.Schema++
		case AdmitConfigSignal:
			report.Counts.ConfigSignal++
		case AdmitNeedsConfigSignal:
			report.Counts.NeedsConfigSignal++
		case AdmitConfigDeclined:
			report.Counts.ConfigDeclined++
		}
		if !c.InTable && (c.Admits == AdmitSchema || c.Admits == AdmitConfigSignal) {
			report.Counts.NewToTable++
		}
	}
	return report
}

// Admits returns one type's verdict, and whether the report has a row for
// it at all. A type with no row is one no evidence here can admit: its
// identity attributes are not configuration arguments, or its identity
// schema requires nothing for import, or the provider serves no identity
// schema for it.
func (r DerivabilityReport) Admits(typeName string) (AdmissionCandidate, bool) {
	i := sort.Search(len(r.Candidates), func(i int) bool {
		return r.Candidates[i].Type >= typeName
	})
	if i < len(r.Candidates) && r.Candidates[i].Type == typeName {
		return r.Candidates[i], true
	}
	return AdmissionCandidate{}, false
}
