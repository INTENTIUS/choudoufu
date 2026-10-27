// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package planfile

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statefile"
)

// The joint run's per-unit plan files (INTENTIUS/choudoufu#2270, under
// #2213). Each unit's plan file carries the joint plan, and the joint plan
// holds every unit's input variable values. A unit's file leaves out the
// sensitive inputs of the other units, which are put back from their own
// files at apply. These two functions are the plan-file half of that: read
// the variable values a plan file holds, and copy a plan file with its
// variable values edited and every other entry unchanged. Both take the
// unencrypted bytes of a plan file, which is what the joint run writes.
//
// #2277 adds the third: [DropSensitiveValues] copies a plan file with its
// sensitive planned values set to null, for the apply to recompute.

// ReadVariableValues returns the input variable values the plan file in
// raw holds.
func ReadVariableValues(raw []byte) (map[string]plans.DynamicValue, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.Name != tfplanFilename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		plan, err := readPlan(rc)
		if err != nil {
			return nil, err
		}
		return plan.VariableValues, nil
	}
	return nil, fmt.Errorf("the given file is not a valid plan file")
}

// RewriteVariableValues returns a copy of the plan file in raw whose plan
// has its variable values changed by edit. A name edit deletes is dropped
// from the plan's ephemeral set as well. Every other entry of the file is
// copied as it is.
func RewriteVariableValues(raw []byte, edit func(map[string]plans.DynamicValue)) ([]byte, error) {
	return rewritePlanFile(raw, func(plan *plans.Plan, _ map[addrs.Provider]providers.ProviderSchema) error {
		if plan.VariableValues == nil {
			plan.VariableValues = map[string]plans.DynamicValue{}
		}
		before := make(map[string]bool, len(plan.VariableValues))
		for name := range plan.VariableValues {
			before[name] = true
		}
		edit(plan.VariableValues)
		for name := range before {
			if _, ok := plan.VariableValues[name]; !ok {
				delete(plan.EphemeralVariables, name)
			}
		}
		for name := range plan.VariableValues {
			if _, ok := plan.EphemeralVariables[name]; !ok {
				plan.EphemeralVariables[name] = false
			}
		}
		return nil
	}, nil)
}

