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
// Every one of those tests starts its own emulator on a port the kernel picks
// for it, under a container name that no other process - not even another
// checkout running this same tier at the same time - can collide with.
// Cleanup removes exactly the containers this process created; leftovers from
// a run that died without its cleanup are swept only once they are old enough
// that no live run can still own them.
package flocitest

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// defaultImage is the emulator every test in this tier runs against:
// lex00/floci, not upstream floci/floci — the fork carries lex00/floci#24
// (iam:GetRole/GetUser/GetPolicy now return Tags, which retired the
// standing unowned-role residue live/e2e/run.sh's floci-tier tests used to
// tolerate) and lex00/floci#25 (ecr:CreateRepository no longer needs a
// Docker daemon), neither merged upstream yet. Pinned by digest rather than
// `:latest` so a later push to the fork's main cannot silently change what
// this tier runs against; this is lex00/floci's `latest` tag as of
// 2026-08-12 (commit b2548a0). FLOCI_IMAGE overrides it, the same env-var
// name run.sh uses for the same purpose.
const defaultImage = "ghcr.io/lex00/floci@sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34"

// image is what StartFloci actually runs: defaultImage unless FLOCI_IMAGE
// overrides it.
func image() string {
	if v := os.Getenv("FLOCI_IMAGE"); v != "" {
		return v
	}
	return defaultImage
}

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
	// This file lives at internal/live/flocitest/flocitest.go.
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
	return fixtureDir(t, filepath.Join("live", "e2e", "estate"))
}

// LimitsDir returns the path of the limits fixture, one directory per
// construct the stateless mode does not serve.
func LimitsDir(t *testing.T) string {
	t.Helper()
	return fixtureDir(t, filepath.Join("live", "e2e", "limits"))
}

// ImportFixtureDir returns the path of the live-import fixture (issue #61):
// a small, deliberately marker-free configuration standing in for an estate
// that has run under ordinary state-backed OpenTofu/Terraform and never used
// live resource markers.
func ImportFixtureDir(t *testing.T) string {
	t.Helper()
	return fixtureDir(t, filepath.Join("live", "e2e", "import-fixture"))
}

// EstatesDir returns the path of the per-cohort verification estates
// directory, live/e2e/estates: one subdirectory per ratification batch,
// exercised only in the gated tier (#48, phase 3 of #38). Unlike EstateDir,
// a missing directory here is not a fixture error - CohortDirs treats it as
// an empty cohort set rather than failing the test.
func EstatesDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(RepoRoot(t), "live", "e2e", "estates")
}

// CohortDirs returns the path of every per-cohort verification estate under
// live/e2e/estates, sorted. Adding a estates/<cohort> directory picks it up
// here with no test-file edits, which is the union pin's whole point.
func CohortDirs(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(EstatesDir(t))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("reading the per-cohort estates directory: %v", err)
	}

	var dirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(EstatesDir(t), entry.Name()))
	}
	sort.Strings(dirs)
	return dirs
}

