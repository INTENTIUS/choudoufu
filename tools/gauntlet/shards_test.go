// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The board is measured one estate per CI job (#1550/#1554): the serial
// run spent its whole job budget on the corpus leg and published nothing,
// twice. These are the guards on the two halves of that change - the estate
// list the matrix is built from, and the command that combines the shards
// back into one artifact.

const (
	shardCommit   = "1111111111111111111111111111111111111111"
	shardDate     = "2026-09-23T00:00:00Z"
	shardEmulator = "ghcr.io/lex00/floci@sha256:aaaa"
)

// shardTestManifest is a manifest with one estate of every shape the
// selection has to get right: two core, one growing on the emulator, and
// one growing in the kubernetes lane (which runs on kind, is in neither
// AWS set, and is measured by the nightly all the same).
func shardTestManifest() []Estate {
	return []Estate{
		{Name: "alpha", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "beta", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "gamma", Source: "s", URL: "u", Pin: "p", Lane: "published-deployment", Set: SetGrowing},
		{Name: "kube", Source: "s", Lane: LaneKubernetes, Set: SetGrowing},
	}
}

func shardTestRoot(t *testing.T, estates []Estate) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(ManifestPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(root, &Manifest{Estates: estates}); err != nil {
		t.Fatal(err)
	}
	return root
}

// shardBase is the committed artifact the shards start from: every row
// present, nothing measured.
func shardBase(t *testing.T, root string) *Artifact {
	t.Helper()
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	a := &Artifact{}
	a.Rebuild(m, nil, shardEmulator, OracleVersions{})
	return a
}

func copyArtifact(t *testing.T, a *Artifact) *Artifact {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var cp Artifact
	if err := json.Unmarshal(b, &cp); err != nil {
		t.Fatal(err)
	}
	return &cp
}

// measure writes into a the row a `gauntlet run <estate>` would have left:
// every headline stage passing, with per-stage provenance naming this run,
// so the verdicts tally as pass rather than as carried (#1069).
func measure(t *testing.T, root string, a *Artifact, estate, commit, emulator string) {
	t.Helper()
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range a.Estates {
		if a.Estates[i].Name != estate {
			continue
		}
		found = true
		r := &a.Estates[i]
		r.Protocol = ProtocolGauntlet
		r.Stages = stagesAllPass()
		r.StageRuns = map[string]StageRun{}
		for id := range r.Stages {
			r.StageRuns[id] = StageRun{Commit: commit, Date: shardDate}
		}
		r.LastRun = &LastRun{Commit: commit, Date: shardDate, Emulator: emulator, DurationS: 1}
	}
	if !found {
		t.Fatalf("no row for %q to measure", estate)
	}
	a.Rebuild(m, nil, shardEmulator, OracleVersions{})
}

// shardOf is one shard job's uploaded artifact: base, with its own estate
// measured.
func shardOf(t *testing.T, root string, base *Artifact, estate string) ShardArtifact {
	t.Helper()
	a := copyArtifact(t, base)
	measure(t, root, a, estate, shardCommit, shardEmulator)
	return ShardArtifact{Estate: estate, Path: "shard-" + estate + ".json", Artifact: a}
}

// TestShardEstatesCoversEveryEstateTheRunMeasures: the matrix is computed
// from the manifest so an estate added to the corpus cannot silently drop
// out of the board. The wanted set is walked out of the fixture manifest
// rather than retyped, so adding an estate to shardTestManifest fails this
// until ShardEstates carries it.
//
// The kubernetes lane is the case the obvious implementation gets wrong: a
// lane estate is `growing`, so a plain set filter drops all four from a
// `-set core` nightly - and the nightly measures them today, in a separate
// hand-written step (TestKubernetesLaneRunsNightly, live/k8s_ci_test.go).
func TestShardEstatesCoversEveryEstateTheRunMeasures(t *testing.T) {
	m := &Manifest{Estates: shardTestManifest()}
	if err := m.Validate(); err != nil {
		t.Fatalf("the fixture manifest is not valid: %v", err)
	}

	for _, set := range []string{SetCore, "all"} {
		got := map[string]bool{}
		for _, n := range ShardEstates(m, set) {
			if got[n] {
				t.Errorf("-set %s lists %q twice; a duplicated matrix entry runs one estate in two jobs and makes two rows out of one measurement", set, n)
			}
			got[n] = true
		}
		for _, e := range m.Estates {
			want := set == "all" || e.Set == SetCore || e.Lane == LaneKubernetes
			if want && !got[e.Name] {
				t.Errorf("-set %s does not list %q (set=%s lane=%s); an estate the matrix never names is an estate the board stops measuring", set, e.Name, e.Set, e.Lane)
			}
			if !want && got[e.Name] {
				t.Errorf("-set %s lists %q (set=%s lane=%s), which a `gauntlet run -set %s` would not run", set, e.Name, e.Set, e.Lane, set)
			}
		}
	}
}

