// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// Scope reports whether one resource block is inside the run's evaluation
// scope: whether stock OpenTofu's own plan graph would still hold that block
// after -target / -exclude filtering.
//
// GitHub issue #352. A nil Scope is the default and means every block is in
// scope, which is what an untargeted run passes and what every caller passed
// before this existed.
//
// Nothing in this package computes a Scope. The only honest source is the
// graph the plan itself will be built from - see
// [github.com/intentius/choudoufu/internal/tofu.Context.TargetedResources],
// whose answer is [github.com/intentius/choudoufu/internal/tofu.TargetingTransformer]'s
// own, ancestors included. Resolution takes it as given for the same reason
// it takes [Context.Schemas] as given: the fact lives behind machinery that
// runs after this package, and inventing a second version of it here would
// be a second set of targeting semantics for a plan to disagree with.
type Scope func(addrs.ConfigResource) bool

// inScope reports whether one resource block of the module the resolver is
// currently in is in scope. A resolver with no scope answers true for
// everything.
func (r *resolver) inScope(rc *configs.Resource) bool {
	if r.scope == nil {
		return true
	}
	return r.scope(addrs.ConfigResource{
		Module:   r.modInst.Module(),
		Resource: rc.Addr(),
	})
}

// walkOutOfScope classifies one resource block the run's -target / -exclude
// filtering has already removed from the plan graph.
//
// It still tries. An out-of-scope block that resolves normally keeps its
// resolution, and that is not a courtesy: internal/live/discovery builds its
// "this address is declared" set from the resolutions it is handed
// (declared.all), and that set is what stops the estate-wide marker sweep
// reading a live object as an orphan to remove. Dropping an out-of-scope
// resource outright would turn every marked object it owns into an undeclared
// one, which is a policy threshold away from being acted on.
//
// What changes is the failure. A block stock OpenTofu removed before
// evaluating it has no business refusing this run, so every diagnostic its
// own attempt raised is rolled back and the instance is simply absent - the
// same absence the plan's targeting produces, arrived at one pass earlier.
// The diagnostics are rolled back rather than downgraded to warnings because
// they name arguments of a resource this run is not acting on; the operator
// asked for a subset and gets a subset's report.
//
// Rolling back cannot hide a refusal an in-scope resource needed: an in-scope
// resource's identity can only reference blocks the plan graph also keeps,
// since a reference IS the dependency edge [TargetingTransformer] follows
// when it adds a targeted vertex's ancestors. A reference reaching a block
// that is out of scope therefore cannot arise from a scope this fork
// computes, and a caller that hand-builds an inconsistent one gets the
// referencing instance's own refusal, which is the one the operator can act
// on anyway.
func (r *resolver) walkOutOfScope(rc *configs.Resource, result *Result) {
	diagMark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	exp, ok := r.expansionFor(rc)
	if !ok {
		r.rollback(diagMark, sibMark)
		return
	}
	for _, key := range exp.keys {
		addr := rc.Addr().Instance(key).Absolute(r.modInst)
		instDiagMark, instSibMark := len(r.diags), len(r.pendingSiblingApply)
		res, ok := r.instance(addr, rc.DeclRange)
		if !ok {
			r.rollback(instDiagMark, instSibMark)
			continue
		}
		result.add(res)
	}
}

// rollback drops every diagnostic raised since diagMark, along with the
// sibling-apply refusals recorded since sibMark.
//
// It is [resolver.probeString]'s own discipline, factored out and for its
// stated reason: a [siblingApplyRefusal] holds the INDEX of the diagnostic it
// raised, so discarding diagnostics without discarding the refusals pointing
// into them leaves a later withdrawal rewriting a diagnostic that is now
// something else entirely.
func (r *resolver) rollback(diagMark, sibMark int) {
	if diagMark <= len(r.diags) {
		r.diags = r.diags[:diagMark:diagMark]
	}
	if sibMark <= len(r.pendingSiblingApply) {
		r.pendingSiblingApply = r.pendingSiblingApply[:sibMark]
	}
}
