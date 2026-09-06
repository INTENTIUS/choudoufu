// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package staticeval is the config-subset evaluator the live path shares:
// the one place that says what "statically evaluable" means, the one place
// that evaluates it, and the one place the count/for_each key derivation and
// the read-one-argument-off-a-resource-body walk live.
//
// It exists because the subset had been re-implemented once per consumer -
// identity's evalPure/isSymbolic, lint's staticCount/staticForEachKeys,
// dataread's staticEvalExpr, discovery's staticArgumentValue, foreign's
// staticString - and seven implementations of one language subset drift
// apart without anything noticing. HANDOFF's "The order" item 3 says this
// evaluator is the estate-wide demand computation live-import, live-mv,
// live-check, discovery and the instruments all consume, and that it does
// NOT retire when the plan-node seam lands, so it is worth having exactly
// one of.
//
// It is a leaf on purpose: configs, hcl and cty (plus addrs, lang, tfdiags
// and instances, each of which internal/configs already depends on), and
// nothing under internal/live. That is what lets identity, lint, dataread,
// discovery and foreign - which import each other in several directions -
// all call it without a cycle.
//
// scope.go holds the allowlist predicates: which traversal roots a static
// scope can answer, and which ones an evaluator will answer or refuse for
// itself rather than panic on.
package staticeval

import (
	"github.com/hashicorp/hcl/v2"
)

// Allowed reports whether a traversal root names something the module's
// static scope can answer without a plan: an input variable, a local value,
// path.*, terraform.* or tofu.*.
//
// The predicate is a pre-filter, not an optimization. internal/configs'
// staticScopeData panics by contract ("Not Available in Static Context") on
// a repetition, resource, module, output or check reference, so an
// expression mentioning one of those must not reach the evaluator at all.
// Every caller that hands an arbitrary expression to
// [configs.StaticEvaluator] runs this over expr.Variables() first.
func Allowed(root string) bool {
	switch root {
	case "var", "local", "path", "terraform", "tofu":
		// Evaluable in a static scope.
		return true
	}
	return false
}

// AllowedExpr reports whether every traversal in expr has an [Allowed]
// root. A nil expression is not special-cased here; a caller that treats
// "no expression" as evaluable says so itself.
func AllowedExpr(expr hcl.Expression) bool {
	_, bad := FirstDisallowed(expr)
	return !bad
}

// FirstDisallowed returns the root name of the first traversal in expr that
// [Allowed] refuses, and whether there was one. The name is returned rather
// than only a bool because several callers put it in the refusal they
// render ("its %s argument refers to %s, which is not known until the run is
// under way").
func FirstDisallowed(expr hcl.Expression) (root string, found bool) {
	for _, trav := range expr.Variables() {
		if !Allowed(trav.RootName()) {
			return trav.RootName(), true
		}
	}
	return "", false
}

// Evaluable is the wider root set: [Allowed] plus count, module, data and
// self. It answers a different question - "will the evaluator deal with
// this root itself" rather than "can the static scope produce a value for
// it" - and the two sets are deliberately not the same.
//
// A caller pre-filtering to keep panics OUT of the static evaluator wants
// [Allowed]. A caller deciding whether an expression escapes the subset
// entirely - identity's isSymbolic, which is asking "is there a managed
// resource in here" - wants this one, because count.index, module.*,
// data.* and self.* are each either evaluated in a richer scope or rejected
// with a message of their own, and neither outcome is "handle this
// structurally as a resource reference".
func Evaluable(root string) bool {
	switch root {
	case "count", "module", "data", "self":
		return true
	}
	return Allowed(root)
}
