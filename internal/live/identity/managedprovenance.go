// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the provenance half of GitHub issue #187's second pass: given
// an identity argument that evaluated to an unknown, WHERE did the unknown
// come from?
//
// The question has to be asked because two very different situations produce
// the same cty.UnknownVal at [resolver.stringValue]:
//
//   - The caller performed a managed read or a managed plan this run, and the
//     value it handed back is unknown until the sibling resource is applied.
//     aws_acm_certificate's domain_validation_options is the carrier: the AWS
//     provider fills each element's domain_name during PlanResourceChange and
//     leaves resource_record_name unknown. Measured against the real provider
//     in internal/live/projection's TestPlanInstancesAgainstTheAWSProvider.
//
//   - internal/live/check's loader substituted cty.UnknownVal for a required
//     root variable nothing set (check/load.go's variableValues.value). That
//     is the #183 cohort, which must stay refused, and it is the path
//     tools/refusal-probe, "just corpus" and live-check all take.
//
// cty cannot tell them apart. Marks were tried and refuted - IsMarked() is
// this fork's panic guard in roughly seventy places and internal/live/marksafe
// is a static prover requiring exactly that shape, so a second mark kind is
// indistinguishable from a sensitivity mark to every one of those guards.
//
// So the discriminator is structural, and it is deliberately NARROW. Three
// things must all hold before an unknown is attributed to a managed read, and
// the first of them makes the whole classification inert for any run that
// supplied no managed results at all - which is every offline corpus run:
//
//  1. This run was given [Context.ManagedResults]. A resolver with none can
//     never reach the classification, whatever the configuration says.
//
//     Measured by mutation: forcing this condition true breaks no test,
//     because conditions 3's two legs are each individually sufficient today -
//     one needs an entry in the results index, the other needs an expansion
//     built from one. It is kept because that sufficiency rests on how the
//     shared index is KEYED (data addresses carry a "data." prefix, managed
//     ones do not - see [newResolver]), and the safety property here should
//     not depend on a keying convention two seams away. Reported as an
//     untested condition rather than left looking load-bearing.
//  2. The expression names no root variable directly. An unset variable is
//     the collision this exists to avoid, so any expression that could be
//     drawing its unknown from one is not attributed. This under-attributes
//     - "${var.prefix}-${each.value.name}" is refused rather than classified
//     - which is the safe direction: a missing marker outranks a wrong one.
//  3. Either the expression itself names an attribute of a managed resource
//     this run's results cover and whose covered value is not wholly known,
//     or it reads each.* inside a block whose for_each was built from such a
//     reference.
//
// The third condition's second leg is what the ACM shape needs: the refused
// argument is `name = each.value.name`, which mentions no resource at all.
// The provenance lives on the expansion, recorded when the for_each value was
// evaluated - see [expansion.managedFrom].

// managedFromExpr reports the managed resource block whose covered-but-unknown
// value expr reads, when there is exactly the kind of one condition 3's first
// leg describes - directly, or through a chain of locals or module
// variables expr names but does not itself read into.
//
// The returned address is module-relative and rendered the way the
// configuration writes it, because it goes into an operator-facing sentence.
//
// # Why this chases local and var
//
// The founding case for [managedFromScope]'s "each.value" leg is
// `for_each = { for k, v in aws_x.y.z : ... }`, a for_each source that names
// the resource directly. A real module composition routinely puts a local
// between the two instead -
// `local.rows = [for v in aws_x.y.z : merge(v, {...})]` then
// `element(local.rows, count.index)["k"]` - and neither leg of condition 3
// sees the resource at all: this expression's own [hcl.Expression.Variables]
// names "local.rows", not "aws_x.y.z", and there is no each.value in a
// count-based reference for [managedFromScope] to ask about either. The
// resource is there; it is one name away. See [scope.chaseNamed], which
// asks the identical question of local.rows's OWN defining expression,
// evaluated in the scope [resolver.namedDef] says that expression owns -
// [hcl.Expression.Variables] walks the WHOLE expression tree regardless of
// how many for-comprehensions, merge() calls or other functions sit between
// the traversal root and the reference, so a chain of pure structure never
// hides the resource from this check the way it does from
// [resolver.selectStatic]'s literal-shape decomposition, which exists to
// recover a VALUE rather than to answer "is a covered resource in here
// somewhere".
//
// Bounded by [maxManagedProvenanceChase] for [maxStaticDecomposeDepth]'s own
// reason: an ordinary configuration is two or three locals deep, and the
// bound turns a pathological or self-referential chain into "not
// attributed" rather than unbounded recursion.
func (r *resolver) managedFromExpr(expr hcl.Expression, scope instScope) (string, bool) {
	return r.managedFromExprAt(expr, scope, 0)
}

