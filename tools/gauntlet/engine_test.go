// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsEngineStaleOnADifferentBase is #1778 ruling 6's rule (a): a row
// measured on engine base A reads engine-stale once the current base is B,
// and current while the two agree.
func TestIsEngineStaleOnADifferentBase(t *testing.T) {
	r := EstateResult{Name: "x", LastRun: &LastRun{Commit: "c", UpstreamVersion: "1.13.0-dev"}}
	if IsEngineStale(r, "1.13.0-dev") {
		t.Errorf("a row measured on 1.13.0-dev must not read engine-stale while the base is 1.13.0-dev")
	}
	if !IsEngineStale(r, "1.13.0") {
		t.Errorf("a row measured on 1.13.0-dev must read engine-stale once the base is 1.13.0")
	}
	r.LastRun.UpstreamVersion = "1.13.0"
	if IsEngineStale(r, "1.13.0") {
		t.Errorf("a row measured on 1.13.0 must not read engine-stale while the base is 1.13.0")
	}
	if !IsEngineStale(r, "1.13.1") {
		t.Errorf("a row measured on 1.13.0 must read engine-stale once the base is 1.13.1")
	}
}

// TestEngineStaleMissingFieldIsUnknownUntilTheBaseMoves is rule (b): a row
// written before LastRun.UpstreamVersion existed was measured on
// LegacyEngineBase, so it reads as unknown (not stale) while that is still
// the base, and stale once the base has moved. A row with no last_run is
// never engine-stale, the same carve-out IsStale makes.
func TestEngineStaleMissingFieldIsUnknownUntilTheBaseMoves(t *testing.T) {
	missing := EstateResult{Name: "old", LastRun: &LastRun{Commit: "c"}}
	if IsEngineStale(missing, LegacyEngineBase) {
		t.Errorf("a row with no upstream_version must not read engine-stale while the base is %s", LegacyEngineBase)
	}
	if !IsEngineStale(missing, "1.13.0") {
		t.Errorf("a row with no upstream_version must read engine-stale once the base is 1.13.0")
	}
	if IsEngineStale(missing, "") {
		t.Errorf("an unreadable engine pin is no evidence the base moved; a row with no upstream_version must not read stale against it")
	}
	if IsEngineStale(EstateResult{Name: "never"}, "1.13.0") {
		t.Errorf("a row with no last_run must not read engine-stale")
	}
}

// TestRunEstatesRecordsUpstreamVersion is rule (c): RunEstates stamps every
// row it touches with the engine base its binary reported.
func TestRunEstatesRecordsUpstreamVersion(t *testing.T) {
	orig := engineVersionProbe
	defer func() { engineVersionProbe = orig }()
	calls := 0
	engineVersionProbe = func(root string, env []string) string {
		calls++
		return "1.13.0"
	}

	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "z", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass duration_s=0\\n'\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manifest{Estates: []Estate{
		{Name: "z", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath},
		{Name: "y", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath},
	}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"z", "y"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"z", "y"} {
		r, ok := a.Result(name)
		if !ok || r.LastRun == nil {
			t.Fatalf("no last_run for %s", name)
		}
		if r.LastRun.UpstreamVersion != "1.13.0" {
			t.Errorf("%s: LastRun.UpstreamVersion = %q, want the probed %q", name, r.LastRun.UpstreamVersion, "1.13.0")
		}
	}
	if calls != 1 {
		t.Errorf("engine probed %d times; one RunEstates call runs one binary and should probe it once", calls)
	}
}

// TestProbeEngineAsksTheBinaryInTOFU_BIN: with TOFU_BIN set (how CI runs
// every estate), probeEngine runs that binary's `version -json` and records
// what it says, not the tree's pin.
func TestProbeEngineAsksTheBinaryInTOFU_BIN(t *testing.T) {
	root := t.TempDir()
	writeEngineVersionPin(t, root, "1.13.0\n")
	stub := filepath.Join(t.TempDir(), "choudoufu")
	body := "#!/usr/bin/env bash\n" +
		"[ \"$1 $2\" = \"version -json\" ] || exit 2\n" +
		"printf '{\"choudoufu_version\": \"\", \"terraform_version\": \"1.12.0\", \"platform\": \"linux_amd64\", \"provider_selections\": {}}\\n'\n"
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := probeEngine(root, []string{"TOFU_BIN=" + stub}); got != "1.12.0" {
		t.Errorf("probeEngine = %q, want the binary's own %q", got, "1.12.0")
	}
}

