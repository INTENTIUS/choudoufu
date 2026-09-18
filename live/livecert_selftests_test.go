// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// This file is issue #1267.
//
// live/live-cert/ holds six selftest-*.sh scripts. Every one of them proves
// something about the PAID real-AWS path - the path whose defects cost
// money and wall clock rather than a red cell - and until #1143 wired the
// first of them in, nothing invoked any of them. A script that is a guard
// only when someone remembers to type its name is not a guard; #691 named
// that class one level up ("a test tier that exists but gates nothing") and
// this is the same shape in shell.
//
// Four of the remaining five are hermetic: they extract functions verbatim
// out of live/live-cert/terralith-scale.sh (or source lib/live-cert.sh) and
// drive them against stubs, with no AWS, no docker, no terraform and no go
// build. Those run here, in the ordinary Go tier, on every push and pull
// request. The fifth, selftest-kill.sh, launches the real harness against
// the pinned emulator and needs docker, terraform and the AWS CLI, so it
// runs as its own ci.yml job; TestCIRunsTheKillSelftest below is what keeps
// that job from quietly going away.
//
// The roster is NOT the point of trust here - the disk is.
// TestLiveCertSelftestRosterIsComplete compares this table against
// live/live-cert/selftest-*.sh in both directions, so a seventh selftest
// written tomorrow reddens this package until somebody says where it runs.
// That, rather than any single wiring, is what closes #1267's class.

// liveCertSelftestDir is where the scripts live, relative to this package.
const liveCertSelftestDir = "live-cert"

// selftestRunner says what actually invokes a selftest. The strings are
// read by humans in failure messages; the behaviour hangs off the
// constants, so a typo in one is a compile error rather than a silently
// unwired script.
type selftestRunner int

const (
	// runsHere: TestLiveCertSelftestsPass below execs it.
	runsHere selftestRunner = iota
	// runsInGoTestFile: another test file in this package already execs
	// it. The roster check reads that file and asserts the script's path
	// is in it, so a rename on either side is caught.
	runsInGoTestFile
	// runsInCIJob: a ci.yml job execs it, because it needs docker,
	// terraform and the AWS CLI.
	runsInCIJob
)

type liveCertSelftest struct {
	// script is the basename under live/live-cert/.
	script string
	// proves is the issue the script was written for, for the failure
	// message: a reader who has to decide what to do about a red one
	// wants that issue, not this file.
	proves string
	runner selftestRunner
	// where names the Go test file (runsInGoTestFile) or the ci.yml job
	// (runsInCIJob) that runs it.
	where string
	// bound is how long runsHere gives the script before killing it and
	// failing. Not a performance budget: it is issue #1267's hazard 2 -
	// a wired selftest that can hang turns one slow guard into a stuck
	// CI job, so every one of these fails in bounded time. The measured
	// runtimes on a 2026-09-17 laptop are in `measured`.
	bound time.Duration
	// measured is what the script actually took when it was wired, and
	// the hazard note for why the bound above is safe.
	measured string
}

