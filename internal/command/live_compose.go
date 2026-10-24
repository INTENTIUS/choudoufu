// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	backendLocal "github.com/intentius/choudoufu/internal/backend/local"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/clistate"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// A composed run (GitHub issue #2059) is one plan and one apply over several
// estates. The root has no live block of its own; it calls each estate's
// root as a module, and each called directory keeps the live block (or
// sidecar) it has when it runs alone:
//
//	module "network" { source = "../network" }
//	module "app"     { source = "../app" }
//
// The walk is one walk over one configuration, so a read of an output that
// is only known after apply resolves in that walk. Everything that decides
// ownership is still per estate. Each module gets a runner of its own, built
// the way a single-estate run is built, over that module's configuration
// taken as a root: its estate name, its record store, its discovery, its
// projection. Each runner's prior state is moved under module.<call> to make
// the walk's prior state, and each runner is handed its slice of the final
// state, with the module call's step taken off every address, to write its
// records. The markers the walk stamps are made by the module's runner too
// (estateRouter), from the same root-relative address.
//
// It is not atomic. Each estate's records are written when the apply ends,
// and a resource's record is written as it is applied (WriteInstance); a
// failed apply leaves what was applied applied and recorded, and a re-run
// plans only what is left.

// composedSettings is liveSettings' second question: a root with no live
// block of its own that calls local modules whose directories carry one.
// Anything it cannot load or does not understand is "not composed", so a
// directory that was never going to be a composed root is untouched.
func (m *Meta) composedSettings(ctx context.Context) *configs.Live {
	mod, diags := m.loadSingleModule(ctx, ".", configs.SelectiveLoadAll)
	if diags.HasErrors() || mod == nil || mod.Live != nil || len(mod.ModuleCalls) == 0 {
		return nil
	}
	names := make([]string, 0, len(mod.ModuleCalls))
	for name := range mod.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)
	var found []configs.ComposedEstate
	for _, name := range names {
		mc := mod.ModuleCalls[name]
		local, ok := mc.SourceAddr.(addrs.ModuleSourceLocal)
		if !ok {
			continue
		}
		child, childDiags := m.loadSingleModule(ctx, string(local), configs.SelectiveLoadBackend)
		if childDiags.HasErrors() || child == nil || child.Live == nil {
			continue
		}
		found = append(found, configs.ComposedEstate{Call: name, Dir: string(local), Live: child.Live, DeclRange: mc.DeclRange})
	}
	if len(found) == 0 {
		return nil
	}
	live := configs.NewComposedLive(found)
	if _, err := os.Stat(jointRootMarker); err == nil {
		live.Joint = true
	}
	return live
}

// composedEstate is one module's runner and the configuration it runs over.
type composedEstate struct {
	call   string
	live   *configs.Live
	runner *liveRunner
	config *configs.Config
	estate string
}

// composedRunner is the [backendLocal.LiveRun] of a composed run.
type composedRunner struct {
	settings      *configs.Live
	mgr           *projection.Manager
	estateOutputs *liveEstateOutputs
	estates       []*composedEstate
	by            map[string]*composedEstate
}

var _ backendLocal.LiveRun = (*composedRunner)(nil)

// liveBeginComposed is [liveBegin] for a composed root.
func liveBeginComposed(
	be backend.Enhanced,
	opReq *backend.Operation,
	settings *configs.Live,
	view *views.View,
	adoptionOnly bool,
	filter arguments.ReportFilter,
	estateOutputs *liveEstateOutputs,
	rejections tfdiags.Diagnostics,
) tfdiags.Diagnostics {
	diags := rejections
	runWorkspace := ignoreLiveWorkspace(opReq)
	local, ok := be.(*backendLocal.Local)
	if !ok || local.ContextOpts == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Live resource markers require local operations",
			Detail:   "This root calls modules that carry live blocks, which puts it under live resource markers, and those run plan and apply in this process. The backend selected for this run performs its operations elsewhere.",
			Subject:  settings.DeclRange.Ptr(),
		})
	}
	if len(opReq.Targets) > 0 || len(opReq.Excludes) > 0 {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "-target and -exclude are not available in a composed run",
			"A composed run plans and applies every estate it calls. Target an estate by running its directory alone, or leave the module call out of the root."))
	}
	if diags.HasErrors() {
		return diags
	}

	c := &composedRunner{
		settings:      settings,
		mgr:           projection.NewManager(),
		estateOutputs: estateOutputs,
		by:            map[string]*composedEstate{},
	}
	router := &estateRouter{by: map[string]*projection.NodeResolver{}}
	runView := livePlanView(view, adoptionOnly, filter)
	for _, ce := range settings.Composed {
		if ce.Live.EstateSet && !discovery.ValidEstateName(ce.Live.Estate) {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid estate name",
				Detail:   fmt.Sprintf("The live block of module %q names estate %q, which does not match the tofu-estate marker grammar in live/MARKERS.md.", ce.Call, ce.Live.Estate),
				Subject:  ce.Live.EstateRange.Ptr(),
			})
			continue
		}
		readsSelective := readsPolicyFor(ce.Live.Reads) != "full"
		sub := &liveRunner{
			settings:         ce.Live,
			baseDir:          ce.Dir,
			cacheServesReads: !opReq.PlanRefresh && readsSelective,
			refreshFalse:     !opReq.PlanRefresh,
			envelopeVouch:    !opReq.PlanRefresh && readsSelective && opReq.Type == backend.OperationTypePlan,
			workspace:        runWorkspace,
			applying:         opReq.Type == backend.OperationTypeApply,
			lib:              local.ContextOpts.Plugins,
			mgr:              projection.NewManager(),
			view:             runView,
			filter:           filter,
			adoptionOnly:     adoptionOnly,
		}
		sub.resolver = &projection.NodeResolver{}
		sub.nodeResolve = nodeResolveEnabled()
		router.by[ce.Call] = sub.resolver
		est := &composedEstate{call: ce.Call, live: ce.Live, runner: sub}
		c.estates = append(c.estates, est)
		c.by[ce.Call] = est
	}
	if diags.HasErrors() {
		return diags
	}
	local.LiveRun = c
	local.ContextOpts.ConfigValueAdjuster = router
	if nodeResolveEnabled() {
		local.ContextOpts.ResourceIdentityResolver = router
	}
	opReq.StateLocker = clistate.NewNoopLocker()
	return diags
}

