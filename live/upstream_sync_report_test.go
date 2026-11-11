// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/engine/forkport"
)

// TestUpstreamSyncReportFlagsExpiredForkPorts runs scripts/upstream-sync.sh
// --report against canned upstream issue states (UPSTREAM_SYNC_ISSUE_STATES,
// so no network) and the committed gap table, for every registered fork port
// (#2376): with its expiry issues open there is no EXPIRE line, with any
// unknown there is none, and with all of them closed there is exactly
// "EXPIRE: remove port <name> (upstream #N closed)". A port with no expiry
// issue is never flagged.
func TestUpstreamSyncReportFlagsExpiredForkPorts(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatal("scripts/upstream-sync.sh needs jq on PATH")
	}
	if len(forkport.Ports) == 0 {
		t.Fatal("no fork port is registered; this test would check nothing")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	report := func(t *testing.T, state string) string {
		canned := map[string]string{}
		if state != "" {
			for _, p := range forkport.Ports {
				for _, n := range p.Expiry {
					canned[strings.TrimPrefix(n, "#")] = state
				}
			}
		}
		b, _ := json.Marshal(canned)
		file := filepath.Join(t.TempDir(), "states.json")
		if err := os.WriteFile(file, b, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", "scripts/upstream-sync.sh", "--report")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "UPSTREAM_SYNC_ISSUE_STATES="+file, "UPSTREAM_SYNC_GAP_FILE=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("upstream-sync.sh --report: %v\n%s", err, out)
		}
		return string(out)
	}
	expire := func(p forkport.Port) string {
		issues := slices.Clone(p.Expiry)
		slices.Sort(issues) // the script lists them as jq's unique does
		return "EXPIRE: remove port " + p.Name + " (upstream " + strings.Join(issues, ", ") + " closed)"
	}

	for _, state := range []string{"open", ""} {
		out := report(t, state)
		if strings.Contains(out, "EXPIRE:") {
			t.Errorf("with expiry issues %q, the report flags an expiry:\n%s", state, out)
		}
		for _, p := range forkport.Ports {
			if !strings.Contains(out, "  "+p.Name+"  rows ") {
				t.Errorf("the report does not list port %q:\n%s", p.Name, out)
			}
		}
	}
	out := report(t, "closed")
	for _, p := range forkport.Ports {
		if len(p.Expiry) == 0 {
			// No upstream issue (#2379): removed when its row closes, never
			// flagged by the report.
			if strings.Contains(out, "EXPIRE: remove port "+p.Name+" ") {
				t.Errorf("port %q has no expiry issue, and the report flags it:\n%s", p.Name, out)
			}
			continue
		}
		if !strings.Contains(out, "\n"+expire(p)+"\n") {
			t.Errorf("with every expiry issue closed, the report lacks %q:\n%s", expire(p), out)
		}
	}
}
