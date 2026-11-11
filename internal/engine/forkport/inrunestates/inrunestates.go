// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package inrunestates is a choudoufu fork port (#2370, unit U9 of #2256):
// in-run estates and the fork's builtin terraform_estate_outputs data source
// on OpenTofu's new runtime (internal/engine).
//
// Upstream's new runtime cannot read a data source yet: its planning engine
// reads every data block during the plan, never defers one to the apply, and
// keeps no result in the refreshed state, and its apply engine's DataRead is
// unimplemented. Upstream's own data-resource work is `opentofu/opentofu#4058`.
// Until it lands this package carries just enough for the one data source
// choudoufu owns:
//
//   - a terraform_estate_outputs block naming an estate the run applies as a
//     module (internal/tofu's ContextOpts.InRunEstates, GitHub issue #2059)
//     is answered from that module's outputs, never from the record store,
//     and is deferred to the apply while the module has changes pending or
//     an output it reads is not yet known;
//   - every other terraform_estate_outputs block is read from the provider
//     during the plan, as upstream reads any data block, and its result is
//     kept in the refreshed state so the apply can read it back.
//
// It is the whole port. The engine calls it from a handful of sites, each
// commented "choudoufu fork port (#2370): remove when upstream #4058 lands".
// Removing the port is deleting this package, those call sites and the
// "in-run-estates" row of internal/live/newruntime/gaps.json.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), [New] returns nil and the gap table's preflight
// refuses these blocks again, under its "data-sources" row.
package inrunestates

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TypeName is the builtin terraform provider's cross-estate output read. The
// name is repeated here, not imported, as internal/tofu repeats it: the
// provider package sits above both. internal/tofu's tests check the copies.
const TypeName = "terraform_estate_outputs"

// Name is the port's name in the internal/engine/forkport registry.
const Name = "inrunestates"

// Estates maps an estate name to the module call that carries it in a
// composed run. It is internal/tofu's ContextOpts.InRunEstates.
type Estates map[string]addrs.Module

// Port is the port's state for one plan or apply. A nil *Port is off.
type Port struct {
	Estates Estates
}

// New returns the port for a run with these in-run estates, or nil when the
// port is switched off.
func New(estates map[string]addrs.Module) *Port {
	if !forkport.Enabled(Name) {
		return nil
	}
	return &Port{Estates: estates}
}

// Covers reports whether the port handles this data block: a
// terraform_estate_outputs block of the builtin terraform provider. The gap
// table's preflight lets exactly these through while the port is on.
func Covers(r *configs.Resource) bool {
	if r == nil || r.Mode != addrs.DataResourceMode || r.Type != TypeName {
		return false
	}
	provider := r.Provider
	if provider.IsZero() {
		provider = addrs.ImpliedProviderForUnqualifiedType(r.Addr().ImpliedProvider())
	}
	return provider == addrs.NewBuiltInProvider("terraform")
}

// IsEstateOutputs reports whether addr is a terraform_estate_outputs block.
func IsEstateOutputs(addr addrs.AbsResourceInstance) bool {
	return addr.Resource.Resource.Mode == addrs.DataResourceMode && addr.Resource.Resource.Type == TypeName
}

// Target reports the module an in-run read of addr is answered from, whether
// it is an in-run read at all, and whether it names the estate it is in.
func (p *Port) Target(addr addrs.AbsResourceInstance, configVal cty.Value) (target addrs.Module, inRun, self bool) {
	if p == nil || len(p.Estates) == 0 || !IsEstateOutputs(addr) {
		return nil, false, false
	}
	if configVal == cty.NilVal || !configVal.IsKnown() || configVal.IsNull() || !configVal.Type().IsObjectType() || !configVal.Type().HasAttribute("estate") {
		return nil, false, false
	}
	est := configVal.GetAttr("estate")
	if !est.IsKnown() || est.IsNull() || est.IsMarked() {
		return nil, false, false
	}
	target, ok := p.Estates[est.AsString()]
	if !ok {
		return nil, false, false
	}
	self = len(addr.Module) > 0 && len(target) > 0 && addr.Module[0].Name == target[0]
	return target, true, self
}

// Value builds the data source's object from the outputs of the module an
// in-run read names, as the evaluator holds them: an object with one
// attribute per output. The second result is false when an output read is
// not yet wholly known. The refusals are the default runtime's, word for
// word (internal/tofu's inRunEstateValue).
func Value(target addrs.Module, self bool, configVal cty.Value, outputs cty.Value) (cty.Value, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	estate := configVal.GetAttr("estate").AsString()
	if self {
		return cty.NilVal, false, diags.Append(tfdiags.Sourceless(tfdiags.Error, "An estate cannot read its own outputs this way",
			fmt.Sprintf("data \"terraform_estate_outputs\" names estate %q, which is the estate this block is in. Its outputs are root outputs of its own configuration; read them directly.", estate)))
	}
	namesVal := configVal.GetAttr("names")
	if !namesVal.IsWhollyKnown() || namesVal.IsNull() {
		return cty.NilVal, false, diags
	}
	var names []string
	for it := namesVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		names = append(names, v.AsString())
	}
	sort.Strings(names)
	outputsKnown := outputs != cty.NilVal && outputs.IsKnown() && !outputs.IsNull() && outputs.Type().IsObjectType()
	attrs := map[string]cty.Value{}
	known := true
	for _, name := range names {
		if !outputsKnown {
			// The module's outputs are not known as a whole yet, which
			// happens only while the evaluator has not settled them.
			attrs[name] = cty.DynamicVal
			known = false
			continue
		}
		if !outputs.Type().HasAttribute(name) {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Another estate has not recorded this output",
				fmt.Sprintf("data \"terraform_estate_outputs\" reads output %q of estate %q, which this run applies as module %s, and that module declares no such output.", name, estate, target)))
			continue
		}
		v := outputs.GetAttr(name)
		if marks.Contains(v, marks.Sensitive) {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Another estate has not recorded this output",
				fmt.Sprintf("Output %q of estate %q is sensitive. Only non-sensitive outputs cross between estates, in a composed run as in separate ones.", name, estate)))
			continue
		}
		v, _ = v.UnmarkDeep()
		if !v.IsWhollyKnown() {
			known = false
		}
		attrs[name] = v
	}
	if diags.HasErrors() {
		return cty.NilVal, false, diags
	}
	return cty.ObjectVal(map[string]cty.Value{
		"estate": configVal.GetAttr("estate"),
		"names":  namesVal,
		"values": cty.ObjectVal(attrs),
	}), known, diags
}

