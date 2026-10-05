// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The command layer's half of the field-owner boundary (GitHub issue
// #1191, ruled 2026-10-03; the engine half is
// discovery.CheckKubernetesFieldOwners). It sits beside the server-side dry
// run for the same reason that does: the plan is in hand, each change's
// After is the planned write with the estate's field manager already
// stamped into it, and the cluster client is the sweep's.
//
// A change is field-granular by its schema (substrate.FieldGranularShape),
// never by its type name. Where the write lands in the object is read off
// the schema's own attributes too: a labels map writes metadata.labels, an
// annotations map metadata.annotations, template_annotations the pod
// template's, a data map the object's data, an env block one container's
// env, a taint block the node's taints.

// collectKubernetesFieldOwners judges every planned create or update of a
// field-granular instance, per cluster, refuses two kept instances of the
// estate on one object whatever their actions, and returns the refusals and
// warnings discovery.CheckKubernetesFieldOwners and
// discovery.SameObjectFieldWrites raise. estate "" (a run with no estate
// name has stamped nothing) checks nothing.
//
// missing is discovery's Result.FieldGranularMissing (#1885, ruled
// 2026-10-04): a planned create of one of those instances is refused,
// naming the object, unless the same plan creates the object on the same
// cluster ([discovery.FieldGranularMissingRefusals]).
func collectKubernetesFieldOwners(ctx context.Context, sweepers map[string]kubesweep.Sweeper, config *configs.Config, plan *plans.Plan, schemas *tofu.Schemas, estate string, missing map[string]string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if estate == "" || plan == nil || plan.Changes == nil || schemas == nil {
		return diags
	}
	byProvider := map[string][]discovery.FieldGranularWrite{}
	// kept is every field-granular instance the plan keeps, per provider
	// configuration, whatever its action: the same-object refusal's input.
	// A block with nothing to change still writes under the estate's one
	// field manager on its next apply, so a new block on its object erases
	// it as surely as an updated one would (epic #1885: a
	// kubernetes_annotations added to the Deployment an unchanged
	// kubernetes_env.web writes into planned "1 to add" at exit 0). Only
	// the planned creates and updates in byProvider are read back for
	// their fields' owners.
	kept := map[string][]discovery.FieldGranularWrite{}
	// fgCreates and objCreates are #1885's inputs, per cluster: the
	// field-granular creates whose object is known, and the whole objects
	// the plan creates or replaces.
	fgCreates := map[string][]discovery.FieldGranularWrite{}
	objCreates := map[string][]discovery.PlannedObject{}
	var keys []string
	for _, rc := range plan.Changes.Resources {
		if rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		writes := rc.Action == plans.Create || rc.Action == plans.Update || rc.Action == plans.CreateThenDelete || rc.Action == plans.DeleteThenCreate
		if !writes && rc.Action != plans.NoOp {
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if schema == nil {
			continue
		}
		creates := rc.Action == plans.Create || rc.Action == plans.CreateThenDelete || rc.Action == plans.DeleteThenCreate
		key := providerCacheKey(rc.ProviderAddr)
		if _, ok := substrate.FieldGranularShape(schema.Block); !ok {
			if creates {
				if obj, isObj := plannedWholeObject(rc, schema); isObj {
					objCreates[key] = append(objCreates[key], obj)
				}
			}
			continue
		}
		w, ok := plannedFieldGranularWrite(rc, schema)
		if creates && w.Object.Name != "" && w.Object.Kind != "" && len(missing) > 0 {
			fgCreates[key] = append(fgCreates[key], w)
		}
		if ok || w.Object.Name != "" && w.Object.Kind != "" && w.Object.APIVersion != "" {
			// The object alone is what the same-object refusal needs: a
			// value whose written keys are not known yet still names it.
			if _, seen := kept[key]; !seen {
				keys = append(keys, key)
			}
			kept[key] = append(kept[key], w)
		}
		if !ok || !writes {
			continue
		}
		w.Create = rc.Action == plans.Create
		byProvider[key] = append(byProvider[key], w)
	}
	for key := range fgCreates {
		if !containsKey(keys, key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		diags = diags.Append(discovery.FieldGranularMissingRefusals(missing, fgCreates[key], objCreates[key]))
		diags = diags.Append(discovery.SameObjectFieldWrites(config, estate, kept[key]))
		if len(byProvider[key]) == 0 {
			continue
		}
		reader, _ := sweepers[key].(kubesweep.ObjectReader)
		if reader == nil {
			// No cluster client for this provider configuration: the sweep
			// has already said so, once.
			continue
		}
		diags = diags.Append(discovery.CheckKubernetesFieldOwners(ctx, reader, config, estate, byProvider[key]))
	}
	return diags
}

// plannedFieldGranularWrite reads one planned change's After back as the
// object it patches and the fields it writes. ok is false when the planned
// value does not say yet - an unknown name or key - which leaves the
// instance to the apply, where the API server answers.
func plannedFieldGranularWrite(rc *plans.ResourceInstanceChangeSrc, schema *providers.Schema) (discovery.FieldGranularWrite, bool) {
	change, err := rc.Decode(schema)
	if err != nil {
		return discovery.FieldGranularWrite{Addr: rc.Addr}, false
	}
	w, ok := discovery.FieldGranularWriteOf(rc.Addr.Resource.Resource.Type, schema.Block, change.After)
	w.Addr = rc.Addr
	return w, ok
}

// containsKey reports whether keys holds key.
func containsKey(keys []string, key string) bool {
	for _, k := range keys {
		if k == key {
			return true
		}
	}
	return false
}

// plannedWholeObject reads a planned change of a whole Kubernetes object -
// a typed hashicorp/kubernetes resource with a metadata block, or a
// kubernetes_manifest - as the object it creates (#1885). isObj is false
// for anything else. A kind, namespace or name not known until apply
// leaves Known false.
func plannedWholeObject(rc *plans.ResourceInstanceChangeSrc, schema *providers.Schema) (discovery.PlannedObject, bool) {
	typeName := rc.Addr.Resource.Resource.Type
	manifest := markers.ManifestSurface(schema.Block)
	kind, _, typed := kubesweep.KindOfType(typeName)
	if !manifest && (!typed || schema.Block.BlockTypes["metadata"] == nil) {
		return discovery.PlannedObject{}, false
	}
	change, err := rc.Decode(schema)
	if err != nil {
		return discovery.PlannedObject{Kind: kind}, true
	}
	after, _ := change.After.UnmarkDeep()
	var meta cty.Value
	if manifest {
		kind = ""
		m := cty.NilVal
		if after.IsKnown() && !after.IsNull() && after.Type().IsObjectType() && after.Type().HasAttribute(markers.ManifestSurfaceAttr) {
			m = after.GetAttr(markers.ManifestSurfaceAttr)
		}
		if m == cty.NilVal || !m.IsKnown() || m.IsNull() || !(m.Type().IsObjectType() || m.Type().IsMapType()) {
			return discovery.PlannedObject{}, true
		}
		kind = ctyString(m, "kind")
		if m.Type().IsObjectType() && m.Type().HasAttribute("metadata") {
			meta = m.GetAttr("metadata")
		}
	} else if after.IsKnown() && !after.IsNull() && after.Type().IsObjectType() && after.Type().HasAttribute("metadata") {
		md := after.GetAttr("metadata")
		if md.IsKnown() && !md.IsNull() && md.CanIterateElements() && md.LengthInt() == 1 {
			meta = md.Index(cty.NumberIntVal(0))
		}
	}
	if meta == cty.NilVal || !meta.IsKnown() || meta.IsNull() || !meta.Type().IsObjectType() {
		return discovery.PlannedObject{Kind: kind}, true
	}
	name := ctyString(meta, "name")
	namespace := ctyString(meta, "namespace")
	known := kind != "" && name != ""
	if meta.Type().HasAttribute("namespace") {
		if ns := meta.GetAttr("namespace"); !ns.IsKnown() {
			known = false
		}
	}
	return discovery.PlannedObject{Kind: kind, Namespace: namespace, Name: name, Known: known}, true
}

// fieldGranularFixedKind is [kubesweep.FieldGranularFixedKind].
func fieldGranularFixedKind(typeName string) (apiVersion, kind string) {
	return kubesweep.FieldGranularFixedKind(typeName)
}

// envWrite is [discovery.FieldGranularEnvWrite].
func envWrite(after cty.Value, kind string) (kubesweep.FieldWrite, bool) {
	return discovery.FieldGranularEnvWrite(after, kind)
}

// taintWrite is [discovery.FieldGranularTaintWrite].
func taintWrite(after cty.Value) (kubesweep.FieldWrite, bool) {
	return discovery.FieldGranularTaintWrite(after)
}

// ctyString is a known string attribute of obj, or "".
func ctyString(obj cty.Value, name string) string {
	if obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}
