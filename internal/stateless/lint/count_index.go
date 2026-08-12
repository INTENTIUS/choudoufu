// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
)

// metaArgumentAttributes and metaArgumentBlocks are the top-level names
// configs.ResourceBlockSchema (internal/configs/resource.go) extracts from a
// resource block before what remains becomes resource.Config: count,
// for_each, provider, and depends_on as attributes; locals, lifecycle,
// connection, provisioner, and the "_" meta-argument escape hatch as blocks.
//
// hclsyntax.Body.PartialContent does not remove these from the underlying
// *hclsyntax.Body's Attributes/Blocks fields — it only tracks them as
// "hidden" for later calls to Content/PartialContent/JustAttributes on the
// same body — so a direct field walk of resource.Config, as checkCountIndex
// does, still sees them unless they are skipped explicitly. That skip is
// this rule's scope boundary: it is applied only at the top level (depth 0)
// of a resource's own body, so the count expression itself, provider,
// depends_on, and the lifecycle/connection/provisioner blocks are never
// walked, while genuine nested blocks the resource type defines (an
// aws_security_group's ingress block, for example) are walked in full at
// every depth below that. See doc.go's "Scope of the count.index rule" for
// the reasoning.
var (
	metaArgumentAttributes = map[string]bool{
		"count":      true,
		"for_each":   true,
		"provider":   true,
		"depends_on": true,
	}
	metaArgumentBlocks = map[string]bool{
		"locals":      true,
		"lifecycle":   true,
		"connection":  true,
		"provisioner": true,
		"_":           true,
	}
)

// checkCountIndex rejects count.index wherever it appears inside a managed
// resource's own configuration body: a plain argument, a tag map value, a
// nested block, or nested inside a conditional or template expression that
// only references it indirectly. It is a traversal walk, not a literal
// string match, so every one of those positions is caught the same way.
//
// The walk starts from resource.Config, the body left over after
// configs.decodeResourceBlock has already extracted the meta-arguments (see
// metaArgumentAttributes/metaArgumentBlocks above), and additionally skips
// those same names at the top level as a second, explicit line of defense —
// so the count expression itself, and the depends_on/provider/
// lifecycle/connection/provisioner positions, are always out of scope,
// regardless of what they contain.
func checkCountIndex(resource *configs.Resource, addr string, path addrs.Module, issues *[]Issue) {
	body, ok := resource.Config.(*hclsyntax.Body)
	if !ok {
		// JSON-syntax configuration (*.tf.json) parses to a different
		// hcl.Body implementation with no schema-less way to walk into
		// nested blocks. No fixture in this repository (the estate or the
		// limits wing) uses JSON syntax, so this is a documented gap, not a
		// silently accepted one: see doc.go.
		return
	}

	for _, traversal := range countIndexCandidates(body, true) {
		ref, refDiags := addrs.ParseRef(traversal)
		if refDiags.HasErrors() || ref == nil {
			continue
		}
		countAttr, ok := ref.Subject.(addrs.CountAttr)
		if !ok || countAttr.Name != "index" {
			continue
		}

		*issues = append(*issues, Issue{
			Rule:      RuleCountIndex,
			Construct: fmt.Sprintf("count.index in %s", addr),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s's configuration reads count.index: the lexical index of a count instance is "+
					"not stable across scale-up, scale-down, or reordering, so a property built from "+
					"it cannot be recovered from the live system with no memory, and the instances it "+
					"names stop being fungible. count survives in stateless mode only as cardinality "+
					"over a fungible set bound by stable slot markers, not by position (stateless/LIMITATIONS.md, "+
					`"count-index-in-tag"). Replace count with for_each keyed by a stable identifier`,
				addr,
			),
			Subject: traversal.SourceRange(),
		})
	}
}

// countIndexCandidates collects every variable traversal referenced by the
// given body's own attribute expressions and nested blocks. topLevel is true
// only for a resource's own top-level body, where meta-argument attribute
// and block names are skipped; every level below that is resource-schema
// content the type itself defines, never a meta-argument, so it is walked in
// full.
func countIndexCandidates(body *hclsyntax.Body, topLevel bool) []hcl.Traversal {
	var traversals []hcl.Traversal

	for name, attr := range body.Attributes {
		if topLevel && metaArgumentAttributes[name] {
			continue
		}
		traversals = append(traversals, exprVariables(attr.Expr)...)
	}

	for _, block := range body.Blocks {
		if topLevel && metaArgumentBlocks[block.Type] {
			continue
		}
		traversals = append(traversals, countIndexCandidates(block.Body, false)...)
	}

	return traversals
}

// markerKeysExemptFromCountIndex is the one deliberate exception to this
// rule's "anywhere in the body" reach: object-constructor keys (the shape of
// a `tags = { ... }` argument) whose value is allowed to contain
// count.index because carrying it is that key's specified job, not a leak.
//
// stateless/MARKERS.md defines tofu-address as "the resource's canonical
// config address ... including module path and any for_each or count
// instance key" — for a count instance that address is, and will permanently
// remain, something like aws_eip.this[2]. That is not an identity-bearing
// property this rule exists to protect; it IS the marker doing its specified
// job of recording which config address a live resource occupies. Contrast
// tofu-slot, deliberately left out of this set: MARKERS.md specifies it as
// an opaque counter "independent of" the config address, so count.index
// appearing there would be exactly the anti-pattern this rule exists to
// catch, not a second exemption from it.
var markerKeysExemptFromCountIndex = map[string]bool{
	"tofu-address": true,
}

// exprVariables is expr.Variables(), except when expr is an object
// constructor: then it looks at each entry individually and omits the
// traversals of any entry whose key is in markerKeysExemptFromCountIndex,
// rather than blanket-collecting every traversal the expression contains
// (which is what expr.Variables() does, and is exactly right for every
// other expression shape — conditionals, templates, tuples, function calls —
// none of which carry a per-entry key to exempt anything by).
func exprVariables(expr hcl.Expression) []hcl.Traversal {
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return expr.Variables()
	}

	var traversals []hcl.Traversal
	for _, item := range obj.Items {
		if key, ok := objectKeyLiteral(item.KeyExpr); ok && markerKeysExemptFromCountIndex[key] {
			continue
		}
		traversals = append(traversals, exprVariables(item.ValueExpr)...)
	}
	return traversals
}

// objectKeyLiteral extracts an object-constructor key's literal string, for
// the two forms the marker keys are ever written in: a bare identifier
// (tofu-address = ...), handled by [hcl.ExprAsKeyword], and a constant
// quoted string ("tofu-address" = ...), handled by evaluating the key
// expression with no variables in scope. Any other key form (one built from
// an interpolation, a variable, a function call) reports not-ok: a marker
// key is never written that way in practice, and if it ever were, failing to
// recognize it as exempt is the safe direction for this rule to fail in.
func objectKeyLiteral(keyExpr hcl.Expression) (string, bool) {
	if kw := hcl.ExprAsKeyword(keyExpr); kw != "" {
		return kw, true
	}
	val, diags := keyExpr.Value(nil)
	if diags.HasErrors() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
		return "", false
	}
	return val.AsString(), true
}
