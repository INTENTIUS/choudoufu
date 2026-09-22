// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
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

	// `down` counts VERSIONS, not current objects. A deleted record is a
	// delete marker over a recoverable noncurrent version, and a count of
	// current objects reads that bucket as empty.
	down := justRecipeWithParams(t, string(raw), "down")
	if !strings.Contains(down, "list-object-versions") {
		t.Error("`just down` does not count object versions before tearing the bucket down")
	}
	// The per-page count shape is the #1382 defect itself: the AWS CLI
	// applies --query per page under --output text, so a bucket past one
	// page printed several lines and both integer tests errored. The
	// behaviour is proved below by running the recipe; this keeps the shape
	// from coming back in some other recipe's copy of it.
	for _, banned := range []string{"length(Versions", "length(DeleteMarkers"} {
		if strings.Contains(down, banned) {
			t.Errorf("`just down` counts with %s...) again. Under --output text the AWS CLI applies --query per page, so that count is one line per page (issue #1382, #1047)", banned)
		}
	}
	if strings.Contains(down, `--prefix "tofu-"`) {
		t.Error("`just down` counts only under tofu-*, so an estate with a key_prefix override is invisible to it (issue #1382)")
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

// recordStoreAWSStub answers the calls the recipes make and refuses
// everything else, so a recipe that reached for some other API would fail
// loudly rather than quietly do nothing. It records every call in STUB_LOG.
//
// Every answer comes from the environment, so one stub serves a table of
// buckets. HELD_JSON is what `down`'s version count reads: since #1382 the
// recipe asks with --output json, which the AWS CLI merges across pages
// before the query runs, so the answer is ONE document however many pages
// the real API would have returned. The old --output text shape (one
// tab-separated count per page) is still reachable, by setting HELD_JSON to
// it, which is how the paging defect is tested.
const recordStoreAWSStub = `
echo "$*" >> "$STUB_LOG"
case "$*" in
  *"s3api list-object-versions"*"--prefix _verify/"*)
    if [ -n "${VERIFY_PROBES:-}" ]; then printf '%s\n' "$VERIFY_PROBES"; else printf '{"Objects": []}\n'; fi
    exit 0 ;;
  *"s3api list-object-versions"*)
    printf '%s\n' "${HELD_JSON:-}"; exit 0 ;;
  *"s3api delete-objects"*)
    exit 0 ;;
  *"s3api get-bucket-lifecycle-configuration"*)
    if [ -n "${LIFECYCLE_ERROR:-}" ]; then printf '%s\n' "$LIFECYCLE_ERROR" >&2; exit 254; fi
    if [ -n "${LIFECYCLE_JSON:-}" ]; then printf '%s\n' "$LIFECYCLE_JSON"; else printf '{"Rules": []}\n'; fi
    exit 0 ;;
  *"s3api get-bucket-encryption"*)
    if [ -n "${ENCRYPTION_ERROR:-}" ]; then printf '%s\n' "$ENCRYPTION_ERROR" >&2; exit 254; fi
    if [ -n "${ENCRYPTION_JSON:-}" ]; then printf '%s\n' "$ENCRYPTION_JSON"; else
      printf '{"ServerSideEncryptionConfiguration": [{"ApplyServerSideEncryptionByDefault": {"SSEAlgorithm": "AES256"}}]}\n'
    fi
    exit 0 ;;
  *"cloudformation delete-stack"*|*"cloudformation wait"*|*"cloudformation deploy"*)
    exit 0 ;;
esac
echo "stub aws: the recipe made a call this test did not expect: $*" >&2
exit 1
`

// recordStoreHeld is the document `down` reads: the key of every version and
// of every delete marker in the bucket. A nil list marshals to null, which is
// what the CLI prints for a key the API did not return, and the recipe has to
// survive that.
func recordStoreHeld(versions, markers []string) string {
	raw, err := json.Marshal(struct {
		Versions      []string
		DeleteMarkers []string
	}{versions, markers})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// recordStoreKeys is n keys under one prefix, for a bucket that is larger
// than one page of the real API.
func recordStoreKeys(n int, prefix string) []string {
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		keys = append(keys, fmt.Sprintf("%s%06d", prefix, i))
	}
	return keys
}

// recordStoreChant fails rather than skips when the chant project cannot be
// built. The template is the only place DeletionPolicy and the lifecycle rule
// can be read as CloudFormation will read them, and a test that skips itself
// wherever node is missing is permanently green in exactly the environments
// nobody watches. `npm ci` is not run from here on purpose: a test that
// reaches a package registry fails for reasons that have nothing to do with
// the code, so this names the command instead.
func recordStoreChant(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("npx"); err != nil {
		t.Fatalf("npx is not on PATH; this test builds the CloudFormation template and must not be skipped (a skipping guard is permanently green)")
	}
	dep := filepath.Join(recordStoreProject, "node_modules", "@intentius", "chant-lexicon-aws")
	if _, err := os.Stat(dep); err != nil {
		t.Fatalf("%s is not installed (%v). Run `npm ci` in %s. This test builds the template rather than reading the TypeScript, and it does not skip.", dep, err, recordStoreProject)
	}
}

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
	return recordStoreRunIn(t, recordStoreProject, env, args...)
}

