// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// commandTimeoutContext bounds a live-cert run at the process level, from
// Go's side, independent of live/live-cert/run.sh's own `timeout` wrapper -
// #440's brief asks for the wall-clock ceiling enforced more than one way,
// not trusted to a single mechanism. ceilingSeconds <= 0 means no Go-side
// bound (the shell wrapper's `timeout` is still there; this is defense in
// depth, not the only layer).
func commandTimeoutContext(ceilingSeconds int) (context.Context, context.CancelFunc) {
	if ceilingSeconds <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), time.Duration(ceilingSeconds)*time.Second)
}

// LiveCertScript is where a real-AWS certification script lives for a given
// estate, mirroring Estate.ScriptPath's convention for live/e2e/*/run.sh.
// Only reference-ec2-vpc exists today (issue #440, ruled 2026-08-29: this
// one estate, $5/run ceiling); a second estate needs its own ruling before
// its own script is added here.
func LiveCertScript(estate string) string {
	return filepath.Join("live", "live-cert", estate+".sh")
}

// LiveCertScopeStages is the stage subset a live-AWS certification measures:
// the same four stages #440's brief scopes to (cold_deploy, migrate,
// test_plan, test_apply) - the stock-apply-then-adopt core, not the full
// day-2 suite live/e2e/*/run.sh exercises against the emulator. A stage id
// here must already exist in Stages() (stages.go): the MEANING of
// "cold_deploy" etc. does not change between an emulator row and a
// live-aws row, only what backs the verdict does - see LiveCertResult's own
// doc comment.
func LiveCertScopeStages() []string {
	return []string{"cold_deploy", "migrate", "test_plan", "test_apply"}
}

// LiveCertResult is one real-AWS certification run for one estate (issue
// #440) - never the emulator, never a repeatable comparison against stock
// the way an EstateResult row is. It answers a categorically different
// question than a.Estates does: not "does choudoufu match stock against the
// pinned emulator, re-measurable any time," but "did THIS ONE run, against a
// REAL account, on THIS ONE date, verify what the emulator already agreed
// to." HANDOFF.md's "What a measurement is worth" section is exactly this
// distinction one layer up (an emulator agreeing with itself proves a
// shared code path, not correctness against a real account); #440 asks that
// the reverse also hold - a live-AWS pass must never be silently averaged
// into the emulator-driven headline bars as if it were more of the same
// evidence.
//
// It therefore never appears as a row in Artifact.Estates and never carries
// Protocol == ProtocolLiveAWS on an EstateResult (TestArtifactAgreesWithManifest
// rejects that value there on purpose - see ProtocolLiveAWS's own comment).
// It lives in its own top-level Artifact.LiveCert slice, which
// Artifact.Rebuild never reads or writes, so it can never be summed into
// Sets["core"]/Sets["all"] - the exact conflation #440's Accept criterion
// forbids.
type LiveCertResult struct {
	Estate     string            `json:"estate"`
	Protocol   string            `json:"protocol"` // always ProtocolLiveAWS
	Target     string            `json:"target"`   // "aws" for a real run; "floci" only ever appears from a Stage-1 proving run, never committed as a live-aws-labelled result (see RunLiveCert)
	Region     string            `json:"region"`
	CeilingUSD float64           `json:"ceiling_usd"`
	Stages     map[string]string `json:"stages"`
	Clear      bool              `json:"clear"` // every id in LiveCertScopeStages() passed
	Commit     string            `json:"commit"`
	Date       string            `json:"date"`
	ExitCode   int               `json:"exit_code"`
	Detail     map[string]string `json:"detail,omitempty"`
	DurationS  float64           `json:"duration_s,omitempty"`
	// Seconds is per-stage wall-clock seconds, the exact same meaning as
	// LastRun.Seconds (artifact.go) carries for an emulator row: stage id ->
	// that stage's own duration_s, read off the script's "GAUNTLET
	// stage=... duration_s=..." line (protocol.go) and computed
	// unconditionally by gauntlet_stage regardless of pass or fail
	// (live/e2e/lib/gauntlet.sh - the delta-timer runs on every call, a
	// failing stage included). Absent until this field existed: ParseProtocol
	// has always computed res.Seconds from the script's own stdout, but
	// RunLiveCert below discarded it, so every LiveCertResult recorded
	// before this field was added has no way to recover a stage's true wall
	// duration - scalerecord.go's BuildScaleRecordFromLiveCert leaves such a
	// stage's own Seconds absent rather than substituting the stage's
	// inner-operation timing (OperationSeconds there), which answers a
	// different question and is exactly the defect issue #1051/#1053's
	// wall-time-accounting unit found (a stage's "seconds" silently meaning
	// the inner op's time, not the stage's own duration, so a published
	// breakdown could not be summed against its own total).
	Seconds map[string]float64 `json:"stage_seconds,omitempty"`
}

