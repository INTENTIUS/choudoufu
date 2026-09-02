// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// The reserved-symbol rule.
//
// Issue #378 added one symbol to the language's "terraform"/"tofu" object,
// [markers.ModulePrefixAttr], whose value is the evaluating module INSTANCE's
// own escaped marker prefix. It exists so internal/live/stamp can write a
// tofu-address for a resource declared inside a module call with more than
// one instance, where no literal in the one shared configuration body is
// right for every instance.
//
// It is the tool's, not the operator's, and this rule is what makes that
// true rather than merely intended. Three reasons, in the order they matter:
//
//   - A marker built by hand from it is a marker this pass does not verify.
//     The whole reason #378's fix is safe is that stamp composes the prefix
//     with an escaped resource address it computed itself; an operator
//     composing it with something else - a name, a path, an unescaped
//     address - produces a string discovery will never match, silently, on a
//     real object. HANDOFF.md's safety rule puts that above every other
//     consideration.
//
//   - Its value is deliberately undefined in half the places a configuration
//     is evaluated. internal/configs' static evaluator refuses it unless a
//     caller threaded a module instance in, so an expression that reads it
//     works during plan and fails during identity resolution, which is a
//     confusing failure to hand someone who did nothing else wrong.
//
//   - It is this fork's own addition to a language surface stock OpenTofu
//     also owns. Leaving it readable would make a configuration that depends
//     on it non-portable to stock, which is the opposite of the compatibility
//     promise.
//
// The rule fires on the reference, wherever it appears, and never on
// anything this fork writes: stamp's injection happens in memory, in a later
// pass than [CheckWith], and is never serialized back to a file - see
// internal/live/stamp/doc.go, "The seam: configuration synthesis, before the
// plan runs". So a configuration reaching this rule with the symbol in it is
// one a person typed.
//
// Two bounds, stated rather than implied.
//
// It covers every expression reachable from a *configs.Module's own decoded
// constructs - resource, data and ephemeral bodies including their nested
// blocks, module call bodies, provider blocks, locals, outputs, import
// blocks, checks, and the count/for_each meta-arguments of each. It does not
// re-read the module's source files, so an expression in a construct this
// package's loader does not decode is not seen. That is the same boundary
// every other rule in this package works within.
//
// It reads native syntax only: a body this fork loaded from .tf.json is an
// hcl.Body the JSON parser produced rather than an *hclsyntax.Body, and it is
// walked past rather than scanned. That is the "filter narrower than the
// loader" shape, named here because it is real and because it is bounded:
// the consequence is that a JSON configuration could READ the symbol without
// this rule saying so. It could not write a wrong marker with it - stamping
// declines a JSON body outright ([stamp.SkipNotHCL]) - and internal/configs'
// static evaluator still refuses the symbol wherever no module instance was
// threaded in, so the value such a configuration could obtain is the module
// instance's real prefix during plan and a hard refusal everywhere else.
// Closing it properly needs a JSON-body traversal reader this package does
// not have; widening the reservation is not worth inventing one for.
func checkReservedSymbols(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	var found []hcl.Range
	seen := map[string]struct{}{}
	note := func(expr hcl.Expression) {
		if expr == nil {
			return
		}
		for _, trav := range expr.Variables() {
			if !reservedMarkerTraversal(trav) {
				continue
			}
			rng := trav.SourceRange()
			key := fmt.Sprintf("%s\x00%d\x00%d", rng.Filename, rng.Start.Line, rng.Start.Column)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			found = append(found, rng)
		}
	}
	noteBody := func(body hcl.Body) {
		syn, ok := body.(*hclsyntax.Body)
		if !ok {
			return
		}
		walkSyntaxBody(syn, note)
	}

	for _, res := range allResources(mod) {
		note(res.Count)
		note(res.ForEach)
		noteBody(res.Config)
	}
	for _, call := range mod.ModuleCalls {
		note(call.Count)
		note(call.ForEach)
		noteBody(call.Config)
	}
	for _, pc := range mod.ProviderConfigs {
		if pc != nil {
			noteBody(pc.Config)
		}
	}
	for _, l := range mod.Locals {
		note(l.Expr)
	}
	for _, o := range mod.Outputs {
		note(o.Expr)
	}
	for _, imp := range mod.Import {
		if imp != nil {
			note(imp.ID)
			note(imp.ForEach)
		}
	}
	for _, c := range mod.Checks {
		if c.DataResource != nil {
			noteBody(c.DataResource.Config)
		}
	}

	// Sorted by position, numerically rather than as text: an issue list this
	// package builds has to be the same on every run, and "10" sorts before
	// "9" as a string.
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Start.Line != b.Start.Line {
			return a.Start.Line < b.Start.Line
		}
		return a.Start.Column < b.Start.Column
	})

	for _, rng := range found {
		*issues = append(*issues, Issue{
			Rule:      RuleReservedSymbol,
			Construct: markers.ModulePrefixRef,
			Module:    path,
			Detail: fmt.Sprintf(
				"%s is this fork's own symbol, not part of the OpenTofu language: it carries the "+
					"ownership marker prefix of the module instance being evaluated, and it exists so "+
					"that a resource declared inside a module call with more than one instance can be "+
					"given a %s that varies per instance. It is written into a resource's marker tags "+
					"by this fork and is not readable from a configuration. A configuration that "+
					"referenced it would also not run on stock OpenTofu, and its value is undefined "+
					"during static evaluation, where the module instance is not known. Remove the "+
					"reference; if you are writing a %s by hand, build it from a variable the module "+
					"call passes through from its own each.key",
				markers.ModulePrefixRef, markers.TagAddress, markers.TagAddress,
			),
			Subject: rng,
		})
	}
}

