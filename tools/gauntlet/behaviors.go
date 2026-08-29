// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// BehaviorIndexPath is the tier-1 behavior-matrix catalogue (issue #522's
// ruling): the ~30 purpose-built live/e2e/ scripts that sit beside the 26
// board estates, each exercising one seam with a minimal, cheap fixture
// rather than a whole third-party estate.
//
// Unlike ArtifactPath, this one file plays both roles gauntlet.json and
// estates.json split between them: the catalogue fields (Category, Seam,
// Shapes, IdentityKind, ...) are hand-curated exactly like estates.json's
// manifest entries, and LastRun on each fixture is written only by
// RunBehaviors, exactly like an estate row's own last_run. There is no
// generator that produces the catalogue half from source - it is knowledge
// about what each script tests, gathered by reading it, the same way
// estates.json's own "reason" field is.
const BehaviorIndexPath = "live/behaviors.json"

// Behavior categories. Only "shape" fixtures are the tier-1 behavior matrix
// the #522 ruling formalizes; "adoption" and "legacy-demo" are catalogued
// because they are real scripts under live/e2e/, but they measure something
// else and are excluded from `gauntlet behaviors`'s default run.
const (
	// CategoryShape: a small, purpose-built fixture (2-6 resources) that
	// exercises one seam - a count block, a for_each map, a module nesting,
	// an identity kind - built for the test rather than adopted from a real
	// repository. This is the #522 tier-1 candidate set.
	CategoryShape = "shape"
	// CategoryAdoption: a real third-party estate pulled from .corpus/ and
	// crossed for cold-adoption discovery (issue #274's campaign). HANDOFF.md
	// is explicit that cold adoption "is a feature with its own ladder. It
	// is not part of the promise, and a number about it is not a number
	// about the product" - so these are catalogued, not folded into the
	// tier-1 matrix.
	CategoryAdoption = "adoption"
	// CategoryLegacyDemo: live/e2e/run.sh, the original pre-protocol,
	// pre-board "stateless mode" demo. It predates both the stage protocol
	// and the estate board, and its own steps (standup, adopt, drift,
	// rename, count scale-down, block removal via the estate-block fixture,
	// receipts, teardown) overlap almost every shape fixture and
	// reference-ec2-vpc combined. live-markers.md's verification-budget
	// section records it at "several minutes" - alone, on the wrong side of
	// the #522 five-minute bar - so it is catalogued but not run by default.
	CategoryLegacyDemo = "legacy-demo"
)

// BehaviorLastRun is one fixture's most recent recorded run, written only by
// RunBehaviors - the same rule EstateResult.LastRun follows for estates.
type BehaviorLastRun struct {
	Commit    string  `json:"commit"`
	Date      string  `json:"date"`
	ExitCode  int     `json:"exit_code"`
	Verdict   string  `json:"verdict"` // pass, fail, or not_run (never attempted)
	DurationS float64 `json:"duration_s"`
}