// maxManagedProvenanceChase bounds [resolver.managedFromExpr]'s chase
// through locals and module variables. See that function's doc comment.
const maxManagedProvenanceChase = 16

func (r *resolver) managedFromExprAt(expr hcl.Expression, scope instScope, depth int) (string, bool) {
	if !r.managedResults || expr == nil || depth > maxManagedProvenanceChase {
		return "", false
	}
	travs := expr.Variables()
	// Depth 0 is the identity argument itself, where [namesAVariable]'s
	// blunt rule is the right one - see its own doc comment. Past depth 0,
	// this is a local's or a module variable's own defining expression, one
	// hop removed from anything the caller wrote, and
	// [resolver.namesAnUnprovenVariable] is the narrower, still-safe
	// question for that case. See its own doc comment for the site that
	// needed it: terraform-aws-modules/acm's
	// `try(aws_x.y.z, var.fallback)`.
	unproven := namesAVariable
	if depth > 0 {
		unproven = r.namesAnUnprovenVariable
	}
	if unproven(travs) {
		return "", false
	}

	// Every candidate this level's own traversals name, direct or chased,
	// collected rather than returned on the first hit. A single reference
	// is the overwhelming case at depth 0 (one identity argument, one
	// attribute), but the CHASE this file adds reaches expressions no
	// caller wrote by hand - a local built for one purpose and reused by
	// many resources, terraform-aws-modules/alb's own combined listener
	// configuration among them, can legitimately name more than one
	// covered-but-unknown managed resource in the same breath (an ACM
	// certificate beside an unrelated Cognito user pool, say). Picking
	// whichever one Variables() happened to list first would attribute an
	// argument to a resource it may have nothing to do with - a wrong
	// "waiting on" claim is not a wrong marker, but it is a wrong claim,
	// and this package's own rule (a missing attribution outranks a wrong
	// one) applies here exactly as it does to condition 2's var check.
	// TestManagedFromDeclinesAmbiguousMultiResourceLocal pins it.
	found := map[string]bool{}
	for _, trav := range travs {
		if addr, ok := r.managedUnknownAt(trav); ok {
			found[addr] = true
		}
	}
	// Condition 3's first leg, one hop out: expr does not read the resource
	// directly, but it reads a local or a module variable whose OWN
	// definition might. Every traversal root this expression names is a
	// candidate, not only ones [namedDef] would call the "head" of a
	// selector - `element(local.rows, count.index)` names "local.rows" as
	// one of exactly two Variables() results (the other, "count.index", is
	// repetition data with no definition to chase).
	for _, trav := range travs {
		if len(trav) < 2 {
			continue
		}
		root, ok := trav[0].(hcl.TraverseRoot)
		if !ok || (root.Name != "local" && root.Name != "var") {
			continue
		}
		nameStep, ok := trav[1].(hcl.TraverseAttr)
		if !ok {
			continue
		}
		if addr, ok := r.managedFromNamed(root.Name, nameStep.Name, scope, depth+1); ok {
			found[addr] = true
		}
	}
	if len(found) != 1 {
		return "", false
	}
	for addr := range found {
		return addr, true
	}
	return "", false
}

// managedFromNamed is [resolver.managedFromExprAt]'s one hop through
// [resolver.namedDef]: it looks up what "local.name" or "var.name" is
// defined as, in the scope that definition owns, and asks the identical
// provenance question of that expression.
//
// namedDef's own module-switching (a "var" hop reads the CALLING module's
// argument expression) is exactly what a value chase needs here too - the
// resource a module argument's own expression names lives in the caller,
// not in the module receiving it - and restore() undoes it before this
// returns, on every path, so a chase that finds nothing leaves the resolver
// exactly where it was.
func (r *resolver) managedFromNamed(root, name string, scope instScope, depth int) (string, bool) {
	defExpr, defScope, _, restore, ok := r.namedDef(root, name, scope)
	if !ok {
		return "", false
	}
	defer restore()
	return r.managedFromExprAt(defExpr, defScope, depth)
}

