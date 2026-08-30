// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// RunLiveCert runs live/live-cert/<estate>.sh (or LIVECERT_SCRIPT_OVERRIDE
// for a test) and records the result into a.LiveCert. It is the one place a
// live-aws certification result gets written, so every safety rail lives
// here once rather than in each caller:
//
//   - target must be "floci" or "aws". "aws" refuses outright unless
//     confirm is exactly "yes" (LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY,
//     read by main.go's flag parsing, not here, so this function has no env
//     var of its own to bypass) - belt and suspenders with the shell
//     script's own identical gate (live/live-cert/reference-ec2-vpc.sh).
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
func RunLiveCert(root string, estate, target, region string, ceilingUSD float64, ceilingSeconds int, confirm string) (*LiveCertResult, *ProtocolResult, int, error) {
	if target != "floci" && target != "aws" {
		return nil, nil, 0, fmt.Errorf("target must be floci or aws, got %q", target)
	}
	if target == "aws" && confirm != "yes" {
		return nil, nil, 0, fmt.Errorf("target=aws needs -confirm yes (from LIVECERT_I_UNDERSTAND_THIS_SPENDS_REAL_MONEY); refusing before starting anything")
	}
	script := LiveCertScript(estate)
	if override := os.Getenv("LIVECERT_SCRIPT_OVERRIDE"); override != "" {
		script = override
	}
	full := filepath.Join(root, script)
	if _, err := os.Stat(full); err != nil {
		return nil, nil, 0, fmt.Errorf("no live-cert script for estate %q (%s): %w", estate, script, err)
	}

	ctx, cancel := commandTimeoutContext(ceilingSeconds)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", full)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TARGET="+target, "REGION="+region)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out

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
	r.Clear = liveCertClear(r.Stages)
	return &r, res, exit, nil
}
