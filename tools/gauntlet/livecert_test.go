// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProtocolLiveAWSNeverValidOnEstateResult proves #440's structural
// separation is load-bearing, not merely documented: an EstateResult
// carrying Protocol == ProtocolLiveAWS must be rejected by the exact
// predicate TestArtifactAgreesWithManifest uses. Per HANDOFF.md's "prove
// your checks can fail" rule, this is written from what the separation
// promises (a live-aws row never counts as a valid emulator row), not from
// the implementation, and it fails on purpose if IsValidEstateProtocol is
// ever loosened to accept it.
func TestProtocolLiveAWSNeverValidOnEstateResult(t *testing.T) {
	if IsValidEstateProtocol(ProtocolLiveAWS) {
		t.Fatal("ProtocolLiveAWS must never be a valid EstateResult.Protocol value - a live-aws certification belongs in Artifact.LiveCert, never a row in Artifact.Estates (see LiveCertResult's doc comment, livecert.go)")
	}
	rogue := EstateResult{Name: "reference-ec2-vpc", Protocol: ProtocolLiveAWS}
	if IsValidEstateProtocol(rogue.Protocol) {
		t.Fatal("a rogue EstateResult carrying ProtocolLiveAWS was accepted - TestArtifactAgreesWithManifest would silently let a live-aws verdict into the emulator-driven manifest rows")
	}
	// The two protocols an EstateResult DOES legitimately carry must still
	// be accepted - this function is a narrow exclusion, not a blanket
	// refusal that would make the positive case above vacuous.
	for _, p := range []string{ProtocolGauntlet, ProtocolLegacy} {
		if !IsValidEstateProtocol(p) {
			t.Errorf("IsValidEstateProtocol(%q) = false, want true", p)
		}
	}
}

// TestRebuildNeverTouchesLiveCert is #440 blocker 3's core claim, proven
// directly: populating a.LiveCert with a passing certification must not
// change a single number Rebuild computes from a.Estates - not the per-set
// Estates/Clear counts, not any stage's Tally. If it did, a live-aws pass
// would be silently inflating the "N of 25 core estates clear" headline bar
// exactly the way the brief warns against.
func TestRebuildNeverTouchesLiveCert(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}

	baseline := &Artifact{}
	baseline.Rebuild(m, nil, "sha256:baseline", OracleVersions{})

	withLiveCert := &Artifact{
		LiveCert: []LiveCertResult{{
			Estate: "reference-ec2-vpc", Protocol: ProtocolLiveAWS, Target: "aws",
			Region: "us-east-1", CeilingUSD: 5,
			Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
			Clear:  true, Date: "2026-08-29T00:00:00Z",
		}},
	}
	withLiveCert.Rebuild(m, nil, "sha256:baseline", OracleVersions{})

	if len(withLiveCert.LiveCert) != 1 || withLiveCert.LiveCert[0].Estate != "reference-ec2-vpc" {
		t.Fatalf("Rebuild must never modify a.LiveCert; got %+v", withLiveCert.LiveCert)
	}
	for key := range SetLabels {
		b, w := baseline.Sets[key], withLiveCert.Sets[key]
		if b.Estates != w.Estates || b.Clear != w.Clear {
			t.Errorf("set %q: adding a passing LiveCert row changed Estates/Clear from %d/%d to %d/%d - a live-aws certification is counting toward the emulator-driven headline bar", key, b.Clear, b.Estates, w.Clear, w.Estates)
		}
		for id, bt := range b.Stages {
			wt := w.Stages[id]
			if bt != wt {
				t.Errorf("set %q stage %q: tally changed from %+v to %+v after adding a LiveCert row", key, id, bt, wt)
			}
		}
	}
}

// TestLiveCertClearScopedToFourStages: liveCertClear must key off exactly
// LiveCertScopeStages() (cold_deploy, migrate, test_plan, test_apply, per
// #440's brief), never HeadlineStages() - a live cert says nothing about
// day2_rename/day2_remove/day2_count/day2_replace/greenfield, so those
// stages being absent or failing in a LiveCertResult's Stages map must not
// affect Clear.
func TestLiveCertClearScopedToFourStages(t *testing.T) {
	allFour := map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass}
	if !liveCertClear(allFour) {
		t.Fatal("all four scoped stages passing should be clear")
	}
	for _, id := range LiveCertScopeStages() {
		cp := map[string]string{}
		for k, v := range allFour {
			cp[k] = v
		}
		cp[id] = VerdictFail
		if liveCertClear(cp) {
			t.Errorf("stage %q failing should make liveCertClear false", id)
		}
	}
	// A stage OUTSIDE the scope failing (or simply absent) must not affect
	// the verdict - proves the scope is exactly four stages, not
	// accidentally every stage in the registry.
	outside := map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass, "day2_rename": VerdictFail}
	if !liveCertClear(outside) {
		t.Fatal("a failing day2_rename must not affect a live cert's Clear - it is outside LiveCertScopeStages()")
	}
}

