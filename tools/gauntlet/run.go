// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogDir holds one stdout+stderr capture per estate run. Gitignored.
const LogDir = "live/gauntlet/logs"

// RunOptions controls a run.
type RunOptions struct {
	Names []string // estates to run; empty means the selected set
	Set   string   // "core", "all"; used when Names is empty
	Env   []string // extra KEY=VALUE for every script
	// Parallel is how many estates run concurrently, each against its own
	// isolated floci emulator (#437). <=1 means serial: the exact code path
	// this runner has always used - one estate at a time, in order - with
	// one addition since #520: every run this package launches, serial
	// included, is assigned an explicit FLOCI_PORT (flociPortEnv(0) for
	// serial, the same allocator -parallel N>1 uses per slot), so a plain
	// `gauntlet run` no longer leaves FLOCI_PORT unset for the script's own
	// hard-coded default to catch. Nothing else about the serial path
	// changed; #437's equivalence argument still holds for everything but
	// the port itself.
	Parallel int
	Stdout   io.Writer
}

// parallelPortBase and parallelPortStride assign each concurrent slot (not
// each estate - a slot is reused once its estate finishes) its own
// FLOCI_PORT, overriding the fixed default every live/e2e/*/run.sh falls
// back to when the variable is unset. The stride has to clear the largest
// offset any script derives FROM FLOCI_PORT today: FLOCI_PORT+2000
// (corpus-ecs-fargate, corpus-eks-basic, corpus-giantswarm-crossplane,
// corpus-evoteum-modules - see corpus-ecs-fargate/run.sh's own comment on
// why it jumped from 1/2 to 1000/2000 after a live collision at
// FLOCI_PORT+20). 5000 leaves headroom above that without the base climbing
// anywhere near a fixed script's own hard-coded default (4600-4800) or the
// 65535 ceiling for any parallelism this runner is actually asked for.
//
// #520: every run this package launches - serial included - is assigned a
// FLOCI_PORT from this same allocator (flociPortEnv below), not only
// -parallel N>1 runs. Before #520, a serial run (the default: a plain
// `gauntlet run <estate>`, or -parallel 1 explicitly) left FLOCI_PORT unset
// and so fell back to whatever constant that estate's own run.sh hard-coded
// - a fixed set of defaults allocated one apart while several scripts derive
// green/oracle ports as base+1 and base+2, so they land on neighbours'
// bases. Two estates only actually collide when they run at the same time
// on the same host, which a purely serial runner invocation never does to
// itself - but every worker in this repo's catch-up work invokes estates
// directly, several at once, and every one of them fell back to the same
// colliding defaults. Making the runner assign a port on every invocation,
// not just concurrent ones, means a script's own default is a fallback for
// hand-invocation only and never participates in a runner-launched run,
// concurrent or not.
const (
	parallelPortBase   = 20000
	parallelPortStride = 5000
)

// flociPortEnv returns the FLOCI_PORT=<port> environment entry for
// concurrency slot n (0 for the serial path, which only ever has one slot
// live at a time). It is the single source both runResults branches use, so
// the serial path's port and slot 0 of a parallel run are computed exactly
// the same way.
func flociPortEnv(slot int) string {
	return fmt.Sprintf("FLOCI_PORT=%d", parallelPortBase+slot*parallelPortStride)
}

