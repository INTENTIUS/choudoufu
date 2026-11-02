// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import "testing"

// ResetRunCacheForTest clears the process-wide "something has been written"
// switch that [RunCache] uses to decide whether it may still trust its
// snapshot (see RunCache's doc comment, "Why it cannot serve a stale value",
// for why that switch is one atomic per process rather than one per cache).
//
// The switch is deliberately sticky for the product - one process is one
// run, and a run that has written once must never serve a remembered value
// again - but that same stickiness means it is sticky across every test in
// a `go test -count=N` process: iteration 1's write turns every cache off
// for iterations 2..N, so a test whose premise is "the cache is currently
// serving" has no way to establish that premise from inside the test.
//
// Call it before any assertion that depends on the switch's state. t.Cleanup
// restores whatever the switch held before the call, so this cannot leak a
// fixed state into whatever else runs in the same process afterward.
func ResetRunCacheForTest(t testing.TB) {
	t.Helper()
	prev := wroteSomething.Load()
	wroteSomething.Store(false)
	t.Cleanup(func() { wroteSomething.Store(prev) })
}
