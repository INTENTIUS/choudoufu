// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/command/views"
	"github.com/opentofu/opentofu/internal/command/workdir"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/terminal"
)

// These tests drive plain "choudoufu plan" and plain "choudoufu apply" over a
// configuration carrying a live block, against the same mock cloud the
// live-plan tests use. What they are checking, together, is that the
// block is the only switch: nothing here passes a flag that asks for
// stateless behaviour, and the last test in the file checks that a
// configuration without the block still writes its state file exactly as it
// always did.

// ---------------------------------------------------------------------------
// Plan
// ---------------------------------------------------------------------------

// TestStatelessMode_plainPlan: "choudoufu plan", no flags, runs the pipeline -
// discovery binds the marker-identified VPC, the projection reads both
// resources, and the plan is empty.
func TestStatelessMode_plainPlan(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	cloud := liveBlockCloud()
	c, done := newLiveBlockPlanCommand(t, cloud)

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("plan is not empty:\n%s", stdout)
	}
	if !cloud.imported("aws_vpc", "vpc-owned") {
		t.Errorf("the VPC was never read from the live system; imports were %v", cloud.imports)
	}
	assertNoStateArtifacts(t, td)
}

// TestStatelessMode_planParity: the plan a live block produces is the
// plan "choudoufu live-plan -estate=..." produces for the same estate. The
// two fixtures differ only by the block, so any difference in the rendered
// plan would be a difference in the pipeline.
func TestStatelessMode_planParity(t *testing.T) {
	viaBlock := func() string {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		c, done := newLiveBlockPlanCommand(t, liveBlockCloud())
		if code := c.Run([]string{"-no-color"}); code != 0 {
			out := done(t)
			t.Fatalf("plan exit code %d, want 0\n%s\n%s", code, out.Stdout(), out.Stderr())
		}
		return done(t).Stdout()
	}()

	viaCommand := func() string {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan"), td)
		t.Chdir(td)

		c, done := newLivePlanCommand(t, liveBlockCloud())
		if code := c.Run([]string{"-no-color", "-estate=stateless-unit"}); code != 0 {
			out := done(t)
			t.Fatalf("live-plan exit code %d, want 0\n%s\n%s", code, out.Stdout(), out.Stderr())
		}
		return done(t).Stdout()
	}()

	if got, want := statelessPlanBody(viaBlock), statelessPlanBody(viaCommand); got != want {
		t.Errorf("plain plan and live-plan disagree.\n--- plain plan ---\n%s\n--- live-plan ---\n%s", got, want)
	}
}

// statelessPlanBody is the part of a plan's output the two entry points must
// agree on: everything up to the summary line. What follows is the next-step
// hint, which differs because only one of the two commands can save a plan
// file, and that difference is the point of the -out rejection rather than a
// pipeline difference.
func statelessPlanBody(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Plan:") || strings.Contains(line, "No changes.") {
			return strings.Join(lines[:i+1], "\n")
		}
	}
	return out
}

// TestStatelessMode_livePlanIsAnAlias: with the block present,
// "choudoufu live-plan" is "choudoufu plan", and the -estate flag the block
// replaces is refused rather than silently winning.
func TestStatelessMode_livePlanIsAnAlias(t *testing.T) {
	t.Run("delegates", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		cloud := liveBlockCloud()
		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, cloud)}

		code := c.Run([]string{"-no-color"})
		output := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if !strings.Contains(output.Stdout(), "No changes.") {
			t.Errorf("the alias did not run the pipeline:\n%s", output.Stdout())
		}
		assertNoStateArtifacts(t, td)
	})

	t.Run("refuses -estate", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}

		code := c.Run([]string{"-no-color", "-estate=somewhere-else"})
		output := done(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
		}
		if !strings.Contains(output.Stderr(), "Estate named by both the live block and -estate") {
			t.Errorf("wrong diagnostic:\n%s", output.Stderr())
		}
	})
}

// ---------------------------------------------------------------------------
// Apply, and the no-persistence proof
// ---------------------------------------------------------------------------

