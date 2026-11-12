// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package configadjust is choudoufu's seam for ownership markers on the new
// runtime (#2256 U4, #2340). It is a fork-only package: upstream's
// internal/engine has no extension point for rewriting a resource instance's
// configuration value before it is planned, so the planning and applying
// engines call [Adjust] at the two places a managed resource instance is
// planned from configuration.
//
// The interfaces here mirror the ones internal/tofu defines in
// resource_identity.go (ConfigValueAdjuster and its optional capabilities)
// method for method. internal/engine must not import internal/tofu, and Go
// interfaces are satisfied structurally, so the value internal/tofu holds in
// its Context is passed through unchanged and the type assertions below find
// the same optional capabilities the old runtime finds. internal/tofu checks
// at compile time that the two sets stay identical.
package configadjust

import (
	"context"
	"reflect"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ConfigValueAdjuster is internal/tofu's ConfigValueAdjuster. See its doc
// comment there for the contract, in particular that it is applied to the
// evaluated configuration value only, before ignore_changes and before the
// provider plans (`opentofu/opentofu#3016`).
type ConfigValueAdjuster interface {
	AdjustConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics)
}

// CreateConfigValueAdjuster is internal/tofu's CreateConfigValueAdjuster
// (GitHub issues #1084 and #1512).
type CreateConfigValueAdjuster interface {
	AdjustCreateConfigValue(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics)
}

// PriorConfigValueAdjuster is internal/tofu's PriorConfigValueAdjuster
// (epic #1885).
type PriorConfigValueAdjuster interface {
	AdjustConfigValueToPrior(ctx context.Context, addr addrs.AbsResourceInstance, evaluated, adjusted, prior cty.Value, schema providers.Schema) cty.Value
}

// IgnoreChangesAdjuster is internal/tofu's IgnoreChangesAdjuster (GitHub
// issue #451).
type IgnoreChangesAdjuster interface {
	AdjustIgnoreChanges(ctx context.Context, addr addrs.AbsResourceInstance, schema providers.Schema) []cty.Path
}

// AppliedMarkerVerifier is internal/tofu's AppliedMarkerVerifier (GitHub
// issue #1192, #2256 U5). It is asked after a create or an update, never a
// delete, whether the applied object still carries the marker the plan did.
type AppliedMarkerVerifier interface {
	VerifyAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, action plans.Action, planned, applied cty.Value, schema providers.Schema) tfdiags.Diagnostics
}

// AppliedMarkerWriter is internal/tofu's AppliedMarkerWriter (GitHub issue
// #1084, #2256 U5). It is called only for a create that has succeeded so
// far, and the value it returns is the one stored in state.
type AppliedMarkerWriter interface {
	WriteAppliedMarkers(ctx context.Context, addr addrs.AbsResourceInstance, provider addrs.AbsProviderConfig, action plans.Action, applied cty.Value, schema providers.Schema) (cty.Value, tfdiags.Diagnostics)
}

// Adjusted is what [Adjust] returns.
type Adjusted struct {
	// Config is the configuration value to plan this instance with: a create
	// when the prior was null, an update (or the update half of a replace
	// decision) otherwise.
	Config cty.Value

	// CreateConfig is the value the create half of a replace is planned
	// with (GitHub issue #1512). At apply the new runtime plans that create
	// with no prior object, so [Adjust] takes the create entry point there,
	// and the plan must have used the same value or the final plan is
	// inconsistent with it. It equals Config when the instance is being
	// created or the adjuster does not distinguish creates.
	CreateConfig cty.Value

	// ExtraIgnoreChanges are paths to ignore on top of the configuration's
	// own lifecycle ignore_changes.
	ExtraIgnoreChanges []cty.Path
}

// Adjust applies adj to one managed resource instance's evaluated
// configuration value, with the ordering rules the old runtime follows in
// NodeAbstractResourceInstance.plan (internal/tofu,
// node_resource_abstract_instance.go):
//
//   - an instance with no prior object (prior is cty.NilVal or null) is being
//     created, and an adjuster implementing [CreateConfigValueAdjuster] is
//     asked through that entry point instead of AdjustConfigValue;
//   - for an instance with a prior object, such an adjuster is also asked for
//     the create value, kept in CreateConfig for a replace's create half;
//   - [PriorConfigValueAdjuster] then gets a second look with the evaluated
//     value, the adjusted one and the prior, for an instance with a prior
//     object only;
//   - [IgnoreChangesAdjuster] contributes extra ignore_changes paths.
//
// The engine's own bookkeeping marks on config as a whole are peeled off
// before the adjuster sees it and put back on both returned values; see
// [peelEngineMarks].
//
// Callers must pass the result to the provider's planning step and never
// adjust anything the provider returns. A nil adj returns config unchanged
// in both value fields and loads no schema. prior should be unmarked.
func Adjust(
	ctx context.Context,
	adj ConfigValueAdjuster,
	addr addrs.AbsResourceInstance,
	config, prior cty.Value,
	loadSchema func(context.Context) (providers.Schema, tfdiags.Diagnostics),
) (Adjusted, tfdiags.Diagnostics) {
	if adj == nil {
		return Adjusted{Config: config, CreateConfig: config}, nil
	}
	config, engineMarks := peelEngineMarks(config)
	ret, diags := adjust(ctx, adj, addr, config, prior, loadSchema)
	if len(engineMarks) != 0 {
		ret.Config = ret.Config.WithMarks(engineMarks)
		ret.CreateConfig = ret.CreateConfig.WithMarks(engineMarks)
	}
	return ret, diags
}

