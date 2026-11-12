// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package identityresolve is choudoufu's identity-resolution seam on the new
// runtime (#2256 U6, #2342). It is a fork-only package, the sibling of
// configadjust: upstream's planning engine adopts an existing object only
// through an import statement (`#4726`, `#4727`), so the planning engine asks
// a [ResourceIdentityResolver] about each desired managed resource instance
// that has no prior object and no import statement, and turns a found target
// into an import statement of its own.
//
// [ResourceIdentityResolver] mirrors internal/tofu's interface of the same
// name method for method; internal/tofu checks at compile time that the two
// stay identical. The helpers below are the rules the old runtime applies to
// a resolver-supplied target in NodePlannableResourceInstance
// (internal/tofu/node_resource_plan_instance.go), shared so the two runtimes
// read one copy:
//
//   - a resolver's target is a guess, not the operator's promise an import
//     block is, so an import that finds nothing ([ImportAbsent]) plans a
//     create instead of failing the plan;
//   - a type with no classic Importer gets the stub ImportResourceState would
//     have returned, built from the target's own identity object
//     ([IdentityValues], noimporter.SynthesizeStub), or the refusal
//     [NoImporterRefusal] when the target carries only an opaque ID.
package identityresolve

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/ctymarks"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/engine/configadjust"
	"github.com/intentius/choudoufu/internal/live/absent"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// ResourceIdentityResolver is internal/tofu's ResourceIdentityResolver. See
// its doc comment there for the contract: it is asked once per desired
// managed resource instance that has no prior object and no import
// statement, and found is false when it has nothing to say.
type ResourceIdentityResolver interface {
	ResolveResourceIdentity(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (target providers.ImportTarget, found bool, diags tfdiags.Diagnostics)
}

// Resolve asks r about one instance. It loads the schema only when r is not
// nil, and reports found only for a target that names something (an ID or an
// identity object), the same test NodeResolver's own steps apply. config
// reaches r with the engine's own marks removed ([PeelEngineMarks]).
func Resolve(
	ctx context.Context,
	r ResourceIdentityResolver,
	addr addrs.AbsResourceInstance,
	config cty.Value,
	loadSchema func(context.Context) (providers.Schema, tfdiags.Diagnostics),
) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	if r == nil || config == cty.NilVal {
		return providers.ImportTarget{}, false, nil
	}
	var diags tfdiags.Diagnostics
	schema, moreDiags := loadSchema(ctx)
	diags = diags.Append(moreDiags)
	if diags.HasErrors() || schema.Block == nil {
		return providers.ImportTarget{}, false, diags
	}
	target, found, moreDiags := r.ResolveResourceIdentity(ctx, addr, PeelEngineMarks(config), schema)
	diags = diags.Append(moreDiags)
	if diags.HasErrors() || !found {
		return providers.ImportTarget{}, false, diags
	}
	if !target.IsIDBased() && !target.IsIdentityBased() {
		return providers.ImportTarget{}, false, diags
	}
	return target, true, diags
}

// PeelEngineMarks removes every mark on config, at any depth, that is not one
// of the language's own (configadjust.IsLanguageMark): the new runtime's
// bookkeeping, chiefly objchange.PendingChange.
//
// The new runtime marks each part of a resource instance's planned value
// that the plan changes with PendingChange, and a reference carries the mark
// into whatever reads it. So a resource whose identity argument interpolates
// a sibling's ordinary attribute ("crash-read-${a.data["suffix"]}"), while
// that sibling is being created or updated, reaches the resolver with a
// known string marked on that one attribute. NodeResolver refuses a marked
// identity value, because a marked value may be sensitive
// (identity.ComponentsFromValue), so the instance failed with "No source for
// this instance's identity" (reference-k8s day2_crash on #2256). The old
// runtime puts no such mark on the value it hands the resolver. The value is
// the planned one either way; the mark only says it is not applied yet, which
// says nothing about the identity it names.
//
// The language's marks (Sensitive, Ephemeral, deprecation) stay, so a
// sensitive identity argument is still refused. configadjust's peel removes
// only the marks on the whole value, because a marker adjuster's own
// attribute marks are its business; the resolver reads attributes, so it
// needs them gone at depth.
func PeelEngineMarks(config cty.Value) cty.Value {
	if config == cty.NilVal || !config.ContainsMarked() {
		return config
	}
	peeled, err := config.WrangleMarksDeep(func(mark any, _ cty.Path) (ctymarks.WrangleAction, error) {
		if configadjust.IsLanguageMark(mark) {
			return nil, nil
		}
		return ctymarks.WrangleDrop, nil
	})
	if err != nil {
		return config
	}
	return peeled
}

// importSyntheticAbsentSummaries are the import path's own diagnostics that
// already mean "there is nothing here": an empty ImportedResources list, an
// imported object with a null value, or a post-import refresh that comes back
// null. Both runtimes construct these with fixed text.
var importSyntheticAbsentSummaries = map[string]bool{
	"Import returned no resources":             true,
	"Import returned null resource":            true,
	"Cannot import non-existent remote object": true,
}

// ImportAbsent reports whether every error in diags describes an ordinary
// absence of the object target names, either one of the import path's own
// "nothing here" diagnostics or a provider's not-found-shaped error, rather
// than a genuine failure to answer. diags with no errors reports false.
//
// providerType is the type name of the provider the import was asked of: an
// Azure 404 reads as absence only from an Azure provider and only when it is
// about that object ([absent.Object], GitHub issues #2133 and #2140).
func ImportAbsent(diags tfdiags.Diagnostics, target providers.ImportTarget, providerType string) bool {
	object := absent.Object{ID: target.ID, Identity: IdentityValues(target), Provider: providerType}
	sawError := false
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		sawError = true
		desc := d.Description()
		if importSyntheticAbsentSummaries[desc.Summary] {
			continue
		}
		if !absent.Matches(desc.Summary, desc.Detail, object) {
			return false
		}
	}
	return sawError
}

// IdentityValues extracts a target's identity object as one string per
// attribute, keyed by the provider's own name for it, the shape
// noimporter.SynthesizeStub needs. Only an identity-based target reports
// anything: an opaque ID has no attribute breakdown, and none is invented. A
// null, unknown or unconvertible attribute is skipped rather than forced.
func IdentityValues(target providers.ImportTarget) map[string]string {
	if !target.IsIdentityBased() {
		return nil
	}
	ty := target.Identity.Type()
	if !ty.IsObjectType() {
		return nil
	}
	values := make(map[string]string, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		v := target.Identity.GetAttr(name)
		if v.IsNull() || !v.IsKnown() {
			continue
		}
		s, err := convert.Convert(v, cty.String)
		if err != nil {
			continue
		}
		values[name] = s.AsString()
	}
	return values
}

// NoImporterRefusalSummary is the summary of [NoImporterRefusal].
const NoImporterRefusalSummary = "Resource type has no classic Importer"

// NoImporterRefusal is the error for a resolved target of a type whose
// provider implements no ImportResourceState, when the target carries no
// identity values to build a stub from. detail is noimporter.Diagnostics'
// own account of the provider's answer.
func NoImporterRefusal(addr fmt.Stringer, target providers.ImportTarget, detail string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(
		tfdiags.Error,
		NoImporterRefusalSummary,
		fmt.Sprintf(
			"The %s with %s cannot be imported: %s. This is not the provider erroring - it is answering that ImportResourceState is not implemented for this type at all, a fixed property of the provider's own code that no identity or retry changes.",
			addr, target.String(), detail,
		),
	)
}