// liveCertClear reports whether every scoped stage passed. Mirrors isClear
// in artifact.go, against LiveCertScopeStages() instead of HeadlineStages() -
// deliberately its own function rather than a shared helper, so a future
// change to what counts as "clear" for the emulator bars cannot silently
// start counting toward a live-aws row's Clear too, or vice versa.
func liveCertClear(stages map[string]string) bool {
	for _, id := range LiveCertScopeStages() {
		if stages[id] != VerdictPass {
			return false
		}
	}
	return true
}

// LiveCertResultFor returns a's row for estate, if any.
func (a *Artifact) LiveCertResultFor(estate string) (LiveCertResult, bool) {
	for _, r := range a.LiveCert {
		if r.Estate == estate {
			return r, true
		}
	}
	return LiveCertResult{}, false
}

// SetLiveCertResult replaces or appends estate's row in a.LiveCert. It never
// touches a.Estates or a.Sets - see LiveCertResult's own doc comment.
func (a *Artifact) SetLiveCertResult(r LiveCertResult) {
	for i := range a.LiveCert {
		if a.LiveCert[i].Estate == r.Estate {
			a.LiveCert[i] = r
			return
		}
	}
	a.LiveCert = append(a.LiveCert, r)
}

// RecordsLiveCert reports whether a run's result should be written to
// live/gauntlet.json at all (issue #1100).
//
// `live_cert` keeps one row per estate, so writing to it REPLACES the last
// certification outright - there is no earlier version to fall back to. A run
// the harness refused before it started creates nothing, spends nothing and
// speaks no stage, and must not be what replaces a run that did all three.
//
// It happened on 2026-09-13. A scale-136 attempt refused at one of the
// live-cert gates of the time - the script's own log line is "nothing has
// been created" - and overwrote the 3,705-resource real-AWS row, three stages
// of evidence with throttle and retry counts, with a bare exit-2 record
// carrying no detail. The runner printed "recorded live-aws certification",
// which reads as progress. Those gates are gone (#1102); this guard is not,
// because a refusal for any later reason must still not displace a
// certification.
//
// Spoken is the existing answer: false when a script emitted no GAUNTLET
// line. RunEstates already gates on it for the same reason (run.go). This is
// deliberately NOT a filter on failure - a run that spoke and failed is
// evidence and is recorded exactly as before.
//
// A refusal is the second answer (#1151), and it is the same rule seen from
// the other side. Once a run can say "I declined this rung, and here is the
// arithmetic" (ProtocolResult.Refusal), it may well speak a stage or two
// first - a ceiling hit at migrate leaves a real cold_deploy behind it - so
// Spoken alone stops being enough. A refused run's evidence goes to
// live/gauntlet-scale.json, which is keyed by (estate, target, SCALE) and
// can hold the refused rung beside the measured one; live_cert is keyed by
// estate alone and has room for exactly one certification, so a refusal at
// scale 136 must not be what replaces a certification at scale 50.
func RecordsLiveCert(res *ProtocolResult) bool {
	return res != nil && res.Spoken && res.Refusal == nil
}

// LiveCertWrites is what a finished run writes, and why. Split out of
// cmdLiveCert so the decision can be tested without a checkout, a manifest
// or a render: the two "do not write" cases are the ones that have gone
// wrong before, and both of them lose a real-AWS certification when they go
// wrong (#1100, #1151).
type LiveCertWrites struct {
	// LiveCertRow: write the live_cert row in live/gauntlet.json.
	LiveCertRow bool
	// ScaleRecord: write the (estate, target, scale) row in
	// live/gauntlet-scale.json, subject to that file's own superseding rule
	// and to the run naming a scale at all.
	ScaleRecord bool
	// Why is one sentence for the human, whenever something is NOT written.
	Why string
}