// managedFromScope is condition 3's second leg: the argument reads each.key or
// each.value, and this instance's expansion was built from a managed value
// this run supplied. See [expansion.managedFrom].
func (r *resolver) managedFromScope(expr hcl.Expression, scope instScope) (string, bool) {
	if !r.managedResults || expr == nil || scope.managedFrom == "" {
		return "", false
	}
	travs := expr.Variables()
	if namesAVariable(travs) {
		return "", false
	}
	for _, trav := range travs {
		if len(trav) == 0 {
			continue
		}
		if root, ok := trav[0].(hcl.TraverseRoot); ok && root.Name == "each" {
			return scope.managedFrom, true
		}
	}
	return "", false
}

// managedFrom is [resolver.managedFromExpr] and [resolver.managedFromScope]
// together: the one question every caller actually asks, and the one that
// keeps the two legs' precedence in a single place.
func (r *resolver) managedFrom(expr hcl.Expression, scope instScope) (string, bool) {
	if addr, ok := r.managedFromExpr(expr, scope); ok {
		return addr, true
	}
	return r.managedFromScope(expr, scope)
}

// namesAVariable reports whether any traversal roots at `var`. It is
// condition 2, stated once, for the TOP-level identity argument expression -
// [resolver.managedFromScope]'s each.value leg, and [resolver.managedFromExprAt]
// at depth 0, before any chase through a local or a module variable has
// happened.
//
// It looks only at DIRECT references, which is the whole of what an
// expression's own Variables() can see: a variable reached through a local is
// invisible here. For the each.value leg that is not a hole, because an
// expression that reads a local names no managed resource and no each.*
// either, so neither leg of condition 3 can fire for it in the first place.
// [resolver.managedFromExprAt]'s own chase (depth > 0) is exactly the
// exception that stopped being true the moment it existed - see
// [resolver.namesAnUnprovenVariable], which is what depth > 0 uses instead of
// this.
func namesAVariable(travs []hcl.Traversal) bool {
	for _, trav := range travs {
		if len(trav) == 0 {
			continue
		}
		if root, ok := trav[0].(hcl.TraverseRoot); ok && root.Name == "var" {
			return true
		}
	}
	return false
}

// namesAnUnprovenVariable is condition 2 restated for a CHASED expression -
// a local's or a module variable's own defining expression, reached only
// through [resolver.managedFromExprAt]'s recursion - where the blunt "any
// var anywhere in this expression" rule measurably misattributes a real
// site.
//
// The carrier is terraform-aws-modules/acm's own
// `try(aws_acm_certificate.this[0].domain_validation_options,
// var.acm_certificate_domain_validation_options)`: the var is a documented
// fallback for when no certificate is created, sitting in try()'s SECOND
// argument, syntactically unconnected to the resource reference in the
// first. [namesAVariable]'s rule bails on the whole expression because a
// var root exists ANYWHERE in it, which is the right call for a TOP-level
// argument that might INTERPOLATE a var and a resource attribute into one
// ambiguous unknown ("${var.prefix}-${each.value.name}") - but this
// expression is not that: the var and the resource sit in two different
// try() arguments that can never both be "the" value at once, and the
// managed reference this function is asked about was found independently,
// by [resolver.managedUnknownAt], before this check ever runs.
//
// So the question this asks is narrower and answerable: could ANY of the
// var references in this expression be issue #183's own hazard - the
// offline check loader's silent cty.UnknownVal substitute for a REQUIRED
// root variable nothing set (check/load.go's variableValues)? A variable
// with its own default can never be that substitute
// (prepareFinalInputVariableValue only reaches for the stub when
// v.Required() is true), so a chased expression naming only variables WITH
// defaults carries none of that ambiguity, whatever else it also names.
// r.mod is deliberately read fresh rather than carried from the caller:
// [resolver.managedFromNamed]'s own "var" hop has already switched modules
// by the time this runs, exactly the module a var.NAME in the chased
// expression is declared in.
//
// A variable this module does not declare at all - which normal
// configuration loading would already have refused before resolution ever
// ran - is treated as unproven, the same fail-closed direction every other
// branch here takes.
func (r *resolver) namesAnUnprovenVariable(travs []hcl.Traversal) bool {
	for _, trav := range travs {
		if len(trav) < 2 {
			continue
		}
		root, ok := trav[0].(hcl.TraverseRoot)
		if !ok || root.Name != "var" {
			continue
		}
		nameStep, ok := trav[1].(hcl.TraverseAttr)
		if !ok {
			return true
		}
		if r.mod == nil {
			return true
		}
		decl, declared := r.mod.Variables[nameStep.Name]
		if !declared || decl.Required() {
			return true
		}
	}
	return false
}

