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
// example directory. The forge keys are ciPipelineForges' keys: a policy with
// no pipeline, or a pipeline with no policy, fails in
// TestPipelineGovernanceCoversEveryForge below.
var pipelineGovernancePolicies = map[string]string{
	"github":  filepath.Join("github", "governance.yml"),
	"forgejo": filepath.Join("forgejo", "governance.yml"),
}

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
// `requiredStatusCheckContexts` and `statusCheckContexts` respectively.
type govRule struct {
	Pattern                     string   `yaml:"pattern"`
	RuleName                    string   `yaml:"ruleName"`
	RequiredStatusCheckContexts []string `yaml:"requiredStatusCheckContexts"`
	StatusCheckContexts         []string `yaml:"statusCheckContexts"`
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
// generated pipeline. A forge whose pipeline ships with no policy is a
// pipeline nothing protects, and this file would otherwise never mention it.
func TestPipelineGovernanceCoversEveryForge(t *testing.T) {
	for forge := range ciPipelineForges {
		rel, ok := pipelineGovernancePolicies[forge]
		if !ok {
			t.Errorf("examples/ci-pipelines generates a %s pipeline and examples/pipeline-governance ships no %s policy", forge, forge)
			continue
		}
		if _, err := os.Stat(filepath.Join(pipelineGovernanceDir, rel)); err != nil {
			t.Errorf("the %s policy is declared at %s and is not there: %v", forge, rel, err)
		}
	}
	for forge := range pipelineGovernancePolicies {
		if _, ok := ciPipelineForges[forge]; !ok {
			t.Errorf("examples/pipeline-governance ships a %s policy for a pipeline examples/ci-pipelines does not generate", forge)
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

// TestPipelineGovernanceEnvironmentGapIsStillReal is a tripwire on a documented
// limitation rather than on a property, and it is written to fail the day the
// limitation goes away.
//
// The GitHub policy declares a `production` environment with a required
// reviewer, and a GitHub environment's protection rules bind a job only when
// that job declares `environment: production`. chant's generateOpsPipeline
// emits no `environment:` key, so today that reviewer gates nothing, and both
// the README and examples/ci-pipelines/src/live-apply.op.ts say so.
//
// If a regenerated live-apply.yml ever carries the key, that paragraph is
// wrong and this test says which paragraph. It is the same reasoning as a
// skipped test that fails when the bug it names is fixed.
func TestPipelineGovernanceEnvironmentGapIsStillReal(t *testing.T) {
	const applyOp = "live-apply"

	repo := govPolicyRepo(t, "github")
	if envs := govNames(repo.Environments); len(envs) != 1 || envs[0] != "production" {
		t.Errorf("the github policy declares environments %v; the README and this test are written about exactly one, named production", envs)
	}

	doc := govWorkflowDoc(t, "github", applyOp)
	job, ok := doc.Jobs[applyOp]
	if !ok {
		t.Fatalf("github/%s.yml declares no %q job; the policy and the README are both written about it", applyOp, applyOp)
	}
	if job.Environment.Kind != 0 {
		t.Errorf("github/%s.yml now declares an `environment:`, so the generated apply job CAN be gated by the production environment.\n"+
			"That is good news and it makes examples/pipeline-governance/README.md's \"What the environment does not do\" section wrong. "+
			"Rewrite that section and delete this test.", applyOp)
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
