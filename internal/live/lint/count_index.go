// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
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
	//     terraform_data's identity is likewise not argument-derived. Note
	//     RECORD_ADMITTED specifically and not "record-backed": the other
	//     record-backed class, EXTERNAL_ADMITTED, is exactly the case where
	//     an argument DOES name something outside the record, and it does
	//     not skip.
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
//
// # A logical type is decided by its class alone, and that is deliberate
//
// Both admitted logical classes resolve [identity.ClassRecordBacked], so both
// carry a RecordBacked row, and the RecordBacked disjunct further down used to
// be a second, quieter door onto the same skip - one keyed on the identity
// table rather than on the classification. It reached the same answer for
// every type only because the two sets happened to coincide;
// TestRecordBackedSkipIsRedundantWithTheClassSkip was written when they had
// already silently diverged once (row-gen derived four RecordBacked rows lint
// had no row for at all, and the walk went quiet for them through a leg
// nothing guarded).
//
// [ClassExternalAdmitted] (issue #314) makes the two sets genuinely differ:
// local_file is RecordBacked and must NOT skip, because its filename argument
// names a real file and two instances at distinct addresses hold distinct
// records and still collide on one path. So the logical branch returns for
// every logical type, and the RecordBacked disjunct below is gone rather than
// merely bypassed - a leg that cannot be reached is one nobody can
// accidentally widen. What is left of it is the honest default: a RecordBacked
// row for a type lint does not classify at all now gets walkAll, which refuses
// more, not less.
func countIndexScopeForType(resourceType string, lt LogicalType, isLogical bool) countIndexScope {
	if isLogical {
		if lt.Class == ClassRecordAdmitted {
			return countIndexScope{skip: true}
		}
		// Every other logical class, admitted or refused. EXTERNAL_ADMITTED
		// is the one that reaches checkCountIndex for real (a refused class
		// raises RuleLogicalResource and the resource is out anyway), and it
		// gets the safe default: no identity row names which argument
		// carries the external object, so every argument is treated as
		// carrying it. See [ClassExternalAdmitted].
		return countIndexScope{walkAll: true}
	}

	entry, ok := identity.LookupType(resourceType)
	if !ok {
		return countIndexScope{walkAll: true}
	}
	if entry.ServerAssigned {
		return countIndexScope{skip: true}
	}
	if len(entry.Components) == 0 {
		// No Components and not ServerAssigned is the shape a RecordBacked
		// row has, and removing the RecordBacked disjunct above left it
		// falling through to build an EMPTY attrs set - which puts no
		// argument in scope and is therefore a silent skip wearing a
		// different name. This is walkAll's own documented condition ("no
		// data exists to say which arguments are identity-relevant") reached
		// by a second route, and it answers it the same way.
		//
		// It is unreachable while every RecordBacked row is a type lint
		// classifies, which TestRecordBackedSkipIsRedundantWithTheClassSkip
		// requires. It is written anyway because the pre-#314 code was
		// unreachable in exactly the same way and was reached: row-gen
		// derived four RecordBacked rows lint had no row for, and the walk
		// went quiet for a release.
		return countIndexScope{walkAll: true}
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
func checkCountIndex(ctx context.Context, mod *configs.Module, resource *configs.Resource, addr string, path addrs.Module, scope countIndexScope, issues *[]Issue) {
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

	domain := countIndexDomainFor(ctx, mod, resource, addr)

	for _, hit := range countIndexCandidates(body, true, scope, domain) {
		traversal := hit.traversal
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
			Detail:    countIndexDetail(addr, hit.verdict),
			Subject:   traversal.SourceRange(),
		})
	}
}