// recordStoreRunIn is recordStoreRun for another example's justfile
// (record_store_cluster_test.go).
func recordStoreRunIn(t *testing.T, dir string, env []string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("just", args...)
	cmd.Dir = dir
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
//
// Four of these cases are GitHub issue #1382. Before that fix the recipe
// counted with --query 'length(...)' --output text under --prefix "tofu-",
// and every one of them reached `cloudformation delete-stack`:
//
//   - a bucket past one page printed one count per line, both
//     `[ "$x" -gt 0 ]` tests errored with "integer expression expected" and
//     the `if` read false;
//   - an estate with a `key_prefix` override writes outside `tofu-`;
//   - an answer that is not a count at all was treated as zero.
func TestRecordStoreBucketDownRefusesWhileRecordsAreHeld(t *testing.T) {
	recordStoreBins(t)
	for _, tc := range []struct {
		name        string
		held        string
		wantRefusal bool
		wantSaid    []string
	}{
		{
			name:        "record versions are held",
			held:        recordStoreHeld([]string{"tofu-records/prod/aws_s3_bucket.logs"}, nil),
			wantRefusal: true,
			wantSaid:    []string{"1 object version(s)", "tofu-records/"},
		},
		{
			name:        "only delete markers are held",
			held:        recordStoreHeld(nil, []string{"tofu-records/prod/gone", "tofu-records/prod/also-gone"}),
			wantRefusal: true,
			wantSaid:    []string{"2 object version(s)", "tofu-records/"},
		},
		{
			// The defect: 1,234 versions is two pages of the real API.
			name:        "more than one page of versions is held",
			held:        recordStoreHeld(recordStoreKeys(1234, "tofu-records/prod/r"), nil),
			wantRefusal: true,
			wantSaid:    []string{"1234 object version(s)", "tofu-records/  1234"},
		},
		{
			// The defect: these records are not under tofu-.
			name:        "an estate with a key_prefix override holds records outside tofu-",
			held:        recordStoreHeld([]string{"team/prod/records/aws_instance.web"}, []string{"team/prod/records/aws_instance.old"}),
			wantRefusal: true,
			wantSaid:    []string{"2 object version(s)", "team/", `aws s3 rm "s3://chdf-guard-bucket/team/" --recursive`},
		},
		{
			// The defect: the old per-page text shape, fed to the recipe
			// as it would arrive from a two-page bucket.
			name:        "the count is not a count",
			held:        "1000\t0\n234\t0",
			wantRefusal: true,
			wantSaid:    []string{"which is not a count"},
		},
		{
			name:        "only verify's own probes are held",
			held:        recordStoreHeld([]string{"_verify/default", "_verify/aes256"}, nil),
			wantRefusal: false,
		},
		{
			name:        "nothing is held",
			held:        recordStoreHeld(nil, nil),
			wantRefusal: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "aws", recordStoreAWSStub)
			env := recordStoreEnv(t, stubDir, "HELD_JSON="+tc.held)
			code, out := recordStoreRun(t, env, "down", "chdf-guard-bucket")
			calls := recordStoreCalls(t, stubDir)
			deleted := strings.Contains(calls, "cloudformation delete-stack")

			if tc.wantRefusal {
				if code == 0 {
					t.Errorf("`just down` exited 0; a refusal that does not exit non-zero stops nothing:\n%s", out)
				}
				if !strings.Contains(out, "REFUSING") {
					t.Errorf("`just down` did not say REFUSING:\n%s", out)
				}
				if deleted {
					t.Errorf("`just down` refused and called cloudformation delete-stack anyway. It ran:\n%s\noutput:\n%s", calls, out)
				}
				for _, said := range tc.wantSaid {
					if !strings.Contains(out, said) {
						t.Errorf("`just down`'s refusal does not say %q, so it does not say what it found or where:\n%s", said, out)
					}
				}
			} else {
				if code != 0 {
					t.Errorf("`just down` exited %d over an empty bucket:\n%s", code, out)
				}
				if !deleted {
					t.Errorf("`just down` over an empty bucket never called cloudformation delete-stack, so the tests above prove nothing about the refusal. It ran:\n%s\noutput:\n%s", calls, out)
				}
				// With DeletionPolicy Retain the bucket survives the
				// stack, so the old closing line ("gone: <bucket>") was
				// false. The recipe has to say what actually happened and
				// how to remove the bucket on purpose (issue #1382).
				if strings.Contains(out, "gone: ") {
					t.Errorf("`just down` still says the bucket is gone. The bucket carries DeletionPolicy Retain, so delete-stack leaves it behind:\n%s", out)
				}
				for _, said := range []string{"stack deleted:", "RETAINED", "aws s3api delete-bucket --bucket chdf-guard-bucket"} {
					if !strings.Contains(out, said) {
						t.Errorf("`just down` does not say %q, so it does not say what is left behind or how to remove it:\n%s", said, out)
					}
				}
			}
		})
	}
}

