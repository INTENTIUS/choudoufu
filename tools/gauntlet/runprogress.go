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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Watching a `gauntlet run` (#2037).
//
// On 2026-10-12 the only way to tell what a three-hour run was doing was `ps`
// and the scripts' temp directories. The runner now prints a line when each
// stage starts and ends, with the elapsed time and how many estates are done,
// and keeps the same state in a progress file outside the checkout, which
// `gauntlet status` reads from any other terminal.
//
// Stage boundaries come from the script's own protocol lines as they are
// written: `GAUNTLET begin=<id>` (gauntlet_begin_stage), a verdict line
// `GAUNTLET stage=<id> verdict=...`, and `GAUNTLET end_stage=<id>`
// (gauntlet_end_stage). Nothing here is recorded in the artifact; a stage's
// seconds there come from its verdict line's duration_s.

// progressDir is where every run's progress file lives: the OS temp dir,
// never the checkout, so a progress file can never be committed and two
// worktrees' runs land in one place `gauntlet status` can look.
func progressDir() string { return filepath.Join(os.TempDir(), "choudoufu-gauntlet-progress") }

// progressPath is one run's progress file, keyed by the runner's pid.
func progressPath(pid int) string {
	return filepath.Join(progressDir(), fmt.Sprintf("run-%d.json", pid))
}

// ProgressFile is what a progress file holds.
type ProgressFile struct {
	Pid      int               `json:"pid"`
	Root     string            `json:"root"`
	Started  time.Time         `json:"started"`
	Updated  time.Time         `json:"updated"`
	Total    int               `json:"total"`
	Done     int               `json:"done"`
	Parallel int               `json:"parallel"`
	State    string            `json:"state"` // running, finished, stopped
	Running  []ProgressEstate  `json:"running"`
	Finished []ProgressOutcome `json:"finished,omitempty"`
}

// ProgressEstate is one estate that is running now.
type ProgressEstate struct {
	Name         string    `json:"name"`
	Started      time.Time `json:"started"`
	Stage        string    `json:"stage,omitempty"` // "" between stages
	StageStarted time.Time `json:"stage_started,omitempty"`
}

// ProgressOutcome is one estate that has finished.
type ProgressOutcome struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Outcome string  `json:"outcome"`
}

// runProgress is the live state, written to out as lines and to path as a
// ProgressFile on every change. Safe from every runOne goroutine at once.
type runProgress struct {
	mu   sync.Mutex
	out  io.Writer
	path string // "" writes no file (tests that only read lines)
	now  func() time.Time
	file ProgressFile
	// lastMark is when each running estate last crossed a stage boundary,
	// so a verdict line with no begin line before it still gets a duration.
	lastMark map[string]time.Time
}

func newRunProgress(out io.Writer, path, root string, total, parallel int) *runProgress {
	p := &runProgress{out: out, path: path, now: time.Now, lastMark: map[string]time.Time{}}
	t := p.now()
	p.file = ProgressFile{Pid: os.Getpid(), Root: root, Started: t, Updated: t, Total: total, Parallel: parallel, State: "running"}
	p.writeLocked()
	return p
}

func (p *runProgress) elapsed() string {
	return clockDuration(p.now().Sub(p.file.Started).Seconds())
}

func (p *runProgress) sayLocked(format string, args ...any) {
	fmt.Fprintf(p.out, "[%s, %d/%d done] %s\n", p.elapsed(), p.file.Done, p.file.Total, fmt.Sprintf(format, args...))
}

func (p *runProgress) writeLocked() {
	if p.path == "" {
		return
	}
	p.file.Updated = p.now()
	sort.Slice(p.file.Running, func(i, j int) bool { return p.file.Running[i].Name < p.file.Running[j].Name })
	b, err := json.MarshalIndent(p.file, "", "  ")
	if err != nil {
		return
	}
	// Best effort, through a rename so `gauntlet status` never reads half a
	// file: progress must never stop a run.
	if os.MkdirAll(filepath.Dir(p.path), 0o755) != nil {
		return
	}
	tmp := p.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, p.path)
	}
}

func (p *runProgress) runningLocked(name string) *ProgressEstate {
	for i := range p.file.Running {
		if p.file.Running[i].Name == name {
			return &p.file.Running[i]
		}
	}
	return nil
}

// estateStarted is called as an estate's script starts.
func (p *runProgress) estateStarted(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	p.file.Running = append(p.file.Running, ProgressEstate{Name: name, Started: t})
	p.lastMark[name] = t
	p.sayLocked("%s: started", name)
	p.writeLocked()
}

// stageBegan is a `GAUNTLET begin=<id>` line.
func (p *runProgress) stageBegan(name, stage string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.runningLocked(name)
	if e == nil {
		return
	}
	t := p.now()
	e.Stage, e.StageStarted = stage, t
	p.lastMark[name] = t
	p.sayLocked("%s: %s started (estate running %s)", name, stage, clockDuration(t.Sub(e.Started).Seconds()))
	p.writeLocked()
}