// TestReconcileEngineVersion: a development build prints `-dev` whatever
// version/VERSION says (version/version.go sets Prerelease to "dev" unless
// linked with dev=no), so after the upgrade a dev build of a 1.13.0 tree
// prints 1.13.0-dev - the same string the pre-upgrade engine printed. The
// binary is believed for the core version; the tree it was built from
// settles the one bit a dev build erases.
func TestReconcileEngineVersion(t *testing.T) {
	cases := []struct{ reported, tree, want string }{
		{"1.13.0-dev", "1.13.0-dev", "1.13.0-dev"}, // today
		{"1.13.0-dev", "1.13.0", "1.13.0"},         // dev build after the upgrade
		{"1.13.0", "1.13.0", "1.13.0"},             // release build
		{"1.12.0-dev", "1.13.0", "1.12.0-dev"},     // a binary from another base: believed
		{"1.13.0-dev", "", "1.13.0-dev"},           // no pin to consult
		{"", "1.13.0", ""},                         // the binary said nothing: nothing recorded
	}
	for _, c := range cases {
		if got := reconcileEngineVersion(c.reported, c.tree); got != c.want {
			t.Errorf("reconcileEngineVersion(%q, %q) = %q, want %q", c.reported, c.tree, got, c.want)
		}
	}
}

// TestEngineVersionReadsThePin: engineVersion reads version/VERSION,
// trimmed, and a missing file is the zero value.
func TestEngineVersionReadsThePin(t *testing.T) {
	root := t.TempDir()
	if got := engineVersion(root); got != "" {
		t.Errorf("engineVersion with no pin = %q, want empty", got)
	}
	writeEngineVersionPin(t, root, "1.13.0-dev\n")
	if got := engineVersion(root); got != "1.13.0-dev" {
		t.Errorf("engineVersion = %q, want %q", got, "1.13.0-dev")
	}
}

