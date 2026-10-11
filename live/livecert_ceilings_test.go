// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The ceilings on a real-AWS certification used to be set to values that
// could not carry one (GitHub issue #1102's open item, fixed 2026-09-15).
// `gauntlet live-cert` defaulted -timeout-seconds to 900 and a since-deleted
// live-cert workflow pinned the job at timeout-minutes: 60, while the
// largest real-AWS run on record - 3,705 resources - took over three hours,
// and scale 136's cold_deploy alone is 8,735s on the emulator.
//
// Neither number failed anything, which is why they survived: no run ever
// hit them. They were a guaranteed failure waiting for the first person to
// try the thing the estate exists for.
//
// This is a bound, not an equality: a ceiling is free to grow. What the
// guard refuses is a return to a value that cannot carry the run.

// liveCertMinProcessSeconds is what the Go-side ceiling must at least allow.
// Four hours. The 3,705-resource run took over three; this is the smallest
// default that does not cut into a size already measured.
const liveCertMinProcessSeconds = 14400

func TestLiveCertProcessCeilingCanCarryARealRun(t *testing.T) {
	src, err := os.ReadFile("../tools/gauntlet/main.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`fs\.Int\("timeout-seconds",\s*(\d+),`).FindSubmatch(src)
	if m == nil {
		t.Fatal("tools/gauntlet/main.go declares no -timeout-seconds flag with a literal default; " +
			"if the flag moved, move this guard with it rather than deleting it")
	}
	got, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 && got < liveCertMinProcessSeconds {
		t.Errorf("-timeout-seconds defaults to %d, below %d.\n"+
			"    A real-AWS certification at 3,705 resources took over three hours. A default under four\n"+
			"    hours does not bound that run, it kills it, and the caller only finds out after paying for\n"+
			"    cold_deploy and migrate. Raise the default or pass 0 to disable the ceiling deliberately.",
			got, liveCertMinProcessSeconds)
	}
}