// liveCertSelftests is the roster. Every live/live-cert/selftest-*.sh must
// appear here exactly once.
var liveCertSelftests = []liveCertSelftest{
	{
		script:   "selftest-pagination.sh",
		proves:   "#1047 - `--query 'length(...)' --output text` counts PER PAGE, so lib/live-cert.sh's livecert_rgta_count counts the ARN array instead",
		runner:   runsHere,
		bound:    60 * time.Second,
		measured: "0.3s. Stubs `aws` with a three-page fake and sources the shipped lib; nothing polls, sleeps or waits.",
	},
	{
		script:   "selftest-teardown-timeout.sh",
		proves:   "#1048 - teardown()'s untrusted step ran with no timeout, so a hang there blocked the trusted destroy indefinitely",
		runner:   runsHere,
		bound:    120 * time.Second,
		measured: "2.2s. Already carries its own OUTER_BOUND_S=15 `timeout` around the extracted teardown(), under the fake hung step's own 30s sleep, so the unfixed script fails in seconds rather than hanging.",
	},
	{
		script:   "selftest-hold-resume.sh",
		proves:   "#1032 - LIVECERT_HOLD / LIVECERT_RESUME / teardown-only, so one real-AWS stage costs minutes instead of a full deploy-and-destroy cycle",
		runner:   runsHere,
		bound:    60 * time.Second,
		measured: "0.6s. Three cases over extracted functions and one teardown-only dispatch that exits before `0. tools`; no sleep, poll or wait anywhere in it.",
	},
	{
		script:   "selftest-record-store-s3.sh",
		proves:   "#1145 - RECORD_STORE_BACKEND=s3 took the local-disk else in both branches, so teardown printed VERIFIED EMPTY over a store it never listed",
		runner:   runsHere,
		bound:    120 * time.Second,
		measured: "1.6s. Stub `aws` over two text files by default; LIVECERT_SELFTEST_ENDPOINT swaps in the real CLI against a real S3. Neither mode polls.",
	},
	{
		script:   "selftest-index-wait.sh",
		proves:   "#1046, #1049, #1143 - index_wait polled for a VERIFIED total the Resource Groups Tagging API can never serve, and three real-AWS runs read the resulting plateau as a slow index",
		runner:   runsInGoTestFile,
		where:    "indexwait_partition_test.go",
		measured: "7.4s. Every case passes index_wait a small bound; #1143's first red arm hung on the production 1800s one, which is where #1267's hazard 2 comes from.",
	},
	{
		script: "selftest-kill.sh",
		proves: "#440 stage 1 - a real SIGTERM mid-apply still runs the harness's trap, tears the estate down and removes the emulator",
		runner: runsInCIJob,
		where:  killSelftestJobName,
		measured: "19s against the pinned emulator with TOFU_BIN prebuilt, 7s of it setup; the first cold GitHub runner spent over 30s on " +
			"setup alone. It is the one selftest that needs docker, terraform and the AWS CLI, so it cannot run in this package. Its three " +
			"waits are bounded: setup by SELFTEST_KILL_SETUP_BOUND_S (600s), the apply by SELFTEST_KILL_APPLY_BOUND_S (180s), " +
			"and the post-SIGTERM wait for the harness's trap by SELFTEST_KILL_WAIT_BOUND_S (240s, watchdog-enforced). " +
			"What it proves is narrower than its own PASS line says: #1279 - its \"independent verification\" listing is unreachable on any " +
			"passing run, because the harness removes the emulator container before the driver gets there.",
	},
}

// TestLiveCertSelftestRosterIsComplete is the guard that actually closes
// #1267, as opposed to the wirings below, which close the six instances of
// it that exist today.
//
// It compares the roster against the glob in both directions. A new
// selftest-*.sh with no roster entry reddens this package until someone
// says what runs it; a roster entry whose script has been renamed or
// deleted reddens it too, rather than leaving a wiring that execs a path
// that no longer exists.
func TestLiveCertSelftestRosterIsComplete(t *testing.T) {
	onDisk, err := filepath.Glob(filepath.Join(liveCertSelftestDir, "selftest-*.sh"))
	if err != nil {
		t.Fatalf("globbing %s/selftest-*.sh: %v", liveCertSelftestDir, err)
	}
	if len(onDisk) == 0 {
		t.Fatalf("no %s/selftest-*.sh found at all; the glob is broken rather than the directory being empty "+
			"(this test runs with the package directory as its working directory)", liveCertSelftestDir)
	}

	rostered := map[string]bool{}
	for _, s := range liveCertSelftests {
		if rostered[s.script] {
			t.Errorf("%s appears twice in liveCertSelftests", s.script)
		}
		rostered[s.script] = true
	}

	found := map[string]bool{}
	for _, p := range onDisk {
		name := filepath.Base(p)
		found[name] = true
		if !rostered[name] {
			t.Errorf("live/live-cert/%s is not in liveCertSelftests (live/livecert_selftests_test.go).\n"+
				"That means nothing runs it, which is the whole of issue #1267: a script that looks like a guard "+
				"and is one only if someone remembers to type its name.\n"+
				"Add an entry saying what runs it - runsHere if it is hermetic and bounded, runsInCIJob if it needs "+
				"docker/terraform/the AWS CLI - or, if it genuinely is a hand-run tool rather than a guard, add the "+
				"entry anyway and say so, so the exemption is written down where the next reader will find it.", name)
		}
	}
	for _, s := range liveCertSelftests {
		if !found[s.script] {
			var names []string
			for n := range found {
				names = append(names, n)
			}
			sort.Strings(names)
			runBy := s.where
			if runBy == "" {
				runBy = "TestLiveCertSelftestsPass in this file"
			}
			t.Errorf("liveCertSelftests names live/live-cert/%s, which does not exist. Present: %s.\n"+
				"A renamed script leaves its wiring exec'ing a path that is gone, which fails as loudly as it should "+
				"but says nothing useful; fix the roster entry, and whatever runs it: %s.", s.script, strings.Join(names, ", "), runBy)
		}
	}
}

