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
//	go run ./tools/gauntlet scale-backfill [rev...]  # regenerate live/gauntlet-scale.json (#1051) from live/gauntlet.json at HEAD and, optionally, past revisions
//	go run ./tools/gauntlet scale-import-slice [-estate name] <slice_out.json> # merge a slicing-bench SLICE_OUT report's plan_calls (the CLI cold/warm plan pair) and audit_calls (the CollectUnclaimed sweep) into live/gauntlet-scale.json (#1053)
//	go run ./tools/gauntlet scale-patch-seconds -estate E -target T -scale N [-stage id=seconds]... [-note text] [-accounting-inconsistent] # patch an existing ScaleRecord's stage wall-durations from a source scale-backfill cannot read, or name a record's own arithmetic as a known inconsistency (#1051/#1053/#1069)
//	go run ./tools/gauntlet backfill-stage-provenance [-n] # one-shot, re-runnable: write per-stage provenance (#1069) into every committed row the artifact can honestly support one for
package main

import (
	"bytes"
	"encoding/json"
	"errors"
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
	case "scale-backfill":
		fatalIf(cmdScaleBackfill(root, os.Args[2:]))
	case "scale-import-slice":
		fatalIf(cmdScaleImportSlice(root, os.Args[2:]))
	case "scale-patch-seconds":
		fatalIf(cmdScalePatchSeconds(root, os.Args[2:]))
	case "backfill-stage-provenance":
		fatalIf(cmdBackfillStageProvenance(root, os.Args[2:], os.Stdout))
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
	fmt.Fprintln(os.Stderr, "usage: gauntlet render | run [-set core|all] [-env K=V]... [-parallel N] [name...] | behaviors [-all] [-port N] [-env K=V]... [id...] | live-cert <estate> [-target floci|aws] [-region R] [-ceiling-usd N] [-timeout-seconds N] | next [-n N] [-set core|all] [-types T1,T2,...] [-json] | add <name> <url> <ref> -lane <lane> -source <text> [-core -reason <text>] | import-legacy | snapshot <version> | notes <old.json> <new.json> | merge-artifact <base> <ours> <theirs> | scale-backfill [rev...] | scale-import-slice [-estate name] <slice_out.json> | scale-patch-seconds -estate E -target T -scale N [-stage id=seconds]... [-note text] [-accounting-inconsistent] | backfill-stage-provenance [-n] | check")
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

// gitOutput runs a git command in dir and returns its trimmed stdout. On
// failure the error carries git's OWN first line of stderr, not just the
// bare "exit status 128" that exec.Cmd.Output()'s ExitError formats as
// (#1149).
//
// The distinction is the whole issue. When a machine's git began refusing
// every invocation with "You have not agreed to the Xcode license
// agreements", `gauntlet live-cert` reported "built an invalid scale record:
// missing required field(s): commit" - a message that names the record
// builder and says nothing about the toolchain, so the reader debugs the
// wrong half of the program. Every git call in this package that a human
// ever reads the error of goes through here.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // a fixed subcommand list, arguments are internal
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg, _, _ := strings.Cut(strings.TrimSpace(stderr.String()), "\n"); msg != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// repoRoot finds the checkout root from the working directory.
func repoRoot() (string, error) {
	out, err := gitOutput("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a git checkout: %w", err)
	}
	return out, nil
}

// headCommit is the provenance stamp every recorded run carries.
//
// It used to swallow git's error and return "" (#1149). An empty commit is
// not a commit, and downstream it became "missing required field(s):
// commit", which dropped the scale record of an eleven-hour real-AWS run
// while the live_cert row for the same run wrote fine - one half of a run's
// evidence landing and the other half silently not. The error is the
// caller's to refuse with.
func headCommit(root string) (string, error) {
	return gitOutput(root, "rev-parse", "HEAD")
}

// isShallowRepo reports whether root is a shallow git checkout - one with a
// truncated commit history, which is what actions/checkout produces by
// default when a workflow's checkout step does not set fetch-depth: 0
// (issue #511). A shallow checkout cannot answer "is X an ancestor of HEAD"
// correctly: `git merge-base --is-ancestor` only sees the truncated
// history, so a commit that really is an ancestor in the full history can
// read as "not an ancestor" purely because its object was never fetched -
// a false positive for the #509 defect class, not a true one.
func isShallowRepo(root string) (bool, error) {
	out, err := gitOutput(root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return false, err
	}
	return out == "true", nil
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
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	written, err := Render(root, m, a, tt, scale)
	if err != nil {
		return err
	}
	fmt.Printf("rendered %d files\n", len(written))
	return nil
}

