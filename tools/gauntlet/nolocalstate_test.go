// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// reportsNoLocalState says whether a crossing script reports the
// no_local_state stage (#1098) on a line that is not a comment: either
// through live/e2e/lib/gauntlet.sh's gauntlet_kind_no_local_state, or with
// a gauntlet_stage line of its own (a pass, a fail, or a not_run that says
// why).
func reportsNoLocalState(script string) bool {
	for _, line := range strings.Split(script, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") {
			continue
		}
		if strings.Contains(l, "gauntlet_kind_no_local_state ") ||
			strings.Contains(l, "gauntlet_stage no_local_state ") {
			return true
		}
	}
	return false
}

// TestEveryKubernetesLaneScriptReportsNoLocalState is #1966's guard: in
// 2026-10 the kubernetes lane had 23 estates and 21 of them recorded
// no_local_state as not_run on every run, because their scripts never
// mentioned the stage. A script that does not name it reads not_run
// silently; this makes the omission a test failure instead, so a new
// kubernetes-lane estate either runs the stage or says why it cannot.
func TestEveryKubernetesLaneScriptReportsNoLocalState(t *testing.T) {
	// The red arm first: a script that names the stage only in a comment,
	// or not at all, must read as not reporting it, or the scan below
	// would pass whatever it was given.
	for _, s := range []string{
		"#!/usr/bin/env bash\n# no_local_state is not reported (not_run)\n# gauntlet_kind_no_local_state \"$GREEN\" configmap x \"$NS\"\n",
		"#!/usr/bin/env bash\ngauntlet_stage greenfield pass \"ok\"\n",
	} {
		if reportsNoLocalState(s) {
			t.Fatalf("reportsNoLocalState(%q) = true, want false", s)
		}
	}
	for _, s := range []string{
		"gauntlet_kind_no_local_state \"$GREEN\" configmap app-config \"$NS\"\n",
		"  gauntlet_stage no_local_state not_run \"why\"\n",
	} {
		if !reportsNoLocalState(s) {
			t.Fatalf("reportsNoLocalState(%q) = false, want true", s)
		}
	}

	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	var missing []string
	for _, e := range m.Estates {
		if e.Lane != LaneKubernetes {
			continue
		}
		n++
		b, err := os.ReadFile(filepath.Join(root, "live", "e2e", e.Name, "run.sh"))
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if !reportsNoLocalState(string(b)) {
			missing = append(missing, e.Name)
		}
	}
	if n == 0 {
		t.Fatal("the manifest lists no kubernetes-lane estate, so this check read nothing")
	}
	if len(missing) > 0 {
		t.Errorf("%d of %d kubernetes-lane scripts never report no_local_state, so every run records it as not_run without saying why: %s.\nCall gauntlet_kind_no_local_state after greenfield's verdict (live/e2e/lib/gauntlet.sh), or report `gauntlet_stage no_local_state not_run \"<why>\"`.",
			len(missing), n, strings.Join(missing, ", "))
	}
}