// SummaryNonStaticIdentityArgument is [resolver.stringValue]'s refusal for a
// value that is not wholly known. It is spelled out at the raise site as a
// literal, because internal/live/refusalscan reads summaries statically and
// fails the build on one it cannot see there; this constant exists only for
// the withdrawal below, which has to be sure of what it is removing.
const SummaryNonStaticIdentityArgument = "Non-static identity argument"

// siblingApplyResolution turns this instance's sibling-apply refusals into a
// [ClassNeedsDiscovery] resolution, withdrawing the diagnostics they raised,
// and reports whether it applied at all.
//
// It applies only when EVERY component that failed did so for this reason.
// hardFailed is the whole of that test and it fails closed: a component that
// could not be resolved for any other reason leaves the instance refused, its
// diagnostics untouched, exactly as before. That ordering matters because the
// alternative - classifying whenever any sibling-apply refusal is present -
// would silently swallow a real refusal standing beside it.
//
// The diagnostics are removed by index and only when the diagnostic at that
// index still carries the summary [resolver.stringValue] raised. A mismatch
// cannot happen through any path this package has today, and the check is
// what makes it harmless if one is ever added: the worst outcome becomes a
// leftover refusal beside the classification, not a different resource's
// error disappearing.
func (r *resolver) siblingApplyResolution(addr addrs.AbsResourceInstance, sibMark int, sibArgs []string, hardFailed bool) (Resolution, bool) {
	if hardFailed || len(r.pendingSiblingApply) <= sibMark {
		return Resolution{}, false
	}
	mine := r.pendingSiblingApply[sibMark:]

	drop := make(map[int]bool, len(mine))
	sibling := ""
	for _, ref := range mine {
		if ref.diagIdx >= 0 && ref.diagIdx < len(r.diags) &&
			r.diags[ref.diagIdx].Description().Summary == SummaryNonStaticIdentityArgument {
			drop[ref.diagIdx] = true
		}
		if sibling == "" {
			sibling = ref.sibling
		}
	}
	if len(drop) > 0 {
		kept := make(tfdiags.Diagnostics, 0, len(r.diags)-len(drop))
		for i, d := range r.diags {
			if drop[i] {
				continue
			}
			kept = append(kept, d)
		}
		r.diags = kept
	}
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]

	args := append([]string{sibling}, sibArgs...)
	return Resolution{
		Addr:  addr,
		Class: ClassNeedsDiscovery,
		Reason: fmt.Sprintf(
			"%s takes %s from %s, which the provider does not fill in until %s is applied, so the value is not known until that object exists.",
			addr.String(), orList(sibArgs), sibling, sibling),
		Cause:     DiscoverySiblingApply,
		CauseArgs: args,
	}, true
}

// managedUnknownAt reports whether trav names an attribute of a managed
// resource this run's results cover AND the covered value is not wholly
// known. It is [resolver.managedCovered] plus the knownness test, which is
// what separates "the read answered" from "the read answered with an unknown".
//
// # Whole object, unless the referenced attribute can be isolated safely
//
// The knownness and sensitivity questions were once asked of the whole
// covered object rather than of the selected attribute - "selecting would
// mean walking trav's remaining steps into a value a provider process
// produced, which is a marked-value hazard for no gain," reasoned when this
// package's only covered shapes had no unrelated sensitive attribute to
// collide with. aws_acm_certificate's schema does:
// private_key/private_key_wo are marked sensitive on every instance,
// including one whose own certificate has not been created yet, so
// val.ContainsMarked() is true for THAT reason alone, on an object whose
// domain_validation_options - the attribute this call is actually about -
// is neither sensitive nor the source of the mark. Measured on
// corpus-alb-complete: without isolating the attribute, every ACM/Route53
// validation site this file exists to attribute silently declined, for a
// field the identity argument never reads.
//
// [selectReferencedValue] walks the SAME steps [resolver.managedCovered]
// already proved present (the instance key, then trav's remaining
// attribute/index steps) without ever calling a cty operation on a value
// that is itself marked - the hazard the original comment warned about -
// because each step's receiver is validated unmarked before its own
// GetAttr/Index runs. The selected leaf's own marks and knownness are what
// decide the answer; the surrounding object's are no longer asked. A shape
// selection cannot handle (an index or a key selection walk declines) falls
// back to the CONTAINER'S own answer, which is today's behaviour, applied
// exactly where it already was.
func (r *resolver) managedUnknownAt(trav hcl.Traversal) (string, bool) {
	if !r.managedCovered(trav) {
		return "", false
	}
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() {
		return "", false
	}
	var res addrs.Resource
	var key addrs.InstanceKey
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		res = subj
	case addrs.ResourceInstance:
		res = subj.Resource
		key = subj.Key
	default:
		return "", false
	}
	val, ok := r.dataIndex[r.modInst.String()][res.String()]
	if !ok || val == cty.NilVal {
		return "", false
	}

	target := val
	if leaf, selected := selectReferencedValue(val, key, ref.Remaining); selected {
		target = leaf
	}

	// ContainsMarked before IsWhollyKnown: the latter iterates collections
	// and panics on a marked one. A marked result is not attributed at all -
	// a sensitive value has its own refusal in [resolver.stringValue], which
	// is the message it deserves.
	if target.ContainsMarked() {
		return "", false
	}
	if target.IsWhollyKnown() {
		return "", false
	}
	return res.String(), true
}

