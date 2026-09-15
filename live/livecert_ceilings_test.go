// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"regexp"
	"strconv"
	"testing"
)

// The two ceilings on a real-AWS certification used to be set to values that
// could not carry one (GitHub issue #1102's open item, fixed 2026-09-15).
// `gauntlet live-cert` defaulted -timeout-seconds to 900 and
// .github/workflows/live-cert.yml pinned the job at timeout-minutes: 60,
// while the largest real-AWS run on record - 3,705 resources - took over
// three hours, and scale 136's cold_deploy alone is 8,735s on the emulator.
//
// Neither number failed anything, which is why they survived: nothing ever
// dispatched the workflow, so no run ever hit them. They were a guaranteed
// failure waiting for the first person to try the thing the estate exists
// for, and that person had been blocked on other gates long enough not to
// reach them.
//
// These are bounds, not equalities: a ceiling is free to grow. What the
// guards refuse is a return to a value that cannot carry the run.

// liveCertMinProcessSeconds is what the Go-side ceiling must at least allow.
// Four hours. The 3,705-resource run took over three; this is the smallest
// default that does not cut into a size already measured.
const liveCertMinProcessSeconds = 14400

// liveCertGitHubJobCapMinutes is GitHub's own hard limit on a hosted-runner
// job. Six hours. A timeout-minutes default above it is not a bigger
// ceiling, it is a workflow that fails to start, so the guard bounds this
// one from BOTH sides - the only one here that does.
const liveCertGitHubJobCapMinutes = 360

// liveCertMinJobMinutes is the floor for the workflow's own default. Five
// hours, which carries the 10k run inside the cap above with room for the
// approval wait, the checkout and the artifact upload.
const liveCertMinJobMinutes = 300

func TestLiveCertProcessCeilingCanCarryARealRun(t *testing.T) {
	src, err := os.ReadFile("../tools/gauntlet/main.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`fs\.Int\("timeout-seconds",\s*(\d+),`).FindSubmatch(src)
	if m == nil {
		t.Fatal("tools/gauntlet/main.go declares no -timeout-seconds flag with a literal default; " +
			"if the flag moved, move this guard with it rather than deleting it")
	}
	got, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 && got < liveCertMinProcessSeconds {
		t.Errorf("-timeout-seconds defaults to %d, below %d.\n"+
			"    A real-AWS certification at 3,705 resources took over three hours. A default under four\n"+
			"    hours does not bound that run, it kills it, and the caller only finds out after paying for\n"+
			"    cold_deploy and migrate. Raise the default or pass 0 to disable the ceiling deliberately.",
			got, liveCertMinProcessSeconds)
	}
}

func TestLiveCertJobCeilingCanCarryARealRun(t *testing.T) {
	src, err := os.ReadFile("../.github/workflows/live-cert.yml")
	if err != nil {
		t.Fatal(err)
	}
	yml := string(src)

	if m := regexp.MustCompile(`(?m)^\s*timeout-minutes:\s*(\d+)\s*$`).FindStringSubmatch(yml); m != nil {
		t.Fatalf("live-cert.yml pins timeout-minutes to the literal %s.\n"+
			"    It was 60, which no real estate fits inside. It is an input now so one dispatch can be a\n"+
			"    quick reference-ec2-vpc pass and the next a 10k terralith, and so raising it is recorded on\n"+
			"    the run rather than edited into this file.", m[1])
	}

	m := regexp.MustCompile(`(?s)timeout_minutes:.*?default:\s*"(\d+)"`).FindStringSubmatch(yml)
	if m == nil {
		t.Fatal("live-cert.yml has no timeout_minutes input with a literal default")
	}
	got, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if got < liveCertMinJobMinutes {
		t.Errorf("timeout_minutes defaults to %d, below %d - too short to carry a 10k run", got, liveCertMinJobMinutes)
	}
	if got > liveCertGitHubJobCapMinutes {
		t.Errorf("timeout_minutes defaults to %d, above GitHub's hosted-runner cap of %d.\n"+
			"    That is not a longer run, it is a job that never starts. A size needing more than the cap\n"+
			"    cannot be dispatched here at all and has to be driven by hand.",
			got, liveCertGitHubJobCapMinutes)
	}
}

// TestLiveCertPassesItsJobCeilingToTheTool is the seam between the two
// guards above. Both can be generous and the run still die at the Go side's
// default if the workflow does not pass one, which is exactly the shape the
// old file had: a 60-minute job invoking a tool that stopped at 900s.
func TestLiveCertPassesItsJobCeilingToTheTool(t *testing.T) {
	src, err := os.ReadFile("../.github/workflows/live-cert.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`-timeout-seconds\s+"\$\(\(\s*\$\{\{\s*github\.event\.inputs\.timeout_minutes\s*\}\}\s*\*\s*60\s*-\s*\d+\s*\)\)"`).Match(src) {
		t.Error("live-cert.yml does not derive -timeout-seconds from its own timeout_minutes input.\n" +
			"    Without it the tool falls back to its default and can stop the script well before the job's\n" +
			"    ceiling, which is how a 60-minute job came to invoke a 900-second tool.")
	}
}

// TestLiveCertDoesNotOfferHoldOrResume pins the reasoning in the workflow's
// own header. #1051 asked for these inputs and they are refused here: every
// one of them addresses a work dir by path, and on a hosted runner that
// directory dies with the job, so a held dispatch would leave a live,
// billing estate that no later dispatch could name, resume or tear down.
func TestLiveCertDoesNotOfferHoldOrResume(t *testing.T) {
	src, err := os.ReadFile("../.github/workflows/live-cert.yml")
	if err != nil {
		t.Fatal(err)
	}
	yml := string(src)
	for _, v := range []string{"LIVECERT_HOLD", "LIVECERT_RESUME", "LIVECERT_TEARDOWN_ONLY"} {
		if regexp.MustCompile(`(?m)^\s*` + v + `:`).MatchString(yml) {
			t.Errorf("live-cert.yml sets %s. A CI run's work dir dies with the job, so a held estate "+
				"could never be resumed or torn down by a later dispatch. That loop is a local one.", v)
		}
	}
}
