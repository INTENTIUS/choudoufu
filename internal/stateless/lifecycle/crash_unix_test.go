// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

//go:build !windows

package lifecycle

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/opentofu/opentofu/internal/stateless/flocitest"
)

// TestStatelessCrashMidApplyAgainstFloci is the crash row of the concurrency
// taxonomy, run rather than argued.
//
//	TF_FLOCI_TEST=1 go test ./internal/stateless/lifecycle/ -run TestStatelessCrashMidApplyAgainstFloci -v
//
// The docs page claims, for "crash mid-apply", that a backend leaves a stale
// lock blocking the team and resources created-but-unrecorded orphaned EVEN
// WITH the lock held, while stateless mode has "markers rode the create call
// itself: all discoverable; nothing to unlock or recover". Every other test
// on this branch runs an apply to completion, so the entire evidence for the
// row a reader is most likely to care about was prose plus a successful
// apply - which is exactly the kind of claim the audit was written to
// distrust.
//
// So: start a real apply against a real (emulated) cloud, kill it partway
// through with SIGINT and then SIGKILL, and check the two halves of the
// claim against what is actually left behind.
//
//  1. No state artifact. Not a tfstate, not a backup, not a lock file, not
//     an errored.tfstate, not a workspace directory - and specifically none
//     of the partial-write wreckage a killed backend run leaves. There is
//     nothing to unlock and nothing to recover because there is nothing.
//  2. Every resource that reached the cloud before the kill carries its
//     markers already. Not "will be marked on the next run" - marked now,
//     readable with the AWS CLI, which is what "markers rode the create call
//     itself" has to mean if it means anything. A resource created without
//     its marker would be invisible to every later run: an orphan, which is
//     the failure mode this whole design claims not to have.
//  3. And the recovery is a re-run. Nothing is unlocked, nothing is
//     imported, nothing is repaired: the next apply picks up the resources
//     the killed one made, by their markers, and finishes the job.
//
// Killing an apply is inherently racy - the test has to interrupt somewhere
// between the first create and the last one - so the interesting failure
// mode is a run that got killed before it did anything, which proves
// nothing. That case is detected and retried rather than passed.
func TestStatelessCrashMidApplyAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "crash-mid-apply")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	crashPort = flocitest.StartFloci(t, "cdf-ra4-crash")

	t.Setenv("AWS_ENDPOINT_URL", "http://localhost:"+crashPort)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(crashFixture()), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}

	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")
	assertNoState(t, dir, "after init")

	// --- The kill ---------------------------------------------------------
	//
	// The delay is tuned by observation, not by hope: the apply is polled
	// for its first live resource and killed once one exists, so the run is
	// always interrupted with work done and work outstanding. The ceiling
	// stops a hung apply from turning this into a timeout.

	var (
		killedOutput string
		liveAtKill   []string
		interrupted  bool
	)
	for attempt := 1; attempt <= 5; attempt++ {
		var completed bool
		killedOutput, liveAtKill, completed = crashApply(t, tofuBin, dir)
		// "Mid-flight" is both halves: something got made, and something did
		// not. Either extreme measures nothing - a run killed before its
		// first create says nothing about markers, and one that finished
		// says nothing about crashing.
		if !completed && len(liveAtKill) > 0 && len(liveAtKill) < crashVPCCount {
			interrupted = true
			break
		}
		t.Logf("attempt %d did not land mid-flight (completed=%v, %d of %d VPCs live); cleaning up and retrying",
			attempt, completed, len(liveAtKill), crashVPCCount)
		crashDeleteVPCs(t)
	}
	if !interrupted {
		t.Fatal("could not interrupt an apply between its first and last create; the test proves nothing in this state")
	}
	t.Logf("the apply was killed with %d of %d VPCs already live: %v", len(liveAtKill), crashVPCCount, liveAtKill)

	// --- 1. Nothing was recorded, and nothing needs recovering ------------

	assertNoState(t, dir, "after the apply was killed mid-flight")

	// assertNoState's list is the one every other test uses. A killed run is
	// where the backend-shaped wreckage would appear specifically, so those
	// names are checked by hand too rather than assumed to be covered.
	for _, name := range []string{
		"errored.tfstate",
		"terraform.tfstate",
		"terraform.tfstate.backup",
		".terraform.tfstate.lock.info",
		"terraform.tfstate.lock.info",
	} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("a killed apply left %s behind (stat error %v); there is supposed to be nothing to recover", name, err)
		}
	}
	// Belt and braces: the retry loop above only accepts an attempt that did
	// not complete, and this says so where a reader of the failure would
	// look for it.
	if strings.Contains(killedOutput, "Apply complete!") {
		t.Errorf("the apply completed despite being killed, so this test measured nothing:\n%s", killedOutput)
	} else {
		t.Log("confirmed: the killed apply never reported completion")
	}

	// --- 2. What was created is already marked ----------------------------
	//
	// Read with the AWS CLI, never through tofu: the claim is that the
	// ownership record is ON the resource, and the tool that wrote it is the
	// last thing that should be asked to confirm it.

	for _, vpcID := range liveAtKill {
		tags := crashTags(t, vpcID)
		if tags[markerEstate] != crashEstate {
			t.Errorf("%s was created by the killed apply and carries tofu-estate=%q, want %q — a resource created without its marker is an orphan no later run can find (all tags: %v)",
				vpcID, tags[markerEstate], crashEstate, tags)
		}
		if tags[markerAddress] == "" {
			t.Errorf("%s was created by the killed apply and carries no tofu-address (all tags: %v)", vpcID, tags)
		} else {
			t.Logf("%s carries %s=%s and %s=%s, written by the create call that made it",
				vpcID, markerEstate, tags[markerEstate], markerAddress, tags[markerAddress])
		}
	}

	// --- 3. Recovery is a re-run, and nothing else ------------------------
	//
	// No unlock, no import, no state surgery: the next apply reads the
	// markers off the resources the killed run made and finishes the job.

	recovery := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(recovery, "Apply complete!") {
		t.Fatalf("the recovery apply did not complete:\n%s", recovery)
	}
	added, changed, destroyed, ok := applySummary(recovery)
	if !ok {
		t.Fatalf("no apply summary from the recovery run:\n%s", recovery)
	}
	t.Logf("recovery apply: %d added, %d changed, %d destroyed", added, changed, destroyed)
	if destroyed != 0 {
		t.Errorf("the recovery apply destroyed %d resource(s); the killed run's work must be picked up, not undone:\n%s",
			destroyed, recovery)
	}

	// The resources the killed run created are the same ones, not
	// replacements: a recovery that quietly rebuilt them would satisfy
	// "apply complete" and be precisely the orphaning this claims not to do.
	after := crashVPCs(t)
	for _, before := range liveAtKill {
		found := false
		for _, id := range after {
			if id == before {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s existed before the recovery and is gone after it; the killed run's resource was orphaned or replaced", before)
		}
	}
	if len(after) != crashVPCCount {
		t.Errorf("the estate has %d VPCs after recovery, want %d: %v", len(after), crashVPCCount, after)
	}

	assertNoState(t, dir, "after the recovery apply")

	converged := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(converged, "No changes.") {
		t.Errorf("the estate did not converge after recovering from the crash:\n%s", converged)
	}
	assertNoState(t, dir, "at the end of the crash test")
}

