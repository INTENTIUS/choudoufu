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
// three pass-through refusals are the three largest blockers there are - 66,
// 57 and 30 configurations of 105, ahead of every rule either live package
// owns. A LIMITATIONS.md generated from the other two registries alone would
// omit the top of its own list.
//
// # What is in here, and how completeness is argued
//
// Two different arguments, because the two halves have different evidence.
//
// The internal/configs half is complete by the same scan the other two
// registries use: [TestConfigsRefusalsRegistered] parses
// internal/configs/static_scope.go and static_evaluator.go and requires every
// Summary literal there to appear below. A new static-evaluation diagnostic
// cannot be added upstream without failing that test.
//
// The internal/addrs and HCL halves cannot be argued that way. addrs' parser
// raises one Summary from nine sites, and HCL's expression evaluation is a
// third-party surface whose diagnostic set is not ours to enumerate - a scan
// of it would demand entries for parse errors that can never reach here,
// which would be fiction rather than documentation. What backs those entries
// instead is a sweep: [OriginHCL] and [OriginAddrs] entries are the ones
// observed across 1572 loadable configurations spanning .corpus/, live/ and
// internal/**/testdata, which is the broadest configuration set in the
// checkout. The instrument that keeps them honest is live/corpus-refusals.json's
// totals.refusals_unregistered, asserted at zero by
// internal/live/check's TestCorpusArtifactHasNoUnregisteredRefusals.
//
// So: the configs half cannot silently grow, and the other two halves cannot
// silently grow past what the corpus covers. That is a weaker claim than the
// first, and it is stated here rather than left for a reader to discover.
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
	// that rejects a sensitive or ephemeral result. Scan-enforced.
	OriginConfigs Origin = "internal/configs"

	// OriginAddrs is internal/addrs' reference parser, reached through
	// lang.References before any evaluation happens. Sweep-observed.
	OriginAddrs Origin = "internal/addrs"

	// OriginHCL is HCL's own expression evaluation, reached when the
	// assembled hcl.EvalContext does not answer a traversal.
	// Sweep-observed.
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
		Summary: "Circular reference",
		What:    "A local or variable is defined, directly or transitively, in terms of itself.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Dynamic value in static context",
		What:    "An identity argument, a count or a for_each reads a value that only exists once something has been applied: another resource's attribute, a data source, or any reference other than var, local, path and terraform.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Ephemeral value not allowed",
		What:    "A statically evaluated expression resolves to an ephemeral value, which by definition is not written down anywhere this run can read back.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Failed to get working directory",
		What:    "path.cwd could not be resolved because the operating system refused the working directory. An environment failure, not a configuration one.",
		Origin:  OriginConfigs,
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
		Summary: "Invalid reference",
		What:    "A reference is not a shape this fork's address parser recognises at all - an operator, an index, or a traversal into something that has no attributes.",
		Origin:  OriginAddrs,
	},
	{
		Summary: "Invalid value for input variable",
		What:    "The value supplied for a variable does not convert to its declared type.",
		Origin:  OriginConfigs,
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
		Summary: "Module output not supported in static context",
		What:    "An identity argument, a count or a for_each reads a child module's output. Module outputs are produced by evaluating the module, which has not happened yet.",
		Origin:  OriginConfigs,
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
		Summary: "Sensitive value not allowed",
		What:    "A statically evaluated expression resolves to a sensitive value in a position that would write it somewhere readable.",
		Origin:  OriginConfigs,
	},
	{
		Summary: "Unable to compute static value",
		What:    "Something an identity argument, a count or a for_each depends on could not be computed. It is the trailing half of another refusal: the diagnostic before it names what actually failed, and this one names the chain that led there.",
		Origin:  OriginConfigs,
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
