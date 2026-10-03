// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package setplan plans a set of estate roots in one invocation (GitHub
// issue #1752, part of epic #1749): every root gets its own saved plan
// file, one root's failure never stops the others, and the whole set is
// reported in one JSON document.
//
// # One invocation, not one process
//
// The issue asked for one process. Planning two roots in one process is not
// something this fork's commands can do, because every one of them reaches
// its root module through the process working directory: upstream's -chdir
// is an os.Chdir in main, [workdir.Dir.NormalizePath] answers relative to
// the root module on the assumption the two are the same directory, and the
// live pipeline loads "." directly (live_mode.go's statelessSettings,
// live_plan.go's loadConfig, the record store opened at "." in
// live_record_store_open.go, the variable files read from "." in
// meta_vars.go, the operation's ConfigDir in plan.go). The working directory
// is one per process, so N roots planned concurrently in one process would
// all read whichever root was the working directory last.
//
// So one orchestrating process runs each root's three steps (init, plan
// -out, show -json) as child processes of the same binary, each with
// -chdir=ROOT, under a bound of -parallel-estates. What the issue's "one
// process" was for survives intact:
//
//   - Providers are installed once per invocation, not once per root. Every
//     child shares one global plugin cache (TF_PLUGIN_CACHE_DIR), whose
//     installs are serialised by a file lock per provider release
//     (providercache.Dir.lock), so the first root to need a release unpacks
//     it and every other root links it. The children also carry
//     TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE=1, without which a root
//     with no dependency lock file re-downloads a release the cache already
//     holds, because it has no checksum to match the cached copy against.
//     The cost of that variable is the one it is named for: a root with no
//     lock file gets one recording only the running platform's checksum.
//   - One document, one exit code, one place to look.
//
// # The document
//
// [Document] is what -json prints. Its contract with the grouped summary
// (#1753) and the set digest and wave apply (#1754) is that the top level
// carries "roots", and each root carries at least "root", "estate",
// "status", "error" and "plan", where "plan" is exactly the object
// "choudoufu show -json PLANFILE" prints for that root's plan file -
// stock OpenTofu's machine-readable plan, resource_changes included. Fields
// are added, never renamed or dropped; [FormatVersion] moves when one is.
// testdata/document.golden.json pins the shape.
//
// # Exit codes
//
//	0  every root planned, and no root has changes
//	2  every root planned, and at least one root has changes
//	   (the meaning plan -detailed-exitcode gives a single root)
//	4  at least one root failed; the others are still planned and reported
//	1  the command itself could not run (bad flags, no roots, a root given
//	   twice); no root was planned
//
// 3 is skipped on purpose: "choudoufu apply PLANFILE" already exits 3 when
// the live system moved under an approved plan, and #1754's wave apply
// extends that refusal to a set. A pipeline routing on exit codes should
// never have to ask which command produced a 3.
package setplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/intentius/choudoufu/internal/live/setdigest"
)

// FormatVersion is the document's format version. It moves when a field is
// renamed or dropped, never when one is added.
const FormatVersion = "1"

// The exit codes. See the package documentation.
const (
	ExitClean      = 0
	ExitError      = 1
	ExitChanges    = 2
	ExitRootFailed = 4
)

// Status is a root's outcome.
type Status string

const (
	// StatusPlanned is a root whose plan file was written and read back.
	StatusPlanned Status = "planned"
	// StatusFailed is a root that stopped at one of its stages; Error says
	// why and Stage says where.
	StatusFailed Status = "failed"
)

// Stage is one step of a root's run, in order.
type Stage string

const (
	// StageEstate reads the root's live block (or sidecar) for its estate.
	// A root with no live block fails here: a plain "choudoufu plan" there
	// would be a stock state-backed plan, not a live one.
	StageEstate Stage = "estate"
	StageInit   Stage = "init"
	StagePlan   Stage = "plan"
	StageShow   Stage = "show"
)

// Root is one root's entry in the document.
type Root struct {
	// Root is the root directory relative to the directory the command ran
	// in, with forward slashes.
	Root string `json:"root"`
	// Estate is the estate the root's live block names. Empty when the
	// root failed before its live block was read, or when the block names
	// none and the run derives it from the configuration's markers.
	Estate string `json:"estate"`
	Status Status `json:"status"`
	// Error is the failure, set when Status is failed and empty otherwise.
	Error string `json:"error"`
	// Stage is the stage that failed, set when Status is failed.
	Stage Stage `json:"stage,omitempty"`
	// Changes is what plan -detailed-exitcode said: true when the plan
	// proposes any change. False for a failed root.
	Changes bool `json:"changes"`
	// PlanFile is the saved plan, relative to the command's directory.
	// Set whenever the plan stage succeeded.
	PlanFile string `json:"plan_file"`
	// LogFile holds every stage's combined output for this root.
	LogFile string `json:"log_file"`
	// DurationMS is the root's wall time across all its stages.
	DurationMS int64 `json:"duration_ms"`
	// Plan is stock OpenTofu's machine-readable plan for this root, the
	// object "choudoufu show -json PLANFILE" prints. null for a failed root.
	Plan json.RawMessage `json:"plan"`
	// Digest is this root's digest (#1754): what [Document.Digest] is
	// computed over. internal/live/setdigest's package doc says what it covers.
	Digest string `json:"digest"`
}