// TestRecordStoreBucketDaysReadsTheLiveWindowOrStops runs `just _days`, the
// recipe that decides what retention window `up` builds with.
//
// It used to end its read with `2>/dev/null || true`, so a throttle, an
// expired session and AccessDenied all printed 30, and the next `up` reset a
// window somebody chose to the default. It also took the first rule with any
// NoncurrentDays, whatever its Status or Filter (GitHub issue #1382).
func TestRecordStoreBucketDaysReadsTheLiveWindowOrStops(t *testing.T) {
	recordStoreBins(t)
	const awsErr = "An error occurred (%s) when calling the GetBucketLifecycleConfiguration operation: %s"
	rule := func(id, status string, days int, extra string) string {
		return fmt.Sprintf(`{"ID": %q, "Status": %q, "NoncurrentVersionExpiration": {"NoncurrentDays": %d}%s}`, id, status, days, extra)
	}
	rules := func(inner ...string) string {
		return `{"Rules": [` + strings.Join(inner, ",") + `]}`
	}
	for _, tc := range []struct {
		name     string
		env      string
		want     string
		wantFail bool
		wantSaid []string
	}{
		{
			name: "the bucket is not there",
			env:  "LIFECYCLE_ERROR=" + fmt.Sprintf(awsErr, "NoSuchBucket", "The specified bucket does not exist"),
			want: "30",
		},
		{
			name: "the bucket has no lifecycle configuration",
			env:  "LIFECYCLE_ERROR=" + fmt.Sprintf(awsErr, "NoSuchLifecycleConfiguration", "The lifecycle configuration does not exist"),
			want: "30",
		},
		{
			name:     "the read is denied",
			env:      "LIFECYCLE_ERROR=" + fmt.Sprintf(awsErr, "AccessDenied", "Access Denied"),
			wantFail: true,
			wantSaid: []string{"AccessDenied", "RECORD_NONCURRENT_DAYS"},
		},
		{
			name:     "the read is throttled",
			env:      "LIFECYCLE_ERROR=" + fmt.Sprintf(awsErr, "SlowDown", "Please reduce your request rate"),
			wantFail: true,
			wantSaid: []string{"SlowDown"},
		},
		{
			name: "one enabled rule reaching every key",
			env:  "LIFECYCLE_JSON=" + rules(rule("expire-superseded-record-versions", "Enabled", 90, "")),
			want: "90",
		},
		{
			name: "a disabled rule is not the window",
			env:  "LIFECYCLE_JSON=" + rules(rule("old", "Disabled", 5, ""), rule("live", "Enabled", 90, "")),
			want: "90",
		},
		{
			name: "a prefix-filtered rule is not the window",
			env:  "LIFECYCLE_JSON=" + rules(rule("outputs-only", "Enabled", 5, `, "Filter": {"Prefix": "tofu-outputs/"}`), rule("live", "Enabled", 90, "")),
			want: "90",
		},
		{
			name: "a tag-filtered rule is not the window",
			env:  "LIFECYCLE_JSON=" + rules(rule("tagged", "Enabled", 5, `, "Filter": {"Tag": {"Key": "tofu-estate", "Value": "prod"}}`), rule("live", "Enabled", 90, "")),
			want: "90",
		},
		{
			name: "no rule expires noncurrent versions",
			env:  "LIFECYCLE_JSON=" + `{"Rules": [{"ID": "abort-only", "Status": "Enabled", "AbortIncompleteMultipartUpload": {"DaysAfterInitiation": 7}}]}`,
			want: "30",
		},
		{
			name:     "two rules could be the window",
			env:      "LIFECYCLE_JSON=" + rules(rule("one", "Enabled", 90, ""), rule("two", "Enabled", 7, "")),
			wantFail: true,
			wantSaid: []string{"one: 90 day(s)", "two: 7 day(s)", "RECORD_NONCURRENT_DAYS"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "aws", recordStoreAWSStub)
			env := recordStoreEnv(t, stubDir, tc.env)
			code, out := recordStoreRun(t, env, "_days", "chdf-guard-bucket")
			if tc.wantFail {
				if code == 0 {
					t.Errorf("`just _days` exited 0 and printed %q. A read that did not happen is not a bucket without a window, and answering 30 here resets one somebody chose:\n%s", strings.TrimSpace(out), out)
				}
				// The line, not the substring: the refusal itself talks
				// about the 30 day default it is declining to use.
				for _, line := range strings.Split(out, "\n") {
					if strings.TrimSpace(line) == "30" {
						t.Errorf("`just _days` printed a bare 30 on a failed read:\n%s", out)
					}
				}
				for _, said := range tc.wantSaid {
					if !strings.Contains(out, said) {
						t.Errorf("`just _days` did not say %q, so its refusal does not show what stopped it:\n%s", said, out)
					}
				}
				return
			}
			if code != 0 {
				t.Fatalf("`just _days` exited %d:\n%s", code, out)
			}
			if got := strings.TrimSpace(out); got != tc.want {
				t.Errorf("`just _days` printed %q, want %q:\n%s", got, tc.want, out)
			}
		})
	}
}

