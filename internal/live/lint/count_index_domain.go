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
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
)

// The rule this file implements, stated once:
//
//	An expression that reads count.index is admitted exactly when the
//	values it actually renders — one per index in the resource's real
//	index range — are all known, pure, of one type, and pairwise
//	distinct.
//
// That is not a proof about the expression's shape. It is the property
// [analyzeCountIndexSafety]'s shape proofs exist to approximate, checked
// directly and exhaustively over its entire domain. Where it can run it is
// therefore strictly stronger than any syntactic rule: it is not a wider
// list of blessed shapes but the absence of a list, and the shapes it
// admits are whatever the operators and functions in the expression turn
// out to do, not whatever this package thought to write down.
//
// Two consequences worth stating explicitly, because both are cases a
// shape rule gets wrong in the safe-but-useless direction and this one gets
// exactly right:
//
//   - count.index % 3 is refused at count = 5 (indices 0 and 3 collide on
//     the same rendered value — the defect that reopened GitHub issue #192)
//     and ADMITTED at count = 3, where the modulo is the identity map and
//     no two instances can collide. Injectivity is a property of a function
//     ON A DOMAIN; the domain is knowable here, so there is no reason to
//     answer as if it were not.
//   - min(count.index, 5) is admitted at count <= 6 and refused at count =
//     7, which is precisely where it stops being injective. No case in this
//     file mentions min, modulo, format, or any other operation by name.
//
// The soundness of "exhaustive over the domain" rests on the domain being
// the real one. Every way that could fail to hold is a refusal below, not
// an assumption: a count this package cannot evaluate, an expression
// reaching outside the static scope, an unknown or sensitive rendered
// value, and a count larger than [countIndexDomainMax] all report "cannot
// prove" and fall back to the syntactic rule, which refuses by default.
//
// Scale-down stability, the second half of what [checkCountIndex] needs,
// comes free for the same reason it does in analyzeCountIndexSafety:
// OpenTofu retires the highest index first, and every value here is
// rendered from the index alone against a configuration that is otherwise
// fixed, so a surviving index renders after a scale-down exactly what it
// rendered before. What this — like the syntactic rule before it — does not
// and cannot promise is stability across an edit to the configuration
// itself (a count = var.n whose var.n also appears in the rendered value);
// that is a configuration change, and drift and live-mv are what answer it.

// countIndexDomainMax bounds how many indices [countIndexDomain] will
// evaluate and compare. It exists for cost, not for correctness: the
// comparison is pairwise, so the work grows as the square of the count, and
// a configuration with a five-figure count would otherwise spend real time
// in a lint rule. Exceeding it is not an error and not an acceptance — the
// domain simply reports itself unavailable and the syntactic rule decides,
// which is what happened for every count before this file existed.
const countIndexDomainMax = 256

// countIndexStaticRoots is the set of reference roots an expression may
// read and still be answerable from configuration alone. It is deliberately
// the same set internal/live/identity guards its own count and module-key
// evaluation with (var, local, path, terraform, tofu), plus count itself,
// which is the variable being bound.
//
// This set is what closes the hole GitHub issue #187 asks about. A
// collection-indexing expression — var.subnets[count.index],
// element(local.azs, count.index) — is admitted here only when the
// COLLECTION is itself static, because a collection rooted at a managed
// resource, a data source, or a module output names a root not in this set
// and never reaches evaluation at all. The index being static is not
// sufficient and is not what is checked; the whole expression has to be.
var countIndexStaticRoots = map[string]bool{
	"var":       true,
	"local":     true,
	"path":      true,
	"terraform": true,
	"tofu":      true,
	"count":     true,
}

// countIndexDomain is a resource's real index range, together with the
// evaluators that can render an expression at each index in it - one per
// INSTANCE of the module the resource is written in, since a module
// instance's var.* answers depend on what its own caller passed it. An
// unavailable domain (ok false) answers every question with "cannot
// prove", which leaves [unsafeCountIndexHits] exactly the syntactic
// rule it had before.
type countIndexDomain struct {
	ctx     context.Context
	subject string
	insts   []countIndexModuleInstance
	ok      bool
}

// countIndexModuleInstance is one module instance's half of a
// [countIndexDomain]: the evaluator that instance's expressions are read
// through, and the count that instance's own count expression evaluates to
// under it. The count is per instance because a module argument can feed
// the count - `count = var.pod_size` - and two instances of one call can be
// passed different values for it.
type countIndexModuleInstance struct {
	eval *configs.StaticEvaluator
	n    int
}

