// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// examples/ci-pipelines' backend-prepare Op (GitHub issue #1832, #1244 ruling
// 3) stands the record store bucket up through the pipeline. It holds the
// widest role in the example - one that can create the bucket and rewrite its
// policy - so the properties that make that safe are held here, where the Go
// CI runs, as well as in the example's own `npm test`:
//
//   - it fires on a push to `bootstrap` and nowhere else;
//   - its role (or, on Forgejo, its key pair) is read by its own job and no
//     other, so the apply role never grows the power to rewrite the bucket
//     policy, and no pull-request job can reach it;
//   - it applies through the bucket project's own `just up`, which refuses an
//     encryption downgrade and keeps the live retention window, rather than
//     handing a built template to CloudFormation itself;
//   - and scripts/backend-prepare.sh, the three steps the Op runs, does what
//     its doc says against faked tools: refuses the wrong account before it
//     reads or writes anything, prints exactly the template digest the gate
//     binds to, and verifies from the estate's root.
//
// The generic guards in ci_pipelines_test.go (tracked, named, pinned, not
// stale, trigger parity) already cover this Op, because they walk src/*.op.ts.

const backendPrepareOp = "backend-prepare"

// backendPrepareCredential matches every credential name the backend job
// reads, on every forge: the GitHub/GitLab role variable and Forgejo's key
// pair.
var backendPrepareCredential = regexp.MustCompile(`CHOUDOUFU_BACKEND_[A-Z_]+`)

func TestCIPipelineBackendPrepareFiresOnBootstrapOnly(t *testing.T) {
	trig := ciPipelineTriggerTable(t)[backendPrepareOp]
	if trig.Kind != "push" || strings.Join(trig.Branches, ",") != "bootstrap" {
		t.Fatalf("backend-prepare fires on %+v; #1244 ruling 3's design is a push to bootstrap and nothing else. "+
			"A trigger on main would leave a pending approval on every merge", trig)
	}
	for op, other := range ciPipelineTriggerTable(t) {
		if op == backendPrepareOp {
			continue
		}
		for _, b := range other.Branches {
			if b == "bootstrap" {
				t.Errorf("%s also fires on bootstrap; that branch is the backend's alone", op)
			}
		}
	}
}

func TestCIPipelineBackendPrepareHoldsItsOwnCredential(t *testing.T) {
	for forge := range ciPipelineForges {
		for _, op := range ciPipelineOps(t) {
			found := backendPrepareCredential.FindAllString(ciPipelineWorkflow(t, forge, op), -1)
			switch {
			case op == backendPrepareOp && len(found) == 0:
				t.Errorf("%s/%s.yml reads no CHOUDOUFU_BACKEND_* credential; it would run under whatever the runner has", forge, op)
			case op != backendPrepareOp && len(found) > 0:
				t.Errorf("%s/%s.yml reads %v, the backend credential; only backend-prepare may hold it", forge, op, found)
			}
		}
	}

	// GitLab: every job is a top-level key of one file, so read it per job.
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(ciPipelineGitLabBody(t)), &doc); err != nil {
		t.Fatalf("parsing %s: %v", ciPipelineGitLabFile, err)
	}
	for _, op := range ciPipelineOps(t) {
		job, ok := doc[op]
		if !ok {
			t.Errorf("%s has no job %q", ciPipelineGitLabFile, op)
			continue
		}
		body, err := yaml.Marshal(job)
		if err != nil {
			t.Fatalf("re-rendering gitlab job %s: %v", op, err)
		}
		found := backendPrepareCredential.FindAllString(string(body), -1)
		switch {
		case op == backendPrepareOp && len(found) == 0:
			t.Errorf("gitlab job %s reads no CHOUDOUFU_BACKEND_* credential", op)
		case op != backendPrepareOp && len(found) > 0:
			t.Errorf("gitlab job %s reads %v, the backend credential; only backend-prepare may hold it", op, found)
		}
	}
}

// TestCIPipelineBackendPrepareAppliesThroughJustUp holds the reason #1244's
// first draft could not be revived: an Op that handed the built template to
// CloudFormation directly skipped `just up`'s live reads.
func TestCIPipelineBackendPrepareAppliesThroughJustUp(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(ciPipelinesDir, "src", backendPrepareOp+".op.ts"))
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(src), "")
	for _, banned := range []string{"awsApply", "chant-lexicon-aws"} {
		if strings.Contains(code, banned) {
			t.Errorf("backend-prepare.op.ts uses %s; it must apply through examples/record-store-bucket's `just up`, "+
				"which refuses an encryption downgrade and keeps the live retention window", banned)
		}
	}
	for _, want := range []string{`gate("prepare"`, `plan: stepOutput(plan)`, `backend-prepare.sh plan`, `backend-prepare.sh up`, `backend-prepare.sh verify`} {
		if !strings.Contains(code, want) {
			t.Errorf("backend-prepare.op.ts does not contain %q", want)
		}
	}
	if strings.Index(code, `gate("prepare"`) > strings.Index(code, `backend-prepare.sh up`) {
		t.Errorf("backend-prepare.op.ts reaches `up` before its gate")
	}

	script, err := os.ReadFile(filepath.Join(ciPipelinesDir, "scripts", "backend-prepare.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(script), "cloudformation deploy") {
		t.Errorf("scripts/backend-prepare.sh deploys a stack itself; it must call the bucket project's `just up`")
	}
}

