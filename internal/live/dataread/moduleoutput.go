// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// moduleOutputLookup returns a [configs.StaticModuleOutputLookup] scoped to
// module: a closure that, asked about module.<call>, evaluates every output
// CALL's own module declares - strictly, through the exact same live
// evaluator [liveModuleEvaluator] already builds for that module's own data
// sources and managed-resource projections - and assembles them into one
// object, one attribute per output, the shape
// [configs.StaticModuleOutputLookup] documents.
//
// This is the seam a data source's own argument (or, since #313, a provider
// block's) needs to cross a module boundary: `name = module.eks.cluster_id`
// names an output this phase has never had a way to answer, because
// [staticScopeData.StaticValidateReferences] refuses a module-call reference
// in a dedicated case that neither [configs.StaticDataLookup] nor this
// package's managed-resource projection ever reaches. Wired into
// [liveModuleEvaluator] via [configs.StaticEvaluator.WithModuleOutputResults]
// below, so every evaluator this phase builds - offline classification and
// the real read alike - gets it for free.
//
// Deliberately narrower than [identity]'s own module-output resolution
// (moduleoutputvalue.go), which rebuilds one constructor LEAF for a
// module-call argument: this answers a WHOLE call's outputs at once, built
// from primitives this package already owns ([liveModuleEvaluator],
// [staticEvalExpr]) rather than from identity's resolver-tied state, so
// this phase can call it without importing anything identity's own
// evaluation depends on.
//
// # What refuses rather than substitutes
//
// A sensitive or ephemeral output refuses the whole call outright - never
// answers with everything else and a hole for that one name - because a
// caller on this seam (a provider's own configuration, a data source's own
// argument) has no way to keep the mark from flowing into a value this
// package may hand to a live provider process or store as a read result.
// An output whose own expression does not evaluate statically (an unknown,
// a managed-resource reference [managedProjector] cannot cover, a nested
// module boundary this same function cannot cross) refuses only that call:
// [configs.staticScopeData.StaticValidateReferences]'s existing "only a
// covered call passes" discipline then keeps the reference refusing exactly
// as it did before this lookup existed.
func moduleOutputLookup(ctx context.Context, cfg *configs.Config, module addrs.Module, lookup func(addrs.Module) configs.StaticDataLookup, materialize bool, recordManagedRefusal func(*hcl.Diagnostic), wanted map[string]moduleOutputWant) configs.StaticModuleOutputLookup {
	return func(call addrs.ModuleCall) (cty.Value, bool) {
		childPath := make(addrs.Module, 0, len(module)+1)
		childPath = append(childPath, module...)
		childPath = append(childPath, call.Name)

		child := cfg.Descendent(childPath)
		if child == nil || child.Module == nil {
			return cty.NilVal, false
		}
		if len(child.Module.Outputs) == 0 {
			return cty.EmptyObjectVal, true
		}

		// want.only, when true, is issue #391's own fourth finding closed:
		// this call is answering a reference whose OWN traversal names one
		// or more SPECIFIC outputs of this call (module.<call>.<name>,
		// never a whole-object use) - see [moduleOutputWantsFor], which
		// only ever narrows to this from a traversal shape it can spell
		// back out exactly. Every OTHER output declared here is skipped
		// entirely: not merely omitted from attrs the way an unanswered
		// one already was (this file's earlier fix), but never evaluated
		// at all, so whatever DATA SOURCE or managed-resource dependency a
		// completely unrelated sibling output's own expression reaches
		// never reaches lookup's own `record` side effect either - the
		// dependency-tracking half of the same "one call answers 27
		// outputs, only one of them asked for" bug this file's value-level
		// fix above already closed for VALUES. A caller that names no
		// specific output (jsonencode(module.child), a splat, an
		// unrecognized traversal shape) gets want.only false, the
		// unchanged, fully-conservative "evaluate and record every
		// output" behavior this function has always had - correct there,
		// because a whole-object use genuinely does depend on every
		// output.
		want := wanted[call.Name]

		// The recursive call below answers THIS child's own module-output
		// crossings, if it has any (module.child.grandchild.output) - a
		// deeper level than [moduleOutputWantsFor] scoped its answer to,
		// so it is handed nil, the same fully-conservative "evaluate and
		// record every output" default every OTHER liveModuleEvaluator
		// call site already gets. Narrowing that deeper level too is a
		// straightforward extension of the same mechanism, not attempted
		// here: nothing in this package's corpus coverage reaches a
		// second module-output hop.
		eval := liveModuleEvaluator(ctx, cfg, childPath, lookup, materialize, recordManagedRefusal, nil)
		if eval == nil {
			return cty.NilVal, false
		}

		attrs := make(map[string]cty.Value, len(child.Module.Outputs))
		// unanswered tracks whether any output was left out of attrs below,
		// so a WHOLE-OBJECT use (jsonencode(module.child), a splat) still
		// refuses correctly even though a NAMED attribute access to a
		// different, answerable output now succeeds. See unanswered's own
		// assignment for why omitting the attribute - not substituting
		// cty.DynamicVal for it - is what a single output's own failure
		// needs.
		unanswered := false
		for name, out := range child.Module.Outputs {
			if out.Sensitive || out.Ephemeral {
				// Unconditional, even for an output want.only would
				// otherwise skip below: this is the existing whole-call
				// refusal [moduleOutputLookup]'s own doc comment
				// ("What refuses rather than substitutes") describes, and
				// narrowing IT to the wanted output(s) too is a separate
				// question from the dependency-tracking one this
				// function's want.only branch below closes - left as it
				// was.
				return cty.NilVal, false
			}
			if want.only && !want.names[name] {
				// Not the output this call's referencing expression
				// names, and this call's own traversal named at least one
				// SPECIFIC output (want.only), so no whole-object use is
				// in play this round: skip evaluating it entirely, the
				// same as if it did not exist for this call's own
				// purposes. Its own expression's dependencies - the
				// entire point - never reach lookup's `record` side
				// effect, so an unrelated sibling's own refusal can never
				// attribute itself to the output actually being read.
				// Never counts toward unanswered/unprojectedAttr below:
				// those exist for a WHOLE-OBJECT use, which this is not.
				continue
			}
			val, _, refused, ok := staticEvalExprRefused(ctx, childPath, "output."+name, "value", out.Expr, eval)
			if !ok {
				// A managed-resource reference this output's own expression
				// reaches, and that neither the block's own arguments nor
				// [Options.LiveManagedResults] covers, is recorded the same
				// way [analyzer.classify] records one at its own layer -
				// see [Analysis.ManagedRefusals] - so a caller one layer
				// out (issue #313's own fixpoint) learns what a live read
				// would need to name, exactly as it would if the reference
				// had sat directly in the data source's own argument
				// instead of one module-output hop away.
				if recordManagedRefusal != nil && refused != nil {
					if ref, isRef := refused.Extra.(configs.RefusedReference); isRef && ref.Category == configs.CategoryManagedResource {
						recordManagedRefusal(refused)
					}
				}
				unanswered = true
				continue
			}
			if val.IsNull() || val.ContainsMarked() {
				unanswered = true
				continue
			}
			// materialize mirrors managedProjector.argument's own rule
			// exactly: offline classification needs COVERAGE, not a value,
			// so an output whose expression is answerable at all - even
			// through a nested data-source or managed-resource reference
			// this run has not read yet, itself covered only as
			// cty.DynamicVal at this stage - counts as covered. The read
			// phase (materialize true) needs the real thing, and refuses
			// here exactly as [managedProjector.argument] refuses an
			// unknown or impure managed argument: this seam must never
			// hand a data source, or a provider block, an unknown to read
			// with. This branch is untouched by this file's own "refuses
			// only that call" fix below: an output covered-but-not-yet-
			// read is not a failure at all in this mode, and cty.DynamicVal
			// here is what makes that count as coverage, exactly as it
			// always has.
			if !materialize {
				attrs[name] = cty.DynamicVal
				continue
			}
			if !val.IsWhollyKnown() {
				unanswered = true
				continue
			}
			attrs[name] = val
		}
		if unanswered {
			// A caller naming ONE answerable output (module.eks.cluster_id)
			// must not refuse over a completely different, unrelated
			// output failing (module.eks.workers_asg_arns, which needs a
			// live attribute marker discovery has not swept yet) - that
			// whole-lookup abort is the bug this fix closes; terraform-aws-
			// eks's own eks module ships 27 outputs, and which one failed
			// first used to decide the WHOLE object's fate,
			// non-deterministically, by Go's own map iteration order over
			// child.Module.Outputs.
			//
			// The failing output's own key is left OUT of attrs entirely -
			// not set to cty.DynamicVal - because materialize=false's
			// "covered but not yet known" meaning (the branch just above)
			// is a DIFFERENT claim from this one: "not yet known" is
			// eligible, on purpose, because the read phase will supply a
			// value later, while a genuine !ok/null/marked/unknown failure
			// here may NEVER resolve (this baseline call, Options{}, has no
			// LiveManagedResults at all and never will for a wall that is
			// structurally real). Two claims sharing one cty.Value shape
			// would make analyze.go's own coverage-mode caller treat
			// "genuinely will never work" as "will work eventually" -
			// exactly the false-reassurance shape this package's own doc.go
			// forbids. Omitting the key instead means a caller naming the
			// failing output DIRECTLY (module.child.workers_asg_arns) gets
			// HCL's own "this object does not have an attribute named…"
			// diagnostic - !ok, refusing exactly as it always did - while a
			// caller naming only a DIFFERENT, successful attribute never
			// sees it.
			//
			// A WHOLE-OBJECT use (jsonencode(module.child), a splat over
			// it) must still refuse, and unprojectedAttr - the identical
			// unspellable-attribute-name device managedproj.go already
			// uses for the same "one attribute access succeeds, whole-
			// object doesn't" shape - is what makes that so: it's an
			// unknown that IsWhollyKnown() sees on the object, but that no
			// traversal beginning with a "." can ever spell.
			//
			// Skipped entirely when want.only: this round evaluated only
			// the SPECIFIC output(s) the caller's own traversal names, not
			// every output the call declares, so "unanswered" here can
			// only ever mean one of THOSE named outputs itself failed -
			// already correctly refused by the omitted key above, via
			// HCL's own "no such attribute" - never a stand-in for "the
			// whole object is incomplete", which is the one claim
			// unprojectedAttr exists to make and this round never asked.
			if !want.only {
				attrs[unprojectedAttr] = cty.DynamicVal
			}
		}
		return cty.ObjectVal(attrs), true
	}
}

