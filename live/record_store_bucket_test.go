// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// justRecipeWithParams is ci_coverage_test.go's justRecipe for a recipe whose
// header carries parameters (`verify bucket="":`), which that one's exact
// `name:` match does not find. It returns every line from the header to the
// next line that starts in column zero.
func justRecipeWithParams(t *testing.T, justfile, name string) string {
	t.Helper()
	lines := strings.Split(justfile, "\n")
	header := regexp.MustCompile(`^` + regexp.QuoteMeta(name) + `( [^:]*)?:`)
	var body []string
	in := false
	for _, line := range lines {
		switch {
		case header.MatchString(line):
			in = true
		case in && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t"):
			return strings.Join(body, "\n")
		}
		if in {
			body = append(body, line)
		}
	}
	if !in {
		t.Fatalf("no recipe %q in the justfile", name)
	}
	return strings.Join(body, "\n")
}

// TestRecordStoreBucketVerifyAsksTheBinary holds GitHub issue #1341's first
// rule. Two codebases checking the same three settings drift, and the one
// that drifts is the one nobody runs - the first version of this project
// checked them in bash and had already drifted (it demanded SSE-KMS, which
// the tool does not assert). So `verify` calls `choudoufu live-bucket`, and
// reads none of the three settings itself.
func TestRecordStoreBucketVerifyAsksTheBinary(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "examples", "record-store-bucket", "justfile"))
	if err != nil {
		t.Fatal(err)
	}
	verify := justRecipeWithParams(t, string(raw), "verify")
	if !strings.Contains(verify, "live-bucket") {
		t.Error("`just verify` does not call `choudoufu live-bucket`")
	}
	for _, reimplementation := range []string{"get-bucket-versioning", "get-bucket-lifecycle", "get-public-access-block"} {
		if strings.Contains(verify, reimplementation) {
			t.Errorf("`just verify` reads %s itself: that is a second implementation of a check the binary owns", reimplementation)
		}
	}

	// `down` counts VERSIONS under the record roots, not current objects. A
	// deleted record is a delete marker over a recoverable noncurrent
	// version, and a count of current objects reads that bucket as empty.
	down := justRecipeWithParams(t, string(raw), "down")
	if !strings.Contains(down, "list-object-versions") || !strings.Contains(down, `--prefix "tofu-"`) {
		t.Error("`just down` does not count object versions under tofu-* before tearing the bucket down")
	}
	// The COUNT expressions, not the bare words: `down` also names Versions
	// and DeleteMarkers where it cleans up verify's probe objects, so
	// matching the words alone passed with the delete-marker count removed.
	for _, counted := range []string{"length(Versions", "length(DeleteMarkers"} {
		if !strings.Contains(down, counted) {
			t.Errorf("`just down`'s refusal does not count %s...): a bucket holding only deleted records would read as empty", counted)
		}
	}
}

// The tests above read the justfile. That is not enough on its own: #1379's
// audit changed `verify` to `"$bin" live-bucket ... || true` and deleted the
// `exit 1` from `down`'s refusal, and every string those tests grep for was
// still in the file. What follows runs the two recipes, with a stub `aws`
// and a stub `choudoufu` first on PATH, and reads their exit codes and their
// output.

const recordStoreProject = "../examples/record-store-bucket"

// recordStoreBins fails rather than skips. A guard that skips itself when a
// tool is missing is permanently green wherever that tool is missing, which
// is the one place nobody looks.
func recordStoreBins(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"just", "bash", "jq"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatalf("%s is not on PATH; these run the shipped recipes and must not be skipped (a skipping guard is permanently green)", bin)
		}
	}
}

// recordStoreStub writes one executable stub into dir.
func recordStoreStub(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/usr/bin/env bash\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// recordStoreAWSStub answers the three calls `down` makes and refuses
// everything else, so a recipe that reached for some other API would fail
// loudly rather than quietly do nothing. It records every call in STUB_LOG.
//
// The version count comes back as one tab-separated line because `down`
// asks for it with --query 'length(...)' --output text, which is a single
// page of results however many there are. That is a known limitation of the
// recipe (a bucket past one page of versions is counted short) and not this
// test's to fix; the stub reproduces the shape the recipe is written for.
const recordStoreAWSStub = `
echo "$*" >> "$STUB_LOG"
case "$*" in
  *"s3api list-object-versions"*"--prefix tofu-"*)
    printf '%s\t%s\n' "$HELD_VERSIONS" "$HELD_MARKERS"; exit 0 ;;
  *"s3api list-object-versions"*"--prefix _verify/"*)
    echo '{"Objects": []}'; exit 0 ;;
  *"cloudformation delete-stack"*|*"cloudformation wait"*)
    exit 0 ;;
esac
echo "stub aws: the recipe made a call this test did not expect: $*" >&2
exit 1
`

// recordStoreEnv is the environment for a recipe run: the stub directory
// first on PATH, and none of the variables that would change what the
// recipes do inherited from whoever is running the tests.
func recordStoreEnv(t *testing.T, stubDir string, extra ...string) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "PATH", "CHOUDOUFU_BIN", "RECORD_KMS_KEY_ARN", "AWS_REGION", "RECORD_NONCURRENT_DAYS":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"PATH="+stubDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AWS_REGION=us-east-2",
		"STUB_LOG="+filepath.Join(stubDir, "calls.log"),
	)
	return append(env, extra...)
}

