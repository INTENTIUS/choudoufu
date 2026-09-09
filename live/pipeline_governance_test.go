// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// examples/pipeline-governance ships one warden policy per forge for a
// repository running examples/ci-pipelines' generated workflows (GitHub issue
// #807, sub-issue (c)). The policies name job names, branch names and
// credential names that a generator two directories away decides, and none of
// those names is checked by either warden: github-warden and forgejo-warden
// both send a required status-check context to their forge as an opaque
// string, and a forge accepts a context nothing will ever report. A required
// check that no job produces is worse than no check at all - it leaves every
// pull request pending forever and reads, in a settings page, exactly like a
// check that works.
//
// So this file is the join. It reads both sides - the policy YAML and the
// generated workflow YAML - and holds that the names agree:
//
//   - the required status checks are exactly the jobs that run on a pull
//     request to the branch the policy protects, in each forge's own context
//     format;
//   - the branch the policy protects is the branch those workflows target;
//   - the credentials the policy declares are exactly the `vars.` and
//     `secrets.` references the same forge's workflows read;
//   - and the one thing the README says the GitHub policy cannot do today is
//     still true of the generated workflow.
//
// Renaming an Op in examples/ci-pipelines/src therefore fails here, which is
// the whole point: the Op names are the job names, and the job names are the
// policy.
//
// What it does not check, and cannot: that a repository which adopts these
// policies is running these workflows. Nothing in a policy file points at a
// workflow file. examples/ci-pipelines' own currency guards
// (ci_pipelines_test.go and the example's `npm test`) hold that the checked-in
// workflows are what the generator emits; whether the repository you applied
// the policy to has copied them is a question for that repository.

const pipelineGovernanceDir = "../examples/pipeline-governance"

// pipelineGovernancePolicies maps a forge to its policy file, relative to the
// example directory. The forge keys are the forges ciPipelineAcceptedForges
// reads out of src/forge.ts: a policy with no pipeline, or a pipeline with no
// policy, fails in TestPipelineGovernanceCoversEveryForge below.
//
// github's and forgejo's policies share one config shape (govPolicy below,
// `orgs: -> repos:`) and are read by the generic tests that follow. gitlab's
// is a different tool with a different shape (`nodes:`, see #1008) and is
// read by its own parser and its own tests, in the delimited section near
// the bottom of this file.
var pipelineGovernancePolicies = map[string]string{
	"github":  filepath.Join("github", "governance.yml"),
	"forgejo": filepath.Join("forgejo", "governance.yml"),
	"gitlab":  filepath.Join("gitlab", "governance.yml"),
}

// pipelineGateLedgerBranch is the branch chant's own gate resolution lives
// on: `chant approve` writes a {@link GateResolutionRecord} there as a commit
// (chant/packages/core/src/lifecycle/gate-ledger.ts), and both this
// project's READMEs call it the approval of record.
//
// It is not importable from the Go side: chant's own name for it,
// `STATE_BRANCH` (chant/packages/core/src/lifecycle/git.ts), is a private
// const with no `export` keyword, and a Go program could not read a
// TypeScript source constant out of a published package either way. So this
// is the one place choudoufu pins the literal. Both policies and both
// project READMEs were grepped for `chant/lifecycle` when this constant was
// added, and agree with it; TestPipelineGovernanceProtectsTheGateLedgerBranch
// below is what a policy renaming its own rule out from under that agreement
// would fail.
const pipelineGateLedgerBranch = "chant/lifecycle"

// ---------------------------------------------------------------------------
// The policy, as much of it as this file reads
// ---------------------------------------------------------------------------

// govPolicy is the slice of a warden governance config these tests read. Both
// wardens share the `orgs: <org>: repos: <repo>:` spine; the leaves differ,
// and govRule carries both spellings.
type govPolicy struct {
	Orgs map[string]struct {
		Repos map[string]govRepo `yaml:"repos"`
	} `yaml:"orgs"`
}

type govRepo struct {
	BranchProtection []govRule  `yaml:"branchProtection"`
	Environments     []govNamed `yaml:"environments"`
	Secrets          []govNamed `yaml:"secrets"`
	Variables        []govNamed `yaml:"variables"`
}

type govNamed struct {
	Name string `yaml:"name"`
}

// govRule is one branch-protection entry. github-warden keys it by `pattern`
// and forgejo-warden by `ruleName`; the required checks are
// `requiredStatusCheckContexts` and `statusCheckContexts` respectively, a
// required review is `requirePullRequestReviews`/`requiredApprovingReviewCount`
// and `requiredApprovals` respectively, and a disabled force push is
// `allowForcePushes: false` and `enablePush: false` respectively - forgejo's
// `enablePush` disables an ordinary push too, which disables a force push a
// fortiori.
type govRule struct {
	Pattern                      string   `yaml:"pattern"`
	RuleName                     string   `yaml:"ruleName"`
	RequiredStatusCheckContexts  []string `yaml:"requiredStatusCheckContexts"`
	StatusCheckContexts          []string `yaml:"statusCheckContexts"`
	RequirePullRequestReviews    bool     `yaml:"requirePullRequestReviews"`
	RequiredApprovingReviewCount int      `yaml:"requiredApprovingReviewCount"`
	RequiredApprovals            int      `yaml:"requiredApprovals"`
	AllowForcePushes             *bool    `yaml:"allowForcePushes"`
	EnablePush                   *bool    `yaml:"enablePush"`
}

// branch is the branch (or glob) this rule protects, whichever warden spelled it.
func (r govRule) branch() string {
	if r.Pattern != "" {
		return r.Pattern
	}
	return r.RuleName
}

