// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package largeset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/setplan"
)

// TestLargeSetSetPlanAgainstFloci is #1752's equivalence test and
// measurement: the fixture at N=5 applied at A, the module regenerated at B,
// then planned two ways, repeatedly and alternately - `live-plan-set` over
// every root, and `live-plan -out` plus `show -json` in each root in turn.
// Every root's resource_changes must be the same both ways on every repeat,
// and the same change set the baseline record (live/large-set/
// baseline-n5.json) recorded for the bump. Calls, wall clock and peak memory
// for both arms go into a record gated against that baseline
// (setplan_record.go).
//
// Dispatch only, like the baseline:
//
//	LARGESET_SETPLAN=1 LARGESET_SETPLAN_RECORD=$PWD/live/large-set/setplan-n5.json \
//	  env -u PWD go test ./internal/live/largeset/ -run TestLargeSetSetPlanAgainstFloci -v -timeout 40m
//
//	LARGESET_FLOCI_PORT     run floci on this host port as cdfa-setplan-floci
//	                        (default: a kernel-chosen port, flocitest.StartFloci)
//	LARGESET_REPEATS        repeats per arm (default 3)
//	LARGESET_SETPLAN_RECORD where to write the record; unset, the gate runs
//	                        and nothing is written
//
// N is 5 and nothing else: the baseline it is gated against is N=5.
func TestLargeSetSetPlanAgainstFloci(t *testing.T) {
	if os.Getenv("LARGESET_SETPLAN") == "" {
		t.Skip("the set-plan measurement runs on dispatch only: set LARGESET_SETPLAN=1 (needs docker, bash, go and the pinned floci image)")
	}
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "bash")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, "ps")

	const n = 5
	repeats := envInt(t, "LARGESET_REPEATS", 3)
	basePath := filepath.Join(flocitest.RepoRoot(t), "live", "large-set", "baseline-n5.json")
	base, err := ReadBaseline(basePath)
	if err != nil {
		t.Fatal(err)
	}

	port := startSetPlanFloci(t)
	proxy := flocitest.NewCountingProxy(t, flocitest.Endpoint(port))
	flocitest.PluginCacheDir(t)
	bin := flocitest.BuildTofu(t)
	t.Setenv("AWS_ENDPOINT_URL", proxy.Endpoint())
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	work := t.TempDir()
	dir := filepath.Join(work, "fixture")
	m, err := Write(dir, Options{Estates: n, ModuleVersion: VersionA})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", filepath.Join(flocitest.RepoRoot(t), "live", "large-set", "apply.sh"), dir) //nolint:gosec // fixed script path
	cmd.Env = append(os.Environ(), "CHOUDOUFU_BIN="+bin, "LARGESET_ENDPOINT="+proxy.Endpoint())
	var applyErr strings.Builder
	cmd.Stderr = &applyErr
	out, _ := cmd.Output()
	verdict := strings.TrimSpace(string(out))
	t.Logf("%s", verdict)
	if !strings.HasPrefix(verdict, "LARGESET-APPLY: green") {
		t.Fatalf("the apply at A did not go green:\n%s\n%s", verdict, applyErr.String())
	}
	if _, err := Write(dir, Options{Estates: n, ModuleVersion: VersionB}); err != nil {
		t.Fatal(err)
	}

	byDir := map[string]Estate{}
	for _, e := range m.Estates {
		byDir[e.Dir] = e
	}
	rec := SetPlanRecord{
		Fixture:   FixtureShape{Estates: n, Source: SourceLocal, Prefix: m.Prefix, From: VersionA, To: VersionB},
		Condition: ConditionSetPlanVsSeparate,
		Substrate: "floci",
		Emulator:  flocitest.Image(),
		Commit:    baselineCommit(t),
		Date:      time.Now().UTC().Format(time.RFC3339),
		Repeats:   repeats,
		Baseline:  BaselineRef{Path: "live/large-set/baseline-n5.json", Commit: base.Commit, Date: base.Date},
		Set:       SetArm{ParallelEstates: n},
	}
	equivalent := map[string]bool{}
	for _, d := range m.ApplyOrder {
		rec.Estates = append(rec.Estates, SetPlanEstate{Estate: byDir[d].Name, Role: byDir[d].Role})
		equivalent[d] = true
	}

	for rep := 0; rep < repeats; rep++ {
		// ---- separate: live-plan -out, then show -json, one root at a time ----
		sepPlans := map[string]string{}
		var sepCalls int
		var sepSecs, slowest, showSecs float64
		var sepPeak int64
		var sepProcs int
		for _, d := range m.ApplyOrder {
			root := filepath.Join(dir, d)
			planFile := filepath.Join(work, fmt.Sprintf("sep-%d-%s.tfplan", rep, byDir[d].Name))
			before := proxy.Total()
			s, err := sampled(exec.Command(bin, "live-plan", "-no-color", "-input=false", "-detailed-exitcode", "-out="+planFile), root) //nolint:gosec // the binary this test built
			calls := proxy.Total() - before
			if code := exitOf(err); code != 2 {
				t.Fatalf("repeat %d: separate live-plan in %s exited %d, want 2 (changes):\n%s", rep+1, d, code, s.output)
			}
			sepCalls += calls
			sepSecs += s.seconds
			slowest = max(slowest, s.seconds)
			sepPeak = max(sepPeak, s.peakKB)
			sepProcs = max(sepProcs, s.peakProcs)

			show := exec.Command(bin, "show", "-json", "-no-color", planFile) //nolint:gosec // the binary this test built
			show.Dir = root
			var stdout bytes.Buffer
			show.Stdout = &stdout
			start := time.Now()
			if err := show.Run(); err != nil {
				t.Fatalf("repeat %d: separate show -json in %s: %v", rep+1, d, err)
			}
			showSecs += time.Since(start).Seconds()
			sepPlans[d] = resourceChanges(t, stdout.Bytes())
		}
		rec.Separate.LivePlan.Calls = append(rec.Separate.LivePlan.Calls, sepCalls)
		rec.Separate.LivePlan.Seconds = append(rec.Separate.LivePlan.Seconds, round2(sepSecs))
		rec.Separate.LivePlan.PeakRSSKB = append(rec.Separate.LivePlan.PeakRSSKB, sepPeak)
		rec.Separate.LivePlan.PeakProcs = append(rec.Separate.LivePlan.PeakProcs, sepProcs)
		rec.Separate.SlowestSeconds = append(rec.Separate.SlowestSeconds, round2(slowest))
		rec.Separate.ShowSeconds = append(rec.Separate.ShowSeconds, round2(showSecs))

		// ---- set: one live-plan-set over every root ----
		outDir := filepath.Join(work, fmt.Sprintf("set-%d", rep))
		args := append([]string{"live-plan-set", "-json", "-no-color", "-parallel-estates=" + strconv.Itoa(n), "-out-dir=" + outDir}, m.ApplyOrder...)
		before := proxy.Total()
		s, err := sampled(exec.Command(bin, args...), dir) //nolint:gosec // the binary this test built
		calls := proxy.Total() - before
		code := exitOf(err)
		var doc setplan.Document
		if jerr := json.Unmarshal([]byte(s.stdout), &doc); jerr != nil {
			t.Fatalf("repeat %d: live-plan-set exited %d and printed no document: %v\n%s", rep+1, code, jerr, s.output)
		}
		for _, r := range doc.Roots {
			if r.Status != setplan.StatusPlanned {
				t.Errorf("repeat %d: %s failed at %s: %s", rep+1, r.Root, r.Stage, r.Error)
			}
		}
		rec.Set.Calls = append(rec.Set.Calls, calls)
		rec.Set.Seconds = append(rec.Set.Seconds, round2(s.seconds))
		rec.Set.PeakRSSKB = append(rec.Set.PeakRSSKB, s.peakKB)
		rec.Set.PeakProcs = append(rec.Set.PeakProcs, s.peakProcs)
		rec.Set.ExitCodes = append(rec.Set.ExitCodes, code)

		// ---- equivalence, per root ----
		setPlans := map[string]setplan.Root{}
		for _, r := range doc.Roots {
			setPlans[r.Root] = r
		}
		for i, d := range m.ApplyOrder {
			r, ok := setPlans[d]
			if !ok || len(r.Plan) == 0 || string(r.Plan) == "null" {
				t.Errorf("repeat %d: the set has no plan for %s", rep+1, d)
				equivalent[d] = false
				continue
			}
			have := resourceChanges(t, r.Plan)
			if have != sepPlans[d] {
				t.Errorf("repeat %d: %s: the set's resource_changes differ from the separate live-plan's\nset:      %s\nseparate: %s", rep+1, d, have, sepPlans[d])
				equivalent[d] = false
			}
			add, change, destroy, changed, err := ChangeSet(r.Plan)
			if err != nil {
				t.Fatal(err)
			}
			if rep == 0 {
				e := &rec.Estates[i]
				e.Add, e.Change, e.Destroy, e.Changed = add, change, destroy, changed
			}
		}
	}
	for i, d := range m.ApplyOrder {
		rec.Estates[i].Equivalent = equivalent[d]
	}

	t.Logf("SET PLAN N=%d repeats=%d emulator=%s", n, repeats, rec.Emulator)
	t.Logf("set (parallel %d, init+plan+show per root): calls %v seconds %v peak_rss_kb %v procs %v exits %v",
		n, rec.Set.Calls, rec.Set.Seconds, rec.Set.PeakRSSKB, rec.Set.PeakProcs, rec.Set.ExitCodes)
	t.Logf("separate (live-plan -out, sequential): calls %v seconds %v slowest %v peak_rss_kb %v procs %v (show -json alone: %v)",
		rec.Separate.LivePlan.Calls, rec.Separate.LivePlan.Seconds, rec.Separate.SlowestSeconds, rec.Separate.LivePlan.PeakRSSKB, rec.Separate.LivePlan.PeakProcs, rec.Separate.ShowSeconds)
	for _, e := range rec.Estates {
		t.Logf("%s %-8s equivalent=%v %d/%d/%d %v", e.Estate, e.Role, e.Equivalent, e.Add, e.Change, e.Destroy, e.Changed)
	}

	path := os.Getenv("LARGESET_SETPLAN_RECORD")
	if path == "" {
		if err := GateSetPlan(rec, base); err != nil {
			t.Fatalf("the measurement does not pass the record's gate: %s", err)
		}
		t.Logf("set-plan record gated clean; set LARGESET_SETPLAN_RECORD=<path> to write it")
		return
	}
	if err := WriteSetPlan(path, rec, base); err != nil {
		t.Fatal(err)
	}
	t.Logf("set-plan record written: %s", path)
}

