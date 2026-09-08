// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// examples/ci-pipelines ships generated CI: one chant project, five Ops, and
// the GitHub and Forgejo workflows those Ops produce, checked in beside the
// config that produces them (GitHub issue #807). A generated file that has
// drifted from its generator is worse than no generated file, because it
// reads as authoritative and describes a pipeline nobody has.
//
// The example's own `npm test` is the real currency guard: it re-runs
// generate.ts for both forges into a scratch directory and diffs byte for
// byte, in both directions. It needs node and `npm install`, and this
// repository's Go CI has neither, so it does not run there.
//
// This file is the backstop that does run there. It proves, with nothing but
// git and the files themselves:
//
//   - every generated workflow is tracked (a generated file that is only on
//     one machine is not a deliverable);
//   - the set of workflows is exactly the set of Ops, per forge, so an Op
//     added or deleted without regenerating fails here;
//   - each file says which forge and which Op source it came from, and its
//     own CHANT_FORGE agrees with the directory it is in - the one invariant
//     the whole two-forge arrangement rests on;
//   - the choudoufu install every job runs is pinned to a version and a
//     checksum rather than floating;
//   - and, when git history is deep enough to answer, no generator input was
//     committed after the workflows it generates.
//
// What it cannot see, stated so nobody reads a green run here as more than it
// is: with no node available it never regenerates, so it proves correspondence
// and ordering, not equality. A hand-edit to a workflow committed in the SAME
// commit as the source change it pretends to reflect is invisible to the
// ordering check by construction, and invisible to the correspondence checks
// unless it touches a banner, a CHANT_FORGE or the install line.
// TestCIPipelineWorkflowsRegenerate closes that gap on any machine that has
// run `npm install` in the example.
//
// The example's third tree, `gitlab/`, is not generated at all: chant's gitlab
// Op generator is cron-only and refuses four of the five Ops by name, so the
// one job GitLab gets is hand-written (#807, sub-issue (b)). The guards for it
// are at the bottom of this file and say what they hold instead of currency.

const ciPipelinesDir = "../examples/ci-pipelines"

// ciPipelineForges maps a forge to where its generated workflows live,
// relative to the example directory. Forgejo reads `.forgejo/workflows`, the
// same way GitHub reads `.github/workflows`; the extra directory level is the
// repository root a consumer copies each tree into.
var ciPipelineForges = map[string]string{
	"github":  filepath.Join("github", ".github", "workflows"),
	"forgejo": filepath.Join("forgejo", ".forgejo", "workflows"),
}

// ciPipelineOps is every Op the example declares, read from the source of
// truth rather than restated: `src/<name>.op.ts` is what chant's own Op
// discovery scans for, and the Op names are also the job names a governance
// policy names.
func ciPipelineOps(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(ciPipelinesDir, "src"))
	if err != nil {
		t.Fatalf("reading the example's Op directory: %v", err)
	}

	var ops []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".op.ts") {
			continue
		}
		ops = append(ops, strings.TrimSuffix(entry.Name(), ".op.ts"))
	}
	sort.Strings(ops)

	// The example is five Ops. A walk that found nothing would otherwise make
	// every check below pass over an empty set.
	if len(ops) < 5 {
		t.Fatalf("found %d Op files under %s/src; the example declares five, so this walk is not reaching the tree it is supposed to cover",
			len(ops), ciPipelinesDir)
	}
	return ops
}

// gitLines runs a git command in the example directory and returns its
// non-empty output lines. A git failure is fatal: this guard has no answer
// without git, and a guard with no answer must say so rather than pass.
func gitLines(t *testing.T, args ...string) []string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = ciPipelinesDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}

	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestCIPipelineWorkflowsAreTracked holds that each forge's generated
// workflows are in git, and that there is exactly one per Op.
//
// Both halves matter. A workflow present on disk but untracked is a file the
// next clone does not have; a workflow tracked for an Op that no longer
// exists is a job that references a `chant run` target nothing declares, and
// it fails on its first trigger rather than at review.
func TestCIPipelineWorkflowsAreTracked(t *testing.T) {
	ops := ciPipelineOps(t)

	want := make([]string, 0, len(ops))
	for _, op := range ops {
		want = append(want, op+".yml")
	}

	for forge, dir := range ciPipelineForges {
		tracked := gitLines(t, "ls-files", "--", dir)

		got := make([]string, 0, len(tracked))
		for _, path := range tracked {
			got = append(got, filepath.Base(path))
		}
		sort.Strings(got)

		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("%s: git tracks %v under %s, and the example declares Ops %v.\n"+
				"Every Op gets one workflow per forge. Run `npm run generate` in %s and commit the result.",
				forge, got, dir, ops, ciPipelinesDir)
		}
	}
}