// RunEstates executes each selected estate's script, parses the protocol,
// and updates the artifact in memory. It returns the number of scripts that
// exited non-zero. The caller saves and renders.
//
// emulator is stamped onto every row's LastRun exactly as commit already is:
// it is the pin the caller read from live/floci-image right before calling
// this (main.go's cmdRun), the same file each script itself reads to launch
// its own emulator (FLOCI_IMAGE defaults to `cat live/floci-image`, e.g.
// live/e2e/corpus-vpc-complete/run.sh:262) - so what gets recorded is what
// that run actually used, not a value borrowed from configuration at some
// later render. It is never read back out of a.Emulator here, on purpose:
// a.Emulator is what the NEXT run will use, which is a different fact than
// what THIS run used, even though the two are equal at the instant this
// function is called.
//
// Every row this call touches is also stamped with this call's own
// probeOracle() result (oracle.go, issue #544) - the stock terraform/tofu
// releases actually found on PATH, probed once here rather than threaded in
// like emulator: unlike the emulator digest, nothing forces the binaries a
// script finds on PATH to match live/oracle-versions.json's pin, so that
// pin would be configuration asserted as evidence, not evidence. See
// OracleVersions's doc comment (artifact.go).
func RunEstates(root string, m *Manifest, a *Artifact, opts RunOptions, commit, emulator string) (int, error) {
	oracle := probeOracle()
	var selected []Estate
	if len(opts.Names) > 0 {
		for _, n := range opts.Names {
			e, ok := m.ByName(n)
			if !ok {
				return 0, fmt.Errorf("estate %q is not in %s", n, ManifestPath)
			}
			selected = append(selected, e)
		}
	} else {
		for _, e := range m.Estates {
			if opts.Set == "core" && e.Set != SetCore {
				continue
			}
			selected = append(selected, e)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, LogDir), 0o755); err != nil {
		return 0, err
	}

	// Run every selected estate's script first, at whatever concurrency was
	// asked for, and merge results into the artifact afterwards in a single
	// thread, in `selected` order - the same order and the same per-estate
	// merge code the old purely-sequential loop used. That split is what
	// makes parallel mode a proven equivalence, not just a faster-looking
	// one: the concurrency lives entirely in runResults, and everything
	// below that reads runResults is textually the loop this function had
	// before #437, untouched.
	results := runResults(root, selected, opts)

	failures := 0
	for i, e := range selected {
		res, exit, elapsed, err := results[i].res, results[i].exit, results[i].elapsed, results[i].err
		r, _ := a.Result(e.Name)
		r.Name = e.Name
		if err != nil {
			return failures, err
		}
		if exit != 0 {
			failures++
		}
		// A stage's detail text is carried forward across runs the same
		// way its verdict already is (the loop just below): a script that
		// now fails or aborts at an EARLIER stage than a prior run reached
		// still only reports that earlier stage's line, and a bare
		// `r.LastRun.Detail = res.Detail` here would silently drop every
		// later stage's detail from the artifact even though its verdict
		// in r.Stages is untouched - found re-verifying
		// corpus-giantswarm-crossplane after the record-orphan-read sweep
		// (610511fb73): day2_rename regressed from pass to fail, the
		// script now exits before reaching day2_remove, and a
		// replace-not-merge Detail map turned day2_remove's rich,
		// previously-recorded wall text into an empty string although its
		// stage verdict correctly stayed "fail". Merging preserves stale
		// detail for a stage this run never reached, exactly like Stages
		// already does for the verdict itself.
		prevDetail := map[string]string{}
		prevSeconds := map[string]float64{}
		if r.LastRun != nil {
			for k, v := range r.LastRun.Detail {
				prevDetail[k] = v
			}
			for k, v := range r.LastRun.Seconds {
				prevSeconds[k] = v
			}
		}
		rowOracle := oracle
		r.LastRun = &LastRun{Commit: commit, Date: time.Now().UTC().Format(time.RFC3339), Emulator: emulator, Oracle: &rowOracle, ExitCode: exit, DurationS: roundSeconds(elapsed)}
		if res.Spoken {
			if r.Stages == nil {
				r.Stages = map[string]string{}
			}
			for id, v := range res.Stages {
				r.Stages[id] = v
			}
			for id, v := range res.Detail {
				prevDetail[id] = v
			}
			for id, v := range res.Seconds {
				prevSeconds[id] = v
			}
			if len(prevDetail) > 0 {
				r.LastRun.Detail = prevDetail
			}
			if len(prevSeconds) > 0 {
				r.LastRun.Seconds = prevSeconds
			}
			r.Protocol = ProtocolGauntlet
			if len(res.Stages) == 0 {
				// Spoke the protocol (printed GAUNTLET protocol=1) but died
				// before a single GAUNTLET stage= line - e.g. failed the
				// step-0 tool/corpus check (the exact shape of #497:
				// gauntlet.yml never installed the real tofu binary eight
				// estates hard-require). r.Stages above is unchanged from
				// the prior run in this case (the merge loop ran zero
				// times), so without recordRunnerFailure below, every
				// existing verdict, including a full pass, would be carried
				// forward untouched while Commit/Date/ExitCode stamp
				// fresh - exactly the stale-carry-forward shape
				// TestNonzeroExitCodeImpliesAFailingStage
				// (gauntlet_test.go) exists to catch, and did catch (#497).
				// recordRunnerFailure writes a real fail into the earliest
				// active stage so the guard passes honestly instead of
				// being relaxed; every other stage's carried-forward
				// verdict is left untouched, because this run genuinely
				// says nothing new about them.
				failedStage := recordRunnerFailure(&r, exit)
				fmt.Fprintf(opts.Stdout, "%s: script spoke the gauntlet protocol but reported no stage verdicts this run; recorded %s=%s as a runner/environment failure (not a product regression), every other stage carried forward untouched, exit code %d noted\n", e.Name, failedStage, VerdictFail, exit)
			}
			for _, u := range res.Unknown {
				fmt.Fprintf(opts.Stdout, "%s: reported unknown stage %q; add it to tools/gauntlet/stages.go or fix the script\n", e.Name, u)
			}
		} else {
			// Legacy script: verdicts stay as imported; only the run is recorded.
			if r.Protocol == "" {
				r.Protocol = ProtocolLegacy
			}
			fmt.Fprintf(opts.Stdout, "%s: script does not speak the gauntlet protocol (source live/e2e/lib/gauntlet.sh); verdicts left as recorded, exit code %d noted\n", e.Name, exit)
		}
		a.SetResult(r)
		fmt.Fprintf(opts.Stdout, "%s: exit %d, %s\n", e.Name, exit, summarize(r.Stages))
	}
	return failures, nil
}

