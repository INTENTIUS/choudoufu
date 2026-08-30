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

// TestOracleVersionsReadsThePin: oracleVersions reads live/oracle-versions.json
// the same way emulatorPin reads live/floci-image - the CONFIGURATION half
// of issue #544.
func TestOracleVersionsReadsThePin(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	pin := `{"terraform_version": "1.16.0", "tofu_version": "1.12.6"}`
	if err := os.WriteFile(filepath.Join(root, "live", "oracle-versions.json"), []byte(pin), 0o644); err != nil {
		t.Fatal(err)
	}
	got := oracleVersions(root)
	want := OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}
	if got != want {
		t.Errorf("oracleVersions = %+v, want %+v", got, want)
	}
}

// TestOracleVersionsMissingPinIsZeroValue: a missing pin file reads as the
// zero value, never an error and never a guess - the same graceful-empty
// behaviour emulatorPin already has.
func TestOracleVersionsMissingPinIsZeroValue(t *testing.T) {
	root := t.TempDir()
	if got := oracleVersions(root); got != (OracleVersions{}) {
		t.Errorf("oracleVersions with no pin file = %+v, want zero value", got)
	}
}

// TestProbeOracleIsMeasuredNotConfigured: probeOracle calls
// oracleVersionProbe per binary and never reads live/oracle-versions.json -
// the EVIDENCE half of issue #544. A binary probeOracleVersion could not
// find (simulated here by the override returning "") leaves that field
// empty rather than falling back to any configured pin.
func TestProbeOracleIsMeasuredNotConfigured(t *testing.T) {
	orig := oracleVersionProbe
	defer func() { oracleVersionProbe = orig }()

	calls := map[string]int{}
	oracleVersionProbe = func(bin string) string {
		calls[bin]++
		switch bin {
		case "terraform":
			return "1.16.0"
		case "tofu":
			return "" // simulates tofu missing from PATH
		default:
			t.Fatalf("probeOracle asked for unexpected binary %q", bin)
			return ""
		}
	}

	got := probeOracle()
	want := OracleVersions{Terraform: "1.16.0", Tofu: ""}
	if got != want {
		t.Errorf("probeOracle = %+v, want %+v", got, want)
	}
	if calls["terraform"] != 1 || calls["tofu"] != 1 {
		t.Errorf("expected exactly one probe each of terraform and tofu, got %+v", calls)
	}
}

// TestProbeOracleVersionParsesRealJSON: probeOracleVersion parses the
// `terraform_version` field real `terraform version -json` and
// `tofu version -json` both emit (OpenTofu kept the key name), not a
// bin-specific schema.
func TestProbeOracleVersionParsesRealJSON(t *testing.T) {
	// A stub binary that prints terraform's own -json shape.
	dir := t.TempDir()
	stub := "#!/usr/bin/env bash\n" +
		"printf '{\"terraform_version\": \"1.16.0\", \"platform\": \"linux_amd64\", \"provider_selections\": {}, \"terraform_outdated\": false}\\n'\n"
	stubPath := filepath.Join(dir, "fake-terraform")
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := probeOracleVersion(stubPath), "1.16.0"; got != want {
		t.Errorf("probeOracleVersion = %q, want %q", got, want)
	}
}

// TestProbeOracleVersionMissingBinaryIsEmpty: a binary not on PATH reads as
// "", never an error the caller has to handle and never a panic.
func TestProbeOracleVersionMissingBinaryIsEmpty(t *testing.T) {
	if got := probeOracleVersion("definitely-not-a-real-binary-544"); got != "" {
		t.Errorf("probeOracleVersion for a missing binary = %q, want empty", got)
	}
}