// contexts are the required status checks this rule declares, whichever
// warden spelled them.
func (r govRule) contexts() []string {
	if len(r.RequiredStatusCheckContexts) > 0 {
		return r.RequiredStatusCheckContexts
	}
	return r.StatusCheckContexts
}

// reviewRequired reports whether this rule requires at least one approving
// review, in whichever warden spelled it: github-warden's boolean
// `requirePullRequestReviews`, or forgejo-warden's positive `requiredApprovals` count.
func (r govRule) reviewRequired() bool {
	if r.RequirePullRequestReviews {
		return true
	}
	return r.RequiredApprovals > 0
}

// forcePushDisabled reports whether this rule disables a force push, in
// whichever warden spelled it: github-warden's `allowForcePushes: false`, or
// forgejo-warden's `enablePush: false`, which disables an ordinary push too
// and so disables a force push a fortiori. A key neither warden declares
// reads as force pushes allowed, the same default each warden itself uses.
func (r govRule) forcePushDisabled() bool {
	if r.AllowForcePushes != nil {
		return !*r.AllowForcePushes
	}
	if r.EnablePush != nil {
		return !*r.EnablePush
	}
	return false
}

// govPolicyRepo reads one forge's policy and returns its single managed
// repository.
//
// Both policies declare exactly one org and one repo. That is asserted rather
// than assumed: a second repo added without a second set of checks would make
// every assertion below quietly cover half the file.
func govPolicyRepo(t *testing.T, forge string) govRepo {
	t.Helper()

	path := filepath.Join(pipelineGovernanceDir, pipelineGovernancePolicies[forge])
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the %s policy: %v", forge, err)
	}

	var policy govPolicy
	if err := yaml.Unmarshal(body, &policy); err != nil {
		t.Fatalf("parsing %s as YAML: %v", path, err)
	}

	if len(policy.Orgs) != 1 {
		t.Fatalf("%s declares %d orgs; these tests read one, so a second would go unchecked", path, len(policy.Orgs))
	}
	for _, org := range policy.Orgs {
		if len(org.Repos) != 1 {
			t.Fatalf("%s declares %d repos; these tests read one, so a second would go unchecked", path, len(org.Repos))
		}
		for _, repo := range org.Repos {
			return repo
		}
	}
	panic("unreachable: the loop above returns or fatals")
}

// ---------------------------------------------------------------------------
// The generated workflows, as much of them as this file reads
// ---------------------------------------------------------------------------

// govWorkflow is the slice of a generated workflow these tests read.
//
// `on` survives as a string key: gopkg.in/yaml.v3 follows the YAML 1.2 core
// schema, where `on` is not a boolean (that is a YAML 1.1 rule, and the reason
// other tools spell this key "true").
type govWorkflow struct {
	On struct {
		PullRequest *govTrigger `yaml:"pull_request"`
		Push        *govTrigger `yaml:"push"`
	} `yaml:"on"`
	Jobs map[string]govJob `yaml:"jobs"`
}

type govTrigger struct {
	Branches []string `yaml:"branches"`
}

// govJob reads only whether a job declares an `environment:`. A yaml.Node
// rather than a string because the key takes either a scalar or a mapping,
// and the question here is only whether it is there at all: an absent key
// leaves the node's Kind at zero, and decoding into a *yaml.Node instead
// fails outright on a scalar, which would have read as a decode error rather
// than as the answer.
type govJob struct {
	Environment yaml.Node `yaml:"environment"`
}

// govWorkflowDoc parses one generated workflow.
func govWorkflowDoc(t *testing.T, forge, op string) govWorkflow {
	t.Helper()

	var doc govWorkflow
	if err := yaml.Unmarshal([]byte(ciPipelineWorkflow(t, forge, op)), &doc); err != nil {
		t.Fatalf("parsing %s/%s.yml as YAML: %v", forge, op, err)
	}
	if len(doc.Jobs) == 0 {
		t.Fatalf("%s/%s.yml parsed with no jobs; this file's assertions would all pass over an empty set", forge, op)
	}
	return doc
}

// govPullRequestJobs returns the branch every pull-request-triggered workflow
// in this forge targets, and the sorted names of the jobs those workflows
// declare.
//
// Those job names are the only names that can ever be required status checks:
// a forge reports a check when a job runs, and a job that runs on a push or a
// cron never runs on a pull request. Requiring live-apply would leave every
// pull request pending forever.
func govPullRequestJobs(t *testing.T, forge string) (string, []string) {
	t.Helper()

	branches := map[string]bool{}
	var jobs []string

	for _, op := range ciPipelineOps(t) {
		doc := govWorkflowDoc(t, forge, op)
		if doc.On.PullRequest == nil {
			continue
		}
		for _, branch := range doc.On.PullRequest.Branches {
			branches[branch] = true
		}
		for name := range doc.Jobs {
			jobs = append(jobs, name)
		}
	}

	if len(jobs) == 0 {
		t.Fatalf("%s: no generated workflow triggers on a pull request, so there is nothing a required status check could name", forge)
	}
	if len(branches) != 1 {
		t.Fatalf("%s: the pull-request workflows target %d branches (%v); this file protects one", forge, len(branches), branches)
	}

	sort.Strings(jobs)
	var branch string
	for b := range branches {
		branch = b
	}
	return branch, jobs
}

// govExpectedContext renders one job name as the status-check context that
// forge reports for it.
//
// GitHub names a check run after its job, so the context is the bare job name.
//
// Forgejo does not. It builds the context as "<workflow display name> / <job>
// (<event>)" (services/actions/commit_status.go), where the display name is
// the workflow's own `name:` key - which chant's generator does not emit, so
// the stored context for live-check is literally "/ live-check
// (pull_request)". Forgejo compiles each required context as a glob
// (services/pull/commit_status.go, gobwas/glob), so the policy binds the two
// halves that are stable - the job name and the event - and keeps matching if
// a display name ever appears in front of them.
func govExpectedContext(forge, job string) string {
	if forge == "forgejo" {
		return fmt.Sprintf("*/ %s (pull_request)", job)
	}
	return job
}

