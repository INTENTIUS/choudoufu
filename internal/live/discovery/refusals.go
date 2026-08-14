// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import "sort"

// This file is GitHub issue #110 applied to section D of #101's inventory,
// the discovery pass. See internal/live/stamp/refusals.go for why a pass no
// corpus run can measure is the one most worth writing down.
//
// Both entries are the same rule seen twice: an address has to survive the
// round trip into a tag value and back. One fails because the address is too
// long for the tags to carry, the other because two addresses arrive at one
// tag value. Discovery refuses rather than guessing which live object a
// marker meant, and that is the whole of this pass's own refusal surface -
// everything else it reports is about what it found, not about what it will
// not do.

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
		Summary: "Address too long to carry an ownership marker",
		What:    "A resource instance's address, escaped for a tag value, is longer than the tofu-address tag and its continuations can hold, so no live object can carry it.",
		Doc:     `live/LIMITATIONS.md, "overlong-address"`,
	},
	{
		Summary: "One marker value for two declared addresses",
		What:    "Two declared instances escape to the same tofu-address value, so a marker cannot say which of them a live object belongs to. Binding either would be a guess.",
	},
}

// Refusals returns every refusal this package can produce, sorted by Summary.
func Refusals() []Refusal {
	out := make([]Refusal, len(refusals))
	copy(out, refusals)
	sort.Slice(out, func(i, j int) bool { return out[i].Summary < out[j].Summary })
	return out
}

// LookupRefusal returns the registry entry for a diagnostic Summary.
func LookupRefusal(summary string) (Refusal, bool) {
	for _, r := range refusals {
		if r.Summary == summary {
			return r, true
		}
	}
	return Refusal{}, false
}