// crashApply starts an apply, waits until it has created at least one
// resource, kills it, and returns what it printed plus the VPCs that existed
// at the moment of the kill.
//
// SIGINT first, because that is what a ctrl-C or a CI cancellation sends and
// it is the case a graceful shutdown path could handle; SIGKILL after a short
// grace period, because the claim is about a crash and a process that got to
// clean up after itself is not one. Both signals go to the process GROUP: the
// provider plugin is a child, and killing only the parent would leave it
// holding the emulator connection.
func crashApply(t *testing.T, tofuBin, dir string) (out string, live []string, completed bool) {
	t.Helper()

	// -parallelism=1 is half of what makes this measurable at all. Against an
	// emulator on localhost the default ten-way apply creates every VPC in
	// well under the time it takes to notice the first one. Serializing the
	// creates opens a window between them for a signal to land in - which is
	// also the shape a real apply against a real cloud has anyway, where each
	// create is a round trip measured in seconds.
	cmd := exec.Command(tofuBin, "apply", "-auto-approve", "-input=false", "-no-color", "-parallelism=1") //nolint:gosec // this test's own temp dir
	cmd.Dir = dir
	// Signals go to the process GROUP: the provider plugin is a child, and
	// killing only the parent would leave it holding the emulator connection.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// The other half. Polling the cloud is far too slow to catch this - one
	// "aws ec2 describe-vpcs" round trip costs more than the whole apply -
	// so the trigger is the apply's own progress output, read as it is
	// produced. It says "Creation complete after ..." per resource, and the
	// kill goes out on the killAfter'th one: work definitely done, work
	// definitely outstanding, and no dependence on how fast anything is.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the output pipe: %v", err)
	}
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		_ = pr.Close()
		t.Fatalf("starting the apply: %v", err)
	}
	// The child holds its own copy of the write end; this one has to go or
	// the scanner below never sees EOF.
	_ = pw.Close()
	pgid := cmd.Process.Pid

	const killAfter = 3
	var buf strings.Builder
	killed := make(chan struct{})
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		creations := 0
		for sc.Scan() {
			line := sc.Text()
			buf.WriteString(line)
			buf.WriteByte('\n')
			if strings.Contains(line, "Creation complete after") {
				creations++
				if creations == killAfter {
					// SIGINT first, because that is what a ctrl-C or a CI
					// cancellation sends and it is the case a graceful
					// shutdown path could handle.
					_ = syscall.Kill(-pgid, syscall.SIGINT)
					close(killed)
				}
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-killed:
		// SIGKILL after a short grace period: the claim is about a crash,
		// and a process that got to clean up after itself is not one.
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Log("the apply did not exit on SIGINT within 3s; sending SIGKILL, which is the case this test is really about")
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("the apply survived SIGKILL")
			}
		}
	case <-done:
		// Finished before the killAfter'th create: nothing to interrupt.
	case <-time.After(3 * time.Minute):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
		t.Fatal("the apply neither finished nor reached its third create within 3m")
	}

	_ = pr.Close()
	<-scanned

	// The set at the moment of the kill, read after the process is gone: a
	// create that was in flight when the signal landed may still have
	// completed on the cloud's side, and that resource is exactly as much
	// this test's business as the others.
	out = buf.String()
	return out, crashVPCs(t), strings.Contains(out, "Apply complete!")
}