// TestStatelessMode_plainApply is the fork's first real apply: a plain
// "choudoufu apply -auto-approve" that creates a resource in the (mock) cloud with
// the ownership markers stamping put on it, and writes no state anywhere.
//
// The no-persistence proof has two halves, because either one alone is weak.
// The filesystem half walks the whole working directory afterwards and fails
// on any state artifact - a state file, a backup, a lock, an errored.tfstate,
// a workspace directory. The manager half asserts that PersistState was
// actually called: the run did take the code path that writes state, and that
// path wrote nothing. Without it, "no file appeared" would also be satisfied
// by an apply that never got as far as persisting.
func TestStatelessMode_plainApply(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	// An empty estate: nothing exists yet, so the apply creates both.
	cloud := newStatelessTestCloud()

	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}

	var captured *statelessRunner
	defer statelessRunnerTestHook(func(r *statelessRunner) { captured = r })()

	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.") {
		t.Errorf("apply did not create both resources:\n%s", stdout)
	}

	// The markers ride the apply. They are in no fixture: stamping put them
	// into the configuration this run planned from, so this is the moment
	// create-with-markers becomes real.
	for _, addr := range []string{"aws_s3_bucket.data", "aws_vpc.main"} {
		tags := cloud.applied[addr]
		if tags == nil {
			t.Fatalf("%s was never applied; applied: %v", addr, cloud.applied)
		}
		if got := tags["tofu-estate"]; got != "stateless-unit" {
			t.Errorf("%s was created with tofu-estate %q, want %q", addr, got, "stateless-unit")
		}
		if got := tags["tofu-address"]; got != addr {
			t.Errorf("%s was created with tofu-address %q, want %q", addr, got, addr)
		}
	}

	assertNoStateArtifacts(t, td)

	if captured == nil {
		t.Fatal("no stateless runner was installed, so this apply was not stateless")
	}
	if n := captured.mgr.Persists(); n == 0 {
		t.Error("PersistState was never called, so the no-persistence claim was not exercised")
	}
}