// ---------------------------------------------------------------------------
// The assertions
// ---------------------------------------------------------------------------

// TestPipelineGovernanceCoversEveryForge holds that there is one policy per
// forge examples/ci-pipelines builds for. A forge whose pipeline ships with
// no policy is a pipeline nothing protects, and this file would otherwise
// never mention it.
//
// It reads the forge list from src/forge.ts (ciPipelineAcceptedForges, also
// used by ci_pipelines_test.go) rather than iterating ciPipelineForges, a
// two-entry map of only github and forgejo. Iterating that map is the
// version of this test that shipped green while GitLab had a generated
// pipeline and no policy (#1008, #1021): a two-entry map cannot fail on a
// third forge it never looks at. ciPipelineAcceptedForges names every forge
// src/forge.ts accepts, so a fourth forge added there and left unpoliced
// fails here the same way GitLab did.
func TestPipelineGovernanceCoversEveryForge(t *testing.T) {
	accepted := ciPipelineAcceptedForges(t)
	acceptedSet := make(map[string]bool, len(accepted))
	for _, forge := range accepted {
		acceptedSet[forge] = true
	}

	for _, forge := range accepted {
		rel, ok := pipelineGovernancePolicies[forge]
		if !ok {
			t.Errorf("src/forge.ts accepts %q and examples/ci-pipelines builds a pipeline for it, and examples/pipeline-governance ships no %s policy", forge, forge)
			continue
		}
		if _, err := os.Stat(filepath.Join(pipelineGovernanceDir, rel)); err != nil {
			t.Errorf("the %s policy is declared at %s and is not there: %v", forge, rel, err)
		}
	}
	for forge := range pipelineGovernancePolicies {
		if !acceptedSet[forge] {
			t.Errorf("examples/pipeline-governance ships a %s policy for a forge src/forge.ts does not accept", forge)
		}
	}
}

// TestPipelineGovernanceProtectsTheGateLedgerBranch holds that every policy
// carries a rule for pipelineGateLedgerBranch ("chant/lifecycle"), requiring
// a review and refusing a force push, in that warden's own spelling of both.
//
// Both READMEs call this branch the approval of record: `chant approve`
// writes the gate's resolution there as a commit, so whoever can push it can
// let a gated apply through regardless of what a forge-side environment
// reviewer does. Nothing asserted that before this test - a policy that
// dropped the rule, or a branch that got renamed out from under it, still
// read as a green TestPipelineGovernanceCoversEveryForge, because that test
// only holds that a policy file exists, not what it protects.
func TestPipelineGovernanceProtectsTheGateLedgerBranch(t *testing.T) {
	for forge := range pipelineGovernancePolicies {
		// See the same skip elsewhere in this file: gitlab's policy is a
		// different shape govPolicyRepo cannot parse. Its version is
		// TestPipelineGovernanceGitLabProtectsTheGateLedgerBranch, in the
		// delimited GitLab section below (#1008).
		if forge == "gitlab" {
			continue
		}
		repo := govPolicyRepo(t, forge)

		var protected []string
		found := false
		for _, rule := range repo.BranchProtection {
			protected = append(protected, rule.branch())
			if rule.branch() != pipelineGateLedgerBranch {
				continue
			}
			if !rule.reviewRequired() {
				t.Errorf("the %s policy's %s rule does not require a review", forge, pipelineGateLedgerBranch)
				continue
			}
			if !rule.forcePushDisabled() {
				t.Errorf("the %s policy's %s rule does not disable a force push", forge, pipelineGateLedgerBranch)
				continue
			}
			found = true
		}
		if !found {
			t.Errorf("the %s policy has no branch-protection rule for %q, the branch chant's own gate "+
				"resolution lives on and both READMEs call the approval of record.\n"+
				"It protects %v. Either chant's gate-ledger branch moved (chant/packages/core/src/lifecycle/git.ts's "+
				"STATE_BRANCH) or the policy dropped the rule.",
				forge, pipelineGateLedgerBranch, protected)
		}
	}
}

// TestPipelineGovernanceProtectsTheBranchTheWorkflowsTarget holds that the
// rule carrying the required checks protects the branch those pull requests
// are opened against.
//
// A rule on the wrong branch is the failure mode with no symptom: the checks
// are declared, the settings page shows them, and pull requests to the branch
// the pipeline actually watches merge unchecked.
func TestPipelineGovernanceProtectsTheBranchTheWorkflowsTarget(t *testing.T) {
	for forge := range pipelineGovernancePolicies {
		// gitlab has no per-op workflow directory (ciPipelineForges has no
		// "gitlab" entry - its one file is read a different way) and its
		// policy is a different shape (govPolicyRepo assumes github-warden's
		// and forgejo-warden's shared `orgs: -> repos:` spine). Its version
		// of this assertion is TestPipelineGovernanceGitLabProtectsTheBranchTheWorkflowsTarget,
		// in the delimited GitLab section below (#1008).
		if forge == "gitlab" {
			continue
		}
		branch, _ := govPullRequestJobs(t, forge)
		repo := govPolicyRepo(t, forge)

		var protected []string
		found := false
		for _, rule := range repo.BranchProtection {
			protected = append(protected, rule.branch())
			if rule.branch() == branch && len(rule.contexts()) > 0 {
				found = true
			}
		}
		if !found {
			t.Errorf("the %s policy has no branch-protection rule with required status checks for %q, the branch its pull-request workflows target.\n"+
				"It protects %v. Either the workflows' trigger moved (regenerate and re-read examples/ci-pipelines) or the policy names the wrong branch.",
				forge, branch, protected)
		}
	}
}