// recordRunnerFailure marks r as having failed the earliest active stage
// (today: cold_deploy, the stage every script confirms before anything
// else) with a detail line that names this as a runner/environment
// failure rather than a product regression. It is called only from the
// res.Spoken && len(res.Stages) == 0 branch above: the script spoke the
// gauntlet protocol but died before its first GAUNTLET stage= line, so
// r.Stages was left exactly as the prior run recorded it and would
// otherwise carry a stale "pass" forward under a freshly stamped
// commit/date/exit_code (#497).
//
// It returns the stage id it wrote, for the caller's own log line.
//
// r.Stages must already be non-nil (RunEstates's res.Spoken branch
// guarantees this before calling) and r.LastRun must already be set; both
// are asserted implicitly by writing straight into them, matching every
// other write in this file's merge loop.
func recordRunnerFailure(r *EstateResult, exit int) string {
	active := ActiveStages()
	if len(active) == 0 {
		// Stages() always declares cold_deploy active; this is defensive,
		// not a reachable case today.
		return ""
	}
	id := active[0].ID
	r.Stages[id] = VerdictFail
	if r.LastRun.Detail == nil {
		r.LastRun.Detail = map[string]string{}
	}
	r.LastRun.Detail[id] = fmt.Sprintf("RUNNER: script spoke the gauntlet protocol but reported zero stage verdicts this run (exit code %d) - environment/setup failure before stage 1, not a product regression; every other stage below is carried forward from an earlier run, not reconfirmed this run", exit)
	return id
}

// oneResult is what runOne produced for one estate; runResults collects one
// per selected estate, indexed the same way selected is.
type oneResult struct {
	res     *ProtocolResult
	exit    int
	elapsed float64
	err     error
}

// runResults runs every estate in selected and returns their oneResults in
// the same order, regardless of the order they actually finish in.
//
// opts.Parallel <=1 runs them one at a time, in order, on the calling
// goroutine - textually the same loop this function replaced, so serial
// mode's behaviour (including "stop dispatching more scripts after the
// first hard runOne error", which the merge loop in RunEstates still relies
// on) is unchanged apart from the one thing #520 adds: each call now also
// gets an explicit FLOCI_PORT (flociPortEnv(0), the same value slot 0 of a
// parallel run would get), instead of leaving FLOCI_PORT unset the way
// serial mode did before #520. Nothing else about the serial code path
// moved - #437's equivalence argument (see RunOptions.Parallel) rests on
// that.
//
// opts.Parallel >1 runs up to that many scripts at once. Each concurrent
// slot (not each estate - a slot is handed back to the pool and reused the
// moment its estate's script exits) gets a FLOCI_PORT of its own
// (parallelPortBase + slot*parallelPortStride), so N estates really do run
// against N isolated emulators: every live/e2e/*/run.sh already derives
// every other port it needs (green, oracle, adopt, ...) from FLOCI_PORT, and
// already names its floci container from its own process ID ($$), which two
// concurrently running bash processes never share. Nothing here dispatches
// fewer scripts than serial mode would on a runOne error - a script that is
// already running is left to finish and tear its own container down rather
// than killed, so a slot's container is never abandoned mid-life; the first
// error is still surfaced, in `selected` order, by RunEstates's merge loop.
func runResults(root string, selected []Estate, opts RunOptions) []oneResult {
	results := make([]oneResult, len(selected))
	parallel := opts.Parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(selected) {
		parallel = len(selected)
	}
	if parallel <= 1 {
		for i, e := range selected {
			res, exit, elapsed, err := runOne(root, e, opts, []string{flociPortEnv(0)})
			results[i] = oneResult{res, exit, elapsed, err}
			if err != nil {
				// Matches the pre-#437 loop exactly: a hard runOne error
				// (not a script exiting non-zero, which is not an error
				// here - a Go-level failure like the script file missing)
				// stops dispatching further scripts, the same way
				// RunEstates's merge loop below stops merging further
				// results the moment it reaches this index.
				break
			}
		}
		return results
	}

	// runOne's first line of output goes to opts.Stdout before its script
	// even starts; with several goroutines calling it at once that write
	// needs serializing, or two "running ..." lines racing on the same
	// io.Writer.Write call could interleave mid-line. Everything else
	// RunEstates prints happens after runResults returns, back on a single
	// goroutine, so this is the only writer that needs it.
	runOpts := opts
	runOpts.Stdout = &syncWriter{w: opts.Stdout}

	slots := make(chan int, parallel)
	for i := 0; i < parallel; i++ {
		slots <- i
	}
	var wg sync.WaitGroup
	for i, e := range selected {
		i, e := i, e
		slot := <-slots
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { slots <- slot }()
			env := []string{flociPortEnv(slot)}
			res, exit, elapsed, err := runOne(root, e, runOpts, env)
			results[i] = oneResult{res, exit, elapsed, err}
		}()
	}
	wg.Wait()
	return results
}

