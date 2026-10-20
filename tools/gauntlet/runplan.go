// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Choosing, sizing and estimating a `gauntlet run` before it starts (#2037).
//
// On 2026-10-12 a 21-estate kubernetes re-run was chosen by pasting 21 names
// by hand, ran serially because -parallel defaulted to 1, and every estimate
// of how long it would take was a guess. This file is the part of the fix
// that happens before the prompt: which estates, how many at once, and how
// long that should take - all printed so the person sees it before typing
// run.

// runSelection is what `gauntlet run`'s selection flags asked for.
type runSelection struct {
	Names []string // explicit estates, always run
	Set   string   // "core" or "all": the base the filters narrow, and the whole selection when nothing else is given
	// Filters. Each one narrows; together they intersect; the result is
	// unioned with Names.
	Lane     string
	NotClear bool
	Stale    string // a commit-ish: rows not measured at or after it
}

func (s runSelection) filtered() bool { return s.Lane != "" || s.NotClear || s.Stale != "" }

// ancestorFunc answers `git merge-base --is-ancestor <ref> <commit>`. An
// error means git could not compare the two at all - an unknown commit, a
// shallow checkout - which selectEstates reads as stale.
type ancestorFunc func(ref, commit string) (bool, error)

// selectEstates turns a runSelection into the estates to run, in a stable
// order: explicit names first as given, then the filtered estates in
// manifest order. a must already be Rebuilt (loadAll does), because
// -not-clear reads each row's derived Clear.
//
// With no names and no filters it is -set's meaning, unchanged. With
// filters it is never that: an empty result is an error that says what
// matched nothing, so `-not-clear` on a fully clear board refuses rather
// than quietly becoming a whole-set run.
func selectEstates(m *Manifest, a *Artifact, s runSelection, ancestor ancestorFunc) ([]Estate, error) {
	var out []Estate
	seen := map[string]bool{}
	add := func(e Estate) {
		if !seen[e.Name] {
			seen[e.Name] = true
			out = append(out, e)
		}
	}
	for _, n := range s.Names {
		e, ok := m.ByName(n)
		if !ok {
			return nil, fmt.Errorf("estate %q is not in %s", n, ManifestPath)
		}
		add(e)
	}
	if s.Lane != "" && !contains(KnownLanes, s.Lane) {
		return nil, fmt.Errorf("-lane %q is not a lane; known lanes: %s", s.Lane, strings.Join(KnownLanes, ", "))
	}
	if len(s.Names) > 0 && !s.filtered() {
		return out, nil
	}
	for _, e := range m.Estates {
		if s.Set == "core" && e.Set != SetCore {
			continue
		}
		if s.Lane != "" && e.Lane != s.Lane {
			continue
		}
		r, _ := a.Result(e.Name)
		if s.NotClear && r.Clear {
			continue
		}
		if s.Stale != "" && !rowStale(r, s.Stale, ancestor) {
			continue
		}
		add(e)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gauntlet run: the selection is empty (%s) - nothing to run, and a selection that matches nothing never falls back to a whole-set run", s.describe())
	}
	return out, nil
}

// rowStale is -stale's definition: the row was not measured at or after
// ref, i.e. `git merge-base --is-ancestor <ref> <last_run.commit>` is false.
// A row with no run or no commit is stale, and so is one whose commit git
// cannot compare (an orphaned sha, a shallow checkout): there is no evidence
// it was measured after ref, which is the question.
func rowStale(r EstateResult, ref string, ancestor ancestorFunc) bool {
	if r.LastRun == nil || r.LastRun.Commit == "" {
		return true
	}
	ok, err := ancestor(ref, r.LastRun.Commit)
	return err != nil || !ok
}

func (s runSelection) describe() string {
	var parts []string
	if s.Set == "core" {
		parts = append(parts, "-set core")
	}
	if s.Lane != "" {
		parts = append(parts, "-lane "+s.Lane)
	}
	if s.NotClear {
		parts = append(parts, "-not-clear")
	}
	if s.Stale != "" {
		parts = append(parts, "-stale "+s.Stale)
	}
	if len(parts) == 0 {
		return "no filters"
	}
	return strings.Join(parts, " ")
}

