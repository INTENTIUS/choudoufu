// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Issue #1550, second act: the board is measured one estate per CI job.
//
// The serial run measured every estate and published nothing, twice - the
// corpus leg alone runs over four hours, so the job hit its own limit
// before Render and the verdicts pull request, and about fifteen hours of
// CI left live/gauntlet.json untouched. #1554 raised the limit, which buys
// time and changes nothing structural. This file is the structural half:
// every estate runs in its own job, in parallel, and a collecting job folds
// the shards back into one artifact.
//
// Two commands live here, and the split matters.
//
// ShardEstates answers "which estates does this run measure", from the
// MANIFEST, so the workflow's matrix cannot silently disagree with the
// corpus. It is not the same question as `gauntlet run -set core`: the
// nightly also measures the kubernetes lane, today through a second,
// hand-written step naming its four estates (TestKubernetesLaneRunsNightly,
// live/k8s_ci_test.go), and a hand-written list is exactly what an estate
// drops out of. The lane is folded in here instead, and the same function
// backs both the matrix and the collect job's expectation, so a name can
// never be in one and not the other.
//
// CombineShards is a sibling of MergeArtifact (mergeartifact.go) and
// deliberately NOT the same command. MergeArtifact merges two BRANCHES,
// each measured against a possibly different tree, which is why it refuses
// when product code moved between them. The shards of one run are the
// opposite case: one commit, one emulator pin, N jobs, and the danger is
// not divergent code but a job that died, a stale upload, or a shard
// contributing a row it never measured. So it refuses on those instead,
// and it never computes an aggregate - Rebuild does, the same call `run`
// and `render` make, so the headline cannot contradict its own rows.

// ShardFilePrefix is the naming convention one shard's uploaded copy of the
// artifact follows: shard-<estate>.json, beside that shard's own logs. The
// estate name in the filename is what the shard job was told to run
// (${{ matrix.estate }}), and CombineShards holds the file's contents to
// that claim rather than trusting it.
const ShardFilePrefix = "shard-"

// ShardArtifact is one shard's uploaded artifact plus the estate the job
// that produced it was given.
type ShardArtifact struct {
	Estate   string
	Path     string
	Artifact *Artifact
}

// ShardEstates is the estate list one gauntlet CI run measures for set, in
// name order: exactly what `gauntlet run -set <set>` selects (run.go), plus
// every kubernetes-lane estate, which the nightly measures on a kind
// cluster whatever the set is. Deduplicated, because `-set all` already
// contains the lane and an estate named twice would be run twice and turn
// one measurement into two rows.
func ShardEstates(m *Manifest, set string) []string {
	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		names = append(names, n)
	}
	for _, e := range m.Estates {
		// The runner's own selection rule, spelled the same way
		// (RunEstates, run.go).
		if set == SetCore && e.Set != SetCore {
			continue
		}
		add(e.Name)
	}
	for _, e := range m.Estates {
		if e.Lane == LaneKubernetes {
			add(e.Name)
		}
	}
	sort.Strings(names)
	return names
}

// LoadShardArtifacts reads every shard-<estate>.json under dir, at any
// depth: actions/download-artifact gives each shard a directory of its own,
// so the files arrive as
// <dir>/gauntlet-shard-<estate>/shard-<estate>.json beside that shard's
// logs.
//
// Two files claiming the same estate is a refusal, not a last-one-wins:
// they are two measurements of the same estate and nothing here can say
// which is current. That is the same rule MergeArtifact applies to an
// estate changed on both sides.
func LoadShardArtifacts(dir string) ([]ShardArtifact, error) {
	byEstate := map[string]string{}
	var out []ShardArtifact
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, ShardFilePrefix) || !strings.HasSuffix(name, ".json") {
			return nil
		}
		estate := strings.TrimSuffix(strings.TrimPrefix(name, ShardFilePrefix), ".json")
		if estate == "" {
			return fmt.Errorf("combine-shards: %s names no estate", p)
		}
		if first, dup := byEstate[estate]; dup {
			return fmt.Errorf("combine-shards: refusing - two shard files claim estate %q (%s and %s); they are two measurements and nothing here can say which is current, so re-run that estate's job", estate, first, p)
		}
		byEstate[estate] = p
		a, err := loadArtifactFile(p)
		if err != nil {
			return err
		}
		out = append(out, ShardArtifact{Estate: estate, Path: p, Artifact: a})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Estate < out[j].Estate })
	return out, nil
}