// TestRecordStoreBucketUpRefusesASilentEncryptionDowngrade runs `just up`.
//
// `up` already preserves the retention window it finds on the live bucket.
// The key is the same kind of decision and had no such protection: a second
// `up` with RECORD_KMS_KEY_ARN unset rebuilt a KMS bucket as SSE-S3 and
// dropped both KMS Deny statements from the bucket policy (GitHub issue
// #1382). The accepting cases matter as much as the refusing ones: a guard
// that refuses everything would pass the refusal tests alone.
func TestRecordStoreBucketUpRefusesASilentEncryptionDowngrade(t *testing.T) {
	recordStoreBins(t)
	recordStoreChant(t)
	const key = "arn:aws:kms:us-east-2:111122223333:key/8c1e7b2a-0000-4a4a-9d3d-records"
	const other = "arn:aws:kms:us-east-2:111122223333:key/deadbeef-0000-4a4a-9d3d-records"
	kmsConfig := fmt.Sprintf(`{"ServerSideEncryptionConfiguration": [{"ApplyServerSideEncryptionByDefault": {"SSEAlgorithm": "aws:kms", "KMSMasterKeyID": %q}, "BucketKeyEnabled": true}]}`, key)
	for _, tc := range []struct {
		name        string
		env         []string
		wantRefusal bool
		wantSaid    []string
	}{
		{
			name:        "the bucket has a key and RECORD_KMS_KEY_ARN is unset",
			env:         []string{"ENCRYPTION_JSON=" + kmsConfig},
			wantRefusal: true,
			wantSaid:    []string{key, "RECORD_KMS_KEY_ARN is unset", "Deny statements"},
		},
		{
			name:        "RECORD_KMS_KEY_ARN names a different key",
			env:         []string{"ENCRYPTION_JSON=" + kmsConfig, "RECORD_KMS_KEY_ARN=" + other},
			wantRefusal: true,
			wantSaid:    []string{key, other},
		},
		{
			name:        "the encryption read fails",
			env:         []string{"ENCRYPTION_ERROR=An error occurred (AccessDenied) when calling the GetBucketEncryption operation: Access Denied"},
			wantRefusal: true,
			wantSaid:    []string{"AccessDenied"},
		},
		{
			name: "RECORD_KMS_KEY_ARN is the key the bucket has",
			env:  []string{"ENCRYPTION_JSON=" + kmsConfig, "RECORD_KMS_KEY_ARN=" + key},
		},
		{
			name: "the bucket has no customer managed key",
			env:  []string{"ENCRYPTION_ERROR=An error occurred (ServerSideEncryptionConfigurationNotFoundError) when calling the GetBucketEncryption operation: The server side encryption configuration was not found"},
		},
		{
			name: "there is no bucket yet",
			env:  []string{"ENCRYPTION_ERROR=An error occurred (NoSuchBucket) when calling the GetBucketEncryption operation: The specified bucket does not exist"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubDir := t.TempDir()
			recordStoreStub(t, stubDir, "aws", recordStoreAWSStub)
			extra := append([]string{
				"LIFECYCLE_ERROR=An error occurred (NoSuchBucket) when calling the GetBucketLifecycleConfiguration operation: The specified bucket does not exist",
			}, tc.env...)
			env := recordStoreEnv(t, stubDir, extra...)
			code, out := recordStoreRun(t, env, "up", "chdf-guard-bucket")
			calls := recordStoreCalls(t, stubDir)
			deployed := strings.Contains(calls, "cloudformation deploy")

			if tc.wantRefusal {
				if code == 0 {
					t.Errorf("`just up` exited 0:\n%s", out)
				}
				if deployed {
					t.Errorf("`just up` refused and deployed anyway. It ran:\n%s\noutput:\n%s", calls, out)
				}
				for _, said := range tc.wantSaid {
					if !strings.Contains(out, said) {
						t.Errorf("`just up`'s refusal does not name %q:\n%s", said, out)
					}
				}
				return
			}
			if code != 0 {
				t.Errorf("`just up` exited %d where nothing was being changed behind anybody's back:\n%s", code, out)
			}
			if !deployed {
				t.Errorf("`just up` never deployed, so the refusals above prove nothing: a guard that stops every run stops the wrong ones too. It ran:\n%s\noutput:\n%s", calls, out)
			}
		})
	}
}