// ciPipelineWorkflow reads one generated workflow.
func ciPipelineWorkflow(t *testing.T, forge, op string) string {
	t.Helper()

	path := filepath.Join(ciPipelinesDir, ciPipelineForges[forge], op+".yml")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(body)
}

// TestCIPipelineWorkflowsNameTheirOwnSource holds the invariant the two-forge
// arrangement rests on.
//
// An Op's finding mode is baked into the Op at build time, and the Ops read
// CHANT_FORGE to decide it - on GitHub the plan lands as a pull-request
// comment, on Forgejo it stays in the run's own log, because every posting
// mode chant has shells to `gh` and `gh` cannot be pointed at a Forgejo
// instance. So the generated workflow has to hand the runner the same forge
// the generator read. If those two ever disagree, the permissions the
// workflow grants describe an Op the runner is not building, which is silent
// on both sides.
//
// The banner is checked in the same test because it is the other half of the
// same claim: it names the forge the file was generated for and the Op source
// behind it, and it is the only thing in the file that points a reader who
// found the workflow first at the config that produced it.
func TestCIPipelineWorkflowsNameTheirOwnSource(t *testing.T) {
	for forge := range ciPipelineForges {
		for _, op := range ciPipelineOps(t) {
			body := ciPipelineWorkflow(t, forge, op)

			wantBanner := "# " + op + ".yml - generated by examples/ci-pipelines/generate.ts. DO NOT EDIT."
			if !strings.HasPrefix(body, wantBanner) {
				t.Errorf("%s/%s.yml does not open with its own generated-file banner.\nwant prefix: %s",
					forge, op, wantBanner)
			}

			wantRegen := "# Regenerate with: CHANT_FORGE=" + forge + " npm run generate"
			if !strings.Contains(body, wantRegen) {
				t.Errorf("%s/%s.yml does not carry %q, so it does not say which forge it was generated for",
					forge, op, wantRegen)
			}

			wantSource := "src/" + op + ".op.ts"
			if !strings.Contains(body, wantSource) {
				t.Errorf("%s/%s.yml does not name its own Op source %q", forge, op, wantSource)
			}

			wantEnv := "CHANT_FORGE: " + forge
			if !strings.Contains(body, wantEnv) {
				t.Errorf("%s/%s.yml does not set %q in its own env.\n"+
					"The Op the runner builds has to be the Op this file was generated from: the Ops read CHANT_FORGE "+
					"to choose a finding mode, so a workflow that does not set it, or sets another forge's, grants "+
					"permissions for an Op nobody runs.", forge, op, wantEnv)
			}
		}
	}
}

// ciPipelineInstallPin matches the one line every generated job runs to put
// choudoufu on the runner: a release asset at an explicit vX.Y.Z, and the
// SHA256 that release published.
var ciPipelineInstallPin = regexp.MustCompile(
	`releases/download/v[0-9]+\.[0-9]+\.[0-9]+/choudoufu_v[0-9]+\.[0-9]+\.[0-9]+_linux_amd64\.tar\.gz`)

var ciPipelineChecksum = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

// TestCIPipelineInstallIsPinned holds that no generated job installs a
// floating choudoufu.
//
// A generated workflow is committed once and re-run unattended, two of them
// over a role that can write to the account. "Whatever was released last" is
// not a version, and a tag can be moved and a release asset can be replaced
// without the run noticing - which is why the checksum is checked too, not
// only the version.
func TestCIPipelineInstallIsPinned(t *testing.T) {
	for forge := range ciPipelineForges {
		for _, op := range ciPipelineOps(t) {
			body := ciPipelineWorkflow(t, forge, op)

			if !ciPipelineInstallPin.MatchString(body) {
				t.Errorf("%s/%s.yml has no pinned choudoufu release asset (want a releases/download/vX.Y.Z/choudoufu_vX.Y.Z_linux_amd64.tar.gz URL)",
					forge, op)
			}
			if !strings.Contains(body, "sha256sum -c -") || !ciPipelineChecksum.MatchString(body) {
				t.Errorf("%s/%s.yml downloads choudoufu without verifying a SHA256 against the release's published checksum",
					forge, op)
			}
			if strings.Contains(body, "releases/latest") {
				t.Errorf("%s/%s.yml installs a floating release", forge, op)
			}
		}
	}
}