// countIndexCandidates collects every count.index traversal referenced by
// the given body's own attribute expressions and nested blocks, restricted
// to what scope says is identity-relevant. topLevel is true only for a
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
// The unsafeCountIndexHits call on an in-scope attribute walks that
// attribute's whole expression tree - conditionals, templates, function
// calls, arithmetic, nested object and list literals - deciding, position
// by position, whether count.index there is a provably injective, stable
// function of the index alone (see [analyzeCountIndexSafety]); scope only
// decides which top-level attribute gets that treatment, and within one it
// never shortens the walk.
func countIndexCandidates(body *hclsyntax.Body, topLevel bool, scope countIndexScope, domain countIndexDomain) []countIndexHit {
	var traversals []countIndexHit

	for name, attr := range body.Attributes {
		if topLevel {
			if metaArgumentAttributes[name] {
				continue
			}
			if !scope.walkAll && !scope.attrs[name] {
				continue
			}
		}
		traversals = append(traversals, unsafeCountIndexHits(attr.Expr, domain)...)
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
		traversals = append(traversals, countIndexCandidates(block.Body, false, scope, domain)...)
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

// unsafeCountIndexHits decides, for expr as a whole, whether
// count.index appears anywhere in it in a shape [checkCountIndex] cannot
// trust as a provably injective, scale-down-stable function of the index
// alone (see [analyzeCountIndexSafety]), and if so returns every
// count.index traversal reachable from expr, for the caller to report as an
// Issue. When expr does not reference count.index at all, or references it
// only in shapes analyzeCountIndexSafety proves safe, it returns nil.
//
// The default here is refuse, not accept: analyzeCountIndexSafety
// enumerates the shapes it can prove injective, and anything else —
// including a node type it has no case for at all — comes back unsafe. See
// its own doc comment for why the rule was inverted this way (GitHub issue
// #217).
//
// A shape analyzeCountIndexSafety cannot prove gets a second, strictly
// stronger question: not "is this expression's SHAPE injective over the
// integers" but "are the values it ACTUALLY renders, one per index this
// resource's count actually expands to, all distinct" — see
// [countIndexDomain.verdict]. That check subsumes the syntactic one
// wherever it can run at all, so it is only consulted second because it is
// the more expensive of the two, never because it is the weaker.
func unsafeCountIndexHits(expr hclsyntax.Expression, domain countIndexDomain) []countIndexHit {
	result := analyzeCountIndexSafety(expr)
	if !result.hasIndex || result.safe {
		return nil
	}
	verdict := domain.verdict(expr)
	if verdict == countIndexDistinct {
		return nil
	}
	hits := make([]countIndexHit, 0, 1)
	for _, t := range countIndexOnlyTraversals(expr.Variables()) {
		hits = append(hits, countIndexHit{traversal: t, verdict: verdict})
	}
	return hits
}

// countIndexHit is one refused count.index reference together with WHY it
// was refused: a demonstrated collision between two instances, or an
// inability to see what the instances render at all. The two want different
// things from the reader - the first is a bug in the configuration, the
// second is usually an unset variable or a reference to something that only
// exists after an apply - so they are reported as different sentences
// rather than as one hedged one.
type countIndexHit struct {
	traversal hcl.Traversal
	verdict   countIndexVerdict
}

// countIndexOnlyTraversals filters traversals down to the ones that resolve
// to count.index itself, via the same addrs.ParseRef check
// [checkCountIndex] applies to its own candidates — so a traversal like
// count.index_typo, or some other identifier that merely starts with the
// keyword "count", is never reported.
func countIndexOnlyTraversals(traversals []hcl.Traversal) []hcl.Traversal {
	var found []hcl.Traversal
	for _, t := range traversals {
		if isCountIndexTraversal(t) {
			found = append(found, t)
		}
	}
	return found
}

// isCountIndexTraversal reports whether t resolves to count.index, via
// addrs.ParseRef the same way [checkCountIndex] validates its own reported
// traversals.
func isCountIndexTraversal(t hcl.Traversal) bool {
	ref, diags := addrs.ParseRef(t)
	if diags.HasErrors() || ref == nil {
		return false
	}
	countAttr, ok := ref.Subject.(addrs.CountAttr)
	return ok && countAttr.Name == "index"
}

// countIndexSafety is the result of [analyzeCountIndexSafety] for one
// subexpression: whether it references count.index at all (hasIndex), and
// — only meaningful when hasIndex is true — whether every reference found
// is a provably injective, scale-down-stable function of the index alone
// (safe). A subexpression with hasIndex false is vacuously safe: it says
// nothing about count.index either way, so it can never be the reason a
// larger expression is refused.
type countIndexSafety struct {
	hasIndex bool
	safe     bool
}

// analyzeCountIndexSafety is checkCountIndex's proof engine. It recurses
// over expr's syntax tree and decides, bottom-up, whether the value expr
// renders is guaranteed to be a distinct, reproducible function of
// count.index for every index in 0..count-1, at every count, and unchanged
// for any index that survives a scale-down — the property
// [countIndexScope]'s callers need to hold before a value built from
// count.index can be trusted not to collide with a sibling instance's
// value once it reaches a live resource's identity.
//
// OpenTofu always retires the highest count index first on scale-down, so
// a scalar function of the index alone that is injective at every count is
// automatically also stable under scale-down: a surviving index's rendered
// value never depended on how many higher indices existed, so it cannot
// change when they are removed. That is why the two halves of the property
// collapse into one check here: injective-at-every-count is sufficient: it
// is not merely necessary.
//
// The shapes proven safe, and why each one holds at count = 1, at a large
// count, and after a scale-down:
//
//   - The bare traversal count.index itself. The identity function is
//     injective by definition; at count = 1 it renders 0, at any larger
//     count every index renders itself, and a surviving index after
//     scale-down renders exactly what it always rendered.
//   - A string template with count.index (or any of these safe shapes)
//     interpolated among literal parts ("name-${count.index}"). Decimal
//     rendering of a non-negative integer is injective (two different
//     integers never share a decimal representation), and concatenating
//     that injective substring between two literal, index-independent
//     strings is still injective: for a fixed prefix and suffix, distinct
//     substrings always produce distinct whole strings. At count = 1 the
//     one instance's string is well-defined; at large count each index's
//     decimal representation stays distinct; scale-down changes nothing
//     about how a surviving index renders.
//   - Addition or subtraction where exactly one operand references
//     count.index (itself safely) and the other does not reference it at
//     all — 100 + count.index, count.index - 1, var.base - count.index.
//     For any fixed value c independent of the index, x -> x+c and x ->
//     c-x are both injective (each is invertible: given the result and c,
//     x is uniquely recoverable), regardless of what c happens to be,
//     including zero or a runtime value like var.base. count.index +
//     count.index is refused: both operands vary with the index, and
//     nothing here proves the combination (e.g. a hypothetical
//     count.index - count.index) stays injective, so it falls to the
//     conservative default. At count = 1 there is one value to check
//     (trivially distinct from nothing); at large count the shift by a
//     fixed c preserves distinctness pairwise; a surviving index after
//     scale-down still adds or subtracts the same fixed c it always did.
//   - Multiplication where exactly one operand references count.index
//     (itself safely) and the other statically evaluates, with no
//     variables in scope, to a known nonzero number — 2 * count.index,
//     count.index * -1. For a fixed nonzero c, x -> c*x is injective (c*x1
//     = c*x2 implies x1 = x2 exactly because c != 0), but x -> 0*x
//     collapses every index to zero, and a multiplier this rule cannot
//     evaluate at all (a var or local reference — this is a syntax-only
//     check with no evaluation context) is refused rather than assumed
//     nonzero: count.index * var.n is not injective when var.n happens to
//     be zero, and nothing here can rule that out. Unary negation
//     (-count.index) is the c = -1 case of this same rule. At count = 1
//     there is one product to check; at large count the fixed nonzero
//     scale factor preserves distinctness; scale-down leaves a surviving
//     index's fixed multiplier unchanged.
//   - A conditional (? :) whose own Condition does not reference
//     count.index, with both the True and False results independently
//     safe (or not referencing count.index at all). Every instance takes
//     the same branch — the branch choice does not vary with the index —
//     so the whole expression reduces to whichever single branch's own,
//     already-proven injective transform of the index. At count = 1 only
//     one branch is ever evaluated; at large count the branch stays fixed
//     across every index so the branch's own injectivity carries through;
//     scale-down does not change which branch a surviving index takes.
//   - A list or object literal every one of whose elements (values only,
//     for an object — see the tofu-address carve-out below for keys) is
//     independently safe or does not reference count.index. Two literals
//     differ as structured values whenever any one element differs, so if
//     one element is an injective function of the index and the rest are
//     fixed or independently injective, the whole structure is injective
//     positionally. This shape is not known to occur in the reviewed
//     corpus; it is included because the recursive proof extends to it for
//     free, not because a real site needed it.
//
// Everything else defaults to unsafe whenever it contains count.index at
// all, including a node type with no case below — an unrecognized shape
// refuses, it does not fall through as safe (GitHub issue #217). Notably
// refused, with the injectivity argument each one fails:
//
//   - Indexing a collection: count.index anywhere in the Key position of
//     an IndexExpr (var.list[count.index], aws_x.y[count.index]), or as an
//     argument to element, lookup, slice, or chunklist — the built-in
//     functions that pick a value out of a collection positionally or by
//     key the same way [] does. The picked value is controlled by the
//     collection's contents, not computed from the index, so nothing
//     guarantees two different indices pick two different values, however
//     the key itself is built.
//   - Modulo, integer division, and any other function this rule has not
//     specifically proven injective (min, max, floor, ceil, abs, signum,
//     pow, format, tostring, and the rest) — none of these preserve
//     injectivity in general, and several (modulo, integer division, min,
//     max) actively collapse distinct indices onto the same value once
//     count grows past the modulus, divisor, or bound. This is the
//     shape GitHub issue #217's audit found accepted before this rule was
//     inverted: 100 + (count.index % 3) renders indices 0 and 3 to the
//     identical value 100 at count = 5.
//   - A conditional whose own Condition reads count.index
//     (count.index == 0 ? a : b): it selects one of exactly two outcomes
//     however large count grows, so it is provably non-injective as soon
//     as count exceeds two.
//   - Comparison and logical operators (==, <, &&, and so on): their
//     result is always one of a small fixed set of values (usually just
//     true/false), which collides as soon as count exceeds that set's
//     size.
func analyzeCountIndexSafety(expr hclsyntax.Expression) countIndexSafety {
	switch e := expr.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if isCountIndexTraversal(e.Traversal) {
			return countIndexSafety{hasIndex: true, safe: true}
		}
		return countIndexSafety{}

	case *hclsyntax.RelativeTraversalExpr:
		// The trailing traversal steps (".id", and so on) are literal
		// attribute names, never expressions, so only Source can possibly
		// reference count.index.
		return analyzeCountIndexSafety(e.Source)

	case *hclsyntax.ParenthesesExpr:
		return analyzeCountIndexSafety(e.Expression)

	case *hclsyntax.LiteralValueExpr:
		return countIndexSafety{}

	case *hclsyntax.TemplateWrapExpr:
		return analyzeCountIndexSafety(e.Wrapped)

	case *hclsyntax.TemplateExpr:
		return analyzeCountIndexParts(e.Parts)

	case *hclsyntax.UnaryOpExpr:
		inner := analyzeCountIndexSafety(e.Val)
		if !inner.hasIndex {
			return inner
		}
		if e.Op == hclsyntax.OpNegate {
			return countIndexSafety{hasIndex: true, safe: inner.safe}
		}
		return countIndexSafety{hasIndex: true, safe: false}

	case *hclsyntax.BinaryOpExpr:
		return analyzeCountIndexBinaryOp(e)

	case *hclsyntax.ConditionalExpr:
		cond := analyzeCountIndexSafety(e.Condition)
		trueResult := analyzeCountIndexSafety(e.TrueResult)
		falseResult := analyzeCountIndexSafety(e.FalseResult)
		hasIndex := cond.hasIndex || trueResult.hasIndex || falseResult.hasIndex
		safe := !cond.hasIndex &&
			(!trueResult.hasIndex || trueResult.safe) &&
			(!falseResult.hasIndex || falseResult.safe)
		return countIndexSafety{hasIndex: hasIndex, safe: safe}

	case *hclsyntax.IndexExpr:
		key := analyzeCountIndexSafety(e.Key)
		collection := analyzeCountIndexSafety(e.Collection)
		hasIndex := key.hasIndex || collection.hasIndex
		// The Key position is unsafe whenever it references count.index at
		// all, regardless of the Key subexpression's own injectivity: the
		// value it picks is controlled by the collection, not computed, so
		// var.list[count.index + 1] is exactly as unsafe as
		// var.list[count.index].
		safe := !key.hasIndex && (!collection.hasIndex || collection.safe)
		return countIndexSafety{hasIndex: hasIndex, safe: safe}

	case *hclsyntax.FunctionCallExpr:
		hasIndex := false
		for _, arg := range e.Args {
			if analyzeCountIndexSafety(arg).hasIndex {
				hasIndex = true
				break
			}
		}
		if !hasIndex {
			return countIndexSafety{}
		}
		// No function is proven injective in count.index here — not even
		// tostring or format, which render decimal digits injectively on
		// their own but are not reasoned about in combination with
		// arbitrary other arguments — so any function call that reaches
		// count.index anywhere in its arguments is refused. element,
		// lookup, slice, and chunklist (the collection-accessor functions
		// GitHub issue #187 specifically named) fall under this same
		// default; they need no special case because none is safe.
		return countIndexSafety{hasIndex: true, safe: false}

	case *hclsyntax.ObjectConsExpr:
		hasIndex, safe := false, true
		for _, item := range e.Items {
			if key, ok := objectKeyLiteral(item.KeyExpr); ok && markerKeysExemptFromCountIndex[key] {
				continue
			}
			if r := analyzeCountIndexSafety(item.KeyExpr); r.hasIndex {
				hasIndex = true
				if !r.safe {
					safe = false
				}
			}
			if r := analyzeCountIndexSafety(item.ValueExpr); r.hasIndex {
				hasIndex = true
				if !r.safe {
					safe = false
				}
			}
		}
		return countIndexSafety{hasIndex: hasIndex, safe: safe}

	case *hclsyntax.TupleConsExpr:
		hasIndex, safe := false, true
		for _, item := range e.Exprs {
			if r := analyzeCountIndexSafety(item); r.hasIndex {
				hasIndex = true
				if !r.safe {
					safe = false
				}
			}
		}
		return countIndexSafety{hasIndex: hasIndex, safe: safe}

	default:
		// A node type with no case above - a for expression, a splat, or
		// anything hclsyntax adds in the future. The conservative default
		// is refuse, not fall through as safe: containment is checked
		// generically via Variables(), which every hclsyntax.Expression
		// implements regardless of its concrete type, so an unrecognized
		// shape that reaches count.index at all is still caught.
		if len(countIndexOnlyTraversals(expr.Variables())) > 0 {
			return countIndexSafety{hasIndex: true, safe: false}
		}
		return countIndexSafety{}
	}
}

// analyzeCountIndexParts implements the template-literal shape of
// [analyzeCountIndexSafety]: every part (a mix of literal text and
// interpolated expressions) must itself be free of count.index or proven
// safe. Concatenating literal, index-independent text around an injective
// substring is still injective — see analyzeCountIndexSafety's own doc
// comment for the argument.
func analyzeCountIndexParts(parts []hclsyntax.Expression) countIndexSafety {
	hasIndex, safe := false, true
	for _, part := range parts {
		if r := analyzeCountIndexSafety(part); r.hasIndex {
			hasIndex = true
			if !r.safe {
				safe = false
			}
		}
	}
	return countIndexSafety{hasIndex: hasIndex, safe: safe}
}

// analyzeCountIndexBinaryOp implements the arithmetic shapes of
// [analyzeCountIndexSafety]: addition, subtraction, and multiplication by a
// statically-known nonzero constant, each requiring that exactly one
// operand reference count.index (and do so safely) while the other does
// not reference it at all. Every other binary operator — modulo, integer
// division, comparisons, and, or — is refused whenever either operand
// references count.index, since none is known to preserve injectivity.
func analyzeCountIndexBinaryOp(e *hclsyntax.BinaryOpExpr) countIndexSafety {
	lhs := analyzeCountIndexSafety(e.LHS)
	rhs := analyzeCountIndexSafety(e.RHS)
	hasIndex := lhs.hasIndex || rhs.hasIndex
	if !hasIndex {
		return countIndexSafety{}
	}

	switch e.Op {
	case hclsyntax.OpAdd, hclsyntax.OpSubtract:
		switch {
		case lhs.hasIndex && rhs.hasIndex:
			return countIndexSafety{hasIndex: true, safe: false}
		case lhs.hasIndex:
			return countIndexSafety{hasIndex: true, safe: lhs.safe}
		default:
			return countIndexSafety{hasIndex: true, safe: rhs.safe}
		}

	case hclsyntax.OpMultiply:
		switch {
		case lhs.hasIndex && rhs.hasIndex:
			return countIndexSafety{hasIndex: true, safe: false}
		case lhs.hasIndex:
			return countIndexSafety{hasIndex: true, safe: lhs.safe && isNonzeroLiteralConstant(e.RHS)}
		default:
			return countIndexSafety{hasIndex: true, safe: rhs.safe && isNonzeroLiteralConstant(e.LHS)}
		}

	default:
		// OpDivide, OpModulo, every comparison, OpLogicalAnd, OpLogicalOr:
		// none is reasoned about as preserving injectivity, so any operand
		// referencing count.index makes the whole expression unsafe.
		return countIndexSafety{hasIndex: true, safe: false}
	}
}

// isNonzeroLiteralConstant reports whether expr statically evaluates, with
// no variables in scope at all, to a known, non-null cty.Number that is not
// zero. Used only for a multiplication's non-index operand: a var or local
// reference fails to evaluate against a nil *hcl.EvalContext (this is a
// syntax-only check with no access to the module's variable values) and is
// therefore treated as not provably nonzero, the safe direction for this
// check to fail in.
func isNonzeroLiteralConstant(expr hclsyntax.Expression) bool {
	val, diags := expr.Value(nil)
	if diags.HasErrors() || val.IsNull() || !val.IsKnown() || val.Type() != cty.Number {
		return false
	}
	return val.AsBigFloat().Sign() != 0
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