// BehaviorFixture is one catalogued live/e2e/ script (or, for a fixture
// embedded in a larger script, one identifiable seam inside it).
type BehaviorFixture struct {
	// ID is the fixture's name: the live/e2e/ directory for a standalone
	// script, or a synthetic id (e.g. "estate-block") for a shape embedded
	// in a larger legacy script.
	ID string `json:"id"`
	// Script is the run.sh this fixture's evidence actually comes from,
	// relative to the repo root. For an embedded fixture this is the HOST
	// script, not a script of the fixture's own - see Runnable below.
	Script string `json:"script"`
	// Category is one of the Category* constants above.
	Category string `json:"category"`
	// Runnable is false when the fixture has no independent entry point of
	// its own (it is one step inside Script, folded in AS-IS per the #522
	// ruling rather than extracted - extracting it would be re-authoring an
	// existing script, which this foundation unit does not do).
	Runnable bool `json:"runnable"`
	// RunnableNote explains a false Runnable, or any other caveat about
	// invoking this fixture directly.
	RunnableNote string `json:"runnable_note,omitempty"`
	// Runner is true when `gauntlet behaviors` includes this fixture in its
	// default run - Runnable CategoryShape fixtures, and nothing else. Kept
	// as its own field (rather than derived at call time) so the artifact
	// itself states plainly which fixtures the five-minute bar is measured
	// over.
	Runner bool `json:"runner"`
	// Seam is a one-paragraph, human-written description of the specific
	// claim this fixture proves - what would have to be true for it to pass
	// vacuously, and how it rules that out. Free text, not derived.
	Seam string `json:"seam"`
	// Shapes are the mandatory-shape tags this fixture satisfies, from the
	// #522 ruling's list: "count", "for_each", "module-nested", "scalar".
	Shapes []string `json:"shapes,omitempty"`
	// IdentityKind is one of the ruling's three identity kinds this fixture
	// exercises, when its seam is about identity resolution at all:
	// "server-minted", "server-minted-untaggable" (the record rung's own
	// case - ClassRecordLocated - which the ruling's three-kind list does
	// not name but which today's fixtures do exercise), "deterministic", or
	// "none" (no server id at all, provable only by absence). Empty when the
	// fixture's seam is not primarily about identity kind.
	IdentityKind string `json:"identity_kind,omitempty"`
	// Resources is the rough instance count the fixture stands up, counted
	// by reading the script (a heredoc-written .tf, an adjacent main.tf, or
	// - for an adoption estate - the count its own header comment states).
	Resources int `json:"resources,omitempty"`
	// Needs lists external dependencies beyond `go build`: "docker",
	// "aws-cli", "corpus" (a populated .corpus/ checkout).
	Needs []string `json:"needs,omitempty"`
	// DefaultPort is the FLOCI_PORT the script falls back to when the
	// runner does not override it (measured from the script's own source,
	// not its header comment, which has gone stale before - see #522's PR
	// body). Zero for a fixture that needs no floci emulator at all
	// (record-store) or is not independently runnable (estate-block).
	DefaultPort int `json:"default_port,omitempty"`
	// Stage is the gauntlet stage id (tools/gauntlet/stages.go) this
	// fixture is representative-set evidence for. Empty means unmapped:
	// the #522 ruling's "mandatory shapes per stage" cell assignment is
	// the NEXT unit, not this one, so every fixture ships with this blank
	// and BehaviorsProven (artifact.go) is computed accordingly - a stage
	// with no fixture mapped to it is not proven, honestly, rather than
	// vacuously true.
	Stage string `json:"stage,omitempty"`
	// LastRun is set only by RunBehaviors, never by hand.
	LastRun *BehaviorLastRun `json:"last_run,omitempty"`
}

// BehaviorIndex is the top-level live/behaviors.json document.
type BehaviorIndex struct {
	Comment  []string          `json:"_comment,omitempty"`
	Fixtures []BehaviorFixture `json:"fixtures"`
}

// ByID returns the fixture with the given id, or false.
func (bi *BehaviorIndex) ByID(id string) (BehaviorFixture, bool) {
	if bi == nil {
		return BehaviorFixture{}, false
	}
	for _, f := range bi.Fixtures {
		if f.ID == id {
			return f, true
		}
	}
	return BehaviorFixture{}, false
}

// SetFixture replaces or appends a fixture by id.
func (bi *BehaviorIndex) SetFixture(f BehaviorFixture) {
	for i := range bi.Fixtures {
		if bi.Fixtures[i].ID == f.ID {
			bi.Fixtures[i] = f
			return
		}
	}
	bi.Fixtures = append(bi.Fixtures, f)
}

// LoadBehaviorIndex reads live/behaviors.json. A missing file is an empty
// index, matching LoadArtifact's rule for a first run.
func LoadBehaviorIndex(root string) (*BehaviorIndex, error) {
	b, err := os.ReadFile(filepath.Join(root, BehaviorIndexPath))
	if os.IsNotExist(err) {
		return &BehaviorIndex{}, nil
	}
	if err != nil {
		return nil, err
	}
	var bi BehaviorIndex
	if err := json.Unmarshal(b, &bi); err != nil {
		return nil, fmt.Errorf("%s: %w", BehaviorIndexPath, err)
	}
	return &bi, nil
}

