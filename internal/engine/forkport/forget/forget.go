// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package forget is a choudoufu fork port (#2409, wave 2 of #2256): removed
// blocks and lifecycle { destroy = false } on OpenTofu's new runtime
// (internal/engine).
//
// Both say "drop the object from state and leave it running". The new
// runtime's execution graph can already forget an object (upstream's
// `#4729`), but its planning engine never plans that: an unwanted object is
// always deleted, a replace always deletes the old object, and the applied
// state never records destroy = false. Upstream's work is `#4682` (the
// destroy argument), `#4686` (removed blocks) and `#4693` (a moved chain that
// ends at a removed block). Until it lands this package decides, the way the
// default runtime's plan nodes do (internal/tofu's node_resource_plan_orphan.go,
// node_resource_deposed.go, node_resource_plan_destroy.go and
// node_resource_abstract_instance.go):
//
//   - [Port.Unwanted]: whether an orphaned or deposed object is forgotten
//     rather than deleted: destroy = false on its resource block or recorded
//     on the object in state, overridden in a normal plan by a removed block
//     that covers it;
//   - [Port.SkipDestroy]: what an object the configuration still declares
//     records as destroy = false in state, and whether its replace is a
//     ForgetThenCreate;
//   - [DestroyLeftForgotten]: the default runtime's error for a destroy that
//     left forgotten objects behind, unless -suppress-forget-errors is set.
//
// In live mode a forget is the default runtime's forget and nothing more: the
// object leaves the final state, so internal/live/projection's write-back
// deletes its record in the final pass, and its ownership marker is left on
// the live object (live/MIGRATE.md: undeclared_tagged = "untag" is how live
// mode releases a marker). The backend does both after either runtime's
// apply, from the final state.
//
// The engine calls it from internal/engine/planning's plan_managed.go and
// internal/engine/applying's operations_resource_managed.go, and
// internal/tofu passes it in and raises [DestroyLeftForgotten]. Each site is
// commented "choudoufu fork port (#2409): remove when upstream <#N> lands".
// internal/states carries destroy = false through the new runtime's state
// objects with the field upstream's `#4839` adds. Removing the port is
// deleting this package, those call sites and the fork_port fields of the
// "removed" and "lifecycle-destroy" rows of
// internal/live/newruntime/gaps.json.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), [New] returns nil, every method of a nil *Port says
// "delete", and both rows refuse again.
package forget

import (
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/engine/forkport"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/refactoring"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "forget"

// Port is the forget port for one plan or apply. A nil *Port is off.
type Port struct {
	config *configs.Config

	// removes are the configuration's removed blocks, read only in a
	// normal plan, as the default runtime reads them (Context.plan).
	removes     []*refactoring.RemoveStatement
	removeDiags tfdiags.Diagnostics

	mu sync.Mutex
	// declared is every resource instance the configuration declares that
	// the planning engine has seen. In a destroy plan every object is
	// unwanted, and for a declared one the configuration's destroy setting
	// replaces the one recorded in state, as the default runtime's refresh
	// rewrites it before its destroy plan reads it.
	declared addrs.Set[addrs.AbsResourceInstance]
}

// New returns the port for a run of config in mode, or nil when it is
// switched off. An apply passes the plan's mode.
func New(config *configs.Config, mode plans.Mode) *Port {
	if !forkport.Enabled(Name) || config == nil {
		return nil
	}
	p := &Port{config: config, declared: addrs.MakeSet[addrs.AbsResourceInstance]()}
	if mode == plans.NormalMode {
		p.removes, p.removeDiags = refactoring.FindRemoveStatements(config)
	}
	return p
}

// Validate returns the default runtime's errors for removed blocks whose
// resource or module block still exists. Plan raises them before planning.
func (p *Port) Validate() tfdiags.Diagnostics {
	if p == nil {
		return nil
	}
	return p.removeDiags
}

// SkipDestroy reports whether the resource block of addr sets
// lifecycle { destroy = false }. The argument must be a constant boolean, as
// on the default runtime (skipDestroyValueFromConstantExpression). False for
// a nil port or no resource block.
func (p *Port) SkipDestroy(addr addrs.AbsResourceInstance) (bool, tfdiags.Diagnostics) {
	if p == nil {
		return false, nil
	}
	r := p.resource(addr)
	if r == nil || r.Managed == nil || r.Managed.Destroy == nil {
		return false, nil
	}
	expr := r.Managed.Destroy
	v, hclDiags := expr.Value(nil)
	var diags tfdiags.Diagnostics
	diags = diags.Append(hclDiags)
	if hclDiags.HasErrors() {
		return false, diags
	}
	if v.Type() != cty.Bool || v.IsNull() || !v.IsKnown() {
		return false, diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid lifecycle destroy expression",
			Detail:   "The lifecycle destroy expression must be a boolean constant.",
			Subject:  expr.Range().Ptr(),
		})
	}
	return v.False(), diags
}