// gitAncestor is the real ancestorFunc for root's history.
func gitAncestor(root string) ancestorFunc {
	return func(ref, commit string) (bool, error) { return isAncestor(root, ref, commit) }
}

// Per-estate memory footprints for -parallel auto (#2037). Estimates, not
// measurements: a kind estate runs one or two kind node containers (the
// estate's cluster and the oracle's) plus tofu and the providers, and a
// floci estate runs one floci container plus tofu. They are deliberately
// round and on the generous side, because guessing low starts more estates
// than the machine holds and an OOM-killed kind node fails a stage for a
// reason that is not the product's. Replace them with measured numbers when
// a run records peak memory.
const (
	kindEstateFootprint  = 2.5 * (1 << 30) // kubernetes lane, floci-eks, or any script that calls gauntlet_kind_up
	flociEstateFootprint = 0.5 * (1 << 30) // floci-only estates
)

// dockerCapacity is what -parallel auto reads from the docker daemon.
type dockerCapacity struct {
	MemTotal int64 // bytes, `docker info` MemTotal
	InUse    int64 // bytes, summed over running containers (`docker stats`)
	NCPU     int
}

// usesKind reports whether an estate needs a kubernetes cluster: the
// kubernetes lane, a floci-eks estate (floci starts the cluster leg), or a
// script under its directory that calls gauntlet_kind_up. A floci-az estate
// (#1120) runs one emulator container, like a floci estate.
func usesKind(root string, e Estate) bool {
	if e.Lane == LaneKubernetes || e.Substrate() == SubstrateKind || e.Substrate() == SubstrateFlociEKS {
		return true
	}
	dir := filepath.Dir(filepath.Join(root, e.ScriptPath()))
	files, _ := filepath.Glob(filepath.Join(dir, "*.sh"))
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // estate scripts under the checkout
		if err == nil && strings.Contains(string(b), "gauntlet_kind_up") {
			return true
		}
	}
	return false
}

// autoParallel is -parallel auto's arithmetic: free memory (MemTotal minus
// what running containers already use) divided by the selection's largest
// per-estate footprint, capped by CPUs and by the number of estates, never
// below 1. It returns the count and a sentence saying how it was reached,
// which the plan prints.
//
// The largest footprint, not an average: a mixed selection can have all its
// kind estates running at once, and sizing for the floci ones would start
// more clusters than fit.
func autoParallel(c dockerCapacity, kind, total int) (int, string) {
	if total < 1 {
		return 1, "no estates"
	}
	footprint, what := float64(flociEstateFootprint), "floci estate"
	if kind > 0 {
		footprint, what = float64(kindEstateFootprint), "kind estate"
	}
	free := c.MemTotal - c.InUse
	if free < 0 {
		free = 0
	}
	n := int(math.Floor(float64(free) / footprint))
	why := fmt.Sprintf("auto: docker has %s, %s in use by running containers, %s free / %s per %s = %d", gib(c.MemTotal), gib(c.InUse), gib(free), gib(int64(footprint)), what, n)
	if c.NCPU > 0 && n > c.NCPU {
		n = c.NCPU
		why += fmt.Sprintf(", capped by %d CPUs", c.NCPU)
	}
	if n > total {
		n = total
		why += fmt.Sprintf(", capped by %d estate(s)", total)
	}
	if n < 1 {
		n = 1
		why += ", raised to the minimum of 1"
	}
	return n, why
}