// Canonical encodes the index the one way this tool writes it.
func (bi *BehaviorIndex) Canonical() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(bi); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SaveBehaviorIndex writes live/behaviors.json.
func SaveBehaviorIndex(root string, bi *BehaviorIndex) error {
	b, err := bi.Canonical()
	if err != nil {
		return err
	}
	p := filepath.Join(root, BehaviorIndexPath)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

// BehaviorsLogDir mirrors LogDir for estates: one stdout+stderr capture per
// fixture, gitignored.
const BehaviorsLogDir = "live/gauntlet/logs/behaviors"

// DefaultBehaviorsPort is the single FLOCI_PORT every fixture in a serial
// (`-parallel 1`) `gauntlet behaviors` run is given, whatever its own
// script-level default. #522 originally asked for "ONE shared emulator"
// across the whole matrix, run strictly one fixture at a time - never two
// containers bound to the same host port at once, and never a script
// modified to skip container management, which the #522 ruling's "fold in
// AS-IS" forecloses; #541 made concurrency (behaviorsParallelPortBase) the
// default instead, since a serial sum of ten scripts left the five-minute
// bar a four-second margin. Serial mode, and this port, still exist for
// debugging one fixture's timing in isolation without the noise of nine
// others running at once. Picked clear of every fixture's individual
// hard-coded default (4599-4800, see each fixture's DefaultPort) and clear
// of the 20000+ range #520's `-parallel` estate runner assigns, so a
// `gauntlet behaviors` run and a `gauntlet run -parallel` run never collide
// even if both happen to be live at once.
const DefaultBehaviorsPort = 4900

// defaultBehaviorsParallel is `gauntlet behaviors -parallel`'s own default
// (main.go's cmdBehaviors) when the flag is not passed at all - concurrent
// by default (#541), not 1 like `gauntlet run`'s equivalent flag: an estate
// run is heavier and has an existing CI dependency on serial-by-default,
// where the tier-1 matrix has neither yet (it is wired into no `just`
// recipe or CI step - #541's own context) and #522's whole argument for it
// is that it has to stay a fast development loop. 8 covers the whole
// current 10-fixture default set in two waves without assuming every future
// addition still fits one; a caller measuring a specific fixture's own
// timing in isolation passes -parallel 1.
const defaultBehaviorsParallel = 8

// behaviorsParallelPortBase and behaviorsParallelPortStride mirror
// parallelPortBase/parallelPortStride (run.go, #525's per-slot allocator):
// each concurrent slot gets its own FLOCI_PORT
// (behaviorsParallelPortBase + slot*behaviorsParallelPortStride), clear of
// every fixture's own hard-coded default (4599-4900, see each fixture's
// DefaultPort and DefaultBehaviorsPort above) and clear of the estate
// runner's 20000+ range (parallelPortBase in run.go), so a `gauntlet
// behaviors -parallel` run and a `gauntlet run -parallel` run never collide
// even if both are live at once. No shape fixture derives a second port
// from FLOCI_PORT by offset the way some estate scripts do (checked against
// every live/e2e/*/run.sh's own FLOCI_PORT usage, issue #541), so - unlike
// run.go's 5000 - a narrow stride is enough; 50 leaves headroom above the
// one container plus incidental local ports (AWS CLI, docker) each fixture
// actually opens.
const (
	behaviorsParallelPortBase   = 25000
	behaviorsParallelPortStride = 50
)

// BehaviorsRunOptions controls RunBehaviors.
type BehaviorsRunOptions struct {
	// Names restricts the run to these fixture ids; empty means every
	// fixture with Runner true.
	Names []string
	All   bool // include every runnable fixture regardless of Runner
	Port  int  // FLOCI_PORT for a serial run; 0 means DefaultBehaviorsPort. Ignored when Parallel > 1.
	// Parallel is how many fixtures run concurrently, each against its own
	// floci emulator on its own port (behaviorsParallelPortBase +
	// slot*behaviorsParallelPortStride). <=1 means serial against one
	// shared port (Port, or DefaultBehaviorsPort) - the exact code path
	// this runner used before #541, so a plain `gauntlet behaviors` with
	// no -parallel flag is unaffected byte-for-byte by parallel mode
	// existing at all. Every fixture in the default tier-1 set already
	// starts and tears down its own `docker run --rm` / `docker rm -f`
	// container named from its own process id ($$, unique per concurrently
	// running bash process), so N fixtures really do run against N
	// isolated emulators - the same precondition #525 established for
	// concurrent estates.
	Parallel int
	Env      []string
	Stdout   io.Writer
}

// behaviorResult is what runOneBehavior produced for one fixture; used by
// both the serial and parallel paths so the merge step below (bi.SetFixture
// + the per-fixture log line) is identical regardless of how the run
// happened.
type behaviorResult struct {
	f       BehaviorFixture
	exit    int
	elapsed float64
	err     error
}

// runOneBehavior runs one fixture's script to completion and reports its
// exit code and wall-clock seconds. It does not touch bi or opts.Stdout's
// per-fixture summary line - callers do that after every fixture in a run
// has finished, on a single goroutine, so concurrent runs merge exactly
// like a serial one would (mirrors run.go's runOne/runResults split).
func runOneBehavior(root string, f BehaviorFixture, env []string, port int, stdout io.Writer) behaviorResult {
	if !f.Runnable {
		return behaviorResult{f: f, err: fmt.Errorf("fixture %q is not independently runnable (%s)", f.ID, f.RunnableNote)}
	}
	script := filepath.Join(root, f.Script)
	if _, err := os.Stat(script); err != nil {
		return behaviorResult{f: f, err: fmt.Errorf("fixture %q: %w", f.ID, err)}
	}
	logPath := filepath.Join(root, BehaviorsLogDir, f.ID+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		return behaviorResult{f: f, err: err}
	}
	defer logf.Close()

	cmd := exec.Command("bash", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = setEnv(cmd.Env, fmt.Sprintf("FLOCI_PORT=%d", port))
	cmd.Stdout = logf
	cmd.Stderr = logf
	fmt.Fprintf(stdout, "%s: running %s (log: %s)\n", f.ID, f.Script, filepath.Join(BehaviorsLogDir, f.ID+".log"))
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start).Seconds()

	exit := 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return behaviorResult{f: f, err: fmt.Errorf("fixture %q: %w", f.ID, runErr)}
		}
	}
	return behaviorResult{f: f, exit: exit, elapsed: elapsed}
}