// TestRecordStoreBucketTemplateRetainsAndClearsDeleteMarkers reads the BUILT
// CloudFormation template, which is the only place these two say what
// CloudFormation and S3 will read (GitHub issue #1382).
func TestRecordStoreBucketTemplateRetainsAndClearsDeleteMarkers(t *testing.T) {
	recordStoreChant(t)
	out := filepath.Join(t.TempDir(), "template.json")
	cmd := exec.Command("npx", "chant", "build", "src", "--lexicon", "aws", "-o", out)
	cmd.Dir = recordStoreProject
	cmd.Env = append(os.Environ(), "RECORD_BUCKET=chdf-guard-bucket", "RECORD_NONCURRENT_DAYS=30")
	if built, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("chant build: %v\n%s", err, built)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var tmpl struct {
		Resources map[string]struct {
			Type                string `json:"Type"`
			DeletionPolicy      string `json:"DeletionPolicy"`
			UpdateReplacePolicy string `json:"UpdateReplacePolicy"`
			Properties          struct {
				LifecycleConfiguration struct {
					Rules []map[string]any `json:"Rules"`
				} `json:"LifecycleConfiguration"`
			} `json:"Properties"`
		} `json:"Resources"`
	}
	if err := json.Unmarshal(raw, &tmpl); err != nil {
		t.Fatal(err)
	}
	var found bool
	for name, res := range tmpl.Resources {
		if res.Type != "AWS::S3::Bucket" {
			continue
		}
		found = true
		// Retain on both. DeletionPolicy covers deleting the stack;
		// UpdateReplacePolicy covers CloudFormation replacing the bucket
		// during an update, which a BucketName change is, and which would
		// take the records with it just as finally.
		if res.DeletionPolicy != "Retain" {
			t.Errorf("%s has DeletionPolicy %q, want Retain: a delete-stack would take every record with it", name, res.DeletionPolicy)
		}
		if res.UpdateReplacePolicy != "Retain" {
			t.Errorf("%s has UpdateReplacePolicy %q, want Retain: a replacement during an update would take every record with it", name, res.UpdateReplacePolicy)
		}
		rules := res.Properties.LifecycleConfiguration.Rules
		if len(rules) != 1 {
			t.Fatalf("%s has %d lifecycle rules, want 1: %v", name, len(rules), rules)
		}
		rule := rules[0]
		if rule["ExpiredObjectDeleteMarker"] != true {
			t.Errorf("%s's lifecycle rule does not set ExpiredObjectDeleteMarker, so a destroyed estate's delete markers never expire and `just down` refuses forever: %v", name, rule)
		}
		// S3 rejects a rule that combines ExpiredObjectDeleteMarker with
		// an expiry by days or date, or with tag filters. It also must
		// never expire current objects: a record is not a log.
		for _, banned := range []string{"ExpirationInDays", "ExpirationDate", "TagFilters"} {
			if _, ok := rule[banned]; ok {
				t.Errorf("%s's lifecycle rule sets both ExpiredObjectDeleteMarker and %s. S3 rejects that combination, and this configuration would not apply at all: %v", name, banned, rule)
			}
		}
	}
	if !found {
		t.Fatalf("the built template has no AWS::S3::Bucket:\n%s", raw)
	}
}
