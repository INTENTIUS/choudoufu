// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import "sort"

// This file is the phase's refusal registry, in the shape every live
// package's registry uses (identity's is the model): the Summary strings
// are the rule identities, the scan test in refusals_test.go enforces both
// directions, and live/LIMITATIONS.md's enumerated section is generated
// from this table by tools/limits-gen.

// Refusal is one thing this package can refuse, keyed by the Summary its
// diagnostic carries.
type Refusal struct {
	// Summary is the hcl.Diagnostic Summary, and this refusal's identity.
	Summary string

	// What is a one-line description of the situation that triggers it.
	What string

	// Doc overrides where it is documented. Empty means the generated
	// entry under its own Summary; see identity.Refusal.Doc.
	Doc string
}

// DocsRef is where a user is sent to read about this refusal.
func (r Refusal) DocsRef() string {
	if r.Doc != "" {
		return r.Doc
	}
	return `live/LIMITATIONS.md, "` + r.Summary + `"`
}

// refusals is the registry. Keep it sorted by Summary.
var refusals = []Refusal{
	{
		Summary: SummaryCrossStackOutputsUnavailable,
		What:    "A tfe_outputs value the phase must read has no auth surface available: no token argument, no TFE_TOKEN environment variable, and no credentials entry for its host in the CLI configuration (checked offline, at read time, before any read is attempted); or the read itself failed - workspace not found, no current state version, insufficient permissions - quoted from the provider.",
	},
	{
		Summary: SummaryCrossStackStateUnavailable,
		What:    "A terraform_remote_state value the phase must read could not be read from its backend: the backend type is not one this binary links, the backend could not be configured or reached, no state exists for the named key or workspace, or the state snapshot could not be decoded (a newer format, or encryption this fork cannot open) - quoted from the backend at read time.",
	},
	{
		Summary: SummaryNotReadable,
		What:    "A data source's value is needed to resolve an identity, a count or a for_each, but the data source depends on a managed resource, names one in depends_on, or has an argument that is not statically evaluable, so it cannot be read before the plan.",
	},
	{
		Summary: SummaryProviderNotConfigurable,
		What:    "A data source the phase must read belongs to a provider configuration that cannot be built before the plan: its provider block needs full evaluation, or the provider's own configure call refused - bad or missing credentials land here, quoted.",
	},
	{
		Summary: SummaryReadFailed,
		What:    "The provider returned an error for a pre-resolution data-source read, quoted verbatim. Fatal for the run: resolution built on a missing value would plan to create things that exist.",
	},
	{
		Summary: SummaryProviderNotLive,
		What:    "A data source a root output's value reaches belongs to a provider this configuration manages no live object through, so a pre-plan read of it would not be one more read of an API the projection already reads. The read is skipped and the output shows its planned value as new; nothing else in the run is affected.",
	},
	{
		Summary: SummaryEligibleRead,
		What:    "Not a refusal: live-check's finding for a data-source reference in an identity-bearing position that a live-plan resolves by reading the data source before resolution. No edit to the configuration is needed, but the read itself has not been performed - it can still fail at plan time.",
	},
}

// StopsTheRun reports whether a refusal under this Summary can stop a run.
//
// The two demand classes this package serves have opposite contracts, and
// the registry alone cannot say which one a Summary belongs to. An identity
// demand that cannot be met refuses the run, because resolution built on a
// missing value plans to create objects that already exist. A ROOT OUTPUT
// demand that cannot be met costs exactly one output its prior value; the
// plan renders it as newly created and everything else proceeds. See
// [ReadForOutputs].
//
// It exists because tools/limits-gen labels a refusal `error` unless the
// raising layer says otherwise, and lint and discovery were the only two
// layers with anything to say. A scoped refusal rendered as a blocker in
// live/LIMITATIONS.md would be read as something that stops a run, which is
// the same mistake that once put five discovery warnings in that table as
// blockers.
func StopsTheRun(summary string) bool {
	switch summary {
	case SummaryProviderNotLive:
		// Raised only for a root-output demand, and in fact never raised as
		// a diagnostic at all: the source is skipped in silence and the
		// plan's own "+ name = ..." line is the report.
		return false
	case SummaryEligibleRead:
		// Not a refusal at all - live-check's "this resolves at plan time"
		// finding.
		return false
	}
	return true
}

// Refusals returns every refusal this package can produce, sorted by
// Summary.
func Refusals() []Refusal {
	out := make([]Refusal, len(refusals))
	copy(out, refusals)
	sort.Slice(out, func(i, j int) bool { return out[i].Summary < out[j].Summary })
	return out
}

// LookupRefusal returns the registry entry for one Summary.
func LookupRefusal(summary string) (Refusal, bool) {
	for _, r := range refusals {
		if r.Summary == summary {
			return r, true
		}
	}
	return Refusal{}, false
}
