// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	backendLocal "github.com/intentius/choudoufu/internal/backend/local"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/cone"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// summaryConeFallback is the warning a cone plan that planned the whole
// estate carries (GitHub issue #1956). The detail names the guard.
const summaryConeFallback = "Cone plan fell back to a full plan"

// liveCone is a cone plan's state on the runner (GitHub issue #1956).
type liveCone struct {
	// result is the cone as live-cone computes it.
	result *cone.Result
	// targets is result.Targets parsed; the operation's -target list.
	targets []addrs.Targetable
	// extra are the config resources the scope admits beyond what the
	// plan graph keeps over an empty state: deleted blocks and moved-from or
	// removed addresses, whose objects exist only live and in the cache.
	// Without them their removal is withheld, which is what plain -target
	// does and what a cone must not.
	extra map[string]bool
	// types are the resource types the cone's resource targets name, nil
	// when a module call is in the cone and the types are not known here.
	// Stage 3 narrows the vouch legs to them.
	types map[string]bool
	// fallback is the guard's reason once it tripped inside PriorState.
	fallback string
}

func newLiveCone(res *cone.Result) (*liveCone, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	lc := &liveCone{result: res, extra: map[string]bool{}, types: map[string]bool{}}
	for _, s := range res.Targets {
		t, tDiags := addrs.ParseTargetStr(s)
		diags = diags.Append(tDiags)
		if tDiags.HasErrors() {
			continue
		}
		lc.targets = append(lc.targets, t.Subject)
		switch sub := t.Subject.(type) {
		case addrs.AbsResource:
			if lc.types != nil {
				lc.types[sub.Resource.Type] = true
			}
		case addrs.AbsResourceInstance:
			if lc.types != nil {
				lc.types[sub.Resource.Resource.Type] = true
			}
		default:
			lc.types = nil
		}
	}
	for _, s := range append(append([]string(nil), res.Deleted...), res.Refactor...) {
		if t, tDiags := addrs.ParseTargetStr(s); !tDiags.HasErrors() {
			if r, ok := t.Subject.(addrs.AbsResource); ok {
				lc.extra[r.Config().String()] = true
			}
		}
	}
	return lc, diags
}

// planRejectCone refuses -cone where it would do nothing or mean two
// things: a state-backed plan, -target or -exclude (the cone IS the target
// list), and a destroy, which is the whole estate by definition.
func planRejectCone(args *arguments.Plan, live bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if args.Cone == "" {
		return diags
	}
	switch {
	case !live:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"-cone needs a configuration under live resource markers",
			"-cone plans only the cone of a change within an estate (GitHub issue #1956), and this configuration has no live block. Add a live block, or drop -cone."))
	case len(args.Operation.Targets) > 0 || len(args.Operation.Excludes) > 0:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"-cone cannot be combined with -target or -exclude",
			"-cone computes the plan's targets from the change between the base revision and the working tree. Rerun with -cone alone, or with -target alone."))
	case args.Operation.PlanMode != plans.NormalMode:
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error,
			"-cone plans changes only",
			"-cone is for a plan of a change; -destroy and -refresh-only cover the whole estate. Rerun without -cone."))
	}
	return diags
}

// liveConePrepare computes the cone for a plan. A nil cone with no error is
// the guard's verdict: the warning it returns says why, and the plan runs
// in full.
func liveConePrepare(ctx context.Context, base string) (*liveCone, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	wd, err := os.Getwd()
	if err != nil {
		return nil, diags.Append(err)
	}
	res, err := cone.Compute(ctx, cone.Options{Dir: wd, Base: base})
	if err != nil {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot compute the cone", err.Error()))
	}
	if res.Guard.Full {
		return nil, diags.Append(coneFallbackDiag(res.Guard.Kind, res.Guard.Reason))
	}
	lc, parseDiags := newLiveCone(res)
	if parseDiags.HasErrors() {
		return nil, diags.Append(coneFallbackDiag(cone.GuardParse, "a cone address does not parse as a target: "+parseDiags.Err().Error()))
	}
	return lc, diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cone plan",
		fmt.Sprintf("This plan covers the cone of the change since %s: %d of the estate's %d blocks (GitHub issue #1956). Drift and removals outside it are a full plan's to report.", base, res.ConeBlocks, res.Blocks)))
}

func coneFallbackDiag(kind, reason string) tfdiags.Diagnostic {
	return tfdiags.Sourceless(tfdiags.Warning, summaryConeFallback,
		fmt.Sprintf("This plan covers the whole estate (guard %s): %s.", kind, reason))
}

// scope widens the targeting scope with the cone's deleted and refactored
// addresses. Under a fallback the scope is the run's own.
func (lc *liveCone) scope(inner identity.Scope) identity.Scope {
	if lc == nil || lc.fallback != "" || inner == nil {
		return inner
	}
	return func(addr addrs.ConfigResource) bool {
		return inner(addr) || lc.extra[addr.String()]
	}
}

