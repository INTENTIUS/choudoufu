// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// gauntlet runs choudoufu's real-estate test suite against stock OpenTofu
// and renders everything the project says about its own progress from the
// result. live/GAUNTLET.md, rendered by this tool, is the contract.
//
//	go run ./tools/gauntlet render                 # regenerate artifact, spec, site pages
//	go run ./tools/gauntlet next [-n N] [-json]    # the next unit(s) of work, deterministically
//	go run ./tools/gauntlet run [-set core] [-parallel N] [name] # run crossing scripts, record verdicts, render
//	go run ./tools/gauntlet behaviors [-all] [-port N] [-parallel N] [id...] # run the tier-1 behavior matrix (#522), record verdicts, render
//	go run ./tools/gauntlet add <name> <url> <ref> -lane <lane> -source "..." [-core -reason "..."]
//	go run ./tools/gauntlet import-legacy          # one-time seed from live/corpus-crossing-manifest.json
//	go run ./tools/gauntlet snapshot <version>     # copy the artifact to live/history/<version>.json
//	go run ./tools/gauntlet notes <old.json> <new.json> # release-highlights markdown from a snapshot diff
//	go run ./tools/gauntlet check                  # exit 1 if a rendered file is stale
//	go run ./tools/gauntlet merge-artifact <base> <ours> <theirs> # row-granular artifact merge across sibling estate PRs (#488)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	switch os.Args[1] {
	case "render":
		fatalIf(cmdRender(root))
	case "run":
		fatalIf(cmdRun(root, os.Args[2:]))
	case "behaviors":
		fatalIf(cmdBehaviors(root, os.Args[2:]))
	case "live-cert":
		fatalIf(cmdLiveCert(root, os.Args[2:]))
	case "add":
		fatalIf(cmdAdd(root, os.Args[2:]))
	case "import-legacy":
		fatalIf(cmdImportLegacy(root))
	case "snapshot":
		if len(os.Args) < 3 {
			fatal(fmt.Errorf("snapshot needs a version"))
		}
		fatalIf(cmdSnapshot(root, os.Args[2]))
	case "notes":
		fatalIf(cmdNotes(root, os.Args[2:]))
	case "merge-artifact":
		fatalIf(cmdMergeArtifact(root, os.Args[2:]))
	case "next":
		fatalIf(cmdNext(root, os.Args[2:]))
	case "check":
		stale, err := StaleFiles(root)
		fatalIf(err)
		if len(stale) > 0 {
			fmt.Fprintf(os.Stderr, "stale rendered files (run `go run ./tools/gauntlet render`):\n  %s\n", strings.Join(stale, "\n  "))
			os.Exit(1)
		}
		fmt.Println("rendered files are current")
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gauntlet render | run [-set core|all] [-env K=V]... [-parallel N] [name...] | behaviors [-all] [-port N] [-env K=V]... [id...] | live-cert <estate> [-target floci|aws] [-region R] [-ceiling-usd N] [-timeout-seconds N] | next [-n N] [-set core|all] [-types T1,T2,...] [-json] | add <name> <url> <ref> -lane <lane> -source <text> [-core -reason <text>] | import-legacy | snapshot <version> | notes <old.json> <new.json> | merge-artifact <base> <ours> <theirs> | check")
}

