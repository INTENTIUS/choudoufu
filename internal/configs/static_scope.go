// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/didyoumean"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// newStaticScope creates a lang.Scope that's backed by the static view of the module represented by the StaticEvaluator
func newStaticScope(eval *StaticEvaluator, stack0 StaticIdentifier, stack ...StaticIdentifier) *lang.Scope {
	return &lang.Scope{
		Data:        staticScopeData{eval, append([]StaticIdentifier{stack0}, stack...)},
		ParseRef:    addrs.ParseRef,
		BaseDir:     eval.baseDir(),
		PureOnly:    eval.pureOnly,
		ConsoleMode: false,
	}
}

// Pure returns a copy of the evaluator whose scopes evaluate uuid(),
// timestamp() and bcrypt() to unknown values instead of calling them, the
// same way the plan walk does outside of apply (see lang.Scope.PureOnly and
// internal/tofu/evaluate.go).
//
// The default is impure because the original callers of the static evaluator
// - module source, backend configuration, module call variables - all want
// whatever the function returns and consume it once, immediately.
//
// A caller that is deriving a stable identity from configuration wants the
// opposite. An impure function evaluated here produces a value that is real,
// known, and different on the next run: for stateless mode's identity
// resolution that means a fabricated import ID, a plan that proposes to
// create something that already exists, and a leaked resource per run, with
// no diagnostic anywhere because nothing about the value looks wrong. Such a
// caller asks for a pure evaluator, and gets an unknown value it can refuse
// on rather than a plausible one it cannot tell from a real answer.
func (s *StaticEvaluator) Pure() *StaticEvaluator {
	if s == nil || s.pureOnly {
		return s
	}
	dup := *s
	dup.pureOnly = true
	return &dup
}

// This structure represents the data required to evaluate a specific identifier reference (top of the stack)
// It is used by lang.Scope to link the given StaticEvaluator data to addrs.References in the current scope.
type staticScopeData struct {
	eval  *StaticEvaluator
	stack []StaticIdentifier
}

// staticScopeData must implement lang.Data
var _ lang.Data = (*staticScopeData)(nil)

// Creates a nested scope to evaluate nested references
func (s staticScopeData) scope(ident StaticIdentifier) (*lang.Scope, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	for _, frame := range s.stack {
		if frame.String() == ident.String() {
			return nil, diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Circular reference",
				Detail:   fmt.Sprintf("%s is self referential", ident.String()), // TODO use stack in error message
				Subject:  ident.DeclRange.Ptr(),
			})
		}
	}
	return newStaticScope(s.eval, s.stack[0], append(s.stack[1:], ident)...), diags
}

// If an error occurs when resolving a dependent value, we need to add additional context to the diagnostics
func (s staticScopeData) enhanceDiagnostics(ident StaticIdentifier, diags tfdiags.Diagnostics) tfdiags.Diagnostics {
	if diags.HasErrors() {
		top := s.stack[len(s.stack)-1]
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Unable to compute static value",
			Detail:   fmt.Sprintf("%s depends on %s which is not available", top, ident.String()),
			Subject:  top.DeclRange.Ptr(),
		})
	}
	return diags
}

// ReferenceCategory classifies the kind of object a reference site named,
// derived structurally from ref.Subject's Go type at the point
// StaticValidateReferences refused it - never by parsing a diagnostic's
// rendered text. It rides in the diagnostic's Extra field (the same
// mechanism tfdiags.ExtraInfo already exposes for other diagnostic
// metadata), so a caller can recover it with
// tfdiags.ExtraInfo[ReferenceCategory](diag) instead of re-deriving it.
//
// #178's scoping pass found the split between a same-stack data source and
// a cross-stack one load-bearing for its design question: a same-stack
// data source is an ordering problem this repo could plausibly solve with
// a pre-resolution read phase against the live provider, while a
// cross-stack one (terraform_remote_state, tfe_outputs) depends on a
// second state backend this fork does not open, and needs its own design
// call regardless of what the first gets.
type ReferenceCategory string