// TestLiveCertSelftestsPass runs every hermetic selftest and fails with its
// whole output. Each script prints one line per confirmed property and one
// FAIL line per broken one, so the Go-side failure message is the
// self-test's own reasoning rather than an exit code - the shape #1143
// established for selftest-index-wait.sh.
//
// The bound is the point of the CommandContext: a selftest that hangs must
// fail this package in bounded time rather than stall it (#1267 hazard 2).
// WaitDelay makes that bite even when the script has left a child holding
// the pipe open.
func TestLiveCertSelftestsPass(t *testing.T) {
	for _, st := range liveCertSelftests {
		if st.runner != runsHere {
			continue
		}
		t.Run(strings.TrimSuffix(strings.TrimPrefix(st.script, "selftest-"), ".sh"), func(t *testing.T) {
			script := filepath.Join(liveCertSelftestDir, st.script)
			ctx, cancel := context.WithTimeout(context.Background(), st.bound)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", script)
			cmd.WaitDelay = 5 * time.Second
			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatalf("live/live-cert/%s did not finish within %s and was killed.\n"+
					"It is bounded here on purpose (#1267): %s\n"+
					"Its output up to the kill:\n%s", st.script, st.bound, st.measured, out.String())
			}
			if err != nil {
				t.Fatalf("live/live-cert/%s failed (%v).\nIt proves: %s\nIts own output:\n%s",
					st.script, err, st.proves, out.String())
			}
		})
	}
}

// TestEveryGoTierSelftestIsActuallyExecd closes the gap between the roster
// saying a script runs somewhere in this package and it actually doing so.
// For runsInGoTestFile entries - selftest-index-wait.sh, wired by #1143 -
// it reads the named file and requires the script's path to be in it.
//
// Without this, renaming the script or deleting TestIndexWaitSelftestPasses
// would leave the roster claiming coverage nobody provides, which is
// exactly #1267 again one level in.
func TestEveryGoTierSelftestIsActuallyExecd(t *testing.T) {
	for _, st := range liveCertSelftests {
		if st.runner != runsInGoTestFile {
			continue
		}
		data, err := os.ReadFile(st.where)
		if err != nil {
			t.Errorf("liveCertSelftests says live/live-cert/%s is run by %s, which cannot be read: %v",
				st.script, st.where, err)
			continue
		}
		// The needle is built from the roster entry rather than written
		// out, so a rename on either side breaks it.
		//
		// It has to be an exec, on a line that is not a comment. Written
		// as a plain strings.Contains over the file this test was GREEN
		// after the exec was deleted, because indexwait_partition_test.go's
		// doc comment names the script three times in prose - a guard
		// passing over a condition it cannot observe, which is the class
		// #1267 is clearing. Proven red afterwards by replacing that file's
		// exec.Command with exec.Command("bash", "-c", "true").
		needle := liveCertSelftestDir + "/" + st.script
		execd := false
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, needle) && strings.Contains(line, "exec.Command") {
				execd = true
				break
			}
		}
		if !execd {
			t.Errorf("%s has no exec.Command line naming %q, so nothing in this package runs live/live-cert/%s.\n"+
				"It proves: %s\n"+
				"Prose mentioning the script does not count and is deliberately not accepted here. Either restore the "+
				"wiring there, move the entry to runsHere, or - if the exec now builds the path some other way - widen "+
				"this check rather than deleting it.",
				st.where, needle, st.script, st.proves)
		}
	}
}

// killSelftestJobName is the ci.yml job that runs live/live-cert/selftest-kill.sh.
const killSelftestJobName = "livecert-selftest-kill"