// cmdNext prints the next unit(s) of work, deterministically, from the
// committed artifact. See next.go for the ordering.
//
// -types is an additional, orthogonal filter (#436) on top of that ordering:
// it intersects live/estate-types.json's per-estate exercised-type index
// against the requested types and drops any ordinary unit for an estate that
// exercises none of them. It never touches the stale-pin rule (FilterByTypes,
// typeindex.go) - a repin still queues every stale-clear estate regardless of
// -types, since emulator behaviour is not type-scoped. A type-filtered
// confirmation is evidence about those types specifically, never a
// board-wide claim; see live/GAUNTLET.md.
func cmdNext(root string, args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	n := fs.Int("n", 1, "how many units to print")
	set := fs.String("set", "all", "core or all; core first either way")
	types := fs.String("types", "", "comma-separated resource type names; only queue estates that exercise at least one (live/estate-types.json, #435); additional to, never a replacement for, the stale-pin rule")
	asJSON := fs.Bool("json", false, "print JSON, one unit per line")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := LoadArtifact(root)
	if err != nil {
		return err
	}
	units := NextUnits(a, *set)
	wanted := ParseTypes(*types)
	if len(wanted) > 0 {
		idx, err := LoadTypeIndex(root)
		if err != nil {
			return err
		}
		units = FilterByTypes(units, idx, wanted)
	}
	if len(units) == 0 {
		if len(wanted) > 0 {
			fmt.Printf("nothing to do: no estate in the set exercises any of %s (per %s)\n", strings.Join(wanted, ","), TypeIndexPath)
		} else {
			fmt.Println("nothing to do: every estate in the set is clear")
		}
		return nil
	}
	if *n < len(units) {
		units = units[:*n]
	}
	for i, u := range units {
		if *asJSON {
			b, _ := json.Marshal(u)
			fmt.Println(string(b))
			continue
		}
		if i > 0 {
			fmt.Println()
		}
		r, _ := a.Result(u.Estate)
		fmt.Print(FormatUnit(u, r))
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gauntlet:", err)
	os.Exit(1)
}

func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}

// repoRoot finds the checkout root from the working directory.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not in a git checkout: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func headCommit(root string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func emulatorPin(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "live", "floci-image"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// loadAll loads manifest and artifact and rebuilds the derived parts,
// including the #522 behaviors-proven metric from live/behaviors.json (a
// missing file loads as an empty index, same rule as LoadArtifact).
func loadAll(root string) (*Manifest, *Artifact, error) {
	m, err := LoadManifest(root)
	if err != nil {
		return nil, nil, err
	}
	a, err := LoadArtifact(root)
	if err != nil {
		return nil, nil, err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return nil, nil, err
	}
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	return m, a, nil
}

func cmdRender(root string) error {
	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	written, err := Render(root, m, a)
	if err != nil {
		return err
	}
	fmt.Printf("rendered %d files\n", len(written))
	return nil
}

func cmdRun(root string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	set := fs.String("set", "all", "which set to run when no names are given: core or all")
	parallel := fs.Int("parallel", 1, "run this many estates concurrently, each against its own isolated floci emulator (#437); 1 (default) is serial, one estate at a time. Every run, serial included, is assigned an explicit FLOCI_PORT by this same allocator (#520), so a script's own hard-coded default only ever applies when it is invoked by hand, outside this runner")
	var envs multiFlag
	fs.Var(&envs, "env", "KEY=VALUE passed to every script (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	// committed is a completely independent read of live/gauntlet.json as
	// it stood on disk before this run - never a or a.Estates, and never
	// taken by copying a's slice/map values, which would alias the very
	// maps RunEstates is about to mutate in place (EstateResult.Stages is
	// a map, and RunEstates's merge loop writes into an existing row's map
	// rather than allocating a new one - see RunEstates's doc comment). A
	// fresh JSON unmarshal owns brand new maps no other code holds a
	// reference to, the same way acceptance's readArtifact(artifactPath)
	// does for the cohort ratchet (#539/#552). This is the "committed"
	// half of the regression check below.
	committed, err := LoadArtifact(root)
	if err != nil {
		return err
	}
	commit := headCommit(root)
	failures, err := RunEstates(root, m, a, RunOptions{Names: fs.Args(), Set: *set, Env: envs, Parallel: *parallel, Stdout: os.Stdout}, commit, emulatorPin(root))
	if err != nil {
		return err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return err
	}
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))

	// The regression ratchet (issue #553): a stage this run reports as
	// anything other than pass, for an estate/stage the committed artifact
	// recorded as passing, fails the run - not merely a lower number on
	// the board - unless a human has acknowledged it in RegressionsPath in
	// this same change. The artifact is still written below regardless:
	// ground truth is never withheld to avoid a bad headline, exactly the
	// same choice cohorts' enforceRatchet makes (t.Error, not t.Fatal,
	// around its own artifact write).
	acks, err := LoadRegressions(root)
	if err != nil {
		return err
	}
	violations := UnacknowledgedViolations(RatchetViolations(committed.Estates, a.Estates), acks)

	if _, err := Render(root, m, a); err != nil {
		return err
	}
	core, all := a.Sets["core"], a.Sets["all"]
	fmt.Printf("core %d of %d clear, all %d of %d clear, %d script(s) exited non-zero\n", core.Clear, core.Estates, all.Clear, all.Estates, failures)
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, "REGRESSION: "+v.Error())
	}
	if failures > 0 || len(violations) > 0 {
		os.Exit(1)
	}
	return nil
}