// countIndexDomainFor evaluates the resource's own count expression to
// learn how many instances it expands to. It refuses — reporting an
// unavailable domain rather than an error — for every count this package
// cannot pin down: a resource with no count at all (for_each and single
// resources have no count.index to bind), a module with no static
// evaluator, a count reaching outside [countIndexStaticRoots], and a count
// that evaluates to something unknown, sensitive, null, negative, not a
// whole number, or larger than [countIndexDomainMax].
//
// The evaluation goes through [configs.StaticEvaluator.Pure], the same
// gate internal/live/identity uses, so an impure function anywhere in the
// count (timestamp, uuid) yields unknown and the domain is unavailable.
//
// # Which evaluator, and why there is more than one
//
// A resource written inside a module that is CALLED with for_each or count
// has one index range per module instance, and - more to the point - one
// set of var.* answers per module instance. [configs.Module]'s own frozen
// closure cannot supply those (see module_instance_eval.go), so a call
// argument reading each.key used to make every expression downstream of it
// unprovable and this rule refused a configuration stock plans without
// complaint: GitHub issue #580.
//
// So the per-instance evaluators are tried first, and the module's own
// frozen closure is the fallback for everything they cannot cover - a
// static call chain, an unenumerable key set, a cross product past
// [lintModuleInstanceMax], a total index range past [lintModuleRenderMax].
// The fallback is not a weaker answer being accepted: it is precisely the
// evaluator this rule used before per-instance evaluation existed, so the
// worst case of every bound in this file is today's verdict rather than a
// new one.
func countIndexDomainFor(ctx context.Context, cfg *configs.Config, resource *configs.Resource, addr string) countIndexDomain {
	if cfg == nil || cfg.Module == nil {
		return countIndexDomain{}
	}
	mod := cfg.Module
	if resource.Count == nil || mod.StaticEvaluator == nil {
		return countIndexDomain{}
	}
	if !countIndexStaticallyScoped(resource.Count) {
		return countIndexDomain{}
	}

	if evals, ok := moduleInstanceEvaluators(ctx, cfg); ok {
		if domain, ok := countIndexDomainOver(ctx, evals, resource, addr); ok {
			return domain
		}
	}
	domain, _ := countIndexDomainOver(ctx, []*configs.StaticEvaluator{mod.StaticEvaluator.Pure()}, resource, addr)
	return domain
}

// countIndexDomainOver evaluates the resource's count expression once under
// every evaluator it is given, and reports the domain only when every one
// of them answers. All-or-nothing is the point: a module instance whose
// count this pass cannot see is one whose instances it also cannot compare,
// and admitting the rest would be proving distinctness over part of the
// population while reporting it over the whole.
func countIndexDomainOver(ctx context.Context, evals []*configs.StaticEvaluator, resource *configs.Resource, addr string) (countIndexDomain, bool) {
	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   addr,
		DeclRange: resource.Count.Range(),
	}

	insts := make([]countIndexModuleInstance, 0, len(evals))
	total := 0
	for _, eval := range evals {
		if eval == nil {
			return countIndexDomain{}, false
		}
		// EvaluateStructural, not Evaluate: a count expression built from
		// length(var.x) (or similar) needs only var.x's own SHAPE - how many
		// elements it has - never any one element's contents. A list-of-objects
		// module argument where one sibling field is genuinely resource-derived
		// (terraform-aws-modules/security-group's ingress_with_cidr_blocks
		// pattern, cidr_blocks set from a VPC module's own output) still has a
		// perfectly well-defined length even though that one field does not;
		// see EvaluateStructural's own doc for why Evaluate cannot tell the two
		// apart and this can. GitHub issue #304.
		val, diags := eval.EvaluateStructural(ctx, resource.Count, ident)
		if diags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.ContainsMarked() {
			return countIndexDomain{}, false
		}
		num, err := convert.Convert(val, cty.Number)
		if err != nil {
			return countIndexDomain{}, false
		}
		var n int
		if err := gocty.FromCtyValue(num, &n); err != nil {
			return countIndexDomain{}, false
		}
		if n < 0 || n > countIndexDomainMax {
			return countIndexDomain{}, false
		}
		total += n
		if total > lintModuleRenderMax {
			return countIndexDomain{}, false
		}
		insts = append(insts, countIndexModuleInstance{eval: eval, n: n})
	}
	if len(insts) == 0 {
		return countIndexDomain{}, false
	}
	return countIndexDomain{ctx: ctx, subject: addr, insts: insts, ok: true}, true
}