// behaviorResults runs every fixture in selected and returns their results
// in the same order, regardless of the order they actually finish in.
// Structured exactly like run.go's runResults: opts.Parallel <=1 runs them
// one at a time on the calling goroutine against one shared port; >1 runs
// up to that many at once, each on its own port from a channel-backed slot
// pool, with opts.Stdout wrapped in a syncWriter so two goroutines' first
// "running ..." lines cannot interleave mid-line.
func behaviorResults(root string, selected []BehaviorFixture, opts BehaviorsRunOptions) []behaviorResult {
	results := make([]behaviorResult, len(selected))
	parallel := opts.Parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(selected) {
		parallel = len(selected)
	}
	if parallel <= 1 {
		port := opts.Port
		if port == 0 {
			port = DefaultBehaviorsPort
		}
		for i, f := range selected {
			results[i] = runOneBehavior(root, f, opts.Env, port, opts.Stdout)
		}
		return results
	}

	stdout := &syncWriter{w: opts.Stdout}
	slots := make(chan int, parallel)
	for i := 0; i < parallel; i++ {
		slots <- i
	}
	var wg sync.WaitGroup
	for i, f := range selected {
		i, f := i, f
		slot := <-slots
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { slots <- slot }()
			port := behaviorsParallelPortBase + slot*behaviorsParallelPortStride
			results[i] = runOneBehavior(root, f, opts.Env, port, stdout)
		}()
	}
	wg.Wait()
	return results
}