// TestRunEstatesRecordsOracle: RunEstates stamps every touched row's
// LastRun.Oracle with probeOracle()'s result, deterministically (via the
// oracleVersionProbe override) rather than depending on whatever happens to
// be installed on the machine running this test.
func TestRunEstatesRecordsOracle(t *testing.T) {
	orig := oracleVersionProbe
	defer func() { oracleVersionProbe = orig }()
	oracleVersionProbe = func(bin string) string {
		switch bin {
		case "terraform":
			return "1.16.0"
		case "tofu":
			return "1.12.6"
		}
		return ""
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

	m := &Manifest{Estates: []Estate{{Name: "z", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1}
	var out bytes.Buffer
	if _, err := RunEstates(root, m, a, RunOptions{Names: []string{"z"}, Stdout: &out}, "c", "e"); err != nil {
		t.Fatal(err)
	}

	r, ok := a.Result("z")
	if !ok {
		t.Fatal("no result for z")
	}
	if r.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	if r.LastRun.Oracle == nil {
		t.Fatal("LastRun.Oracle is nil; RunEstates never stamped it")
	}
	want := OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}
	if *r.LastRun.Oracle != want {
		t.Errorf("LastRun.Oracle = %+v, want %+v", *r.LastRun.Oracle, want)
	}
}

// TestRebuildSetsArtifactOracle: a.Oracle is a plain copy of Rebuild's oracle
// argument, refreshed on every call - the same "configuration for the next
// run" role a.Emulator already plays.
func TestRebuildSetsArtifactOracle(t *testing.T) {
	m := &Manifest{Estates: []Estate{{Name: "a", Source: "s", Lane: "reference", Set: SetCore, Reason: "r"}}}
	a := &Artifact{}
	a.Rebuild(m, "img", OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"})
	if a.Oracle != (OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}) {
		t.Errorf("a.Oracle = %+v after Rebuild, want the passed-in value", a.Oracle)
	}
	a.Rebuild(m, "img", OracleVersions{Terraform: "1.16.1"})
	if a.Oracle != (OracleVersions{Terraform: "1.16.1"}) {
		t.Errorf("a.Oracle = %+v after a second Rebuild, want the refreshed value (not carried forward from the first)", a.Oracle)
	}
}

// TestRenderEstatePageOracleLine: the estate page's oracle-provenance line
// (beside the existing emulator one) is silent for a row that predates
// #544 - no LastRun at all, or a LastRun that never recorded Oracle - and
// otherwise reports a match or a **Stale** note against a.Oracle, the same
// three-way shape the emulator line already uses.
func TestRenderEstatePageOracleLine(t *testing.T) {
	a := &Artifact{Oracle: OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}, Stages: Stages()}
	r := EstateResult{Name: "x", Protocol: ProtocolGauntlet, Stages: map[string]string{}}

	if page := renderEstatePage(r, a); strings.Contains(page, "Oracle:") {
		t.Errorf("no LastRun at all: page should not mention Oracle:\n%s", page)
	}

	r.LastRun = &LastRun{Commit: "c", Date: "d"}
	if page := renderEstatePage(r, a); strings.Contains(page, "Oracle:") {
		t.Errorf("LastRun.Oracle is nil: page should not mention Oracle:\n%s", page)
	}

	r.LastRun.Oracle = &OracleVersions{Terraform: "1.16.0", Tofu: "1.12.6"}
	if page := renderEstatePage(r, a); !strings.Contains(page, "Oracle: stock terraform `1.16.0`, stock tofu `1.12.6` (matches the current pin).") {
		t.Errorf("expected a matching-oracle line; got:\n%s", page)
	}

	r.LastRun.Oracle = &OracleVersions{Terraform: "1.15.8", Tofu: "1.12.5"}
	page := renderEstatePage(r, a)
	if !strings.Contains(page, "Oracle: stock terraform `1.15.8`, stock tofu `1.12.5`.") {
		t.Errorf("expected the recorded (stale) versions in the line; got:\n%s", page)
	}
	if !strings.Contains(page, "**Stale**: the current pin is terraform `1.16.0`, tofu `1.12.6`.") {
		t.Errorf("expected a stale note naming the current pin; got:\n%s", page)
	}
}