func cmdRun(root string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	set := fs.String("set", "all", "which set to run when no names are given: core or all")
	parallel := fs.Int("parallel", 1, "run this many estates concurrently, each against its own isolated floci emulator (#437); 1 (default) is serial, one estate at a time. Every run, serial included, is assigned an explicit FLOCI_PORT by this same allocator (#520), so a script's own hard-coded default only ever applies when it is invoked by hand, outside this runner. A FLOCI_PORT passed via -env (or inherited from this process's own environment) is honored as the base instead of the fixed default (#1040), so two invocations given distinct bases at least 3 apart never collide")
	var envs multiFlag
	fs.Var(&envs, "env", "KEY=VALUE passed to every script (repeatable); FLOCI_PORT=<port> here is honored as this run's port base (#1040) instead of being overridden")
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
	// #1149: refuse before the run rather than stamping every row it
	// produces with an empty commit. A row whose provenance cannot be
	// established is not cheaper to discover afterwards.
	commit, err := headCommit(root)
	if err != nil {
		return err
	}
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

	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	if _, err := Render(root, m, a, tt, scale); err != nil {
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
	commit, err := headCommit(root) // #1149: same rule as cmdRun - no provenance, no run
	if err != nil {
		return err
	}
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
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	if _, err := Render(root, m, a, tt, scale); err != nil {
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
	// 14400 (four hours), not 900 (#1102's open item). Fifteen minutes cannot
	// carry any real estate: the 3,705-resource real-AWS run took over three
	// hours, and on the emulator scale 136's cold_deploy alone is 8,735s. A
	// default that kills every run it is asked to bound is not a ceiling, it
	// is a guaranteed failure that the caller has to know to override, and a
	// backstop nobody can leave at its default gets routed around.
	//
	// Still a real bound, because it is a process ceiling and not a spend
	// one: what bounds spend is the account's AWS Budgets alarm, and this
	// estate is IAM by construction - tools/cost-project puts scale 136 at
	// about $0.50 for four hours. What this stops is a hung run holding a
	// runner or a laptop indefinitely. 0 disables it (commandTimeoutContext),
	// and the largest sizes want more: HANDOFF.md's worked scale-136 command
	// passes -timeout-seconds 34000.
	timeoutSeconds := fs.Int("timeout-seconds", 14400, "Go-side process ceiling, independent of live/live-cert/run.sh's own `timeout` wrapper; 0 disables")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("live-cert needs exactly one estate name, got %d", fs.NArg())
	}
	estate := fs.Arg(0)
	r, res, exit, err := RunLiveCert(root, estate, *target, *region, *ceilingUSD, *timeoutSeconds)
	if err != nil {
		return err
	}
	if res != nil {
		for id, v := range res.Stages {
			fmt.Printf("live-cert %s: stage=%s verdict=%s\n", estate, id, v)
		}
	}
	fmt.Printf("live-cert %s: target=%s exit=%d clear=%v\n", estate, *target, exit, r.Clear)
	if res != nil && res.Refusal != nil {
		fmt.Printf("live-cert %s: REFUSED - %s\n", estate, res.Refusal.Reason)
		if res.Refusal.Needed != nil {
			fmt.Printf("live-cert %s: the arithmetic: %d needed against a limit of %d %s\n", estate, *res.Refusal.Needed, *res.Refusal.Limit, res.Refusal.Unit)
		}
	}

	writes := PlanLiveCertWrites(*target, res)
	if writes.Why != "" {
		fmt.Printf("live-cert %s: %s\n", estate, writes.Why)
	}
	if !writes.LiveCertRow && !writes.ScaleRecord {
		return nil
	}

	m, a, err := loadAll(root)
	if err != nil {
		return err
	}
	if writes.LiveCertRow {
		a.SetLiveCertResult(*r)
		if err := SaveArtifact(root, a); err != nil {
			return err
		}
		fmt.Printf("recorded live-aws certification for %s: clear=%v (live/gauntlet.json live_cert; never counted in sets.core/sets.all)\n", estate, r.Clear)
	}

	// Issue #1051: every real-AWS run also upserts its own structured
	// ScaleRecord into live/gauntlet-scale.json, the same instant its prose
	// detail lands in live/gauntlet.json - so a NEW scale point (the
	// eventual 10,000+ resource run this issue is for) never needs a
	// separate backfill step the way the points `gauntlet scale-backfill`
	// recovers today did. Skipped (with a note, not silently) for an estate
	// this schema recognizes no scale for - a live-aws certification that
	// is not about scale, e.g. reference-ec2-vpc, has nothing for this file
	// to add.
	//
	// Issue #1149: any OTHER reason the scale row does not get written is a
	// failure of the run, not an omission. The live_cert row is already on
	// disk by now - deliberately, it is hours of real-AWS evidence and is
	// never withheld - so the two halves of the run's evidence would
	// otherwise disagree with nothing saying so. scaleErr is carried past
	// the render below rather than returned here, so a half-written run
	// does not also leave a stale published copy behind it.
	//
	// A refused run goes through the SAME path (#1151): the scale record is
	// keyed by (estate, target, scale), so a refusal lands on its own rung
	// and leaves every other one alone - under SupersedeScaleRecord's rule
	// rather than a bare upsert, which is what stops it replacing a rung
	// that was measured. For a refusal this is the run's ONLY record, since
	// PlanLiveCertWrites deliberately keeps it out of live_cert, so #1149's
	// rule binds harder here rather than less: there is no second half to
	// fall back on.
	scaleSource := fmt.Sprintf("gauntlet live-cert %s (commit %s)", estate, r.Commit)
	scaleRec := BuildScaleRecordFromLiveCert(*r, scaleSource)
	if res != nil {
		scaleRec = scaleRec.WithRefusal(res.Refusal)
	}
	// Which home a scale-less refusal has depends on whether the estate is
	// run at a size at all, and only the estate can say (#1233). Asked
	// only when it matters: every other run records the same way it did
	// before this existed, and a live-cert estate the manifest does not
	// carry keeps working right up until one of its runs refuses without
	// naming a rung, at which point it has to be declared one way or the
	// other rather than guessed.
	laddered := false
	if scaleRec.IsRefusal() && scaleRec.Scale == 0 {
		var ladderErr error
		if laddered, ladderErr = EstateHasScaleLadder(m, estate); ladderErr != nil {
			return fmt.Errorf("live-cert %s: this run REFUSED and its refusal names no scale, so where it is recorded depends on whether the estate has a scale ladder - and nothing says: %w", estate, ladderErr)
		}
	}
	plan := planLiveCertScaleRow(estate, writes.ScaleRecord, scaleRec, laddered)
	scaleErr, scaleNote := plan.Err, plan.Note
	if plan.Write {
		var written ScaleRecord
		written, scaleErr = saveLiveCertRecord(root, plan, scaleRec)
		if scaleErr == nil {
			scaleNote = describeScaleWrite(estate, written)
		}
	}

	// One render, after BOTH halves are on disk (issue #1187). It used to
	// run between them: `SaveScaleArtifact` wrote live/gauntlet-scale.json
	// and nothing afterwards touched SiteScalePath, which only Render
	// writes. After the scale-128 certification the two files disagreed by
	// exactly one record - the new one, the entire point of a 39,610-second
	// real-AWS run - and the site simply lacked the point while the repo
	// looked fine. The missing record is always the newest, which is always
	// the one someone went to the most trouble to produce.
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return errors.Join(scaleErr, err)
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return errors.Join(scaleErr, err)
	}
	if _, err := Render(root, m, a, tt, scale); err != nil {
		return errors.Join(scaleErr, err)
	}
	if scaleErr != nil {
		if writes.LiveCertRow {
			return fmt.Errorf("live-cert %s: the live_cert row for this run is recorded in %s but its scale record is NOT in %s, so the two halves of this run's evidence disagree - fix the cause and re-record with `gauntlet scale-import-slice`/`scale-backfill` rather than re-running: %w", estate, ArtifactPath, ScaleRecordsPath, scaleErr)
		}
		// A refusal writes no live_cert row, by design (#1151), so there
		// are no two halves to disagree - there is one record and it did
		// not land. #1149's rule is the same either way: a run whose record
		// did not get written fails, it does not print a note and return.
		return fmt.Errorf("live-cert %s: this run produced no live_cert row (it refused, or spoke nothing) AND its record is NOT in %s, so the run's only evidence is its log: %w", estate, ScaleRecordsPath, scaleErr)
	}
	fmt.Print(scaleNote)
	return nil
}