// cmdBehaviors is `gauntlet behaviors` (#522): the tier-1 behavior-matrix
// runner. By default it runs every fixture in live/behaviors.json whose
// Runner field is true (the purpose-built "shape" fixtures the ruling
// formalizes as tier 1), then records pass/fail and wall-clock per
// fixture, recomputes behaviors_proven, and re-renders.
//
// -parallel defaults to defaultBehaviorsParallel (issue #541): the matrix's
// own sequential sum passed the five-minute bar by four seconds on a loaded
// machine, which is a coin flip, not a margin - #522's whole argument for
// tier 1 is that it is a development loop, and a squeaker stops being one.
// Every default fixture already starts and tears down its own floci
// container named from its own process id, so nothing about running them
// concurrently is new work; #525's per-slot port allocator (run.go) is the
// proven pattern this reuses. -parallel 1 restores the old fully-serial,
// one-shared-port behavior for debugging a single fixture's timing in
// isolation.
//
// -all runs every independently Runnable fixture regardless of Runner
// (including the adoption and legacy-demo scripts catalogued but excluded
// from the default matrix) - useful for auditing the full catalogue's
// timing, never for the five-minute bar itself, which is about the default
// set only.
func cmdBehaviors(root string, args []string) error {
	fs := flag.NewFlagSet("behaviors", flag.ContinueOnError)
	all := fs.Bool("all", false, "run every independently runnable fixture, not just the default tier-1 set (Runner=true)")
	port := fs.Int("port", 0, "FLOCI_PORT for a serial (-parallel 1) run; 0 means DefaultBehaviorsPort")
	parallel := fs.Int("parallel", defaultBehaviorsParallel, "run this many fixtures concurrently, each against its own isolated floci emulator (#541); 1 is fully serial, byte-for-byte the runner's original behaviour")
	var envs multiFlag
	fs.Var(&envs, "env", "KEY=VALUE passed to every script (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return err
	}
	commit := headCommit(root)
	start := time.Now()
	failures, err := RunBehaviors(root, bi, BehaviorsRunOptions{Names: fs.Args(), All: *all, Port: *port, Parallel: *parallel, Env: envs, Stdout: os.Stdout}, commit)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}
	if err := SaveBehaviorIndex(root, bi); err != nil {
		return err
	}
	a, err := LoadArtifact(root)
	if err != nil {
		return err
	}
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	if _, err := Render(root, m, a); err != nil {
		return err
	}
	selected := len(fs.Args())
	if selected == 0 {
		selected = countRunner(bi, *all)
	}
	fmt.Printf("behaviors: %d fixture(s) run in %s, %d failed; behaviors_proven %d of %d\n", selected, elapsed.Round(time.Millisecond), failures, a.BehaviorsProven, a.BehaviorsTotal)
	if failures > 0 {
		os.Exit(1)
	}
	return nil
}

// countRunner reports how many fixtures a Names-less RunBehaviors call
// selects, purely for cmdBehaviors's own summary line.
func countRunner(bi *BehaviorIndex, all bool) int {
	n := 0
	for _, f := range bi.Fixtures {
		if all {
			if f.Runnable {
				n++
			}
			continue
		}
		if f.Runner {
			n++
		}
	}
	return n
}

