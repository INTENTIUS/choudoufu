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
func moduleOutputLookup(ctx context.Context, cfg *configs.Config, module addrs.Module, lookup func(addrs.Module) configs.StaticDataLookup, materialize bool, recordManagedRefusal func(*hcl.Diagnostic)) configs.StaticModuleOutputLookup {
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

		eval := liveModuleEvaluator(ctx, cfg, childPath, lookup, materialize, recordManagedRefusal)
		if eval == nil {
			return cty.NilVal, false
		}

		attrs := make(map[string]cty.Value, len(child.Module.Outputs))
		for name, out := range child.Module.Outputs {
			if out.Sensitive || out.Ephemeral {
				return cty.NilVal, false
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
				// A DynamicVal placeholder here, not a whole-call refusal:
				// this doc comment's own words above ("refuses only that
				// call") describe the intent this line now actually
				// carries out. module.eks.cluster_id and
				// module.eks.workers_asg_arns are two entirely different
				// attributes of the same returned object; a caller naming
				// cluster_id never touches workers_asg_arns's own value at
				// all, so workers_asg_arns being unanswerable today (it
				// needs a live attribute of an autoscaling group marker
				// discovery has not swept yet) must not be why cluster_id
				// itself - answerable on its own - refuses too. Before this
				// line, a single unanswerable output among a module's whole
				// set (terraform-aws-eks's own module ships 27) poisoned
				// every other output's own, independently answerable value,
				// non-deterministically depending on Go's own map
				// iteration order over child.Module.Outputs deciding which
				// output's failure was hit first. A whole-object USE still
				// refuses correctly: [cty.ObjectVal]'s attribute carries no
				// mark of its own, but see unprojectedAttr's identical
				// reasoning in managedproj.go - this file's own
				// [staticEvalExprRefused] call three lines below already
				// treats an object containing any DynamicVal as not wholly
				// known, so jsonencode(module.eks) or a splat over this
				// object still refuses exactly as before; only naming ONE
				// answerable attribute changes.
				attrs[name] = cty.DynamicVal
				continue
			}
			if val.IsNull() || val.ContainsMarked() {
				// Same reasoning as the refusal above: a null or marked
				// SIBLING output must not block cluster_id's own value.
				// cty.DynamicVal carries no mark itself, so nothing here
				// can leak a mark forward - the mark simply never crosses,
				// which is the safe direction (see HANDOFF's "never
				// Unmark" rule: this substitutes an unknown, never the
				// marked value itself).
				attrs[name] = cty.DynamicVal
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
			// with.
			if !materialize {
				attrs[name] = cty.DynamicVal
				continue
			}
			if !val.IsWhollyKnown() {
				// Same reasoning again: an unknown SIBLING output's value
				// - one this read pass could not fully materialize - must
				// not cost every OTHER, already-known output its own
				// value.
				attrs[name] = cty.DynamicVal
				continue
			}
			attrs[name] = val
		}
		return cty.ObjectVal(attrs), true
	}
}