// syncWriter serializes concurrent writes to an underlying io.Writer.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

// setEnv returns env with any existing entry for kv's key removed and kv
// appended, so exactly one entry for that key survives. Relying on "the
// last of two duplicate keys wins" in a process's environment is not
// portable enough to trust for #437's per-slot FLOCI_PORT: it must be
// observed unconditionally, not merely in whichever order a given libc or
// shell happens to scan envp.
func setEnv(env []string, kv string) []string {
	key := kv
	if i := strings.IndexByte(kv, '='); i >= 0 {
		key = kv[:i+1]
	} else {
		key += "="
	}
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if strings.HasPrefix(e, key) {
			continue
		}
		out = append(out, e)
	}
	return append(out, kv)
}

// runOne runs one estate's script and returns its parsed protocol result,
// its exit code, and the wall-clock seconds the process itself took (from
// just before cmd.Run() to just after it returns - includes the script's
// own setup and teardown, not just the stage work inside it, which is the
// honest answer to "how long did running this estate take"). extraEnv is
// applied after opts.Env, one setEnv per entry, so a caller's own -env
// value is only overridden for a key extraEnv actually names (#437's
// per-slot FLOCI_PORT).
func runOne(root string, e Estate, opts RunOptions, extraEnv []string) (*ProtocolResult, int, float64, error) {
	script := filepath.Join(root, e.ScriptPath())
	if _, err := os.Stat(script); err != nil {
		return nil, 0, 0, fmt.Errorf("estate %q: %w", e.Name, err)
	}
	logPath := filepath.Join(root, LogDir, e.Name+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, 0, 0, err
	}
	defer logf.Close()

	var captured bytes.Buffer
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), opts.Env...)
	for _, kv := range extraEnv {
		cmd.Env = setEnv(cmd.Env, kv)
	}
	cmd.Stdout = io.MultiWriter(&captured, logf)
	cmd.Stderr = logf
	fmt.Fprintf(opts.Stdout, "%s: running %s (log: %s)\n", e.Name, e.ScriptPath(), filepath.Join(LogDir, e.Name+".log"))
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Seconds()
	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return nil, 0, elapsed, fmt.Errorf("estate %q: %w", e.Name, runErr)
		}
	}
	res, err := ParseProtocol(&captured)
	if err != nil {
		return nil, exit, elapsed, fmt.Errorf("estate %q: %w", e.Name, err)
	}
	return res, exit, elapsed, nil
}

// roundSeconds rounds to one decimal place: enough resolution to see a
// stage that takes a few seconds without printing false precision off a
// process-wide wall clock that also includes OS scheduling noise.
func roundSeconds(s float64) float64 {
	return math.Round(s*10) / 10
}

func summarize(stages map[string]string) string {
	var parts []string
	for _, s := range Stages() {
		if s.Status != StatusActive {
			continue
		}
		parts = append(parts, s.ID+"="+stages[s.ID])
	}
	return strings.Join(parts, " ")
}