// startSetPlanFloci starts floci on LARGESET_FLOCI_PORT when it is set, as
// cdfa-setplan-floci, and removes it at the end; otherwise it defers to
// flocitest.StartFloci's kernel-chosen port.
func startSetPlanFloci(t *testing.T) string {
	t.Helper()
	port := os.Getenv("LARGESET_FLOCI_PORT")
	if port == "" {
		return flocitest.StartFloci(t, "cdfa-setplan")
	}
	name := "cdfa-setplan-floci"
	if out, err := exec.Command("docker", "run", "-d", "--rm", "-p", "127.0.0.1:"+port+":4566", "--name", name, flocitest.Image()).CombinedOutput(); err != nil {
		t.Fatalf("docker run %s on :%s: %v\n%s", name, port, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	deadline := time.Now().Add(3 * time.Minute)
	for {
		resp, err := http.Get(flocitest.Endpoint(port) + "/_localstack/health") //nolint:gosec,noctx // fixed localhost URL
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return port
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("floci on :%s did not become healthy within 3m", port)
		}
		time.Sleep(2 * time.Second)
	}
}

type sample struct {
	seconds   float64
	peakKB    int64
	peakProcs int
	stdout    string
	output    string
}

// sampled runs cmd in dir in a process group of its own and samples the
// group's total resident memory from ps every 100ms. The group holds the
// command, every child it starts (live-plan-set's per-root stages) and the
// provider plugins they launch.
func sampled(cmd *exec.Cmd, dir string) (sample, error) {
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return sample{}, err
	}
	pgid := cmd.Process.Pid
	var s sample
	var mu sync.Mutex
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			kb, procs := groupRSS(pgid)
			mu.Lock()
			s.peakKB = max(s.peakKB, kb)
			s.peakProcs = max(s.peakProcs, procs)
			mu.Unlock()
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	err := cmd.Wait()
	s.seconds = time.Since(start).Seconds()
	close(done)
	wg.Wait()
	s.stdout = stdout.String()
	s.output = stdout.String() + stderr.String()
	return s, err
}

// groupRSS sums ps's RSS (KiB on both macOS and Linux) over the processes in
// process group pgid.
func groupRSS(pgid int) (int64, int) {
	out, err := exec.Command("ps", "-A", "-o", "pgid=,rss=").Output()
	if err != nil {
		return 0, 0
	}
	var total int64
	procs := 0
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		g, err1 := strconv.Atoi(f[0])
		rss, err2 := strconv.ParseInt(f[1], 10, 64)
		if err1 != nil || err2 != nil || g != pgid {
			continue
		}
		total += rss
		procs++
	}
	return total, procs
}

func exitOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func round2(f float64) float64 { return float64(int(f*100)) / 100 }

// resourceChanges is a plan's resource_changes re-encoded, so two plans'
// compare as strings.
func resourceChanges(t *testing.T, plan []byte) string {
	t.Helper()
	var p struct {
		ResourceChanges any `json:"resource_changes"`
	}
	if err := json.Unmarshal(plan, &p); err != nil {
		t.Fatalf("not a plan: %v", err)
	}
	b, err := json.Marshal(p.ResourceChanges)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
