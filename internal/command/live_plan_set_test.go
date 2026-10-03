// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/setplan"
)

// TestLiveBlockEstate reads the estate a root's live block names, from the
// block or from the sidecar, and says when there is no block at all.
func TestLiveBlockEstate(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return filepath.Dir(p)
	}
	block := write("block/main.tf", "terraform {\n  live {\n    estate = \"from-block\"\n  }\n}\n")
	sidecar := write("sidecar/main.tf", "resource \"terraform_data\" \"x\" {}\n")
	write("sidecar/"+"estate.chdf.hcl", "estate = \"from-sidecar\"\n")
	plain := write("plain/main.tf", "resource \"terraform_data\" \"x\" {}\n")
	bad := write("bad/main.tf", "resource \"terraform_data\" {\n")

	for _, tc := range []struct {
		dir    string
		estate string
		ok     bool
		err    string
	}{
		{block, "from-block", true, ""},
		{sidecar, "from-sidecar", true, ""},
		{plain, "", false, ""},
		{bad, "", false, "does not load"},
	} {
		estate, ok, err := liveBlockEstate(tc.dir)
		if estate != tc.estate || ok != tc.ok || (tc.err == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: (%q, %v, %v), want (%q, %v, error containing %q)", filepath.Base(tc.dir), estate, ok, err, tc.estate, tc.ok, tc.err)
		}
	}
}

// TestLivePlanSet_refusesDataDir: TF_DATA_DIR would make every root share
// one data directory.
func TestLivePlanSet_refusesDataDir(t *testing.T) {
	t.Setenv("TF_DATA_DIR", t.TempDir())
	view, done := testView(t)
	c := &LivePlanSetCommand{Meta: Meta{View: view}}
	args, closer, diags := arguments.ParseLivePlanSet([]string{"a"})
	defer closer()
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	code := c.Execute(args)
	out := done(t)
	if code != setplan.ExitError || !strings.Contains(out.Stderr(), "TF_DATA_DIR cannot be set") {
		t.Errorf("exit %d, stderr:\n%s", code, out.Stderr())
	}
}

// TestLivePlanSet_commandIsolation runs the command itself with a child
// executable that cannot start, so every root with a live block fails at
// init and the root without one fails before any child is started; the
// document still names all three, and the exit code is 4.
func TestLivePlanSet_commandIsolation(t *testing.T) {
	t.Setenv("TF_DATA_DIR", "")
	dir := t.TempDir()
	t.Chdir(dir)
	for name, body := range map[string]string{
		"a/main.tf":     "terraform {\n  live {\n    estate = \"set-a\"\n  }\n}\n",
		"b/main.tf":     "terraform {\n  live {\n    estate = \"set-b\"\n  }\n}\n",
		"plain/main.tf": "resource \"terraform_data\" \"x\" {}\n",
	} {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := livePlanSetBin
	livePlanSetBin = func() (string, error) { return filepath.Join(dir, "no-such-binary"), nil }
	t.Cleanup(func() { livePlanSetBin = old })

	view, done := testView(t)
	c := &LivePlanSetCommand{Meta: Meta{View: view}}
	args, closer, diags := arguments.ParseLivePlanSet([]string{"-json", "a", "plain", "b"})
	defer closer()
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	code := c.Execute(args)
	out := done(t)
	if code != setplan.ExitRootFailed {
		t.Fatalf("exit %d, want %d\nstderr:\n%s", code, setplan.ExitRootFailed, out.Stderr())
	}
	var doc setplan.Document
	if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
		t.Fatalf("stdout is not the document: %s\n%s", err, out.Stdout())
	}
	want := map[string]setplan.Stage{"a": setplan.StageInit, "plain": setplan.StageEstate, "b": setplan.StageInit}
	if len(doc.Roots) != 3 {
		t.Fatalf("%d roots in the document", len(doc.Roots))
	}
	for _, r := range doc.Roots {
		if r.Status != setplan.StatusFailed || r.Stage != want[r.Root] || r.Error == "" {
			t.Errorf("%s: status %q stage %q error %q, want failed at %q", r.Root, r.Status, r.Stage, r.Error, want[r.Root])
		}
	}
	if got := doc.Roots[0].Estate; got != "set-a" {
		t.Errorf("a's estate %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, arguments.DefaultPlanSetOutDir, "plugin-cache")); err != nil {
		t.Errorf("no shared provider cache under the output directory: %s", err)
	}
}
