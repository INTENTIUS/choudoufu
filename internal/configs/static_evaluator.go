// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"context"
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/zclconf/go-cty/cty"
)

// StaticIdentifier holds a Referenceable item and where it was declared
type StaticIdentifier struct {
	Module    addrs.Module
	Subject   string
	DeclRange hcl.Range
}

func (ref StaticIdentifier) String() string {
	val := ref.Subject
	if len(ref.Module) != 0 {
		val = ref.Module.String() + ":" + val
	}
	return val
}

type StaticModuleVariables func(v *Variable) (cty.Value, hcl.Diagnostics)

// StaticModuleCall contains the information required to call a given module
type StaticModuleCall struct {
	addr      addrs.Module
	declRange hcl.Range
	vars      StaticModuleVariables
	rootPath  string
	workspace string
}

func NewStaticModuleCall(addr addrs.Module, declRange hcl.Range, vars StaticModuleVariables, rootPath string, workspace string) StaticModuleCall {
	return StaticModuleCall{
		addr:      addr,
		declRange: declRange,
		vars:      vars,
		rootPath:  rootPath,
		workspace: workspace,
	}
}

func (s StaticModuleCall) Variables() StaticModuleVariables {
	return s.vars
}

func (s StaticModuleCall) WithVariables(vars StaticModuleVariables) StaticModuleCall {
	return StaticModuleCall{
		addr:      s.addr,
		declRange: s.declRange,
		vars:      vars,
		rootPath:  s.rootPath,
		workspace: s.workspace,
	}
}

// only used in testing
func RootModuleCallForTesting() StaticModuleCall {
	return NewStaticModuleCall(addrs.RootModule, hcl.Range{}, func(_ *Variable) (cty.Value, hcl.Diagnostics) {
		panic("Variables have not been configured for this test!")
	}, "<testing>", "")
}

// A static evaluator contains the information required to build a EvalContext
// which only understands "static" (non-state) data. Internally, it relies
// on staticData
type StaticEvaluator struct {
	call StaticModuleCall
	cfg  *Module

	// pureOnly makes every scope this evaluator builds refuse to produce a
	// value from an impure function. See [StaticEvaluator.Pure].
	pureOnly bool

	// dataLookup, when non-nil, answers data-resource references from values
	// a caller read before evaluation began. See
	// [StaticEvaluator.WithDataResults].
	dataLookup StaticDataLookup

	// repetition holds the each.key/each.value/count.index values the
	// single resource instance whose arguments are being evaluated actually
	// has. See [StaticEvaluator.WithRepetitionData].
	repetition instances.RepetitionData
}

// StaticDataLookup answers a data-resource reference with a value read ahead
// of static evaluation, or reports that it has none. The address is the
// module-relative resource ([addrs.Resource] with
// [addrs.DataResourceMode]); the value is the whole resource's: the single
// instance's object for an unexpanded block, a tuple for count, an object
// keyed by string for for_each, matching how the plan-time evaluator shapes
// a resource reference so that instance indexing works unchanged.
type StaticDataLookup func(addr addrs.Resource) (cty.Value, bool)

// WithDataResults returns a copy of the evaluator whose scopes answer
// data-resource references through the given lookup instead of refusing them
// as dynamic values.
//
// The base evaluator refuses every resource reference, because static
// evaluation runs before anything has been read and inventing a value here
// would be a guess a later phase treats as fact. A caller that HAS performed
// reads - the pre-resolution data-read phase - hands the results in through
// this seam, and only references the lookup actually covers are permitted;
// everything else keeps refusing exactly as before. Managed-mode references
// are never answered this way: there is nothing a pre-plan read could
// honestly say about an object the plan may be about to change.
func (s *StaticEvaluator) WithDataResults(lookup StaticDataLookup) *StaticEvaluator {
	if s == nil || lookup == nil {
		return s
	}
	dup := *s
	dup.dataLookup = lookup
	return &dup
}

