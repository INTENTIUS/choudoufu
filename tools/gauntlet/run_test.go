// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestRunEstatesPreservesDetailForUnreachedStages: found re-verifying
// corpus-giantswarm-crossplane after the record-orphan-read sweep
// (610511fb73) - day2_rename regressed from pass to fail there, the script
// now exits before ever reaching day2_remove, and RunEstates's old
// `r.LastRun.Detail = res.Detail` line silently dropped day2_remove's
// previously-recorded wall text from the artifact even though its verdict
// in r.Stages was correctly left untouched. A script whose first run
// reaches every stage and whose second run stops early (an earlier stage
// regressed to fail) must keep the first run's detail for the stage the
// second run never printed a GAUNTLET line for.
func TestRunEstatesPreservesDetailForUnreachedStages(t *testing.T) {
	root := t.TempDir()
	scriptPath := filepath.Join("live", "e2e", "x", "run.sh")
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(scriptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	// A script that only ever reports cold_deploy and day2_rename (fail),
	// standing in for a script that aborts before reaching day2_remove.
	script := "#!/usr/bin/env bash\n" +
		"printf 'GAUNTLET protocol=1\\n'\n" +
		"printf 'GAUNTLET stage=cold_deploy verdict=pass detail=ok\\n'\n" +
		"printf 'GAUNTLET stage=day2_rename verdict=fail detail=regressed\\n'\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(root, scriptPath), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := &Manifest{Estates: []Estate{{Name: "x", Source: "s", Lane: "reference", Set: SetGrowing, Script: scriptPath}}}
	a := &Artifact{Schema: 1, Estates: []EstateResult{{
		Name:     "x",
		Protocol: ProtocolGauntlet,
		Stages: map[string]string{
			"cold_deploy": "pass",
			"day2_rename": "pass",
			"day2_remove": "fail",
		},
		LastRun: &LastRun{
			Commit: "priorcommit",
			Detail: map[string]string{
				"cold_deploy": "old cold_deploy detail",
				"day2_rename": "old day2_rename detail (was passing)",
				"day2_remove": "old, precisely-named day2_remove wall - must survive an early abort",
			},
		},
	}}}

	var out bytes.Buffer
	failures, err := RunEstates(root, m, a, RunOptions{Names: []string{"x"}, Stdout: &out}, "newcommit")
	if err != nil {
		t.Fatal(err)
	}
	if failures != 1 {
		t.Fatalf("failures = %d, want 1", failures)
	}

	r, ok := a.Result("x")
	if !ok {
		t.Fatal("no result for x")
	}
	if r.Stages["day2_rename"] != "fail" {
		t.Errorf("day2_rename verdict = %q, want fail (the regression itself)", r.Stages["day2_rename"])
	}
	if r.Stages["day2_remove"] != "fail" {
		t.Errorf("day2_remove verdict = %q, want fail (untouched, carried forward)", r.Stages["day2_remove"])
	}
	if r.LastRun == nil {
		t.Fatal("LastRun is nil")
	}
	if got := r.LastRun.Detail["day2_remove"]; got != "old, precisely-named day2_remove wall - must survive an early abort" {
		t.Errorf("day2_remove detail was dropped by the early abort: %q", got)
	}
	if got := r.LastRun.Detail["day2_rename"]; got != "regressed" {
		t.Errorf("day2_rename detail = %q, want this run's fresh detail (\"regressed\"), not the stale pass-era text", got)
	}
	if got := r.LastRun.Detail["cold_deploy"]; got != "ok" {
		t.Errorf("cold_deploy detail = %q, want this run's fresh detail", got)
	}
}