const (
	CategoryManagedResource  ReferenceCategory = "managed_resource"
	CategoryDataSource       ReferenceCategory = "data_source"
	CategoryTfeOutputs       ReferenceCategory = "tfe_outputs"
	CategoryRemoteState      ReferenceCategory = "remote_state"
	CategoryModuleOutput     ReferenceCategory = "module_output"
	CategoryProviderFunction ReferenceCategory = "provider_function"
	CategoryOther            ReferenceCategory = "other"
)

// tfeOutputsType and remoteStateType are the two cross-stack data source
// types: mechanically both are provider data sources, but each carries its
// own auth surface and failure modes and lands as its own stage (issue
// #179). This is the one place a type name IS the category definition -
// every other rule in this file stays structural.
const (
	tfeOutputsType  = "tfe_outputs"
	remoteStateType = "terraform_remote_state"
)

// crossStackDataSources are the data source types that read another
// stack's recorded outputs rather than a live cloud resource.
var crossStackDataSources = map[string]bool{
	remoteStateType: true,
	tfeOutputsType:  true,
}

// RefusedReference is the structured account of one reference
// [staticScopeData.StaticValidateReferences] refused, carried in the
// diagnostic's Extra field. It wraps the [ReferenceCategory] that has ridden
// there since #178 - tfdiags.ExtraInfo[ReferenceCategory] still finds it,
// through the unwrap chain - and adds the two facts a consumer needs to act
// on the refusal rather than merely count it: which object was referenced
// and in which module. The pre-resolution data-read phase
// (internal/live/dataread) is the consumer: it derives which data sources
// identity resolution demands from exactly these refusals.
type RefusedReference struct {
	// Category classifies the referenced object. See [ReferenceCategory].
	Category ReferenceCategory

	// Subject is the referenced object itself, module-relative, exactly as
	// the reference parser produced it.
	Subject addrs.Referenceable

	// Module is the module the referencing expression belongs to: the
	// evaluating module's call path, with no instance keys, because a static
	// evaluator is shared by every instance of its module.
	Module addrs.Module

	// NeededBy names the static identifier whose evaluation needed the
	// refused reference - the top of the evaluation stack, rendered the way
	// the diagnostic's own text renders it.
	NeededBy string
}

// UnwrapDiagnosticExtra keeps tfdiags.ExtraInfo[ReferenceCategory] working:
// the category used to BE the Extra value, and every consumer of it reads
// through the standard unwrap chain.
func (r RefusedReference) UnwrapDiagnosticExtra() interface{} { return r.Category }

// IsCrossStackDataSource reports whether typeName is one of the data source
// types that read another stack's recorded outputs rather than a live cloud
// resource - the union of [IsTfeOutputs] and [IsRemoteState]. Exported
// because the data-read phase draws its coverage line exactly here: a
// same-stack data source is an ordering problem the phase solves, a
// cross-stack one carries its own auth surface and failure modes and stays
// refused until its own stage ships (issue #179).
func IsCrossStackDataSource(typeName string) bool {
	return crossStackDataSources[typeName]
}

// IsTfeOutputs reports whether typeName is the tfe provider's cross-stack
// output reader. Stage 2 of #179 gives it its own read pipeline; see
// [IsRemoteState] for the flavor that stays refused until stage 3.
func IsTfeOutputs(typeName string) bool {
	return typeName == tfeOutputsType
}

// IsRemoteState reports whether typeName is the builtin
// terraform_remote_state data source. It stays refused byte-for-byte until
// #179's stage 3 gives it its own read pipeline.
func IsRemoteState(typeName string) bool {
	return typeName == remoteStateType
}

// categorizeReference derives subject's [ReferenceCategory] from its Go
// type and, for a data resource, its type name - structural signals
// available at the raise site, with nothing read from rendered text.
func categorizeReference(subject addrs.Referenceable) ReferenceCategory {
	switch s := subject.(type) {
	case addrs.Resource:
		return categorizeResourceMode(s.Mode, s.Type)
	case addrs.ResourceInstance:
		return categorizeResourceMode(s.Resource.Mode, s.Resource.Type)
	case addrs.ModuleCallInstanceOutput:
		return CategoryModuleOutput
	case addrs.ModuleCall, addrs.ModuleCallInstance:
		// module.foo (every output, unkeyed) and module.foo[0] (every
		// output of one instance) name the whole module call rather than
		// one named output, but need the same thing a named output needs:
		// the module evaluated, which static evaluation never does. See
		// the matching case in [staticScopeData.StaticValidateReferences].
		return CategoryModuleOutput
	case addrs.ProviderFunction:
		return CategoryProviderFunction
	default:
		return CategoryOther
	}
}