// TestRebuildSetsArtifactUpstreamVersion: a.UpstreamVersion is Rebuild's
// engine argument, refreshed on every call like a.Providers.
func TestRebuildSetsArtifactUpstreamVersion(t *testing.T) {
	m := &Manifest{Estates: []Estate{{Name: "a", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}}
	a := &Artifact{}
	a.Rebuild(m, nil, "img", OracleVersions{}, ProviderVersions{}, "1.13.0-dev")
	if a.UpstreamVersion != "1.13.0-dev" {
		t.Errorf("a.UpstreamVersion = %q after Rebuild, want the passed-in value", a.UpstreamVersion)
	}
	a.Rebuild(m, nil, "img", OracleVersions{}, ProviderVersions{}, "1.13.0")
	if a.UpstreamVersion != "1.13.0" {
		t.Errorf("a.UpstreamVersion = %q after a second Rebuild, want the refreshed value", a.UpstreamVersion)
	}
}

// TestNextSurfacesEngineStaleEstates: a clear estate measured on an engine
// base the tree has since left is trailing work, the same as a stale
// emulator or provider pin, and a pre-field row becomes work only once the
// base moves.
func TestNextSurfacesEngineStaleEstates(t *testing.T) {
	active := HeadlineStages()
	m := &Manifest{Estates: []Estate{
		{Name: "c-fresh", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-old-engine", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
		{Name: "c-unrecorded", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"},
	}}
	build := func(base string) []string {
		a := &Artifact{}
		a.Rebuild(m, nil, "pin", OracleVersions{}, ProviderVersions{AWS: "6.63.0"}, base)
		for _, e := range m.Estates {
			r, _ := a.Result(e.Name)
			for _, s := range active {
				r.Stages[s.ID] = VerdictPass
			}
			r.LastRun = &LastRun{Commit: "x", Date: "2020-01-01T00:00:00Z", Emulator: "pin", AWSProviderVersion: "6.63.0"}
			switch e.Name {
			case "c-fresh":
				r.LastRun.UpstreamVersion = base
			case "c-old-engine":
				r.LastRun.UpstreamVersion = "1.12.0-dev"
			}
			a.SetResult(r)
		}
		a.Rebuild(m, nil, "pin", OracleVersions{}, ProviderVersions{AWS: "6.63.0"}, base)
		var ids []string
		for _, u := range NextUnits(a, "all", "") {
			ids = append(ids, u.ID)
			if u.Estate == "c-old-engine" && !strings.Contains(u.Detail, "1.12.0-dev") {
				t.Errorf("the stale_pin unit should name the engine it was measured on; got %q", u.Detail)
			}
		}
		return ids
	}
	got := strings.Join(build(LegacyEngineBase), ",")
	if got != "c-old-engine/stale_pin" {
		t.Errorf("base %s: units = %q, want only c-old-engine/stale_pin", LegacyEngineBase, got)
	}
	got = strings.Join(build("1.13.0"), ",")
	if got != "c-old-engine/stale_pin,c-unrecorded/stale_pin" {
		t.Errorf("base 1.13.0: units = %q, want c-old-engine and c-unrecorded stale", got)
	}
}

// TestBoardEstateEngineNote: the estate page names the engine base its run
// used beside the oracle and provider notes, with **Stale** when
// IsEngineStale says so, and an unrecorded base reads as unknown rather than
// stale until the base moves.
func TestBoardEstateEngineNote(t *testing.T) {
	a := &Artifact{UpstreamVersion: "1.13.0", Stages: Stages()}
	r := EstateResult{Name: "x", Protocol: ProtocolGauntlet, Stages: map[string]string{}}
	if note := boardEstate(r, a, ScriptStaleness{}, "").EngineNote; note != "" {
		t.Errorf("no last_run: note should be empty, got %q", note)
	}
	r.LastRun = &LastRun{Commit: "c", Date: "d", UpstreamVersion: "1.13.0"}
	if note := boardEstate(r, a, ScriptStaleness{}, "").EngineNote; !strings.Contains(note, "`1.13.0`") || !strings.Contains(note, "matches the current base") {
		t.Errorf("expected a matching-engine note; got %q", note)
	}
	r.LastRun.UpstreamVersion = "1.13.0-dev"
	if note := boardEstate(r, a, ScriptStaleness{}, "").EngineNote; !strings.Contains(note, "**Stale**") || !strings.Contains(note, "`1.13.0-dev`") {
		t.Errorf("expected a stale-engine note naming both bases; got %q", note)
	}
	r.LastRun.UpstreamVersion = ""
	if note := boardEstate(r, a, ScriptStaleness{}, "").EngineNote; !strings.Contains(note, "**Stale**") {
		t.Errorf("an unrecorded base after the base moved must read stale; got %q", note)
	}
	a.UpstreamVersion = LegacyEngineBase
	if note := boardEstate(r, a, ScriptStaleness{}, "").EngineNote; strings.Contains(note, "**Stale**") || !strings.Contains(note, "not recorded") {
		t.Errorf("an unrecorded base while the base is %s must read unknown, not stale; got %q", LegacyEngineBase, note)
	}
}

func writeEngineVersionPin(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "version", "VERSION"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEngineBannerCountsEngineStaleRows: the board-wide sentence counts the
// rows IsEngineStale marks - the same rule `gauntlet next` enqueues by - and
// keeps pre-field rows in their own bucket while they read unknown.
func TestEngineBannerCountsEngineStaleRows(t *testing.T) {
	a := &Artifact{UpstreamVersion: "1.13.0", Estates: []EstateResult{
		{Name: "now", LastRun: &LastRun{UpstreamVersion: "1.13.0"}},
		{Name: "old", LastRun: &LastRun{UpstreamVersion: "1.13.0-dev"}},
		{Name: "pre", LastRun: &LastRun{}},
		{Name: "never"},
	}}
	got := engineBanner(a)
	for _, want := range []string{"`1.13.0`", "Of the 3 estates measured so far", "1 recorded running on it", "2 are engine-stale"} {
		if !strings.Contains(got, want) {
			t.Errorf("engineBanner = %q, want it to contain %q", got, want)
		}
	}
	a.UpstreamVersion = LegacyEngineBase
	a.Estates[0].LastRun.UpstreamVersion = LegacyEngineBase
	got = engineBanner(a)
	for _, want := range []string{"2 recorded running on it", "1 predate `last_run.upstream_version`"} {
		if !strings.Contains(got, want) {
			t.Errorf("engineBanner = %q, want it to contain %q", got, want)
		}
	}
	if strings.Contains(got, "engine-stale") {
		t.Errorf("no row is engine-stale while the base is %s; got %q", LegacyEngineBase, got)
	}
}