// resource is the resource block of addr, or nil when the configuration has
// none.
func (p *Port) resource(addr addrs.AbsResourceInstance) *configs.Resource {
	c := p.config.Descendent(addr.Module.Module())
	if c == nil || c.Module == nil {
		return nil
	}
	return c.Module.ResourceByAddr(addr.Resource.Resource)
}

// Declared records that the configuration declares addr and returns what
// its object records as destroy = false. The planning engine calls it for
// every desired managed resource instance.
func (p *Port) Declared(addr addrs.AbsResourceInstance) (bool, tfdiags.Diagnostics) {
	if p == nil {
		return false, nil
	}
	p.mu.Lock()
	p.declared.Add(addr)
	p.mu.Unlock()
	return p.SkipDestroy(addr)
}

// Unwanted decides whether an orphaned or deposed object at addr, its
// address in this run after any move from prevRunAddr, is forgotten instead
// of deleted. stateSkip is the destroy = false recorded on the object in
// state. The rules are the default runtime's: destroy = false in the
// configuration or in state forgets, except that a current object the
// configuration still declares (a destroy plan's) goes by the configuration
// alone; then in a normal plan a removed block covering addr decides, as the
// latest intent.
//
// reason is the forget's action reason, as the default runtime gives it: the
// lifecycle argument's when one is set, none for a deposed object, a missing
// resource block or move target when the configuration has no resource
// block, and orphanReason, the caller's, otherwise.
func (p *Port) Unwanted(addr addrs.AbsResourceInstanceObject, prevRunAddr addrs.AbsResourceInstance, stateSkip bool, orphanReason plans.ResourceInstanceChangeActionReason) (forget bool, reason plans.ResourceInstanceChangeActionReason, diags tfdiags.Diagnostics) {
	if p == nil {
		return false, orphanReason, nil
	}
	configSkip, diags := p.SkipDestroy(addr.InstanceAddr)
	if diags.HasErrors() {
		return false, orphanReason, diags
	}
	if addr.IsCurrent() {
		p.mu.Lock()
		declared := p.declared.Has(addr.InstanceAddr)
		p.mu.Unlock()
		if declared {
			stateSkip = false
		}
	}
	forget = configSkip || stateSkip
	for _, rs := range p.removes {
		if rs.From.TargetContains(addr.InstanceAddr) {
			forget = !rs.Destroy
		}
	}
	switch {
	case !forget:
		reason = orphanReason
	case configSkip:
		reason = plans.ResourceInstanceForgotBecauseLifecycleDestroyInConfig
	case stateSkip:
		reason = plans.ResourceInstanceForgotBecauseLifecycleDestroyInState
	case addr.IsDeposed():
		reason = plans.ResourceInstanceChangeNoReason
	case p.resource(addr.InstanceAddr) == nil && !addr.InstanceAddr.Equal(prevRunAddr):
		reason = plans.ResourceInstanceDeleteBecauseNoMoveTarget
	case p.resource(addr.InstanceAddr) == nil:
		reason = plans.ResourceInstanceDeleteBecauseNoResourceConfig
	default:
		reason = orphanReason
	}
	return forget, reason, diags
}

// ReplaceAction is the action for a replace of addr: ForgetThenCreate when
// its resource block sets destroy = false, as on the default runtime
// (NodeAbstractResourceInstance.plan), and action otherwise.
func (p *Port) ReplaceAction(addr addrs.AbsResourceInstance, action plans.Action) (plans.Action, tfdiags.Diagnostics) {
	if p == nil || !action.IsReplace() {
		return action, nil
	}
	skip, diags := p.SkipDestroy(addr)
	if skip {
		return plans.ForgetThenCreate, diags
	}
	return action, diags
}

// DestroyLeftForgotten is the default runtime's error for a destroy that
// forgot objects instead of deleting them (Context.Apply), or nil when
// mode is not a destroy, nothing was forgotten, suppress is set
// (-suppress-forget-errors), or the port is off.
func DestroyLeftForgotten(mode plans.Mode, forgotten int, suppress bool) tfdiags.Diagnostics {
	if mode != plans.DestroyMode || forgotten == 0 || suppress || !forkport.Enabled(Name) {
		return nil
	}
	var diags tfdiags.Diagnostics
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Destroy was successful but left behind forgotten instances",
		`As requested, OpenTofu has not deleted some remote objects that are no longer managed by this configuration. Those objects continue to exist in their remote system and so may continue to incur charges. Refer to the original plan for more information.
To suppress this error for the future 'destroy' runs, you can add the CLI flag "-suppress-forget-errors".`,
	))
}

// Covers reports whether the port handles removed block r. A removed block
// that deletes and has provisioners stays refused: the default runtime runs
// them as destroy-time provisioners, and the new runtime would not.
func Covers(r *configs.Removed) bool {
	return r != nil && (!r.Destroy || len(r.Provisioners) == 0)
}
