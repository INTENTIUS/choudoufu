// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"
	"gopkg.in/yaml.v3"
)

// examples/ci-pipelines ships generated CI: one chant project, five Ops, and
// the GitHub, Forgejo and GitLab workflows those Ops produce, checked in
// beside the config that produces them (GitHub issue #807). A generated file
// that has drifted from its generator is worse than no generated file,
// because it reads as authoritative and describes a pipeline nobody has.
//
// The example's own `npm test` is the real currency guard: it re-runs
// generate.ts for all three forges into a scratch directory and diffs byte
// for byte, in both directions. It needs node and `npm install`, and this
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
//     every forge's arrangement rests on;
//   - the choudoufu install every job runs is pinned to a version and a
//     checksum rather than floating;
//   - the generator has been run since the inputs last changed, by re-hashing
//     those inputs and comparing against the stamp generate.ts writes;
//   - and, when git history is deep enough to answer, no generator input was
//     committed after the workflows it generates.
//
// What it cannot see, stated so nobody reads a green run here as more than it
// is: with no node available it never regenerates, so it proves correspondence,
// input state and ordering, not equality. A hand-edit to a workflow committed
// in the SAME commit as the source change it pretends to reflect is invisible
// to the ordering check by construction, and invisible to the correspondence
// checks unless it touches a banner, a CHANT_FORGE or the install line - the
// stamp catches an input that moved without the generator running, not a
// generated file edited after it ran. TestCIPipelineWorkflowsRegenerate closes
// that last gap on any machine that has run `npm install` in the example.
//
// The example's third tree, `gitlab/`, is generated too, since chant #2268
// (0.60.0) taught its Op generator `pull_request`/`push` triggers. It used to
// be hand-written (#807, sub-issue (b)), because that generator was cron-only
// through 0.59.0 and refused four of the five Ops by name. GitLab's generator
// returns one combined file rather than one per Op - a GitLab trigger is
// job-scoped, not workflow-scoped, so there is nothing to split into separate
// files - which does not fit `ciPipelineForges` below (built around "one
// tracked file per Op", true of GitHub and Forgejo and not of GitLab). The
// guards for it are at the bottom of this file and prove the same things the
// per-Op guards above prove, read against the one file GitLab gets.

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

// TestCIPipelineWorkflowsNameTheirOwnSource holds, for github and forgejo,
// the invariant every forge's generated workflow rests on.
// TestCIPipelineGitLabBuildsItsOwnForge holds the same invariant for GitLab,
// whose one-file-for-every-job shape does not fit the per-Op loop below.
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

// ---------------------------------------------------------------------------
// Trigger parity: one table, read off the GitHub tree, checked against
// Forgejo's `on:` and GitLab's `rules:`.
// ---------------------------------------------------------------------------
//
// examples/ci-pipelines/tests/pipelines.test.ts builds the same table from
// `specs()` directly, since chant is a TypeScript dependency and specs() is
// not readable from here. This file reads it off GitHub's generated tree
// instead - the reference dialect everywhere else in this file compares
// Forgejo and GitLab against - which is provably the same table:
// TestCIPipelineWorkflowsRegenerate and TestCIPipelineForgejoTriggerParity
// hold, on a machine with node, that GitHub's own tree is what specs()
// produces and that Forgejo's agrees with it.

// ciPipelineTrigger is one Op's trigger, forge-neutral: what kind of event
// fires the job, which branch a `pull_request` or `push` trigger names, and
// which cron a `schedule` trigger names.
type ciPipelineTrigger struct {
	Kind     string // "pull_request", "push", or "schedule"
	Branches []string
	Cron     string
}