// crashDeleteVPCs empties the estate between attempts, so a retry starts from
// the same place the first attempt did.
func crashDeleteVPCs(t *testing.T) {
	t.Helper()
	for _, id := range crashVPCs(t) {
		if _, err := crashAWS("ec2", "delete-vpc", "--vpc-id", id); err != nil {
			t.Logf("cleaning up %s between attempts: %v", id, err)
		}
	}
}

// crashVPCs is every VPC in the account carrying this test's estate CIDR
// prefix, by ID. Read straight from the cloud.
func crashVPCs(t *testing.T) []string {
	t.Helper()

	out, err := crashAWS("ec2", "describe-vpcs",
		"--query", "Vpcs[].{Id:VpcId,Cidr:CidrBlock}", "--output", "json")
	if err != nil {
		return nil
	}
	var raw []struct {
		Id   string `json:"Id"`
		Cidr string `json:"Cidr"`
	}
	if strings.TrimSpace(out) == "" || strings.TrimSpace(out) == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil
	}
	var ids []string
	for _, v := range raw {
		if strings.HasPrefix(v.Cidr, crashCIDRPrefix) {
			ids = append(ids, v.Id)
		}
	}
	return ids
}

// crashTags is the tag set of one live resource, read on THIS test's port.
// The package's own ec2Tags reads on P4.1's, and a test that silently
// inspected a neighbouring container would pass or fail for reasons that have
// nothing to do with it.
func crashTags(t *testing.T, id string) map[string]string {
	t.Helper()

	out, err := crashAWS("ec2", "describe-tags",
		"--filters", "Name=resource-id,Values="+id,
		"--query", "Tags[].{Key:Key,Value:Value}", "--output", "json")
	if err != nil {
		t.Fatalf("reading the tags of %s: %v", id, err)
	}
	return decodeTagList(t, out)
}

func crashAWS(args ...string) (string, error) {
	full := append([]string{"--endpoint-url", "http://localhost:" + crashPort}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	return strings.TrimSpace(string(out)), err
}

// ---------------------------------------------------------------------------
// The fixture
// ---------------------------------------------------------------------------

// crashPort is this run's emulator port, chosen by the kernel when RA.4's
// test starts its container. crashAWS reads it after that test assigns it.
var crashPort string

const (
	crashEstate     = "ra4-crash"
	crashCIDRPrefix = "10.81."
	// crashVPCCount is large enough that a serialized apply (-parallelism=1,
	// see crashApply) has a window between creates for a signal to land in,
	// and small enough that the recovery run is quick. They are independent
	// of each other on purpose: nothing here depends on anything else here,
	// so the order the apply creates them in is the scheduler's business and
	// the kill can land anywhere in it.
	crashVPCCount = 12

	markerEstate  = "tofu-estate"
	markerAddress = "tofu-address"
)

func crashFixture() string {
	var b strings.Builder
	fmt.Fprintf(&b, `terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true

  s3_use_path_style = true
}
`, crashEstate)

	// Nothing in the fixture writes an ownership tag. Every marker this test
	// reads off a resource created by the killed run got there because
	// stamping put it into the configuration the run planned from, and the
	// create call carried it - which is the entire claim.
	for i := 0; i < crashVPCCount; i++ {
		fmt.Fprintf(&b, `
resource "aws_vpc" "n%d" {
  cidr_block = "%s%d.0/24"
}
`, i, crashCIDRPrefix, i)
	}
	return b.String()
}
