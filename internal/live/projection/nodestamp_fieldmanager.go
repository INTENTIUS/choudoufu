// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/noimporter"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the field-granular half of the node-path stamp (GitHub
// issue #1191, ruled 2026-10-03): a Kubernetes resource that owns fields of
// an object rather than an object ([substrate.FieldGranularShape]) is
// marked by the server-side-apply field manager its writes are made under,
// [markers.FieldManagerFor]'s "choudoufu:<estate>". There is no label to
// write - the patched object's own estate label, if it has one, is
// irrelevant - so the stamp sets the resource's own field_manager argument,
// and the provider then makes every apply under the estate's name.
//
// The read side is the same name: [fieldGranularSeed] puts it in the prior
// the projection hands ReadResource, because hashicorp/kubernetes reads
// back only the fields managedFields says THAT manager owns (plus the keys
// the prior already names). Read under the provider's default "Terraform"
// instead, a live plan would report this estate's fields as somebody
// else's and plan them as a change on every run.
//
// What a hand-written field_manager meets is what a hand-written estate
// label meets: the same estate's own name is accepted unchanged, and any
// other value is the fatal [SummaryMarkerConflict], because writing under
// it would either claim the fields for another estate or for no estate at
// all.

// fieldGranularOwned reports whether schema is the field-granular shape on
// the Kubernetes family's provider. providerType "" asks the shape alone,
// as [substrate.SurfaceOf] does for a provider no family claims.
func fieldGranularOwned(providerType string, schema providers.Schema) bool {
	if providerType != "" {
		if s, ok := substrate.ForProvider(providerType); !ok || s != substrate.Kubernetes {
			return false
		}
	}
	_, ok := substrate.FieldGranularShape(schema.Block)
	return ok
}

// stampFieldManager returns config with its field_manager argument set to
// this estate's field manager, or config unchanged with a diagnostic
// saying why it could not be.
func (n *NodeResolver) stampFieldManager(addr addrs.AbsResourceInstance, config cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if why := markers.ValidFieldManagerEstate(n.Estate); why != "" {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryFieldManagerNotCarried,
			fmt.Sprintf("%s cannot carry this estate's ownership marker: %s. Rename the estate in the live block.", addr, why)))
		return config, diags
	}
	if config == cty.NilVal || config.IsNull() || !config.IsKnown() || config.IsMarked() || !config.Type().IsObjectType() || !config.Type().HasAttribute(substrate.FieldManagerAttr) {
		return config, diags
	}
	if n.PolicyUntag[addr.String()] == markers.TagEstate {
		// GitHub issue #1002's release: the configuration's own value (the
		// provider's default manager when it names none) is what the next
		// apply writes under, which hands the fields to no estate.
		return config, diags
	}

	want := markers.FieldManagerFor(n.Estate)
	declared, declaredMarks := config.GetAttr(substrate.FieldManagerAttr).Unmark()
	switch {
	case declared.IsNull():
	case !declared.IsKnown():
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryFieldManagerUnresolved,
			fmt.Sprintf("%s's field_manager is not known until apply, so this run cannot tell which estate its writes would belong to. Under a live block the field manager is the ownership marker: remove the argument and this run writes under %q, or set it to that.", addr, want)))
		return config, diags
	case declared.Type() != cty.String:
		return config, diags
	case declared.AsString() == want:
		return config, diags
	default:
		got := declared.AsString()
		detail := fmt.Sprintf(
			"%s declares field_manager = %q and this run writes under %q, the estate %q's field manager. Under a live block the field manager is the ownership marker for a resource that owns fields rather than an object, so a write under any other name would leave this estate's fields owned by someone else. Remove the argument, or set it to %q.",
			addr, got, want, n.Estate, want)
		if other, ok := markers.EstateOfFieldManager(got); ok {
			detail = fmt.Sprintf(
				"%s declares field_manager = %q, the estate %q's field manager, and this run is the estate %q. A plan never writes under another estate's name: name %s in the live block if that is the estate this run is for, or remove the argument.",
				addr, got, other, n.Estate, other)
		}
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryMarkerConflict, detail))
		return config, diags
	}

	elems := config.AsValueMap()
	elems[substrate.FieldManagerAttr] = cty.StringVal(want).WithMarks(declaredMarks)
	return cty.ObjectVal(elems), diags
}

// fieldGranularWrittenMaps are the map attributes a field-granular type
// writes into its object: metadata.labels, metadata.annotations, the pod
// template's annotations, and a ConfigMap's or Secret's data. Read off
// the schema by name, as the field-owner check in internal/command reads
// them.
var fieldGranularWrittenMaps = []string{"labels", "annotations", "template_annotations", "data"}

// fieldGranularWrittenBlocks are the nested blocks a field-granular type
// writes: one container's env, a node's taints.
var fieldGranularWrittenBlocks = []string{"env", "taint"}

// fieldGranularSeed puts this estate's field manager into the prior the
// projection hands ReadResource, and takes the fields the block writes OUT
// of it. hashicorp/kubernetes reads back the keys the manager owns plus
// the keys the prior already names; seeded from configuration, a key some
// other manager wrote would read back as if this estate's, and the plan
// would show no change over a field this estate has never written. Left
// out, the read is the manager's own fields and nothing else - this
// estate's ownership, read off the API server. See this file's doc
// comment.
func fieldGranularSeed(seed map[string]cty.Value, estate string) map[string]cty.Value {
	if estate == "" || markers.ValidFieldManagerEstate(estate) != "" {
		return seed
	}
	out := make(map[string]cty.Value, len(seed)+1)
	for k, v := range seed {
		out[k] = v
	}
	for _, name := range fieldGranularWrittenMaps {
		delete(out, name)
	}
	out[substrate.FieldManagerAttr] = cty.StringVal(markers.FieldManagerFor(estate))
	return out
}

