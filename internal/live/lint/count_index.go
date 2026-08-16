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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
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

// countIndexScope narrows checkCountIndex's walk to the arguments that
// could plausibly feed a resource type's live identity, using the same
// classification and identity-table data checkManagedResources (lint.go)
// already computes and internal/live/identity's own Resolve already trusts
// — so this rule and admission can never disagree about what an argument
// does. See [countIndexScopeForType].
type countIndexScope struct {
	// skip is true when this type's identity can never be built from any
	// configuration argument at all, so count.index anywhere in the body is
	// as harmless as it already is in the tofu-address marker value itself
	// (see markerKeysExemptFromCountIndex below). Two cases:
	//
	//   - a RECORD_ADMITTED logical type (ClassifyLogicalType), whose
	//     identity is the persisted record addressed by the resource's own
	//     instance address, never by any argument value - null_resource's
	//     only attributes are "triggers" and a create-time random "id",
	//     terraform_data's identity is likewise not argument-derived.
	//   - a ServerAssigned type (identity.LookupType), whose
	//     Resolve (internal/live/identity/resolve.go) returns
	//     ClassNeedsDiscovery before ever calling identityArgs - not one
	//     configuration attribute is read building such a resolution, so
	//     none can be identity-relevant. Discovery then matches the live
	//     object by its tofu-address marker (internal/live/discovery's
	//     declaredInstances), which stamp (internal/live/stamp's
	//     addressExpr) recomputes fresh from the resource's own address at
	//     every run - never sourced from this or any other configuration
	//     argument - so no config argument's count.index usage can reach
	//     that path either.
	skip bool

	// walkAll is true when identity.LookupType has no row for this type: no
	// data exists to say which arguments are identity-relevant, so every
	// argument at every depth is treated as identity-relevant, exactly as
	// this rule always has (the safe default for an unreviewed type).
	walkAll bool

	// attrs is the set of top-level resource-body attribute names that
	// could feed this type's identity: the union of every
	// identity.Component.Attrs name across the type's Components. Nothing
	// else can, because identity's own identityArgs
	// (internal/live/identity/resolve.go) builds its schema from exactly
	// these names and reads it with a top-level-only
	// hcl.Body.PartialContent - a nested block's content is never consulted
	// for identity, whatever its own attribute names happen to be. Used
	// only when skip and walkAll are both false.
	attrs map[string]bool
}

// countIndexScopeForType computes scope for one resource type, from the two
// classifications lint.go's checkManagedResources already has in hand: lt/
// isLogical from ClassifyLogicalType, and identity.LookupType's own table
// row.
func countIndexScopeForType(resourceType string, lt LogicalType, isLogical bool) countIndexScope {
	if isLogical && lt.Class == ClassRecordAdmitted {
		return countIndexScope{skip: true}
	}

	entry, ok := identity.LookupType(resourceType)
	if !ok {
		return countIndexScope{walkAll: true}
	}
	if entry.ServerAssigned || entry.RecordBacked {
		return countIndexScope{skip: true}
	}

	attrs := make(map[string]bool)
	for _, comp := range entry.Components {
		for _, name := range comp.Attrs {
			attrs[name] = true
		}
	}
	return countIndexScope{attrs: attrs}
}

// checkCountIndex rejects count.index wherever it appears inside a managed
// resource's own configuration body, but only within the arguments scope
// says could plausibly feed this type's identity: a plain argument, a tag
// map value, or nested inside a conditional or template expression that
// only references it indirectly. It is a traversal walk, not a literal
// string match, so every one of those positions is caught the same way
// within an in-scope argument. See [countIndexScope] for what determines
// whether an argument - or the whole resource - is in scope at all, and
// doc.go's "Scope of the count.index rule" for why this rule exists only to
// protect identity, not to police count.index's use in general.
//
// The walk starts from resource.Config, the body left over after
// configs.decodeResourceBlock has already extracted the meta-arguments (see
// metaArgumentAttributes/metaArgumentBlocks above), and additionally skips
// those same names at the top level as a second, explicit line of defense —
// so the count expression itself, and the depends_on/provider/
// lifecycle/connection/provisioner positions, are always out of scope,
// regardless of what they contain.
func checkCountIndex(resource *configs.Resource, addr string, path addrs.Module, scope countIndexScope, issues *[]Issue) {
	if scope.skip {
		return
	}

	body, ok := resource.Config.(*hclsyntax.Body)
	if !ok {
		// JSON-syntax configuration (*.tf.json) parses to a different
		// hcl.Body implementation with no schema-less way to walk into
		// nested blocks. No fixture in this repository (the estate or the
		// limits wing) uses JSON syntax, so this is a documented gap, not a
		// silently accepted one: see doc.go.
		return
	}

	for _, traversal := range countIndexCandidates(body, true, scope) {
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
					"names stop being fungible. count survives under live resource markers only as cardinality "+
					"over a fungible set bound by stable slot markers, not by position (live/LIMITATIONS.md, "+
					`"count-index-in-tag"). Replace count with for_each keyed by a stable identifier`,
				addr,
			),
			Subject: traversal.SourceRange(),
		})
	}
}