// detachConfig returns c as if it were the root of its own configuration
// tree: the module call's step is gone from every path below it.
func detachConfig(c *configs.Config) *configs.Config {
	root := &configs.Config{
		Module:          c.Module,
		CallRange:       c.CallRange,
		SourceAddr:      c.SourceAddr,
		SourceAddrRaw:   c.SourceAddrRaw,
		SourceAddrRange: c.SourceAddrRange,
		Version:         c.Version,
	}
	root.Root = root
	var copyChildren func(dst, src *configs.Config)
	copyChildren = func(dst, src *configs.Config) {
		dst.Children = make(map[string]*configs.Config, len(src.Children))
		for name, ch := range src.Children {
			nc := &configs.Config{
				Root:            root,
				Parent:          dst,
				Path:            append(addrs.Module(nil), ch.Path[len(c.Path):]...),
				Module:          ch.Module,
				CallRange:       ch.CallRange,
				SourceAddr:      ch.SourceAddr,
				SourceAddrRaw:   ch.SourceAddrRaw,
				SourceAddrRange: ch.SourceAddrRange,
				Version:         ch.Version,
			}
			copyChildren(nc, ch)
			dst.Children[name] = nc
		}
	}
	copyChildren(root, c)
	return root
}

// composedCheck refuses a root that is not a set of estate module calls. A
// module call passes nothing in and nothing through: an estate's inputs are
// its own variables' defaults, and its providers are its own provider blocks,
// which is what keeps each estate's role on that estate's calls.
func composedCheck(settings *configs.Live, config *configs.Config) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if config == nil || config.Module == nil {
		return diags
	}
	fail := func(subject *hcl.Range, summary, detail string) {
		diags = diags.Append(&hcl.Diagnostic{Severity: hcl.DiagError, Summary: summary, Detail: detail, Subject: subject})
	}
	for _, r := range config.Module.ManagedResources {
		fail(r.DeclRange.Ptr(), "A composed root holds module calls only",
			fmt.Sprintf("%s is declared in the root of a composed run. The root has no estate, so nothing could own it. Move it into one of the estates the root calls.", r.Addr()))
	}
	estateCalls := map[string]bool{}
	for _, ce := range settings.Composed {
		estateCalls[ce.Call] = true
	}
	names := make([]string, 0, len(config.Module.ModuleCalls))
	for name := range config.Module.ModuleCalls {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mc := config.Module.ModuleCalls[name]
		if !estateCalls[name] {
			fail(mc.DeclRange.Ptr(), "A module call in a composed root has no live block",
				fmt.Sprintf("Module %q is called by a root that composes estates, but its directory carries no live block, so it is not an estate. Every module call in a composed root must be a local directory with a live block of its own.", name))
			continue
		}
		switch {
		case mc.Count != nil || mc.ForEach != nil || mc.Enabled != nil:
			fail(mc.DeclRange.Ptr(), "An estate module call cannot repeat",
				fmt.Sprintf("Module %q is an estate, and an estate is one set of resources with one owner. Remove count, for_each and enabled from the call.", name))
		case len(mc.Providers) > 0:
			fail(mc.DeclRange.Ptr(), "An estate module call cannot be handed providers",
				fmt.Sprintf("Module %q uses the provider blocks in its own directory. Those carry the estate's own role, and a provider handed in from the root would be one identity for every estate. Remove the providers argument.", name))
		}
		if mc.Config != nil && !settings.Joint {
			if attrs, ad := mc.Config.JustAttributes(); !ad.HasErrors() && len(attrs) > 0 {
				argNames := make([]string, 0, len(attrs))
				for a := range attrs {
					argNames = append(argNames, a)
				}
				sort.Strings(argNames)
				fail(mc.DeclRange.Ptr(), "An estate module call takes no arguments",
					fmt.Sprintf("Module %q is called with %v. An estate's inputs are its own variables' defaults, because the estate's ownership is worked out from its files and an argument from the root would change them. Remove the arguments.", name, argNames))
			}
		}
	}
	return diags
}

