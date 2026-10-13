// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package setplan

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// readsOf is a fake Options.ReadsOf: root base name to the estates it reads.
func readsOf(m map[string][]string) func(context.Context, string) ([]string, error) {
	return func(_ context.Context, dir string) ([]string, error) { return m[filepath.Base(dir)], nil }
}

func runOrdered(t *testing.T, f *fakeRunner, reads map[string][]string, roots ...string) *Document {
	t.Helper()
	base := setup(t, roots...)
	doc, err := Run(context.Background(), Options{
		Roots: roots, BaseDir: base, OutDir: "out", Parallel: 4,
		Runner: f, ReadsOf: readsOf(reads), Now: fixedClock(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

// GitHub issue #2051: a downstream root is planned after the upstream it
// reads, on its plan document, and the document marks it.
func TestRun_downstreamPlannedOnUpstreamPlan(t *testing.T) {
	// The reader is given first, so only the order makes it wait.
	f := &fakeRunner{}
	doc := runOrdered(t, f, map[string][]string{"app": {"est-network", "est-elsewhere"}}, "estates/app", "estates/network", "estates/other")
	roots := byRoot(doc)

	app := roots["estates/app"]
	if app.Status != StatusPlanned {
		t.Fatalf("app: %s at %s", app.Error, app.Stage)
	}
	if app.Reads != ReadsPlanned || len(app.PlannedFrom) != 1 {
		t.Fatalf("app: reads %q from %v, want planned from network alone", app.Reads, app.PlannedFrom)
	}
	if got := app.PlannedFrom[0]; got.Root != "estates/network" || got.Estate != "est-network" || got.PlanJSON != "out/estates/network.tfplan.json" {
		t.Errorf("app planned from %+v", got)
	}
	for _, r := range []string{"estates/network", "estates/other"} {
		if roots[r].Reads != "" || roots[r].PlannedFrom != nil {
			t.Errorf("%s is marked as reading a plan: %+v", r, roots[r])
		}
	}
	// The fake's plan stage refuses a document that does not exist yet, so
	// app planning at all proves network's was written first; the order
	// says so directly.
	pos := map[string]int{}
	for i, n := range f.planned {
		pos[n] = i
	}
	if pos["app"] < pos["network"] {
		t.Errorf("planned in order %v: app before network", f.planned)
	}
}

// An upstream that did not plan leaves its reader unplanned, naming it,
// rather than planned on what the upstream last applied.
func TestRun_upstreamFailureLeavesReaderUnplanned(t *testing.T) {
	f := &fakeRunner{breakAt: map[string]Stage{"network": StagePlan}}
	doc := runOrdered(t, f, map[string][]string{"app": {"est-network"}}, "estates/app", "estates/network")
	app := byRoot(doc)["estates/app"]
	if app.Status != StatusFailed || app.Stage != StagePlan || !strings.Contains(app.Error, "estates/network") {
		t.Fatalf("app: %+v", app)
	}
}

// Reads that form a cycle within the set cannot be ordered, and both roots
// fail rather than the set hanging.
func TestRun_cycleFailsBothRoots(t *testing.T) {
	doc := runOrdered(t, &fakeRunner{}, map[string][]string{"a": {"est-b"}, "b": {"est-a"}}, "estates/a", "estates/b", "estates/c")
	roots := byRoot(doc)
	for _, r := range []string{"estates/a", "estates/b"} {
		if roots[r].Status != StatusFailed || !strings.Contains(roots[r].Error, "cycle") {
			t.Errorf("%s: %+v", r, roots[r])
		}
	}
	if roots["estates/c"].Status != StatusPlanned {
		t.Errorf("c: %+v", roots["estates/c"])
	}
}