func categorizeResourceMode(mode addrs.ResourceMode, typeName string) ReferenceCategory {
	switch mode {
	case addrs.ManagedResourceMode:
		return CategoryManagedResource
	case addrs.DataResourceMode:
		switch typeName {
		case tfeOutputsType:
			return CategoryTfeOutputs
		case remoteStateType:
			return CategoryRemoteState
		default:
			return CategoryDataSource
		}
	default:
		return CategoryOther
	}
}

// Early check to only allow references we expect in a static context
func (s staticScopeData) StaticValidateReferences(_ context.Context, refs []*addrs.Reference, _ addrs.Referenceable, _ addrs.Referenceable) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	top := s.stack[len(s.stack)-1]
	refused := func(subject addrs.Referenceable) RefusedReference {
		return RefusedReference{
			Category: categorizeReference(subject),
			Subject:  subject,
			Module:   s.eval.call.addr,
			NeededBy: top.String(),
		}
	}
	for _, ref := range refs {
		switch subject := ref.Subject.(type) {
		case addrs.LocalValue:
			continue
		case addrs.InputVariable:
			continue
		case addrs.PathAttr:
			continue
		case addrs.TerraformAttr:
			continue
		case addrs.ModuleCallInstanceOutput, addrs.ModuleCall, addrs.ModuleCallInstance:
			// The named-output form (module.foo.bar) and the whole-module
			// form (module.foo, or the keyed module.foo[0]) both refuse for
			// the same reason: whatever they name is produced by evaluating
			// the module, which has not happened yet. Splitting them into
			// two diagnostics here would separate a user's "I referenced
			// the whole module instead of one output" mistake from its own
			// twin for no reason a reader could tell was intentional.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Module output not supported in static context",
				Detail:   fmt.Sprintf("Unable to use %s in static context, which is required by %s", subject.String(), top.String()),
				Subject:  ref.SourceRange.ToHCL().Ptr(),
				Extra:    refused(subject),
			})
		case addrs.ProviderFunction:
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Provider function in static context",
				Detail:   fmt.Sprintf("Unable to use %s in static context, which is required by %s", subject.String(), top.String()),
				Subject:  ref.SourceRange.ToHCL().Ptr(),
				Extra:    refused(subject),
			})
		case addrs.CountAttr, addrs.ForEachAttr:
			// count.index, each.key and each.value are dynamic in the sense
			// that plain static evaluation has no notion of a resource
			// instance at all - but a caller resolving one specific
			// instance's identity does, and hands the values in through
			// [StaticEvaluator.WithRepetitionData]. Only the specific
			// attribute that data covers passes; an evaluator with no
			// repetition data, or one missing this particular attribute
			// (each.value under a for_each whose values are not statically
			// known, for instance), refuses exactly as it always has.
			if _, ok := s.eval.repetitionAttr(subject); ok {
				continue
			}
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Dynamic value in static context",
				Detail:   fmt.Sprintf("Unable to use %s in static context, which is required by %s", subject.String(), top.String()),
				Subject:  ref.SourceRange.ToHCL().Ptr(),
				Extra:    refused(subject),
			})
		default:
			// A data-resource reference the evaluator's data lookup covers
			// is not dynamic anymore: a pre-resolution phase read the value
			// from the provider itself, and [staticScopeData.GetResource]
			// answers it below. Only covered references pass - everything
			// the lookup does not carry keeps refusing, so an evaluator
			// with no lookup behaves exactly as it always has.
			if res, ok := dataResourceSubject(subject); ok && s.eval.dataLookup != nil {
				if _, covered := s.eval.dataLookup(res); covered {
					continue
				}
			}
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Dynamic value in static context",
				Detail:   fmt.Sprintf("Unable to use %s in static context, which is required by %s", subject.String(), top.String()),
				Subject:  ref.SourceRange.ToHCL().Ptr(),
				Extra:    refused(subject),
			})
		}
	}
	return diags
}