// cmdLiveCert is `gauntlet live-cert <estate>` (issue #440): a real-AWS (or,
// for Stage 1 proving, floci) certification run. Its result is recorded
// into a.LiveCert - NEVER a.Estates - and TARGET=floci is never written to
// the committed artifact at all (RunLiveCert's own doc comment): this
// subcommand exists to run the harness and, for a real target=aws run,
// persist evidence distinct from every emulator row, not to fold a
// proving run into the board.
func cmdLiveCert(root string, args []string) error {
	fs := flag.NewFlagSet("live-cert", flag.ContinueOnError)
	target := fs.String("target", "floci", "floci (Stage 1 proving; never recorded to the artifact) or aws (Stage 2; recorded)")
	region := fs.String("region", "us-east-1", "AWS region")
	ceilingUSD := fs.Float64("ceiling-usd", 5, "cost ceiling this run is certifying under (#440 ruling: $5 for reference-ec2-vpc); informational here, enforced by the account's own AWS Budgets alarm and by -timeout-seconds/live/live-cert/run.sh's process timeout")
	timeoutSeconds := fs.Int("timeout-seconds", 900, "Go-side process ceiling, independent of live/live-cert/run.sh's own `timeout` wrapper")
	confirm := fs.String("confirm", "", "must be exactly \"yes\" for -target aws; refused otherwise before anything is started")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("live-cert needs exactly one estate name, got %d", fs.NArg())
	}
	estate := fs.Arg(0)

	r, res, exit, err := RunLiveCert(root, estate, *target, *region, *ceilingUSD, *timeoutSeconds, *confirm)
	if err != nil {
		return err
	}
	if res != nil {
		for id, v := range res.Stages {
			fmt.Printf("live-cert %s: stage=%s verdict=%s\n", estate, id, v)
		}
	}
	fmt.Printf("live-cert %s: target=%s exit=%d clear=%v\n", estate, *target, exit, r.Clear)

	if *target != "aws" {
		fmt.Println("target=floci: this is Stage-1 proving evidence only; NOT written to live/gauntlet.json (RunLiveCert never records a floci run)")
		return nil
	}

	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	a.SetLiveCertResult(*r)
	if err := SaveArtifact(root, a); err != nil {
		return err
	}
	if _, err := Render(root, m, a); err != nil {
		return err
	}
	fmt.Printf("recorded live-aws certification for %s: clear=%v (live/gauntlet.json live_cert; never counted in sets.core/sets.all)\n", estate, r.Clear)
	return nil
}

// cmdMergeArtifact is `gauntlet merge-artifact <base> <ours> <theirs>`
// (#488): a row-granular three-way merge of live/gauntlet.json across
// sibling estate PRs, so landing one no longer forces every other open PR
// to pay a full re-run just to reconcile the aggregate. See MergeArtifact
// (mergeartifact.go) for the merge itself and live/GAUNTLET.md for when
// this applies and when a re-run is still mandatory. On success this
// writes the merged, rebuilt artifact and re-renders every file it drives,
// exactly like `gauntlet run` does after a real run.
func cmdMergeArtifact(root string, args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("merge-artifact needs exactly 3 revisions: <base> <ours> <theirs>, got %d", len(args))
	}
	merged, err := MergeArtifact(root, args[0], args[1], args[2])
	if err != nil {
		return err
	}
	if err := SaveArtifact(root, merged); err != nil {
		return err
	}
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	if _, err := Render(root, m, merged); err != nil {
		return err
	}
	core, all := merged.Sets["core"], merged.Sets["all"]
	fmt.Printf("merged: core %d of %d clear, all %d of %d clear\n", core.Clear, core.Estates, all.Clear, all.Estates)
	return nil
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