// FixtureDirs returns the demo estate plus every per-cohort verification
// estate under live/e2e/estates - the union a table-equals-fixture pin now
// covers, generalized from table == estate to table == union(estate,
// estates/*) (#48). The demo estate is always first and always present;
// cohorts follow in sorted order.
func FixtureDirs(t *testing.T) []string {
	t.Helper()
	return append([]string{EstateDir(t)}, CohortDirs(t)...)
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
// returns its directory, so that terraform's own artifacts - .terraform,
// state - and any edit a test makes never touch the checkout.
//
// The fixture's .terraform.lock.hcl comes along too: it is what lets an init
// against the shared plugin cache trust the cached package instead of
// re-downloading it over a copy some other process is executing.
func CopyEstate(t *testing.T) string {
	t.Helper()
	return CopyFixtureDir(t, EstateDir(t))
}

// CopyFixtureDir is [CopyEstate] generalized to any fixture directory: it
// makes a scratch copy of src's .tf files (and .terraform.lock.hcl, for the
// same plugin-cache-trust reason CopyEstate's doc explains) and returns the
// copy's directory, so terraform's own artifacts and any edit a test makes
// never touch the checkout.
func CopyFixtureDir(t *testing.T, src string) string {
	t.Helper()

	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading the fixture directory %s: %v", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".tf") && e.Name() != ".terraform.lock.hcl") {
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

// FreePort asks the kernel for a free TCP port on 127.0.0.1 and returns it.
//
// The port is only guaranteed free at the moment the probe listener closes,
// not at the moment the container binds it. StartFloci accepts that gap and
// retries once on a fresh port if docker loses the race.
func FreePort(t *testing.T) string {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probing for a free port: %v", err)
	}
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	if err := l.Close(); err != nil {
		t.Fatalf("releasing the port probe on %s: %v", port, err)
	}
	return port
}

// StartFloci runs the emulator on a host port of the kernel's choosing, waits
// for it to report healthy, and removes it when the test finishes. It returns
// the port, which the test threads to AWS_ENDPOINT_URL the same way run.sh
// threads FLOCI_PORT.
//
// prefix names the container: the running process's PID and a random suffix
// are appended, so no other run - not another package in this process's test
// sweep, and not another checkout running the tier at the same time - can
// collide with it or mistake it for its own. Cleanup removes exactly the
// container this call created, never a neighbour's.
func StartFloci(t *testing.T, prefix string) string {
	t.Helper()

	// A run that died without its cleanup - a panic, a kill -9, a test
	// timeout - leaks its container. It no longer holds a port anybody wants,
	// but it does hold memory, so sweep the ones old enough that no live run
	// can still own them. Recent ones may belong to a parallel session and
	// are not ours to touch.
	removeAged(t, prefix)

	port, err := startFlociOnce(t, prefix)
	if err != nil {
		// Most likely the free-port race: the port FreePort probed was taken
		// again by the time docker tried to bind it. One fresh port, one
		// more try.
		t.Logf("starting floci: %v\nretrying once on a fresh port", err)
		if port, err = startFlociOnce(t, prefix); err != nil {
			t.Fatalf("starting floci: %v", err)
		}
	}
	return port
}

// startFlociOnce makes one attempt: pick a port, start the container, wait
// for health. A docker run failure comes back as an error so the caller can
// retry it; a container that starts but never turns healthy fails the test
// here, because a second port would not cure it.
func startFlociOnce(t *testing.T, prefix string) (string, error) {
	t.Helper()

	hostPort := FreePort(t)
	name := fmt.Sprintf("%s-%d-%s", prefix, os.Getpid(), randomSuffix(t))

	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", "127.0.0.1:"+hostPort+":4566", "--name", name, image()).CombinedOutput()
	if err != nil {
		// A failed docker run can still leave a Created container holding
		// the name. Ours to remove, by exact name.
		_ = exec.Command("docker", "rm", "-f", name).Run()
		return "", fmt.Errorf("docker run --name %s -p %s: %w\n%s", name, hostPort, err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			t.Logf("removing the floci container %s: %v\n%s", name, err, out)
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
				return hostPort, nil
			}
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Fatalf("floci did not become healthy at %s within 3m (last error: %v)\ncontainer logs:\n%s", health, err, logs)
		}
		time.Sleep(2 * time.Second)
	}
}