// fieldGranularOrphanSeed adds to seed every top-level string attribute
// of schema that values names and seed does not (GitHub issue #1863). An
// orphan the field-manager sweep filed (internal/live/discovery's
// kubernetes_fieldorphans.go) carries its object's api_version and kind
// and, for kubernetes_env, the container whose env the estate's manager
// owns: hashicorp/kubernetes' env read and delete find the container by
// that argument, and an import stub never carries it. A declared block's
// seed comes from its configuration instead, so this is for an undeclared
// read only. The metadata block is not touched here; [fieldGranularStub]
// places it from the same values.
func fieldGranularOrphanSeed(seed map[string]cty.Value, schema providers.Schema, values map[string]string) map[string]cty.Value {
	if schema.Block == nil || len(values) == 0 {
		return seed
	}
	out := make(map[string]cty.Value, len(seed)+len(values))
	for k, v := range seed {
		out[k] = v
	}
	for name, v := range values {
		if _, set := out[name]; set {
			continue
		}
		a, ok := schema.Block.Attributes[name]
		if !ok || a == nil || a.Type != cty.String || a.Computed && !a.Optional {
			continue
		}
		out[name] = cty.StringVal(v)
	}
	return out
}

// fieldGranularHoldsFields reports whether a field-granular read came back
// holding any written field: a non-empty written map, or a non-empty
// written block. An unknown value counts as holding, so nothing is called
// absent on a value this pass cannot see into.
func fieldGranularHoldsFields(obj cty.Value, schema providers.Schema) bool {
	if obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return true
	}
	obj, _ = obj.UnmarkDeep()
	names := append(append([]string(nil), fieldGranularWrittenMaps...), fieldGranularWrittenBlocks...)
	sawWritten := false
	for _, name := range names {
		if !obj.Type().HasAttribute(name) {
			continue
		}
		sawWritten = true
		// Unmarked here, at the read: a secret's data map is sensitive,
		// and only its length is asked.
		v, _ := obj.GetAttr(name).UnmarkDeep()
		if !v.IsKnown() {
			return true
		}
		if v.IsNull() || !v.CanIterateElements() {
			continue
		}
		if v.LengthInt() > 0 {
			return true
		}
	}
	// A schema none of whose attributes is a known written field is a
	// shape this pass does not understand; it is never called absent.
	return !sawWritten
}

// fieldGranularStub is the stub ImportResourceState would have returned for
// a field-granular type, five of whose six types have no Importer at all
// (hashicorp/kubernetes 3.2.1 answers "resource ... doesn't support
// import" for all but kubernetes_env). It extends
// [noimporter.SynthesizeStub]'s top-level placement with the two things
// these types' reads need and a top-level placement cannot give them:
//
//   - the metadata block, holding the patched object's name and, when the
//     identity has one, its namespace. The identity reads both out of that
//     block ([substrate.FieldGranularShape]), so the values are already
//     this run's resolved identity, never a guess.
//   - the id, as the import id this run rendered. The provider's SDK
//     reports an object with an empty id as gone, and kubernetes_env's
//     Read parses its id for apiVersion, kind and name, which is exactly
//     the [substrate.FieldGranularImportSyntax] the import id is rendered
//     in.
//
// ok is false when there is no name to place, in which case the caller's
// ordinary refusal stands.
func fieldGranularStub(schema providers.Schema, values map[string]string, importID string) (cty.Value, bool) {
	if schema.Block == nil || values["name"] == "" || importID == "" {
		return cty.NilVal, false
	}
	nested, ok := schema.Block.BlockTypes["metadata"]
	if !ok || nested == nil {
		return cty.NilVal, false
	}
	stub, ok := noimporter.SynthesizeStub(schema, values)
	if !ok {
		stub = cty.NullVal(schema.Block.ImpliedType())
	}
	attrs := map[string]cty.Value{}
	for name, ty := range schema.Block.ImpliedType().AttributeTypes() {
		if stub.IsNull() {
			attrs[name] = cty.NullVal(ty)
			continue
		}
		attrs[name] = stub.GetAttr(name)
	}

	metaAttrs := map[string]cty.Value{}
	for name, ty := range nested.Block.ImpliedType().AttributeTypes() {
		v, has := values[name]
		if has && (name == "name" || name == "namespace") && ty == cty.String {
			metaAttrs[name] = cty.StringVal(v)
			continue
		}
		metaAttrs[name] = cty.NullVal(ty)
	}
	attrs["metadata"] = cty.ListVal([]cty.Value{cty.ObjectVal(metaAttrs)})
	if ty, ok := schema.Block.ImpliedType().AttributeTypes()["id"]; ok && ty == cty.String {
		attrs["id"] = cty.StringVal(importID)
	}
	return cty.ObjectVal(attrs), true
}

// The field-manager stamp's diagnostic summaries; registered in
// refusals.go.
const (
	SummaryFieldManagerNotCarried = "Ownership marker is not a legal field manager name"
	SummaryFieldManagerUnresolved = "Cannot set the field manager on an unresolved value"
)