// countIndexCandidates collects every variable traversal referenced by the
// given body's own attribute expressions and nested blocks, restricted to
// what scope says is identity-relevant. topLevel is true only for a
// resource's own top-level body, where meta-argument attribute and block
// names are skipped, and - when scope narrows at all - so is everything
// scope excludes: a top-level attribute whose name is not in scope.attrs,
// and every nested block outright, since identity.LookupType's Components
// only ever name top-level attributes (see [countIndexScope]), so nothing
// inside a nested block can be identity-relevant for a type this function
// narrows at all. When scope.walkAll is set - the unreviewed-type default -
// every level below the top is resource-schema content the type itself
// defines, never a meta-argument, so it is walked in full exactly as
// before.
//
// The exprVariables call on an in-scope attribute still walks that
// attribute's whole expression tree - conditionals, templates, function
// calls, nested object keys - looking for count.index used to index into a
// collection at any depth (see unsafeCountIndexRanges); scope only decides
// which top-level attribute gets that treatment, and within one it never
// shortens the walk, only narrows which syntactic position - an
// IndexExpr's Key, as opposed to a scalar position - counts as a hit.
func countIndexCandidates(body *hclsyntax.Body, topLevel bool, scope countIndexScope) []hcl.Traversal {
	var traversals []hcl.Traversal

	for name, attr := range body.Attributes {
		if topLevel {
			if metaArgumentAttributes[name] {
				continue
			}
			if !scope.walkAll && !scope.attrs[name] {
				continue
			}
		}
		traversals = append(traversals, exprVariables(attr.Expr)...)
	}

	for _, block := range body.Blocks {
		if topLevel {
			if metaArgumentBlocks[block.Type] {
				continue
			}
			if !scope.walkAll {
				continue
			}
		}
		traversals = append(traversals, countIndexCandidates(block.Body, false, scope)...)
	}

	return traversals
}

// markerKeysExemptFromCountIndex is the one deliberate exception to this
// rule's "anywhere in the body" reach: object-constructor keys (the shape of
// a `tags = { ... }` argument) whose value is allowed to contain
// count.index because carrying it is that key's specified job, not a leak.
//
// live/MARKERS.md defines tofu-address as "the resource's canonical
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

