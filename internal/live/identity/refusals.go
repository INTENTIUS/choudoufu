// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"
)

// This file is GitHub issue #110's first half: making this package's refusals
// enumerable.
//
// internal/live/lint has most of this - a Rule constant per refusal, a
// summary and docs anchor per Rule, and tests tying fixture directories to
// live/LIMITATIONS.md headings. It closes its own loop now, which two earlier
// versions of this comment were wrong about in opposite directions: it once
// claimed the loop was closed when nothing asserted it, and then claimed it
// was open after TestEveryRuleConstantIsRegistered and
// TestEveryRuleDocsRefIsResolvable had landed. Both now hold - every Rule
// constant has a ruleInfo entry, and every docsRef resolves to a heading in
// a shipped file under live/.
//
// Three of its nineteen rules still have no fixture directory: the
// receipt-shape rules, which are specified in live/RECEIPTS.md alongside the
// pattern they guard rather than in the limits wing.
//
// This package had none of it. Its refusals are hcl.Diagnostic values built
// inline, so nothing could ask "what can this package refuse?" - which is
// exactly why live/LIMITATIONS.md documents almost none of them, and why the
// refusals most likely to hit an ordinary configuration are the ones an
// operator can find no documentation for.
//
// The Summary strings turned out to be de-facto rule identities already:
// thirty-two distinct ones across roughly forty sites, with the repeats being
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

	// Doc overrides where this refusal is documented, in the same form
	// lint.Rule.DocsRef uses.
	//
	// Empty is the ordinary case and does NOT mean undocumented. Every
	// refusal here has an entry in live/LIMITATIONS.md's enumerated
	// section, generated from this table by tools/limits-gen under its own
	// Summary as the heading, and [Refusal.DocsRef] derives the reference
	// to it. Set this only when a fuller treatment exists somewhere else -
	// a hand-written limits entry, or a page like live/MARKERS.md that
	// specifies the rule rather than just naming it.
	//
	// It used to be the reference itself, stored per row. Thirty of the
	// thirty-four rows would now carry the same mechanically derivable
	// string, and the field's emptiness was the repository's measure of an
	// undocumented refusal - a measure that stopped meaning anything the
	// moment the document was generated from the table it measures.
	Doc string
}

// DocsRef is where a user is sent to read about this refusal, in the form
// lint.Rule.DocsRef uses.
//
// It is [Refusal.Doc] when that names a fuller treatment, and otherwise the
// generated entry in live/LIMITATIONS.md. Deriving it is what keeps a new
// refusal documented by default: adding a row to this table adds a heading
// to the document, and internal/live/check's TestEveryRefusalDocsRefIsResolvable
// fails if the generator has not been run.
func (r Refusal) DocsRef() string {
	if r.Doc != "" {
		return r.Doc
	}
	return fmt.Sprintf("live/LIMITATIONS.md, %q", r.Summary)
}

// refusals is the registry. Keep it sorted by Summary; the test compares
// sets, but a sorted literal keeps its diffs readable.
//
// The third field is [Refusal.Doc], an override, not the reference itself:
// empty means "the generated entry", which is the ordinary case.
var refusals = []Refusal{
	{"Ambiguous list-valued identity argument", "A Component.SoleElement identity argument is a statically-written list or set construct with zero elements or more than one; the AWS API, not the configuration's own list order, decides how more than one value composes, so this package will not guess which one to use.", ""},
	{"Circular for_each reference", "A resource's for_each depends on its own instances, directly or through another resource's for_each.", ""},
	{"Circular identity reference", "A resource's identity is composed, directly or transitively, from its own identity.", ""},
	{"Configuration loaded without a static evaluator", "The configuration was not loaded through configs.Parser.LoadConfigDir or the configload package. A caller error, not a configuration one.", ""},
	{"Expression not evaluable here", "Static evaluation of an identity argument panicked and was recovered; most often an expression inside a keyed module resolving, several layers down, back to the module call's own each.key or each.value.", ""},
	{"Identity argument not set", "The argument carrying this type's identity has no value - most often a *_prefix argument used in place of the name itself.", ""},
	{"Identity derived from a sensitive value", "An identity argument reads a sensitive or ephemeral value. Import identities are written to logs and plan output, so neither can be part of one.", ""},
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
	{"Resource type has no orphan recovery", "The type is admitted by the provider's identity schema rather than by the generated admission table, so it plans and applies but the estate-wide sweep will not list it: deleting its last block leaves the live resource with no run proposing to remove it. Reported as a warning.", ""},
	{"Resource type outside the live-markers subset", "The type is absent from the admission table, and neither the provider's identity schema nor the configuration's own arguments settle its identity.", `live/LIMITATIONS.md, "unadmitted-type"`},
	{"Identity table and provider schema disagree", "The identity table and the installed provider's schema differ about a type in a way that is not fatal; reported as a warning.", ""},
	{"Sensitive count expression", "A count expression reads a sensitive or ephemeral value; the instance keys it produces become marker values.", ""},
	{"Sensitive lifecycle.enabled expression", "A lifecycle.enabled expression reads a sensitive or ephemeral value, so whether the resource exists is decided by something this run may not record.", ""},
	{"Sensitive for_each expression", "A for_each expression reads a sensitive or ephemeral value; instance keys become marker values, which are written to the cloud.", ""},
	{"The identity table names something the provider does not have", "The identity table builds a type's identity from an argument the installed provider's schema has no such name for; usually provider-version skew.", ""},
	{"Two resources with the same identity", "Two resource blocks resolve to one identity, so one live object would have two owners.", `live/LIMITATIONS.md, "duplicate-identity"`},
	{"Unresolvable identity", "An identity could not be built because a reference it depends on failed; the reference's own error explains why.", ""},
	{"Unusable data-source result", "The data-read phase handed resolution a result it cannot index: not an absolute data resource instance address, or one resource's instances mixing key kinds. A caller error, not a configuration one.", ""},
	{"Unsupported each.value reference", "each.value is used as other than each.value.<attr> when for_each iterates over a resource.", ""},
	{"for_each key cannot be recorded as a marker", "A for_each key contains a character the tofu-address marker cannot carry.", `live/MARKERS.md, "Ownership semantics"`},
	{"for_each over a resource that is not keyed", "for_each iterates a resource that has no instance keys to iterate - one expanded with count, or one using neither count nor for_each.", ""},
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

// RefusalsWithOwnDoc returns the refusals whose [Refusal.Doc] names a fuller
// treatment somewhere else.
//
// tools/limits-gen skips these when generating live/LIMITATIONS.md's
// enumerated section: a refusal already described by a hand-written entry
// should not also get a one-line generated one under a second heading, or
// the document would answer the same question twice and in two voices.
func RefusalsWithOwnDoc() []Refusal {
	var out []Refusal
	for _, r := range Refusals() {
		if r.Doc != "" {
			out = append(out, r)
		}
	}
	return out
}