// Summary counts the document's roots.
type Summary struct {
	Roots   int `json:"roots"`
	Planned int `json:"planned"`
	Changed int `json:"changed"`
	Failed  int `json:"failed"`
}

// Document is the -json output: one object, printed once.
type Document struct {
	FormatVersion string `json:"format_version"`
	// Roots are in the order the command was given them, whatever order
	// they finished in.
	Roots   []Root  `json:"roots"`
	Summary Summary `json:"summary"`
	// Digest is the set digest (#1754): one digest over every root's
	// planned changes, independent of root order, moving whenever any one
	// root's changes move. It is what an approval names and what
	// live-wave-apply checks the set against.
	Digest string `json:"digest"`
	// ExitCode is the code the command exits with, so a reader of a saved
	// document does not have to have watched the process.
	ExitCode int `json:"exit_code"`
	// PluginCacheDir is the one provider cache every root shared.
	PluginCacheDir string `json:"plugin_cache_dir"`
	// ParallelEstates is the bound the roots ran under.
	ParallelEstates int `json:"parallel_estates"`
}

// Runner does one root's stages. [Exec] is the real one; tests substitute
// their own to break a chosen root at a chosen stage.
type Runner interface {
	// Estate reads dir's estate from its live block. ok is false when dir
	// has no live block at all.
	Estate(ctx context.Context, dir string) (estate string, ok bool, err error)
	Init(ctx context.Context, dir string, log io.Writer) error
	// Plan writes planFile and reports whether the plan has changes.
	Plan(ctx context.Context, dir, planFile string, log io.Writer) (changes bool, err error)
	// Show returns the machine-readable form of planFile.
	Show(ctx context.Context, dir, planFile string, log io.Writer) (json.RawMessage, error)
}

// Options are one set plan's inputs.
type Options struct {
	// Roots are the root directories, as given.
	Roots []string
	// BaseDir is the directory Roots, OutDir and the document's paths are
	// relative to: the command's working directory.
	BaseDir string
	// OutDir receives one plan file and one log per root, named by root.
	OutDir string
	// Parallel bounds how many roots run at once. At least 1.
	Parallel int
	// PluginCacheDir is reported in the document; the Runner is what uses
	// it.
	PluginCacheDir string
	Runner         Runner
	// Now is the clock, for tests. Defaults to time.Now.
	Now func() time.Time
}

// rootPaths is one root's resolved paths.
type rootPaths struct {
	rel      string // relative to BaseDir, forward slashes
	abs      string
	planFile string // absolute
	logFile  string // absolute
}

// resolve checks the roots and works out each one's paths, before anything
// runs. Its errors are the command's own (exit 1): a root outside the base
// directory has no name to give its plan file, and a root given twice would
// write one plan file from two runs.
func resolve(opts Options) ([]rootPaths, error) {
	if len(opts.Roots) == 0 {
		return nil, errors.New("no roots given: name at least one root directory")
	}
	base, err := filepath.Abs(opts.BaseDir)
	if err != nil {
		return nil, err
	}
	outDir := opts.OutDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(base, outDir)
	}
	seen := map[string]string{}
	var out []rootPaths
	for _, given := range opts.Roots {
		abs := given
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(base, abs)
		}
		abs = filepath.Clean(abs)
		rel, err := filepath.Rel(base, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("root %q is outside the working directory %s; its plan file is named by its path from there, so run from a directory that contains every root", given, base)
		}
		rel = filepath.ToSlash(rel)
		if prev, dup := seen[rel]; dup {
			return nil, fmt.Errorf("root %q is given twice (also as %q); one root has one plan file", given, prev)
		}
		seen[rel] = given
		name := PlanName(rel)
		out = append(out, rootPaths{
			rel:      rel,
			abs:      abs,
			planFile: filepath.Join(outDir, filepath.FromSlash(name)+".tfplan"),
			logFile:  filepath.Join(outDir, filepath.FromSlash(name)+".log"),
		})
	}
	return out, nil
}

// PlanName is the name a root's plan file and log take inside the output
// directory, before their extensions: the root's own relative path, so
// estates/e01 plans to OUT/estates/e01.tfplan. The base directory itself
// has no path to mirror and is named "_root".
func PlanName(rel string) string {
	if rel == "." || rel == "" {
		return "_root"
	}
	return rel
}