// TestRunLiveCertSendsSIGTERMOnCeiling proves the exact defect found running
// issue #567 against real AWS (2026-08-30) is fixed: calling `gauntlet
// live-cert -target aws` directly (not through live/live-cert/run.sh's own
// `timeout --signal=TERM --kill-after=30` wrapper) at an estate whose
// pipeline outran the Go-side ceilingSeconds got its process SIGKILLed -
// exec.CommandContext's default behavior on context expiry - which cannot be
// trapped, so the script's own `trap teardown EXIT INT TERM` never ran and
// every real-AWS resource it had created up to that point was abandoned,
// live and billing, found only by this issue's own independent post-run AWS
// CLI check. Written from what RunLiveCert PROMISES (the script gets a
// SIGTERM, the same signal its own trap already handles, not an unblockable
// kill), not from the implementation: a script here that ignores SIGTERM
// entirely would time out this test via *testing.T's own deadline, not pass
// it by other means.
func TestRunLiveCertSendsSIGTERMOnCeiling(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "sigterm-received")
	script := filepath.Join(dir, "estate.sh")
	// Traps SIGTERM and writes the marker file before exiting - if
	// RunLiveCert's ceiling instead sent SIGKILL (the pre-fix behavior),
	// this trap would never run and the marker would never appear, and
	// this test would time out waiting on RunLiveCert to return (a plain
	// SIGKILL is instantaneous, so a hang here specifically implicates a
	// SIGTERM that never arrived, not a slow trap). The backgrounded sleep
	// redirects its own stdin/stdout/stderr away from what it would
	// otherwise inherit from this script - a first draft left them
	// inherited, and cmd.Run() then blocked for the FULL 10s regardless of
	// how fast the trap fired: Go's os/exec waits for the output pipe to
	// see EOF, and an orphaned grandchild that still holds the pipe's
	// write end open (because it inherited it) keeps that EOF from ever
	// arriving even after this script's own process has already exited -
	// a well-known os/exec gotcha, not a signal-handling problem, but easy
	// to mistake for one here since it silently made the test's timing
	// meaningless (the marker appeared "in time" only because the assertion
	// runs after cmd.Run() returns, whenever that ends up being).
	body := "#!/usr/bin/env bash\n" +
		"trap 'touch \"" + marker + "\"; exit 0' TERM\n" +
		"sleep 10 </dev/null >/dev/null 2>&1 &\n" +
		"wait $!\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // test fixture script, not a secret
		t.Fatal(err)
	}

	t.Setenv("LIVECERT_SCRIPT_OVERRIDE", script)
	// The maintainer-run-guard (maintainerguard.go) refuses a local
	// live-cert run with no allow file - correct, but not what this test is
	// about, so it opts out the same way CI itself does rather than faking
	// an allow file this test has no other reason to manage.
	t.Setenv("GITHUB_ACTIONS", "true")
	// target=floci (never requires -confirm) and a 1-second ceiling: the
	// script sleeps for 10s, so RunLiveCert's ceiling fires almost
	// immediately, well before the sleep would exit on its own - any
	// marker file the assertion below finds was written BECAUSE of the
	// ceiling's own signal, not because the script merely finished.
	if _, _, exit, err := RunLiveCert("", "unused-estate-name", "floci", "us-east-1", 5, 1, ""); err != nil {
		t.Fatalf("RunLiveCert returned an error: %v", err)
	} else if exit != -1 {
		t.Errorf("exit = %d, want -1 (killed by the ceiling, per RunLiveCert's own doc comment)", exit)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("marker file was never written - the script's SIGTERM trap never ran, meaning RunLiveCert did not send SIGTERM on ceiling expiry (stat error: %v)", statErr)
	}
}