// WithRepetitionData returns a copy of the evaluator whose scopes answer
// each.key, each.value and count.index from rd, for every reference this
// evaluator resolves at any depth - not only the expression a caller hands
// straight to [StaticEvaluator.Evaluate], but also any local value or
// module-call variable that expression reaches through [StaticEvaluator]'s
// own reference resolution (a local's own defining expression evaluates
// through a freshly built child scope, and that scope carries the same
// [*StaticEvaluator], so it sees the same rd).
//
// rd is a value bag, not a source of truth: whatever the caller passes is
// what a reference gets back, with no independent check that it matches the
// resource instance actually being evaluated. The caller - identity
// resolution's own per-instance expansion - is the party responsible for
// that, because only it knows which instance's arguments it is asking this
// evaluator to answer. A field of rd left at cty.NilVal (the zero value)
// means "not known for this instance", which behaves exactly as an
// evaluator built with no repetition data at all: an unset each.value under
// a for_each whose values are not statically known must still refuse a
// reference to it, not answer with something invented.
//
// The base evaluator refuses every each/count reference (see
// [staticScopeData.GetCountAttr] and [staticScopeData.GetForEachAttr]),
// because plain static evaluation - module source, backend configuration,
// module call variables - has no notion of a resource instance at all. A
// caller resolving one instance's identity is the first caller of this
// package that does.
func (s *StaticEvaluator) WithRepetitionData(rd instances.RepetitionData) *StaticEvaluator {
	if s == nil {
		return s
	}
	dup := *s
	dup.repetition = rd
	return &dup
}

// repetitionAttr answers a count.<name> or each.<name> reference from the
// evaluator's repetition data, reporting whether it is covered. It is the
// single place both [staticScopeData.StaticValidateReferences] (whether to
// refuse) and [staticScopeData.GetCountAttr]/[staticScopeData.GetForEachAttr]
// (what to return) consult, so the two can never disagree about which
// references are answerable.
func (s *StaticEvaluator) repetitionAttr(subject addrs.Referenceable) (cty.Value, bool) {
	if s == nil {
		return cty.NilVal, false
	}
	switch a := subject.(type) {
	case addrs.CountAttr:
		if a.Name == "index" && s.repetition.CountIndex != cty.NilVal {
			return s.repetition.CountIndex, true
		}
	case addrs.ForEachAttr:
		switch a.Name {
		case "key":
			if s.repetition.EachKey != cty.NilVal {
				return s.repetition.EachKey, true
			}
		case "value":
			if s.repetition.EachValue != cty.NilVal {
				return s.repetition.EachValue, true
			}
		}
	}
	return cty.NilVal, false
}

// Creates a static evaluator based from the given module and module call
func NewStaticEvaluator(mod *Module, call StaticModuleCall) *StaticEvaluator {
	return &StaticEvaluator{
		call: call,
		cfg:  mod,
	}
}

func (s *StaticEvaluator) scope(ident StaticIdentifier) *lang.Scope {
	return newStaticScope(s, ident)
}

func (s StaticEvaluator) Evaluate(ctx context.Context, expr hcl.Expression, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	val, diags := s.scope(ident).EvalExpr(ctx, expr, cty.DynamicPseudoType)
	return val, diags.ToHCL()
}

func (s StaticEvaluator) DecodeExpression(ctx context.Context, expr hcl.Expression, ident StaticIdentifier, val any) hcl.Diagnostics {
	srcVal, diags := s.Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		return diags
	}

	if marks.Contains(srcVal, marks.Sensitive) {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Sensitive value not allowed",
			Detail:   fmt.Sprintf("Sensitive values, or values derived from sensitive values, cannot be used as %s.", ident.String()),
			Subject:  expr.Range().Ptr(),
		})
	}
	if marks.Contains(srcVal, marks.Ephemeral) {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Ephemeral value not allowed",
			Detail:   fmt.Sprintf("Ephemeral values, or values derived from ephemeral values, cannot be used as %s.", ident.String()),
			Subject:  expr.Range().Ptr(),
		})
	}

	return diags.Extend(gohcl.DecodeValue(srcVal, expr.StartRange(), expr.Range(), val))
}

func (s StaticEvaluator) DecodeBlock(ctx context.Context, body hcl.Body, spec hcldec.Spec, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	refs, refsDiags := lang.References(addrs.ParseRef, hcldec.Variables(body, spec))
	diags = append(diags, refsDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	hclCtx, ctxDiags := s.scope(ident).EvalContext(ctx, refs)
	diags = append(diags, ctxDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	val, valDiags := hcldec.Decode(body, spec, hclCtx)
	diags = append(diags, valDiags...)
	return val, diags
}

func (s StaticEvaluator) EvalContext(ctx context.Context, ident StaticIdentifier, refs []*addrs.Reference) (*hcl.EvalContext, hcl.Diagnostics) {
	return s.EvalContextWithParent(ctx, nil, ident, refs)
}

func (s StaticEvaluator) EvalContextWithParent(ctx context.Context, parent *hcl.EvalContext, ident StaticIdentifier, refs []*addrs.Reference) (*hcl.EvalContext, hcl.Diagnostics) {
	evalCtx, diags := s.scope(ident).EvalContextWithParent(ctx, parent, refs)
	return evalCtx, diags.ToHCL()
}