func gib(b int64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

// readDockerCapacity asks the docker daemon. An error leaves -parallel auto
// at 1, and the plan says why.
func readDockerCapacity() (dockerCapacity, error) {
	var c dockerCapacity
	out, err := exec.Command("docker", "info", "--format", "{{json .}}").Output()
	if err != nil {
		return c, fmt.Errorf("docker info: %w", err)
	}
	var info struct {
		MemTotal int64
		NCPU     int
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return c, fmt.Errorf("docker info: %w", err)
	}
	if info.MemTotal <= 0 {
		return c, fmt.Errorf("docker info reported no MemTotal")
	}
	c.MemTotal, c.NCPU = info.MemTotal, info.NCPU
	stats, err := exec.Command("docker", "stats", "--no-stream", "--format", "{{.MemUsage}}").Output()
	if err != nil {
		return c, fmt.Errorf("docker stats: %w", err)
	}
	c.InUse = sumMemUsage(string(stats))
	return c, nil
}

// sumMemUsage adds up the used half of each `docker stats` MemUsage line
// ("1.2GiB / 7.6GiB"). A line it cannot read counts as nothing.
func sumMemUsage(lines string) int64 {
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(lines), "\n") {
		used, _, _ := strings.Cut(line, "/")
		total += parseByteSize(strings.TrimSpace(used))
	}
	return total
}

// parseByteSize reads docker's sizes: "512MiB", "1.5GiB", "800kB", "0B".
func parseByteSize(s string) int64 {
	units := []struct {
		suffix string
		mult   float64
	}{
		{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30}, {"TiB", 1 << 40},
		{"kB", 1e3}, {"KB", 1e3}, {"MB", 1e6}, {"GB", 1e9}, {"TB", 1e12}, {"B", 1},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil {
				return 0
			}
			return int64(v * u.mult)
		}
	}
	return 0
}

// parseParallel reads -parallel: "auto" or a positive integer.
func parseParallel(v string) (n int, auto bool, err error) {
	if v == "auto" {
		return 0, true, nil
	}
	n, err = strconv.Atoi(v)
	if err != nil || n < 1 {
		return 0, false, fmt.Errorf("-parallel %q: want auto or a positive number", v)
	}
	return n, false, nil
}

// estimateWall is longest-processing-time scheduling of the recorded
// durations over slots: longest first, each onto the least-loaded slot, and
// the answer is the most-loaded slot. It is how runResults actually hands
// out work only approximately - runResults goes in selection order - so it
// is an estimate, which is all the plan claims.
func estimateWall(durations []float64, slots int) float64 {
	if slots < 1 {
		slots = 1
	}
	d := append([]float64(nil), durations...)
	sort.Sort(sort.Reverse(sort.Float64Slice(d)))
	load := make([]float64, slots)
	for _, x := range d {
		min := 0
		for i := range load {
			if load[i] < load[min] {
				min = i
			}
		}
		load[min] += x
	}
	most := 0.0
	for _, l := range load {
		most = math.Max(most, l)
	}
	return most
}

// roughDuration is an estimate's precision: minutes once there are any.
func roughDuration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

// printRunPlan is what the person reads before typing run: every selected
// estate with its last recorded wall time, the parallel count and how it
// was reached, and the estimated wall time.
func printRunPlan(w io.Writer, selected []Estate, a *Artifact, parallel int, why string) {
	fmt.Fprintf(w, "gauntlet run plan (#2037): %d estate(s)\n", len(selected))
	var timed []float64
	var untimed []string
	for _, e := range selected {
		r, _ := a.Result(e.Name)
		last := "no timing recorded"
		if r.LastRun != nil && r.LastRun.DurationS > 0 {
			last = "last run " + formatDuration(r.LastRun.DurationS)
			timed = append(timed, r.LastRun.DurationS)
		} else {
			untimed = append(untimed, e.Name)
		}
		fmt.Fprintf(w, "  %-48s %-20s %s\n", e.Name, e.Lane, last)
	}
	fmt.Fprintf(w, "parallel: %d (%s)\n", parallel, why)
	switch {
	case len(timed) == 0:
		fmt.Fprintln(w, "estimated wall time: no timing recorded for any selected estate")
	default:
		est := estimateWall(timed, parallel)
		line := fmt.Sprintf("estimated wall time: %s over %d slot(s), from last_run.duration_s of %d of %d estate(s)", roughDuration(est), parallel, len(timed), len(selected))
		if len(untimed) > 0 {
			line += fmt.Sprintf("; no timing recorded, not counted: %s", strings.Join(untimed, " "))
		}
		fmt.Fprintln(w, line)
	}
}