// rewritePlanFile copies the plan file in raw with its plan edited by
// editPlan and, when editState is not nil, its two state snapshots edited by
// editState. editPlan is given the schemas the file embeds, or nil. Every
// other entry is copied as it is.
func rewritePlanFile(raw []byte, editPlan func(*plans.Plan, map[addrs.Provider]providers.ProviderSchema) error, editState func(*statefile.File) error) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}
	var schemas map[addrs.Provider]providers.ProviderSchema
	for _, f := range zr.File {
		if f.Name != tfschemasFilename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		schemas, err = readSchemas(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	found := false
	for _, f := range zr.File {
		switch {
		case f.Name == tfplanFilename:
			found = true
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			plan, err := readPlan(rc)
			rc.Close()
			if err != nil {
				return nil, err
			}
			if err := editPlan(plan, schemas); err != nil {
				return nil, err
			}
			// writePlan records an ephemeral variable's name only for a name
			// VariableValues holds, and readPlan leaves ephemeral ones out of
			// it, so they are put back here to survive the copy.
			for name, is := range plan.EphemeralVariables {
				if _, ok := plan.VariableValues[name]; is && !ok {
					if plan.VariableValues == nil {
						plan.VariableValues = map[string]plans.DynamicValue{}
					}
					plan.VariableValues[name] = nil
				}
			}
			if err := writePlanEntry(zw, plan); err != nil {
				return nil, err
			}
		case editState != nil && (f.Name == tfstateFilename || f.Name == tfstatePreviousFilename):
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			sf, err := statefile.Read(rc, encryption.StateEncryptionDisabled())
			rc.Close()
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", f.Name, err)
			}
			if err := editState(sf); err != nil {
				return nil, err
			}
			if err := writeStateFileEntry(zw, f.Name, sf); err != nil {
				return nil, err
			}
		default:
			if err := zw.Copy(f); err != nil {
				return nil, err
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("the given file is not a valid plan file")
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DroppedValue is one sensitive planned value [DropSensitiveValues] left
// out of a plan file (INTENTIUS/choudoufu#2277).
type DroppedValue struct {
	// Address is the change's address as [ChangeAddress] renders it, or the
	// output value's address when Output is set.
	Address string
	// Module is the first module call of the address, "" for the root
	// module. In a joint plan it is the unit.
	Module string
	Output bool
	// Side is "before" or "after".
	Side string
	// Path is where in that side the value was, nil for the whole side.
	Path cty.Path
	// Value is the value dropped, without marks. It may hold unknowns.
	// cty.NilVal when Raw is set.
	Value cty.Value
	// Raw is set when the file carries no schema to decode the change with:
	// the whole side was dropped, and Raw is its encoding.
	Raw []byte
}

// ChangeAddress is the address a resource instance change is known by in
// a [DroppedValue]: its instance address, and its deposed key if it has one.
func ChangeAddress(change *plans.ResourceInstanceChangeSrc) string {
	addr := change.Addr.String()
	if change.DeposedKey != states.NotDeposed {
		addr += " (deposed " + string(change.DeposedKey) + ")"
	}
	return addr
}

// SensitivePaths are the paths of marks a mark sensitive.
func SensitivePaths(pvms []cty.PathValueMarks) []cty.Path {
	var out []cty.Path
	for _, pvm := range pvms {
		if _, ok := pvm.Marks[marks.Sensitive]; ok {
			out = append(out, pvm.Path)
		}
	}
	return out
}

// DropSensitiveValues returns a copy of the plan file in raw with every
// sensitive value of its planned changes set to null (#2277): each resource
// change's before and after values at the paths marked sensitive, each
// sensitive output change, the same in the plan's drift, and the sensitive
// attributes and outputs of its two state snapshots. Each dropped value of
// a resource or output change that is not null is passed to keep, which
// can record what is needed to check it again; drift and the state
// snapshots are not passed, since an apply compares neither. A change whose
// schema the file does not carry has its whole side dropped (Raw).
//
// The marks stay, so a reader still sees which values are sensitive. The
// plan's input variable values are not changed (see
// [RewriteVariableValues]).
func DropSensitiveValues(raw []byte, keep func(DroppedValue)) ([]byte, error) {
	if keep == nil {
		keep = func(DroppedValue) {}
	}
	return rewritePlanFile(raw, func(plan *plans.Plan, schemas map[addrs.Provider]providers.ProviderSchema) error {
		if plan.Changes == nil {
			return nil
		}
		for _, rc := range plan.Changes.Resources {
			if err := dropResourceChange(rc, schemas, keep); err != nil {
				return err
			}
		}
		for _, rc := range plan.DriftedResources {
			if err := dropResourceChange(rc, schemas, func(DroppedValue) {}); err != nil {
				return err
			}
		}
		for _, oc := range plan.Changes.Outputs {
			if err := dropOutputChange(oc, keep); err != nil {
				return err
			}
		}
		return nil
	}, dropStateSensitive)
}

func moduleOf(addr addrs.ModuleInstance) string {
	if len(addr) == 0 {
		return ""
	}
	return addr[0].Name
}

func dropResourceChange(rc *plans.ResourceInstanceChangeSrc, schemas map[addrs.Provider]providers.ProviderSchema, keep func(DroppedValue)) error {
	if rc == nil {
		return nil
	}
	var schema *providers.Schema
	if ps, ok := schemas[rc.ProviderAddr.Provider]; ok {
		schema, _ = ps.SchemaForResourceType(rc.Addr.Resource.Resource.Mode, rc.Addr.Resource.Resource.Type)
	}
	addr, mod := ChangeAddress(rc), moduleOf(rc.Addr.Module)
	for _, side := range []struct {
		name string
		val  *plans.DynamicValue
		vms  []cty.PathValueMarks
	}{{"before", &rc.Before, rc.BeforeValMarks}, {"after", &rc.After, rc.AfterValMarks}} {
		paths := SensitivePaths(side.vms)
		if len(paths) == 0 || len(*side.val) == 0 {
			continue
		}
		dv := DroppedValue{Address: addr, Module: mod, Side: side.name}
		dropped, err := dropPaths(*side.val, schema, paths, func(p cty.Path, v cty.Value) {
			d := dv
			d.Path, d.Value = p, v
			keep(d)
		})
		if err != nil {
			// No schema, or a path the value does not have: the whole side
			// goes.
			d := dv
			d.Raw = append([]byte(nil), (*side.val)...)
			keep(d)
			null, nerr := plans.NewDynamicValue(cty.NullVal(cty.DynamicPseudoType), cty.DynamicPseudoType)
			if nerr != nil {
				return nerr
			}
			*side.val = null
			continue
		}
		*side.val = dropped
	}
	return nil
}

// dropPaths decodes val with schema's type, sets each of paths in it to
// null, passing each value it replaced that is not null to kept, and
// encodes it again.
func dropPaths(val plans.DynamicValue, schema *providers.Schema, paths []cty.Path, kept func(cty.Path, cty.Value)) (plans.DynamicValue, error) {
	if schema == nil || schema.Block == nil {
		return nil, fmt.Errorf("no schema")
	}
	ty := schema.Block.ImpliedType()
	v, err := val.Decode(ty)
	if err != nil {
		return nil, err
	}
	// A path under one already dropped is covered by it.
	sort.SliceStable(paths, func(i, j int) bool { return len(paths[i]) < len(paths[j]) })
	var done []cty.Path
	covered := func(p cty.Path) bool {
		for _, d := range done {
			if len(d) <= len(p) && d.Equals(p[:len(d)]) {
				return true
			}
		}
		return false
	}
	for _, p := range paths {
		if covered(p) {
			continue
		}
		done = append(done, p)
		if len(p) == 0 {
			if !v.IsNull() {
				kept(p, v)
			}
			v = cty.NullVal(ty)
			continue
		}
		at, err := p.Apply(v)
		if err != nil {
			return nil, err
		}
		if at.IsNull() {
			continue
		}
		kept(p, at)
		if v, err = SetPath(v, p, cty.NullVal(at.Type())); err != nil {
			return nil, err
		}
	}
	return plans.NewDynamicValue(v, ty)
}

// SetPath returns v with the value at path replaced by to.
func SetPath(v cty.Value, path cty.Path, to cty.Value) (cty.Value, error) {
	if len(path) == 0 {
		return to, nil
	}
	return cty.Transform(v, func(p cty.Path, at cty.Value) (cty.Value, error) {
		if p.Equals(path) {
			return to, nil
		}
		return at, nil
	})
}

func dropOutputChange(oc *plans.OutputChangeSrc, keep func(DroppedValue)) error {
	if oc == nil {
		return nil
	}
	if !oc.Sensitive && len(SensitivePaths(oc.BeforeValMarks)) == 0 && len(SensitivePaths(oc.AfterValMarks)) == 0 {
		return nil
	}
	addr := oc.Addr.String()
	mod := moduleOf(oc.Addr.Module)
	for _, side := range []struct {
		name string
		val  *plans.DynamicValue
	}{{"before", &oc.Before}, {"after", &oc.After}} {
		if len(*side.val) == 0 {
			continue
		}
		v, err := side.val.Decode(cty.DynamicPseudoType)
		if err != nil {
			return fmt.Errorf("decoding the %s value of %s: %w", side.name, addr, err)
		}
		if v.IsNull() {
			continue
		}
		keep(DroppedValue{Address: addr, Module: mod, Output: true, Side: side.name, Value: v})
		null, err := plans.NewDynamicValue(cty.NullVal(cty.DynamicPseudoType), cty.DynamicPseudoType)
		if err != nil {
			return err
		}
		*side.val = null
	}
	return nil
}

// dropStateSensitive sets the sensitive attributes and outputs of a state
// snapshot to null. Attributes are edited in their JSON form, so no schema
// is needed.
func dropStateSensitive(sf *statefile.File) error {
	if sf == nil || sf.State == nil {
		return nil
	}
	for _, mod := range sf.State.Modules {
		for _, rs := range mod.Resources {
			for _, is := range rs.Instances {
				objs := []*states.ResourceInstanceObjectSrc{is.Current}
				for _, d := range is.Deposed {
					objs = append(objs, d)
				}
				for _, o := range objs {
					if err := dropObjectSensitive(o); err != nil {
						return fmt.Errorf("%s: %w", rs.Addr, err)
					}
				}
			}
		}
		for _, ov := range mod.OutputValues {
			if ov != nil && ov.Sensitive && ov.Value != cty.NilVal {
				ov.Value = cty.NullVal(ov.Value.Type())
			}
		}
	}
	return nil
}

func dropObjectSensitive(o *states.ResourceInstanceObjectSrc) error {
	if o == nil || len(o.AttrsJSON) == 0 {
		return nil
	}
	paths := SensitivePaths(o.AttrSensitivePaths)
	if len(paths) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(o.AttrsJSON))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	for _, p := range paths {
		var ok bool
		if v, ok = dropJSONPath(v, p); !ok && len(p) > 0 {
			// A path the JSON does not follow: the top-level attribute goes.
			if ga, isAttr := p[0].(cty.GetAttrStep); isAttr {
				if m, isMap := v.(map[string]any); isMap {
					m[ga.Name] = nil
				}
			}
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o.AttrsJSON = b
	return nil
}

// dropJSONPath sets the value at path in a decoded JSON value to null, and
// reports whether the path was found.
func dropJSONPath(v any, path cty.Path) (any, bool) {
	if len(path) == 0 {
		return nil, true
	}
	switch step := path[0].(type) {
	case cty.GetAttrStep:
		m, ok := v.(map[string]any)
		if !ok {
			return v, false
		}
		child, ok := m[step.Name]
		if !ok {
			return v, false
		}
		nv, found := dropJSONPath(child, path[1:])
		m[step.Name] = nv
		return m, found
	case cty.IndexStep:
		k := step.Key
		if !k.IsKnown() || k.IsNull() {
			return v, false
		}
		switch k.Type() {
		case cty.String:
			m, ok := v.(map[string]any)
			if !ok {
				return v, false
			}
			child, ok := m[k.AsString()]
			if !ok {
				return v, false
			}
			nv, found := dropJSONPath(child, path[1:])
			m[k.AsString()] = nv
			return m, found
		case cty.Number:
			list, ok := v.([]any)
			if !ok {
				return v, false
			}
			i, acc := k.AsBigFloat().Int64()
			if acc != big.Exact || i < 0 || int(i) >= len(list) {
				return v, false
			}
			nv, found := dropJSONPath(list[i], path[1:])
			list[i] = nv
			return list, found
		}
	}
	return v, false
}