// ciTriggerWorkflow reads only the `on:` block of a generated GitHub or
// Forgejo workflow - the shape both dialects share.
type ciTriggerWorkflow struct {
	On struct {
		PullRequest *struct {
			Branches []string `yaml:"branches"`
		} `yaml:"pull_request"`
		Push *struct {
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
		Schedule []struct {
			Cron string `yaml:"cron"`
		} `yaml:"schedule"`
	} `yaml:"on"`
}

// ciPipelineWorkflowTrigger reads one generated GitHub or Forgejo workflow's
// `on:` block and returns it as a ciPipelineTrigger. Fatal on a shape this
// project does not use: every Op here fires on exactly one of pull_request,
// push or a single schedule entry, and a workflow matching none of them is
// not one this reader can answer for.
func ciPipelineWorkflowTrigger(t *testing.T, forge, op string) ciPipelineTrigger {
	t.Helper()

	var doc ciTriggerWorkflow
	if err := yaml.Unmarshal([]byte(ciPipelineWorkflow(t, forge, op)), &doc); err != nil {
		t.Fatalf("parsing %s/%s.yml as YAML: %v", forge, op, err)
	}

	switch {
	case doc.On.PullRequest != nil:
		return ciPipelineTrigger{Kind: "pull_request", Branches: doc.On.PullRequest.Branches}
	case doc.On.Push != nil:
		return ciPipelineTrigger{Kind: "push", Branches: doc.On.Push.Branches}
	case len(doc.On.Schedule) == 1:
		return ciPipelineTrigger{Kind: "schedule", Cron: doc.On.Schedule[0].Cron}
	default:
		t.Fatalf("%s/%s.yml's `on:` is none of pull_request, push or a single schedule entry; "+
			"this reader does not know how to read its trigger", forge, op)
		panic("unreachable")
	}
}

// ciPipelineTriggerTable reads the trigger every Op fires on off the GitHub
// tree - the reference dialect - once, so the Forgejo and GitLab guards below
// check a derived table rather than restating a second (or third) literal
// per Op that could drift from GitHub's own with nobody noticing.
func ciPipelineTriggerTable(t *testing.T) map[string]ciPipelineTrigger {
	t.Helper()

	table := make(map[string]ciPipelineTrigger)
	for _, op := range ciPipelineOps(t) {
		table[op] = ciPipelineWorkflowTrigger(t, "github", op)
	}
	return table
}

// ciPipelineTriggersEqual compares two triggers by value: same kind, same
// branches in the same order (every trigger in this project names exactly
// one), same cron.
func ciPipelineTriggersEqual(a, b ciPipelineTrigger) bool {
	return a.Kind == b.Kind && a.Cron == b.Cron && strings.Join(a.Branches, ",") == strings.Join(b.Branches, ",")
}

// TestCIPipelineForgejoTriggerParity holds that Forgejo's `on:` fires on the
// same trigger GitHub's does, for every Op - read off ciPipelineTriggerTable
// rather than a second hand-copied literal per Op, so a forge drifting from
// the other is caught here instead of only by a human reading both files.
func TestCIPipelineForgejoTriggerParity(t *testing.T) {
	table := ciPipelineTriggerTable(t)

	for _, op := range ciPipelineOps(t) {
		want := table[op]
		got := ciPipelineWorkflowTrigger(t, "forgejo", op)
		if !ciPipelineTriggersEqual(got, want) {
			t.Errorf("forgejo/%s.yml triggers on %+v, and github/%s.yml (the reference dialect) triggers on %+v",
				op, got, op, want)
		}
	}
}

// ciPipelineGitLabExpectedRule renders trig as the `if:` condition
// generate.ts's gitlab Op generator writes for it: a merge_request_event
// targeting trig's one branch for a pull_request trigger, a push to it for a
// push trigger, and - GitLab has no in-file cron (see the generated file's
// own header comment) - a schedule pipeline naming op's own
// CHANT_SCHEDULED_OP for a schedule trigger.
//
// Fatal on a branch count other than one: every trigger in this project
// names exactly one branch, and a second would need this rendering taught
// the syntax for it rather than silently comparing against the wrong thing.
func ciPipelineGitLabExpectedRule(t *testing.T, op string, trig ciPipelineTrigger) string {
	t.Helper()

	switch trig.Kind {
	case "pull_request", "push":
		if len(trig.Branches) != 1 {
			t.Fatalf("%s: the reference trigger names %d branches (%v); this rendering only reasons about one",
				op, len(trig.Branches), trig.Branches)
		}
		if trig.Kind == "pull_request" {
			return fmt.Sprintf(`$CI_PIPELINE_SOURCE == "merge_request_event" && $CI_MERGE_REQUEST_TARGET_BRANCH_NAME == "%s"`,
				trig.Branches[0])
		}
		return fmt.Sprintf(`$CI_PIPELINE_SOURCE == "push" && $CI_COMMIT_BRANCH == "%s"`, trig.Branches[0])
	case "schedule":
		return fmt.Sprintf(`$CI_PIPELINE_SOURCE == "schedule" && $CHANT_SCHEDULED_OP == "%s"`, op)
	default:
		t.Fatalf("%s: trigger kind %q has no known GitLab rendering", op, trig.Kind)
		panic("unreachable")
	}
}

// TestCIPipelineGitLabTriggerParity holds that every job's `rules:` fires on
// the GitLab rendering of the same trigger table TestCIPipelineForgejoTriggerParity
// checks Forgejo against, read off the GitHub tree.
func TestCIPipelineGitLabTriggerParity(t *testing.T) {
	table := ciPipelineTriggerTable(t)

	var doc map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(ciPipelineGitLabBody(t)), &doc); err != nil {
		t.Fatalf("parsing %s as YAML: %v", ciPipelineGitLabFile, err)
	}

	for _, op := range ciPipelineOps(t) {
		node, ok := doc[op]
		if !ok {
			t.Fatalf("%s declares no %q job", ciPipelineGitLabFile, op)
		}

		var job struct {
			Rules []struct {
				If string `yaml:"if"`
			} `yaml:"rules"`
		}
		if err := node.Decode(&job); err != nil {
			t.Fatalf("%s's %q job does not decode: %v", ciPipelineGitLabFile, op, err)
		}
		if len(job.Rules) != 1 {
			t.Fatalf("%s's %q job declares %d rules; this project's generator writes exactly one per job",
				ciPipelineGitLabFile, op, len(job.Rules))
		}

		want := ciPipelineGitLabExpectedRule(t, op, table[op])
		if job.Rules[0].If != want {
			t.Errorf("%s's %q job fires on %q, and github/%s.yml (the reference dialect) fires on %+v, "+
				"which renders on GitLab as %q",
				ciPipelineGitLabFile, op, job.Rules[0].If, op, table[op], want)
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
// it pins is what emits the YAML. generate.ts holds the same list in
// GENERATOR_INPUTS, and TestCIPipelineStampRecordsItsInputs fails when the two
// disagree.
var ciPipelineGeneratorInputs = []string{"generate.ts", "chant.config.ts", "src", "package.json", "package-lock.json"}

// ciPipelineStampFile is what generate.ts writes at the end of every run: the
// state of the inputs it just read, one SHA256 per file.
//
// It is what makes the currency question answerable where node is not
// installed. Without it the only signal here is commit order, and commit order
// has a false answer built into it: an input change that moves no output byte
// leaves nothing to commit, so the ordering check below reports stale forever
// and its own remedy - regenerate and commit the result - produces no commit.
// #807's third forge value is exactly that change; `src/forge.ts` grew a value
// neither generated forge reads.
const ciPipelineStampFile = "generated-from.json"

// ciPipelineStamp is the stamp's shape. Only `inputs` is load-bearing; `note`
// is there for whoever opens the file first.
type ciPipelineStamp struct {
	Note   []string          `json:"note"`
	Inputs map[string]string `json:"inputs"`
}

// ciPipelineInputHashes hashes every generator input the way the stamp keys
// them: relative to the example directory, slash-separated.
func ciPipelineInputHashes(t *testing.T) map[string]string {
	t.Helper()

	hashes := make(map[string]string)
	for _, entry := range ciPipelineGeneratorInputs {
		root := filepath.Join(ciPipelinesDir, entry)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(ciPipelinesDir, path)
			if relErr != nil {
				return relErr
			}
			hashes[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(body))
			return nil
		})
		if err != nil {
			t.Fatalf("hashing the generator input %s: %v", entry, err)
		}
	}

	// A walk that reached nothing would let every comparison below pass over
	// an empty set.
	if len(hashes) < len(ciPipelineGeneratorInputs) {
		t.Fatalf("hashed %d files for %d generator inputs (%v); this walk is not reaching the tree it covers",
			len(hashes), len(ciPipelineGeneratorInputs), ciPipelineGeneratorInputs)
	}
	return hashes
}

// TestCIPipelineStampRecordsItsInputs holds that the generator has been run
// since the inputs last changed, by content rather than by commit order.
//
// This is the check the ordering one below cannot make without node: it re-does
// the hashing generate.ts did and compares. A source change with no
// regeneration fails here even when the two land in the same commit, which is
// the blind spot TestCIPipelineWorkflowsAreNotStale documents; and an input
// change that moves no emitted byte passes here for the right reason, having
// moved the stamp.
//
// What it still cannot see: whether the emitted YAML is what those inputs
// produce. Only running the generator answers that, which is
// TestCIPipelineWorkflowsRegenerate and the example's own `npm test`.
func TestCIPipelineStampRecordsItsInputs(t *testing.T) {
	if tracked := gitLines(t, "ls-files", "--", ciPipelineStampFile); len(tracked) != 1 {
		t.Fatalf("git tracks %v for %s; the stamp is how a machine with no node reads the currency of the "+
			"generated workflows, so it has to be committed with them", tracked, ciPipelineStampFile)
	}

	path := filepath.Join(ciPipelinesDir, ciPipelineStampFile)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var stamp ciPipelineStamp
	if err := json.Unmarshal(body, &stamp); err != nil {
		t.Fatalf("%s is not readable as JSON (%v); it is written by generate.ts, so run `npm run generate` in %s "+
			"rather than repairing it by hand", ciPipelineStampFile, err, ciPipelinesDir)
	}
	if len(stamp.Inputs) == 0 {
		t.Fatalf("%s records no inputs, so it can hold nothing; run `npm run generate` in %s",
			ciPipelineStampFile, ciPipelinesDir)
	}

	hashes := ciPipelineInputHashes(t)

	for input, want := range hashes {
		got, recorded := stamp.Inputs[input]
		if !recorded {
			t.Errorf("%s is a generator input, and %s does not record it.\n"+
				"Run `npm run generate` in %s and commit the result; if it is genuinely not an input, drop it from "+
				"GENERATOR_INPUTS in generate.ts and from ciPipelineGeneratorInputs here, in one change.",
				input, ciPipelineStampFile, ciPipelinesDir)
			continue
		}
		if got != want {
			t.Errorf("%s has changed since the generator last ran: %s records %s, the file hashes to %s.\n"+
				"The checked-in workflows were produced from an older %s. Run `npm run generate` in %s and commit "+
				"the result - including the stamp, which moves even when no emitted byte does.",
				input, ciPipelineStampFile, got[:12], want[:12], input, ciPipelinesDir)
		}
	}

	for input := range stamp.Inputs {
		if _, ok := hashes[input]; !ok {
			t.Errorf("%s records %s, which is no longer a generator input on disk.\n"+
				"Run `npm run generate` in %s and commit the result.", ciPipelineStampFile, input, ciPipelinesDir)
		}
	}
}

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
// The stamp counts as generated output here, and has to: an input change that
// moves no emitted byte leaves nothing else to commit, and without the stamp
// this check would then report stale with no way to satisfy it. With it, the
// remedy in the failure message is always available - regenerating always
// rewrites the stamp - and TestCIPipelineStampRecordsItsInputs is the content
// check standing behind it.
//
// Its blind spot is the same commit: a source change and a hand-written
// workflow landing together are, to this check, indistinguishable from a
// source change and its regeneration. The stamp closes that on the input side
// (it cannot be rewritten without running the generator over those inputs);
// the example's own `npm test` and TestCIPipelineWorkflowsRegenerate are what
// close it on the emitted-YAML side.
func TestCIPipelineWorkflowsAreNotStale(t *testing.T) {
	generated := make([]string, 0, len(ciPipelineForges)+1)
	for _, dir := range ciPipelineForges {
		generated = append(generated, dir)
	}
	generated = append(generated, ciPipelineStampFile)
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
// GitLab: one file, every job in it - generated now (#807, sub-issue (e)).
// ------------------------------------------------------------------------
//
// chant's gitlab Op generator was cron-only through 0.59.0, refusing four of
// the five Ops by name; examples/ci-pipelines/gitlab/.gitlab-ci.yml was
// hand-written for exactly that reason (#807, sub-issue (b)). chant #2268
// (0.60.0) taught it `pull_request` and `push` triggers and a merge-request
// note activity behind `findingMode: "comment"`, so `generate.ts` now emits
// GitLab too, retiring the hand-written file.
//
// It emits one combined file rather than one per Op - a GitLab trigger is
// job-scoped rather than workflow-scoped (`rules:` on the job, not `on:` on
// the file), so there is nothing to split into separate files the way GitHub
// and Forgejo's per-Op workflows are. That shape does not fit
// `ciPipelineForges` (built around "one tracked file per Op", which is what
// `TestCIPipelineWorkflowsAreTracked` and its neighbours above assert), so
// this section proves the same things the per-op guards above prove, read
// against the one file GitLab gets: tracked and generated, one job per Op,
// its own forge and install pin, and not stale relative to its inputs.

// ciPipelineGitLabDir and ciPipelineGitLabFile are where the gitlab tree
// lands and what the generator names its one file - not `.gitlab-ci.yml`,
// because a consuming repository already has one and includes this file from
// it (see the example's README).
const ciPipelineGitLabDir = "gitlab"
const ciPipelineGitLabFile = "scheduled-ops.gitlab-ci.yml"

// ciPipelineGitLabRelPath is the file's path relative to the example
// directory, slash-separated so it can be passed to git as a pathspec.
func ciPipelineGitLabRelPath() string {
	return ciPipelineGitLabDir + "/" + ciPipelineGitLabFile
}

// ciPipelineGitLabBody reads the generated pipeline. A missing file is fatal
// rather than a skip: the deliverable is a file, and an absent one is the
// failure this guard exists to catch.
func ciPipelineGitLabBody(t *testing.T) string {
	t.Helper()

	path := filepath.Join(ciPipelinesDir, ciPipelineGitLabDir, ciPipelineGitLabFile)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the generated GitLab pipeline %s: %v", path, err)
	}
	return string(body)
}

// TestCIPipelineGitLabIsTrackedAndGenerated holds the GitLab counterpart of
// TestCIPipelineWorkflowsAreTracked and TestCIPipelineWorkflowsNameTheirOwnSource:
// the file is committed, carries its own generated-file banner, and runs all
// five Ops the example declares - GitLab gets every job now, not the one
// scheduled sweep it was limited to before #2268.
func TestCIPipelineGitLabIsTrackedAndGenerated(t *testing.T) {
	rel := ciPipelineGitLabRelPath()
	if tracked := gitLines(t, "ls-files", "--", rel); len(tracked) != 1 {
		t.Fatalf("git tracks %v for %s; the generated GitLab pipeline has to be committed, "+
			"or the example's GitLab half is a README paragraph with no file behind it", tracked, rel)
	}

	body := ciPipelineGitLabBody(t)

	wantBanner := "# " + ciPipelineGitLabFile + " - generated by examples/ci-pipelines/generate.ts. DO NOT EDIT."
	if !strings.HasPrefix(body, wantBanner) {
		t.Errorf("%s does not open with its own generated-file banner.\nwant prefix: %s", rel, wantBanner)
	}
	wantRegen := "# Regenerate with: CHANT_FORGE=gitlab npm run generate"
	if !strings.Contains(body, wantRegen) {
		t.Errorf("%s does not carry %q, so it does not say how to regenerate it", rel, wantRegen)
	}

	ops := ciPipelineOps(t)
	for _, op := range ops {
		if !strings.Contains(body, "chant run "+op) {
			t.Errorf("%s does not run %q, one of the five Ops the example declares (%v)", rel, op, ops)
		}
	}
}

// TestCIPipelineGitLabInstallIsPinned is TestCIPipelineInstallIsPinned's
// GitLab half, counting install lines instead of iterating files: GitLab's
// five jobs share one file, so one pinned install per job is what "every job
// installs a pinned choudoufu" comes down to here.
func TestCIPipelineGitLabInstallIsPinned(t *testing.T) {
	body := ciPipelineGitLabBody(t)
	ops := ciPipelineOps(t)

	if got := ciPipelineInstallPin.FindAllString(body, -1); len(got) != len(ops) {
		t.Errorf("%s has %d pinned choudoufu install lines; the example declares %d Ops (%v), one job apiece",
			ciPipelineGitLabFile, len(got), len(ops), ops)
	}
	if !strings.Contains(body, "sha256sum -c -") || !ciPipelineChecksum.MatchString(body) {
		t.Errorf("%s downloads choudoufu without verifying a SHA256 against the release's published checksum",
			ciPipelineGitLabFile)
	}
	if strings.Contains(body, "releases/latest") {
		t.Errorf("%s installs a floating release", ciPipelineGitLabFile)
	}
}

// ciPipelineGitLabForgeVar reads the CHANT_FORGE the GitLab job builds its Ops
// with, or nil when the file sets none.
var ciPipelineGitLabForgeVar = regexp.MustCompile(`(?m)^\s*CHANT_FORGE:\s*(\S+)\s*$`)

// ciPipelineForgeList and ciPipelineForgeLiteral read the forge values
// src/forge.ts accepts, out of the source rather than restated here. The Ops
// read CHANT_FORGE at module load and `readForge` throws on anything not in
// this list, so a pipeline setting a value the list does not carry fails on
// every run before it builds an Op.
var (
	ciPipelineForgeList    = regexp.MustCompile(`(?m)^export const FORGES: readonly Forge\[\] = \[([^\]]*)\];`)
	ciPipelineForgeLiteral = regexp.MustCompile(`"([a-z]+)"`)
)

// ciPipelineAcceptedForges returns the forges src/forge.ts accepts.
func ciPipelineAcceptedForges(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(ciPipelinesDir, "src", "forge.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	list := ciPipelineForgeList.FindSubmatch(source)
	if list == nil {
		t.Fatalf("%s no longer declares `export const FORGES: readonly Forge[] = [...]`, so this guard cannot "+
			"tell which forge values the Ops accept. Restore the declaration or teach this reader the new shape "+
			"- do not leave it matching nothing, which would pass over an empty set.", path)
	}

	var forges []string
	for _, match := range ciPipelineForgeLiteral.FindAllSubmatch(list[1], -1) {
		forges = append(forges, string(match[1]))
	}

	// A parse that found nothing, or that lost the default forge, is a broken
	// reader rather than a source with no forges.
	found := false
	for _, forge := range forges {
		if forge == "github" {
			found = true
		}
	}
	if !found {
		t.Fatalf("read %v as the forges %s accepts, which does not include the default (github); this reader is "+
			"matching the wrong thing", forges, path)
	}
	return forges
}

// TestCIPipelineGitLabBuildsItsOwnForge holds the invariant
// TestCIPipelineWorkflowsNameTheirOwnSource holds for github and forgejo:
// the file builds its Ops with CHANT_FORGE: gitlab, checked against the forge
// list itself rather than against a literal repeated here, because
// `chant run` throws on module load on a value that list does not carry -
// before any Op is built, on every triggered or scheduled run.
func TestCIPipelineGitLabBuildsItsOwnForge(t *testing.T) {
	body := ciPipelineGitLabBody(t)

	match := ciPipelineGitLabForgeVar.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("%s sets no CHANT_FORGE, so `chant run` there builds the Ops for the default forge (github), "+
			"whose live-discover opens a GitHub issue and fails on every run", ciPipelineGitLabFile)
	}

	accepted := ciPipelineAcceptedForges(t)
	known := false
	for _, forge := range accepted {
		if forge == match[1] {
			known = true
		}
	}
	if !known {
		t.Errorf("%s builds its Ops with CHANT_FORGE=%q, which src/forge.ts does not accept (it takes %v).\n"+
			"`chant run` loads src/forge.ts before it builds an Op, and readForge throws there, so every "+
			"run fails before it starts.", ciPipelineGitLabFile, match[1], accepted)
	}
	if match[1] != "gitlab" {
		t.Errorf("%s builds its Ops with CHANT_FORGE=%q rather than %q, so it claims a forge it is not running on",
			ciPipelineGitLabFile, match[1], "gitlab")
	}
}

// TestCIPipelineGitLabWorkflowsAreNotStale is
// TestCIPipelineWorkflowsAreNotStale's GitLab half: no generator input was
// committed after the one file GitLab gets. See that test's doc comment for
// what a commit-order check can and cannot prove.
func TestCIPipelineGitLabWorkflowsAreNotStale(t *testing.T) {
	generated := []string{ciPipelineGitLabRelPath(), ciPipelineStampFile}
	sort.Strings(generated)

	lastSource := lastCommitTouching(t, ciPipelineGeneratorInputs...)
	lastGenerated := lastCommitTouching(t, generated...)

	if lastSource == "" || lastGenerated == "" {
		t.Logf("git history does not reach a commit touching the generator inputs (%q) or %s (%q); the "+
			"commit-order check did not run.", lastSource, ciPipelineGitLabRelPath(), lastGenerated)
		return
	}
	if lastSource == lastGenerated {
		return
	}

	cmd := exec.Command("git", "merge-base", "--is-ancestor", lastGenerated, lastSource)
	cmd.Dir = ciPipelinesDir
	if err := cmd.Run(); err == nil {
		t.Errorf("%s is stale: %s last changed it, and %s changed a generator input (%v) after that.\n"+
			"Run `npm run generate` in %s and commit the result.",
			ciPipelineGitLabRelPath(), lastGenerated[:12], lastSource[:12], ciPipelineGeneratorInputs, ciPipelinesDir)
	}
}

// TestCIPipelineGitLabRegenerates is TestCIPipelineWorkflowsRegenerate's
// GitLab half: on a machine that can run the generator, regenerate into a
// scratch directory and compare byte for byte. Skips under the same
// conditions that test does.
func TestCIPipelineGitLabRegenerates(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("no node on PATH, so the generator cannot run here; the tracked, banner, forge and "+
			"install-pin checks in this file still ran (%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ciPipelinesDir, "node_modules", "@intentius", "chant")); err != nil {
		t.Skipf("the example's dependencies are not installed (run `npm install` in %s); the checks in this file "+
			"that need no node still ran", ciPipelinesDir)
	}

	scratch := t.TempDir()
	cmd := exec.Command(node, "--import", "tsx", "generate.ts")
	cmd.Dir = ciPipelinesDir
	cmd.Env = append(os.Environ(), "CHANT_FORGE=gitlab", "CHANT_PIPELINE_OUT_DIR="+scratch)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("regenerating the gitlab pipeline: %v\n%s", err, out)
	}

	wantPath := filepath.Join(scratch, ciPipelineGitLabDir, ciPipelineGitLabFile)
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("reading the regenerated %s: %v", ciPipelineGitLabRelPath(), err)
	}
	if got := ciPipelineGitLabBody(t); got != string(want) {
		t.Errorf("%s is not what generate.ts emits today.\n"+
			"Run `npm run generate` in %s and commit the result; never edit a generated workflow by hand.",
			ciPipelineGitLabRelPath(), ciPipelinesDir)
	}
}