// TestRunLiveCertCapturesPerStageSeconds is the wall-time-accounting unit's
// own proof of its first two fixes (issue #1051/#1053): RunLiveCert must
// carry a stage's own duration_s through to LiveCertResult.Seconds, for a
// PASSING stage and a FAILING one alike - gauntlet_stage's delta-timer
// (live/e2e/lib/gauntlet.sh) computes duration_s unconditionally, so a
// stage that fails still reports one, and RunLiveCert must not discard it
// the way it silently did before this fix (see LiveCertResult.Seconds's own
// doc comment - res.Seconds existed in ParseProtocol's output all along;
// nothing before this change ever kept it).
//
// Written from what RunLiveCert now PROMISES, not from its
// implementation: temporarily reverting the `r.Seconds = res.Seconds`
// assignment in RunLiveCert (livecert.go) makes this test fail with
// `Seconds map is nil` - see this worker's report for that RED output,
// quoted verbatim.
func TestRunLiveCertCapturesPerStageSeconds(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "estate.sh")
	// Speaks the exact GAUNTLET protocol shape live/e2e/lib/gauntlet.sh
	// emits: duration_s present on both a pass and a fail line, the second
	// stage failing WITHOUT naming a duration in its own detail text (the
	// same shape terralith-scale's real scale=50 test_plan failure has) -
	// duration_s is still there because gauntlet_stage computes it before
	// ever looking at the verdict.
	body := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=2035 detail=3705 resources ... in 2023s\\n'\n" +
		"printf 'GAUNTLET stage=test_plan verdict=fail duration_s=1867 detail=the post-migrate plan is not empty\\n'\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil { //nolint:gosec // test fixture script, not a secret
		t.Fatal(err)
	}

	t.Setenv("LIVECERT_SCRIPT_OVERRIDE", script)
	t.Setenv("GITHUB_ACTIONS", "true") // opt out of the maintainer-run-guard, same as TestRunLiveCertSendsSIGTERMOnCeiling

	r, _, exit, err := RunLiveCert("", "unused-estate-name", "floci", "us-east-1", 5, 30, "")
	if err != nil {
		t.Fatalf("RunLiveCert returned an error: %v", err)
	}
	if exit != 1 {
		t.Fatalf("exit = %d, want 1 (the script's own gauntlet_stage fail exits non-zero)", exit)
	}
	if r.Seconds == nil {
		t.Fatal("Seconds map is nil - RunLiveCert discarded the protocol's own duration_s readings (the exact defect issue #1051/#1053's wall-time-accounting unit fixes)")
	}
	if got, ok := r.Seconds["cold_deploy"]; !ok || got != 2035 {
		t.Errorf(`Seconds["cold_deploy"] = %v (ok=%v), want 2035`, got, ok)
	}
	if got, ok := r.Seconds["test_plan"]; !ok || got != 1867 {
		t.Errorf(`Seconds["test_plan"] = %v (ok=%v), want 1867 - a FAILING stage must still carry its own wall duration`, got, ok)
	}
}

// TestBoardLiveCertIsSeparate: the board carries live-cert evidence in its
// own field, and adding it leaves every other field (the estate rows, the
// stage table, the banners - everything feeding {{< gauntlet-bars >}} and
// the estate table) unaffected - a live-aws row must never be conflated
// with emulator rows "anywhere they both appear ... including the rendered
// progress page" (#440's own wording). The site's prose beside the table
// says in words that these rows never count toward the bars.
func TestBoardLiveCertIsSeparate(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	without := &Artifact{}
	without.Rebuild(m, nil, "sha256:test", OracleVersions{})
	withoutBoard := buildBoard(m, without)
	if len(withoutBoard.LiveCert) != 0 {
		t.Fatal("buildBoard must carry no live-cert rows when a.LiveCert is empty")
	}

	with := &Artifact{LiveCert: []LiveCertResult{{
		Estate: "reference-ec2-vpc", Protocol: ProtocolLiveAWS, Target: "aws",
		Region: "us-east-1", CeilingUSD: 5, Clear: true, Date: "2026-08-29T00:00:00Z",
		Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
	}}}
	with.Rebuild(m, nil, "sha256:test", OracleVersions{})
	withBoard := buildBoard(m, with)

	if len(withBoard.LiveCert) != 1 || withBoard.LiveCert[0].Estate != "reference-ec2-vpc" || withBoard.LiveCert[0].Region != "us-east-1" {
		t.Fatalf("live-cert row not carried as expected: %+v", withBoard.LiveCert)
	}

	// Every OTHER field must be identical with and without live cert - the
	// addition must be purely additive, never editing existing evidence.
	withBoard.LiveCert = withoutBoard.LiveCert
	a, _ := withBoard.Canonical()
	b, _ := withoutBoard.Canonical()
	if string(a) != string(b) {
		t.Fatal("a.LiveCert changed board content OUTSIDE its own field - the separation is not purely additive")
	}
}