// dataResourceSubject extracts the containing data-mode resource from a
// reference subject, when it is one.
func dataResourceSubject(subject addrs.Referenceable) (addrs.Resource, bool) {
	switch s := subject.(type) {
	case addrs.Resource:
		if s.Mode == addrs.DataResourceMode {
			return s, true
		}
	case addrs.ResourceInstance:
		if s.Resource.Mode == addrs.DataResourceMode {
			return s.ContainingResource(), true
		}
	}
	return addrs.Resource{}, false
}

// GetCountAttr answers count.index from the evaluator's repetition data,
// when it has one that covers it.
// [staticScopeData.StaticValidateReferences] refuses every count.index
// reference the repetition data does not cover before evaluation starts, so
// the panic below is unreachable through this package's own scopes; it
// stays as the same programming-error backstop the other unavailable
// getters keep.
func (s staticScopeData) GetCountAttr(_ context.Context, addr addrs.CountAttr, _ tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	if val, ok := s.eval.repetitionAttr(addr); ok {
		return val, nil
	}
	panic("Not Available in Static Context")
}

// GetForEachAttr answers each.key or each.value from the evaluator's
// repetition data, when it has one that covers it. See
// [staticScopeData.GetCountAttr] for why the panic below is unreachable
// through this package's own scopes.
func (s staticScopeData) GetForEachAttr(_ context.Context, addr addrs.ForEachAttr, _ tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	if val, ok := s.eval.repetitionAttr(addr); ok {
		return val, nil
	}
	panic("Not Available in Static Context")
}

// GetResource answers a data-mode resource reference from the evaluator's
// data lookup, when it has one that covers the address.
// [staticScopeData.StaticValidateReferences] refuses every resource
// reference the lookup does not cover before evaluation starts, so the
// panic below is unreachable through this package's own scopes; it stays as
// the same programming-error backstop the other unavailable getters keep.
func (s staticScopeData) GetResource(_ context.Context, addr addrs.Resource, _ tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	if addr.Mode == addrs.DataResourceMode && s.eval.dataLookup != nil {
		if val, ok := s.eval.dataLookup(addr); ok {
			return val, nil
		}
	}
	panic("Not Available in Static Context")
}

func (s staticScopeData) GetLocalValue(ctx context.Context, ident addrs.LocalValue, rng tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	local, ok := s.eval.cfg.Locals[ident.Name]
	if !ok {
		return cty.DynamicVal, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Undefined local",
			Detail:   fmt.Sprintf("Undefined local %s", ident.String()),
			Subject:  rng.ToHCL().Ptr(),
		})
	}

	id := StaticIdentifier{
		Module:    s.eval.call.addr,
		Subject:   fmt.Sprintf("local.%s", local.Name),
		DeclRange: local.DeclRange,
	}

	scope, scopeDiags := s.scope(id)
	diags = diags.Append(scopeDiags)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	val, valDiags := scope.EvalExpr(ctx, local.Expr, cty.DynamicPseudoType)
	return val, s.enhanceDiagnostics(id, diags.Append(valDiags))
}

func (s staticScopeData) GetModule(context.Context, addrs.ModuleCall, tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	panic("Not Available in Static Context")
}