// exprVariables collects only the traversals reached through
// [unsafeCountIndexRanges] — count.index used to index into a collection,
// as opposed to being rendered as a pure scalar — except when expr is an
// object constructor: then it looks at each entry individually, recurses
// the same way into each entry's value, and omits any entry whose key is in
// markerKeysExemptFromCountIndex outright (a tag-shaped map's marker key is
// allowed to carry count.index in any shape, indexing included, because
// carrying it is that key's specified job — see
// markerKeysExemptFromCountIndex).
func exprVariables(expr hclsyntax.Expression) []hcl.Traversal {
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return unsafeCountIndexRanges(expr)
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

// unsafeCountIndexRanges walks expr's syntax tree and returns every
// variable traversal that appears in one of two shapes, neither of which
// [checkCountIndex] can trust as a pure, injective function of the index
// alone:
//
//   - Inside the Key position of an *hclsyntax.IndexExpr anywhere within
//     it — count.index used to index into a collection
//     (var.list[count.index], aws_x.y[count.index]), directly or through
//     an arithmetic offset inside the Key (var.list[count.index + 1]) — or
//     inside a call to one of countIndexAccessorFunctions, the built-in
//     functions that pick a value out of a collection positionally or by
//     key the same way `[]` does
//     (element(aws_x.y.*.arn, count.index),
//     lookup(var.instance_types, tostring(count.index), "default")). What
//     sits at the picked position is controlled by the collection, not by
//     the index itself, so two different indices are never guaranteed to
//     pick distinct values the way two different indices always produce
//     distinct results under a pure arithmetic or string transform of the
//     index.
//   - Inside the Condition of an *hclsyntax.ConditionalExpr (a `? :`)
//     anywhere within it (count.index == 0 ? a : b) — not the True or
//     False result, which stay subject to this same analysis on their own
//     merits. A conditional's condition selects one of exactly two
//     outcomes regardless of how large count.index's range is, so it is
//     provably non-injective as soon as count exceeds two: with count = 3,
//     `count.index == 0 ? 100 : 200` renders indices 1 and 2 to the
//     identical value 200, so if this were left unrefused, two distinct
//     config addresses could resolve to the same live-marker identity,
//     which is a wrong marker, not merely an unrefined refusal - the one
//     outcome every part of this rule exists to prevent.
//
// Everywhere else — a bare argument, a template ("name-${count.index}"),
// arithmetic (100 + count.index), a result branch of a conditional whose
// own condition does not depend on count.index, an argument to any other
// function — count.index is rendered as a pure, injective function of the
// index alone, with no external collection consulted and no branch
// selection keyed on the index, so the mapping from index to value never
// changes shape independently of the index itself: OpenTofu always
// retires the highest count index first on scale-down, so a surviving
// index's rendered value is exactly reproducible on every run regardless
// of how many higher indices ever existed, and never collides with a
// sibling index's value. Those positions are left alone here — see
// doc.go's "Scope of the count.index rule".
func unsafeCountIndexRanges(expr hclsyntax.Expression) []hcl.Traversal {
	w := &countIndexKeyWalker{seen: make(map[hcl.Range]bool)}
	hclsyntax.Walk(expr, w)
	return w.found
}

// countIndexAccessorFunctions are the built-in functions whose result picks
// one value out of a collection at a computed position or key — the same
// hazard an IndexExpr's Key position carries, spelled as a function call
// instead of `[]` syntax. Terraform/OpenTofu configurations use both
// spellings interchangeably; this project's own reviewed corpus has real,
// currently-refused sites using element() for a splat-expression collection
// no `[]` syntax can index at all (element(aws_x.y.*.arn, count.index)) and
// lookup() keyed on count.index's string form
// (lookup(var.instance_types, tostring(count.index), "default")). slice()
// and chunklist() carry the identical hazard in their bound arguments, with
// no occurrence of either found there, so their inclusion here is the
// conservative default for an unreviewed shape rather than evidence from
// the corpus.
//
// Every argument position of a call to one of these is treated as unsafe,
// not just the specific argument that is conventionally the index or key —
// a traversal misclassified as unsafe here is a spurious refusal, and a
// spurious refusal is the safe direction for this rule to fail in; a wrong
// marker is not.
var countIndexAccessorFunctions = map[string]bool{
	"element":   true,
	"lookup":    true,
	"slice":     true,
	"chunklist": true,
}

// countIndexKeyWalker implements hclsyntax.Walker. It walks the whole
// expression tree looking for three node shapes: an *hclsyntax.IndexExpr,
// where it collects every traversal referenced by the node's own Key
// subexpression; a *hclsyntax.FunctionCallExpr naming one of
// countIndexAccessorFunctions, where it collects every traversal
// referenced by any of the call's arguments; and an
// *hclsyntax.ConditionalExpr, where it collects every traversal referenced
// by the node's own Condition subexpression (not its True/False results,
// which the walk still reaches and examines independently, since Walk
// descends into every child regardless of what Enter does at a parent).
// All three collections use Variables(), which already walks its subtree
// in full, nested occurrences of any of the three shapes included, so a
// doubly-indexed key (var.list[var.other[count.index]]), a nested accessor
// call (element(var.list, element(var.offsets, count.index))), or a
// condition built from a nested conditional are each caught the same way a
// single one is. It never collects a traversal from an IndexExpr's
// Collection, a plain (non-accessor) function call's arguments, a
// conditional's True or False result taken on its own, or anywhere else
// outside these three shapes — a bare or arithmetic count.index reference
// is left uncollected, exactly the scalar positions
// [unsafeCountIndexRanges] documents as safe.
//
// seen dedupes by source range: because the normal depth-first walk also
// visits a nested unsafe node directly (Walk descends into every child
// regardless of what an outer Enter already collected), the same inner
// traversal would otherwise be collected twice — once via the outer node's
// own collection, and again when the walk reaches the inner node and Enter
// fires for it too. A traversal's SourceRange is unique to that syntax
// occurrence, so deduping by range is exact, not a heuristic.
type countIndexKeyWalker struct {
	found []hcl.Traversal
	seen  map[hcl.Range]bool
}

func (w *countIndexKeyWalker) Enter(node hclsyntax.Node) hcl.Diagnostics {
	switch n := node.(type) {
	case *hclsyntax.IndexExpr:
		w.collect(n.Key.Variables())
	case *hclsyntax.FunctionCallExpr:
		if !countIndexAccessorFunctions[n.Name] {
			return nil
		}
		for _, arg := range n.Args {
			w.collect(arg.Variables())
		}
	case *hclsyntax.ConditionalExpr:
		w.collect(n.Condition.Variables())
	}
	return nil
}

func (w *countIndexKeyWalker) Exit(hclsyntax.Node) hcl.Diagnostics {
	return nil
}

func (w *countIndexKeyWalker) collect(traversals []hcl.Traversal) {
	for _, t := range traversals {
		r := t.SourceRange()
		if w.seen[r] {
			continue
		}
		w.seen[r] = true
		w.found = append(w.found, t)
	}
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