// TestPipelineGovernanceRequiredChecksAreTheGeneratedPullRequestJobs is the
// join this file exists for: the required status checks in each policy are
// exactly the jobs the same forge's generated workflows run on a pull request,
// spelled the way that forge reports them.
//
// Both directions matter. A check the policy requires and no job reports
// leaves every pull request pending; a job the pipeline runs and the policy
// does not require is a refusal a merge can walk past - which for live-check
// means merging a configuration choudoufu has already said it cannot run
// under live markers.
func TestPipelineGovernanceRequiredChecksAreTheGeneratedPullRequestJobs(t *testing.T) {
	for forge := range pipelineGovernancePolicies {
		// See the same skip in TestPipelineGovernanceProtectsTheBranchTheWorkflowsTarget:
		// gitlab has no per-op workflow directory and a differently-shaped
		// policy. Its join is TestPipelineGovernanceGitLabRequiredJobsAreTheGeneratedMergeRequestJobs,
		// below (#1008).
		if forge == "gitlab" {
			continue
		}
		branch, jobs := govPullRequestJobs(t, forge)

		want := make([]string, 0, len(jobs))
		for _, job := range jobs {
			want = append(want, govExpectedContext(forge, job))
		}
		sort.Strings(want)

		var got []string
		for _, rule := range govPolicyRepo(t, forge).BranchProtection {
			if rule.branch() == branch {
				got = append(got, rule.contexts()...)
			}
		}
		sort.Strings(got)

		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Errorf("the %s policy requires %v on %q, and the generated workflows run %v there.\n"+
				"The Op names in examples/ci-pipelines/src are the job names, and the job names are the policy: "+
				"rename an Op and this policy has to move with it.",
				forge, got, branch, want)
		}
	}
}

// govCredentialRefs finds every repository variable and secret the generated
// workflows of one forge read.
//
// `${{ github.token }}` and the `needs.`/`steps.` references are not matched:
// the two patterns below are anchored on `vars.` and `secrets.`, which are the
// only two namespaces a repository administrator provisions and therefore the
// only two a policy can declare.
var (
	govVarRef    = regexp.MustCompile(`vars\.([A-Za-z_][A-Za-z0-9_]*)`)
	govSecretRef = regexp.MustCompile(`secrets\.([A-Za-z_][A-Za-z0-9_]*)`)
)

func govCredentialRefs(t *testing.T, forge string) (vars, secrets []string) {
	t.Helper()

	varSet, secretSet := map[string]bool{}, map[string]bool{}
	for _, op := range ciPipelineOps(t) {
		body := ciPipelineWorkflow(t, forge, op)
		for _, m := range govVarRef.FindAllStringSubmatch(body, -1) {
			varSet[m[1]] = true
		}
		for _, m := range govSecretRef.FindAllStringSubmatch(body, -1) {
			secretSet[m[1]] = true
		}
	}
	return govSortedKeys(varSet), govSortedKeys(secretSet)
}

func govSortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func govNames(entries []govNamed) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	sort.Strings(out)
	return out
}

// TestPipelineGovernanceDeclaresEveryCredentialTheWorkflowsRead holds that the
// variables and secrets each policy declares are exactly the ones that forge's
// workflows read.
//
// Declaring them is not bookkeeping. Both wardens treat a declared credential
// that does not exist as something to say out loud - github-warden refuses to
// create a variable with no value and names it, and either warden reports a
// missing secret - so the policy is where "the apply role was never set on
// this repository" surfaces at reconcile time rather than in an unattended run
// that then cannot authenticate. The other direction catches the rename: a
// role variable renamed in generate.ts leaves the policy asserting the
// existence of something nothing reads.
func TestPipelineGovernanceDeclaresEveryCredentialTheWorkflowsRead(t *testing.T) {
	for forge := range pipelineGovernancePolicies {
		// See the same skip above: gitlab has no per-op workflow directory,
		// no vars./secrets. namespacing to grep for, and a differently-shaped
		// policy. Its version is TestPipelineGovernanceGitLabDeclaresEveryCredentialTheWorkflowsRead,
		// below (#1008).
		if forge == "gitlab" {
			continue
		}
		wantVars, wantSecrets := govCredentialRefs(t, forge)
		repo := govPolicyRepo(t, forge)

		if got := govNames(repo.Variables); strings.Join(got, " ") != strings.Join(wantVars, " ") {
			t.Errorf("the %s policy declares variables %v, and its generated workflows read %v", forge, got, wantVars)
		}
		if got := govNames(repo.Secrets); strings.Join(got, " ") != strings.Join(wantSecrets, " ") {
			t.Errorf("the %s policy declares secrets %v, and its generated workflows read %v", forge, got, wantSecrets)
		}
	}
}