// scaleRowPlan is what cmdLiveCert does with the scale record it just built:
// write it, skip it as a legitimate omission, or fail the run. Exactly one
// of the three fields is ever set.
//
// It is its own function, like PlanLiveCertWrites, because the difference
// between the last two is a judgement that has to be pinned by a test and
// cannot be reached through cmdLiveCert without a whole checkout.
type scaleRowPlan struct {
	Write bool
	// EstateLevel picks which home Write means. False is the ladder,
	// live/gauntlet-scale.json's `records`, keyed by (estate, target,
	// scale). True is the estate-level refusal shelf, its `refusals`,
	// keyed by (estate, target) - only ever a refusal, and only ever for
	// an estate that declares no ladder (#1233).
	EstateLevel bool
	Note        string
	Err         error
}

// planLiveCertScaleRow decides between #1149's rule (a scale row that does
// not get written fails the run) and its one legitimate exception (a
// certification that was never a scale measurement).
//
// laddered is the estate's own declaration (Estate.ScaleLadder, read
// through EstateHasScaleLadder), never an inference from what this run
// happened to say - see that field's doc comment. It is what tells a
// refusal that forgot its rung from a refusal that has no rung to name.
func planLiveCertScaleRow(estate string, writesScaleRecord bool, rec ScaleRecord, laddered bool) scaleRowPlan {
	switch {
	case !writesScaleRecord:
		// Not reachable from cmdLiveCert, which returns early when a run
		// records nothing at all. Stated rather than assumed, so a later
		// change to PlanLiveCertWrites cannot silently start writing a
		// scale row for a run it decided records nothing.
		return scaleRowPlan{Note: fmt.Sprintf("live-cert %s: this run records no scale row\n", estate)}
	case rec.IsRefusal() && rec.Scale == 0 && !laddered:
		// The estate declares no ladder, so this refusal has no rung to
		// name and never could have - it is an estate-level refusal, and
		// it goes on the shelf beside the ladder rather than on it
		// (#1233). The run's only record still gets written, which is what
		// #1231 requires; live_cert still holds only certifications, which
		// is what #1151 requires.
		return scaleRowPlan{Write: true, EstateLevel: true}
	case rec.IsRefusal() && rec.Scale == 0:
		// The estate DOES declare a ladder, so this refusal declined a rung
		// and did not say which. It is the only evidence this run produced
		// - PlanLiveCertWrites keeps a refusal out of live_cert by design
		// (#1151), so there is no second half to fall back on. A row keyed
		// by scale needs a scale; inventing one, or writing it at scale 0,
		// would put it on a rung nobody ran, and the estate-level shelf
		// above is not a home for it either: this estate's refusals are
		// about sizes, and shelving one would hide exactly which size was
		// declined.
		//
		// This is a FAILURE, and the two cases around it are not, and the
		// line between them is what #1149's rule turns on: below is a
		// certification that was never a scale measurement and has nothing
		// to add, which is an omission with nothing lost. This is a run
		// whose entire result is about to vanish into a log. The cause is a
		// gauntlet_refused call that left out its scale.
		return scaleRowPlan{Err: fmt.Errorf("the refusal names no scale - its `GAUNTLET refused=1` line carried no scale=, so there is no rung on the ladder to record it on, and %q declares a scale ladder (`scale_ladder` in %s) so its refusals are about a size. Pass the scale this run was going to attempt (`gauntlet_refused <scale> ...`): the rung it declined is the whole point of the record", estate, ManifestPath)}
	case rec.Scale == 0 && rec.Resources == nil:
		return scaleRowPlan{Note: fmt.Sprintf("live-cert %s: no scale/resources recognized in this run's own detail text - %s left unchanged, which is expected for a certification that is not a scale measurement\n", estate, ScaleRecordsPath)}
	default:
		return scaleRowPlan{Write: true}
	}
}