// narrowTypes keeps the types in the cone (stage 3). The marker sweep that
// finds orphans is not narrowed: it stays estate-wide, which is what lets
// the sweep guard below see an undeclared object outside the cone.
func (lc *liveCone) narrowTypes(types []string) []string {
	if lc == nil || lc.fallback != "" || lc.types == nil {
		return types
	}
	var out []string
	for _, t := range types {
		if lc.types[t] {
			out = append(out, t)
		}
	}
	return out
}

// coneSweepGuard is the guard's sweep half: an undeclared marked object, or
// an address two live objects claim, outside the cone. Either is a fact a
// cone plan would leave unreported, so the plan runs in full.
func coneSweepGuard(disco *discovery.Result, scope identity.Scope) string {
	if disco == nil || scope == nil {
		return ""
	}
	var outside []string
	for _, o := range disco.Orphans {
		if !scope(o.Addr.ConfigResource()) {
			outside = append(outside, o.Addr.String())
		}
	}
	if len(outside) == 0 {
		return ""
	}
	sort.Strings(outside)
	return fmt.Sprintf("the sweep found %d marked object(s) outside the cone that no block declares, such as %s", len(outside), outside[0])
}

// PlanTargets implements [backendLocal.LiveTargets].
func (r *liveRunner) PlanTargets() ([]addrs.Targetable, bool) {
	if r.cone == nil || r.cone.fallback == "" {
		return nil, false
	}
	return nil, true
}

// coneSidecar is what a cone plan's -out records beside the plan file:
// the cone's address set. The plan file's own target list carries the same
// addresses; the sidecar is what tells "apply PLANFILE" that the targets
// came from a cone and must be replanned as one, rather than from a
// person's -target, whose apply behaviour is unchanged.
type coneSidecar struct {
	Schema  int      `json:"schema"`
	Base    string   `json:"base"`
	BaseSHA string   `json:"base_sha"`
	Targets []string `json:"targets"`
}

func coneSidecarPath(planPath string) string { return planPath + ".cone.json" }

func writeConeSidecar(planPath string, lc *liveCone) error {
	b, err := json.MarshalIndent(coneSidecar{Schema: 1, Base: lc.result.Base, BaseSHA: lc.result.BaseSHA, Targets: lc.result.Targets}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(coneSidecarPath(planPath), b, 0o644)
}

func readConeSidecar(planPath string) (*coneSidecar, error) {
	b, err := os.ReadFile(coneSidecarPath(planPath))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sc coneSidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("%s: %w", coneSidecarPath(planPath), err)
	}
	return &sc, nil
}

// coneFromSidecar rebuilds the runner's cone for an apply of a cone plan.
func coneFromSidecar(sc *coneSidecar) (*liveCone, tfdiags.Diagnostics) {
	res := &cone.Result{Base: sc.Base, BaseSHA: sc.BaseSHA, Targets: sc.Targets}
	// Deleted and refactored addresses are among the targets; any target
	// the configuration does not declare is admitted by name.
	res.Deleted = sc.Targets
	return newLiveCone(res)
}

// liveRunnerOf returns the runner liveBegin installed on be, or nil.
func liveRunnerOf(be backend.Enhanced) *liveRunner {
	local, ok := be.(*backendLocal.Local)
	if !ok || local == nil {
		return nil
	}
	r, _ := local.LiveRun.(*liveRunner)
	return r
}

// targetSet renders a target list as a sorted, comparable string.
func targetSet(ts []addrs.Targetable) string {
	s := make([]string, 0, len(ts))
	for _, t := range ts {
		s = append(s, t.String())
	}
	sort.Strings(s)
	return strings.Join(s, "\n")
}

// coneApprovalGuard wraps the change-set guard with the cone check, which
// runs first: a different cone is refused even when the changes agree.
func coneApprovalGuard(path string, approved []string, inner func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics, refused *bool) func(*plans.Plan, *tofu.Schemas) tfdiags.Diagnostics {
	return func(plan *plans.Plan, schemas *tofu.Schemas) tfdiags.Diagnostics {
		if plan != nil {
			if diags := coneApprovalCheck(path, approved, plan); diags.HasErrors() {
				*refused = true
				return diags
			}
		}
		return inner(plan, schemas)
	}
}

// coneApprovalCheck is the cone half of the approval digest: the plan this
// apply made must cover the same address set the approved cone plan did.
func coneApprovalCheck(path string, approved []string, plan *plans.Plan) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	sort.Strings(approved)
	want := strings.Join(approved, "\n")
	got := targetSet(plan.TargetAddrs)
	if want == got {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(tfdiags.Error, summaryApprovalMismatch,
		fmt.Sprintf("%q was approved as a cone plan over %d address(es) (GitHub issue #1956), and this apply planned a different set. A cone that changed is a different approval: plan again with -cone and approve that.", path, len(approved))))
}