// selectReferencedValue walks from a resource's own aggregate value (one
// object for an unkeyed block, a tuple for count, an object keyed by string
// for for_each - the shape [identity.Context.ManagedResults]/DataResults are
// documented to carry) down to the single leaf a reference's own instance
// key and remaining steps name, without ever touching a value that is
// itself marked: every step first confirms its RECEIVER is unmarked (and
// the shape it expects) before calling GetAttr/Index on it, so a mark
// nested inside a SIBLING attribute the walk never visits - private_key
// beside domain_validation_options, the carrier this exists for - is never
// read and can never panic anything downstream.
//
// ok is false the moment the walk cannot confirm a step - an unexpected
// type, an out-of-range index, a key the aggregate does not carry - and the
// caller falls back to asking its own question of the whole container,
// exactly as it did before this function existed. This never widens what
// gets attributed: it only narrows the marked-value and knownness checks to
// the piece a reference actually names, when that piece can be reached
// with certainty.
func selectReferencedValue(val cty.Value, key addrs.InstanceKey, remaining hcl.Traversal) (cty.Value, bool) {
	if val == cty.NilVal || val.IsMarked() || !val.IsKnown() {
		return cty.NilVal, false
	}
	cur := val
	switch k := key.(type) {
	case nil:
		// No key: val is already the single instance's own object.
	case addrs.IntKey:
		ty := cur.Type()
		if !ty.IsTupleType() && !ty.IsListType() {
			return cty.NilVal, false
		}
		idx := int(k)
		if idx < 0 || idx >= cur.LengthInt() {
			return cty.NilVal, false
		}
		cur = cur.Index(cty.NumberIntVal(int64(idx)))
	case addrs.StringKey:
		ty := cur.Type()
		switch {
		case ty.IsObjectType():
			if !ty.HasAttribute(string(k)) {
				return cty.NilVal, false
			}
			cur = cur.GetAttr(string(k))
		case ty.IsMapType():
			cur = cur.Index(cty.StringVal(string(k)))
		default:
			return cty.NilVal, false
		}
	default:
		return cty.NilVal, false
	}
	for _, step := range remaining {
		if cur == cty.NilVal || cur.IsMarked() || !cur.IsKnown() {
			return cty.NilVal, false
		}
		attr, isAttr := step.(hcl.TraverseAttr)
		if !isAttr {
			// An index step into the selected attribute's own value
			// (`....arn[0]`, say) is not a shape any Component this
			// package resolves needs isolated further; decline rather
			// than guess at collection-vs-tuple semantics here.
			return cty.NilVal, false
		}
		ty := cur.Type()
		if !ty.IsObjectType() || !ty.HasAttribute(attr.Name) {
			return cty.NilVal, false
		}
		cur = cur.GetAttr(attr.Name)
	}
	if cur == cty.NilVal {
		return cty.NilVal, false
	}
	return cur, true
}

