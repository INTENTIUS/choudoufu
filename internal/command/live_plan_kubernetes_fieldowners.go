// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
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

// kubernetesFieldOwners judges every planned create or update of a
// field-granular instance, per cluster, and returns the refusals and
// warnings discovery.CheckKubernetesFieldOwners and
// discovery.SameObjectFieldWrites raise. estate "" (a run with no estate
// name has stamped nothing) checks nothing.
func kubernetesFieldOwners(ctx context.Context, sweepers map[string]kubesweep.Sweeper, config *configs.Config, plan *plans.Plan, schemas *tofu.Schemas, estate string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if estate == "" || plan == nil || plan.Changes == nil || schemas == nil {
		return diags
	}
	byProvider := map[string][]discovery.FieldGranularWrite{}
	var keys []string
	for _, rc := range plan.Changes.Resources {
		if rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if rc.Action != plans.Create && rc.Action != plans.Update && rc.Action != plans.CreateThenDelete && rc.Action != plans.DeleteThenCreate {
			continue
		}
		schema, _ := schemas.ResourceTypeConfig(rc.ProviderAddr.Provider, rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
		if schema == nil {
			continue
		}
		if _, ok := substrate.FieldGranularShape(schema.Block); !ok {
			continue
		}
		w, ok := plannedFieldGranularWrite(rc, schema)
		if !ok {
			continue
		}
		key := providerCacheKey(rc.ProviderAddr)
		if _, seen := byProvider[key]; !seen {
			keys = append(keys, key)
		}
		byProvider[key] = append(byProvider[key], w)
	}
	sort.Strings(keys)
	for _, key := range keys {
		diags = diags.Append(discovery.SameObjectFieldWrites(config, estate, byProvider[key]))
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
	w := discovery.FieldGranularWrite{Addr: rc.Addr}
	change, err := rc.Decode(schema)
	if err != nil {
		return w, false
	}
	after, _ := change.After.UnmarkDeep()
	if after.IsNull() || !after.IsKnown() || !after.Type().IsObjectType() {
		return w, false
	}

	meta, ok := singleBlock(after, "metadata")
	if !ok {
		return w, false
	}
	w.Object.Name = ctyString(meta, "name")
	w.Object.Namespace = ctyString(meta, "namespace")
	if w.Object.Name == "" {
		return w, false
	}
	if substrate.FieldGranularNamesKind(schema.Block) {
		w.Object.APIVersion = ctyString(after, "api_version")
		w.Object.Kind = ctyString(after, "kind")
	} else {
		w.Object.APIVersion, w.Object.Kind = fieldGranularFixedKind(rc.Addr.Resource.Resource.Type)
	}
	if w.Object.APIVersion == "" || w.Object.Kind == "" {
		return w, false
	}
	if after.Type().HasAttribute(substrate.FieldForceAttr) {
		if f := after.GetAttr(substrate.FieldForceAttr); f.IsKnown() && !f.IsNull() && f.Type() == cty.Bool {
			w.Force = f.True()
		}
	}

	for _, m := range []struct {
		attr string
		root []string
	}{
		{"labels", []string{"f:metadata", "f:labels"}},
		{"annotations", []string{"f:metadata", "f:annotations"}},
		{"template_annotations", []string{"f:spec", "f:template", "f:metadata", "f:annotations"}},
		{"data", []string{"f:data"}},
	} {
		attr, has := schema.Block.Attributes[m.attr]
		if !has || attr == nil || !attr.Type.IsMapType() {
			continue
		}
		keys, ok := mapKeys(after, m.attr)
		if !ok {
			return w, false
		}
		if len(keys) == 0 {
			continue
		}
		write := kubesweep.FieldWrite{Root: m.root}
		for _, k := range keys {
			write.Members = append(write.Members, kubesweep.MapMember(k))
		}
		w.Writes = append(w.Writes, write)
	}

	if _, has := schema.Block.BlockTypes["env"]; has {
		write, ok := envWrite(after, w.Object.Kind)
		if !ok {
			return w, false
		}
		w.Writes = append(w.Writes, write)
	}
	if _, has := schema.Block.BlockTypes["taint"]; has {
		write, ok := taintWrite(after)
		if !ok {
			return w, false
		}
		w.Writes = append(w.Writes, write)
	}
	return w, true
}

// fieldGranularFixedKind is the object a field-granular type that names no
// kind patches. hashicorp/kubernetes hardcodes it per type, and names each
// such type after the patched kind's own type plus the field it writes:
// kubernetes_config_map_v1_data patches what kubernetes_config_map_v1
// manages, kubernetes_node_taint what kubernetes_node would. So the last
// segment is dropped and the kind is [kubesweep.KindOfType]'s, the same
// join the sweep makes. Every kind reached this way at 3.2.1 (ConfigMap,
// Secret, Node) is in the core group, whose API version is "v1".
func fieldGranularFixedKind(typeName string) (apiVersion, kind string) {
	i := strings.LastIndex(typeName, "_")
	if i <= 0 {
		return "", ""
	}
	kind, _, ok := kubesweep.KindOfType(typeName[:i])
	if !ok {
		return "", ""
	}
	return "v1", kind
}

// envWrite is one container's env: the container named by container (or
// init_container), keyed in managedFields by name, under the pod spec the
// kind keeps it in.
func envWrite(after cty.Value, kind string) (kubesweep.FieldWrite, bool) {
	list := "f:containers"
	container := ctyString(after, "container")
	if container == "" {
		list = "f:initContainers"
		container = ctyString(after, "init_container")
	}
	if container == "" {
		return kubesweep.FieldWrite{}, false
	}
	var podSpec []string
	switch kind {
	case "Pod":
		podSpec = []string{"f:spec"}
	case "CronJob":
		podSpec = []string{"f:spec", "f:jobTemplate", "f:spec", "f:template", "f:spec"}
	default:
		podSpec = []string{"f:spec", "f:template", "f:spec"}
	}
	root := append(podSpec, list, kubesweep.ListItemMember(map[string]string{"name": container}), "f:env")
	write := kubesweep.FieldWrite{Root: root}
	envs := after.GetAttr("env")
	if envs.IsNull() || !envs.IsKnown() || !envs.CanIterateElements() {
		return write, false
	}
	for it := envs.ElementIterator(); it.Next(); {
		_, e := it.Element()
		name := ctyString(e, "name")
		if name == "" {
			return write, false
		}
		write.Members = append(write.Members, kubesweep.ListItemMember(map[string]string{"name": name}))
	}
	return write, true
}

// taintWrite is the node's taints, each keyed in managedFields by its key
// and effect. A cluster that keeps spec.taints atomic records it as one
// leaf, which [kubesweep.FieldOwners] reports as owned whole.
func taintWrite(after cty.Value) (kubesweep.FieldWrite, bool) {
	write := kubesweep.FieldWrite{Root: []string{"f:spec", "f:taints"}}
	taints := after.GetAttr("taint")
	if taints.IsNull() || !taints.IsKnown() || !taints.CanIterateElements() {
		return write, false
	}
	for it := taints.ElementIterator(); it.Next(); {
		_, t := it.Element()
		key, effect := ctyString(t, "key"), ctyString(t, "effect")
		if key == "" || effect == "" {
			return write, false
		}
		write.Members = append(write.Members, kubesweep.ListItemMember(map[string]string{"key": key, "effect": effect}))
	}
	return write, true
}

// singleBlock is the one element of a list block of at most one.
func singleBlock(obj cty.Value, name string) (cty.Value, bool) {
	if !obj.Type().HasAttribute(name) {
		return cty.NilVal, false
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() || v.LengthInt() != 1 {
		return cty.NilVal, false
	}
	it := v.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() {
		return cty.NilVal, false
	}
	return elem, true
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

// mapKeys is the sorted keys of a map attribute; ok is false when the map
// is not known yet.
func mapKeys(obj cty.Value, name string) ([]string, bool) {
	v := obj.GetAttr(name)
	if v.IsNull() {
		return nil, true
	}
	if !v.IsKnown() || !v.CanIterateElements() {
		return nil, false
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		if !k.IsKnown() || k.IsNull() {
			return nil, false
		}
		out = append(out, k.AsString())
	}
	sort.Strings(out)
	return out, true
}