// TestPipelineGovernanceEnvironmentGates holds that the GitHub policy's
// `production` environment is the same environment `live-apply`'s generated
// job actually declares, closing the gap
// TestPipelineGovernanceEnvironmentGapIsStillReal used to name (chant #2264
// gave `ScheduledOpSpec` an `environment` option, and
// examples/ci-pipelines/generate.ts's `live-apply` spec now sets it - see
// that file and examples/pipeline-governance/README.md's "What the
// environment does" section for how the two gates stack).
//
// Both directions matter, the same way the required-checks join above checks
// both directions: an environment the policy declares and no job names binds
// nothing, and a job naming an environment the policy never provisions gets
// an unprotected one created on first deploy rather than a reviewer.
func TestPipelineGovernanceEnvironmentGates(t *testing.T) {
	const applyOp = "live-apply"

	repo := govPolicyRepo(t, "github")
	if envs := govNames(repo.Environments); len(envs) != 1 || envs[0] != "production" {
		t.Fatalf("the github policy declares environments %v; the README and this test are written about exactly one, named production", envs)
	}

	doc := govWorkflowDoc(t, "github", applyOp)
	job, ok := doc.Jobs[applyOp]
	if !ok {
		t.Fatalf("github/%s.yml declares no %q job; the policy and the README are both written about it", applyOp, applyOp)
	}
	if job.Environment.Kind == 0 {
		t.Fatalf("github/%s.yml declares no `environment:`, so the github policy's production environment "+
			"reviewer gates nothing. examples/ci-pipelines/generate.ts's live-apply spec should carry "+
			"`environment: { name: \"production\" }` (chant #2264).", applyOp)
	}

	var envDoc struct {
		Name string `yaml:"name"`
	}
	if err := job.Environment.Decode(&envDoc); err != nil {
		t.Fatalf("github/%s.yml's `environment:` does not decode as a {name, url?} mapping: %v", applyOp, err)
	}
	if envDoc.Name != "production" {
		t.Errorf("github/%s.yml deploys to environment %q, and the github policy provisions a reviewer on "+
			"%q. A job naming an environment the policy never declares gets an unprotected one created on "+
			"first deploy rather than the reviewer this policy exists to add.", applyOp, envDoc.Name, "production")
	}

	// GitLab's own generator maps the same spec option to its own
	// `environment:` key (chant #2268). Since #1008 there is a GitLab policy
	// too, provisioning the same protected environment the other two forges'
	// policies do (github via `environments:`, gitlab via
	// `protectedEnvironments:`) - checked on both sides, the same as github
	// above.
	var gitlabDoc map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(ciPipelineGitLabBody(t)), &gitlabDoc); err != nil {
		t.Fatalf("parsing %s as YAML: %v", ciPipelineGitLabFile, err)
	}
	applyNode, ok := gitlabDoc[applyOp]
	if !ok {
		t.Fatalf("gitlab/%s declares no %q job", ciPipelineGitLabFile, applyOp)
	}
	var gitlabJob struct {
		Environment struct {
			Name string `yaml:"name"`
		} `yaml:"environment"`
	}
	if err := applyNode.Decode(&gitlabJob); err != nil {
		t.Fatalf("gitlab/%s's %q job does not decode: %v", ciPipelineGitLabFile, applyOp, err)
	}
	if gitlabJob.Environment.Name != "production" {
		t.Errorf("gitlab/%s's %q job deploys to environment %q, not %q",
			ciPipelineGitLabFile, applyOp, gitlabJob.Environment.Name, "production")
	}

	gitlabRepo := govGitLabRepo(t)
	if envs := govGitLabProtectedEnvironmentNames(gitlabRepo); len(envs) != 1 || envs[0] != "production" {
		t.Errorf("the gitlab policy declares protectedEnvironments %v; the README and this test are written about exactly one, named production", envs)
	}
}

// govForgejoReportedContexts returns every status-check context Forgejo could
// report for one job of the generated workflow file named after it.
//
// From services/actions/commit_status.go, which builds the context as
// fmt.Sprintf("%s / %s (%s)", runName, job.Name, event) and stores it trimmed:
//
//   - runName starts as path.Base(run.WorkflowID) - the workflow file name -
//     and is then overwritten by the parsed workflow's own Name, which is its
//     `name:` key. The generated workflows carry no `name:` key, so that Name
//     is the empty string and the stored context begins with the slash;
//   - the file-name spelling survives only when the parse fails, which is the
//     second entry;
//   - and the third is what appears the day chant emits a `name:`.
//
// A required context has to keep matching across all three, because nothing in
// this repository controls which one a given Forgejo version produces.
func govForgejoReportedContexts(file, job string) []string {
	return []string{
		fmt.Sprintf("/ %s (pull_request)", job),
		fmt.Sprintf("%s / %s (pull_request)", file, job),
		fmt.Sprintf("A Display Name / %s (pull_request)", job),
	}
}

// govGlobMatches answers whether a Forgejo required-context glob matches a
// context string.
//
// It implements exactly one glob shape - a leading `*` over a literal tail -
// and refuses anything else rather than approximating gobwas/glob, the library
// Forgejo compiles these patterns with (services/pull/commit_status.go). A
// policy that grows a pattern this cannot reason about fails the test that
// calls it instead of being waved through.
func govGlobMatches(t *testing.T, pattern, context string) bool {
	t.Helper()

	tail, ok := strings.CutPrefix(pattern, "*")
	if !ok || strings.ContainsAny(tail, "*?[]{}\\") {
		t.Fatalf("the forgejo policy declares the required context %q; this test only reasons about a leading `*` over a literal tail, "+
			"which is the one shape the policy uses. Either write the pattern that way or teach govGlobMatches the real gobwas/glob semantics.", pattern)
	}
	return strings.HasSuffix(context, tail)
}

// TestPipelineGovernanceForgejoContextsMatchWhatForgejoWouldReport is the
// reason the Forgejo policy is allowed to name something that is not a job
// name.
//
// The test above holds that the policy's contexts equal a derived string; this
// one holds that the derivation is right, by matching each declared pattern
// against the context strings Forgejo's own code would build for that job. A
// required context that matches nothing Forgejo reports leaves every pull
// request pending forever, and that failure looks identical, in a settings
// page, to a rule that works.
func TestPipelineGovernanceForgejoContextsMatchWhatForgejoWouldReport(t *testing.T) {
	const forge = "forgejo"

	branch, jobs := govPullRequestJobs(t, forge)

	var declared []string
	for _, rule := range govPolicyRepo(t, forge).BranchProtection {
		if rule.branch() == branch {
			declared = append(declared, rule.contexts()...)
		}
	}

	for _, job := range jobs {
		for _, context := range govForgejoReportedContexts(job+".yml", job) {
			matched := false
			for _, pattern := range declared {
				if govGlobMatches(t, pattern, context) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("no required context in the forgejo policy matches %q, which is a context Forgejo would report for the %q job.\n"+
					"The policy declares %v. A required check nothing reports leaves every pull request pending.",
					context, job, declared)
			}
		}
	}
}

