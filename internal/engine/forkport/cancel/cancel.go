// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package cancel is a choudoufu fork port (#2381, unit W4 of #2256): stop
// and Ctrl-C on OpenTofu's new runtime (internal/engine).
//
// tofu.Context.Stop cancels the run's context. On the default runtime the
// graph walk watches it and asks every provider and provisioner the run
// opened to stop gracefully. The new runtime never looks at it, so a stop
// (Ctrl-C) leaves a provider call running to the end (upstream's tests
// skip this as ExperimentalBugCancel). [Watch] does what the walk's watcher
// does, for the length of one plan or apply on the new runtime, and
// [Halted] fails a stopped run as the default runtime's stop hook does, so
// a stopped plan never reads as a complete one.
//
// It is the whole port. internal/tofu's shim calls it where plan and apply
// enter the engine, each call commented "choudoufu fork port (#2381):
// remove when the row closes". Removing the port is deleting this package,
// those call sites and the "cancel" row of
// internal/live/newruntime/gaps.json. No upstream issue tracks the gap yet.
//
// It is registered in internal/engine/forkport as [Name]. Switched off
// (forkport.Enabled), [Watch] does nothing, as before the port.
package cancel

import (
	"context"
	"errors"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

// Name is the port's name in the internal/engine/forkport registry.
const Name = "cancel"

// Watch calls stopAll, once, if run is cancelled before release is called:
// run is the tofu.Context's run context, which Stop cancels, and stopAll
// asks the run's providers and provisioners to stop. Call release when the
// engine has returned, so that ending the run normally, which also
// cancels run, never stops a provider. With the port off, or no run
// context, Watch does nothing.
func Watch(run context.Context, stopAll func()) (release func()) {
	if run == nil || !forkport.Enabled(Name) {
		return func() {}
	}
	stop := context.AfterFunc(run, stopAll)
	return func() { stop() }
}

// Halted is the default runtime's "execution halted" error for a run that
// was stopped (the tofu.Context's stop hook reports it), and nil for one
// that was not, or with the port off. The default runtime's graph walk
// fails a stopped run with it; the new runtime ignores the hook's error.
func Halted(stopped bool) error {
	if !stopped || !forkport.Enabled(Name) {
		return nil
	}
	return errors.New("execution halted")
}