func (s staticScopeData) GetPathAttr(_ context.Context, addr addrs.PathAttr, rng tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	// TODO this is copied and trimmed down from tofu/evaluate.go GetPathAttr.  Ideally this should be refactored to a common location.
	var diags tfdiags.Diagnostics
	switch addr.Name {
	case "cwd":
		wd, err := os.Getwd()
		if err != nil {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  `Failed to get working directory`,
				Detail:   fmt.Sprintf(`The value for path.cwd cannot be determined due to a system error: %s`, err),
				Subject:  rng.ToHCL().Ptr(),
			})
			return cty.DynamicVal, diags
		}
		wd, err = filepath.Abs(wd)
		if err != nil {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  `Failed to get working directory`,
				Detail:   fmt.Sprintf(`The value for path.cwd cannot be determined due to a system error: %s`, err),
				Subject:  rng.ToHCL().Ptr(),
			})
			return cty.DynamicVal, diags
		}

		return cty.StringVal(filepath.ToSlash(wd)), diags

	case "module":
		return cty.StringVal(s.eval.modulePath()), diags

	case "root":
		return cty.StringVal(s.eval.rootDir()), diags

	default:
		suggestion := didyoumean.NameSuggestion(addr.Name, []string{"cwd", "module", "root"})
		if suggestion != "" {
			suggestion = fmt.Sprintf(" Did you mean %q?", suggestion)
		}
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  `Invalid "path" attribute`,
			Detail:   fmt.Sprintf(`The "path" object does not have an attribute named %q.%s`, addr.Name, suggestion),
			Subject:  rng.ToHCL().Ptr(),
		})
		return cty.DynamicVal, diags
	}
}

func (s staticScopeData) GetTerraformAttr(_ context.Context, addr addrs.TerraformAttr, rng tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	// TODO this is copied and trimmed down from tofu/evaluate.go GetTerraformAttr.  Ideally this should be refactored to a common location.
	var diags tfdiags.Diagnostics
	switch addr.Name {
	case "applying":
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  `Invalid attribute in static context`,
			Detail:   `The applying attribute can not be used during static evaluation and is only valid in the plan and apply phases of execution`,
			Subject:  rng.ToHCL().Ptr(),
		})
		return cty.DynamicVal, diags
	case "workspace":
		workspaceName := s.eval.call.workspace
		return cty.StringVal(workspaceName), diags

	case "env":
		// Prior to Terraform 0.12 there was an attribute "env", which was
		// an alias name for "workspace". This was deprecated and is now
		// removed.
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  `Invalid "terraform" attribute`,
			Detail:   `The terraform.env attribute was deprecated in v0.10 and removed in v0.12. The "state environment" concept was renamed to "workspace" in v0.12, and so the workspace name can now be accessed using the terraform.workspace attribute.`,
			Subject:  rng.ToHCL().Ptr(),
		})
		return cty.DynamicVal, diags

	default:
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  `Invalid "terraform" attribute`,
			Detail:   fmt.Sprintf(`The "terraform" object does not have an attribute named %q. The only supported attribute is terraform.workspace, the name of the currently-selected workspace.`, addr.Name),
			Subject:  rng.ToHCL().Ptr(),
		})
		return cty.DynamicVal, diags
	}
}