// ciPipelineGeneratorInputs is everything a regeneration reads, relative to
// the example directory. package-lock.json is here because the chant version
// it pins is what emits the YAML.
var ciPipelineGeneratorInputs = []string{"generate.ts", "chant.config.ts", "src", "package.json", "package-lock.json"}

// lastCommitTouching returns the sha of the most recent commit reachable from
// HEAD that touched any of paths, or "" when git's history does not reach one.
func lastCommitTouching(t *testing.T, paths ...string) string {
	t.Helper()

	args := append([]string{"log", "-1", "--format=%H", "--"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = ciPipelinesDir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// TestCIPipelineWorkflowsAreNotStale holds that no generator input was
// committed after the workflows it generates.
//
// It is deliberately a question about commit order rather than about file
// modification times: in a fresh clone every file is written at checkout
// time, so an mtime comparison there is a coin toss, and a guard that fails
// at random is a guard people delete.
//
// Its blind spot is the same commit: a source change and a hand-written
// workflow landing together are, to this check, indistinguishable from a
// source change and its regeneration. That is what the example's own
// `npm test` and TestCIPipelineWorkflowsRegenerate are for.
func TestCIPipelineWorkflowsAreNotStale(t *testing.T) {
	generated := make([]string, 0, len(ciPipelineForges))
	for _, dir := range ciPipelineForges {
		generated = append(generated, dir)
	}
	sort.Strings(generated)

	lastSource := lastCommitTouching(t, ciPipelineGeneratorInputs...)
	lastGenerated := lastCommitTouching(t, generated...)

	if lastSource == "" || lastGenerated == "" {
		// A shallow clone (`actions/checkout` defaults to depth 1) can see
		// neither commit. Say so rather than pass in silence - the other
		// tests in this file need no history and still ran.
		t.Logf("git history does not reach a commit touching the generator inputs (%q) or the generated workflows (%q); "+
			"the commit-order check did not run. The correspondence, banner, CHANT_FORGE and install-pin checks in this "+
			"file did.", lastSource, lastGenerated)
		return
	}
	if lastSource == lastGenerated {
		return
	}

	// Stale exactly when the generated tree's last commit is a strict
	// ancestor of the sources': the sources moved on and the workflows did
	// not. The other direction (workflows regenerated after a source change)
	// and the incomparable case (two commits on separately merged branches)
	// are both fine.
	cmd := exec.Command("git", "merge-base", "--is-ancestor", lastGenerated, lastSource)
	cmd.Dir = ciPipelinesDir
	if err := cmd.Run(); err == nil {
		t.Errorf("the generated workflows are stale: %s last changed them, and %s changed a generator input (%v) after that.\n"+
			"Run `npm run generate` in %s and commit the result.",
			lastGenerated[:12], lastSource[:12], ciPipelineGeneratorInputs, ciPipelinesDir)
	}
}

// TestCIPipelineWorkflowsRegenerate is the full proof, on a machine that can
// run the generator: regenerate both forges into a scratch directory and
// compare byte for byte.
//
// It skips where node or the example's installed dependencies are absent,
// which is this repository's Go CI. That is why it is not the only test in
// this file: the four above need neither, and none of them can pass over an
// empty set.
func TestCIPipelineWorkflowsRegenerate(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("no node on PATH, so the generator cannot run here; the correspondence, banner, CHANT_FORGE, "+
			"install-pin and commit-order checks in this file still ran (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ciPipelinesDir, "node_modules", "@intentius", "chant")); err != nil {
		t.Skipf("the example's dependencies are not installed (run `npm install` in %s); the checks in this file "+
			"that need no node still ran", ciPipelinesDir)
	}

	scratch := t.TempDir()

	for forge, dir := range ciPipelineForges {
		cmd := exec.Command(node, "--import", "tsx", "generate.ts")
		cmd.Dir = ciPipelinesDir
		cmd.Env = append(os.Environ(), "CHANT_FORGE="+forge, "CHANT_PIPELINE_OUT_DIR="+scratch)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("regenerating the %s pipeline: %v\n%s", forge, err, out)
		}

		for _, op := range ciPipelineOps(t) {
			wantPath := filepath.Join(scratch, dir, op+".yml")
			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("reading the regenerated %s/%s.yml: %v", forge, op, err)
			}
			if got := ciPipelineWorkflow(t, forge, op); got != string(want) {
				t.Errorf("%s/%s.yml is not what generate.ts emits today.\n"+
					"Run `npm run generate` in %s and commit the result; never edit a generated workflow by hand.",
					forge, op, ciPipelinesDir)
			}
		}
	}
}

// ------------------------------------------------------------------------
// GitLab: the one CI file in the example that no generator writes.
// ------------------------------------------------------------------------
//
// examples/ci-pipelines/gitlab/.gitlab-ci.yml is hand-written (#807, sub-issue
// (b)), and the checks above deliberately do not reach it: it is not in
// ciPipelineForges, so it is not one workflow per Op, it carries no generated
// banner, and no regeneration can prove it current.
//
// That is not a licence to leave it unguarded - it is the reason it needs its
// own. A hand-written file loses every property the generator was giving the
// other two trees for free, so the two things worth the most are asserted here
// directly: that the install it runs unattended is still pinned by version and
// checksum, and that the single job it declares is the single Op chant's
// gitlab generator can actually express. The other four Ops fail that
// generator by name - `pull_request` and `push` have no event model there
// (chant #2084) and `live-plan`'s "comment" finding mode has no merge-request
// note activity behind it (chant #2231) - so a job here running one of them
// would be a job whose Op cannot be generated for the forge it is running on.

// ciPipelineGitLabFile is the hand-written pipeline, relative to the example
// directory. Slash-separated on purpose: it is passed to git as a pathspec.
const ciPipelineGitLabFile = "gitlab/.gitlab-ci.yml"

// ciPipelineGitLabOp is the only Op GitLab gets: the scheduled sweep, the one
// Op in the project with a cron trigger and no posting mode.
const ciPipelineGitLabOp = "live-discover"

// ciPipelineGitLabBody reads the hand-written pipeline. A missing file is
// fatal rather than a skip: the deliverable is a file, and an absent one is
// the failure this guard exists to catch.
func ciPipelineGitLabBody(t *testing.T) string {
	t.Helper()

	path := filepath.Join(ciPipelinesDir, filepath.FromSlash(ciPipelineGitLabFile))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the hand-written GitLab pipeline %s: %v", path, err)
	}
	return string(body)
}