// ---------------------------------------------------------------------------
// #1023: the example root's own provider, pinned by a lock rather than left
// floating.
//
// terraform/main.tf constrains hashicorp/aws to "~> 6.59.0" and nothing else,
// so an `init` with no lock present resolves whatever 6.59.x the registry
// serves that day - a customer copying this example inherits an unbisectable
// failure the moment the registry publishes a new patch release, in a
// pipeline holding an apply role. The guards below hold the fix: a
// `.terraform.lock.hcl` is tracked (never ignored), it pins a version that
// satisfies main.tf's own constraint, and it carries hashes for more than one
// platform - the whole point of generating it with multiple `-platform`
// flags rather than letting a single `init` write down only the machine that
// happened to run it.
// ---------------------------------------------------------------------------

// ciPipelineTerraformDir is the root that carries the floating provider.
const ciPipelineTerraformDir = "terraform"

// ciPipelineTerraformLockRelPath is ciPipelineTerraformLockPath, relative to
// ciPipelinesDir - the form git commands below want.
const ciPipelineTerraformLockRelPath = "terraform/.terraform.lock.hcl"

// ciPipelineLockAWSProviderBlock finds the lock file's own
// `provider "registry.../hashicorp/aws" { ... }` block and returns its
// contents. HCL, not a line-oriented format hand-rolled with regexp for
// everything else in this file, but the two things these guards read out of
// it - a `version = "..."` line and a `hashes = [...]` list - are stable
// enough across the lock file's own generated shape that a small
// block-scoped regexp reads them without pulling in an HCL parser for two
// fields.
func ciPipelineLockAWSProviderBlock(t *testing.T, label, body string) string {
	t.Helper()

	re := regexp.MustCompile(`(?s)provider\s+"[^"]*hashicorp/aws"\s*\{(.*?)\n\}`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("%s: no `provider \"...hashicorp/aws\" { ... }` block found", label)
	}
	return m[1]
}

