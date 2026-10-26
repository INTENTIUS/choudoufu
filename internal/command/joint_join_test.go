// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mitchellh/cli"
	"github.com/zclconf/go-cty/cty"
)

// recordingCommand is the ordinary command a joint wrapper falls back to.
type recordingCommand struct {
	ran  [][]string
	code int
}

func (r *recordingCommand) Run(args []string) int {
	r.ran = append(r.ran, append([]string(nil), args...))
	return r.code
}
func (r *recordingCommand) Help() string     { return "" }
func (r *recordingCommand) Synopsis() string { return "" }

// TestJointSingleUnitTakesTheOrdinaryPath is #2218's single-unit item: a
// `plan` or `apply` that no multi-unit Terragrunt or Atmos run started (the
// test binary's parent is `go`, never the tool) is handed to the ordinary
// command unchanged, with the same arguments and its exit code, and opens no
// joint session, so it plans exactly as it did before joint runs existed.
func TestJointSingleUnitTakesTheOrdinaryPath(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv(jointEnv, "")
	if tool := detectJointTool(); tool != nil {
		t.Fatalf("the test's parent process reads as a multi-unit %s run (%v), so this cannot show the single-unit path; run it under go test", tool.Kind, tool.Args)
	}
	for _, name := range []string{"plan", "apply", "show", "output"} {
		t.Run(name, func(t *testing.T) {
			inner := &recordingCommand{code: 7}
			wrapped, err := JointWrap(name, Meta{}, func() (cli.Command, error) { return inner, nil })()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-input=false", "-out=tfplan"}
			if name != "plan" {
				args = []string{"tfplan"}
			}
			if code := wrapped.Run(args); code != 7 {
				t.Errorf("exit %d, want the ordinary command's 7", code)
			}
			if len(inner.ran) != 1 || len(inner.ran[0]) != len(args) {
				t.Fatalf("ordinary command ran %v, want once with %v", inner.ran, args)
			}
			for i := range args {
				if inner.ran[0][i] != args[i] {
					t.Errorf("ordinary command got %v, want %v", inner.ran[0], args)
				}
			}
			if code, ok := JointIntercept(name, Meta{}, args); ok {
				t.Errorf("JointIntercept took a single-unit %s (exit %d)", name, code)
			}
		})
	}
	if entries, err := os.ReadDir(jointSessionsRoot()); err == nil {
		for _, e := range entries {
			t.Errorf("a single-unit run left a joint session behind: %s", e.Name())
		}
	}
}

// TestJointConcurrentInvocationsPlanOnce is #2218's concurrency item: the
// units of one run register into one session, and once a joint plan exists
// for that set of units, every further invocation for the same set (each
// unit's process registering again and asking for the plan, at once) joins
// it. The session's state keeps one plan serial; nobody plans a second
// time. A plan that ran here would fail, having no providers, and record
// serial 2 with an error, so serial 1 and OK afterwards is the proof.
func TestJointConcurrentInvocationsPlanOnce(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	sess, err := openJointSession("terragrunt-7-join")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	unit := func(name string, vars map[string]jointVar) jointUnit {
		dir := filepath.Join(base, name)
		return jointUnit{Key: dir, Dir: dir, Vars: vars, Outputs: []jointOutputDecl{{Name: "id"}}}
	}
	a := unit("network", map[string]jointVar{"cidr": jointVarOf(t, cty.StringVal("10.0.0.0/16"))})
	b := unit("app", map[string]jointVar{"vpc_id": jointVarOf(t, cty.StringVal(jointSentinel("network", "id")))})
	sess.register(a, "network")
	sess.register(b, "app")

	// The first invocation's plan, as ensurePlan records it.
	units := sess.units()
	text, _, err := writeJointRoot(sess.path("root"), units)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(text))
	ids := []string{units[0].ID, units[1].ID}
	if err := sess.saveState(jointState{Serial: 1, Hash: hex.EncodeToString(sum[:]), OK: true, Units: ids}); err != nil {
		t.Fatal(err)
	}

	c := &jointCommand{name: "plan"}
	var wg sync.WaitGroup
	results := make([]jointState, 2)
	errs := make([]error, 2)
	for i, u := range []jointUnit{a, b} {
		wg.Add(1)
		go func(i int, u jointUnit) {
			defer wg.Done()
			unlock, err := sess.lock()
			if err != nil {
				errs[i] = err
				return
			}
			sess.register(u, filepath.Base(u.Dir))
			unlock()
			results[i], errs[i] = c.ensurePlan(sess, []string{"-input=false"})
		}(i, u)
	}
	wg.Wait()
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("invocation %d: %v", i, errs[i])
		}
		if results[i].Serial != 1 || !results[i].OK {
			t.Errorf("invocation %d got plan serial %d (ok %v), want the existing serial 1", i, results[i].Serial, results[i].OK)
		}
	}
	if st := sess.loadState(); st.Serial != 1 || !st.OK || len(st.Units) != 2 {
		t.Errorf("session state after both invocations: %+v, want serial 1 over two units", st)
	}
	if got := len(sess.units()); got != 2 {
		t.Errorf("%d units registered, want 2: a unit registering again keeps its place", got)
	}
}
