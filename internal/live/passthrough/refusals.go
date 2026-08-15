// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package passthrough is the registry of refusals the live path shows a user
// without having written them.
//
// GitHub issue #110's first two registries cover refusals a live package
// raises: internal/live/lint's Rule table, and internal/live/identity's
// refusals.go. Both are enforced by scanning their own package's source, so
// both are complete by construction.
//
// Neither could see a third class. Identity resolution evaluates every
// count, every for_each and every identity-bearing argument through
// [configs.StaticEvaluator], and appends whatever diagnostics that
// evaluation produces (internal/live/identity/resolve.go's evalPure). Those
// diagnostics belong to the static evaluator, to internal/addrs' reference
// parser, or to HCL itself. They reach the user as refusals exactly like the
// other two classes, and no scan of a live package can find them.
//
// The class is not a long tail. Measured over the corpus #102 assembled,
// pass-through refusals take ranks 1, 3 and 7 of the top seven blockers - 66,
// 57 and 30 configurations of 105 - and the single largest blocker of all is
// one of them. So a LIMITATIONS.md generated from the other two registries
// alone would omit the top of its own list.
//
// An earlier version of this paragraph called them "the three largest
// blockers there are, ahead of every rule either live package owns", which
// is false four times over: unadmitted-type at 58 and logical-resource at 49
// both outrank the third, and unadmitted-type outranks the second. The claim
// came from a since-retired handoff document and was copied into five files
// before an audit recomputed it. Rank 1 is the part that is true and the part
// that matters.
//
// # What is in here, and how completeness is argued
//
// One argument, applied three times. [TestConfigsRefusalsRegistered] scans
// the sources of each origin and requires every summary it finds to appear
// below, and every entry claiming that origin to be raised there. A new
// diagnostic on any of the three paths fails it.
//
// The first version of this file argued differently, and worse. It scanned
// internal/configs and claimed HCL's diagnostic set was "not ours to
// enumerate", resting those entries on a sweep of whatever configurations
// happened to be in the tree. An adversarial audit wrote fourteen
// three-line configurations reaching fourteen unregistered refusals -
// `var.list[9]`, `jsondecode("{{{")`, `substr("abc","x",1)` - which is what
// that argument was worth.
//
// The set is enumerable after all, once the enumeration is scoped to
// evaluation rather than to the whole library. A parse error never reaches
// here: a configuration that will not parse fails at load, long before
// identity resolution runs. So the scan reads HCL's six expression-evaluation
// files and nothing else, which is the same narrowing already applied to
// internal/configs, where only the two static-evaluation files are read.
//
// Two instruments back it up rather than replace it. internal/live/check's
// TestNoUnregisteredRefusalsInTheTree runs both configuration-only passes
// over every configuration in the checkout, and live/corpus-refusals.json's
// totals.refusals_unregistered is asserted at zero. Both would have caught
// the omission above eventually; neither did, because neither corpus
// contains a `jsondecode` over malformed JSON. That is the difference
// between a scan and a sample, and it is why the scan is the argument.
package passthrough

import (
	"fmt"
	"sort"
)

// Origin is which package raised a pass-through diagnostic. It is what
// decides how far the completeness claim above reaches, so it is recorded
// per entry rather than described once in prose.
type Origin string

const (
	// OriginConfigs is internal/configs' static evaluator: the scope that
	// answers var, local, path and terraform references, and the evaluator
	// that rejects a sensitive or ephemeral result. Scan-enforced over its
	// two static-evaluation files.
	OriginConfigs Origin = "internal/configs"

	// OriginAddrs is internal/addrs' reference parser, reached through
	// lang.References before any evaluation happens. Scan-enforced over
	// parse_ref.go.
	OriginAddrs Origin = "internal/addrs"

	// OriginHCL is HCL's own expression evaluation, reached when
	// expr.Value runs against the assembled hcl.EvalContext.
	// Scan-enforced over its six expression-evaluation files.
	OriginHCL Origin = "hcl"
)

