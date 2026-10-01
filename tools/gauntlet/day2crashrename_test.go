// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// GitHub issue #1768: day2_crash on kind interrupts the create_before_destroy
// rename window, not only the multi-object apply. The window's body is
// shared by every kind estate, the way day2_replace's is:
// live/e2e/lib/gauntlet.sh's gauntlet_kind_day2_crash_rename. These tests
// hold the four scripts to calling it inside their day2_crash stage and to
// reporting what it measured in every day2_crash verdict they print, and
// hold the shared body to the shape #1768 ruled on: the verdict is the end
// state after one more apply (exactly the new object, the old one gone, the
// record's deposed entry cleared, then No changes), reached through both of
// the rerun's paths.

const crashRenameFunc = "gauntlet_kind_day2_crash_rename"

// TestDay2CrashKindEstatesInterruptTheRenameWindow reads every kubernetes-
// lane estate's script. Between `gauntlet_begin_stage day2_crash` and the
// script's first day2_crash verdict, it must call the shared body; and every
// day2_crash verdict line must carry what the body measured, so a pass
// cannot be printed over a window the run never interrupted.
func TestDay2CrashKindEstatesInterruptTheRenameWindow(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	verdict := regexp.MustCompile(`gauntlet_stage day2_crash (pass|fail) "`)
	seen := 0
	for _, e := range m.Estates {
		if e.Substrate() != SubstrateKind {
			continue
		}
		seen++
		raw, err := os.ReadFile(filepath.Join(root, e.ScriptPath()))
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		script := string(raw)
		begin := strings.Index(script, "gauntlet_begin_stage day2_crash")
		if begin < 0 {
			t.Errorf("%s: %s has no day2_crash stage", e.Name, e.ScriptPath())
			continue
		}
		first := verdict.FindStringIndex(script[begin:])
		if first == nil {
			t.Errorf("%s: %s reports no day2_crash verdict after the stage begins", e.Name, e.ScriptPath())
			continue
		}
		body := script[begin : begin+first[0]]
		if !strings.Contains(body, crashRenameFunc+" ") {
			t.Errorf("%s: day2_crash does not call %s before its first verdict, so the create_before_destroy rename window is not interrupted (#1768)", e.Name, crashRenameFunc)
		}
		for _, loc := range verdict.FindAllStringIndex(script, -1) {
			line := script[loc[0]:]
			if i := strings.IndexByte(line, '\n'); i >= 0 {
				line = line[:i]
			}
			if !strings.Contains(line, "$CRASH_RENAME_DETAIL") {
				t.Errorf("%s: a day2_crash verdict does not report the rename window ($CRASH_RENAME_DETAIL): %.120s...", e.Name, line)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no kubernetes-lane estate in the manifest; this test is checking nothing")
	}
}

// TestDay2CrashRenameBodyVerdictIsTheEndState reads the shared body. It
// must interrupt through the engine's own hook at -parallelism=1, check the
// interrupted position with kubectl and the record, and settle on the end
// state rather than the plan's wording: the old object gone, the new one
// remaining with the block's annotation, the record's deposed entry gone,
// and an empty replan. Both of the rerun's paths are exercised: the orphan
// destroy (same configuration) and the deposed destroy (the name read at
// plan time, #1539's shape, which only the record settles since #1683).
// And the Break line is the stage's: interrupt, assert nothing is
// proposed, and require that to fail.
func TestDay2CrashRenameBodyVerdictIsTheEndState(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh"))
	if err != nil {
		t.Fatal(err)
	}
	lib := string(raw)
	start := strings.Index(lib, "\n"+crashRenameFunc+"() {")
	if start < 0 {
		t.Fatalf("live/e2e/lib/gauntlet.sh defines no %s", crashRenameFunc)
	}
	// The body runs to the next function's header comment: a "}" at the
	// start of a line is no end marker here, because the body's own
	// heredocs write HCL that closes its blocks there.
	end := strings.Index(lib[start+1:], "\n# gauntlet_")
	if end < 0 {
		end = len(lib) - start - 1
	}
	body := lib[start : start+1+end]
	for _, want := range []struct{ text, why string }{
		{`TOFU_E2E_APPLY_RESOURCE_INTERRUPT=`, "the kill is the engine's own hook, the one the AWS half uses (#490)"},
		{`-parallelism=1`, "only at -parallelism=1 does the hook fire before the deposed destroy is dispatched"},
		{`deposed`, "the interrupted position is checked against the record's deposed entry"},
		{`orphan_`, "the same-configuration rerun destroys the old object at its orphan address"},
		{`(deposed object`, "the plan-time-name rerun destroys the old object as the address's deposed object (#1683)"},
		{`No changes.`, "the end state is an empty replan"},
		{`BREAK_CRASH`, "the stage's Break line covers this window too"},
		{`CRASH_RENAME_DETAIL=`, "what the body measured is handed to the caller's verdict"},
	} {
		if !strings.Contains(body, want.text) {
			t.Errorf("%s does not contain %q: %s", crashRenameFunc, want.text, want.why)
		}
	}
}
