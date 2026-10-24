// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/dag"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// inRunEstateOutputsType is the builtin terraform provider's cross-estate
// output read. The name is repeated here, not imported, because the
// provider package sits above this one.
const inRunEstateOutputsType = "terraform_estate_outputs"

// This file is the core half of a composed run: one plan and one apply over
// a root that calls each estate's root as a module (GitHub issue #2059).
// ContextOpts.InRunEstates maps an estate name to the module call that
// carries it. A data "terraform_estate_outputs" block that names one of
// those estates is then answered from that module's outputs in the walk
// rather than from the record store:
//
//   - the block depends on the module's outputs, so the module's resources
//     are applied first (InRunEstateOutputsTransformer);
//   - while the module has changes pending the read is deferred to the
//     apply, like any data source whose dependency has changes;
//   - otherwise it is read at once, from the outputs the walk already holds.
//
// A walk with no InRunEstates takes none of these branches.

// InRunEstateOutputsTransformer connects every in-run estate read to the
// outputs of the module that estate is.
type InRunEstateOutputsTransformer struct {
	Estates map[string]addrs.Module
	Config  *configs.Config
}

func (t *InRunEstateOutputsTransformer) Transform(_ context.Context, g *Graph) error {
	if len(t.Estates) == 0 || t.Config == nil {
		return nil
	}
	outputs := map[string][]dag.Vertex{}
	for _, v := range g.Vertices() {
		if o, ok := v.(*nodeExpandOutput); ok {
			outputs[o.Module.String()] = append(outputs[o.Module.String()], v)
		}
	}
	for _, v := range g.Vertices() {
		cr, ok := v.(GraphNodeConfigResource)
		if !ok {
			continue
		}
		addr := cr.ResourceAddr()
		estate, ok := inRunEstateLiteral(t.Config, addr)
		if !ok {
			continue
		}
		target, ok := t.Estates[estate]
		if !ok {
			continue
		}
		if len(addr.Module) > 0 && len(target) > 0 && addr.Module[0] == target[0] {
			continue // a self-read; the walk refuses it by name
		}
		for _, o := range outputs[target.String()] {
			g.Connect(dag.BasicEdge(v, o))
		}
	}
	return nil
}

// inRunEstateLiteral returns the estate a terraform_estate_outputs block
// names, when addr is one and the estate is written as a literal string.
func inRunEstateLiteral(cfg *configs.Config, addr addrs.ConfigResource) (string, bool) {
	if addr.Resource.Mode != addrs.DataResourceMode || addr.Resource.Type != inRunEstateOutputsType {
		return "", false
	}
	mc := cfg.Descendent(addr.Module)
	if mc == nil || mc.Module == nil {
		return "", false
	}
	r := mc.Module.ResourceByAddr(addr.Resource)
	if r == nil || r.Config == nil {
		return "", false
	}
	content, _, diags := r.Config.PartialContent(&hcl.BodySchema{Attributes: []hcl.AttributeSchema{{Name: "estate"}}})
	if diags.HasErrors() {
		return "", false
	}
	attr := content.Attributes["estate"]
	if attr == nil {
		return "", false
	}
	v, vd := attr.Expr.Value(nil)
	if vd.HasErrors() || !v.IsKnown() || v.IsNull() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}

// inRunEstateTarget reports the module an in-run read of this node is
// answered from.
func (n *NodeAbstractResourceInstance) inRunEstateTarget(evalCtx EvalContext, configVal cty.Value) (addrs.Module, bool, bool) {
	estates := evalCtx.InRunEstates()
	if len(estates) == 0 {
		return nil, false, false
	}
	res := n.Addr.Resource.Resource
	if res.Mode != addrs.DataResourceMode || res.Type != inRunEstateOutputsType {
		return nil, false, false
	}
	if configVal == cty.NilVal || !configVal.IsKnown() || configVal.IsNull() || !configVal.Type().IsObjectType() || !configVal.Type().HasAttribute("estate") {
		return nil, false, false
	}
	est := configVal.GetAttr("estate")
	if !est.IsKnown() || est.IsNull() || est.IsMarked() {
		return nil, false, false
	}
	target, ok := estates[est.AsString()]
	if !ok {
		return nil, false, false
	}
	self := len(n.Addr.Module) > 0 && len(target) > 0 && n.Addr.Module[0].Name == target[0]
	return target, true, self
}

// inRunEstateValue builds the data source's object from the module's outputs
// as the walk holds them. The second result is false when an output is not
// yet wholly known.
func (n *NodeAbstractResourceInstance) inRunEstateValue(evalCtx EvalContext, target addrs.Module, self bool, configVal cty.Value) (cty.Value, bool, tfdiags.Diagnostics) {
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
	attrs := map[string]cty.Value{}
	known := true
	for _, name := range names {
		ov := evalCtx.State().OutputValue(addrs.OutputValue{Name: name}.Absolute(target.UnkeyedInstanceShim()))
		if ov == nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Another estate has not recorded this output",
				fmt.Sprintf("data \"terraform_estate_outputs\" reads output %q of estate %q, which this run applies as module %s, and that module declares no such output.", name, estate, target)))
			continue
		}
		if ov.Sensitive {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Another estate has not recorded this output",
				fmt.Sprintf("Output %q of estate %q is sensitive. Only non-sensitive outputs cross between estates, in a composed run as in separate ones.", name, estate)))
			continue
		}
		if !ov.Value.IsWhollyKnown() {
			known = false
		}
		attrs[name] = ov.Value
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

// inRunEstatePending reports whether any resource of the module has a change
// planned.
func inRunEstatePending(evalCtx EvalContext, target addrs.Module) bool {
	return evalCtx.Changes().AnyUnder(target, func(a plans.Action) bool { return a != plans.NoOp })
}