// TestShardEstatesMatchesWhatRunEstatesWouldSelect holds the list to the
// runner's own selection rather than to a second copy of the rule: for the
// emulator sets, every name ShardEstates yields that is not in the
// kubernetes lane must be one `gauntlet run -set <set>` would have run,
// and vice versa.
func TestShardEstatesMatchesWhatRunEstatesWouldSelect(t *testing.T) {
	m := &Manifest{Estates: shardTestManifest()}
	for _, set := range []string{SetCore, "all"} {
		runner := map[string]bool{}
		for _, e := range m.Estates {
			if set == SetCore && e.Set != SetCore {
				continue
			}
			runner[e.Name] = true
		}
		for _, n := range ShardEstates(m, set) {
			e, ok := m.ByName(n)
			if !ok {
				t.Errorf("-set %s lists %q, which is not in the manifest", set, n)
				continue
			}
			if !runner[n] && e.Lane != LaneKubernetes {
				t.Errorf("-set %s lists %q, which `gauntlet run -set %s` would not select and which is not a lane estate", set, n, set)
			}
		}
		for n := range runner {
			if !contains(ShardEstates(m, set), n) {
				t.Errorf("`gauntlet run -set %s` would run %q and the matrix does not list it", set, n)
			}
		}
	}
}

// TestCombineShardsAggregatesEqualASerialRun is the claim the whole change
// rests on: the combined artifact is what one serial run of the same
// estates would have written. Aggregates are compared, not just rows,
// because the aggregate is the value that has silently contradicted its own
// rows before (live/GAUNTLET.md, "Merging estate rows across PRs").
func TestCombineShardsAggregatesEqualASerialRun(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)

	shards := []ShardArtifact{shardOf(t, root, base, "alpha"), shardOf(t, root, base, "kube")}
	got, err := CombineShards(root, base, shards, []string{"alpha", "kube"}, shardCommit, shardEmulator)
	if err != nil {
		t.Fatalf("CombineShards: %v", err)
	}

	// What one serial `gauntlet run alpha kube` would have produced: the
	// same two rows written into the same base, rebuilt once.
	want := copyArtifact(t, base)
	measure(t, root, want, "alpha", shardCommit, shardEmulator)
	measure(t, root, want, "kube", shardCommit, shardEmulator)

	if !reflect.DeepEqual(got.Sets, want.Sets) {
		t.Errorf("combined sets:\n got %+v\nwant %+v", got.Sets, want.Sets)
	}
	if !reflect.DeepEqual(got.Lanes, want.Lanes) {
		t.Errorf("combined lanes:\n got %+v\nwant %+v", got.Lanes, want.Lanes)
	}
	if !reflect.DeepEqual(got.Estates, want.Estates) {
		t.Errorf("combined rows differ from the serial run's rows")
	}
	// And the headline cannot contradict its own rows.
	clear := 0
	for _, r := range got.Estates {
		if r.Clear && r.Substrate == "" && r.Set == SetCore {
			clear++
		}
	}
	if got.Sets[SetCore].Clear != clear {
		t.Errorf("sets.core.clear = %d, rows say %d", got.Sets[SetCore].Clear, clear)
	}
	if got.Lanes[LaneKubernetes].Clear != 1 {
		t.Errorf("lanes.kubernetes.clear = %d, want 1 (the shard measured it)", got.Lanes[LaneKubernetes].Clear)
	}
}

func combineErr(t *testing.T, root string, base *Artifact, shards []ShardArtifact, expect []string) string {
	t.Helper()
	_, err := CombineShards(root, base, shards, expect, shardCommit, shardEmulator)
	if err == nil {
		t.Fatal("CombineShards accepted this; it must refuse rather than guess")
	}
	return err.Error()
}

