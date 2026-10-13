// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/cone"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #1956: -cone is refused on a state-backed plan, beside
// -target, and with -destroy, and accepted alone under a live block.
func TestPlanRejectCone(t *testing.T) {
	parse := func(args ...string) *arguments.Plan {
		t.Helper()
		p, closer, diags := arguments.ParsePlan(args)
		defer closer()
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		return p
	}
	if d := planRejectCone(parse("-cone=main"), true); d.HasErrors() {
		t.Fatalf("plain -cone under a live block refused: %s", d.Err())
	}
	if d := planRejectCone(parse(), false); d.HasErrors() {
		t.Fatal("a plan without -cone was refused")
	}
	for name, args := range map[string][]string{
		"target":  {"-cone=main", "-target=aws_s3_bucket.a"},
		"destroy": {"-cone=main", "-destroy"},
	} {
		if d := planRejectCone(parse(args...), true); !d.HasErrors() {
			t.Errorf("%s: -cone was not refused", name)
		}
	}
	if d := planRejectCone(parse("-cone=main"), false); !d.HasErrors() {
		t.Error("-cone on a state-backed plan was not refused")
	}
}

// The cone's scope admits its deleted addresses, which the plan graph over
// an empty state cannot keep, so their removal is proposed, not withheld.
// The sweep guard trips on an orphan outside that scope and not inside it.
func TestLiveConeScopeAndSweepGuard(t *testing.T) {
	lc, diags := newLiveCone(&cone.Result{
		Targets: []string{"aws_s3_bucket.a", "aws_ssm_parameter.gone"},
		Deleted: []string{"aws_ssm_parameter.gone"},
	})
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	kept := func(addr addrs.ConfigResource) bool { return addr.String() == "aws_s3_bucket.a" }
	scope := lc.scope(kept)
	gone := mustInstance(t, "aws_ssm_parameter.gone")
	other := mustInstance(t, "aws_ssm_parameter.crashed")
	if !scope(gone.ConfigResource()) {
		t.Fatal("a deleted block is outside the cone's scope")
	}
	if why := coneSweepGuard(&discovery.Result{Orphans: []discovery.OwnedResource{{Addr: gone}}}, scope); why != "" {
		t.Fatalf("an orphan inside the cone tripped the guard: %s", why)
	}
	if why := coneSweepGuard(&discovery.Result{Orphans: []discovery.OwnedResource{{Addr: other}}}, scope); why == "" {
		t.Fatal("an undeclared marked object outside the cone did not trip the guard")
	}
	if got := lc.narrowTypes([]string{"aws_s3_bucket", "aws_sqs_queue"}); len(got) != 1 || got[0] != "aws_s3_bucket" {
		t.Fatalf("vouch types not narrowed to the cone: %v", got)
	}
	lc.fallback = "x"
	if got := lc.narrowTypes([]string{"aws_sqs_queue"}); len(got) != 1 {
		t.Fatal("a fallen-back cone still narrows")
	}
}

// The approval digest's cone half: apply refuses a plan over a different
// address set, and the sidecar round-trips.
func TestConeApprovalRefusesAnotherCone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.tfplan")
	lc, _ := newLiveCone(&cone.Result{Base: "main", Targets: []string{"aws_s3_bucket.a"}})
	if err := writeConeSidecar(path, lc); err != nil {
		t.Fatal(err)
	}
	sc, err := readConeSidecar(path)
	if err != nil || sc == nil {
		t.Fatalf("sidecar: %v", err)
	}
	same := &plans.Plan{TargetAddrs: lc.targets}
	if d := coneApprovalCheck(path, sc.Targets, same); d.HasErrors() {
		t.Fatalf("the same cone was refused: %s", d.Err())
	}
	wider, _ := newLiveCone(&cone.Result{Targets: []string{"aws_s3_bucket.a", "aws_s3_bucket.b"}})
	refused := false
	guard := coneApprovalGuard(path, sc.Targets, func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics { return nil }, &refused)
	if d := guard(&plans.Plan{TargetAddrs: wider.targets}, nil); !d.HasErrors() || !refused {
		t.Fatal("a different cone was not refused")
	}
	if none, _ := readConeSidecar(filepath.Join(dir, "other.tfplan")); none != nil {
		t.Fatal("a plan file with no cone record read as a cone plan")
	}
}

func mustInstance(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	a, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return a
}

// GitHub issue #2051 meets #1956: both flags parse together, -cone is not
// refused beside -estate-outputs-from, and the cone falls back to a full
// plan, since planned upstream outputs are a change the diff cannot show.
func TestConeWithEstateOutputsFromFallsBack(t *testing.T) {
	p, closer, diags := arguments.ParsePlan([]string{"-cone=main", "-estate-outputs-from=up=" + filepath.Join(t.TempDir(), "up.json")})
	defer closer()
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if p.Cone != "main" || len(p.EstateOutputsFrom) != 1 {
		t.Fatalf("parsed cone %q and %d -estate-outputs-from", p.Cone, len(p.EstateOutputsFrom))
	}
	if d := planRejectCone(p, true); d.HasErrors() {
		t.Fatalf("-cone refused beside -estate-outputs-from: %s", d.Err())
	}
	lc, _ := newLiveCone(&cone.Result{Targets: []string{"aws_s3_bucket.a"}})
	got, d := coneWithPlannedOutputs(lc, len(p.EstateOutputsFrom))
	if got != nil || len(d) != 1 || d[0].Description().Summary != summaryConeFallback {
		t.Fatalf("cone kept beside planned outputs: %v %v", got, d)
	}
	if kept, _ := coneWithPlannedOutputs(lc, 0); kept != lc {
		t.Fatal("a cone without -estate-outputs-from fell back")
	}
}
