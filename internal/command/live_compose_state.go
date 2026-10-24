// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/states"
)

// The helpers here move resources between an estate's own address space,
// where it is a root, and the composed walk's, where it is module.<call>
// (GitHub issue #2059). Nothing is copied that the move does not change:
// resource instance objects are shared, except where they carry a module
// path of their own (their dependencies).

// stripCall removes the module call's step from addr. ok is false when addr
// is not inside that call.
func stripCall(addr addrs.AbsResourceInstance, call string) (addrs.AbsResourceInstance, bool) {
	if len(addr.Module) == 0 || addr.Module[0].Name != call || addr.Module[0].InstanceKey != addrs.NoKey {
		return addr, false
	}
	out := addr
	out.Module = append(addrs.ModuleInstance(nil), addr.Module[1:]...)
	return out, true
}

func sliceAddrs(in []addrs.AbsResourceInstance, call string) []addrs.AbsResourceInstance {
	var out []addrs.AbsResourceInstance
	for _, a := range in {
		if rel, ok := stripCall(a, call); ok {
			out = append(out, rel)
		}
	}
	return out
}

func sliceDeposed(in []projection.DeposedDestroy, call string) []projection.DeposedDestroy {
	var out []projection.DeposedDestroy
	for _, d := range in {
		if rel, ok := stripCall(d.Addr, call); ok {
			out = append(out, projection.DeposedDestroy{Addr: rel, Key: d.Key})
		}
	}
	return out
}

// mapState builds a copy of in with every module and module path mapped
// through f. A module f drops is left out.
func mapState(in *states.State, f func(addrs.ModuleInstance) (addrs.ModuleInstance, bool)) *states.State {
	out := states.NewState()
	if in == nil {
		return out
	}
	mapMod := func(m addrs.Module) (addrs.Module, bool) {
		mi := m.UnkeyedInstanceShim()
		r, ok := f(mi)
		if !ok {
			return nil, false
		}
		return r.Module(), true
	}
	for _, ms := range in.Modules {
		na, ok := f(ms.Addr)
		if !ok {
			continue
		}
		nm := out.EnsureModule(na)
		for k, rs := range ms.Resources {
			nr := &states.Resource{
				Addr:           rs.Addr,
				Instances:      make(map[addrs.InstanceKey]*states.ResourceInstance, len(rs.Instances)),
				ProviderConfig: rs.ProviderConfig,
			}
			nr.Addr.Module = na
			if pm, ok := mapMod(rs.ProviderConfig.Module); ok {
				nr.ProviderConfig.Module = pm
			}
			for ik, ri := range rs.Instances {
				ni := &states.ResourceInstance{ProviderKey: ri.ProviderKey}
				ni.Current = mapObject(ri.Current, mapMod)
				if len(ri.Deposed) > 0 {
					ni.Deposed = make(map[states.DeposedKey]*states.ResourceInstanceObjectSrc, len(ri.Deposed))
					for dk, o := range ri.Deposed {
						ni.Deposed[dk] = mapObject(o, mapMod)
					}
				}
				nr.Instances[ik] = ni
			}
			nm.Resources[k] = nr
		}
		for k, ov := range ms.OutputValues {
			nov := *ov
			nov.Addr.Module = na
			nm.OutputValues[k] = &nov
		}
		for k, v := range ms.LocalValues {
			nm.LocalValues[k] = v
		}
	}
	return out
}

func mapObject(o *states.ResourceInstanceObjectSrc, mapMod func(addrs.Module) (addrs.Module, bool)) *states.ResourceInstanceObjectSrc {
	if o == nil {
		return nil
	}
	n := *o
	if len(o.Dependencies) > 0 {
		n.Dependencies = nil
		for _, d := range o.Dependencies {
			if pm, ok := mapMod(d.Module); ok {
				d.Module = pm
				n.Dependencies = append(n.Dependencies, d)
			}
		}
	}
	return &n
}

// rehomeState moves an estate's own state under module.<call>.
func rehomeState(in *states.State, call string) *states.State {
	step := addrs.ModuleInstanceStep{Name: call}
	return mapState(in, func(m addrs.ModuleInstance) (addrs.ModuleInstance, bool) {
		return append(addrs.ModuleInstance{step}, m...), true
	})
}

// sliceState is rehomeState's inverse: the part of a composed state that is
// module.<call>, as that estate's own root state. The module call's own
// output values become the estate's root outputs, which are what the estate
// records for other estates to read.
func sliceState(in *states.State, call string) *states.State {
	out := mapState(in, func(m addrs.ModuleInstance) (addrs.ModuleInstance, bool) {
		if len(m) == 0 || m[0].Name != call || m[0].InstanceKey != addrs.NoKey {
			return nil, false
		}
		return append(addrs.ModuleInstance(nil), m[1:]...), true
	})
	return out
}

// mergeInto adds src's modules to dst.
func mergeInto(dst, src *states.State) {
	for _, ms := range src.Modules {
		if len(ms.Addr) == 0 && len(ms.Resources) == 0 && len(ms.OutputValues) == 0 {
			continue
		}
		dm := dst.EnsureModule(ms.Addr)
		for k, r := range ms.Resources {
			dm.Resources[k] = r
		}
		for k, o := range ms.OutputValues {
			dm.OutputValues[k] = o
		}
		for k, v := range ms.LocalValues {
			dm.LocalValues[k] = v
		}
	}
}