// describeScaleWrite is what the runner prints about the row it just wrote:
// which rung, whether it is a measurement or a refusal, and - the part
// #1151 is about - what it superseded. Superseding used to be invisible: a
// scale-50 row measured on 2026-09-15 replaced the 2026-09-11 one with no
// output saying a row had been replaced at all, in a file
// site/content/docs/what-you-pay.md quotes by path.
func describeScaleWrite(estate string, rec ScaleRecord) string {
	var b strings.Builder
	switch {
	case rec.IsRefusal() && rec.Scale == 0:
		// Never "at scale=0": this estate has no ladder, and printing a
		// rung number for a record that is deliberately not on a rung is
		// the misreading the separate shelf exists to prevent (#1233).
		fmt.Fprintf(&b, "recorded an ESTATE-LEVEL REFUSAL for %s target=%s (%s, `refusals`): %s\n", estate, rec.Target, ScaleRecordsPath, rec.Refusal.Reason)
		fmt.Fprintf(&b, "  this estate declares no scale ladder, so the refusal is recorded beside the ladder rather than on a rung; %s keeps its last certification, because a refusal is the absence of one (#1151)\n", ArtifactPath)
	case rec.IsRefusal():
		fmt.Fprintf(&b, "recorded a REFUSAL for %s at scale=%d (%s): %s\n", estate, rec.Scale, ScaleRecordsPath, rec.Refusal.Reason)
	default:
		fmt.Fprintf(&b, "recorded scale measurement for %s at scale=%d (%s)\n", estate, rec.Scale, ScaleRecordsPath)
	}
	if n := len(rec.Supersedes); n > 0 {
		prev := rec.Supersedes[n-1]
		// "recorded at", not "measured at": the row this one replaced may
		// itself have been a refusal, which measured nothing - always so on
		// the estate-level shelf, where refusals are all there is.
		fmt.Fprintf(&b, "  it superseded the row recorded at %s on %s (outcome %s); the chain is %d row(s) deep and is in the record's own supersedes field\n",
			short(prev.Commit), prev.Date, outcomeOrLegacy(ScaleRecord{Outcome: prev.Outcome}), n)
	}
	return b.String()
}