// PlanLiveCertWrites decides what a finished run records.
func PlanLiveCertWrites(target string, res *ProtocolResult) LiveCertWrites {
	if target != "aws" {
		return LiveCertWrites{Why: "target=floci: this is Stage-1 proving evidence only; NOT written to live/gauntlet.json (RunLiveCert never records a floci run)"}
	}
	if res == nil || !res.Spoken {
		return LiveCertWrites{Why: fmt.Sprintf("the run spoke no stage, so nothing was measured - %s left unchanged rather than overwriting the last certification (#1100)", ArtifactPath)}
	}
	if res.Refusal != nil {
		return LiveCertWrites{
			ScaleRecord: true,
			Why: fmt.Sprintf("the run REFUSED this rung, so %s is left unchanged - a refusal must not replace a certification (#1151). The refusal itself is recorded in %s, which is keyed by scale and can hold it beside the rung below.",
				ArtifactPath, ScaleRecordsPath),
		}
	}
	return LiveCertWrites{LiveCertRow: true, ScaleRecord: true}
}

// RunLiveCert runs live/live-cert/<estate>.sh (or LIVECERT_SCRIPT_OVERRIDE
// for a test) and records the result into a.LiveCert. It is the one place a
// live-aws certification result gets written, so every safety rail lives
// here once rather than in each caller:
//
//   - target must be "floci" or "aws".
//   - a "floci" run is never written with Protocol == ProtocolLiveAWS: it
//     is Stage 1 proof (does the harness work at all), not Stage 2 evidence
//     (did a real account verify this), and the two must never be
//     confusable in the committed artifact - RunLiveCert returns the parsed
//     result to the caller without calling SetLiveCertResult at all when
//     target is "floci", so a Stage-1 proving run cannot end up in
//     live/gauntlet.json no matter what the caller does next.
//   - the process is bounded by ceilingSeconds via exec.CommandContext, a
//     second, independent enforcement alongside live/live-cert/run.sh's own
//     `timeout` wrapper (the brief's "not just an in-script check") and the
//     account-level AWS Budgets alarm that is infrastructure, not code.
//   - only one run per estate at a time, held by a lock file beside the log
//     (livecertlock.go, #1150). A second run refuses, naming the first's run
//     id, pid and start time, because the two would truncate each other's log
//     and race each other's artifact write.
func RunLiveCert(root string, estate, target, region string, ceilingUSD float64, ceilingSeconds int) (*LiveCertResult, *ProtocolResult, int, error) {
	if target != "floci" && target != "aws" {
		return nil, nil, 0, fmt.Errorf("target must be floci or aws, got %q", target)
	}
	script := LiveCertScript(estate)
	if override := os.Getenv("LIVECERT_SCRIPT_OVERRIDE"); override != "" {
		script = override
	}
	full := filepath.Join(root, script)
	if _, err := os.Stat(full); err != nil {
		return nil, nil, 0, fmt.Errorf("no live-cert script for estate %q (%s): %w", estate, script, err)
	}

	// The run lock (#1150), taken before anything is opened or started and
	// held for the whole run. root == "" is the in-process test caller
	// (LIVECERT_SCRIPT_OVERRIDE with no checkout): it has no
	// live/gauntlet/logs to lock in and writes no log either, so there is
	// nothing for a second run to truncate - the same condition the log
	// block below is guarded by, for the same reason.
	if root != "" {
		lock, err := AcquireLiveCertLock(root, estate, target, region)
		if err != nil {
			return nil, nil, 0, err
		}
		fmt.Printf("live-cert %s: holding %s as run %s (pid %d)\n", estate, lock.Path(), lock.RunID(), os.Getpid())
		defer func() {
			if rerr := lock.Release(); rerr != nil {
				fmt.Fprintln(os.Stderr, rerr)
			}
		}()
	}

	ctx, cancel := commandTimeoutContext(ceilingSeconds)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", full)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TARGET="+target, "REGION="+region)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

	// Keep the script's own output, streamed to a file as it is produced
	// (issue #578).
	//
	// It used to go nowhere but this strings.Builder, which ParseProtocol
	// reads for GAUNTLET lines and which is then dropped on the floor. That
	// discards almost everything a live-AWS run learns: the per-stage timing
	// the script logs, the throttle summary, the account inventory
	// verify_empty enumerates, the sweep's own account of what it deleted -
	// all of it survives only as whatever fits in a stage's one-line detail
	// string. For a run that spends real money, once, and cannot be cheaply
	// repeated, that is the wrong thing to throw away; and with no file to
	// tail, there is no way to watch a 45-minute run's progress either.
	//
	// Best-effort: a log that cannot be opened must not stop a certification
	// that is otherwise ready to go. RunEstates writes its per-estate logs
	// to the same gitignored directory (run.go's LogDir), under a
	// live-cert- prefix here so a certification's log can never be mistaken
	// for, or overwrite, the emulator row's log for the same estate.
	// root == "" is the in-process test caller (LIVECERT_SCRIPT_OVERRIDE with
	// no checkout); it must not create live/gauntlet/logs/ relative to
	// whatever the test's working directory happens to be.
	if root != "" && os.MkdirAll(filepath.Join(root, LogDir), 0o755) == nil {
		logPath := filepath.Join(root, LogDir, "live-cert-"+estate+".log")
		if logf, err := os.Create(logPath); err == nil { //nolint:gosec // a gitignored path under the checkout, built from the estate name
			defer func() { _ = logf.Close() }()
			cmd.Stdout = io.MultiWriter(&out, logf)
			cmd.Stderr = cmd.Stdout
		}
	}

	// cmd.Cancel/cmd.WaitDelay: exec.CommandContext's DEFAULT behavior on
	// context expiry is cmd.Process.Kill() - a bare SIGKILL, immediately,
	// with no grace period. That is a real safety gap for a live-AWS
	// script specifically: every estate script's teardown (the destroy +
	// independent listing + raw-CLI sweep this package's own doc comments
	// describe as the thing that makes a live-AWS run safe) runs from a
	// `trap teardown EXIT INT TERM` inside the script - and SIGKILL cannot
	// be trapped, at all, by design. A run that hits THIS ceiling with the
	// default behavior leaves whatever the script had created up to that
	// moment running and billing in the real account with nobody notified,
	// which is a strictly worse failure than a slow run: it fails silently
	// where live/live-cert/run.sh's own `timeout --signal=TERM
	// --kill-after=30` wrapper (this function's OWN doc comment above
	// calls it "a second, independent enforcement alongside run.sh's own
	// timeout wrapper", implying the two are equivalent - they are not)
	// fails loudly, with the script's own trap given a chance to tear down
	// first. Confirmed the hard way running `gauntlet live-cert -target
	// aws` directly against the terralith-scale estate (issue #567,
	// 2026-08-30) at a scale whose four stages alone take longer than this
	// function's 900s default: the process was SIGKILLed mid-stage, the
	// script's teardown never ran, and every resource it had created (28
	// IAM roles, 24 policies, 24 instance profiles, an ECS cluster and its
	// services, a Route53 zone, a VPC/subnet/security group) was left live
	// in the account, found and manually swept only because the
	// independent post-run AWS CLI verification this issue's own brief
	// requires caught it - the account-level Budgets alarm exists as the
	// backstop for exactly this case, but reaching it is a near-miss, not
	// a success. cmd.Cancel below overrides the kill with a SIGTERM (the
	// SAME signal the script's own trap handles), and cmd.WaitDelay gives
	// it the SAME 30s grace period run.sh's wrapper does before Go itself
	// falls back to SIGKILL - the two independent ceilings now agree on
	// HOW they stop the process, not only decide the process, matching
	// this file's own claim that they are independent enforcement of the
	// SAME safety property.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 30 * time.Second

	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Seconds()
	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if ctx.Err() != nil {
			exit = -1 // killed by the ceiling, not a normal exit
		} else {
			return nil, nil, 0, fmt.Errorf("estate %q: %w", estate, runErr)
		}
	}
	res, err := ParseProtocol(strings.NewReader(out.String()))
	if err != nil {
		return nil, res, exit, fmt.Errorf("estate %q: %w", estate, err)
	}

	r := LiveCertResult{
		Estate: estate, Protocol: ProtocolLiveAWS, Target: target, Region: region,
		CeilingUSD: ceilingUSD, Stages: res.Stages, Commit: headCommit(root),
		Date: time.Now().UTC().Format(time.RFC3339), ExitCode: exit, Detail: res.Detail,
		DurationS: roundSeconds(elapsed),
	}
	if len(res.Seconds) > 0 {
		r.Seconds = res.Seconds
	}
	r.Clear = liveCertClear(r.Stages)
	return &r, res, exit, nil
}