// ciPipelineMainTFAWSConstraint pulls the `version = "..."` value out of
// main.tf's `required_providers { aws = { ... } }` entry - a different shape
// from the lock file's own top-level `provider "..." { ... }` block, so it
// gets its own small regexp rather than sharing one with
// ciPipelineLockAWSProviderBlock.
func ciPipelineMainTFAWSConstraint(t *testing.T, label, body string) string {
	t.Helper()

	blockRe := regexp.MustCompile(`(?s)aws\s*=\s*\{(.*?)\n\s*\}`)
	block := blockRe.FindStringSubmatch(body)
	if block == nil {
		t.Fatalf("%s: no `aws = { ... }` entry found in required_providers", label)
	}

	return ciPipelineVersionLine(t, label, block[1])
}

// ciPipelineVersionLine pulls the value out of the first `version = "..."`
// line in block. Both main.tf's `aws = { ... version = "~> 6.59.0" }` and the
// lock file's `provider "...hashicorp/aws" { version = "6.59.0" ... }` shape
// it this way; the lock file also has a `constraints = "..."` line, which
// this regexp does not match because it anchors on the literal word
// `version`.
func ciPipelineVersionLine(t *testing.T, label, block string) string {
	t.Helper()

	re := regexp.MustCompile(`(?m)^\s*version\s*=\s*"([^"]+)"`)
	m := re.FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("%s: no `version = \"...\"` line found in the aws provider block:\n%s", label, block)
	}
	return m[1]
}

