// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package deferral is a choudoufu fork port (#2382, unit W5 of #2256):
// deferral reasons on OpenTofu's new runtime (internal/engine).
//
// Upstream's planning engine marks a deferred resource instance's result
// with one mark that carries no reason, and the gap table's "deferral" row
// refuses any deferral that -target or -exclude did not cause directly. So a
// dependent of an excluded resource is refused where the old runtime leaves
// it out of the plan, and a resource whose count or for_each is unknown is
// refused where the old runtime raises "Invalid count argument" or "Invalid
// for_each argument". Upstream's deferred-actions work is
// `opentofu/opentofu#4697`. Until it lands this package carries:
//
//   - [Mark], a second mark the engine puts on what -target or -exclude
//     deferred, so a value derived only from such a result is deferred
//     silently, as the old runtime excludes it;
//   - [Port.UnknownExpansion], the old runtime's diagnostic for an instance
//     whose count or for_each is not known during the plan.
//
// The engine calls it from internal/engine/planning's plan_managed.go and
// deferred.go, each site commented
// "choudoufu fork port (#2382): remove when upstream #4697 lands". Removing
// the port is deleting this package, those call sites and the fork_port
// fields of the "deferral" row of internal/live/newruntime/gaps.json.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), [New] returns nil, nothing carries [Mark], and the
// "deferral" row refuses these deferrals again.
package deferral

import (
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/lang/evalchecks"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "deferral"

type targetingMark struct{}

// Mark is the cty mark on a result that -target or -exclude deferred, and so
// on everything derived from it.
var Mark = targetingMark{}

// Port is the deferral port for one plan. A nil *Port is off.
type Port struct {
	config *configs.Config
}

// New returns the port for a plan of config, or nil when it is switched off.
func New(config *configs.Config) *Port {
	if !forkport.Enabled(Name) {
		return nil
	}
	return &Port{config: config}
}

// Deferred returns v marked as deferred by -target or -exclude.
func Deferred(v cty.Value) cty.Value {
	return v.Mark(Mark)
}

// IsDeferred reports whether v itself carries [Mark].
func IsDeferred(v cty.Value) bool {
	return v.HasMark(Mark)
}

// DerivedFromDeferred reports whether any part of v carries [Mark].
func DerivedFromDeferred(v cty.Value) bool {
	return v.HasMarkDeep(Mark)
}

// UnknownExpansion returns the old runtime's error for addr, a placeholder
// standing for the instances of a resource or module call whose count or
// for_each is not known: internal/tofu raises it from
// evaluateCountExpression and evaluateForEachExpression, through the same
// internal/lang/evalchecks functions called here. It returns nil when addr is
// not a placeholder of a count or for_each the configuration declares, and
// the caller refuses the deferral as before.
func (p *Port) UnknownExpansion(addr addrs.AbsResourceInstance) tfdiags.Diagnostics {
	var count, forEach hcl.Expression
	var excludable addrs.Targetable
	if addr.Resource.IsPlaceholder() {
		c := p.config.Descendent(addr.Module.Module())
		if c == nil {
			return nil
		}
		r := c.Module.ResourceByAddr(addr.Resource.Resource)
		if r == nil {
			return nil
		}
		count, forEach, excludable = r.Count, r.ForEach, addr.ContainingResource()
	} else {
		for i, step := range addr.Module {
			if !step.IsPlaceholder() {
				continue
			}
			parent := addr.Module[:i]
			c := p.config.Descendent(parent.Module())
			if c == nil {
				return nil
			}
			call := c.Module.ModuleCalls[step.Name]
			if call == nil {
				return nil
			}
			count, forEach, excludable = call.Count, call.ForEach, parent
			break
		}
	}

	// The expression stands in as an unknown value at its own source range,
	// so the diagnostic points where the old runtime's does.
	switch {
	case count != nil:
		expr := hcl.StaticExpr(cty.UnknownVal(cty.Number), count.Range())
		_, diags := evalchecks.EvaluateCountExpression(expr, func(e hcl.Expression) (cty.Value, tfdiags.Diagnostics) {
			v, hclDiags := e.Value(nil)
			return v, tfdiags.Diagnostics(nil).Append(hclDiags)
		}, excludable)
		return diags
	case forEach != nil:
		expr := hcl.StaticExpr(cty.DynamicVal, forEach.Range())
		_, diags := evalchecks.EvaluateForEachExpression(expr, func([]*addrs.Reference) (*hcl.EvalContext, tfdiags.Diagnostics) {
			return nil, nil
		}, excludable)
		return diags
	}
	return nil
}
