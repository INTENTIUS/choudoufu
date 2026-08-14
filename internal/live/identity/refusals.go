// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import "sort"

// This file is GitHub issue #110's first half: making this package's refusals
// enumerable.
//
// internal/live/lint has had this since it existed - a Rule constant per
// refusal, a summary and docs anchor per Rule, a fixture directory per rule,
// and four tests keeping code, fixture and documentation in lockstep. Adding
// a lint rule without a documentation entry fails a test today.
//
// This package had none of it. Its refusals are hcl.Diagnostic values built
// inline, so nothing could ask "what can this package refuse?" - which is
// exactly why live/LIMITATIONS.md documents almost none of them, and why the
// refusals most likely to hit an ordinary configuration are the ones an
// operator can find no documentation for.
//
// The Summary strings turned out to be de-facto rule identities already:
// thirty distinct ones across roughly forty sites, with the repeats being
// genuinely one rule reached several ways (five sites share "Identity not
// resolvable from configuration"). So the registry keys on the Summary rather
// than introducing a parallel constant nobody would remember to set.
//
// [TestRefusalsRegistered] enforces the other direction: every Summary
// literal in this package's non-test source must appear below, so a new
// refusal cannot be added without describing it here.

// Refusal is one thing this package can refuse, keyed by the Summary its
// diagnostic carries.
type Refusal struct {
	// Summary is the hcl.Diagnostic Summary, and this refusal's identity.
	// Several call sites may share one; that is one rule reached more than
	// one way, not two rules.
	Summary string

	// What is a one-line description of the configuration shape that
	// triggers it, in the voice live/LIMITATIONS.md's entries use.
	What string

	// DocsRef is the live/ anchor documenting it, in the same form
	// lint.Rule.DocsRef uses.
	//
	// Empty means no shipped document describes this refusal. That is a
	// gap, not a category: it is deliberately representable so the gap can
	// be counted (see [UndocumentedRefusals]) rather than discovered one
	// support question at a time. Twenty-six of the thirty are empty today.
	DocsRef string
}

// refusals is the registry. Keep it sorted by Summary; the test compares
// sets, but a sorted literal keeps its diffs readable.
var refusals = []Refusal{
	{"Circular for_each reference", "A resource's for_each depends on its own instances, directly or through another resource's for_each.", ""},
	{"Circular identity reference", "A resource's identity is composed, directly or transitively, from its own identity.", ""},
	{"Configuration loaded without a static evaluator", "The configuration was not loaded through configs.Parser.LoadConfigDir or the configload package. A caller error, not a configuration one.", ""},
	{"Expression not evaluable here", "An expression inside a keyed module resolves, several layers down, back to the module call's own each.key or each.value.", ""},
	{"Identity argument not set", "The argument carrying this type's identity has no value - most often a *_prefix argument used in place of the name itself.", ""},
	{"Identity derived from a sensitive value", "An identity argument reads a sensitive variable. Import identities are written to logs and plan output.", ""},
	{"Identity derived from an impure function", "An identity argument calls uuid(), timestamp() or bcrypt(), which return a different value on every evaluation.", ""},
	{"Identity not resolvable from configuration", "An identity argument reads something resolution cannot follow: a value through a function or operator, an indexed or two-step traversal, an ephemeral resource, or a root it does not evaluate.", ""},
	{"Invalid count", "A count expression is not a whole non-negative number.", ""},
	{"Invalid for_each set", "A for_each set's element type is not a string.", ""},
	{"Invalid for_each value", "A for_each value is neither a map nor a set of strings.", ""},
	{"No configuration to resolve", "Resolution was handed an empty configuration. A caller error, not a configuration one.", ""},
	{"No configuration to scan", "Signal collection was handed an empty configuration. A caller error, not a configuration one.", ""},
	{"Non-static count expression", "A count expression evaluates to null, or to a value not knowable from configuration alone.", ""},
	{"Non-static for_each expression", "A for_each expression cannot be resolved from configuration alone - computed from another resource's attributes, or reading a root that is not statically evaluable.", ""},
	{"Non-static identity argument", "An identity argument cannot be evaluated from configuration alone, including an impure call reached through a local or written in .tf.json.", ""},
	{"Non-static lifecycle.enabled expression", "A lifecycle.enabled expression cannot be resolved from configuration alone.", ""},
	{"Non-string identity argument", "An identity argument evaluates to a value that is not a string.", ""},
	{"Not an identity attribute", "An identity argument reads an attribute of another resource that is not part of that resource's identity.", ""},
	{"Null identity argument", "An identity argument evaluates to null.", ""},
	{"Reference to a module instance that does not exist", "A reference names a module instance the configuration does not expand to.", ""},
	{"Reference to a resource instance that does not exist", "A reference names an instance key the target resource does not expand to, or omits one it requires.", ""},
	{"Reference to undeclared resource", "A reference, or a for_each parent, names a resource the module does not declare.", ""},
	{"Resource type outside the live-markers subset", "The type is absent from the admission table, and neither the provider's identity schema nor the configuration's own arguments settle its identity.", `live/LIMITATIONS.md, "unadmitted-type"`},
	{"Sensitive for_each expression", "A for_each expression reads a sensitive value; instance keys become marker values.", ""},
	{"Two resources with the same identity", "Two resource blocks resolve to one identity, so one live object would have two owners.", `live/LIMITATIONS.md, "duplicate-identity"`},
	{"Unresolvable identity", "An identity could not be built because a reference it depends on failed; the reference's own error explains why.", ""},
	{"Unsupported each.value reference", "each.value is used as other than each.value.<attr> when for_each iterates over a resource.", ""},
	{"for_each key cannot be recorded as a marker", "A for_each key contains a character the tofu-address marker cannot carry.", `live/MARKERS.md, "Ownership semantics"`},
	{"for_each over a resource that is not keyed", "for_each iterates a resource expanded with count, which has indices rather than keys.", ""},
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

// UndocumentedRefusals returns the refusals no shipped document describes.
//
// It exists to be counted. The set is large today, and the point of naming it
// in code is that it can be watched shrinking rather than rediscovered by an
// operator who cannot find out why their configuration was refused.
func UndocumentedRefusals() []Refusal {
	var out []Refusal
	for _, r := range Refusals() {
		if r.DocsRef == "" {
			out = append(out, r)
		}
	}
	return out
}