// CombineShards folds every shard's own row into base and rebuilds.
//
// expect is the estate list the run was supposed to measure (ShardEstates,
// or the explicit names a dispatch asked for); commit and emulator are the
// tree and the pin every shard must have measured against. Each is checked
// rather than inferred from the shards themselves: shards that all agree
// with each other and disagree with the checkout are a set of stale
// uploads, and they read identically to a fresh run from the inside.
//
// Every failure is a refusal that names the estate, because every one of
// them has the same fix - re-run that estate's job, which is minutes now
// that a job is one estate - and because the alternative is a board that
// quietly describes fewer estates, or another tree, than it claims to.
func CombineShards(root string, base *Artifact, shards []ShardArtifact, expect []string, commit, emulator string) (*Artifact, error) {
	if len(expect) == 0 {
		return nil, fmt.Errorf("combine-shards: no estates were expected, so there is nothing to combine and nothing to check against")
	}
	m, err := LoadManifest(root)
	if err != nil {
		return nil, err
	}
	bi, err := LoadBehaviorIndex(root)
	if err != nil {
		return nil, err
	}

	wanted := map[string]bool{}
	for _, n := range expect {
		wanted[n] = true
	}

	// The comparison base is base REBUILT against this tree's manifest and
	// pin, because that is what each shard's own artifact came out of
	// (cmdRun calls Rebuild before writing it). Comparing a shard's
	// untouched rows against a base that had not been through the same
	// function would report every derived field as a difference.
	cmp, err := rebuiltCopy(base, m, bi, emulator, oracleVersions(root))
	if err != nil {
		return nil, err
	}
	baseRows := indexRows(cmp.Estates)

	got := map[string]EstateResult{}
	for _, s := range shards {
		if _, ok := m.ByName(s.Estate); !ok {
			return nil, fmt.Errorf("combine-shards: refusing - shard %s is for estate %q, which is not in %s", s.Path, s.Estate, ManifestPath)
		}
		if !wanted[s.Estate] {
			return nil, fmt.Errorf("combine-shards: refusing - shard %s is for estate %q, which this run was not measuring (expected: %s); the matrix and the collect job disagree about what the board is", s.Path, s.Estate, strings.Join(expect, ", "))
		}
		if _, dup := got[s.Estate]; dup {
			return nil, fmt.Errorf("combine-shards: refusing - estate %q has two shards; re-run that estate's job", s.Estate)
		}
		row, ok := indexRows(s.Artifact.Estates)[s.Estate]
		if !ok {
			return nil, fmt.Errorf("combine-shards: refusing - shard %s carries no row for estate %q, the estate its job was given", s.Path, s.Estate)
		}
		if row.LastRun == nil {
			return nil, fmt.Errorf("combine-shards: refusing - estate %q's shard (%s) carries no last_run, so that job never recorded a run; re-run it", s.Estate, s.Path)
		}
		if row.LastRun.Commit != commit {
			return nil, fmt.Errorf("combine-shards: refusing - estate %q was measured at commit %s and this run is %s (%s); a shard from another run is not evidence about this one", s.Estate, orNone(row.LastRun.Commit), commit, s.Path)
		}
		if row.LastRun.Emulator != emulator {
			return nil, fmt.Errorf("combine-shards: refusing - estate %q was measured against emulator %s and this run pins %s (%s); rows measured against different images are not one board", s.Estate, orNone(row.LastRun.Emulator), emulator, s.Path)
		}
		if err := checkShardContributesOnlyItsOwn(s, baseRows, m, bi, emulator, oracleVersions(root)); err != nil {
			return nil, err
		}
		got[s.Estate] = row
	}

	var missing []string
	for _, n := range expect {
		if _, ok := got[n]; !ok {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("combine-shards: refusing - no shard for %s; a job that died cannot shrink the board silently, so re-run those jobs (the rest of this run's shards are kept)", strings.Join(missing, ", "))
	}

	rows := make([]EstateResult, 0, len(baseRows))
	for name, r := range baseRows {
		if s, ok := got[name]; ok {
			r = s
		}
		rows = append(rows, r)
	}
	// A shard for an estate the base artifact has no row for yet (a newly
	// added estate) still contributes.
	for name, r := range got {
		if _, ok := baseRows[name]; !ok {
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	if err := checkExitFailShape(rows); err != nil {
		return nil, err
	}

	out := &Artifact{Estates: rows, LiveCert: base.LiveCert}
	out.Rebuild(m, bi, emulator, oracleVersions(root))
	return out, nil
}

// checkShardContributesOnlyItsOwn holds a shard to the one estate its job
// was given: every OTHER row in its artifact has to be the base's row,
// untouched.
//
// A shard whose file name and contents disagree - a job that uploaded the
// wrong file, or a `gauntlet run` given more names than the matrix entry -
// would otherwise contribute a measurement nothing in this run accounts
// for, and it would look exactly like a row the collect job had decided to
// keep.
func checkShardContributesOnlyItsOwn(s ShardArtifact, baseRows map[string]EstateResult, m *Manifest, bi *BehaviorIndex, emulator string, oracle OracleVersions) error {
	// The shard's own artifact goes through Rebuild too, for the same
	// reason the base did: like compared with like.
	cmp, err := rebuiltCopy(s.Artifact, m, bi, emulator, oracle)
	if err != nil {
		return err
	}
	for _, r := range cmp.Estates {
		if r.Name == s.Estate {
			continue
		}
		b, ok := baseRows[r.Name]
		if rowChanged(ok, b, true, r) {
			return fmt.Errorf("combine-shards: refusing - estate %q's shard (%s) also carries a changed row for %q, which it was not asked to run; a shard contributes the one estate its job measured", s.Estate, s.Path, r.Name)
		}
	}
	for name := range baseRows {
		if name == s.Estate {
			continue
		}
		found := false
		for _, r := range cmp.Estates {
			if r.Name == name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("combine-shards: refusing - estate %q's shard (%s) dropped the row for %q; a shard contributes the one estate its job measured and changes nothing else", s.Estate, s.Path, name)
		}
	}
	return nil
}

// rebuiltCopy is a deep copy of a with every derived field recomputed -
// through JSON, so the copy shares no map with the original (EstateResult
// carries two, and Rebuild writes into them in place).
func rebuiltCopy(a *Artifact, m *Manifest, bi *BehaviorIndex, emulator string, oracle OracleVersions) (*Artifact, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var cp Artifact
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, err
	}
	cp.Rebuild(m, bi, emulator, oracle)
	return &cp, nil
}
