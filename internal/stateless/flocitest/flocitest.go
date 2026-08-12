// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package flocitest holds the gate, the fixture paths and the container
// bookkeeping that the floci integration tests share.
//
// The tests in this tier drive a real OpenTofu binary against the floci AWS
// emulator running in Docker, so they need Docker, terraform and the AWS CLI
// on PATH. They are opt-in: Gate skips them unless TF_ACC or TF_FLOCI_TEST is
// set, the same shape the backend integration tests use (see
// internal/backend/remote-state/s3/backend_test.go's testACC).
//
// Every one of those tests starts its own emulator on its own fixed port, and
// a run that dies without running its cleanup - a panic, a test timeout, a
// ctrl-C - leaves the container up and the port bound. The next run then fails
// in about a fifth of a second on a port bind, which looks nothing at all like
// whatever actually broke, and the real failure is a day's confusion away.
// StartFloci sweeps the port's own leftovers before it binds it.
package flocitest

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// image is the emulator every test in this tier runs.
const image = "floci/floci:latest"

// Gate skips t unless the floci integration tier is enabled.
//
// Either TF_ACC (the acceptance-test switch the whole repo shares) or
// TF_FLOCI_TEST (this tier alone) enables it, so both `TF_ACC=1 go test ./...`
// and `make test-floci` reach these tests. subject names what is under test,
// and appears in the skip message.
func Gate(t *testing.T, subject string) {
	t.Helper()

	if os.Getenv("TF_ACC") == "" && os.Getenv("TF_FLOCI_TEST") == "" {
		t.Skipf("the %s floci integration test requires setting TF_ACC or TF_FLOCI_TEST, and needs Docker, terraform and the AWS CLI", subject)
	}
}

// RepoRoot returns the absolute path of this checkout's root, resolved from
// this file's own location rather than from the caller's working directory,
// so a test does not have to count "../" for itself.
func RepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve the repository root: runtime.Caller failed")
	}
	// This file lives at internal/stateless/flocitest/flocitest.go.
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	return root
}

// EstateDir returns the path of the shared estate fixture, the multi-service
// configuration the integration tests plan and apply against.
func EstateDir(t *testing.T) string {
	t.Helper()
	return fixtureDir(t, filepath.Join("stateless", "e2e", "estate"))
}

// LimitsDir returns the path of the limits fixture, one directory per
// construct the stateless mode does not serve.
func LimitsDir(t *testing.T) string {
	t.Helper()
	return fixtureDir(t, filepath.Join("stateless", "e2e", "limits"))
}

func fixtureDir(t *testing.T, rel string) string {
	t.Helper()

	dir := filepath.Join(RepoRoot(t), rel)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the %s fixture is missing: %v", rel, err)
	}
	return dir
}

// CopyEstate makes a scratch copy of the estate fixture's .tf files and
// returns its directory, so that terraform's own artifacts - .terraform, the
// lock file, state - and any edit a test makes never touch the checkout.
func CopyEstate(t *testing.T) string {
	t.Helper()

	src := EstateDir(t)
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading the estate fixture: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name())) //nolint:gosec // a fixed path in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", e.Name(), err)
		}
	}
	return dst
}

// StartFloci runs the emulator on hostPort, waits for it to report healthy,
// and removes it when the test finishes.
//
// prefix names the container: the running process's PID is appended, so two
// packages tested in parallel never collide on a name. Each test in this tier
// owns a distinct prefix and a distinct hostPort.
func StartFloci(t *testing.T, prefix, hostPort string) {
	t.Helper()

	name := fmt.Sprintf("%s-%d", prefix, os.Getpid())

	// A leftover from a crashed run holds the port, and would answer our
	// requests with somebody else's estate.
	RemoveStale(t, prefix)

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", hostPort+":4566", "--name", name, image).CombinedOutput()
	if err != nil {
		t.Fatalf("starting floci: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			t.Logf("removing the floci container: %v\n%s", err, out)
		}
	})

	health := Endpoint(hostPort) + "/_localstack/health"
	deadline := time.Now().Add(3 * time.Minute)
	for {
		resp, err := http.Get(health) //nolint:gosec,noctx // fixed localhost URL, test-only
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Fatalf("floci did not become healthy at %s within 3m (last error: %v)\ncontainer logs:\n%s", health, err, logs)
		}
		time.Sleep(2 * time.Second)
	}
}

// RemoveStale kills and removes every container whose name begins with
// prefix, whatever process started it. StartFloci calls it; call it directly
// only for a container this package did not start.
//
// Each test's own pre-run "docker rm -f <name>" never helped, because the name
// carries the running process's PID and the leaked container is always some
// other process's.
//
// Errors are logged, not fatal: docker being unreachable is the caller's next
// problem anyway, and it reports it far better than this can.
func RemoveStale(t *testing.T, prefix string) {
	t.Helper()

	// The ^ anchors the filter to the start of the name, so a prefix of
	// "tofu-stateless-p21" cannot reach a neighbouring suite's container.
	out, err := exec.Command("docker", "ps", "-aq", "--filter", "name=^"+prefix).Output()
	if err != nil {
		t.Logf("listing stale %s* containers: %v", prefix, err)
		return
	}
	for _, id := range strings.Fields(string(out)) {
		if rm, err := exec.Command("docker", "rm", "-f", id).CombinedOutput(); err != nil {
			t.Logf("removing stale container %s: %v\n%s", id, err, rm)
		} else {
			t.Logf("removed a leaked %s* container (%s) before starting a fresh one", prefix, id)
		}
	}
}