// TestCIPipelineTerraformLockIsTracked holds that the example commits a lock
// for its own provider instead of ignoring it. `git ls-files` rather than a
// plain file-exists check: a lock file present on disk but ignored (or never
// added) is not a lock the next clone gets, which is exactly the gap #1023
// found - `.gitignore` named it explicitly.
func TestCIPipelineTerraformLockIsTracked(t *testing.T) {
	tracked := gitLines(t, "ls-files", "--", ciPipelineTerraformLockRelPath)

	if len(tracked) != 1 || tracked[0] != ciPipelineTerraformLockRelPath {
		t.Errorf("git does not track %s (got %v).\n"+
			"Generate it with `tofu providers lock -platform=linux_amd64 -platform=linux_arm64 "+
			"-platform=darwin_arm64 -platform=darwin_amd64` in %s/%s, drop the ignore line in "+
			"%s/.gitignore, and commit the result.",
			ciPipelineTerraformLockRelPath, tracked, ciPipelinesDir, ciPipelineTerraformDir, ciPipelinesDir)
	}
}

// TestCIPipelineTerraformLockSatisfiesConstraint holds that the version the
// lock pins is one main.tf's own constraint would have accepted. A lock that
// pins a version outside the constraint is worse than no lock: it reads as
// the pin while actually recording a version `init` would refuse (or would
// silently accept only because the constraint has since drifted out from
// under it).
func TestCIPipelineTerraformLockSatisfiesConstraint(t *testing.T) {
	mainTFPath := filepath.Join(ciPipelinesDir, ciPipelineTerraformDir, "main.tf")
	mainTF, err := os.ReadFile(mainTFPath)
	if err != nil {
		t.Fatalf("reading %s: %v", mainTFPath, err)
	}
	constraintStr := ciPipelineMainTFAWSConstraint(t, mainTFPath, string(mainTF))

	lockPath := filepath.Join(ciPipelinesDir, ciPipelineTerraformLockRelPath)
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("reading %s: %v (run `tofu init` in %s/%s and commit the lock)",
			lockPath, err, ciPipelinesDir, ciPipelineTerraformDir)
	}
	lockBlock := ciPipelineLockAWSProviderBlock(t, lockPath, string(lock))
	lockedStr := ciPipelineVersionLine(t, lockPath, lockBlock)

	constraint, err := version.NewConstraint(constraintStr)
	if err != nil {
		t.Fatalf("%s: %q does not parse as a version constraint: %v", mainTFPath, constraintStr, err)
	}
	locked, err := version.NewVersion(lockedStr)
	if err != nil {
		t.Fatalf("%s: %q does not parse as a version: %v", lockPath, lockedStr, err)
	}

	if !constraint.Check(locked) {
		t.Errorf("%s pins hashicorp/aws %s, which does not satisfy %s's own constraint %q.\n"+
			"Regenerate the lock (`tofu providers lock -platform=... -platform=...` in %s/%s) so it "+
			"pins a version the root's own required_providers block would accept.",
			lockPath, lockedStr, mainTFPath, constraintStr, ciPipelinesDir, ciPipelineTerraformDir)
	}
}

