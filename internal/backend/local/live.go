// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package local

import (
	"context"

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
// There are two methods because a stateless run has two things to say to the
// operation, and they happen at different moments:
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
// The state manager cannot do the second job itself. [statemgr.Full] has no
// access to configuration or to configured providers, and a projection is
// built entirely out of those two; a manager that could build one would be a
// manager that had been handed the whole run.
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

	// WriteBack is GitHub issue #73's third leg: after a successful apply,
	// persist every record-backed resource instance's new state to the
	// record store PriorState read from, and delete the record for any
	// record-backed instance a destroy removed. finalState is the state the
	// apply finished with; schemas is the run's own schema set, already in
	// hand at the one call site (opApply).
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
	WriteBack(ctx context.Context, finalState *states.State, schemas *tofu.Schemas) tfdiags.Diagnostics
}
