// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package command

import (
	"os"
	"syscall"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/zclconf/go-cty/cty"
)

// interruptSettleTime is how long PostApply waits, after delivering the
// self-signal below, before returning control to the graph walker. It is
// generous headroom for in-process signal delivery (the runtime's signal
// goroutine waking cmd/choudoufu's own makeShutdownCh listener, which calls
// tofu.Context.Stop()), not a guess at how long that actually takes - on
// any real system it is microseconds. It is not the synchronization
// mechanism itself: the mechanism is that this whole method runs
// synchronously inside the single node whose apply just committed, so
// under -parallelism=1 nothing else in the graph can be dispatched until
// this call returns, which makes the pause below a guaranteed lower bound
// on the window's width rather than a hopeful one.
const interruptSettleTime = 200 * time.Millisecond

// PostApply self-delivers SIGTERM - the same signal cmd/choudoufu's
// makeShutdownCh forwards to tofu.Context.Stop() for an ordinary operator
// Ctrl-C - the instant TOFU_E2E_APPLY_RESOURCE_INTERRUPT's named resource
// instance address completes a successful, non-destroying apply (a create
// or update; PostApply's own newState is always null for a destroy, per
// postApplyHook in node_resource_abstract_instance.go, so that half is
// excluded by construction, not by inspecting the change's action).
//
// This exists to give live/e2e/reference-ec2-vpc/run.sh's day2_crash
// stage (GitHub issue #490) a deterministic way to land a real interrupt
// strictly between a create_before_destroy replace's create committing and
// the paired destroy of the deposed object ever being dispatched, replacing
// a prior mechanism that raced an external process (tail -F the apply's
// own log output, grep for the "Creation complete" line, kill -TERM) against
// that same internal gap. That race could not be won reliably: the
// external detect-and-signal chain (file I/O, a poll loop, a process
// fork+exec for kill) is both slower and far more variable than the
// internal step from one graph node finishing to the walker considering
// the next one, so external detection was, at best, hoping unrelated
// filler work got scheduled first. Self-signaling removes the external
// leg of that race entirely - the only remaining latency is in-process
// signal delivery, which interruptSettleTime's own comment sizes.
//
// Reachable only in an e2eTestingFeatures build (cmd/choudoufu/testing.go,
// go build -ldflags="-X 'main.e2eTestingFeatures=yes'"), the same gate
// TOFU_E2E_APPLY_RESOURCE_PANIC already sits behind in apply_e2etesting.go;
// an ordinary build's PostApply is tofu.NilHook's no-op, unconditionally.
// Windows is excluded by this file's build tag because forwardSignals is
// empty there (cmd/choudoufu/signal_windows.go) - SIGTERM triggers nothing
// to interrupt, so self-signaling it would be a silent no-op dressed up as
// a working control.
func (e *e2eTestingApplyHook) PostApply(addr addrs.AbsResourceInstance, gen states.Generation, newState cty.Value, applyErr error) (tofu.HookAction, error) {
	target := os.Getenv("TOFU_E2E_APPLY_RESOURCE_INTERRUPT")
	if target == "" || target != addr.String() || applyErr != nil || newState.IsNull() {
		return tofu.HookActionContinue, nil
	}

	e.interruptOnce.Do(func() {
		proc, err := os.FindProcess(os.Getpid())
		if err != nil {
			// FindProcess is infallible on every platform this file
			// builds for (it never contacts the OS on !windows), but
			// nothing here is worth a panic over: fall through and let
			// the apply proceed as if TOFU_E2E_APPLY_RESOURCE_INTERRUPT
			// had not been set, rather than crashing the whole process
			// on a path that exists only to crash it deliberately.
			return
		}
		_ = proc.Signal(syscall.SIGTERM)
		time.Sleep(interruptSettleTime)
	})

	return tofu.HookActionContinue, nil
}
