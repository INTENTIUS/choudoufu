// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/substrate"
)

// FieldGranularWriteOf reads one field-granular value - a planned After,
// or a stock state file's recorded object (GitHub issue #1863's
// live-import) - as the object it patches and the fields it writes there,
// read off the schema's own attributes: a labels map writes
// metadata.labels, an annotations map metadata.annotations,
// template_annotations the pod template's, a data map the object's data,
// an env block one container's env, a taint block the node's taints. The
// returned write's Addr is unset; the caller names the instance. ok is
// false when the value does not say yet - an unknown name or key - or
// names no object.
func FieldGranularWriteOf(typeName string, block *configschema.Block, after cty.Value) (FieldGranularWrite, bool) {
	var w FieldGranularWrite
	after, _ = after.UnmarkDeep()
	if after.IsNull() || !after.IsKnown() || !after.Type().IsObjectType() {
		return w, false
	}

	meta, ok := fgSingleBlock(after, "metadata")
	if !ok {
		return w, false
	}
	w.Object.Name = fgString(meta, "name")
	w.Object.Namespace = fgString(meta, "namespace")
	if w.Object.Name == "" {
		return w, false
	}
	if substrate.FieldGranularNamesKind(block) {
		w.Object.APIVersion = fgString(after, "api_version")
		w.Object.Kind = fgString(after, "kind")
	} else {
		w.Object.APIVersion, w.Object.Kind = kubesweep.FieldGranularFixedKind(typeName)
	}
	if w.Object.APIVersion == "" || w.Object.Kind == "" {
		return w, false
	}
	if after.Type().HasAttribute(substrate.FieldForceAttr) {
		if f, _ := after.GetAttr(substrate.FieldForceAttr).Unmark(); f.IsKnown() && !f.IsNull() && f.Type() == cty.Bool {
			w.Force = f.True()
		}
	}

	for _, name := range kubesweep.FieldGranularMapAttrs {
		root, _ := kubesweep.FieldGranularMapRoot(name)
		attr, has := block.Attributes[name]
		if !has || attr == nil || !attr.Type.IsMapType() {
			continue
		}
		keys, ok := fgMapKeys(after, name)
		if !ok {
			return w, false
		}
		if len(keys) == 0 {
			continue
		}
		write := kubesweep.FieldWrite{Root: root}
		for _, k := range keys {
			write.Members = append(write.Members, kubesweep.MapMember(k))
		}
		w.Writes = append(w.Writes, write)
	}

	if _, has := block.BlockTypes["env"]; has {
		write, ok := FieldGranularEnvWrite(after, w.Object.Kind)
		if !ok {
			return w, false
		}
		w.Writes = append(w.Writes, write)
	}
	if _, has := block.BlockTypes["taint"]; has {
		write, ok := FieldGranularTaintWrite(after)
		if !ok {
			return w, false
		}
		w.Writes = append(w.Writes, write)
	}
	return w, true
}

// FieldGranularEnvWrite is one container's env: the container named by container (or
// init_container), keyed in managedFields by name, under the pod spec the
// kind keeps it in.
func FieldGranularEnvWrite(after cty.Value, kind string) (kubesweep.FieldWrite, bool) {
	init := false
	container := fgString(after, "container")
	if container == "" {
		init = true
		container = fgString(after, "init_container")
	}
	if container == "" {
		return kubesweep.FieldWrite{}, false
	}
	root := kubesweep.EnvRoot(kind, container, init)
	write := kubesweep.FieldWrite{Root: root}
	envs := after.GetAttr("env")
	if envs.IsMarked() || envs.IsNull() || !envs.IsKnown() || !envs.CanIterateElements() {
		return write, false
	}
	for it := envs.ElementIterator(); it.Next(); {
		_, e := it.Element()
		name := fgString(e, "name")
		if name == "" {
			return write, false
		}
		write.Members = append(write.Members, kubesweep.ListItemMember(map[string]string{"name": name}))
	}
	return write, true
}

// FieldGranularTaintWrite is the node's taints, each keyed in managedFields by its key
// and effect. A cluster that keeps spec.taints atomic records it as one
// leaf, which [kubesweep.FieldOwners] reports as owned whole.
func FieldGranularTaintWrite(after cty.Value) (kubesweep.FieldWrite, bool) {
	write := kubesweep.FieldWrite{Root: kubesweep.TaintsRoot, Atomic: true}
	taints := after.GetAttr("taint")
	if taints.IsMarked() || taints.IsNull() || !taints.IsKnown() || !taints.CanIterateElements() {
		return write, false
	}
	for it := taints.ElementIterator(); it.Next(); {
		_, t := it.Element()
		key, effect := fgString(t, "key"), fgString(t, "effect")
		if key == "" || effect == "" {
			return write, false
		}
		write.Members = append(write.Members, kubesweep.ListItemMember(map[string]string{"key": key, "effect": effect}))
	}
	return write, true
}

// fgSingleBlock is the one element of a list block of at most one.
func fgSingleBlock(obj cty.Value, name string) (cty.Value, bool) {
	if !obj.Type().HasAttribute(name) {
		return cty.NilVal, false
	}
	v := obj.GetAttr(name)
	if v.IsMarked() || v.IsNull() || !v.IsKnown() || !v.CanIterateElements() || v.LengthInt() != 1 {
		return cty.NilVal, false
	}
	it := v.ElementIterator()
	it.Next()
	_, elem := it.Element()
	if elem.IsMarked() || elem.IsNull() || !elem.IsKnown() || !elem.Type().IsObjectType() {
		return cty.NilVal, false
	}
	return elem, true
}

// fgString is a known string attribute of obj, or "".
func fgString(obj cty.Value, name string) string {
	if obj.IsMarked() || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(name) {
		return ""
	}
	v := obj.GetAttr(name)
	if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// fgMapKeys is the sorted keys of a map attribute; ok is false when the map
// is not known yet.
func fgMapKeys(obj cty.Value, name string) ([]string, bool) {
	v := obj.GetAttr(name)
	if v.IsMarked() {
		// The written fields are unmarked where this file takes the value
		// in ([FieldGranularWriteOf]); a marked map here is refused rather
		// than read.
		return nil, false
	}
	if v.IsNull() {
		return nil, true
	}
	if !v.IsKnown() || !v.CanIterateElements() {
		return nil, false
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		k, _ := it.Element()
		if k.IsMarked() || !k.IsKnown() || k.IsNull() {
			return nil, false
		}
		out = append(out, k.AsString())
	}
	sort.Strings(out)
	return out, true
}