// adjust is [Adjust] after the engine's marks are peeled off config.
func adjust(
	ctx context.Context,
	adj ConfigValueAdjuster,
	addr addrs.AbsResourceInstance,
	config, prior cty.Value,
	loadSchema func(context.Context) (providers.Schema, tfdiags.Diagnostics),
) (Adjusted, tfdiags.Diagnostics) {
	ret := Adjusted{Config: config, CreateConfig: config}
	var diags tfdiags.Diagnostics

	schema, moreDiags := loadSchema(ctx)
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return ret, diags
	}

	creating := prior == cty.NilVal || prior.IsNull()
	ca, distinguishesCreate := adj.(CreateConfigValueAdjuster)

	var adjusted cty.Value
	if distinguishesCreate && creating {
		adjusted, moreDiags = ca.AdjustCreateConfigValue(ctx, addr, config, schema)
	} else {
		adjusted, moreDiags = adj.AdjustConfigValue(ctx, addr, config, schema)
	}
	diags = diags.Append(moreDiags)
	if moreDiags.HasErrors() {
		return ret, diags
	}
	createVal := adjusted

	if distinguishesCreate && !creating {
		// Only its errors are kept: any warning it raises, the call above
		// has already raised for the same value.
		var createDiags tfdiags.Diagnostics
		createVal, createDiags = ca.AdjustCreateConfigValue(ctx, addr, config, schema)
		if createDiags.HasErrors() {
			return ret, diags.Append(createDiags)
		}
	}

	if !creating {
		if pa, ok := adj.(PriorConfigValueAdjuster); ok {
			adjusted = pa.AdjustConfigValueToPrior(ctx, addr, config, adjusted, prior, schema)
		}
	}

	if ia, ok := adj.(IgnoreChangesAdjuster); ok {
		ret.ExtraIgnoreChanges = ia.AdjustIgnoreChanges(ctx, addr, schema)
	}
	ret.Config = adjusted
	ret.CreateConfig = createVal
	return ret, diags
}

// peelEngineMarks splits the marks on config itself (not on its attributes)
// into the language's own, which stay on the returned value, and every other
// mark, which is returned separately.
//
// The new runtime marks a resource instance's configuration value as a whole
// with its own bookkeeping: depends_on, and references in count, for_each,
// provisioners and replace_triggered_by, put the evaluator's
// ResourceInstanceMark on the object (internal/lang/eval/internal/tofu2024,
// compile_resources.go), and through depends_on a dependency's planned value
// hoists objchange.PendingChange there too. The ownership-marker adjuster
// (internal/live/projection, NodeResolver) declines a configuration marked
// as a whole, because a marked value may be sensitive, so on such a
// resource it wrote no markers. Worse, the two phases see different marks:
// the planning oracle strips ResourceInstanceMark and the applying oracle
// does not, and PendingChange is there at plan and gone at apply. So the
// same resource was stamped in one phase and not the other, and the final
// plan failed as inconsistent (reference-k8s drift_reconverge,
// reference-azure day2_crash on #2256), or neither stamped and a create
// went out unmarked.
//
// The language's marks (package internal/lang/marks: Sensitive, Ephemeral,
// deprecation) stay on the value, so the adjuster still refuses a
// configuration that is sensitive as a whole. The rest carry no meaning
// for a marker; they are put back unchanged on what [Adjust] returns.
func peelEngineMarks(config cty.Value) (cty.Value, cty.ValueMarks) {
	if config == cty.NilVal || !config.IsMarked() {
		return config, nil
	}
	inner, all := config.Unmark()
	keep := make(cty.ValueMarks, len(all))
	peeled := make(cty.ValueMarks, len(all))
	for m := range all {
		if IsLanguageMark(m) {
			keep[m] = struct{}{}
		} else {
			peeled[m] = struct{}{}
		}
	}
	return inner.WithMarks(keep), peeled
}

// languageMarksPkg is the import path of internal/lang/marks, read off one
// of its mark values so it follows the module path.
var languageMarksPkg = reflect.TypeOf(marks.Sensitive).PkgPath()

// IsLanguageMark reports whether m is one of the language's own marks,
// declared in internal/lang/marks. Every other mark on a configuration value
// is the new runtime's bookkeeping (package identityresolve peels those too).
func IsLanguageMark(m any) bool {
	t := reflect.TypeOf(m)
	if t == nil {
		return false
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == languageMarksPkg
}
