// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

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
	// State is what state the run reached (#1324): "finished", or one of
	// the two signalled states. An exit code cannot express the
	// difference - a wrapper's 143 and a finished run's 0 are both "the
	// process is gone" - and only one of them means the estate is down.
	// PlanLiveCertWrites refuses to write a row at all for a signalled
	// run, so this field never carries a signalled value into
	// live/gauntlet.json; it is here so that a row read back out always
	// says, in its own text, that it came from a run that finished.
	State LiveCertRunState `json:"state,omitempty"`
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

// PlanLiveCertWrites decides what a finished run records. The live_cert half
// of the answer is RecordsLiveCert's, called rather than restated, so the
// rule has one definition and the two cannot drift apart.
//
// state is what state the run reached (#1324), and it is checked first,
// before anything else this function knows about. A signalled run writes
// NOTHING: not the live_cert row, not the scale row. It stopped partway
// through with an unknown amount of the estate still standing, and every
// field either file holds - stage verdicts, resource counts, seconds - is a
// claim about a run that went all the way through. The stages such a run did
// speak before the signal are still in its log, which is where evidence that
// is not a measurement belongs.
//
// The parameter is required rather than defaulted because that is the
// difference between this and an rc file. A caller that has no idea what
// state its run reached has to say RunStateRunning and be refused, not pass
// nothing and be believed.
func PlanLiveCertWrites(target string, res *ProtocolResult, state LiveCertRunState) LiveCertWrites {
	if target != "aws" {
		return LiveCertWrites{Why: "target=floci: this is Stage-1 proving evidence only; NOT written to live/gauntlet.json (RunLiveCert never records a floci run)"}
	}
	if state != RunStateFinished {
		return LiveCertWrites{Why: fmt.Sprintf("the run did not finish - %s. Nothing is written to %s or %s: a run stopped partway through measured nothing, and a row saying otherwise is exactly the 143-reads-as-success defect #1324 was filed for. The stages it spoke before it stopped are in its log, and %s/live-cert-<estate>.run.json says what state it reached.",
			state.Human(), ArtifactPath, ScaleRecordsPath, LogDir)}
	}
	w := LiveCertWrites{LiveCertRow: RecordsLiveCert(res), ScaleRecord: true}
	if w.LiveCertRow {
		return w
	}
	switch {
	case res != nil && res.Refusal != nil:
		// A refusal has somewhere to go: a rung of its own.
		w.Why = fmt.Sprintf("the run REFUSED, so %s is left unchanged - a refusal must not replace a certification (#1151). The refusal itself is recorded in %s: on its own rung when the run named a scale, which holds it beside the rung below, and on that file's estate-level `refusals` shelf when the estate declares no scale ladder at all (#1233).", ArtifactPath, ScaleRecordsPath)
	default:
		// A run that spoke nothing measured nothing, at any scale.
		w.ScaleRecord = false
		w.Why = fmt.Sprintf("the run spoke no stage, so nothing was measured - %s left unchanged rather than overwriting the last certification (#1100)", ArtifactPath)
	}
	return w
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

	// The provenance stamp is resolved BEFORE the script starts (#1149).
	//
	// It used to be resolved after, from a headCommit that swallowed git's
	// error and returned "", and an empty Commit surfaced downstream as
	// "built an invalid scale record: missing required field(s): commit" -
	// after the run had already spent its hours and its money. Every outcome
	// of asking git late is worse than asking early: the run still costs
	// what it costs, the operator debugs a record builder instead of a
	// toolchain, and the artifact ends up holding one half of the evidence.
	// Asking here costs one `git rev-parse` and refuses a run that could not
	// have been recorded honestly anyway. It also pins the commit to the
	// tree the run STARTED from, which is the tree it actually measured.
	//
	// It comes before the run lock below, and the order is deliberate: this
	// refusal is one `git rev-parse` and it refuses a run that could never
	// have been recorded, so there is no reason to take a lock - and then
	// have to release it - on its behalf. A lock taken and dropped again in
	// the same millisecond is also one more window in which a crash leaves a
	// stale lock a human has to clear.
	commit, err := headCommit(root)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("estate %q: refusing to start a live-cert whose provenance commit cannot be resolved (nothing has been created, nothing spent): %w", estate, err)
	}

	// The run lock (#1150), taken before anything is opened or started and
	// held for the whole run. root == "" is the in-process test caller
	// (LIVECERT_SCRIPT_OVERRIDE with no checkout): it has no
	// live/gauntlet/logs to lock in and writes no log either, so there is
	// nothing for a second run to truncate - the same condition the log
	// block below is guarded by, for the same reason.
	runID := ""
	if root != "" {
		lock, lerr := AcquireLiveCertLock(root, estate, target, region)
		if lerr != nil {
			return nil, nil, 0, lerr
		}
		runID = lock.RunID()
		fmt.Printf("live-cert %s: holding %s as run %s (pid %d)\n", estate, lock.Path(), lock.RunID(), os.Getpid())
		defer func() {
			if rerr := lock.Release(); rerr != nil {
				fmt.Fprintln(os.Stderr, rerr)
			}
		}()
	}

	// exec.Command, NOT exec.CommandContext, and the ceiling is enforced
	// by the supervisor below instead (#1324).
	//
	// CommandContext ties cmd.WaitDelay to the context: the delay timer
	// starts when the context is done, and when it expires Wait KILLS the
	// process. So the ceiling path was - ceiling fires, cmd.Cancel sends
	// the group a SIGTERM, the trap starts tearing down, and 30 seconds
	// later Wait SIGKILLs bash out from under it. Measured with a stub
	// whose teardown outlasts the delay: the trap never finished and the
	// run recorded `state="finished"`. That is this issue's defect on the
	// one path HANDOFF and live-cert.yml both call the safe way to stop a
	// certification, and 30 seconds is not a number any real teardown fits
	// in.
	//
	// With no context, WaitDelay keeps only its other job, which is the
	// one worth keeping: its timer also starts when the child has EXITED,
	// bounding a grandchild that has outlived the script and is holding
	// the output pipe open. By then there is no teardown left to cut
	// short.
	cmd := exec.Command("bash", full) //nolint:gosec // a script path under the checkout, resolved above
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "TARGET="+target, "REGION="+region)

	// Setpgid (#1324): bash leads its own process group, so the script,
	// the `terraform plan` it is blocked on and its own background jobs
	// can all be signalled with ONE kill to the negative pgid. Signalling
	// bash alone is not the same thing - the #1324 process listing has a
	// `terraform plan` two levels down that nothing had signalled - and
	// there is no other way to reach a grandchild whose pid this process
	// never learned.
	//
	// The cost of a new process group is that the script no longer
	// receives a terminal's Ctrl-C directly, since that goes to the
	// foreground group. That is deliberate: forwarding below is now the
	// ONLY way a signal reaches the script, which means this process
	// always knows a stop request happened and always gets to wait for the
	// teardown it started. The alternative - both of us receiving the
	// signal independently - is how the harness ends up exiting out from
	// under a trap that is still running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// syncWriter (run.go): the exec package copies the child's output on
	// its own goroutine, and the supervisor below writes its stop-request
	// lines into the same place from another. A strings.Builder is not
	// safe for that, and neither is an *os.File's offset.
	var out strings.Builder
	// The watcher sits between the script and everything else, so it sees
	// the trap's own first line the moment it is written (#1324): the
	// supervisor re-sends SIGTERM to the group until that line appears,
	// and must never re-send after it.
	watch := newTrapWatcher(&out)
	sink := &syncWriter{w: watch}
	cmd.Stdout = sink
	cmd.Stderr = sink

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
			watch = newTrapWatcher(io.MultiWriter(&out, logf))
			sink = &syncWriter{w: watch}
			cmd.Stdout = sink
			cmd.Stderr = sink
		}
	}

	// The ceiling still stops a run that outruns it, and it still does so
	// with a SIGTERM the script's own trap handles rather than an
	// untrappable kill - the property issue #567 paid for, when a
	// `gauntlet live-cert -target aws` run was SIGKILLed mid-stage and
	// left 28 IAM roles, 24 policies, 24 instance profiles, an ECS cluster
	// and its services, a Route53 zone and a VPC live in a real account.
	// What changed with #1324 is only WHO enforces it: the supervisor
	// below, which sends that SIGTERM to the whole process group and then
	// waits for the trap, instead of exec.CommandContext, which sent it to
	// bash alone and then killed bash 30 seconds later.
	//
	// Bounds only the post-exit case described above; it can no longer
	// reach a teardown that is still running.
	cmd.WaitDelay = liveCertWaitDelay

	// The run record (#1324), cleared before the run and stamped as it
	// goes. Cleared first for scripts/ci-gate.sh's reason: a record left
	// over from the previous run is worse than no record, because a reader
	// believes it. root == "" is the in-process test caller, which has no
	// checkout to write into.
	rec := LiveCertRun{
		State: RunStateRunning, Estate: estate, Target: target, Region: region,
		Commit: commit, Pid: os.Getpid(), RunID: runID,
		StartedUTC: time.Now().UTC().Format(time.RFC3339),
	}
	writeRec := func() {
		if root == "" {
			return
		}
		if err := WriteLiveCertRun(root, rec); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	if root != "" {
		if err := ClearLiveCertRun(root, estate); err != nil {
			return nil, nil, 0, err
		}
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, nil, 0, fmt.Errorf("estate %q: %w", estate, err)
	}
	// With Setpgid the child IS its own group leader, so its pid is the
	// pgid. Read once, here, rather than through cmd.Process later: after
	// Wait returns, cmd.Process.Pid is a pid that may already have been
	// reused, and signalling a reused pgid is worse than not signalling.
	pgid := cmd.Process.Pid
	rec.PGID = pgid
	writeRec()

	sup := &liveCertSupervisor{}
	say := sayTo(sink)
	sigc := make(chan os.Signal, 8)
	// SIGHUP is in this list so that closing a terminal does not kill this
	// process in the middle of a teardown it is waiting for, and SIGPIPE
	// so that a write to a dead stdout does not either: a Go program that
	// has not asked for SIGPIPE dies when a write to fd 1 or 2 gets one,
	// and `gauntlet live-cert ... | tee` is exactly that shape. The
	// supervisor drops SIGPIPE and treats the other three as stop
	// requests.
	signal.Notify(sigc, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGPIPE)
	done := make(chan struct{})
	watcher := make(chan struct{})
	go func() {
		defer close(watcher)
		superviseLiveCert(sup, superviseOpts{
			PGID:        pgid,
			Sigc:        sigc,
			Done:        done,
			Ceiling:     time.Duration(ceilingSeconds) * time.Second,
			Bound:       liveCertSignalBound(),
			Tick:        liveCertWaitTick(),
			Say:         say,
			TrapStarted: watch.Started,
			OnStop: func(signalName string) {
				// Stamped BEFORE the wait that may never
				// return: the record on disk has to be true at
				// the worst moment, which is while the estate
				// is still coming down and this process could
				// itself be killed.
				rec.State = RunStateSignalledUnconfirmed
				rec.Signal = signalName
				writeRec()
			},
		})
	}()

	// This is the line #1324 is about: the wait happens AFTER the signal
	// handler is installed, and nothing above it exits early. A signalled
	// run blocks here until bash's `trap teardown EXIT INT TERM` has
	// finished, or until the supervisor's grace period gives up and says
	// so out loud.
	runErr := cmd.Wait()
	close(done)
	<-watcher
	signal.Stop(sigc)

	elapsed := time.Since(start).Seconds()
	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return nil, nil, 0, fmt.Errorf("estate %q: %w", estate, runErr)
		}
	}
	if sup.HitCeiling() {
		// -1 is what this function has always reported for a run its
		// own ceiling stopped, and it still means the same thing: not
		// a normal exit. The script now exits through its own trap, so
		// without this the trap's exit code would read as an ordinary
		// one.
		exit = -1
	}

	// Teardown's verdict comes from teardown's own line, never from the
	// exit code and never from this tool's opinion of how the wait ended.
	confirmed := TeardownConfirmed(out.String())
	signalled, signalName, escalated := sup.Report()
	state := sup.State(confirmed)
	rec.State, rec.Signal, rec.Escalated = state, signalName, escalated
	rec.RepeatSignals = sup.Repeats()
	rec.TrapResends = sup.Resends()
	// The ORDER of the re-sends against the trap's answer, not only the
	// count (#1464). Compared here on the monotonic clock; the RFC3339
	// stamps are for a reader of the record, and for the test that puts
	// them beside the script's own stamp of when its trap began.
	answered := watch.AnsweredAt()
	if !answered.IsZero() {
		rec.TrapAnsweredUTC = answered.UTC().Format(time.RFC3339Nano)
	}
	for _, at := range sup.ResendTimes() {
		rec.TrapResendsUTC = append(rec.TrapResendsUTC, at.UTC().Format(time.RFC3339Nano))
		if !answered.IsZero() && !at.Before(answered) {
			rec.TrapResendsAfterAnswer++
		}
	}
	if signalled && !answered.IsZero() {
		say("live-cert: the script's trap answered at %s, %d re-send(s) went out before that answer and %d after it. A re-send before the answer is the stop request being repeated to a script that had not reached its trap yet; one after it landed on a teardown in progress, which must never happen (#1324, #1464).",
			rec.TrapAnsweredUTC, rec.TrapResends-rec.TrapResendsAfterAnswer, rec.TrapResendsAfterAnswer)
	}
	rec.TeardownConfirmed = confirmed
	rec.ExitCode = exit
	rec.Note = state.Human()
	writeRec()
	if signalled {
		fmt.Printf("live-cert %s: %s\n", estate, state.Human())
		if root != "" {
			fmt.Printf("live-cert %s: run record %s\n", estate, LiveCertRunPath(root, estate))
		}
	}

	res, err := ParseProtocol(strings.NewReader(out.String()))
	if err != nil {
		return nil, res, exit, fmt.Errorf("estate %q: %w", estate, err)
	}

	r := LiveCertResult{
		Estate: estate, Protocol: ProtocolLiveAWS, Target: target, Region: region,
		CeilingUSD: ceilingUSD, Stages: res.Stages, Commit: commit,
		Date: time.Now().UTC().Format(time.RFC3339), ExitCode: exit, Detail: res.Detail,
		DurationS: roundSeconds(elapsed), State: state,
	}
	if len(res.Seconds) > 0 {
		r.Seconds = res.Seconds
	}
	r.Clear = liveCertClear(r.Stages)
	return &r, res, exit, nil
}