// Run plans every root and returns the document. Its error is the command's
// own (see resolve); every root-level failure is in the document instead.
func Run(ctx context.Context, opts Options) (*Document, error) {
	if opts.Parallel < 1 {
		return nil, fmt.Errorf("-parallel-estates must be at least 1, got %d", opts.Parallel)
	}
	if opts.Runner == nil {
		return nil, errors.New("no runner")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	paths, err := resolve(opts)
	if err != nil {
		return nil, err
	}
	base, err := filepath.Abs(opts.BaseDir)
	if err != nil {
		return nil, err
	}

	doc := &Document{
		FormatVersion:   FormatVersion,
		Roots:           make([]Root, len(paths)),
		PluginCacheDir:  opts.PluginCacheDir,
		ParallelEstates: opts.Parallel,
	}

	sem := make(chan struct{}, opts.Parallel)
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func(i int, p rootPaths) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			doc.Roots[i] = runRoot(ctx, opts.Runner, base, p, now)
		}(i, p)
	}
	wg.Wait()

	for _, r := range doc.Roots {
		doc.Summary.Roots++
		switch r.Status {
		case StatusPlanned:
			doc.Summary.Planned++
			if r.Changes {
				doc.Summary.Changed++
			}
		default:
			doc.Summary.Failed++
		}
	}
	if err := digestDocument(doc); err != nil {
		return nil, err
	}
	doc.ExitCode = ExitCode(doc)
	return doc, nil
}

// ExitCode is the code a document earns. See the package documentation.
func ExitCode(doc *Document) int {
	changed := false
	for _, r := range doc.Roots {
		if r.Status != StatusPlanned {
			return ExitRootFailed
		}
		changed = changed || r.Changes
	}
	if changed {
		return ExitChanges
	}
	return ExitClean
}

func relTo(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// runRoot runs one root's stages and never returns an error: whatever goes
// wrong is the root's entry.
func runRoot(ctx context.Context, runner Runner, base string, p rootPaths, now func() time.Time) Root {
	start := now()
	r := Root{
		Root:    p.rel,
		Status:  StatusFailed,
		LogFile: relTo(base, p.logFile),
	}
	fail := func(stage Stage, err error) Root {
		r.Stage = stage
		r.Error = err.Error()
		r.Changes = false
		r.Plan = nil
		r.DurationMS = now().Sub(start).Milliseconds()
		return r
	}

	if err := os.MkdirAll(filepath.Dir(p.logFile), 0o755); err != nil {
		return fail(StageInit, fmt.Errorf("creating the output directory: %w", err))
	}
	logf, err := os.Create(p.logFile)
	if err != nil {
		return fail(StageInit, fmt.Errorf("creating the root's log: %w", err))
	}
	defer logf.Close()

	if info, err := os.Stat(p.abs); err != nil {
		return fail(StageEstate, fmt.Errorf("reading the root directory: %w", err))
	} else if !info.IsDir() {
		return fail(StageEstate, fmt.Errorf("%s is not a directory", p.rel))
	}

	estate, ok, err := runner.Estate(ctx, p.abs)
	if err != nil {
		return fail(StageEstate, err)
	}
	if !ok {
		return fail(StageEstate, errors.New("this root has no live block: \"choudoufu plan\" there would be a stock state-backed plan, not a live one. Declare the estate in a live block (terraform { live { estate = \"...\" } }) or an estate.chdf.hcl sidecar"))
	}
	r.Estate = estate

	if err := runner.Init(ctx, p.abs, logf); err != nil {
		return fail(StageInit, err)
	}
	// A stale plan file from an earlier run must never be read back as this
	// run's: remove it before planning, so a plan stage that fails leaves
	// nothing behind to mistake.
	if err := os.Remove(p.planFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fail(StagePlan, fmt.Errorf("removing an earlier run's plan file: %w", err))
	}
	changes, err := runner.Plan(ctx, p.abs, p.planFile, logf)
	if err != nil {
		return fail(StagePlan, err)
	}
	r.PlanFile = relTo(base, p.planFile)
	plan, err := runner.Show(ctx, p.abs, p.planFile, logf)
	if err != nil {
		planFile := r.PlanFile
		out := fail(StageShow, err)
		out.PlanFile = planFile
		return out
	}
	r.Status = StatusPlanned
	r.Changes = changes
	r.Plan = plan
	r.DurationMS = now().Sub(start).Milliseconds()
	return r
}

// digestDocument sets every root's digest and the set digest, through
// internal/live/setdigest so the digest live-plan-set prints is the one
// live-waves and live-wave-apply compute from this document.
func digestDocument(doc *Document) error {
	entries := make([]setdigest.RootDigestEntry, 0, len(doc.Roots))
	for i := range doc.Roots {
		r := &doc.Roots[i]
		d, err := setdigest.RootDigest(setdigest.RootPlan{Root: r.Root, Estate: r.Estate, Status: string(r.Status), Error: r.Error, Plan: r.Plan})
		if err != nil {
			return err
		}
		r.Digest = d
		entries = append(entries, setdigest.RootDigestEntry{Root: r.Root, Digest: d})
	}
	d, err := setdigest.SetDigest(entries)
	if err != nil {
		return err
	}
	doc.Digest = d
	return nil
}