// TestCombineShardsRefusesAnEmulatorDisagreement: two shards that ran
// against different emulator images are two measurements, not one board.
func TestCombineShardsRefusesAnEmulatorDisagreement(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")
	b := shardOf(t, root, base, "beta")
	other := copyArtifact(t, base)
	measure(t, root, other, "beta", shardCommit, "ghcr.io/lex00/floci@sha256:bbbb")
	b.Artifact = other

	msg := combineErr(t, root, base, []ShardArtifact{a, b}, []string{"alpha", "beta"})
	for _, want := range []string{"beta", "sha256:bbbb", shardEmulator} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
}

// TestCombineShardsRefusesACommitDisagreement: the shards must all be
// measuring the same tree. A shard left over from an earlier run reads
// exactly like a fresh one except for this.
func TestCombineShardsRefusesACommitDisagreement(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")
	stale := copyArtifact(t, base)
	measure(t, root, stale, "beta", "2222222222222222222222222222222222222222", shardEmulator)
	b := ShardArtifact{Estate: "beta", Path: "shard-beta.json", Artifact: stale}

	msg := combineErr(t, root, base, []ShardArtifact{a, b}, []string{"alpha", "beta"})
	for _, want := range []string{"beta", "2222222222222222222222222222222222222222", shardCommit} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
}

// TestCombineShardsRefusesAMissingShard: a job that died cannot quietly
// shrink the board, and the refusal names the estate so the one job to
// re-run is obvious.
func TestCombineShardsRefusesAMissingShard(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")

	msg := combineErr(t, root, base, []ShardArtifact{a}, []string{"alpha", "beta", "kube"})
	for _, want := range []string{"beta", "kube"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name the missing estate %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "alpha") {
		t.Errorf("refusal names alpha, whose shard is present: %s", msg)
	}
}

// TestCombineShardsRefusesAForeignRow: a shard may only contribute the
// estate it ran. A shard carrying somebody else's row is a shard that ran
// something the collect job cannot account for.
func TestCombineShardsRefusesAForeignRow(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")
	measure(t, root, a.Artifact, "gamma", shardCommit, shardEmulator)
	b := shardOf(t, root, base, "beta")

	msg := combineErr(t, root, base, []ShardArtifact{a, b}, []string{"alpha", "beta"})
	for _, want := range []string{"alpha", "gamma"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not name %q: %s", want, msg)
		}
	}
}

// TestCombineShardsRefusesAShardThatNeverRan: a shard cancelled before its
// estate ran uploads base's own untouched row, which carries no evidence at
// all. Taking it would publish "measured" for an estate nothing measured.
func TestCombineShardsRefusesAShardThatNeverRan(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")
	b := ShardArtifact{Estate: "beta", Path: "shard-beta.json", Artifact: copyArtifact(t, base)}

	msg := combineErr(t, root, base, []ShardArtifact{a, b}, []string{"alpha", "beta"})
	if !strings.Contains(msg, "beta") {
		t.Errorf("refusal does not name beta: %s", msg)
	}
}

// TestCombineShardsRefusesAnUnexpectedShard: a shard for an estate this run
// was not measuring means the matrix and the collect job disagree about
// what the board is.
func TestCombineShardsRefusesAnUnexpectedShard(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	a := shardOf(t, root, base, "alpha")
	g := shardOf(t, root, base, "gamma")

	msg := combineErr(t, root, base, []ShardArtifact{a, g}, []string{"alpha"})
	if !strings.Contains(msg, "gamma") {
		t.Errorf("refusal does not name gamma: %s", msg)
	}
}

// TestLoadShardArtifactsTreatsAMissingDirectoryAsNoShards: issue #1563.
// actions/download-artifact never creates its destination directory when
// the pattern it was given matches zero artifacts (this is exactly what
// happened on 2026-09-24/25/26, runs 35975528292/36115165483/36230331089:
// the estate and acceptance jobs were skipped every night - #1563's own
// root cause, a separate bug in the workflow - so zero shards were ever
// uploaded). combine-shards then called LoadShardArtifacts on a path that
// plain does not exist, and WalkDir's first callback fired with that
// lstat error and nothing turned it into a refusal: "gauntlet: lstat
// shards: no such file or directory" on stderr, exit 1, no estate named,
// no hint to re-run anything. An existing-but-empty directory (a shard job
// that ran and uploaded nothing) already came back as zero shards with no
// error - a missing directory is the same fact from the collect job's
// side and must read the same way, so CombineShards's own "no shard for
// <names>" refusal gets the chance to fire instead of a raw path error.
func TestLoadShardArtifactsTreatsAMissingDirectoryAsNoShards(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	got, err := LoadShardArtifacts(dir)
	if err != nil {
		t.Fatalf("LoadShardArtifacts(%q) = %v, want no error - a directory download-artifact never created holds zero shards, not a failure", dir, err)
	}
	if len(got) != 0 {
		t.Fatalf("LoadShardArtifacts(%q) = %v, want zero shards", dir, got)
	}
}