// =============================================================================
// GitLab (#1008): a different warden, a different config shape
//
// gitlab-warden's policy is a single top-level `nodes:` map keyed by full
// path (INTENTIUS/gitlab-warden's POLICY.md, src/config/types.ts), not the
// `orgs: -> repos:` spine github-warden and forgejo-warden share, so govPolicy
// and govRule above cannot parse it: unmarshalling a `nodes:` document into
// govPolicy finds no `orgs:` key and govPolicyRepo's "exactly one org, one
// repo" assertion fatals on zero. Everything in this section is a parallel
// reader and a parallel set of joins for that shape, not a reuse of the
// generic ones - the generic tests above skip "gitlab" explicitly and point
// here instead.
//
// gitlab-warden also has no field that names a "required status check": a
// GitLab merge request either blocks on the whole pipeline succeeding
// (projectSettings.onlyAllowMergeIfPipelineSucceeds) or it does not, there is
// no per-job equivalent of requiredStatusCheckContexts/statusCheckContexts.
// So gitlab/governance.yml names the jobs that requirement is standing in
// for, in a comment structured for govGitLabDeclaredRequiredJobs to read, and
// the join here holds that comment against the jobs the generated pipeline
// actually runs on a merge request - the same join in spirit as
// TestPipelineGovernanceRequiredChecksAreTheGeneratedPullRequestJobs, reading
// a comment instead of a YAML list because the schema has no list to read.
// =============================================================================

// gitlabPolicy is the slice of a gitlab-warden governance config these tests
// read: a top-level `nodes:` map, keyed by full path.
type gitlabPolicy struct {
	Nodes map[string]gitlabNode `yaml:"nodes"`
}

type gitlabNode struct {
	Kind                  string                  `yaml:"kind"`
	ProjectSettings       gitlabProjectSettings   `yaml:"projectSettings"`
	ApprovalRules         []gitlabApprovalRule    `yaml:"approvalRules"`
	ProtectedBranches     []gitlabProtectedBranch `yaml:"protectedBranches"`
	ProtectedEnvironments []govNamed              `yaml:"protectedEnvironments"`
	Variables             []gitlabVariable        `yaml:"variables"`
}

type gitlabProjectSettings struct {
	OnlyAllowMergeIfPipelineSucceeds bool `yaml:"onlyAllowMergeIfPipelineSucceeds"`
}

type gitlabApprovalRule struct {
	Name              string `yaml:"name"`
	ApprovalsRequired int    `yaml:"approvalsRequired"`
}

type gitlabProtectedBranch struct {
	Name           string `yaml:"name"`
	AllowForcePush *bool  `yaml:"allowForcePush"`
}

// gitlabVariable is a gitlab-warden CI/CD variable. Its identity field is
// `key`, not `name` - govNamed does not fit here, unlike protectedEnvironments
// above, whose ProtectedEnvironmentConfig genuinely is keyed by `name`.
type gitlabVariable struct {
	Key string `yaml:"key"`
}

// govGitLabRepo reads the gitlab policy and returns its single managed
// project node.
//
// Both other policies declare exactly one org and one repo, asserted rather
// than assumed (govPolicyRepo above); this is that assertion's gitlab
// counterpart, so a second node added without a second set of checks does
// not silently go unchecked.
func govGitLabRepo(t *testing.T) gitlabNode {
	t.Helper()

	path := filepath.Join(pipelineGovernanceDir, pipelineGovernancePolicies["gitlab"])
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the gitlab policy: %v", err)
	}

	var policy gitlabPolicy
	if err := yaml.Unmarshal(body, &policy); err != nil {
		t.Fatalf("parsing %s as YAML: %v", path, err)
	}

	if len(policy.Nodes) != 1 {
		t.Fatalf("%s declares %d nodes; these tests read one, so a second would go unchecked", path, len(policy.Nodes))
	}
	for _, node := range policy.Nodes {
		if node.Kind != "project" {
			t.Fatalf("%s declares a node of kind %q; these tests are written about a project node", path, node.Kind)
		}
		return node
	}
	panic("unreachable: the loop above returns or fatals")
}

// govGitLabProtectedEnvironmentNames returns the names of a gitlab node's
// declared protected environments.
func govGitLabProtectedEnvironmentNames(node gitlabNode) []string {
	return govNames(node.ProtectedEnvironments)
}

// govGitLabDeclaredRequiredJobs reads the "required merge-request jobs" line
// gitlab/governance.yml carries in a comment, because gitlab-warden's schema
// has no field that names a required status check the way
// requiredStatusCheckContexts/statusCheckContexts do (see the section
// header above). This is the policy's half of the join: whichever names
// appear after the colon are the jobs the policy is written to require.
var govGitLabRequiredJobsComment = regexp.MustCompile(`(?m)^\s*#\s*required merge-request jobs:\s*(.+?)\s*$`)