// Refusal is one diagnostic the live path passes through, keyed by the
// Summary it carries. The fields are deliberately the same three
// [identity.Refusal] has, so the combined table in internal/live/check can
// hold all three registries without a per-source shape.
type Refusal struct {
	// Summary is the hcl.Diagnostic Summary, and this refusal's identity.
	Summary string

	// What is a one-line description of the configuration shape that
	// triggers it, in the voice live/LIMITATIONS.md's entries use.
	What string

	// Origin is which package raises it. See [Origin].
	Origin Origin
}

// DocsRef is where a user is sent to read about this refusal, in the form
// lint.Rule.DocsRef uses.
//
// Every entry here is documented by the same generated section of
// live/LIMITATIONS.md, under its own Summary as the heading, so unlike
// [identity.Refusal] this has no per-row override: there is no fuller
// treatment of an upstream diagnostic anywhere in live/ for a row to point
// at, and inventing a field for a case that does not exist would be
// speculative.
func (r Refusal) DocsRef() string {
	return fmt.Sprintf("live/LIMITATIONS.md, %q", r.Summary)
}

// refusals is the registry. Keep it sorted by Summary.
//
// Every entry's What describes the configuration shape, not the mechanism.
// A user reading LIMITATIONS.md wants to know which line of their own file
// to change; that the diagnostic came from a static scope's
// StaticValidateReferences is true and useless to them.
var refusals = []Refusal{
	{
		Summary: "Ambiguous attribute key",
		What:    "An object key in a statically evaluated expression is a bare name that could be either a variable reference or a literal string, so which was meant cannot be decided.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Attempt to get attribute from null value",
		What:    "An identity argument, a count or a for_each reads an attribute of something that evaluated to null.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Attempt to index null value",
		What:    "An identity argument, a count or a for_each indexes into something that evaluated to null.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Call to unknown function",
		What:    "A statically evaluated expression calls a function this run does not have. Static evaluation offers the pure standard library only; a provider-defined function needs a running provider.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Circular reference",
		What:    "A local is defined, directly or transitively, in terms of itself. Only local-to-local cycles are detected here: the static scope pushes a frame when it resolves a local and not when it resolves a variable.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Condition is null",
		What:    "A conditional inside a statically evaluated expression has a null condition, so neither branch can be chosen.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Duplicate object key",
		What:    "An object constructor in a statically evaluated expression sets the same key twice.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Dynamic value in static context",
		What:    "An identity argument, a count or a for_each reads a value that only exists once something has been applied: another resource's attribute, or a data source. It is the catch-all of the static-context checks - a module output and a provider function each get their own refusal instead.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Ephemeral value not allowed",
		What:    "A module source or a backend argument resolves to an ephemeral value. It is raised while decoding those two expressions, not during identity resolution: an ephemeral value in an identity argument is refused by identity itself, under \"Identity derived from a sensitive value\".",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Error in function call",
		What:    "A function inside a statically evaluated expression returned an error - jsondecode over text that is not JSON, for instance.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Failed to get working directory",
		What:    "path.cwd could not be resolved because the operating system refused the working directory. An environment failure, not a configuration one.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Function calls not allowed",
		What:    "A function is called where the surrounding context permits none at all.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Inconsistent conditional result types",
		What:    "A conditional's two branches produce types that cannot be reconciled into one.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Incorrect condition type",
		What:    "A conditional's condition is not a boolean and cannot be converted to one - most often a string used where a bool was meant.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Incorrect key type",
		What:    "A map or object is indexed with a key of the wrong type.",
		Origin:  OriginHCL,
	},
	{
		Summary: `Invalid "path" attribute`,
		What:    "path is read with an attribute other than cwd, module or root.",
		Origin:  OriginConfigs,
	},
	{
		Summary: `Invalid "terraform" attribute`,
		What:    "terraform is read with an attribute other than workspace, including the terraform.env removed in v0.12.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Invalid 'for' condition",
		What:    "A for expression's if clause does not evaluate to a boolean.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid attribute in static context",
		What:    "terraform.applying is read where only configuration is available; it has a value during plan and apply, and none here.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Invalid default value for module argument",
		What:    "A variable's default does not fit its own type constraint, so no value for it can be produced.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Invalid expanding argument value",
		What:    "A function call expands an argument with ... over something that is not a list or tuple.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid function argument",
		What:    "A function inside a statically evaluated expression was given an argument of the wrong type or an unacceptable value.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid index",
		What:    "A collection is indexed out of range, or with a key it does not have.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid index key",
		What:    "A reference indexes a resource or module with a key this fork's address parser cannot read - one that is not a literal string or whole number.",
		Origin:  OriginAddrs,
	},
	{
		Summary: "Invalid nested splat expressions",
		What:    "Two splat expressions are nested, which has no defined meaning.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid object key",
		What:    "An object constructor's key does not evaluate to a string and cannot be converted to one.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid operand",
		What:    "An operator inside a statically evaluated expression was given an operand of the wrong type - arithmetic on a string, for instance.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid path step",
		What:    "A traversal steps into a value in a way its type does not support.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid reference",
		What:    "A reference is not a shape this fork's address parser recognises at all - an operator, an index, or a traversal into something that has no attributes.",
		Origin:  OriginAddrs,
	},
	{
		Summary: "Invalid template interpolation value",
		What:    "A ${...} interpolation produces a value with no string form, such as a list or an object.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Invalid value for input variable",
		What:    "The value supplied for a variable does not convert to its declared type.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Iteration over non-iterable value",
		What:    "A for expression iterates over something that is not a collection.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Iteration over null value",
		What:    "A for expression iterates over null.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Missing map element",
		What:    "A map is indexed with a key it does not contain.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Module output not supported in static context",
		What:    "An identity argument, a count or a for_each reads a child module's output. Module outputs are produced by evaluating the module, which has not happened yet.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Not enough function arguments",
		What:    "A function inside a statically evaluated expression was called with too few arguments.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Null condition",
		What:    "A for expression's if clause evaluates to null.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Null value as key",
		What:    "A null is used as an object or map key.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Operation failed",
		What:    "An arithmetic or comparison operator inside a statically evaluated expression failed - division by zero, for instance.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Provider function in static context",
		What:    "A statically evaluated expression calls a provider-defined function, which needs a configured provider this run has not started.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Required variable not set",
		What:    "A non-nullable variable with no default was given no value, so nothing depending on it can be evaluated.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Reserved symbol name",
		What:    "A reference uses a name this fork reserves for future use, so it cannot be read as a reference to anything that exists.",
		Origin:  OriginAddrs,
	},
	{
		Summary: "Sensitive value not allowed",
		What:    "A module source or a backend argument resolves to a sensitive value. Same decoding step as the ephemeral case above, and not the one an identity argument goes through.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Splat of null value",
		What:    "A splat expression is applied to null.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Too many function arguments",
		What:    "A function inside a statically evaluated expression was called with too many arguments.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Unable to compute static value",
		What:    "Something an identity argument, a count or a for_each depends on could not be computed. It is the trailing half of another refusal: the diagnostic before it names what actually failed, and this one names the chain that led there.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Unable to parse provider function",
		What:    "A provider:: function reference is not in the form the address parser accepts.",
		Origin:  OriginAddrs,
	},
	{
		Summary: "Unable to use variable in static context",
		What:    "A variable declared const = false is read where only configuration is available.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Undefined local",
		What:    "A reference names a local the module does not declare.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Undefined variable",
		What:    "A reference names a variable the module does not declare.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Unknown variable",
		What:    "A reference names a symbol that reached evaluation with nothing bound to it - most often each or count read where this run does not supply repetition data.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Unsupported attribute",
		What:    "A statically evaluated expression reads an attribute the value does not have.",
		Origin:  OriginHCL,
	},
	{
		Summary: "Variables not allowed",
		What:    "A reference appears where the surrounding context permits no variables at all.",
		Origin:  OriginHCL,
	},
}

// Refusals returns every pass-through refusal, sorted by Summary.
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
