// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package untag

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Target is one live resource GitHub issue #67's undeclared_tagged =
// "untag" verb governs: enough identity to import and read it fresh (an
// undeclared orphan has no prior state to seed a read from - it was never
// declared) and enough to report on regardless of outcome.
type Target struct {
	// TypeName is the resource type, e.g. "aws_vpc".
	TypeName string

	// ImportID is the import identifier discovery already worked out for
	// this resource.
	ImportID string

	// Identity is the full identity object the provider served during
	// discovery, when it has one. Preferred over ImportID for the import
	// call when the provider's own identity schema accepts it; see
	// [importTarget].
	Identity cty.Value

	// Marker is the tofu-address tag value this resource carries, for
	// reporting - the address it would have been declared at, had it been.
	Marker string

	// DisplayName is the provider's own label, for reporting.
	DisplayName string
}

// String renders a target on one line, for logs and test failures.
func (t Target) String() string {
	return t.TypeName + " " + t.ImportID
}

// Outcome is what [Release] did, or did not do, about one [Target].
type Outcome struct {
	Target

	// OK is true when the tag key was confirmed released - the provider's
	// own apply reported no error, and the object read back afterward no
	// longer carries it. False means the resource was left exactly as it
	// was found: nothing here ever falls back to destroying it.
	OK bool

	// Detail is one sentence aimed at an operator: what happened, or
	// exactly why it did not.
	Detail string
}

// String renders an outcome on one line.
func (o Outcome) String() string {
	status := "RELEASED"
	if !o.OK {
		status = "FAILED"
	}
	return o.Target.String() + " " + status + ": " + o.Detail
}

// Result is what one [Release] call did.
type Result struct {
	// Key is the tag key every outcome tried to release.
	Key string

	Outcomes []Outcome
}

// Failed reports whether any outcome in the result did not confirm the
// release - the caller's cue to surface a per-resource error rather than
// treat the batch as clean.
func (r *Result) Failed() bool {
	if r == nil {
		return false
	}
	for _, o := range r.Outcomes {
		if !o.OK {
			return true
		}
	}
	return false
}

// Release removes key from every target's tags, one provider round trip at
// a time, and reads each one back to confirm before reporting it done.
// Nothing here ever destroys, replaces, or changes anything but key: a
// plan that would do more than that is refused before ApplyResourceChange
// is ever called, and the resource is reported failed rather than
// partially touched.
//
// A per-resource failure never stops the rest of the batch - every target
// gets its own attempt and its own outcome, the same rule
// internal/live/liveimport's Approve follows for the declared side of this
// same policy verb (declared_tagged = "untag", released through the
// ordinary plan graph rather than here).
func Release(ctx context.Context, provider providers.Interface, key string, targets []Target) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	res := &Result{Key: key}
	if len(targets) == 0 {
		return res, diags
	}
	if provider == nil {
		return res, diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"No provider access for the apply-time tag release",
			fmt.Sprintf("GitHub issue #67's undeclared_tagged = \"untag\" verb has %d resource(s) to release %q from, but no configured provider to release it through. Nothing was changed.", len(targets), key)))
	}

	schemaResp := provider.GetProviderSchema(ctx)
	if schemaResp.Diagnostics.HasErrors() {
		return res, diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"Provider schema unavailable for the apply-time tag release",
			fmt.Sprintf("The provider failed to serve its schema, so undeclared_tagged = \"untag\" could not release %q from any of the %d resource(s) it governs: %s. Nothing was changed.", key, len(targets), schemaResp.Diagnostics.Err())))
	}

	for _, t := range targets {
		res.Outcomes = append(res.Outcomes, releaseOne(ctx, provider, schemaResp.ResourceTypes, key, t))
	}

	if res.Failed() {
		var failed []string
		for _, o := range res.Outcomes {
			if !o.OK {
				failed = append(failed, o.String())
			}
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"Some resources were not released",
			fmt.Sprintf("undeclared_tagged = \"untag\" could not release %q from %d of %d resource(s):\n  - %s", key, len(failed), len(targets), strings.Join(failed, "\n  - ")),
		))
	}

	return res, diags
}