// saveLiveCertRecord writes the record where the plan says it goes: the
// ladder, or the estate-level refusal shelf beside it (#1233). One function
// so the decision and the write cannot drift apart - a plan that says
// "estate level" and a writer that puts the row on the ladder anyway would
// file a refusal at scale 0, which reads as the smallest rung.
func saveLiveCertRecord(root string, plan scaleRowPlan, rec ScaleRecord) (ScaleRecord, error) {
	if plan.EstateLevel {
		return saveLiveCertEstateRefusal(root, rec)
	}
	return saveLiveCertScaleRecord(root, rec)
}

// saveLiveCertEstateRefusal is saveLiveCertScaleRecord for the shelf: same
// validation, same "every failure is returned" discipline (#1149/#1231),
// SupersedeEstateRefusal instead of SupersedeScaleRecord.
func saveLiveCertEstateRefusal(root string, rec ScaleRecord) (ScaleRecord, error) {
	if err := ValidateScaleRecord(rec); err != nil {
		return ScaleRecord{}, fmt.Errorf("built an invalid estate-level refusal record: %w", err)
	}
	sa, err := LoadScaleArtifact(root)
	if err != nil {
		return ScaleRecord{}, err
	}
	written, err := sa.SupersedeEstateRefusal(rec)
	if err != nil {
		return ScaleRecord{}, err
	}
	if err := SaveScaleArtifact(root, sa); err != nil {
		return ScaleRecord{}, err
	}
	return written, nil
}

// saveLiveCertScaleRecord validates rec and writes it into
// live/gauntlet-scale.json, returning the row as it landed - with whatever
// it superseded attached, which is the caller's to report.
//
// Split out so cmdLiveCert's own control flow shows the one thing #1149 is
// about: every failure here is returned, none of them leaves the file
// silently unchanged. SupersedeScaleRecord's own refusal - it will not let a
// refusal replace a measured row (#1151) - is one of those failures and
// reaches the caller like any other, rather than being printed and
// swallowed.
func saveLiveCertScaleRecord(root string, rec ScaleRecord) (ScaleRecord, error) {
	if err := ValidateScaleRecord(rec); err != nil {
		return ScaleRecord{}, fmt.Errorf("built an invalid scale record: %w", err)
	}
	sa, err := LoadScaleArtifact(root)
	if err != nil {
		return ScaleRecord{}, err
	}
	written, err := sa.SupersedeScaleRecord(rec)
	if err != nil {
		return ScaleRecord{}, err
	}
	if err := SaveScaleArtifact(root, sa); err != nil {
		return ScaleRecord{}, err
	}
	return written, nil
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
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	if _, err := Render(root, m, merged, tt, scale); err != nil {
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
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return err
	}
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return err
	}
	if _, err := Render(root, m, a, tt, scale); err != nil {
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
	// tt is read from the real checkout root, never from tmp below: tmp is
	// a write-only scratch directory with no live/estate-types.json of its
	// own, the same reason m, a and bi are all loaded from root rather than
	// re-derived inside Render.
	tt, err := LoadTypeIndexTotals(root)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "gauntlet-render-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	// Estate pages are pruned by reading the target dir; mirror the committed
	// one so pruning logic runs the same way.
	scale, err := loadScaleRecordsBytes(root)
	if err != nil {
		return nil, err
	}
	written, err := Render(tmp, m, a, tt, scale)
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