// moduleOutputWant is one module call's own scoping answer, computed once
// per referencing expression by [moduleOutputWantsFor] from that
// expression's own static traversals and handed to [moduleOutputLookup] as
// wanted[call.Name] - never derived inside moduleOutputLookup itself, which
// has no visibility into what the ORIGINAL caller several evaluation layers
// out actually asked for (see this file's own header comment on why not:
// HCL resolves module.eks as a whole object first, .cluster_id as a
// separate step after).
type moduleOutputWant struct {
	// only reports that every traversal this call's own referencing
	// expression contains, if it names this call at all, spelled a
	// SPECIFIC output by name (module.<call>.<name>, never a bare
	// module.<call> or a shape this scan does not recognize). false is
	// the safe, fully-conservative default: evaluate and record every
	// output, unchanged from this function's behavior before want existed.
	only bool

	// names is the set of specifically-named outputs when only is true.
	// Never consulted when only is false.
	names map[string]bool
}

// moduleOutputWantsFor computes, from one expression's own static
// traversals (already filtered for self/dynamic-iterator roots by the
// caller - see [analyzer.evalRecorded]), which module call(s) it reaches
// and, for each, whether every reference to it names one or more SPECIFIC
// outputs rather than the call as a whole.
//
// Deliberately conservative wherever a traversal's own shape is not one
// this function can spell back out exactly - fewer than two steps, a root
// other than "module", a call step that is not a plain name, or anything
// after the call step other than zero or more index steps (an expanded
// call: module.child[0].output, module.child["key"].output) followed by
// either nothing (a whole-object use) or one plain attribute step (a named
// output, with any further steps ignored - they index INTO that output's
// own value, not across another module boundary). Any traversal reaching a
// call this function cannot fully parse widens that call's own want to
// "every output", the untouched behavior [moduleOutputLookup] has always
// had - never narrows past what this function can prove, because a wrong
// narrowing would drop a real dependency silently rather than refuse.
func moduleOutputWantsFor(travs []hcl.Traversal) map[string]moduleOutputWant {
	if len(travs) == 0 {
		return nil
	}
	wanted := make(map[string]moduleOutputWant)
	for _, trav := range travs {
		if len(trav) < 2 || trav.RootName() != "module" {
			continue
		}
		callStep, ok := trav[1].(hcl.TraverseAttr)
		if !ok {
			continue // not an ordinary module.<name>... traversal at all
		}
		callName := callStep.Name
		if w, seen := wanted[callName]; seen && !w.only {
			continue // already conservative for this call; nothing narrows it back
		}

		rest := trav[2:]
		for len(rest) > 0 {
			if _, isIndex := rest[0].(hcl.TraverseIndex); !isIndex {
				break
			}
			rest = rest[1:]
		}

		if len(rest) == 0 {
			wanted[callName] = moduleOutputWant{only: false}
			continue
		}
		attrStep, ok := rest[0].(hcl.TraverseAttr)
		if !ok {
			wanted[callName] = moduleOutputWant{only: false}
			continue
		}

		w := wanted[callName]
		w.only = true
		if w.names == nil {
			w.names = map[string]bool{}
		}
		w.names[attrStep.Name] = true
		wanted[callName] = w
	}
	return wanted
}