// ciPipelineLockHashes returns every `"h1:...` / `"zh:...` entry in the
// lock's `hashes = [ ... ]` list.
func ciPipelineLockHashes(t *testing.T, label, block string) []string {
	t.Helper()

	listRe := regexp.MustCompile(`(?s)hashes\s*=\s*\[(.*?)\]`)
	m := listRe.FindStringSubmatch(block)
	if m == nil {
		t.Fatalf("%s: no `hashes = [ ... ]` list found in the aws provider block:\n%s", label, block)
	}

	entryRe := regexp.MustCompile(`"(?:h1|zh):[^"]*"`)
	return entryRe.FindAllString(m[1], -1)
}

// TestCIPipelineTerraformLockCoversMultiplePlatforms holds that the lock was
// generated with more than the one platform a bare `init` would have
// recorded. The lock format does not tag each hash with the platform it
// covers, so this cannot assert the exact four #1023 asks for
// (linux_amd64, linux_arm64, darwin_arm64, darwin_amd64) by name; what it can
// hold is that the hash list is not the single-entry shape a one-platform
// `init` on whichever machine ran it would leave behind, which is the
// failure mode #1023 is about - a lock that only works on its author's own
// laptop.
func TestCIPipelineTerraformLockCoversMultiplePlatforms(t *testing.T) {
	lockPath := filepath.Join(ciPipelinesDir, ciPipelineTerraformLockRelPath)
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("reading %s: %v (run `tofu init` in %s/%s and commit the lock)",
			lockPath, err, ciPipelinesDir, ciPipelineTerraformDir)
	}
	block := ciPipelineLockAWSProviderBlock(t, lockPath, string(lock))
	hashes := ciPipelineLockHashes(t, lockPath, block)

	const wantMinHashes = 4
	if len(hashes) < wantMinHashes {
		t.Errorf("%s records only %d hash(es) for hashicorp/aws (%v); a lock generated for a single "+
			"platform, not the four #1023 asks for.\n"+
			"Regenerate with `tofu providers lock -platform=linux_amd64 -platform=linux_arm64 "+
			"-platform=darwin_arm64 -platform=darwin_amd64` in %s/%s and commit the result.",
			lockPath, len(hashes), hashes, ciPipelinesDir, ciPipelineTerraformDir)
	}
}