// reservedMarkerTraversal reports whether one traversal names the reserved
// marker symbol, under either of the two roots the language binds it to.
//
// internal/lang/eval.go builds one map of "terraform" attributes and binds it
// to BOTH the "terraform" and "tofu" objects, so terraform.marker_module_prefix
// and tofu.marker_module_prefix are the same symbol and reserving one without
// the other would reserve nothing.
func reservedMarkerTraversal(trav hcl.Traversal) bool {
	if len(trav) < 2 {
		return false
	}
	root, ok := trav[0].(hcl.TraverseRoot)
	if !ok || (root.Name != "terraform" && root.Name != "tofu") {
		return false
	}
	attr, ok := trav[1].(hcl.TraverseAttr)
	return ok && attr.Name == markers.ModulePrefixAttr
}

// walkSyntaxBody visits every attribute expression in a native-syntax body
// and in every block nested inside it, at any depth.
//
// hclsyntax expressions report their references recursively through
// Variables(), so one call per attribute covers a template, a function call's
// arguments, a for expression and a conditional's branches alike; what
// Variables() does NOT cross is a block boundary, which is what this walk is
// for.
func walkSyntaxBody(body *hclsyntax.Body, visit func(hcl.Expression)) {
	if body == nil {
		return
	}
	for _, attr := range body.Attributes {
		if attr != nil {
			visit(attr.Expr)
		}
	}
	for _, blk := range body.Blocks {
		if blk != nil {
			walkSyntaxBody(blk.Body, visit)
		}
	}
}

// allResources is every managed, data and ephemeral resource in one module,
// which is the population this rule scans. A reference is refused wherever it
// is written, not only where a marker could be stamped: the symbol is
// reserved, and a data source reading it would be just as non-portable and
// just as undefined under static evaluation.
func allResources(mod *configs.Module) []*configs.Resource {
	out := make([]*configs.Resource, 0, len(mod.ManagedResources)+len(mod.DataResources)+len(mod.EphemeralResources))
	for _, set := range []map[string]*configs.Resource{mod.ManagedResources, mod.DataResources, mod.EphemeralResources} {
		for _, res := range set {
			out = append(out, res)
		}
	}
	return out
}