// stageEnded is a verdict line (verdict set) or an end_stage line (verdict
// ""). The duration is from the stage's begin line, or from the estate's
// previous boundary when the script printed no begin line.
func (p *runProgress) stageEnded(name, stage, verdict string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.runningLocked(name)
	if e == nil {
		return
	}
	t := p.now()
	from := p.lastMark[name]
	if e.Stage == stage && !e.StageStarted.IsZero() {
		from = e.StageStarted
	}
	if verdict == "" && e.Stage != stage {
		// An end_stage for a stage this runner never saw begin: nothing
		// to close.
		return
	}
	e.Stage, e.StageStarted = "", time.Time{}
	p.lastMark[name] = t
	what := "ended"
	if verdict != "" {
		what = verdict
	}
	p.sayLocked("%s: %s %s after %s", name, stage, what, clockDuration(t.Sub(from).Seconds()))
	p.writeLocked()
}

// estateFinished is called once per estate that started, whatever became
// of it.
func (p *runProgress) estateFinished(name, outcome string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.now()
	var secs float64
	for i := range p.file.Running {
		if p.file.Running[i].Name == name {
			secs = t.Sub(p.file.Running[i].Started).Seconds()
			p.file.Running = append(p.file.Running[:i], p.file.Running[i+1:]...)
			break
		}
	}
	delete(p.lastMark, name)
	p.file.Done++
	p.file.Finished = append(p.file.Finished, ProgressOutcome{Name: name, Seconds: roundSeconds(secs), Outcome: outcome})
	p.sayLocked("%s: %s in %s", name, outcome, clockDuration(secs))
	p.writeLocked()
}

// end marks the run over in the file.
func (p *runProgress) end(state string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.file.State = state
	p.writeLocked()
}

// stageWatcher is an io.Writer on a script's combined output that turns its
// stage boundary lines into progress events as they are written.
type stageWatcher struct {
	p      *runProgress
	estate string
	buf    []byte
}

func (w *stageWatcher) Write(b []byte) (int, error) {
	w.buf = append(w.buf, b...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.line(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	return len(b), nil
}

func (w *stageWatcher) line(text string) {
	if !strings.HasPrefix(text, ProtocolPrefix) {
		return
	}
	f := parseKV(strings.TrimPrefix(text, ProtocolPrefix))
	switch {
	case f["begin"] != "":
		w.p.stageBegan(w.estate, f["begin"])
	case f["end_stage"] != "":
		w.p.stageEnded(w.estate, f["end_stage"], "")
	case f["stage"] != "" && f["verdict"] != "":
		w.p.stageEnded(w.estate, f["stage"], f["verdict"])
	}
}

// cmdStatus is `gauntlet status` (#2037): the latest progress file, read and
// printed. It never prompts and never starts anything - it is the one
// gauntlet subcommand made to be run while a run is going.
func cmdStatus(args []string, out io.Writer) error {
	path := ""
	if len(args) > 0 {
		path = args[0]
	} else {
		matches, _ := filepath.Glob(filepath.Join(progressDir(), "run-*.json"))
		var newest time.Time
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.ModTime().After(newest) {
				newest, path = fi.ModTime(), m
			}
		}
		if path == "" {
			return fmt.Errorf("gauntlet status: no progress file under %s - no `gauntlet run` has started on this machine since the temp dir was last cleared", progressDir())
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f ProgressFile
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	printStatus(out, path, f, time.Now(), pidAlive(f.Pid))
	return nil
}

func printStatus(out io.Writer, path string, f ProgressFile, now time.Time, alive bool) {
	state := f.State
	if state == "running" && !alive {
		state = fmt.Sprintf("not running (pid %d is gone; this is the last state it wrote, at %s)", f.Pid, f.Updated.Local().Format(time.Kitchen))
	}
	fmt.Fprintf(out, "gauntlet run pid %d in %s: %s, %d/%d estate(s) done, elapsed %s, parallel %d\n", f.Pid, f.Root, state, f.Done, f.Total, clockDuration(now.Sub(f.Started).Seconds()), f.Parallel)
	for _, e := range f.Running {
		stage := "between stages"
		if e.Stage != "" {
			stage = fmt.Sprintf("%s for %s", e.Stage, clockDuration(now.Sub(e.StageStarted).Seconds()))
		}
		fmt.Fprintf(out, "  %-48s %s, estate running %s\n", e.Name, stage, clockDuration(now.Sub(e.Started).Seconds()))
	}
	fmt.Fprintf(out, "(%s)\n", path)
}

// clockDuration is an elapsed time to the second, "0s" included: a progress
// line always says how long, where the board's formatDuration says "-" for
// nothing recorded.
func clockDuration(seconds float64) string {
	return time.Duration(seconds * float64(time.Second)).Round(time.Second).String()
}
