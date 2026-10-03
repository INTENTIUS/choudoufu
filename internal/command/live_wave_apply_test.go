// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/waves"
)

// stubWaveRunner plans every root as set.json planned it, except the roots
// in moved, and records applies.
type stubWaveRunner struct {
	set     *waves.SetDocument
	moved   map[string]bool
	applied []string
}

func (s *stubWaveRunner) PlanRoots(_ context.Context, roots []string) (map[string]waves.FreshPlan, error) {
	out := map[string]waves.FreshPlan{}
	for _, r := range s.set.Roots {
		p := r
		if s.moved[r.Root] {
			p.Plan = json.RawMessage(`{"resource_changes":[{"address":"x.y","change":{"actions":["delete"]}}]}`)
		}
		out[r.Root] = waves.FreshPlan{RootPlan: p, PlanFile: r.Root + ".tfplan"}
	}
	return out, nil
}

func (s *stubWaveRunner) Apply(_ context.Context, root, _ string) error {
	s.applied = append(s.applied, root)
	return nil
}

func TestLiveWaveApplyCommand(t *testing.T) {
	dir := liveWavesFixture(t)
	t.Chdir(dir)
	data, err := os.ReadFile("set.json")
	if err != nil {
		t.Fatal(err)
	}
	set, err := waves.ParseSetDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := waves.DocumentDigests(set)
	if err != nil {
		t.Fatal(err)
	}
	stub := &stubWaveRunner{set: set, moved: map[string]bool{}}
	old := liveWaveApplyRunner
	liveWaveApplyRunner = func(*LiveWaveApplyCommand, string, string, int) (waves.ApplyRunner, error) { return stub, nil }
	defer func() { liveWaveApplyRunner = old }()

	run := func(args ...string) (int, string, string) {
		view, done := testView(t)
		code := (&LiveWaveApplyCommand{Meta: Meta{View: view}}).Run(args)
		out := done(t)
		return code, out.Stdout(), out.Stderr()
	}

	// The #1026 shape: a root moved since approval exits 3 naming it and
	// applies nothing.
	stub.moved["estates/e04"] = true
	code, stdout, stderr := run("-plan-set=set.json", "-digest="+digest, "-wave=1", "-resume=resume.json")
	if code != 3 || !strings.Contains(stdout, "estates/e04 moved") || len(stub.applied) != 0 {
		t.Fatalf("moved: exit %d applied %v\n%s\n%s", code, stub.applied, stdout, stderr)
	}
	if _, err := os.Stat("resume.json"); err == nil {
		t.Error("a refused wave wrote the resume file")
	}

	// An approval naming another digest: exit 3, nothing planned.
	stub.moved = map[string]bool{}
	code, _, stderr = run("-plan-set=set.json", "-digest=sha256:00", "-wave=1", "-resume=resume.json")
	if code != 3 || !strings.Contains(stderr, "not the approved") {
		t.Fatalf("wrong digest: exit %d\n%s", code, stderr)
	}

	// The approved set, wave 1: e01 and e04, each landed and recorded.
	code, stdout, stderr = run("-plan-set=set.json", "-digest="+digest, "-wave=1", "-resume=resume.json")
	if code != 0 || strings.Join(stub.applied, ",") != "estates/e01,estates/e04" {
		t.Fatalf("wave 1: exit %d applied %v\n%s\n%s", code, stub.applied, stdout, stderr)
	}
	r, err := waves.ReadResume("resume.json")
	if err != nil {
		t.Fatal(err)
	}
	if r.SetDigest != digest || len(r.Roots) != 2 || r.Roots[0].Outcome != waves.OutcomeLanded {
		t.Errorf("resume %+v", r)
	}
}