// recordStoreRun runs one recipe and returns its exit code and its output.
func recordStoreRun(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("just", args...)
	cmd.Dir = recordStoreProject
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	switch e := err.(type) {
	case nil:
		return 0, string(out)
	case *exec.ExitError:
		return e.ExitCode(), string(out)
	default:
		t.Fatalf("just %s: %v\n%s", strings.Join(args, " "), err, out)
		return 0, ""
	}
}

func recordStoreCalls(t *testing.T, stubDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(stubDir, "calls.log"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(raw)
}

// TestRecordStoreBucketVerifyPassesTheBinarysVerdictOn runs `just verify` and
// reads its exit code. The recipe exists to ask `choudoufu live-bucket` and
// report what it said; with `|| true` after that call it reports a clean
// bucket whatever the binary found, which is worse than not checking.
func TestRecordStoreBucketVerifyPassesTheBinarysVerdictOn(t *testing.T) {
	recordStoreBins(t)
	for _, tc := range []struct {
		name    string
		binExit int
	}{{"the binary is happy", 0}, {"the binary refuses the bucket", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "aws", recordStoreAWSStub)
			recordStoreStub(t, stubDir, "choudoufu", fmt.Sprintf(`
echo "choudoufu $*" >> "$STUB_LOG"
echo "stub choudoufu: live-bucket says %d"
exit %d
`, tc.binExit, tc.binExit))

			env := recordStoreEnv(t, stubDir, "CHOUDOUFU_BIN="+filepath.Join(stubDir, "choudoufu"))
			code, out := recordStoreRun(t, env, "verify", "chdf-guard-bucket")

			calls := recordStoreCalls(t, stubDir)
			if !strings.Contains(calls, "choudoufu live-bucket -bucket=chdf-guard-bucket") {
				t.Errorf("`just verify chdf-guard-bucket` never ran `choudoufu live-bucket -bucket=chdf-guard-bucket`. It ran:\n%s\noutput:\n%s", calls, out)
			}
			if tc.binExit == 0 && code != 0 {
				t.Errorf("the binary exited 0 and `just verify` exited %d:\n%s", code, out)
			}
			if tc.binExit != 0 && code == 0 {
				t.Errorf("the binary exited %d and `just verify` still exited 0, so the recipe reports every bucket correct:\n%s", tc.binExit, out)
			}
		})
	}
}

// TestRecordStoreBucketDownRefusesWhileRecordsAreHeld runs `just down`. For a
// record-backed resource the record is the only copy of the estate's identity
// for it, so the refusal is the whole point of the recipe: it has to exit
// non-zero, say so, and not reach CloudFormation.
func TestRecordStoreBucketDownRefusesWhileRecordsAreHeld(t *testing.T) {
	recordStoreBins(t)
	for _, tc := range []struct {
		name             string
		versions, marker string
		wantRefusal      bool
	}{
		{"record versions are held", "3", "0", true},
		{"only delete markers are held", "0", "2", true},
		{"nothing is held", "0", "0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "aws", recordStoreAWSStub)
			env := recordStoreEnv(t, stubDir, "HELD_VERSIONS="+tc.versions, "HELD_MARKERS="+tc.marker)
			code, out := recordStoreRun(t, env, "down", "chdf-guard-bucket")
			calls := recordStoreCalls(t, stubDir)
			deleted := strings.Contains(calls, "cloudformation delete-stack")

			if tc.wantRefusal {
				if code == 0 {
					t.Errorf("`just down` exited 0 with %s version(s) and %s delete marker(s) held; a refusal that does not exit non-zero stops nothing:\n%s", tc.versions, tc.marker, out)
				}
				if !strings.Contains(out, "REFUSING") {
					t.Errorf("`just down` did not say REFUSING:\n%s", out)
				}
				if deleted {
					t.Errorf("`just down` refused and called cloudformation delete-stack anyway. It ran:\n%s", calls)
				}
			} else {
				if code != 0 {
					t.Errorf("`just down` exited %d over an empty bucket:\n%s", code, out)
				}
				if !deleted {
					t.Errorf("`just down` over an empty bucket never called cloudformation delete-stack, so the tests above prove nothing about the refusal. It ran:\n%s\noutput:\n%s", calls, out)
				}
			}
		})
	}
}