// Module reports what planning knows of the module an in-run read names.
type Module struct {
	// Outputs is the module's outputs object as the evaluator holds it.
	Outputs cty.Value
	// Resources are every resource instance the module declares, each
	// already planned.
	Resources []addrs.AbsResourceInstance
	// Pending is true when any object under the module has a change
	// planned: a desired instance whose action is not no-op, or an object
	// in the prior state the module no longer declares.
	Pending bool
}

// PlanHost is what the planning engine gives the port.
type PlanHost interface {
	// InRunModule evaluates target's outputs and plans every resource
	// instance under it, then reports both.
	InRunModule(target addrs.ModuleInstance) Module
}

// Planned is the port's answer for one terraform_estate_outputs instance.
type Planned struct {
	// Value is the data source's object: the result when the read happened
	// during the plan, or a placeholder for a deferred read, with each
	// output the evaluator already knows filled in so that a dependent of
	// a known output is not replaced.
	Value cty.Value
	// Deferred is true when the read waits for the apply.
	Deferred bool
	// ConfigUnknown is true when it waits because its own configuration is
	// not yet known, rather than for the module it reads.
	ConfigUnknown bool
	// Dependencies are the module's resource instances. The read waits for
	// every one of them that changes, and each is kept in the execution
	// graph so the apply can evaluate the module's outputs.
	Dependencies []addrs.AbsResourceInstance
}

// Plan plans one terraform_estate_outputs instance. handled is false for a
// block that is not an in-run read: the engine then reads it from the
// provider as upstream reads any data block.
func (p *Port) Plan(addr addrs.AbsResourceInstance, configVal cty.Value, schemaType cty.Type, host PlanHost) (planned Planned, handled bool, diags tfdiags.Diagnostics) {
	target, inRun, self := p.Target(addr, configVal)
	if !inRun {
		if !configVal.IsWhollyKnown() {
			// The default runtime defers such a read to the apply. The
			// port's apply reads only in-run estates, so it refuses here
			// rather than send the provider an unknown configuration.
			return planned, true, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Not supported on the new runtime yet",
				fmt.Sprintf("%s is a terraform_estate_outputs read whose estate or names are not known until the apply. The new runtime cannot defer a data source read to the apply yet (upstream opentofu/opentofu #4058). Write them as literal values, or unset TOFU_X_EXPERIMENTAL_RUNTIME to use the default runtime.", addr)))
		}
		return planned, false, diags
	}
	if self {
		_, _, diags = Value(target, true, configVal, cty.NilVal)
		return planned, true, diags
	}
	mod := host.InRunModule(target.UnkeyedInstanceShim())
	planned.Dependencies = mod.Resources
	if !configVal.IsWhollyKnown() {
		planned.Deferred, planned.ConfigUnknown = true, true
		planned.Value = cty.UnknownVal(schemaType)
		return planned, true, diags
	}
	val, known, diags := Value(target, false, configVal, mod.Outputs)
	if diags.HasErrors() {
		return planned, true, diags
	}
	planned.Value = val
	planned.Deferred = mod.Pending || !known
	return planned, true, diags
}

// ApplyValue answers an in-run read during the apply, from the module's
// outputs as the apply settled them. An output still unknown here is an
// error: the read was deferred to wait for it.
func (p *Port) ApplyValue(addr addrs.AbsResourceInstance, configVal cty.Value, outputs func(addrs.ModuleInstance) cty.Value) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	target, inRun, self := p.Target(addr, configVal)
	if !inRun {
		return cty.NilVal, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Not supported on the new runtime yet",
			fmt.Sprintf("The plan defers the read of %s to the apply, which the new runtime does only for a terraform_estate_outputs block naming an estate this run applies (upstream opentofu/opentofu #4058).", addr)))
	}
	val, known, moreDiags := Value(target, self, configVal, outputs(target.UnkeyedInstanceShim()))
	diags = diags.Append(moreDiags)
	if diags.HasErrors() {
		return cty.NilVal, diags
	}
	if !known {
		return cty.NilVal, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Another estate's output is not known after its apply",
			fmt.Sprintf("%s reads outputs of estate %q, which this run applies as module %s, and one of them is still not known after that module's changes were applied.", addr, configVal.GetAttr("estate").AsString(), target)))
	}
	return val, diags
}

// Modules returns the module instance of every in-run estate, sorted, for
// the apply to carry each one's outputs into the final state: the live
// write-back records a module's outputs as its estate's root outputs.
func (p *Port) Modules() []addrs.ModuleInstance {
	if p == nil {
		return nil
	}
	var out []addrs.ModuleInstance
	for _, m := range p.Estates {
		out = append(out, m.UnkeyedInstanceShim())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