// ciPipelineGitLabForgeVar reads the CHANT_FORGE the GitLab job builds its Ops
// with, or "" when the file sets none.
var ciPipelineGitLabForgeVar = regexp.MustCompile(`(?m)^\s*CHANT_FORGE:\s*(\S+)\s*$`)

// TestCIPipelineGitLabIsTrackedAndHandWritten holds that the GitLab pipeline
// exists, is in git, and has not quietly become a generated file.
//
// Both directions matter. Untracked, it is a file the next clone does not
// have, and the example's GitLab half is then a paragraph in a README with
// nothing behind it. Carrying a generated banner, it claims a currency nothing
// checks: `npm run generate` does not write this path, so a banner telling a
// reader to regenerate rather than edit would send them to a command that
// leaves the file exactly as it found it.
func TestCIPipelineGitLabIsTrackedAndHandWritten(t *testing.T) {
	if tracked := gitLines(t, "ls-files", "--", ciPipelineGitLabFile); len(tracked) != 1 {
		t.Fatalf("git tracks %v for %s; the hand-written GitLab pipeline has to be committed, "+
			"or the example's GitLab half is a README paragraph with no file behind it",
			tracked, ciPipelineGitLabFile)
	}

	body := ciPipelineGitLabBody(t)

	for _, marker := range []string{"DO NOT EDIT", "generated by examples/ci-pipelines/generate.ts"} {
		if strings.Contains(body, marker) {
			t.Errorf("%s carries %q, but no generator writes it.\n"+
				"If `generate.ts` has grown a gitlab forge, add it to ciPipelineForges above and delete this guard; "+
				"until then the file is hand-written and must not claim otherwise.", ciPipelineGitLabFile, marker)
		}
	}

	// The same claim from the other side: nothing in the generator's own
	// entry points names gitlab. The day one does, this fails and points
	// whoever wired it at the guard that has to move with it.
	for _, input := range []string{"package.json", "generate.ts"} {
		source, err := os.ReadFile(filepath.Join(ciPipelinesDir, input))
		if err != nil {
			t.Fatalf("reading %s: %v", input, err)
		}
		if strings.Contains(string(source), "gitlab") {
			t.Errorf("%s names gitlab, so the generator may now emit %s.\n"+
				"Move it into ciPipelineForges with the other generated trees and drop the hand-written guards, "+
				"or keep gitlab out of the generator.", input, ciPipelineGitLabFile)
		}
	}
}