// countIndexVerdict is what [countIndexDomain.verdict] concluded.
type countIndexVerdict int

const (
	// countIndexUnprovable means the values this expression renders could
	// not be seen at all: no evaluable count, a reference outside the
	// static scope, or a value that came back unknown, sensitive or of a
	// varying type. It says nothing about whether the instances collide.
	countIndexUnprovable countIndexVerdict = iota

	// countIndexCollides means the values WERE rendered and at least two
	// indices produced the same one. This is a defect in the
	// configuration, not a gap in this package.
	countIndexCollides

	// countIndexDistinct means the values were rendered and all differ.
	countIndexDistinct
)

// verdict is the rule at the top of this file. It reports whether expr
// renders a distinct value at every index in the domain, and therefore
// whether every instance of this resource carries a different value for
// the argument expr belongs to - distinguishing "they collide" from "the
// values could not be seen", which are different answers with different
// remedies (see [countIndexVerdict]).
//
// A count of zero or one is distinct vacuously: with no pair of instances
// to collide, there is nothing an index-derived value can collide with. A
// count of zero additionally means the resource does not exist at all.
//
// Everything else is checked by rendering. Each index is bound as
// count.index through [configs.StaticEvaluator.WithRepetitionData] — the
// same seam a resource's own identity resolution uses — and the resulting
// value must be error-free, wholly known, non-null, and unmarked before it
// counts as evidence of anything. Any index that fails to render answers
// the whole question "cannot prove", because an expression whose value this
// package cannot see is one whose collisions it also cannot see.
// Under a module called with for_each or count the question is asked once
// per module INSTANCE, because that is the population a count.index
// collision would be inside: index 2 of module.m["a"] and index 2 of
// module.m["b"] are different resources at different addresses, and issue
// #580's whole point is that the values they render differ whenever the
// call passes them different arguments. What this does NOT ask is whether
// two DIFFERENT module instances collide with each other - that is a
// collision between two whole rendered identities rather than between two
// indices of one count, and internal/live/identity's own
// checkCollisions is the pass that compares those, over the real
// identities rather than over one argument of them. A configuration whose
// module call passes every instance the SAME prefix is refused there, and
// was before this change too.
//
// Verdicts are combined so that the answer does not depend on which
// instance is visited first: a demonstrated collision anywhere is reported
// as a collision, whatever some other instance could not show, because it
// is a fact about the configuration either way. Absent one, an instance
// that could not be rendered at all makes the whole answer unprovable.
func (d countIndexDomain) verdict(expr hclsyntax.Expression) countIndexVerdict {
	if !d.ok {
		return countIndexUnprovable
	}
	if d.maxCount() <= 1 {
		return countIndexDistinct
	}
	if !countIndexStaticallyScoped(expr) {
		return countIndexUnprovable
	}

	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   d.subject,
		DeclRange: expr.Range(),
	}

	unprovable := false
	for _, inst := range d.insts {
		switch inst.verdict(d.ctx, expr, ident) {
		case countIndexCollides:
			return countIndexCollides
		case countIndexUnprovable:
			unprovable = true
		}
	}
	if unprovable {
		return countIndexUnprovable
	}
	return countIndexDistinct
}

// maxCount is the largest count any module instance in the domain expands
// to. A domain where every instance expands to zero or one is distinct
// vacuously, and is answered before the static-scope pre-filter for the
// same reason it always was: with no pair of instances to compare, what the
// expression reads cannot make two of them collide.
func (d countIndexDomain) maxCount() int {
	max := 0
	for _, inst := range d.insts {
		if inst.n > max {
			max = inst.n
		}
	}
	return max
}