// releaseOne is the whole provider conversation for one target: import to
// turn its identity into a schema-shaped object (an undeclared orphan has
// no prior state to seed a read from), read to refresh it, and - only if
// the named key is actually present - a tags-only Plan+Apply pair that
// removes it and nothing else, verified by reading the object back once
// more.
func releaseOne(ctx context.Context, provider providers.Interface, schemas map[string]providers.Schema, key string, t Target) Outcome {
	out := Outcome{Target: t}

	schema, ok := schemas[t.TypeName]
	if !ok || schema.Block == nil {
		out.Detail = fmt.Sprintf("The provider serves no schema for %s, so the tag release could not run. Nothing was changed.", t.TypeName)
		return out
	}
	if !taggable(schema.Block) {
		out.Detail = fmt.Sprintf("%s has no settable tags argument in the provider's schema, so there is nothing to release. Nothing was changed.", t.TypeName)
		return out
	}
	if t.ImportID == "" && (t.Identity == cty.NilVal || t.Identity.IsNull()) {
		out.Detail = "This resource carries no import identifier and no identity object, so it could not be read. Nothing was changed."
		return out
	}

	target := importTarget(schema, t.ImportID, t.Identity)
	importResp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: t.TypeName,
		Target:   target,
	})
	if importResp.Diagnostics.HasErrors() {
		out.Detail = fmt.Sprintf("The provider failed to look this %s up by %s: %s. Nothing was changed.", t.TypeName, target.String(), importResp.Diagnostics.Err())
		return out
	}
	imported := pickImported(importResp.ImportedResources, t.TypeName)
	if imported == nil || imported.State == cty.NilVal || imported.State.IsNull() {
		out.OK = true
		out.Detail = fmt.Sprintf("The live system reports that this %s no longer exists; there is nothing to release a tag from.", t.TypeName)
		return out
	}

	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:      t.TypeName,
		PriorState:    imported.State,
		Private:       imported.Private,
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: imported.Identity,
	})
	if readResp.Diagnostics.HasErrors() {
		out.Detail = fmt.Sprintf("The provider failed while reading this %s from the live system: %s. Nothing was changed.", t.TypeName, readResp.Diagnostics.Err())
		return out
	}
	if readResp.NewState == cty.NilVal || readResp.NewState.IsNull() {
		out.OK = true
		out.Detail = fmt.Sprintf("The live system reports that this %s no longer exists; there is nothing to release a tag from.", t.TypeName)
		return out
	}
	live := readResp.NewState

	tags, hasTags := tagsFromObj(schema.Block, live)
	if !hasTags {
		out.Detail = fmt.Sprintf("%s carries no readable tags, so there is nothing to release. Nothing was changed.", t.TypeName)
		return out
	}
	if _, present := tags[key]; !present {
		out.OK = true
		out.Detail = fmt.Sprintf("Already carries no %q tag; nothing to release.", key)
		return out
	}

	desired := make(map[string]string, len(tags))
	for k, v := range tags {
		if k == key {
			continue
		}
		desired[k] = v
	}

	desiredObj, err := withTags(schema.Block, live, desired)
	if err != nil {
		out.Detail = fmt.Sprintf("The tags of this %s could not be rewritten: %s. Nothing was changed.", t.TypeName, err)
		return out
	}

	configVal := configValue(schema.Block, desiredObj)
	proposed := objchange.ProposedNew(schema.Block, live, configVal)

	planResp := provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
		TypeName:         t.TypeName,
		PriorState:       live,
		ProposedNewState: proposed,
		Config:           configVal,
		PriorPrivate:     imported.Private,
		// A null of the dynamic pseudo-type, not the zero cty.Value: see
		// internal/live/liveimport's stamp.go, same call, for why a value
		// with no type at all panics the plugin client's conformance
		// check whenever the provider declares a provider_meta schema.
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: imported.Identity,
	})
	if planResp.Diagnostics.HasErrors() {
		out.Detail = fmt.Sprintf("The provider failed while planning the tag release: %s. Nothing was changed.", planResp.Diagnostics.Err())
		return out
	}
	if len(planResp.RequiresReplace) > 0 {
		out.Detail = fmt.Sprintf("Releasing %q from this %s would require replacing it, according to the provider. An untag never destroys or replaces anything; nothing was changed.", key, t.TypeName)
		return out
	}
	planned := planResp.PlannedState
	if planned == cty.NilVal || planned.IsNull() {
		out.Detail = "Planning the tag release produced no object at all. This is a provider bug; nothing was changed."
		return out
	}
	if extra := changedOutsideTags(schema.Block, live, planned); len(extra) > 0 {
		out.Detail = fmt.Sprintf("Releasing %q from this %s would also change %s. An untag is a tags-only write; nothing was changed. Run a plan to see what else has drifted and resolve that first.", key, t.TypeName, strings.Join(extra, ", "))
		return out
	}

	applyResp := provider.ApplyResourceChange(ctx, providers.ApplyResourceChangeRequest{
		TypeName:        t.TypeName,
		PriorState:      live,
		PlannedState:    planResp.PlannedState,
		Config:          configVal,
		PlannedPrivate:  planResp.PlannedPrivate,
		ProviderMeta:    cty.NullVal(cty.DynamicPseudoType),
		PlannedIdentity: planResp.PlannedIdentity,
	})
	if applyResp.Diagnostics.HasErrors() {
		out.Detail = fmt.Sprintf("The provider failed while releasing the tag: %s. The write may have partly landed; read this resource's tags with the cloud's own API before deciding what to do next.", applyResp.Diagnostics.Err())
		return out
	}

	newTags, _ := tagsFromObj(schema.Block, applyResp.NewState)
	if _, stillPresent := newTags[key]; stillPresent {
		out.Detail = fmt.Sprintf("The provider reported no error, but %q is still present on the object read back afterward. Some providers do not serve tags back on a post-apply read; verify with the cloud's own API before relying on this.", key)
		return out
	}

	out.OK = true
	out.Detail = fmt.Sprintf("Released %q. This resource is no longer managed by this estate.", key)
	return out
}

// pickImported selects the imported object belonging to typeName, the same
// rule internal/live/projection/build.go's pickImported follows: an empty
// TypeName is the ordinary single-object answer, matched on typeName
// otherwise. An extra related object the import call also produced is
// ignored - there is no configuration address here to file it under
// either.
func pickImported(imported []providers.ImportedResource, typeName string) *providers.ImportedResource {
	for i := range imported {
		if imported[i].TypeName == "" || imported[i].TypeName == typeName {
			return &imported[i]
		}
	}
	return nil
}

// importTarget mirrors internal/live/projection/build.go's own
// importTarget at a smaller scope: an undeclared orphan has no
// configuration to fall back to building an identity out of, so this
// either converts the identity discovery already read, or falls back to
// the import ID outright - never both, and never a hard failure, since ID
// is always a working answer when identity conversion is not.
func importTarget(schema providers.Schema, importID string, identity cty.Value) providers.ImportTarget {
	if schema.IdentitySchema != nil && identity != cty.NilVal && !identity.IsNull() {
		if val, err := convert.Convert(identity, schema.IdentitySchema.ImpliedType()); err == nil && val.IsWhollyKnown() {
			return providers.ImportTarget{Identity: val}
		}
	}
	return providers.ImportTarget{ID: importID}
}