// StateMgr implements [backendLocal.LiveRun].
func (c *composedRunner) StateMgr() statemgr.Full { return c.mgr }

// RootOutputData implements [backendLocal.LiveRun]. The root has no record
// store of its own, so it has nothing to remember its outputs in.
func (c *composedRunner) RootOutputData() map[string]cty.Value { return nil }

// RecordedRootOutputs implements [backendLocal.LiveRun].
func (c *composedRunner) RecordedRootOutputs() *projection.RootOutputRecords { return nil }

// PriorState implements [backendLocal.LiveRun]: each estate's prior state is
// built by its own runner over its own configuration, then moved under its
// module call.
func (c *composedRunner) PriorState(ctx context.Context, config *configs.Config, core *tofu.Context) (*states.State, tfdiags.Diagnostics) {
	diags := composedCheck(c.settings, config)
	if diags.HasErrors() {
		return nil, diags
	}
	for _, est := range c.estates {
		child := config.Children[est.call]
		if child == nil {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Module not installed",
				fmt.Sprintf("Module %q is not in the loaded configuration. Run \"choudoufu init\" so the root's module calls are installed.", est.call)))
		}
		est.config = detachConfig(child)
	}

	merged := states.NewState()
	inRun := map[string]addrs.Module{}
	owners := map[string]string{}
	for _, est := range c.estates {
		st, d := est.runner.PriorState(ctx, est.config, core)
		diags = diags.Append(d)
		if d.HasErrors() || st == nil {
			return nil, diags
		}
		est.estate = est.runner.resolver.Estate
		if other, dup := owners[est.estate]; dup {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "Two modules are the same estate",
				fmt.Sprintf("Modules %q and %q both resolve to estate %q. An estate is the unit of ownership and a composed run holds each once.", other, est.call, est.estate)))
		}
		owners[est.estate] = est.call
		inRun[est.estate] = addrs.Module{est.call}
		mergeInto(merged, rehomeState(st, est.call))
	}
	core.SetInRunEstates(inRun)

	// Reads of an estate outside this run go through a record store. A
	// composed run has one reader, so it reads through the first estate's.
	opened := false
	for _, est := range c.estates {
		if est.runner.rawStore != nil {
			c.estateOutputs.open(est.runner.rawStore, est.runner.recordStoreCfg, est.runner.recordEstate, "")
			opened = true
			break
		}
	}
	if !opened {
		c.estateOutputs.open(nil, nil, "", "no estate in this composed run has a record store")
	}
	return merged, diags
}

// AfterPlan implements [backendLocal.LiveRun]. The post-plan checks are
// Kubernetes-only (the server-side dry run and the field-granular boundary)
// and are not run for a composed run; see live/LIMITATIONS.md.
func (c *composedRunner) AfterPlan(context.Context, *configs.Config, *plans.Plan, *tofu.Schemas) tfdiags.Diagnostics {
	return nil
}

// BeforeApply implements [backendLocal.LiveRun].
func (c *composedRunner) BeforeApply(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, est := range c.estates {
		diags = diags.Append(est.runner.BeforeApply(ctx))
	}
	return diags
}

// AfterApply implements [backendLocal.LiveRun].
func (c *composedRunner) AfterApply(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, est := range c.estates {
		diags = diags.Append(est.runner.AfterApply(ctx))
	}
	return diags
}

// WriteBack implements [backendLocal.LiveRun]: every estate writes its own
// records from its own slice of the final state. One estate failing to
// write does not stop the others.
func (c *composedRunner) WriteBack(ctx context.Context, finalState *states.State, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance, deposed []projection.DeposedDestroy, wholeDestroy bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, est := range c.estates {
		diags = diags.Append(est.runner.WriteBack(ctx, sliceState(finalState, est.call), schemas, sliceAddrs(replaced, est.call), sliceDeposed(deposed, est.call), wholeDestroy))
	}
	return diags
}

// WriteInstance implements [backendLocal.LiveRun]: the record of the one
// instance just applied goes to the estate its module is.
func (c *composedRunner) WriteInstance(ctx context.Context, state *states.State, addr addrs.AbsResourceInstance, schemas *tofu.Schemas, replaced []addrs.AbsResourceInstance, deposed []projection.DeposedDestroy) tfdiags.Diagnostics {
	if len(addr.Module) == 0 {
		return nil
	}
	est := c.by[addr.Module[0].Name]
	if est == nil {
		return nil
	}
	rel, _ := stripCall(addr, est.call)
	return est.runner.WriteInstance(ctx, sliceState(state, est.call), rel, schemas, sliceAddrs(replaced, est.call), sliceDeposed(deposed, est.call))
}