func govGitLabDeclaredRequiredJobs(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(pipelineGovernanceDir, pipelineGovernancePolicies["gitlab"])
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the gitlab policy: %v", err)
	}

	match := govGitLabRequiredJobsComment.FindSubmatch(body)
	if match == nil {
		t.Fatalf("%s carries no \"required merge-request jobs: ...\" comment; gitlab-warden's schema has no field "+
			"naming a required status check, so this is the only place the policy states which jobs "+
			"onlyAllowMergeIfPipelineSucceeds is standing in for", path)
	}

	var jobs []string
	for _, job := range strings.Split(string(match[1]), ",") {
		if job = strings.TrimSpace(job); job != "" {
			jobs = append(jobs, job)
		}
	}
	sort.Strings(jobs)
	return jobs
}

// govGitLabJob is the slice of one job in the generated GitLab pipeline these
// tests read.
type govGitLabJob struct {
	Rules []struct {
		If string `yaml:"if"`
	} `yaml:"rules"`
}

// govGitLabReservedTopLevelKeys are the scheduled-ops.gitlab-ci.yml top-level
// keys that are not job names. GitLab's five Ops share one file (unlike
// github and forgejo, one workflow per Op), so this join has to tell a job
// apart from the document's own pipeline-wide configuration.
var govGitLabReservedTopLevelKeys = map[string]bool{
	"stages":    true,
	"variables": true,
	"workflow":  true,
	"default":   true,
	"include":   true,
}

// govGitLabJobs parses every job out of the generated GitLab pipeline.
func govGitLabJobs(t *testing.T) map[string]govGitLabJob {
	t.Helper()

	var raw map[string]yaml.Node
	if err := yaml.Unmarshal([]byte(ciPipelineGitLabBody(t)), &raw); err != nil {
		t.Fatalf("parsing %s as YAML: %v", ciPipelineGitLabFile, err)
	}

	jobs := make(map[string]govGitLabJob, len(raw))
	for name, node := range raw {
		if govGitLabReservedTopLevelKeys[name] {
			continue
		}
		var job govGitLabJob
		if err := node.Decode(&job); err != nil {
			t.Fatalf("%s's %q entry does not decode as a job: %v", ciPipelineGitLabFile, name, err)
		}
		jobs[name] = job
	}
	if len(jobs) == 0 {
		t.Fatalf("%s parsed with no jobs; this file's assertions would all pass over an empty set", ciPipelineGitLabFile)
	}
	return jobs
}

// govGitLabMergeRequestEvent and govGitLabMergeRequestBranch pick a job's
// merge-request rule apart: whether it fires on one at all, and which branch
// it targets.
var (
	govGitLabMergeRequestEvent  = regexp.MustCompile(`merge_request_event`)
	govGitLabMergeRequestBranch = regexp.MustCompile(`CI_MERGE_REQUEST_TARGET_BRANCH_NAME\s*==\s*"([^"]+)"`)
)

// govGitLabMergeRequestJobs returns the branch every merge-request-triggered
// job in the generated pipeline targets, and the sorted names of those jobs -
// the gitlab counterpart of govPullRequestJobs above. Those are the only job
// names "the pipeline must succeed" can ever mean on a merge request: a job
// gated on push or schedule never runs on one.
func govGitLabMergeRequestJobs(t *testing.T) (string, []string) {
	t.Helper()

	branches := map[string]bool{}
	var jobs []string

	for name, job := range govGitLabJobs(t) {
		for _, rule := range job.Rules {
			if !govGitLabMergeRequestEvent.MatchString(rule.If) {
				continue
			}
			jobs = append(jobs, name)
			if m := govGitLabMergeRequestBranch.FindStringSubmatch(rule.If); m != nil {
				branches[m[1]] = true
			}
		}
	}

	if len(jobs) == 0 {
		t.Fatalf("%s: no job triggers on a merge_request_event, so there is nothing onlyAllowMergeIfPipelineSucceeds could require", ciPipelineGitLabFile)
	}
	if len(branches) != 1 {
		t.Fatalf("%s: the merge-request jobs target %d branches (%v); this policy protects one", ciPipelineGitLabFile, len(branches), branches)
	}

	sort.Strings(jobs)
	var branch string
	for b := range branches {
		branch = b
	}
	return branch, jobs
}

// govGitLabRoleArnRef and govGitLabPlainVarRef find the credentials the
// generated GitLab pipeline reads.
//
// GitLab CI/CD variables carry no namespace prefix the way `vars.`/`secrets.`
// do on the other two forges - every variable, GitLab's own predefined ones
// (CI_PIPELINE_ID, CI_COMMIT_BRANCH, ...) and chant's own internal ones
// (CHANT_ID_TOKEN, CHANT_GATE_SUMMARY, CHANT_SCHEDULED_OP, CHANT_FORGE) alike,
// is a bare `$NAME`. Grabbing every `$NAME` in the file would require an
// exclusion list of everything that is not a repository-provisioned
// credential, and a new GitLab predefined variable would silently join it.
// Instead these two patterns are anchored on the naming convention the
// credentials this pipeline reads actually use - a role ARN import shells to
// `export AWS_ROLE_ARN="$CHOUDOUFU_<OP>_ROLE_ARN"`, and AWS_REGION/
// GITLAB_TOKEN are named directly (GITLAB_TOKEN only in the file's own header
// comment documenting what live-plan needs, since chant's GitLab REST client
// reads it from the job's process environment rather than interpolating it
// into the YAML) - so a rename to a name outside this convention fails loudly
// here rather than silently dropping out of both sides of the join.
var (
	govGitLabRoleArnRef  = regexp.MustCompile(`\$(CHOUDOUFU_[A-Z]+_ROLE_ARN)\b`)
	govGitLabPlainVarRef = regexp.MustCompile(`\b(AWS_REGION|GITLAB_TOKEN)\b`)
)

