// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// The impure-function rule (audit finding F-IMPURE).
//
// uuid(), timestamp() and bcrypt() return a different value every time they
// are called. An identity built from one is not an identity: it names a
// different cloud object on every run, so the first apply creates a resource,
// the marker records a name nothing will ever compute again, and every plan
// after it proposes a create. Nothing looks wrong at any point - the value is
// a real, known string - which is what made this quiet enough to reach the
// audit.
//
// The fix is two layers, and both are needed:
//
//   - The resolver evaluates through a PURE static scope
//     ([configs.StaticEvaluator.Pure]), so an impure function returns unknown
//     rather than a plausible value. That covers every path into the
//     expression, including JSON-syntax configuration and functions reached
//     indirectly through a local.
//   - This file names the problem when it can see it, so the operator reads
//     "timestamp() cannot be part of an identity" instead of the generic
//     "not statically knowable" that unknown values produce. A named error
//     is the whole difference between a rule and a mystery.

// impureFunctions is lang's own list (internal/lang/functions.go), which is
// what PureOnly makes unpredictable. Keeping the same three names here means
// the diagnostic and the evaluation agree about what is impure; a function
// added there and not here still gets refused, just with the generic
// unknown-value message.
var impureFunctions = map[string]bool{
	"uuid":      true,
	"timestamp": true,
	"bcrypt":    true,
}

// impureCallsIn returns the names of the impure functions an expression
// calls, deduplicated and sorted, looking through every nesting level of the
// expression.
//
// Only native-syntax expressions can be walked, so a .tf.json configuration
// returns nothing here and is caught by the pure scope instead. Calls reached
// indirectly - an identity argument that reads a local whose value is
// uuid() - are likewise not visible here and are caught by the pure scope,
// which is why that layer is the load-bearing one and this one is the
// message.
func impureCallsIn(expr hcl.Expression) []string {
	node, ok := expr.(hclsyntax.Node)
	if !ok {
		return nil
	}

	found := make(map[string]bool)
	// The visitor below returns nil on every path, so VisitAll's accumulated
	// diagnostics are always empty.
	_ = hclsyntax.VisitAll(node, func(n hclsyntax.Node) hcl.Diagnostics {
		call, ok := n.(*hclsyntax.FunctionCallExpr)
		if !ok {
			return nil
		}
		if name, isImpure := impureFunctionName(call.Name); isImpure {
			found[name] = true
		}
		return nil
	})
	if len(found) == 0 {
		return nil
	}

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// impureFunctionName maps a call as written to the base function name, so
// that the namespaced spelling core::uuid() is recognized as the same
// function as uuid().
func impureFunctionName(called string) (string, bool) {
	name := called
	if _, after, found := strings.Cut(called, "::"); found {
		name = after
	}
	return name, impureFunctions[name]
}

// orListQuoted renders function names for a diagnostic: `uuid()`,
// `timestamp() or uuid()`, and so on.
func orListQuoted(names []string) string {
	calls := make([]string, 0, len(names))
	for _, name := range names {
		calls = append(calls, name+"()")
	}
	switch len(calls) {
	case 0:
		return ""
	case 1:
		return calls[0]
	case 2:
		return calls[0] + " and " + calls[1]
	default:
		return strings.Join(calls[:len(calls)-1], ", ") + ", and " + calls[len(calls)-1]
	}
}