// Endpoint is the emulator's URL on hostPort.
func Endpoint(hostPort string) string {
	return "http://localhost:" + hostPort
}

// BuildTofu builds this checkout's binary, which is what these tests are
// actually testing, and returns its path.
func BuildTofu(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "tofu")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/choudoufu")
	cmd.Dir = RepoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/choudoufu: %v\n%s", err, out)
	}
	return bin
}

// Run runs a command in dir and fails the test if it does not succeed.
func Run(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...) //nolint:gosec // fixed binaries, test-only
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

// AWSCLI runs one AWS CLI call against the emulator on hostPort and returns
// its trimmed standard output.
func AWSCLI(t *testing.T, hostPort string, args ...string) string {
	t.Helper()

	full := append([]string{"--endpoint-url", Endpoint(hostPort)}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	if err != nil {
		t.Fatalf("aws %s failed: %v%s", strings.Join(args, " "), err, stderrOf(err))
	}
	return strings.TrimSpace(string(out))
}

func stderrOf(err error) string {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return "\n" + string(exit.Stderr)
	}
	return ""
}

// RequireBinary fails the test when an external tool it drives is not on
// PATH. The gate is the opt-in; once a test is opted in, a missing tool is a
// broken environment rather than a reason to say nothing.
func RequireBinary(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Fatalf("%s is required by this test but is not on PATH", name)
	}
}

// SkipWithoutBinary skips the test when an external tool is not on PATH, for
// the tests that would rather degrade to a named skip than fail.
func SkipWithoutBinary(t *testing.T, name string) {
	t.Helper()

	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("required binary %q not found in PATH", name)
	}
}

// planSummaryLine matches the renderer's one-line plan total.
var planSummaryLine = regexp.MustCompile(`Plan: (\d+) to add, (\d+) to change, (\d+) to destroy`)

// PlanSummary reads the plan totals out of a plan's output. ok is false when
// the output carries no summary line at all, which is what a plan that
// refused to run looks like.
func PlanSummary(output string) (add, change, destroy int, ok bool) {
	m := planSummaryLine.FindStringSubmatch(output)
	if m == nil {
		return 0, 0, 0, false
	}
	add, _ = strconv.Atoi(m[1])
	change, _ = strconv.Atoi(m[2])
	destroy, _ = strconv.Atoi(m[3])
	return add, change, destroy, true
}

// ResourceBlock cuts one resource's diff out of the plan output: from its
// "# <address> will be" header to the closing brace at the same indentation.
func ResourceBlock(t *testing.T, output, addr string) string {
	t.Helper()

	lines := strings.Split(output, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "# "+addr+" will be") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no diff for %s in the plan output:\n%s", addr, output)
	}
	// Four-space indent, exactly: a nested map argument (tags = {...}) closes
	// at a deeper indent and also trims to "}", so a TrimSpace match stops at
	// the tags sub-object's own close and drops every attribute printed after
	// it - tags_all, value, version - which is precisely where a caller goes
	// looking for trouble. run.sh's plan_block carries the same anchor and the
	// same reasoning.
	for i := start + 1; i < len(lines); i++ {
		if lines[i] == "    }" {
			return strings.Join(lines[start:i+1], "\n")
		}
	}
	return strings.Join(lines[start:], "\n")
}

// resourceHeader matches the renderer's per-resource header line.
var resourceHeader = regexp.MustCompile(`^\s*# ([^ ]+) will be `)

// ChangedResources lists the addresses a plan proposes anything for, in the
// order the renderer printed them.
func ChangedResources(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		if m := resourceHeader.FindStringSubmatch(line); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

// changeLine matches a line proposing a change to an argument: the renderer's
// +, ~ or - marker, then the argument name.
var changeLine = regexp.MustCompile(`^\s*[+~-]\s+([A-Za-z_][A-Za-z0-9_]*)\s*=`)

// NonTagChanges names the arguments a resource's diff changes other than tags
// and tags_all, which is what a caller asking "is this diff only about
// ownership markers?" wants to know.
func NonTagChanges(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		m := changeLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch m[1] {
		case "tags", "tags_all":
		default:
			out = append(out, m[1])
		}
	}
	return out
}

// SectionFrom returns the output from a section header to the horizontal rule
// that ends it, or "" when the output has no such header.
func SectionFrom(output, header string) string {
	i := strings.Index(output, header)
	if i < 0 {
		return ""
	}
	rest := output[i:]
	if j := strings.Index(rest, "─────"); j > 0 {
		return rest[:j]
	}
	return rest
}