// TestStatelessMode_plainApplySnapshot is the wiring proof for P4.2's
// snapshot_path attribute: a live block that sets it makes a plain
// "choudoufu apply" write the observational snapshot after the run, naming the
// estate this run resolved and listing the resources it applied - and it is
// the only artifact left behind; the no-persistence proof from
// TestStatelessMode_plainApply still has to hold beside it.
func TestStatelessMode_plainApplySnapshot(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-snapshot"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()

	view, done := testView(t)
	c := &ApplyCommand{Meta: liveBlockMeta(view, cloud)}

	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stdout(), "Apply complete! Resources: 2 added, 0 changed, 0 destroyed.") {
		t.Fatalf("apply did not create both resources:\n%s", output.Stdout())
	}

	snapPath := filepath.Join(td, "snapshot", "estate.json")
	data, err := os.ReadFile(snapPath)
	if err != nil {
		t.Fatalf("reading the snapshot at %s: %s", snapPath, err)
	}

	// A hand-written shape rather than the writer's own type, which is
	// unexported precisely so that no package outside the writer can decode
	// this file into it. Reading it here to assert what the writer produced
	// is the verification of that contract, not a violation of it; see
	// TestSnapshot_noReader in internal/stateless/projection for where the
	// line is drawn and why test files sit on this side of it.
	var snap struct {
		FormatVersion string `json:"formatVersion"`
		Estate        string `json:"estate"`
		WrittenAt     string `json:"writtenAt"`
		Resources     []struct {
			Address        string `json:"address"`
			AttributesHash string `json:"attributesHash"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decoding the snapshot: %s\nraw: %s", err, data)
	}
	if snap.FormatVersion == "" {
		t.Error("the snapshot has no formatVersion")
	}
	if snap.Estate != "stateless-unit" {
		t.Errorf("estate is %q, want %q", snap.Estate, "stateless-unit")
	}
	if snap.WrittenAt == "" {
		t.Error("writtenAt is empty")
	}

	gotAddrs := make(map[string]bool, len(snap.Resources))
	for _, res := range snap.Resources {
		gotAddrs[res.Address] = true
		if res.AttributesHash == "" {
			t.Errorf("%s has no attributesHash", res.Address)
		}
	}
	for _, want := range []string{"aws_s3_bucket.data", "aws_vpc.main"} {
		if !gotAddrs[want] {
			t.Errorf("the snapshot does not list %s: %v", want, gotAddrs)
		}
	}

	// The snapshot is the one artifact this run is allowed to leave behind;
	// everything the state-artifact walk looks for still must not exist.
	assertNoStateArtifacts(t, td)
}

// TestStatelessMode_applyRejections: the two saved-plan halves and the
// options stateless mode v0 removes the ground for, refused rather than
// ignored.
func TestStatelessMode_applyRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"planfile", []string{"saved.tfplan"}, "Applying a saved plan file is not available under live resource markers"},
		{"destroy", []string{"-destroy", "-auto-approve"}, "Only the normal planning mode is available under live resource markers"},
		{"state-out", []string{"-auto-approve", "-state-out=other.tfstate"}, "State file options are not available under live resource markers"},
		{"json", []string{"-auto-approve", "-json"}, "Machine-readable output is not available under live resource markers yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			c := &ApplyCommand{Meta: liveBlockMeta(view, newStatelessTestCloud())}

			code := c.Run(append([]string{"-no-color"}, tc.args...))
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			// -json sends diagnostics to stdout, everything else to
			// stderr, so both are searched.
			if !strings.Contains(output.Stderr()+output.Stdout(), tc.want) {
				t.Errorf("wrong diagnostic:\nstderr:\n%s\nstdout:\n%s", output.Stderr(), output.Stdout())
			}
			assertNoStateArtifacts(t, td)
		})
	}
}

// TestStatelessMode_planRejections is the plan half of the same list.
func TestStatelessMode_planRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"out", []string{"-out=tfplan"}, "Saved plan files are not available under live resource markers"},
		{"state", []string{"-state=other.tfstate"}, "State file options are not available under live resource markers"},
		{"refresh-only", []string{"-refresh-only"}, "Only the normal planning mode is available under live resource markers"},
		{"json", []string{"-json"}, "Machine-readable output is not available under live resource markers yet"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

			code := c.Run(append([]string{"-no-color"}, tc.args...))
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			if !strings.Contains(output.Stderr()+output.Stdout(), tc.want) {
				t.Errorf("wrong diagnostic:\nstderr:\n%s\nstdout:\n%s", output.Stderr(), output.Stdout())
			}
			assertNoStateArtifacts(t, td)
		})
	}
}

// TestStatelessMode_refreshRefused: "choudoufu refresh" writes a state file as
// its entire purpose, so a stateless configuration is refused rather than
// left to produce one from a command that changes nothing.
func TestStatelessMode_refreshRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	view, done := testView(t)
	c := &RefreshCommand{Meta: liveBlockMeta(view, liveBlockCloud())}

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "Refresh is not available under live resource markers") {
		t.Errorf("wrong diagnostic:\n%s", output.Stderr())
	}
	assertNoStateArtifacts(t, td)
}

// TestStatelessMode_backendConflict: a configuration asking for both is
// refused by the decoder, which is the earliest wall and the one every
// command passes through.
func TestStatelessMode_backendConflict(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-backend"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "Both a backend and a live configuration are present") {
		t.Errorf("wrong diagnostic:\n%s", output.Stderr())
	}
	assertNoStateArtifacts(t, td)
}

// ---------------------------------------------------------------------------
// The invariant: no block, no change
// ---------------------------------------------------------------------------

// TestStatelessMode_stockModeUnchanged is the guard the whole fork rides on.
// A configuration without a live block applies exactly as it always did:
// a state file is written, in the default place, with the applied resource in
// it. The rest of this package's test suite says the same thing at greater
// length; this one says it about the specific code path P4.1 added a branch
// to.
func TestStatelessMode_stockModeUnchanged(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("apply"), td)
	t.Chdir(td)

	p := applyFixtureProvider()
	view, done := testView(t)
	c := &ApplyCommand{
		Meta: Meta{
			WorkingDir:       workdir.NewDir("."),
			testingOverrides: metaOverridesForProvider(p),
			View:             view,
		},
	}

	var captured *statelessRunner
	defer statelessRunnerTestHook(func(r *statelessRunner) { captured = r })()

	code := c.Run([]string{"-no-color", "-auto-approve"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if captured != nil {
		t.Fatal("a configuration without a live block was run statelessly")
	}

	state := testStateRead(t, filepath.Join(td, "terraform.tfstate"))
	if state == nil {
		t.Fatal("no state file after a stock apply")
	}
	if state.Empty() {
		t.Fatal("the state file a stock apply wrote is empty")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// statelessRunnerTestHook installs fn as the runner hook and returns the
// function that removes it again.
func statelessRunnerTestHook(fn func(*statelessRunner)) func() {
	testStatelessRunner = fn
	return func() { testStatelessRunner = nil }
}

// assertNoStateArtifacts fails if anything under root looks like state.
//
// The list is every file OpenTofu writes when it is keeping state: the
// snapshot, the backup beside it, the lock metadata, the last-ditch
// errored.tfstate, and the per-workspace directory. Walking the tree rather
// than stat-ing the default path is deliberate - a state file in the wrong
// place is still a state file, and the -state flags this mode rejects exist
// precisely to put one somewhere else.
func assertNoStateArtifacts(t *testing.T, root string) {
	t.Helper()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		switch {
		case d.IsDir():
			if name == "terraform.tfstate.d" {
				t.Errorf("a workspace state directory was created: %s", rel)
			}
		case name == "terraform.tfstate", name == "errored.tfstate",
			strings.HasSuffix(name, ".tfstate"), strings.HasSuffix(name, ".tfstate.backup"),
			strings.HasSuffix(name, ".lock.info"):
			t.Errorf("a state artifact was written: %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the working directory: %s", err)
	}
}

// liveBlockCloud is the estate as it looks once it has been applied
// once: both resources exist and carry their markers, so a plan over it is
// empty.
func liveBlockCloud() *statelessTestCloud {
	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-owned", "the estate's VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})
	return cloud
}

func liveBlockMeta(view *views.View, cloud *statelessTestCloud) Meta {
	return Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
		testingOverrides: &testingOverrides{
			Providers: map[addrs.Provider]providers.Factory{
				addrs.NewDefaultProvider("aws"): providers.FactoryFixed(cloud.provider()),
			},
		},
	}
}

func newLiveBlockPlanCommand(t *testing.T, cloud *statelessTestCloud) (*PlanCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()
	view, done := testView(t)
	return &PlanCommand{Meta: liveBlockMeta(view, cloud)}, done
}