func cmdAdd(root string, args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	lane := fs.String("lane", "", "one of "+strings.Join(KnownLanes, ", "))
	source := fs.String("source", "", "one-line description: repository, path, version")
	core := fs.Bool("core", false, "put the estate in the core set (needs -reason)")
	reason := fs.String("reason", "", "why this estate belongs in the core set")
	script := fs.String("script", "", "script path, default live/e2e/<name>/run.sh")
	// Positional args first: name url ref.
	var pos []string
	var flags []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i:]...)
			break
		}
		pos = append(pos, args[i])
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	if len(pos) < 1 {
		return fmt.Errorf("add needs at least <name>; <url> <ref> unless -lane reference")
	}
	e := Estate{Name: pos[0], Lane: *lane, Source: *source, Reason: *reason, Script: *script, Set: SetGrowing}
	if len(pos) > 1 {
		e.URL = pos[1]
	}
	if len(pos) > 2 {
		e.Pin = pos[2]
	}
	if *core {
		e.Set = SetCore
	}
	if e.Source == "" {
		e.Source = fmt.Sprintf("%s at %s", e.URL, e.Pin)
	}
	m, err := LoadManifest(root)
	if err != nil {
		return err
	}
	if err := AddEstate(root, m, e); err != nil {
		return err
	}
	fmt.Printf("added %s; fill in %s, then `go run ./tools/gauntlet run %s`\n", e.Name, e.ScriptPath(), e.Name)
	return cmdRender(root)
}

// cmdImportLegacy seeds the artifact from live/corpus-crossing-manifest.json,
// the hand-recorded ledger the gauntlet replaces. Run once; afterwards the
// runner is the only writer of verdicts. Entries already carrying the
// gauntlet protocol are left alone.
func cmdImportLegacy(root string) error {
	b, err := os.ReadFile(filepath.Join(root, "live", "corpus-crossing-manifest.json"))
	if err != nil {
		return err
	}
	var legacy struct {
		Estates []struct {
			Dir    string            `json:"dir"`
			Stages map[string]string `json:"stages"`
			Notes  string            `json:"notes"`
		} `json:"estates"`
	}
	if err := json.Unmarshal(b, &legacy); err != nil {
		return err
	}
	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	imported := 0
	for _, e := range m.Estates {
		r, _ := a.Result(e.Name)
		if r.Protocol == ProtocolGauntlet {
			continue
		}
		for _, l := range legacy.Estates {
			if filepath.Base(l.Dir) != e.Name {
				continue
			}
			r.Name = e.Name
			r.Stages = map[string]string{}
			for id, v := range l.Stages {
				r.Stages[id] = v
			}
			r.Notes = l.Notes
			r.Protocol = ProtocolLegacy
			a.SetResult(r)
			imported++
		}
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return err
	}
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	if _, err := Render(root, m, a); err != nil {
		return err
	}
	fmt.Printf("imported %d legacy verdict sets\n", imported)
	return nil
}

func cmdSnapshot(root, version string) error {
	src := filepath.Join(root, ArtifactPath)
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dir := filepath.Join(root, "live", "history")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(dir, version+".json")
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", dst)
	return nil
}

// StaleFiles renders into a temp dir and returns the rendered files whose
// committed copy differs. The test and `check` share it.
func StaleFiles(root string) ([]string, error) {
	m, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	a, err := LoadArtifact(root)
	if err != nil {
		return nil, err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return nil, err
	}
	// Same fresh emulator pin `render` itself would use - there is no
	// stamp left to freeze for content-only comparison (#414).
	a.Rebuild(m, bi, emulatorPin(root), oracleVersions(root))
	tmp, err := os.MkdirTemp("", "gauntlet-render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// Estate pages are pruned by reading the target dir; mirror the committed
	// one so pruning logic runs the same way.
	written, err := Render(tmp, m, a)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, rel := range written {
		want, err := os.ReadFile(filepath.Join(tmp, rel))
		if err != nil {
			return nil, err
		}
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || !bytes.Equal(want, got) {
			stale = append(stale, rel)
		}
	}
	return stale, nil
}