// TestCIPipelineGitLabRunsOneScheduledOp holds what the hand-written file is
// allowed to say: one job, gated to a Pipeline Schedule carrying the selector
// variable, running the one Op that has a cron trigger, on an install pinned
// the same way the generated workflows pin theirs.
func TestCIPipelineGitLabRunsOneScheduledOp(t *testing.T) {
	body := ciPipelineGitLabBody(t)

	ops := ciPipelineOps(t)
	declared := false
	for _, op := range ops {
		if op == ciPipelineGitLabOp {
			declared = true
		}
	}
	if !declared {
		t.Fatalf("%s runs %q, which is not one of the Ops the example declares (%v)",
			ciPipelineGitLabFile, ciPipelineGitLabOp, ops)
	}

	// GitLab has no in-file cron: a schedule is a project-level object that
	// runs the project's existing .gitlab-ci.yml. Without both halves of the
	// rule the job runs on every pipeline the project has, including a merge
	// request, where it would sweep the account with the estate's credentials.
	for _, want := range []string{
		`$CI_PIPELINE_SOURCE == "schedule"`,
		`$CHANT_SCHEDULED_OP == "` + ciPipelineGitLabOp + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s does not gate its job on %s.\n"+
				"GitLab has no in-file cron, so the schedule is a project-level object and the rule is the only "+
				"thing keeping this job off every other pipeline the project runs.", ciPipelineGitLabFile, want)
		}
	}

	if !strings.Contains(body, "chant run "+ciPipelineGitLabOp) {
		t.Errorf("%s does not run `chant run %s`", ciPipelineGitLabFile, ciPipelineGitLabOp)
	}
	for _, op := range ops {
		if op == ciPipelineGitLabOp {
			continue
		}
		if strings.Contains(body, "chant run "+op) {
			t.Errorf("%s runs %q, which chant's gitlab Op generator refuses by name: its trigger is a "+
				"pull_request or a push and GitLab has neither event (chant #2084), and live-plan's finding mode "+
				"posts a pull-request comment GitLab has no activity for (chant #2231).\n"+
				"GitLab gets the cron Op and nothing else until that generator grows the triggers.",
				ciPipelineGitLabFile, op)
		}
	}

	// The generated workflows get their pin from generate.ts; this file has
	// only this check.
	if !ciPipelineInstallPin.MatchString(body) {
		t.Errorf("%s has no pinned choudoufu release asset (want a releases/download/vX.Y.Z/choudoufu_vX.Y.Z_linux_amd64.tar.gz URL)",
			ciPipelineGitLabFile)
	}
	if !strings.Contains(body, "sha256sum -c -") || !ciPipelineChecksum.MatchString(body) {
		t.Errorf("%s downloads choudoufu without verifying a SHA256 against the release's published checksum",
			ciPipelineGitLabFile)
	}
	if strings.Contains(body, "releases/latest") {
		t.Errorf("%s installs a floating release", ciPipelineGitLabFile)
	}

	// The Ops read CHANT_FORGE at build time to choose a finding mode, and
	// src/forge.ts accepts two values. Unset it defaults to github, whose
	// live-discover posts a GitHub issue through `gh` and would fail on every
	// scheduled run; github set explicitly is the same thing said out loud.
	// GitLab is a third report-only instance, so the job builds the forgejo
	// Ops - see the file's own header.
	match := ciPipelineGitLabForgeVar.FindStringSubmatch(body)
	if match == nil {
		t.Errorf("%s sets no CHANT_FORGE, so `chant run` there builds the Ops for the default forge (github), "+
			"whose %s posts a GitHub issue through `gh` and fails on every scheduled run",
			ciPipelineGitLabFile, ciPipelineGitLabOp)
	} else if match[1] != "forgejo" {
		t.Errorf("%s builds its Ops with CHANT_FORGE=%q; GitLab has no posting activity at all "+
			"(every chant finding mode is `reconcilePr` shelling to `gh`), so it needs the report-only build, "+
			"which is %q", ciPipelineGitLabFile, match[1], "forgejo")
	}
}
