// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// StatelessRun is the seam stateless mode enters the ordinary
// local operations through. When [Local.Stateless] is nil - which is every
// run of a configuration without a "live" block - nothing in this file
// is reached and the backend behaves exactly as it always has.
//
// There are three methods because a stateless run has three things to say to
// the operation, and they happen at different moments:
//
//  1. StateMgr replaces the file-backed state manager, before the operation
//     starts. Everything the operation does to state - the periodic hook, the
//     final write, the emergency persist on interrupt, the lock - goes
//     through that object, so replacing it is what makes "no state was
//     written" a property of the run rather than a list of call sites someone
//     has to remember to skip.
//
//  2. PriorState supplies the prior state, after the configuration is loaded
//     and the OpenTofu context exists but before anything plans. That is the
//     only point where all three things a projection needs are in hand: the
//     configuration to read, a context to get provider schemas from, and a
//     run that has not yet decided anything. It is also where the
//     configuration is rewritten to carry ownership markers, which is why it
//     receives the same *configs.Config the plan is about to read rather than
//     a copy.
//
//  3. AfterApply runs once, only from [Local.opApply] and only after
//     tofu.Context.Apply has returned with no errors - never from
//     [Local.opPlan], and never for a trivial apply that never called Apply
//     at all. It is where a stateless run does whatever a resource that is
//     in the prior state but was never a graph node - never declared in
//     configuration - still needs done to it once real infrastructure has
//     genuinely changed: GitHub issue #67's undeclared_tagged = "untag"
//     verb releases its tag key here, because an orphan with no declared
//     address has no configuration for the ordinary apply graph to hang an
//     update diff off of. See internal/live/untag.
//
// The state manager cannot do the second or third job itself. [statemgr.Full]
// has no access to configuration or to configured providers, and both a
// projection and an apply-time release are built entirely out of those two;
// a manager that could do either would be a manager that had been handed the
// whole run.
type StatelessRun interface {
	// StateMgr is the state manager for this run. It is called once per
	// operation and must return the same object each time, because the
	// operation hands it to the state hook and to the final write and expects
	// those to be the same manager.
	StateMgr() statemgr.Full

	// PriorState returns the prior state for the operation: the projection,
	// built by reading the live system. It may also modify config, which the
	// caller has loaded and not yet used.
	//
	// Error diagnostics abort the run before anything is planned.
	PriorState(ctx context.Context, config *configs.Config, core *tofu.Context) (*states.State, tfdiags.Diagnostics)

	// RootOutputData is the live data-source values PriorState read for the
	// configuration's root outputs, keyed by absolute resource instance
	// address, or nil when it read none - which is the ordinary case.
	//
	// It is a second return value of PriorState in everything but shape.
	// The reason it is a method instead is timing: the values are read while
	// the provider instances that serve them are open, inside PriorState,
	// and they are USED a moment later by
	// [projection.ApplyRootOutputValues], which is the caller's own step
	// rather than the run's. Called at most once per operation, always after
	// PriorState has returned without errors.
	RootOutputData() map[string]cty.Value

	// RecordedRootOutputs is what the estate REMEMBERS each root output's
	// value to be - the value it settled on at the last apply, or at the
	// migration that brought it over from a stock state file - keyed by
	// output name, or nil when the run has no record store or nothing is
	// recorded.
	//
	// It is [projection.ApplyRootOutputValues]'s fallback for an output that
	// cannot be evaluated against the projection at all, which is what a
	// stock state file's own stored output values are to `tofu plan`. A
	// method here for [StatelessRun.RootOutputData]'s reason: the store is
	// opened inside PriorState and the values are used a moment later, in
	// the caller's own step. Called at most once per operation, always after
	// PriorState has returned without errors.
	RecordedRootOutputs() map[string]cty.Value

	// WriteBack is GitHub issue #73's third leg: after a successful apply,
	// persist every record-backed resource instance's new state to the
	// record store PriorState read from, and delete the record for any
	// record-backed instance a destroy removed. finalState is the state the
	// apply finished with; schemas is the run's own schema set, already in
	// hand at the one call site (opApply).
	//
	// replaced is every resource instance address THIS RUN'S PLAN scheduled
	// a replace for - [plans.Action.IsReplace], so DeleteThenCreate or
	// CreateThenDelete and nothing else. GitHub issue #854: a replace is
	// the only ordinary shape in which an apply destroys the object an
	// address's record names and leaves the SAME address in the final state
	// wearing a different one, and it is the plan, not the record, that
	// knows a destroy was scheduled. Several other shapes also re-point an
	// address's record at a different object without destroying anything -
	// an `import` block, a `live-mv` onto an address that already held a
	// record, a ForgetThenCreate under lifecycle.destroy = false - and the
	// record alone cannot tell them apart from a replace. See
	// [projection.WriteBackRequest.ReplacedAddrs].
	//
	// An empty or nil slice means "this run replaced nothing", which makes
	// the write side record no destroyed identity at all: the safe
	// direction, since a missing entry costs a refusal the next plan would
	// otherwise have downgraded to a warning, and can reach the live system
	// through nothing.
	//
	// Called exactly once per apply, after the ordinary state write has
	// already succeeded - never mid-apply, and never for a plan-only run.
	// A run with no record store configured (or no live block at all) does
	// nothing and returns no diagnostics. Error diagnostics here are fatal
	// to the run: a version conflict at write-back means this run's apply
	// already happened but the record store's record of it may not match
	// what actually ran, and that must surface loudly rather than
	// silently, the same philosophy live/MARKERS.md's marker-collision
	// handling already uses.
	WriteBack(ctx context.Context, finalState *states.State, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance) tfdiags.Diagnostics

	// AfterApply runs whatever this run still owes the live system once a
	// real apply has finished changing it, and reports what it did as
	// diagnostics rather than by returning state: whatever it touches was
	// never part of the graph's own state in the first place. A resource
	// this run had nothing to do after apply is not an error - AfterApply
	// returning no diagnostics at all is the ordinary case, for every run
	// with nothing pending and for every plan-only operation, which never
	// calls this method to begin with.
	AfterApply(ctx context.Context) tfdiags.Diagnostics
}