// TestCombineShardsRefusesCleanlyWhenNoShardsUploaded reproduces #1563's
// collect-job failure end to end: every estate this run expected a shard
// for, and none uploaded (the shards directory does not exist), must
// refuse by naming every missing estate - the same shape as a partial
// miss (TestCombineShardsRefusesAMissingShard) - rather than crash on the
// directory read before CombineShards ever sees the list.
func TestCombineShardsRefusesCleanlyWhenNoShardsUploaded(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	dir := filepath.Join(t.TempDir(), "shards")

	shards, err := LoadShardArtifacts(dir)
	if err != nil {
		t.Fatalf("LoadShardArtifacts(%q) = %v, want no error", dir, err)
	}

	msg := combineErr(t, root, base, shards, []string{"alpha", "beta", "kube"})
	for _, want := range []string{"alpha", "beta", "kube", "no shard for"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q: %s", want, msg)
		}
	}
}

// TestLoadShardArtifactsReadsTheUploadedLayout: download-artifact puts each
// shard in its own directory, so the loader walks, and two files claiming
// the same estate are a refusal rather than a coin flip.
func TestLoadShardArtifactsReadsTheUploadedLayout(t *testing.T) {
	root := shardTestRoot(t, shardTestManifest())
	base := shardBase(t, root)
	dir := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		sub := filepath.Join(dir, "gauntlet-shard-"+name)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		s := shardOf(t, root, base, name)
		b, err := json.Marshal(s.Artifact)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, ShardFilePrefix+name+".json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
		// A log file beside it, exactly as the shard job uploads.
		if err := os.WriteFile(filepath.Join(sub, "gauntlet-run-"+name+".log"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadShardArtifacts(dir)
	if err != nil {
		t.Fatalf("LoadShardArtifacts: %v", err)
	}
	if len(got) != 2 || got[0].Estate != "alpha" || got[1].Estate != "beta" {
		t.Fatalf("got %d shards %v, want alpha and beta", len(got), got)
	}

	// The same estate twice: which one is the measurement?
	sub := filepath.Join(dir, "gauntlet-shard-alpha-retry")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(shardOf(t, root, base, "alpha").Artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ShardFilePrefix+"alpha.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadShardArtifacts(dir); err == nil {
		t.Error("two files claim estate alpha and the loader picked one; it must refuse")
	} else if !strings.Contains(err.Error(), "alpha") {
		t.Errorf("refusal does not name alpha: %v", err)
	}
}

// TestShardEstatesCarriesTheKubernetesLaneOfThisRepository is the half of
// TestKubernetesLaneRunsNightly (live/k8s_ci_test.go) that moved here when
// the nightly stopped naming the lane's four estates by hand: against THIS
// repository's manifest, a `-set core` run - what the nightly dispatches -
// must still measure every kubernetes-lane estate, or a lane row goes stale
// against every repin with nothing re-measuring it.
func TestShardEstatesCarriesTheKubernetesLaneOfThisRepository(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	nightly := ShardEstates(m, SetCore)
	lane := 0
	for _, e := range m.Estates {
		if e.Lane != LaneKubernetes {
			continue
		}
		lane++
		if !contains(nightly, e.Name) {
			t.Errorf("a `-set core` run does not measure %q (lane %s, set %s)", e.Name, e.Lane, e.Set)
		}
	}
	if lane == 0 {
		t.Fatal("the manifest has no kubernetes-lane estate; this guard is checking nothing")
	}
	// And every core estate, which is the other thing the nightly is for.
	core := 0
	for _, e := range m.Estates {
		if e.Set != SetCore {
			continue
		}
		core++
		if !contains(nightly, e.Name) {
			t.Errorf("a `-set core` run does not measure core estate %q", e.Name)
		}
	}
	if len(nightly) != core+lane {
		t.Errorf("a `-set core` run measures %d estates; the manifest has %d core and %d lane estates", len(nightly), core, lane)
	}
}
