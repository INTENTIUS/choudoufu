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
		Summary: SummaryEligibleRead,
		What:    "Not a refusal: live-check's finding for a data-source reference in an identity-bearing position that a live-plan resolves by reading the data source before resolution. No edit to the configuration is needed, but the read itself has not been performed - it can still fail at plan time.",
	},
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