// TestCIRunsTheKillSelftest is #1267's wiring for the one selftest that
// cannot run in this package.
//
// selftest-kill.sh launches live/live-cert/reference-ec2-vpc.sh against the
// pinned floci emulator, kills it mid-apply with a real SIGTERM, and checks
// that the harness's trap still tore down. That needs docker, terraform and
// the AWS CLI, which the fast job has and this package does not.
//
// It is a ci.yml job, on push and pull_request, rather than a step in
// floci-tier.yml, and the reason is measured rather than stylistic: the
// floci-tier nightly has failed on every run from 2026-09-12 to 2026-09-17,
// every gated test failing at 0.00s with "terraform is required by this
// test but is not on PATH" because that workflow never installs it. A new
// guard added to an already-red job is a guard whose failure nobody would
// see - #1267's own complaint, satisfied a different way. That tier's own
// breakage is #1280; it is not this test's business.
//
// What the job proves is narrower than selftest-kill.sh's PASS line claims.
// Its emptiness half - the "independent verification" that lists the
// endpoint itself - is unreachable on any passing run, because the harness
// removes the emulator container as teardown's last step and the driver
// then has nothing to list. That is #1279, found while wiring this. The
// trap, the exit code, the teardown banner and the container's removal are
// real, and they are what this job guards until #1279 lands.
func TestCIRunsTheKillSelftest(t *testing.T) {
	data, err := os.ReadFile(ciWorkflowRel)
	if err != nil {
		t.Fatalf("reading %s: %v", ciWorkflowRel, err)
	}
	job, ok := workflowJob(string(data), killSelftestJobName)
	if !ok {
		t.Fatalf("%s has no `%s:` job.\n"+
			"live/live-cert/selftest-kill.sh is then back to where issue #1267 found it: written, committed, "+
			"and run only when someone remembers to type its name. It is the proof obligation for #440 stage 1 "+
			"(a mid-apply SIGTERM still tears the estate down), which guards the paid real-AWS path.",
			ciWorkflowRel, killSelftestJobName)
	}

	// Commands only. The job's steps carry comments that talk about the
	// script, the binary and the bound in order to explain them, and a
	// substring scan over the whole block would be satisfied by the
	// explanation of a step that had been deleted.
	var commands []string
	for _, line := range strings.Split(job, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		commands = append(commands, line)
	}
	jobCommands := strings.Join(commands, "\n")

	for _, want := range []struct {
		substr string
		why    string
	}{
		{"live/live-cert/selftest-kill.sh", "the job must run the script itself"},
		{"hashicorp/setup-terraform", "the harness shells out to a literal `terraform` for cold_deploy's destroy; without the binary the run fails for a reason that says nothing about the product, which is exactly how floci-tier.yml has been red since 2026-09-12"},
		{"TOFU_BIN", "the harness rebuilds choudoufu itself when this is unset, and the kill has to land inside a ~29s apply window rather than behind a cold `go build`"},
		{"=== selftest-kill: PASS", "a zero exit is not the evidence: the job must require the script's own verdict line, the same discipline the validate-generated-terralith job holds to"},
		{"timeout ", "the script is bounded internally, but the job bounds it again so a hang in docker or the image pull reddens rather than stalls"},
	} {
		if !strings.Contains(jobCommands, want.substr) {
			t.Errorf("the %s job in %s does not run anything containing %q: %s", killSelftestJobName, ciWorkflowRel, want.substr, want.why)
		}
	}

	// On the ordinary path, not dispatch-only. The same requirement
	// TestCIValidationRunsOnOrdinaryEvents makes of #578's job: a check
	// only a human can trigger is the same as no check.
	triggers := string(data)
	if i := strings.Index(triggers, "\njobs:"); i > 0 {
		triggers = triggers[:i]
	}
	for _, event := range []string{"push:", "pull_request:"} {
		if !strings.Contains(triggers, event) {
			t.Errorf("%s no longer runs on %s, so the %s job does not run on an ordinary change",
				ciWorkflowRel, strings.TrimSuffix(event, ":"), killSelftestJobName)
		}
	}
}

// TestKillSelftestPrintsWhatItRedirected is the guard on the defect that
// made the first CI run of this job unactionable.
//
// cold_deploy's init and apply are redirected into files inside the
// harness's work dir, the apply is additionally backgrounded, and the
// driver's own cleanup deletes that dir on the way out. So the whole
// evidence in the job log was two banner lines and "FAIL - see above" with
// nothing above: the output existed, was never printed, and was then
// removed. A selftest whose failure message points at output it did not
// print cannot be acted on the first time it goes red, which is the only
// time it matters.
//
// Red-armed by deleting the dump_harness_artifacts call on the early-exit
// path; the remaining needles are the call sites and the tail that reads
// the files, so removing either fails this.
func TestKillSelftestPrintsWhatItRedirected(t *testing.T) {
	path := filepath.Join(liveCertSelftestDir, "selftest-kill.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(data)

	// Bare CALL lines only. Counting occurrences of the name was the first
	// attempt and it stayed green after a call was deleted, because the name
	// also appears in the function's definition and in the comment above it:
	// four occurrences, three left after the deletion, threshold never
	// crossed. A line whose entire content is the name is unambiguously a
	// call, and cannot be satisfied by the prose describing one.
	calls := 0
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "dump_harness_artifacts" {
			calls++
		}
	}
	if calls < 2 {
		t.Errorf("live/live-cert/selftest-kill.sh calls dump_harness_artifacts %d time(s); both failure paths need it.\n"+
			"Those are the early exit, when synchronization never reached a mid-apply moment and no SIGTERM was sent, "+
			"and the late one after the teardown assertions. The harness's redirected output is deleted by this "+
			"driver's own cleanup and is usually the only place a failure's reason exists at all.", calls)
	}
	for _, want := range []struct {
		substr string
		why    string
	}{
		{"cold_deploy_apply.out", "the backgrounded apply's output is the file the first CI failure needed and did not get"},
		{`tail -40 "$f"`, "the dump has to actually read the files, not just list the directory"},
	} {
		if !strings.Contains(src, want.substr) {
			t.Errorf("live/live-cert/selftest-kill.sh no longer contains %q: %s", want.substr, want.why)
		}
	}
}

