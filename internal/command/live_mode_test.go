// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
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

// TestStatelessMode_planVerboseSweepGaps: the getting-started tutorial's
// entry point is plain "choudoufu plan"/"apply" against a live block, not
// "choudoufu live-plan" - GitHub issue #78 was filed after walking exactly
// that path on a fresh two-resource estate and hitting a "Not swept for
// removal" list hundreds of types deep. -verbose is a view-level flag
// (internal/command/arguments/view.go), not one of live-plan's own, for
// exactly this reason: it has to reach this alias too, or the fix would miss
// the path the bug was actually found on.
func TestStatelessMode_planVerboseSweepGaps(t *testing.T) {
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

	if !strings.Contains(stdout, "Not swept for removal") {
		t.Errorf("the plain plan does not report the sweep gaps:\n%s", stdout)
	}
	if strings.Contains(stdout, "[TYPE_NOT_LISTABLE]") {
		t.Errorf("the plain plan renders the full type-by-type breakdown without -verbose:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Rerun with -verbose") {
		t.Errorf("the plain plan's summary line does not point at -verbose:\n%s", stdout)
	}

	cloud2 := liveBlockCloud()
	c2, done2 := newLiveBlockPlanCommand(t, cloud2)

	code2 := c2.Run([]string{"-no-color", "-verbose"})
	output2 := done2(t)
	if code2 != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code2, output2.Stdout(), output2.Stderr())
	}
	stdout2 := output2.Stdout()

	if !strings.Contains(stdout2, "[TYPE_NOT_LISTABLE]") {
		t.Errorf("plain \"plan -verbose\" does not render the full type-by-type breakdown:\n%s", stdout2)
	}
	if !strings.Contains(stdout2, "aws_xray_sampling_rule") {
		t.Errorf("plain \"plan -verbose\" does not name the gap types:\n%s", stdout2)
	}
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

	// The delegate above passes -no-color with no other flag, which never
	// exercised the actual hazard: arguments.ParseView compacts recognized
	// flags out of its argument slice IN PLACE, and live-plan's own
	// originalArgs (kept so the plan-command alias can parse the arguments
	// itself) used to be a second slice header over that SAME backing
	// array rather than an independent copy. With any flag after -no-color
	// - -target being the realistic one, since every -target/-exclude run
	// goes through this alias too - the compaction silently overwrote
	// -no-color's slot with the following flag, so the delegate lost
	// -no-color entirely and, for a single -target, saw it twice. Confirmed
	// concretely against a real build before the fix: colored ANSI escapes
	// throughout live-plan's output despite -no-color, for any run that
	// combined a live block with -target.
	t.Run("delegates with -target: -no-color survives, no duplicated flag", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		cloud := liveBlockCloud()
		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, cloud)}

		code := c.Run([]string{"-no-color", "-target=aws_s3_bucket.data"})
		output := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		combined := output.Stdout() + output.Stderr()
		if strings.ContainsRune(combined, '\x1b') {
			t.Errorf("-no-color did not reach the plan-command alias - output carries raw ANSI escapes:\n%s", combined)
		}
		if !strings.Contains(combined, "No changes.") {
			t.Errorf("the alias did not run the pipeline:\n%s", combined)
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

// TestStatelessMode_lintFatal mirrors TestLivePlan_lintFatal for the live
// block's own entry point: a configuration outside the stateless subset
// stops before any provider reads the live system, and the rule that
// rejected it is named. This exercises statelessRunner.PriorState's lint
// call rather than live-plan's own, which is the half of #45 this issue
// (#50) brings the live block up to.
//
// The out-of-subset construct is lifecycle { ignore_changes = all }, which
// discards the update that writes the ownership markers. It was a
// provisioner until choudoufu #364 made every live block
// imply a local record store, which is where a provisioner's tainted bit
// lives - so #353 admits provisioners under the very live block this fixture
// needs, and the fixture stopped being rejected at all. See the fixture's own
// header for what a replacement has to satisfy (no second provider, no
// provider schema).
func TestStatelessMode_lintFatal(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-lint"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	c, done := newLiveBlockPlanCommand(t, cloud)

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stderr := output.Stderr()
	if !strings.Contains(stderr, "Ownership markers would be ignored") {
		t.Errorf("no lint diagnostic for the lifecycle block:\n%s", stderr)
	}
	if !strings.Contains(stderr, "ignore_changes") {
		t.Errorf("the diagnostic does not name the rule that fired:\n%s", stderr)
	}
	if !strings.Contains(stderr, "aws_s3_bucket.data") {
		t.Errorf("the diagnostic does not name the offending resource:\n%s", stderr)
	}
	if len(cloud.imports) > 0 {
		t.Errorf("a rejected configuration still read from the live system: %v", cloud.imports)
	}
	if len(cloud.applied) > 0 {
		t.Errorf("a rejected configuration still wrote to the live system: %v", cloud.applied)
	}
	assertNoStateArtifacts(t, td)
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
	if n := captured.PriorStateCalls(); n != 1 {
		t.Errorf("PriorState ran %d times for one apply, want exactly 1 (GitHub issue #80: the estate sweep and per-resource read cost must be paid once per invocation, not twice)", n)
	}
}

// TestStatelessMode_priorStateRunsOncePlan is the plan half of the same
// pin: "choudoufu plan" reaches PriorState through opPlan rather than
// opApply (internal/backend/local/backend_plan.go), a different call site
// with its own chance to double-invoke, so it earns its own assertion
// rather than riding on the apply test alone.
func TestStatelessMode_priorStateRunsOncePlan(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	cloud := liveBlockCloud()
	c, done := newLiveBlockPlanCommand(t, cloud)

	var captured *statelessRunner
	defer statelessRunnerTestHook(func(r *statelessRunner) { captured = r })()

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if captured == nil {
		t.Fatal("no stateless runner was installed, so this plan was not stateless")
	}
	if n := captured.PriorStateCalls(); n != 1 {
		t.Errorf("PriorState ran %d times for one plan, want exactly 1 (GitHub issue #80)", n)
	}
}

// TestStatelessMode_plainApplyWritesHint is issue #109's wiring proof: a
// live block with a record_store makes a plain "choudoufu apply" persist
// guided discovery's hint into that store after the run, naming the estate
// this run resolved and the resource types it applied - and the record
// directory is the only artifact left behind; the no-persistence proof from
// TestStatelessMode_plainApply still has to hold beside it.
func TestStatelessMode_plainApplyWritesHint(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block-record-store"), td)
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

	// Read the hint back through the same store shape the fixture's
	// record_store "local" block defaults to (a ".tofu-records" directory
	// beside the module) and the exported reader guided discovery itself
	// uses.
	store, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store the apply should have written into: %s", err)
	}
	hint, err := projection.ReadHintStore(context.Background(), store, "stateless-unit")
	if err != nil {
		t.Fatalf("no hint was persisted by the apply: %s", err)
	}
	if hint.Estate != "stateless-unit" {
		t.Errorf("estate is %q, want %q", hint.Estate, "stateless-unit")
	}
	if hint.WrittenAt.IsZero() {
		t.Error("writtenAt is zero")
	}
	for _, want := range []string{"aws_s3_bucket", "aws_vpc"} {
		if !hint.Types[want] {
			t.Errorf("the hint does not record type %s: %v", want, hint.Types)
		}
	}

	// The hint is the one artifact this run is allowed to leave behind;
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
