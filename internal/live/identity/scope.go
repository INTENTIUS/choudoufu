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
	exp, ok := r.expansionOutOfScope(rc)
	if !ok {
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

// expansionOutOfScope is [resolver.expansionFor] for a block this run's
// -target / -exclude filtering has removed from the plan graph: the same
// expansion, with a failure's diagnostics rolled back and the failure
// itself forgotten.
//
// GitHub issue #1470. Both callers of expansionFor that can meet an
// out-of-scope block route through here - [resolver.collectSignalInto],
// which runs over the whole configuration BEFORE the walk, and
// [resolver.walkOutOfScope]. Before this existed, the collection called
// expansionFor with no scope at all, so an excluded block's "Non-static
// for_each expression" was raised ahead of any mark walkOutOfScope could
// roll back to; expansionFor then memoized the failure, so the walk's own
// call returned false with no new diagnostic and its rollback removed
// nothing. The error reached the caller, for a block the run had excluded,
// and a -target run was refused for it (internal/command's
// TestLivePlan_targetIsNotRefusedByAnExcludedForEachTheSecondPassCannotSettle).
//
// The failure is forgotten (r.expFailed) as well as rolled back, and that
// is what keeps walkOutOfScope's own promise about an inconsistent scope
// true. expansionFor's memo exists so two consumers of one failed block
// share one diagnostic; a consumer that finds the memo returns false
// SILENTLY ([resolver.resolveResourceRef]'s parentExp branch), on the
// understanding that the diagnostic is already on r.diags. Here it is not:
// it was just rolled back. An in-scope block whose for_each reads this one
// - which a scope [statelessTargetScope] computes cannot produce, since the
// reference is the graph edge targeting follows, but a hand-built scope can
// - must re-evaluate the expansion and raise the refusal afresh, in its
// own context, rather than resolve to nothing with no diagnostic at all.
// The cost is one extra evaluation of an excluded block's count or
// for_each per call, and only when it fails.
func (r *resolver) expansionOutOfScope(rc *configs.Resource) (*expansion, bool) {
	diagMark, sibMark := len(r.diags), len(r.pendingSiblingApply)
	exp, ok := r.expansionFor(rc)
	if !ok {
		r.rollback(diagMark, sibMark)
		delete(r.expFailed, r.expKey(rc))
	}
	return exp, ok
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