// RunBehaviors runs each selected fixture's script once - serially against
// one shared FLOCI_PORT, or concurrently against one port per slot when
// opts.Parallel > 1 - and records pass/fail + wall-clock seconds into bi in
// memory. The caller saves and renders, exactly like RunEstates.
//
// It returns the number of fixtures that exited non-zero.
func RunBehaviors(root string, bi *BehaviorIndex, opts BehaviorsRunOptions, commit string) (int, error) {
	if err := os.MkdirAll(filepath.Join(root, BehaviorsLogDir), 0o755); err != nil {
		return 0, err
	}

	var selected []BehaviorFixture
	if len(opts.Names) > 0 {
		for _, n := range opts.Names {
			f, ok := bi.ByID(n)
			if !ok {
				return 0, fmt.Errorf("fixture %q is not in %s", n, BehaviorIndexPath)
			}
			selected = append(selected, f)
		}
	} else {
		for _, f := range bi.Fixtures {
			if opts.All {
				if f.Runnable {
					selected = append(selected, f)
				}
				continue
			}
			if f.Runner {
				selected = append(selected, f)
			}
		}
	}

	// Run every selected fixture's script first, at whatever concurrency
	// was asked for, and merge results into bi afterwards in a single
	// thread, in `selected` order - the same split run.go's RunEstates
	// uses, and for the same reason: it makes parallel mode a proven
	// equivalence to serial mode's bookkeeping, not just a faster-looking
	// one.
	results := behaviorResults(root, selected, opts)

	failures := 0
	for _, r := range results {
		if r.err != nil {
			return failures, r.err
		}
		f := r.f
		verdict := VerdictPass
		if r.exit != 0 {
			verdict = VerdictFail
			failures++
		}
		f.LastRun = &BehaviorLastRun{
			Commit:    commit,
			Date:      time.Now().UTC().Format(time.RFC3339),
			ExitCode:  r.exit,
			Verdict:   verdict,
			DurationS: roundSeconds(r.elapsed),
		}
		bi.SetFixture(f)
		fmt.Fprintf(opts.Stdout, "%s: exit %d, %.1fs, %s\n", f.ID, r.exit, r.elapsed, verdict)
	}
	return failures, nil
}

// BehaviorsProven is the #522 headline metric: for every one of the 14
// gauntlet stages (tools/gauntlet/stages.go), a stage counts as proven when
// at least one fixture in bi is mapped to it (BehaviorFixture.Stage) and
// every fixture mapped to it last ran with verdict "pass". A stage with no
// fixture mapped to it is not proven - vacuous agreement is not evidence,
// the same rule HANDOFF.md's safety section applies to an identity check
// that "measures agreement with itself". It returns (proven, total); total
// is always len(Stages()) regardless of bi, so the denominator never lies
// even when the index is missing or empty.
func BehaviorsProven(bi *BehaviorIndex) (proven, total int) {
	stages := Stages()
	total = len(stages)
	if bi == nil {
		return 0, total
	}
	byStage := map[string][]BehaviorFixture{}
	for _, f := range bi.Fixtures {
		if f.Stage == "" {
			continue
		}
		byStage[f.Stage] = append(byStage[f.Stage], f)
	}
	for _, s := range stages {
		fixtures, ok := byStage[s.ID]
		if !ok || len(fixtures) == 0 {
			continue
		}
		allPass := true
		for _, f := range fixtures {
			if f.LastRun == nil || f.LastRun.Verdict != VerdictPass {
				allPass = false
				break
			}
		}
		if allPass {
			proven++
		}
	}
	return proven, total
}