// TestKillSelftestWaitIsBounded is #1267 hazard 2 held against the one
// selftest that drives a real process.
//
// selftest-kill.sh sends the harness a SIGTERM and then waits for its trap
// to finish. That wait used to be a bare `wait "$HARNESS_PID"`, which is
// unbounded, and a hung trap - the exact defect #1048 found in the sibling
// teardown path - would have turned this guard into a stuck CI job instead
// of a red one. It now runs under a SIGKILL watchdog, proven red at
// SELFTEST_KILL_WAIT_BOUND_S=10 against a teardown() with a `sleep 3000`
// in it: harness exited 137, verdict FAIL, 28s wall.
//
// An earlier version of this comment justified the watchdog by claiming a
// `kill -0` poll could not work, because an exited child stays a zombie
// until its parent waits on it and `kill -0` on a zombie succeeds from that
// parent. That claim is FALSE for a bash background job and the control
// that was supposed to confirm it disproved it instead: bash reaps its own
// background children and keeps the status for `wait`, so `kill -0` fails
// once the child is gone (measured on bash 3.2.57). The watchdog is still
// the right mechanism - it bounds the `wait` itself rather than racing it,
// needs no loop, and yields an unambiguous 137 - but it is not the only one
// that could work, and this test pins the mechanism, not that story.
func TestKillSelftestWaitIsBounded(t *testing.T) {
	path := filepath.Join(liveCertSelftestDir, "selftest-kill.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	src := string(data)

	for _, want := range []struct {
		substr string
		why    string
	}{
		// The assignment, not the bare name: "WAIT_BOUND_S" is a
		// substring of "SELFTEST_KILL_WAIT_BOUND_S", so the short form
		// was satisfied by the doc comment that explains the variable
		// even with the assignment deleted. A needle that matches its own
		// explanation is the pgrep-matches-its-own-command-line bug
		// wearing a test's clothes.
		{`WAIT_BOUND_S="${SELFTEST_KILL_WAIT_BOUND_S:`, "the bound has to be assigned, not just described"},
		{"kill -KILL \"$HARNESS_PID\"", "the watchdog is what enforces the bound on the wait itself"},
		{"-eq 137", "the script must distinguish \"the trap hung and we killed it\" from \"the trap ran and exited non-130\", or a hang reports as the wrong defect"},
		// The two synchronization bounds, added after the first CI run
		// (2026-09-18) burned a single 30s bound on a cold runner's image
		// pull and provider download and then reported it as a stalled
		// apply. Separate bounds so that neither interval hides in the
		// other's slack; pinned by assignment for the same reason as the
		// wait bound above.
		{`SETUP_BOUND_S="${SELFTEST_KILL_SETUP_BOUND_S:`, "setup - image pull, health, AMI, init - must be bounded separately from the apply, and generously: none of it is what this selftest is about"},
		{`APPLY_BOUND_S="${SELFTEST_KILL_APPLY_BOUND_S:`, "the apply's own bound is the one that means something; folding it back into the setup bound is how a slow runner reads as a stalled apply"},
	} {
		if !strings.Contains(src, want.substr) {
			t.Errorf("live/live-cert/selftest-kill.sh no longer contains %q: %s.\n"+
				"Restoring an unbounded `wait` here makes a hung harness trap stall CI instead of reddening it (#1267 hazard 2).",
				want.substr, want.why)
		}
	}
}