// randomSuffix is the part of a container name that separates two containers
// started by the same PID, and two processes that happen to share a PID
// across host/container boundaries.
func randomSuffix(t *testing.T) string {
	t.Helper()

	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generating a container name suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// leftoverAge is how old a container under a known prefix must be before the
// sweep may call it leaked. Every test in this tier lives for minutes; a
// parallel session's container is by definition younger than this.
const leftoverAge = time.Hour

// removeAged removes leftover containers whose names begin with prefix and
// whose age says no live run can still own them.
//
// Errors are logged, not fatal: docker being unreachable is the caller's next
// problem anyway, and it reports it far better than this can. A container
// whose age cannot be read is left alone - when in doubt, it is somebody
// else's.
func removeAged(t *testing.T, prefix string) {
	t.Helper()

	// The ^ anchors the filter to the start of the name and the trailing -
	// stops "tofu-stateless-p2" from reaching "tofu-stateless-p21"'s
	// containers.
	out, err := exec.Command("docker", "ps", "-a", "--filter", "name=^"+prefix+"-",
		"--format", "{{.ID}}\t{{.Names}}\t{{.CreatedAt}}").Output()
	if err != nil {
		t.Logf("listing leftover %s-* containers: %v", prefix, err)
		return
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		id, name, createdAt := parts[0], parts[1], parts[2]
		created, err := time.Parse("2006-01-02 15:04:05 -0700 MST", createdAt)
		if err != nil {
			t.Logf("cannot read the age of container %s (%q); leaving it alone", name, createdAt)
			continue
		}
		if time.Since(created) < leftoverAge {
			// Young enough to be a parallel session's live emulator.
			continue
		}
		if rm, err := exec.Command("docker", "rm", "-f", id).CombinedOutput(); err != nil {
			t.Logf("removing leftover container %s: %v\n%s", name, err, rm)
		} else {
			t.Logf("removed %s, a leftover older than %s", name, leftoverAge)
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

// PluginCacheDir points TF_PLUGIN_CACHE_DIR at a stable per-user directory,
// so the provider release the estate fixture pins unpacks once per machine
// rather than once per test package. An environment that already chose a
// cache keeps it; an environment where no per-user cache location exists
// falls back to a per-test directory, which is what every test did before
// the cache was shared.
func PluginCacheDir(t *testing.T) {
	t.Helper()

	if os.Getenv("TF_PLUGIN_CACHE_DIR") != "" {
		return
	}
	base, err := os.UserCacheDir()
	if err != nil {
		t.Logf("no user cache directory (%v); using a per-test plugin cache", err)
		t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())
		return
	}
	dir := filepath.Join(base, "choudoufu-test-plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("cannot create %s (%v); using a per-test plugin cache", dir, err)
		t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())
		return
	}
	t.Setenv("TF_PLUGIN_CACHE_DIR", dir)
	// Without this, an init whose workdir lock file has no entry for the
	// provider - every tofu init here, since the fixture's lock file records
	// registry.terraform.io and tofu resolves registry.opentofu.org -
	// re-downloads the release and rewrites the cache entry in place, under
	// a binary some other package's process is executing. With it, a cache
	// hit is trusted and only the first, lock-serialized init writes.
	t.Setenv("TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE", "1")
}

// lockPluginCache serializes provider installation across every process that
// shares TF_PLUGIN_CACHE_DIR, and returns the unlock. The cache directory is
// not safe for concurrent writers - two inits unpacking the same release into
// it can hand each other half-written files - and this tier runs its packages
// as parallel test processes, possibly next to a second checkout's run.
//
// The lock is a plain O_EXCL lockfile inside the cache directory, so it works
// across processes on every platform. A lockfile older than lockStaleAfter is
// a crashed holder's and is broken; a healthy holder only keeps it for one
// init, which a warm cache finishes in seconds.
func lockPluginCache(t *testing.T) (unlock func()) {
	t.Helper()

	cache := os.Getenv("TF_PLUGIN_CACHE_DIR")
	if cache == "" {
		return func() {}
	}
	lock := filepath.Join(cache, ".choudoufu-init.lock")
	deadline := time.Now().Add(15 * time.Minute)
	for {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // path derives from the env var the caller set
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() {
				if err := os.Remove(lock); err != nil && !errors.Is(err, os.ErrNotExist) {
					t.Logf("releasing the plugin cache lock: %v", err)
				}
			}
		}
		if !errors.Is(err, os.ErrExist) {
			t.Logf("cannot take the plugin cache lock at %s (%v); proceeding without it", lock, err)
			return func() {}
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > lockStaleAfter {
			t.Logf("breaking a stale plugin cache lock at %s (held since %s)", lock, info.ModTime().Format(time.RFC3339))
			_ = os.Remove(lock)
			continue
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin cache lock at %s has been held for 15m; remove it if its owner is gone", lock)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// lockStaleAfter is how long a plugin cache lockfile may sit before a waiter
// calls its holder dead. A cold-cache init downloads one provider release,
// which is minutes at the worst; ten of them is a crash.
const lockStaleAfter = 10 * time.Minute

// Run runs a command in dir and fails the test if it does not succeed.
//
// An init is run under the shared plugin cache's cross-process lock, because
// that is the one command in this tier that writes to the cache.
func Run(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	if len(args) > 0 && args[0] == "init" {
		defer lockPluginCache(t)()
	}
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