// tolerantManagedValue is [resolver.resolveExpr]'s true last resort for one
// identity argument, tried only after evalStatic, namedLeaf,
// resolveSelection, resolveTransformCall, [resolver.tolerantPart] and
// [resolver.foldedSelect] have all declined.
//
// # The shape it closes
//
// [resolver.tolerantEvaluator] already makes a managed-resource or
// data-source reference inside one of THIS module's own locals or outputs
// become an unknown rather than refusing the whole expression - built for a
// module OUTPUT read in the middle of a larger expression
// (tolerantmodule.go) and for a module-call ARGUMENT
// (partialargs.go's tolerantVariables). Neither of those doors is the one a
// plain identity argument walks through when it reads a SAME-MODULE local
// directly: `element(local.rows, count.index)["k"]` over
// `local.rows = distinct([for k, v in aws_x.y.z : merge(v, {...})])` is a
// FunctionCallExpr wrapped in an IndexExpr, not a bare traversal, so
// [resolver.namedLeaf]'s hcl.AbsTraversalForExpr gate never lets it reach
// the local's own definition at all, and [resolver.tolerantPart] never
// tries [resolver.tolerantEvaluator] in the first place - only
// [resolver.tolerantVariables], which has nothing to substitute when no
// module-call argument is involved. The whole expression is handed instead
// to [configs.StaticEvaluator]'s strict evaluator, which raises "Dynamic
// value in static context" or "Unable to compute static value" - summaries
// that name no identity argument at all and give
// [resolver.markerFallback]/[resolver.siblingApplyResolution] nothing to
// act on.
//
// # Why this is not [resolver.tolerantPart] with a different evaluator
//
// tolerantPart exists to rescue a SIBLING leaf the substitution does not
// touch, and its whole safety argument is that the retried value comes back
// WHOLLY KNOWN - only then is it provably independent of the substituted
// unknown. This function is for the opposite outcome: the identity argument
// ITSELF is what the managed resource poisons, so the honest answer is an
// unknown, not a value. An unknown, non-null, unmarked result is handed to
// [resolver.stringValueIn] exactly the way a direct reference's own unknown
// already is - the identical "Non-static identity argument" diagnostic, the
// identical [resolver.managedFrom] provenance check (now able to see through
// the local, see [resolver.managedFromExpr]), the identical
// [siblingApplyRefusal] bookkeeping - so an instance whose only obstacle was
// a hard evaluator refusal reaches the same withdraw-and-reclassify machinery
// [resolver.siblingApplyResolution] already gives a direct reference. A
// value that comes back wholly known is left alone here (false): it already
// had a route through tolerantPart, and if that declined too there is
// nothing this adds.
//
// # What it cannot do
//
// It cannot make a value known that tolerantPart could not, and it never
// invents one: [resolver.tolerantEvaluator] substitutes cty.DynamicVal for a
// refused reference and nothing else, count.index/each.key/each.value keep
// coming from this instance's own real repetition data
// ([resolver.evalPure]'s WithRepetitionData, unaffected by which evaluator
// r.eval points at), and every gate [resolver.stringValueIn] applies -
// sensitivity, null, wholly-known - runs on the result exactly as it does
// for any other value this package resolves.
func (r *resolver) tolerantManagedValue(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier, diagMark, sibMark int) ([]Part, bool) {
	if r.eval == nil {
		return nil, false
	}
	if len(impureCallsIn(expr)) > 0 {
		return nil, false
	}
	// Provenance FIRST, before the retry is even attempted:
	// [resolver.tolerantEvaluator] substitutes an unknown for ANY refused
	// managed-resource, data-source or module-output reference, run with
	// [Context.ManagedResults] or without it - it has no notion of "this
	// run supplied nothing at all". Without this gate, the identical
	// configuration TestDataReferenceRefusesWithoutResults holds to the
	// harsh "Dynamic value in static context" refusal would instead soften
	// to "Non-static identity argument" for a data reference this run never
	// read - dataresults.go's and this file's own #183 distinction, applied
	// here rather than only to a bare variable: "the value is unknown
	// because a live read has not happened yet" is a different claim from
	// "the value is unknown because this run's own inputs are incomplete",
	// and only [resolver.managedFrom]'s success proves the first one.
	if _, sibOK := r.managedFrom(expr, scope); !sibOK {
		return nil, false
	}
	eval := r.tolerantEvaluator(r.modInst, 0)
	if eval == nil {
		return nil, false
	}
	saved := r.eval
	r.eval = eval
	val, diags := r.evalPure(expr, scope, ident)
	r.eval = saved
	if diags.HasErrors() || val == cty.NilVal {
		return nil, false
	}

	// The value from THIS retry only, never composed with anything
	// evalStatic's failed attempt left behind: diagMark/sibMark are the
	// marks the caller captured before evalStatic ran, so both are rolled
	// back to exactly that point before stringValueIn runs and records its
	// own, single diagnostic - the friendlier one replaces the harsher one
	// entirely, it is never stapled on top of it.
	r.diags = r.diags[:diagMark]
	r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	s, ok := r.stringValueIn(val, expr, scope, ident)
	if !ok {
		return nil, false
	}
	return []Part{{Literal: s}}, true
}