// backendPrepareFixture is a scratch world for scripts/backend-prepare.sh:
// fake aws, jq, just, npm and choudoufu on PATH, each appending what it was
// asked to a log, a stand-in bucket project, and a sidecar.
type backendPrepareFixture struct {
	dir, bin, project, sidecar, log string
	template                        string
}

func newBackendPrepareFixture(t *testing.T, sidecar string) *backendPrepareFixture {
	t.Helper()
	dir := t.TempDir()
	f := &backendPrepareFixture{
		dir:      dir,
		bin:      filepath.Join(dir, "bin"),
		project:  filepath.Join(dir, "record-store-bucket"),
		sidecar:  filepath.Join(dir, "estate.chdf.hcl"),
		log:      filepath.Join(dir, "calls.log"),
		template: `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket"}}}`,
	}
	for _, d := range []string{f.bin, f.project} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.project, "justfile"), []byte("# stand-in\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sidecar, []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}

	fakes := map[string]string{
		"aws": `echo "aws $* @ $PWD" >> "$FAKE_LOG"
case "$*" in
  "sts get-caller-identity"*) echo "$FAKE_ACCOUNT" ;;
  *) echo "unexpected aws call: $*" >&2; exit 9 ;;
esac`,
		"jq": `echo "jq $*" >> "$FAKE_LOG"`,
		"just": `echo "just $* @ $PWD" >> "$FAKE_LOG"
case "$1" in
  plan) printf '%s\n' "$FAKE_TEMPLATE" ;;
  up) echo "RECORD_STORE_BUCKET=$2" ;;
  *) exit 9 ;;
esac`,
		"npm": `echo "npm $* @ $PWD" >> "$FAKE_LOG"
mkdir -p node_modules`,
		"choudoufu": `echo "choudoufu $* @ $PWD" >> "$FAKE_LOG"`,
	}
	for name, body := range fakes {
		if err := os.WriteFile(filepath.Join(f.bin, name), []byte("#!/usr/bin/env bash\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *backendPrepareFixture) run(t *testing.T, account, region, stage string) (stdout, stderr string, code int) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(ciPipelinesDir, "scripts", "backend-prepare.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script, stage)
	cmd.Env = []string{
		"PATH=" + f.bin + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin",
		"HOME=" + f.dir,
		"FAKE_LOG=" + f.log,
		"FAKE_ACCOUNT=" + account,
		"FAKE_TEMPLATE=" + f.template,
		"RECORD_BUCKET_PROJECT=" + f.project,
		"BACKEND_PREPARE_ESTATE_FILE=" + f.sidecar,
	}
	if region != "" {
		cmd.Env = append(cmd.Env, "AWS_REGION="+region)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s %s: %v", script, stage, err)
	}
	return out.String(), errb.String(), code
}

func (f *backendPrepareFixture) calls(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

const backendPrepareSidecar = `estate = "e"

record_store "s3" {
  bucket       = "choudoufu-records-111122223333-us-east-1"
  bucket_owner = "111122223333"
}
`

func TestBackendPrepareScriptPlanPrintsOnlyTheTemplateDigest(t *testing.T) {
	f := newBackendPrepareFixture(t, backendPrepareSidecar)
	stdout, stderr, code := f.run(t, "111122223333", "us-east-1", "plan")
	if code != 0 {
		t.Fatalf("plan exited %d\nstderr:\n%s", code, stderr)
	}

	// The gate binds to this line, so it must be the whole of stdout and it
	// must be the digest of what `just plan` built - not of the log around it.
	sum := sha256.Sum256([]byte(f.template))
	if want := "sha256:" + hex.EncodeToString(sum[:]) + "\n"; stdout != want {
		t.Errorf("plan's stdout is %q, want exactly %q", stdout, want)
	}
	if !strings.Contains(stderr, f.template) {
		t.Errorf("plan did not show the reviewer the template on stderr:\n%s", stderr)
	}

	calls := f.calls(t)
	for _, want := range []string{
		"npm ci --no-audit --no-fund @ " + f.project,
		"just plan choudoufu-records-111122223333-us-east-1 @ " + f.project,
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("plan did not run %q; calls:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "just up") {
		t.Errorf("plan ran `just up`; it is the read-only half before the gate. calls:\n%s", calls)
	}
}

func TestBackendPrepareScriptRefusesTheWrongAccountBeforeAnythingElse(t *testing.T) {
	for _, stage := range []string{"plan", "up"} {
		t.Run(stage, func(t *testing.T) {
			f := newBackendPrepareFixture(t, backendPrepareSidecar)
			stdout, stderr, code := f.run(t, "999999999999", "us-east-1", stage)
			if code == 0 {
				t.Fatalf("%s exited 0 with credentials for 999999999999 against bucket_owner 111122223333", stage)
			}
			if stdout != "" {
				t.Errorf("%s printed %q on stdout while refusing; the gate would read it as a digest", stage, stdout)
			}
			if !strings.Contains(stderr, "REFUSING") || !strings.Contains(stderr, "111122223333") || !strings.Contains(stderr, "999999999999") {
				t.Errorf("%s's refusal does not name both accounts:\n%s", stage, stderr)
			}
			calls := f.calls(t)
			if strings.Contains(calls, "just ") || strings.Contains(calls, "npm ") {
				t.Errorf("%s reached the bucket project before refusing; calls:\n%s", stage, calls)
			}
		})
	}
}

func TestBackendPrepareScriptUpRunsTheBucketProjectsUp(t *testing.T) {
	f := newBackendPrepareFixture(t, backendPrepareSidecar)
	stdout, stderr, code := f.run(t, "111122223333", "us-east-1", "up")
	if code != 0 {
		t.Fatalf("up exited %d\nstderr:\n%s", code, stderr)
	}
	if want := "RECORD_STORE_BUCKET=choudoufu-records-111122223333-us-east-1\n"; stdout != want {
		t.Errorf("up's stdout is %q, want %q", stdout, want)
	}
	calls := f.calls(t)
	if !strings.Contains(calls, "just up choudoufu-records-111122223333-us-east-1 @ "+f.project) {
		t.Errorf("up did not run the bucket project's `just up` in its own directory; calls:\n%s", calls)
	}
}

func TestBackendPrepareScriptVerifyAsksChoudoufuFromTheEstateRoot(t *testing.T) {
	f := newBackendPrepareFixture(t, backendPrepareSidecar)
	_, stderr, code := f.run(t, "111122223333", "us-east-1", "verify")
	if code != 0 {
		t.Fatalf("verify exited %d\nstderr:\n%s", code, stderr)
	}
	// No -bucket: from the root, live-bucket checks the bucket as this
	// estate, under the sidecar's own bucket_owner. The directory is matched
	// by its suffix, since the fake reports bash's logical $PWD and a
	// symlinked checkout spells that differently from Go's resolved path.
	calls := f.calls(t)
	want := regexp.MustCompile(`(?m)^choudoufu live-bucket @ .*/examples/ci-pipelines/terraform$`)
	if !want.MatchString(calls) {
		t.Errorf("verify did not run a bare `choudoufu live-bucket` in the example's terraform root; calls:\n%s", calls)
	}
}

func TestBackendPrepareScriptRefusesWithoutARegionOrABucket(t *testing.T) {
	t.Run("no region", func(t *testing.T) {
		f := newBackendPrepareFixture(t, backendPrepareSidecar)
		_, stderr, code := f.run(t, "111122223333", "", "plan")
		if code == 0 || !strings.Contains(stderr, "AWS_REGION") {
			t.Errorf("plan with no AWS_REGION exited %d:\n%s", code, stderr)
		}
		if calls := f.calls(t); calls != "" {
			t.Errorf("plan with no region called out before refusing:\n%s", calls)
		}
	})
	t.Run("no s3 record store", func(t *testing.T) {
		f := newBackendPrepareFixture(t, "estate = \"e\"\n")
		_, stderr, code := f.run(t, "111122223333", "us-east-1", "plan")
		if code == 0 || !strings.Contains(stderr, `record_store \"s3\"`) && !strings.Contains(stderr, `record_store "s3"`) {
			t.Errorf("plan against a sidecar with no s3 store exited %d:\n%s", code, stderr)
		}
	})
	t.Run("unknown stage", func(t *testing.T) {
		f := newBackendPrepareFixture(t, backendPrepareSidecar)
		if _, _, code := f.run(t, "111122223333", "us-east-1", "destroy"); code != 2 {
			t.Errorf("an unknown stage exited %d, want the usage error 2", code)
		}
	})
}
