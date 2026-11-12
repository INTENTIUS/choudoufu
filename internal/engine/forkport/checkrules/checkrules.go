// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package checkrules is a choudoufu fork port (#2409, of #2256): check
// blocks and resource preconditions and postconditions on OpenTofu's new
// runtime, until upstream's #4390 lands.
//
// Upstream's new runtime compiles output preconditions and nothing else:
// resource preconditions and postconditions are TODO stubs and check blocks
// are not compiled at all, so the gap table refused all three. The port
// evaluates them where upstream's issue proposes, inside the evaluator
// (internal/lang/eval/internal/tofu2024/fork_checkrules.go):
//
//   - a resource instance's preconditions guard its configuration value, so
//     a failing one stops the instance from being planned or applied;
//   - its postconditions are checked against the result the planning or
//     apply engine returns for it, so an apply-time failure is reported
//     after the object is saved, as the default runtime does;
//   - each check block's assertions are checked whenever the evaluator walks
//     the module, and a failure is a warning.
//
// What a failing condition reports depends on the phase, as in the
// default runtime: a resource condition failure is an error in a plan or an
// apply, a warning in a refresh-only plan, and nothing at all in a destroy
// plan's refresh or in validation; a check assertion failure is a warning
// except in those last two, and one that cannot be decided yet warns "known
// after apply" while planning. The planning engine and validation name
// their phase with [WithPhase]; an apply needs nothing. Check results are
// not recorded in the plan or state (C3 of #2410), and a check block's
// scoped data source is still refused by the data-sources row.
//
// Every call site is commented "choudoufu fork port (#2409): remove when
// upstream #4390 lands". Removing the port is deleting this package,
// fork_checkrules.go, those call sites and the "checks-conditions" row's
// fork_port and switch in internal/live/newruntime/gaps.json.
//
// Switched off (forkport.Enabled), nothing is compiled and the gap table's
// preflight refuses check blocks and resource conditions again.
package checkrules

import (
	"context"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "checkrules"

// Enabled reports whether the port is on.
func Enabled() bool {
	return forkport.Enabled(Name)
}

// Phase is what the evaluator is evaluating for.
type Phase int

const (
	// Apply is the default, for a context [WithPhase] never saw.
	Apply Phase = iota
	Plan
	RefreshOnly
	DestroyRefresh
	Validate
)

type phaseKey struct{}

// WithPhase returns ctx for evaluating in phase.
func WithPhase(ctx context.Context, phase Phase) context.Context {
	return context.WithValue(ctx, phaseKey{}, phase)
}

// Report says how a failing condition is reported under ctx: with what
// severity, and whether at all. checkBlock is true for a check block's
// assertion and false for a resource precondition or postcondition.
func Report(ctx context.Context, checkBlock bool) (hcl.DiagnosticSeverity, bool) {
	phase, _ := ctx.Value(phaseKey{}).(Phase)
	switch {
	case phase == DestroyRefresh || phase == Validate:
		return hcl.DiagInvalid, false
	case checkBlock || phase == RefreshOnly:
		return hcl.DiagWarning, true
	default:
		return hcl.DiagError, true
	}
}

// PhaseOf is the phase ctx was given with [WithPhase], or [Apply].
func PhaseOf(ctx context.Context) Phase {
	phase, _ := ctx.Value(phaseKey{}).(Phase)
	return phase
}

// WarnUnknown reports whether a check assertion that cannot be decided yet
// warns "known after apply" under ctx: while planning.
func WarnUnknown(ctx context.Context) bool {
	phase, _ := ctx.Value(phaseKey{}).(Phase)
	return phase == Plan || phase == RefreshOnly
}

// PhaseForPlan is [RefreshOnly] for a refresh-only plan and [Plan] otherwise.
func PhaseForPlan(refreshOnly bool) Phase {
	if refreshOnly {
		return RefreshOnly
	}
	return Plan
}