// verdict renders expr at every index in one module instance's own range
// and reports whether the values are all distinct, collide, or could not be
// seen. A range of zero or one is distinct vacuously - the whole-domain
// check above has already established that some OTHER instance has a pair
// to compare, but this one does not.
func (m countIndexModuleInstance) verdict(ctx context.Context, expr hclsyntax.Expression, ident configs.StaticIdentifier) countIndexVerdict {
	if m.n <= 1 {
		return countIndexDistinct
	}

	rendered := make([]cty.Value, 0, m.n)
	for i := 0; i < m.n; i++ {
		eval := m.eval.WithRepetitionData(instances.RepetitionData{
			CountIndex: cty.NumberIntVal(int64(i)),
		})
		// EvaluateStructural for the same reason countIndexDomainFor uses it
		// for the count expression itself: an attribute that reads one KEY
		// of var.x[count.index] (e.g. "from_port") does not need every OTHER
		// key of that same element to be static too. See its own doc and
		// GitHub issue #304.
		val, diags := eval.EvaluateStructural(ctx, expr, ident)
		if diags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.ContainsMarked() {
			return countIndexUnprovable
		}
		rendered = append(rendered, val)
	}

	// One type across the whole range, checked before equality rather than
	// relying on it. Two values of different types are never RawEquals even
	// when they render to the same marker string - cty.StringVal("1") and
	// cty.NumberIntVal(1) both become "1" once an identity component is
	// built from them - so a mixed-type range is a case where structural
	// inequality would be the wrong evidence, and is refused instead.
	for _, val := range rendered[1:] {
		if !val.Type().Equals(rendered[0].Type()) {
			return countIndexUnprovable
		}
	}

	for i := range rendered {
		for j := i + 1; j < len(rendered); j++ {
			if rendered[i].RawEquals(rendered[j]) {
				return countIndexCollides
			}
		}
	}
	return countIndexDistinct
}

// countIndexStaticallyScoped reports whether every reference expr makes
// names a root in [countIndexStaticRoots]. It is a pre-filter, not the
// safety check: an expression that passes it can still fail to render, and
// [countIndexDomain.verdict] treats that as a refusal too. Its job
// is to keep a reference to a managed resource, a data source, or a module
// output from ever reaching the evaluator, so that the answer for those
// shapes is decided by this package rather than by whatever a static
// evaluation of them happens to return.
func countIndexStaticallyScoped(expr hcl.Expression) bool {
	for _, trav := range expr.Variables() {
		if !countIndexStaticRoots[trav.RootName()] {
			return false
		}
	}
	return true
}

// countIndexDetail is the prose one refused count.index reference carries.
// It is written from the verdict rather than from a single fixed sentence,
// because the two verdicts ask the reader for different things and the
// older single sentence - "the lexical index of a count instance is not
// stable across scale-up, scale-down, or reordering" - is now the reason
// for neither. A count.index that renders distinct values IS admitted, so
// telling an operator their index is unstable when the actual finding is a
// duplicated value would send them to rewrite the wrong thing.
func countIndexDetail(addr string, verdict countIndexVerdict) string {
	const forEach = "Either make the value differ for every instance, or replace count with " +
		"for_each keyed by a stable identifier, which is distinct by construction"

	if verdict == countIndexCollides {
		return fmt.Sprintf(
			"%s builds an identity-bearing argument from count.index, and two of its instances "+
				"render the SAME value for it. Under live resource markers a resource is found by its "+
				"identity rather than by a state-file entry, so two instances sharing one value means "+
				"two configuration addresses claiming one cloud object: the second marker overwrites "+
				"the first, and live-mv, drift and destroy can all then act on the wrong resource. This "+
				"is not a refusal for want of proof - the values were computed, at the count this "+
				"configuration actually declares, and they collide (live/LIMITATIONS.md, "+
				`"count-index-in-tag"). %s`,
			addr, forEach)
	}

	return fmt.Sprintf(
		"%s builds an identity-bearing argument from count.index in a shape whose value cannot be "+
			"computed here, so it cannot be shown that the instances get different values - and two "+
			"instances sharing a value would mean two configuration addresses claiming one cloud "+
			"object. count.index itself is fine: rendered injectively - a template, an offset, a "+
			"lookup into a collection whose entries differ - it is admitted. What blocks this one is "+
			"that something it reads is not knowable from configuration alone: an input variable with "+
			"no value, or a reference to a managed resource or data source, whose contents only exist "+
			"after an apply. Supplying the variable may be enough; indexing another resource's "+
			`instances never is (live/LIMITATIONS.md, "count-index-in-tag"). %s`,
		addr, forEach)
}