func (s staticScopeData) GetInputVariable(_ context.Context, ident addrs.InputVariable, rng tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	variable, ok := s.eval.cfg.Variables[ident.Name]
	if !ok {
		return cty.NilVal, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Undefined variable",
			Detail:   fmt.Sprintf("Undefined variable %s", ident.String()),
			Subject:  rng.ToHCL().Ptr(),
		})
	}

	if variable.ConstSet && !variable.Const {
		return cty.NilVal, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Unable to use variable in static context",
			Detail:   fmt.Sprintf("The variable %q cannot be used in a static context, because it is declared as \"const = false\".", variable.Name),
			Subject:  rng.ToHCL().Ptr(),
		})
	}

	id := StaticIdentifier{
		Module:    s.eval.call.addr,
		Subject:   fmt.Sprintf("var.%s", variable.Name),
		DeclRange: variable.DeclRange,
	}

	val, valDiags := s.eval.call.vars(variable)
	diags = diags.Append(valDiags)
	if valDiags.HasErrors() {
		// If the variable value was too invalid to pass the initial request
		// then we'll bail out immediately here since our other checks below
		// are likely to produce redundant errors that would be confusing.
		return cty.DynamicVal, s.enhanceDiagnostics(id, diags)
	}

	// "val" now contains the raw value passed by the caller. We need to prepare it
	// in various ways based on the declaration, so that it conforms to the
	// requirements expected by the module author.
	// FIXME: This is currently essentially a duplication of various logic from
	// internal/tofu/eval_variable.go, prepareFinalInputVariableValue.
	// We should find some way for both of these codepaths to share this logic.

	convertTy := variable.ConstraintType
	if convertTy == cty.NilType {
		convertTy = cty.DynamicPseudoType
	}

	// FIXME: Failing when a required variable isn't set or substituting its default
	// when it is could be implemented centrally here, but currently variations of
	// that are duplicated into each implementation of s.eval.call.vars, so we'll
	// just assume that the default was already substituted if appropriate.
	// We do still need to re-evaluate the default here though, because we might
	// need it downstream.
	defaultVal := variable.Default
	if defaultVal != cty.NilVal {
		var err error
		defaultVal, err = convert.Convert(defaultVal, convertTy)
		if err != nil {
			// We shouldn't get here because the default value's convertability to
			// the type constraint is checked during config decoding, but we'll
			// handle this here just to be robust.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid default value for module argument",
				Detail: fmt.Sprintf(
					"The default value for variable %q is incompatible with its type constraint: %s.",
					variable.Name, err,
				),
				Subject: &variable.DeclRange,
			})
			return cty.UnknownVal(variable.ConstraintType), diags
		}
	}

	// Some type constraints contain default values to use when attributes are null,
	// so we need to resolve those before we proceed further.
	if variable.TypeDefaults != nil && !val.IsNull() {
		val = variable.TypeDefaults.Apply(val)
	}

	// The module author specifies what type of value they are expecting. The
	// caller is allowed to provide any value that can convert to the given
	// type constraint, while expressions in the module only see the converted
	// value.
	var err error
	val, err = convert.Convert(val, convertTy)
	if err != nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid value for input variable",
			Detail: fmt.Sprintf(
				"The given value is not suitable for %s declared at %s: %s.",
				id.Subject, id.DeclRange, err,
			),
		})
		// We'll return a placeholder unknown value to avoid producing
		// redundant downstream errors.
		return cty.UnknownVal(variable.Type), diags
	}

	// By the time we get here, we know:
	// - val matches the variable's type constraint
	// - val is definitely not cty.NilVal, but might be a null value if the given was already null.
	//
	// That means we just need to handle the case where the value is null,
	// which might mean we need to use the default value, or produce an error.
	//
	// For historical reasons we do this only for a "non-nullable" variable.
	// Nullable variables just appear as null if they were set to null,
	// regardless of any default value.
	if val.IsNull() && !variable.Nullable {
		if defaultVal != cty.NilVal {
			val = defaultVal
		} else {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  `Required variable not set`,
				Detail: fmt.Sprintf(
					"The given value is not suitable for %s defined at %s: required variable may not be set to null.",
					id.Subject, id.DeclRange.String(),
				),
				Subject: id.DeclRange.Ptr(),
			})
		}
	}

	if variable.Sensitive {
		// Sensitive input variables must always be marked on the way in, so
		// that their sensitivity can propagate to derived expressions.
		val = val.Mark(marks.Sensitive)
	}
	if variable.Ephemeral {
		// Ephemeral input variables must always be marked as such, so
		// that their ephemerality can propagate to derived expressions.
		val = val.Mark(marks.Ephemeral)
	}

	// TODO: We should check the variable validations here too, since module
	// authors expect that any value that doesn't meet the validation requirements
	// will not propagate to any other expression in the module, but that's
	// a non-trivial amount of code to duplicate here so we'll just let it be
	// for now. This means that invalid variable values can make it into
	// static eval contexts where they will probably produce confusing errors,
	// but if we manage to get far enough despite that then we'll catch the
	// variable validation errors once we reach the dynamic eval phase.

	return val, s.enhanceDiagnostics(id, diags)
}

func (s staticScopeData) GetOutput(context.Context, addrs.OutputValue, tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	panic("Not Available in Static Context")
}

func (s staticScopeData) GetCheckBlock(context.Context, addrs.Check, tfdiags.SourceRange) (cty.Value, tfdiags.Diagnostics) {
	panic("Not Available in Static Context")
}