func govGitLabCredentialRefs(t *testing.T) []string {
	t.Helper()

	body := ciPipelineGitLabBody(t)
	set := map[string]bool{}
	for _, m := range govGitLabRoleArnRef.FindAllStringSubmatch(body, -1) {
		set[m[1]] = true
	}
	for _, m := range govGitLabPlainVarRef.FindAllStringSubmatch(body, -1) {
		set[m[1]] = true
	}
	return govSortedKeys(set)
}

func govGitLabVariableNames(vars []gitlabVariable) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Key)
	}
	sort.Strings(out)
	return out
}

// TestPipelineGovernanceGitLabRequiredJobsAreTheGeneratedMergeRequestJobs is
// the gitlab join TestPipelineGovernanceRequiredChecksAreTheGeneratedPullRequestJobs
// is for github and forgejo: the jobs the policy is written to require equal
// the jobs the generated pipeline actually runs on a merge request, and the
// project setting that stands in for "required" on GitLab is on.
func TestPipelineGovernanceGitLabRequiredJobsAreTheGeneratedMergeRequestJobs(t *testing.T) {
	repo := govGitLabRepo(t)
	if !repo.ProjectSettings.OnlyAllowMergeIfPipelineSucceeds {
		t.Errorf("the gitlab policy does not set projectSettings.onlyAllowMergeIfPipelineSucceeds: true, " +
			"so nothing on GitLab actually requires the jobs it names in its \"required merge-request jobs\" comment")
	}

	_, gotJobs := govGitLabMergeRequestJobs(t)
	wantJobs := govGitLabDeclaredRequiredJobs(t)

	if strings.Join(gotJobs, "\n") != strings.Join(wantJobs, "\n") {
		t.Errorf("the gitlab policy names required merge-request jobs %v, and the generated pipeline runs %v on a merge request.\n"+
			"The Op names in examples/ci-pipelines/src are the job names: rename an Op and this policy's comment has to move with it.",
			wantJobs, gotJobs)
	}
}

// TestPipelineGovernanceGitLabProtectsTheBranchTheWorkflowsTarget is
// TestPipelineGovernanceProtectsTheBranchTheWorkflowsTarget's gitlab
// counterpart: a protectedBranches rule, with no force push, exists for the
// branch the merge-request jobs target.
func TestPipelineGovernanceGitLabProtectsTheBranchTheWorkflowsTarget(t *testing.T) {
	branch, _ := govGitLabMergeRequestJobs(t)
	repo := govGitLabRepo(t)

	var protected []string
	found := false
	for _, rule := range repo.ProtectedBranches {
		protected = append(protected, rule.Name)
		if rule.Name == branch && rule.AllowForcePush != nil && !*rule.AllowForcePush {
			found = true
		}
	}
	if !found {
		t.Errorf("the gitlab policy has no protectedBranches rule with allowForcePush: false for %q, the branch its merge-request jobs target.\n"+
			"It protects %v. Either the pipeline's trigger moved (regenerate and re-read examples/ci-pipelines) or the policy names the wrong branch.",
			branch, protected)
	}
}

// TestPipelineGovernanceGitLabDeclaresEveryCredentialTheWorkflowsRead is
// TestPipelineGovernanceDeclaresEveryCredentialTheWorkflowsRead's gitlab
// counterpart, reading govGitLabCredentialRefs instead of vars./secrets.
// references, since gitlab-warden has one `variables:` collection rather
// than variables and secrets split by namespace.
func TestPipelineGovernanceGitLabDeclaresEveryCredentialTheWorkflowsRead(t *testing.T) {
	want := govGitLabCredentialRefs(t)
	got := govGitLabVariableNames(govGitLabRepo(t).Variables)

	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the gitlab policy declares variables %v, and its generated pipeline reads %v", got, want)
	}
}

// TestPipelineGovernanceGitLabProtectsTheGateLedgerBranch is
// TestPipelineGovernanceProtectsTheGateLedgerBranch's gitlab counterpart:
// gitlabProtectedBranch has no review-required field of its own (see
// ../README.md, "Where GitLab differs, and why" - a review requirement on
// GitLab is the project-wide approvalRules block, not a branch attribute), so
// this checks force-push-disabled on the protectedBranches rule and a
// positive approvalsRequired on approvalRules separately, rather than
// through govRule.reviewRequired()/forcePushDisabled() like the generic test.
func TestPipelineGovernanceGitLabProtectsTheGateLedgerBranch(t *testing.T) {
	repo := govGitLabRepo(t)

	var protected []string
	found := false
	for _, rule := range repo.ProtectedBranches {
		protected = append(protected, rule.Name)
		if rule.Name != pipelineGateLedgerBranch {
			continue
		}
		if rule.AllowForcePush == nil || *rule.AllowForcePush {
			t.Errorf("the gitlab policy's %s rule does not disable a force push", pipelineGateLedgerBranch)
			continue
		}
		found = true
	}
	if !found {
		t.Errorf("the gitlab policy has no protectedBranches rule for %q, the branch chant's own gate "+
			"resolution lives on and both READMEs call the approval of record.\n"+
			"It protects %v. Either chant's gate-ledger branch moved (chant/packages/core/src/lifecycle/git.ts's "+
			"STATE_BRANCH) or the policy dropped the rule.",
			pipelineGateLedgerBranch, protected)
	}

	reviewRequired := false
	for _, rule := range repo.ApprovalRules {
		if rule.ApprovalsRequired > 0 {
			reviewRequired = true
		}
	}
	if !reviewRequired {
		t.Errorf("the gitlab policy declares no approvalRules entry with approvalsRequired > 0, so nothing "+
			"requires a review before a change reaches %q the way the other two policies' branch rules do",
			pipelineGateLedgerBranch)
	}
}
