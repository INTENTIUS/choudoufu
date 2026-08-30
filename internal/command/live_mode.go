// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"time"

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
	"github.com/intentius/choudoufu/internal/live/foreign"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/live/untag"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/states/statemgr"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// This file turns plain "choudoufu plan" and plain "choudoufu apply" stateless when the
// configuration says so, and leaves both of them exactly as they were when it
// does not.
//
// The activation is a configuration block and never a flag. See
// [configs.Live] for why. The consequence for the code here is that
// every entry point has to answer "is this a stateless configuration" before
// it does anything else with state, which is what statelessSettings is for.
//
// # The seam
//
// A stateless run is an ordinary local run with two things replaced, both
// through [backendLocal.StatelessRun]:
//
//   - the state manager, replaced with [projection.Manager], which persists
//     nothing and locks nothing;
//   - the prior state, replaced with a projection built by reading the live
//     system, supplied by [statelessRunner.PriorState] at the one moment the
//     configuration and the OpenTofu context both exist and nothing has been
//     planned yet.
//
// Everything else - the plan renderer, the approval prompt, the apply hooks
// and progress counts, interrupt handling, the resource-count summary - is
// stock, because a stateless run differs from an ordinary one only in where
// prior state comes from and where the result goes (nowhere).
//
// # The provider double-launch
//
// P1.4 hoped a projection state manager would let the run start one set of
// provider plugin processes instead of two. It does not, and the reason is
// structural rather than an unfinished piece of wiring. [statemgr.Full] has
// no access to configuration or to providers, so it cannot build a
// projection; and wherever the projection is built, it must be complete
// before [tofu.Context.Plan] is called, while the plan's own provider
// instances are created by provider nodes during the graph walk that Plan
// starts. There is no seam today for handing tofu.Context an already
// configured provider instance, so the projection's providers are a separate
// set, started before the walk and closed before it begins. Sharing them
// would mean a change to how tofu.Context obtains providers, which is a
// deeper refactor than this task, and it is not contorted into place here.
// What the seam does buy is the schema cache: the projection uses the same
// [plugins.Library] the plan will use, so the provider schemas are fetched
// once.

// statelessSettings reads the "live" block from the root module, or nil
// if this is an ordinary configuration.
//
// Loading is the same selective load that finds the backend block, so this
// costs no more than the backend lookup every command already does, and a
// configuration with a backend block alongside a live block fails here
// - in the decoder - rather than later.
//
// tolerateLoadErrors is for callers that have another source of
// configuration, namely an apply given a saved plan file: for those, a
// working directory that will not load is not evidence about stateless mode
// and is not this function's error to report.
func (m *Meta) statelessSettings(ctx context.Context, tolerateLoadErrors bool) (*configs.Live, tfdiags.Diagnostics) {
	mod, diags := m.loadSingleModule(ctx, ".", configs.SelectiveLoadBackend)
	if diags.HasErrors() {
		if tolerateLoadErrors {
			return nil, nil
		}
		return nil, diags
	}
	if mod == nil {
		return nil, nil
	}
	// Warnings are dropped rather than returned, matching loadBackendConfig:
	// the full configuration load inside the operation raises them again, and
	// reporting them twice is worse than reporting them late.
	return mod.Live, nil
}

// statelessBegin prepares a plan or apply operation to run statelessly, and
// is called only when the configuration has a live block.
//
// It refuses everything stateless mode v0 cannot honor (see
// statelessRejections), replaces the operation's state manager and prior
// state through the backend's seam, and makes the state lock a no-op at the
// CLI layer as well as at the manager - two independent reasons no lock file
// can appear, because one of them being wrong should not be enough.
func statelessBegin(
	be backend.Enhanced,
	opReq *backend.Operation,
	settings *configs.Live,
	view *views.View,
	rejections tfdiags.Diagnostics,
) tfdiags.Diagnostics {
	diags := rejections

	if settings.EstateSet && !discovery.ValidEstateName(settings.Estate) {
		// Configuration content is at fault, so the diagnostic carries the
		// range of the argument that named the estate rather than being
		// sourceless; the environment-versus-content split is the one
		// meta_backend.go follows.
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid estate name",
			Detail:   fmt.Sprintf("The live block names estate %q, which does not match the tofu-estate marker grammar in live/MARKERS.md: a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters.", settings.Estate),
			Subject:  settings.EstateRange.Ptr(),
		})
	}

	if opReq.Workspace != backend.DefaultStateName {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Workspaces are not available under live resource markers",
			fmt.Sprintf("This run selected workspace %q. A workspace is a second state file under a different name, and a live-markers run has no first one: an estate is the unit of ownership instead, named by the live block. Select the default workspace, or give each estate its own configuration.", opReq.Workspace),
		))
	}

	local, ok := be.(*backendLocal.Local)
	if !ok || local.ContextOpts == nil {
		// The live block is the half of this conflict that lives in the
		// configuration, so it is the half the diagnostic can point at.
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Live resource markers require local operations",
			Detail:   "The live block puts this configuration under live resource markers, which run plan and apply in this process against a projection of the live system. The backend selected for this run performs its operations elsewhere, out of the live block's reach.",
			Subject:  settings.DeclRange.Ptr(),
		})
	}

	if diags.HasErrors() {
		return diags
	}

	// The manager's one optional side effect - guided discovery's hint
	// (issue #109) - is enabled later, in PriorState, once the estate name
	// is settled and the live block's record store (its carrier) is open.
	// Nothing to configure here.
	mgr := projection.NewManager()

	runner := &statelessRunner{
		settings: settings,
		lib:      local.ContextOpts.Plugins,
		mgr:      mgr,
		view:     views.NewStatelessPlan(view),
		// GitHub issue #352. The operation carries the run's -target and
		// -exclude addresses; PriorState is where they turn into a scope,
		// because that is where the core context that can answer what the
		// plan graph keeps is finally in hand.
		targets:  opReq.Targets,
		excludes: opReq.Excludes,
	}
	if testStatelessRunner != nil {
		testStatelessRunner(runner)
	}
	local.Stateless = runner

	// GitHub issue #388's plan-node seam, behind its migration flag (see
	// [nodeResolveEnabled]). The resolver is constructed HERE, empty, and
	// handed to ContextOpts before tofu.NewContext is ever called - the
	// only point in the whole call chain where that field can still be
	// set (backend_local.go's localRunDirect takes a *copy* of
	// local.ContextOpts before calling NewContext, so this write has to
	// land before that copy is taken, and PriorState - the step that
	// actually knows the record store and the marker sweep's index - does
	// not run until well after NewContext has returned). runner.resolver
	// is populated a few steps later, at the end of PriorState, once both
	// of those exist; managedResourceExecute never calls into it before
	// then, because Plan()'s own graph walk is later still. When the flag
	// is off (CHOUDOUFU_NODE_RESOLVE=0, the opt-out since the 2026-08-25
	// default flip), ContextOpts.ResourceIdentityResolver is never touched
	// and stays nil, which is the scaffold's own proven nil contract
	// (TestContext2Plan_resourceIdentityResolverNilContract) - an estate
	// run with the opt-out is unaffected byte for byte by anything this
	// seam does.
	//
	// ConfigValueAdjuster rides the same object and the same flag - GitHub
	// issue #388's stamp half, [projection.NodeResolver.AdjustConfigValue] -
	// for the reason that method's own doc comment gives: one resolver
	// serves both tofu.ResourceIdentityResolver and tofu.ConfigValueAdjuster
	// so the two seams can never read a different estate name or a
	// different markers-record selection from each other. The nil contract
	// for THIS field is proven the identical way
	// (TestContext2Plan_resourceIdentityResolverNilContract also pins
	// ConfigValueAdjuster nil/unset; see this package's own
	// TestStatelessBegin_nodeResolveFlagOff for the live-side half of that
	// proof).
	if nodeResolveEnabled() {
		runner.nodeResolve = true
		runner.resolver = &projection.NodeResolver{}
		local.ContextOpts.ResourceIdentityResolver = runner.resolver
		local.ContextOpts.ConfigValueAdjuster = runner.resolver
	}

	// The manager's Lock is already a no-op, so this is redundant on purpose.
	// It also spares the operator the "Acquiring state lock" message for a
	// lock that does not exist.
	opReq.StateLocker = clistate.NewNoopLocker()

	return diags
}

// nodeResolveEnabled reports whether GitHub issue #388's plan-node seam
// (rfc/20260823-foundation-order-ruling.md, ruling 3) routes identity
// resolution through the node - internal/live/projection.NodeResolver,
// installed as the run's tofu.ResourceIdentityResolver - instead of the
// pre-walk static path.
//
// Default ON since 2026-08-25 (GitHub issue #388's comment thread carries
// the measurement): two flag-on sweeps compared all 26 protocol-speaking
// estates flag-on against flag-off, and the alb-flagon follow-up resolved
// the one estate that had genuinely differed. Every estate is now either
// byte-identical between the two paths, or - corpus-alb-complete - clears
// more stages flag-on than flag-off with nothing that regressed. Nothing
// about that measurement retires the static path: HANDOFF.md foundation
// item 3's own "Done when" is the gauntlet holding with the node path on
// AND the static path off, TestIdentityGolden re-pinned against the node
// path, and the refusal registry smaller by the retired stage, none of
// which has happened. This flip only changes which path an operator gets
// with nothing set in the environment; CHOUDOUFU_NODE_RESOLVE=0 is the
// opt-out, and it still selects the static evaluator and the HCL-rewriting
// stamp exactly as before. Any other value, including "1" (the flag's old
// spelling from when it defaulted off, kept working so nobody's existing
// override silently changes meaning) and unset, resolves to the node path.
// Read once per statelessBegin so a single CLI invocation cannot see the
// flag change mid-run.
//
// It is an environment variable and not a live-block toggle for the same
// reason [Live] itself is a configuration block and everything else here is
// not: this one is not a property of an estate's configuration, reviewed
// and checked in with it, but a property of the BUILD an operator is
// running - "does this binary's engine resolve identity the new way yet" -
// which is a fact about migration progress, not about ownership. Every
// toggle that IS a property of an estate (marker_repair, secrets,
// no_source_create) stays in the live block's strict { } schema exactly as
// #365 defines it.
func nodeResolveEnabled() bool {
	return os.Getenv("CHOUDOUFU_NODE_RESOLVE") != "0"
}

// nodeResolverUnownedSet builds a [projection.NodeResolver.Unowned] set from
// a completed projection's own [projection.Result.Unowned] list, keyed by
// [addrs.AbsResourceInstance.String] - the shared helper both
// statelessBegin (live_mode.go) and LivePlanCommand's "-estate" form
// (live_plan.go) call once projResult exists, at the two population sites
// that field's own doc comment points to.
func nodeResolverUnownedSet(unowned []projection.Unowned) map[string]bool {
	if len(unowned) == 0 {
		return nil
	}
	out := make(map[string]bool, len(unowned))
	for _, u := range unowned {
		out[u.Addr.String()] = true
	}
	return out
}

// testStatelessRunner, when set, is handed every runner as it is built. It
// exists so that a test can assert about the state manager afterwards - in
// particular that PersistState was called and still wrote nothing, which is
// the half of the no-persistence proof that walking the filesystem cannot
// make. Nil in every real run.
var testStatelessRunner func(*statelessRunner)

// statelessRejections lists the options a stateless run cannot honor, in the
// shared vocabulary of the plan and apply flag sets. Everything here is
// refused with an explanation rather than accepted and quietly ignored.
//
// planOut and planFile are the two the fork thought hardest about, and they
// are refused for the same reason. A saved plan file records the state
// snapshot the plan was made against so that apply can check that the state
// has not moved. That record is authoritative by the roadmap's own test: if
// it is wrong - if the live system moved between plan and apply - applying it
// does the wrong thing to the world. Stateless mode exists to have no such
// record, so v0 has neither half: "plan -out" produces nothing to apply, and
// "apply <planfile>" is refused rather than being handed a stale projection
// with no discovery, no marker stamping and no live read behind it. The
// stateless answer to "review then apply" is that plan and apply each read
// the world when they run.
func statelessRejections(op *arguments.Operation, state *arguments.State, viewOpts arguments.ViewOptions, planOut, generateConfigOut, planFile string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	reject := func(summary, detail string) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
	}

	if viewOpts.ViewType != arguments.ViewHuman || viewOpts.JSONInto != nil {
		reject("Machine-readable output is not available under live resource markers yet",
			"A live-markers run prints sections describing what it could not read from the live system and what it found that nobody owns, and those sections have no JSON representation yet. Rerun without -json or -json-into.")
	}
	if planOut != "" {
		reject("Saved plan files are not available under live resource markers",
			"A saved plan file records the state snapshot the plan was made against. Here prior state is rebuilt from the live system every time, so an apply plans against the live system at the moment it runs. Rerun without -out and apply directly.")
	}
	if planFile != "" {
		diags = diags.Append(statelessRejectPlanFile(planFile))
	}
	if generateConfigOut != "" {
		reject("Config generation is not available under live resource markers yet",
			"-generate-config-out writes generated configuration for import blocks into a file, and that generated form has not been checked against the live-markers configuration subset yet. Rerun without -generate-config-out.")
	}
	// GitHub issue #320, ruled in #425: DestroyMode is a generalization of
	// the orphan sweep, not a separate mechanism, so it is no longer
	// refused here. The sweep already merges any owned instance with no
	// matching config block into the change set as a destroy (see
	// internal/live/untag/doc.go); DestroyMode's own contract - "destroy
	// all remote objects... even if the configuration for those instances
	// is still present" (plans.DestroyMode's doc comment) - asks for
	// exactly that same merge applied to every owned instance the
	// projection built, declared or not. Nothing downstream of
	// statelessRejections branches on PlanMode: PriorState above builds
	// the same projection of every owned instance regardless of mode, and
	// the plan and apply that follow are stock, so it is stock's own
	// destroy-graph walker - unmodified - that orders the result. See
	// day2_remove and day2_replace, the two active stages that already
	// lean on that same walker for a single instance at a time; this lifts
	// the refusal on the estate-wide case rather than inventing a second
	// ordering mechanism.
	//
	// -refresh-only stays refused: it is a genuinely different operation
	// (compare a stored record against the live system) that has no
	// meaning here, where both sides of that comparison are the live
	// system - not a verification gap the sweep already closes.
	if op != nil && op.PlanMode == plans.RefreshOnlyMode {
		reject("Only the normal planning mode is available under live resource markers",
			"Live resource markers produce and apply normal plans. -refresh-only compares a stored record against the live system, and here both sides of that comparison are the live system. Rerun without -refresh-only.")
	}
	if state != nil && (state.StatePath != "" || state.StateOutPath != "" || state.BackupPath != "") {
		reject("State file options are not available under live resource markers",
			"Prior state is a projection, built from the live system and discarded when the run ends, so these options have no file to act on. Rerun without -state, -state-out and -backup.")
	}

	return diags
}

// statelessRejectPlanFile is its own function because the apply command has
// to refuse a saved plan before it tries to read one: a stateless
// configuration and a plan file are incompatible whether or not the file is
// readable, and "that is not a valid plan file" would be a confusing answer
// to a request that was never going to be honored.
func statelessRejectPlanFile(path string) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Applying a saved plan file is not available under live resource markers",
		fmt.Sprintf("This configuration has a live block, so an apply builds its prior state by reading the live system, stamps ownership markers, and plans against what it finds. Applying %q would instead act on a record of how things were when the plan was made. Run \"choudoufu apply\" with no plan file.", path),
	))
}

// ---------------------------------------------------------------------------
// The runner
// ---------------------------------------------------------------------------

// statelessRunner is the stateless pipeline, wearing the interface the local
// backend calls it through. One runner serves one operation.
type statelessRunner struct {
	// settings is the live block this run was started from. The whole block
	// is kept, not just its estate name, so that a diagnostic raised once the
	// run is under way can still point at the configuration that asked for
	// stateless mode - which is the thing at fault when the estate cannot be
	// settled.
	settings *configs.Live

	// policy is the live block's optional policy block, resolved to a
	// [policy.Policy] once the estate name is settled below. It is GitHub
	// issue #67's config/lint half only: nothing in this struct's own
	// methods reads it yet. See [statelessPolicy].
	policy *policy.Policy

	// untagTargets, untagKey, untagProvider and untagConfig are GitHub issue
	// #67's undeclared_tagged = "untag" verb's apply-time work, captured by
	// PriorState and consumed by AfterApply. They cannot be worked out
	// inside AfterApply itself: by the time it runs, the providers
	// PriorState listed through are already closed (see this file's "The
	// provider double-launch" doc comment), and the orphans that need
	// releasing were only known once discovery and the policy pass had run.
	// Empty on any run with nothing for the untag verb to do, which is
	// every run with no policy block and most runs with one.
	untagTargets  []untag.Target
	untagKey      string
	untagProvider addrs.AbsProviderConfig
	untagConfig   *configs.Config

	lib  plugins.Library
	mgr  *projection.Manager
	view views.StatelessPlan

	// targets and excludes are this operation's -target and -exclude
	// addresses (GitHub issue #352), copied out of the backend operation at
	// [statelessBegin] because the runner never sees it again. Both empty
	// for an untargeted run, which is what makes the scope below nil and the
	// whole pipeline byte-identical to what it was.
	targets  []addrs.Targetable
	excludes []addrs.Targetable

	// recordStore and recordVersions are GitHub issue #73's write-back
	// state, both set by PriorState once the estate name and the live
	// block's record_store (if any) are settled. recordStore is nil for a
	// run with no record_store block, which is what makes WriteBack (and,
	// upstream of it, RECORD_ADMITTED admission at lint) a no-op.
	//
	// GitHub issue #364 folded what used to be three more (co-opened) store
	// handles - locatedStore, residueStore, provisionedStore - into this
	// same field: they are now the same [*projection.RecordStore], reading
	// and writing kind=identity envelopes for the same physical key
	// recordStore reads and writes kind=object ones for. What is still
	// genuinely separate is the version bookkeeping - see envelopeVersions.
	recordStore    *projection.RecordStore
	recordVersions []projection.RecordVersion

	// rawStore is the same underlying [staterecord.Store] recordStore
	// wraps, kept separately because two callers still need the raw
	// interface rather than the envelope view: guided discovery's hint
	// (issue #109, r.mgr.EnableHint and discovery.Request.HintStore) and
	// GitHub issue #349's root-output namespace (rootOutputStore), neither
	// of which is a per-instance record and so neither of which moved into
	// [projection.RecordStore]'s envelope.
	rawStore staterecord.Store

	// envelopeVersions is GitHub issue #364's merge of what used to be
	// three separate fields (locatedVersions, residueVersions,
	// provisionedVersions) for GitHub issues #270, #275 and #353: the
	// plan-time version of every kind=identity envelope that already
	// existed. One field rather than three because the three concerns now
	// share one physical key per instance - see
	// [projection.Result.EnvelopeVersions].
	envelopeVersions []projection.RecordVersion

	// liveConfig is the configuration WriteBack works from. The residue
	// classifier re-opens providers from it - the ones PriorState read
	// through are closed before the plan graph starts (see this file's
	// "The provider double-launch" comment), and it is the only write-back
	// half that needs a live provider, because there is no static answer to
	// which arguments a provider's read manages. The provisioned half needs
	// it for a different reason: whether an instance gets a taint record
	// turns on whether its resource block declares a create-time
	// provisioner, which is a fact about the configuration and is not
	// recoverable from the final state.
	liveConfig *configs.Config

	// rootOutputData holds the live data-source values PriorState read for
	// the configuration's root outputs (GitHub issue #349's sub-problem 2),
	// keyed by absolute resource instance address. Set by PriorState, read
	// once by the caller through [statelessRunner.RootOutputData] at the one
	// moment root outputs are evaluated - which is after PriorState has
	// returned and the provider instances that produced these values are
	// closed, which is the whole reason the values are carried on the runner
	// rather than read where they are used. Nil for a configuration whose
	// outputs reach no data source, which is nearly all of them.
	rootOutputData map[string]cty.Value

	// rootOutputStore is GitHub issue #349's sixth namespace in the record
	// store: what this estate remembers each root output's value to be. Set
	// wherever the record store itself is opened, nil for a run with no
	// record_store block. Written by the apply's write-back; read by
	// PriorState into recordedRootOutputs.
	rootOutputStore *projection.RootOutputStore

	// recordedRootOutputs is what rootOutputStore held for the outputs this
	// configuration declares, read by PriorState and handed to the caller
	// through [statelessRunner.RecordedRootOutputs] at the one moment root
	// outputs are evaluated. Carried on the runner for rootOutputData's
	// reason: it is read where the store is open and used a step later.
	recordedRootOutputs map[string]cty.Value

	// priorStateCalls counts how many times PriorState has run for this
	// runner. GitHub issue #80's pin: one runner serves one operation (see
	// this type's own doc comment), and backend_local.go's localRunDirect
	// calls PriorState exactly once per operation - one CLI invocation of
	// plain "choudoufu plan" or "choudoufu apply" must cost exactly one
	// PriorState cycle, never two, or the estate sweep and per-resource read
	// cost it pays are paid twice. Read only by tests, through
	// PriorStateCalls; not reset between calls, since a runner never outlives
	// the one operation that constructed it. Plain int, not atomic: PriorState
	// runs on the single goroutine backend_local.go's localRunDirect calls it
	// from, never concurrently with itself.
	priorStateCalls int

	// nodeResolve and resolver are GitHub issue #388's plan-node seam,
	// set once in statelessBegin from [nodeResolveEnabled] and populated
	// at the end of PriorState. resolver is nil whenever nodeResolve is
	// false (the default), which is also when it is never installed on
	// ContextOpts at all - see statelessBegin's own comment on why the
	// two are set together, in that order, before tofu.NewContext runs.
	nodeResolve bool
	resolver    *projection.NodeResolver
}

var _ backendLocal.StatelessRun = (*statelessRunner)(nil)

// StateMgr implements [backendLocal.StatelessRun].
func (r *statelessRunner) StateMgr() statemgr.Full {
	return r.mgr
}

// RootOutputData implements [backendLocal.StatelessRun].
func (r *statelessRunner) RootOutputData() map[string]cty.Value {
	return r.rootOutputData
}

// RecordedRootOutputs implements [backendLocal.StatelessRun].
func (r *statelessRunner) RecordedRootOutputs() map[string]cty.Value {
	return r.recordedRootOutputs
}

// PriorStateCalls returns how many times PriorState has run on this runner.
// Exists for the GitHub issue #80 regression pin (see priorStateCalls):
// a passing plan or apply must report exactly one.
func (r *statelessRunner) PriorStateCalls() int {
	return r.priorStateCalls
}

// PriorState implements [backendLocal.StatelessRun]: it runs the whole
// stateless pipeline and returns the projection.
//
// The order is the one internal/command/live_plan.go documents and for
// the same reasons - lint before anything reads the cloud, the estate name
// read out of the configuration before stamping writes into it, discovery
// before stamping so that count instances get the slots the live set already
// uses, and the projection before the schemas that stamping needs. Lint runs
// after the providers are launched, schemas in hand, for the same reason
// live-plan's does: a type with no admission-table row can still pass when
// the provider's own identity schema describes it completely enough. See
// [lint.CheckWith]. The difference from live-plan is the ending: instead of
// planning here, the projection is handed back and the ordinary operation
// plans (and applies) with it.
func (r *statelessRunner) PriorState(ctx context.Context, config *configs.Config, core *tofu.Context) (*states.State, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	r.priorStateCalls++

	estate, estateDiags := r.estateName(ctx, config)
	diags = diags.Append(estateDiags)
	if estateDiags.HasErrors() {
		return nil, diags
	}
	// GitHub issue #352's targeting scope, nil unless this run passed
	// -target or -exclude, and read off the same core context that will
	// build the real plan graph a moment from now. See
	// [statelessTargetScope].
	scope, scopeDiags := statelessTargetScope(ctx, core, config, r.targets, r.excludes)
	diags = diags.Append(scopeDiags)
	if scopeDiags.HasErrors() {
		return nil, diags
	}

	provs := newStatelessProviders(config, r.lib)

	// Read once and handed to both the subset check and resolution below, so
	// that a type the schemas admit reads the same answer at both points. See
	// internal/command/live_plan.go, which does the same.
	resourceSchemas := provs.resourceSchemas(ctx)

	// Subset check first: a configuration outside the stateless subset has to
	// fail with an explanation, and it has to fail before anything is read
	// from or written to the cloud. It runs after the providers are launched
	// now, schemas in hand, so a type with no admission-table row can still
	// pass when the provider's own identity schema describes it completely
	// enough, and a type schemas do refuse is explained in the identity
	// layer's own words. See [lint.CheckWith]. Nothing above this point reads
	// or writes the live system: newStatelessProviders only builds the
	// struct, and resourceSchemas reads unconfigured provider schemas, the
	// same schema-only call live-plan makes ahead of its own lint check.
	if issues := lint.CheckWith(ctx, config, lint.Context{Schemas: resourceSchemas}); len(issues) > 0 {
		diags = diags.Append(lint.Diagnostics(issues))
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}
	// GitHub issue #126's ruling: setting a write-only or sensitive argument
	// warns, never refuses, so it rides alongside the subset check rather
	// than gating on it. See [lint.CheckResidueAttributes].
	diags = diags.Append(lint.CheckResidueAttributes(config, resourceSchemas))

	// Resolved now that lint has passed and the estate name is settled, so
	// that any verb here is already known valid for its quadrant.
	if config.Module != nil {
		r.policy = statelessPolicy(config.Module.Live, estate)
	}

	// GitHub issue #73's record store, built now that lint has already
	// passed - which means every RECORD_ADMITTED resource in this
	// configuration either has one configured or was refused before this
	// point was ever reached - and the estate name is settled, which the
	// "ssm"/"s3" backends' default key namespace needs. A nil RecordStore
	// (a run with no record_store block) makes the hydration and
	// write-back paths below no-ops, exactly like a run with no live block
	// at all skips this whole file.
	var recordStoreCfg *configs.LiveRecordStore
	if config.Module != nil && config.Module.Live != nil {
		recordStoreCfg = config.Module.Live.RecordStore
	}
	if recordStoreCfg != nil {
		store, storeErr := projection.NewRecordStore(ctx, recordStoreCfg, estate, ".")
		if storeErr != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot open the record store", fmt.Sprintf(
				"The live block's record_store %q could not be opened: %s.", recordStoreCfg.Type, storeErr,
			)))
			diags = diags.Append(provs.close(ctx))
			return nil, diags
		}
		r.rawStore = store
		recordKeyPrefix := projection.RecordStoreKeyPrefix(recordStoreCfg, estate)
		// GitHub issue #364: one store now, for the record-backed
		// (kind=object), record-located (issue #270), residue (issue #275)
		// and provisioner-taint (issue #353) halves alike - all four read
		// and write through the same envelope at the same key. Before this
		// merge, the located/residue/provisioned namespaces deliberately
		// ignored any key_prefix override and stayed pinned to the estate
		// name alone; that asymmetry cannot survive the merge, since there
		// is only one key per instance now - an override moves all four
		// facts together, consistently, which is the intended consequence
		// of collapsing four namespace roots into one.
		r.recordStore = projection.NewRecordEnvelopeStore(store, recordKeyPrefix)
		// Issue #349's root-output namespace rides the same underlying
		// store, but stays a namespace of its own rather than joining the
		// envelope: orphan discovery never needs to see it, so it keeps the
		// ESTATE rather than recordKeyPrefix for the reason it always did -
		// a key_prefix override must not be able to move it under the
		// record root, where orphan discovery's listing would find it and
		// the plan would propose destroying whatever it names. An output
		// names no live object at all, so that would be a destroy proposal
		// for something that never existed.
		//
		// Read immediately, while the estate name is settled and the store
		// is open: [projection.ApplyRootOutputValues] runs several steps
		// later, from the backend, and needs the values then.
		r.rootOutputStore = projection.NewRootOutputStore(store, estate)
		r.recordedRootOutputs = projection.ReadRootOutputValues(ctx, r.rootOutputStore, config)
		r.liveConfig = config
		// Guided discovery's hint (issue #109) rides the same store: from
		// the apply's final persist onward, the estate's type roster and a
		// timestamp land at [projection.HintKey](estate), where the next
		// run's guided sweep reads them back. Enabled here rather than in
		// statelessBegin because the store and the settled estate name both
		// exist only now. A plan never persists, so a plan never writes one.
		r.mgr.EnableHint(store, estate, time.Now)
	}

	// GitHub issue #179's data-read phase, exactly as live-plan runs it:
	// data-source values that identity resolution demands are read now,
	// from the same configured provider instances the projection uses.
	// Fatal when a demanded source cannot be read; free when none is.
	dataResults, drDiags := statelessDataReads(ctx, config, provs, resourceSchemas, scope)
	diags = diags.Append(drDiags)
	if drDiags.HasErrors() {
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}

	// Resolution runs ahead of the providers being configured and is handed
	// their schemas, so that a type with no hand-written table row resolves
	// when the provider's own identity schema describes it completely enough.
	// See [identity.SynthesizeTypeIdentity].
	// The same two-pass resolution live-plan runs, through the same helper:
	// see [statelessResolve].
	resolutions, idDiags := statelessResolve(ctx, config, provs, resourceSchemas, dataResults, scope)
	if r.nodeResolve {
		// GitHub issue #388's plan-node seam, #364 unit B's own landing
		// note (item 3): a per-instance refusal here is the static
		// evaluator giving up, not proof the instance has no identity -
		// see identity.DowngradeForNodeResolution's own doc comment for
		// why only an InstanceFailure-tagged diagnostic is touched. The
		// instance stays absent from resolutions (Resolve's contract:
		// "an instance that could not be classified is absent from the
		// Result"), so it reaches the node with no prior state and
		// r.resolver gets the chance the static path never had.
		idDiags = identity.DowngradeForNodeResolution(idDiags)
	}
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		// Fatal on purpose. An identity map with holes in it produces a
		// projection with holes in it, and the run would create objects that
		// already exist.
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}

	// GitHub issue #313's provider-configuration dependency-order fixpoint,
	// now that resolution has settled: a provider block whose own arguments
	// read a data source - which may itself read a managed resource this
	// estate already owns - gets exactly the same value stock OpenTofu's
	// ordinary plan graph would supply once prior state exists. See
	// [statelessProviderDataReads]'s own doc comment for the mechanism and
	// for corpus-eks-basic, the estate this closes: this call was missing
	// here entirely until now - live-plan's own "-estate" form
	// (LivePlanCommand.livePlan) has carried it since 1c1b00324f, but a
	// configuration WITH a live block, which is what plain "choudoufu plan"/
	// "apply" and "live-plan" both run through for such a configuration
	// (LivePlanCommand.Run's own alias, above statelessBegin), reaches this
	// function instead, and nothing here ever called it. r.recordStore is
	// opened unconditionally above whenever the live block names a
	// record_store, regardless of the migration flag - unlike
	// recordShrinkStore below, this is not gated on r.nodeResolve, for the
	// same reason live-plan's own equivalent construction is not: reading a
	// GitHub issue #364 record-backed value that a PARENT_DERIVED formula
	// already names as a parent is not the #388 migration's concern.
	provs.providerDataResults = statelessProviderDataReads(ctx, config, provs, resourceSchemas, resolutions, r.recordStore)

	merged := resolutions.All()
	// GitHub issue #388's plan-node seam, edge 3: r.recordStore is opened
	// unconditionally above whenever the live block names a record_store,
	// regardless of the migration flag, so it is gated here rather than at
	// that assignment - only a flag-on run passes it to statelessDiscover
	// at all, which is what keeps a flag-off run's sweep demand
	// byte-identical no matter what the record store holds. See
	// statelessRecordBackedNeedsDiscoveryAddrs's own doc comment.
	var recordShrinkStore *projection.RecordStore
	if r.nodeResolve {
		recordShrinkStore = r.recordStore
	}
	// GitHub issue #361's crash-window recovery: read from r.recordStore -
	// the same unconditionally-open store just above, never
	// recordShrinkStore, which stays gated on r.nodeResolve for edge 3's
	// own unrelated reason - well before discovery.Discover runs. See
	// live_plan.go's own identical call for the fuller comment; this is
	// the "plain choudoufu plan/apply" path the comment two paragraphs up
	// already says carries the record store for real.
	deposedRecords := collectDeposedRecords(ctx, r.recordStore, resolutions.NeedsDiscovery())
	disco, discoProvider, undeclaredProviders, discoDiags := statelessDiscover(ctx, config, resolutions, estate, provs, r.policy, r.rawStore, r.view, recordShrinkStore, deposedRecords)
	diags = diags.Append(discoDiags)
	if discoDiags.HasErrors() {
		// A marker problem means the estate's ownership records disagree with
		// each other, and acting on them would act on the wrong resource.
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}
	if disco != nil {
		merged = disco.Resolutions
	}

	// GitHub issue #388's plan-node seam: the resolver was constructed
	// empty in statelessBegin (before tofu.NewContext existed to be handed
	// it) and is populated now, the first moment both of its data sources
	// exist - r.recordStore (open or nil a few lines above) and the
	// marker sweep's own resolutions, snapshotted into an address-keyed
	// index because the sweep itself has already finished by the time
	// anything calls the resolver. r.resolver is nil whenever
	// r.nodeResolve is false, so this whole block is a no-op for every run
	// that has not opted into the migration flag.
	if r.nodeResolve {
		r.resolver.RecordStore = r.recordStore
		r.resolver.MarkerIndex = projection.NewMarkerIndex(merged)
		r.resolver.NoSourceCreate = strict.CreatesFromNoSource(identity.NoSourceCreateFor(config))
		// GitHub issue #388's stamp half (AdjustConfigValue,
		// internal/live/projection/nodestamp.go): Estate and Selection are
		// exactly what internal/live/stamp's own Request carries for the
		// HCL path (stamp.Request.Estate, identity.SelectionFor(config)),
		// and Slots is the same disco.SlotTable() stampRes is built from
		// below - disco.SlotTable handles a nil disco already, the same way
		// stamp's own call site does.
		r.resolver.Estate = estate
		r.resolver.Selection = identity.SelectionFor(config)
		r.resolver.Slots = disco.SlotTable()
	}

	// GitHub issue #67's undeclared_untagged = "delete" scoped account
	// reconciliation, when the policy asks for it. Its roster is merged in
	// exactly like a swept orphan: a resolution with no matching declared
	// address is proposed for destruction by the plan engine's ordinary
	// orphan handling, with no synthetic configuration needed. A threshold
	// refusal stops the run here, after the report below has a chance to
	// show the roster that tripped it.
	reconcile, reconcileExtra, reconcileVerified, reconcileDiags := statelessPolicyReconcile(ctx, estate, r.policy, provs, discoProvider)
	diags = diags.Append(reconcileDiags)
	if len(reconcileExtra) > 0 {
		merged = append(merged, reconcileExtra...)
	}
	if reconcileDiags.HasErrors() {
		r.view.Policy(statelessPolicyReport(nil, disco, nil, reconcile))
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}

	// The provider the sweep listed through is the one a resource whose block
	// was deleted is read back through: it has no block to name one, and the
	// account and region it was found in are the account and region it is in.
	// An estate whose managed resources span more than one provider
	// configuration (issue #69) attributes each undeclared instance to its
	// own provider via undeclaredProviders; discoProvider is the fallback
	// and the single-provider case's only answer, unchanged.
	//
	// Ownership is the admission rule for the prior state itself: a live
	// object enters it only when it carries this estate's marker, or when
	// discovery already bound it by one. An apply is the path where that
	// matters most - the prior state here is what a destroy is planned
	// against - and it is why a resource this configuration names but this
	// estate has never owned is left alone rather than adopted, unless a
	// policy verb says otherwise.
	projResult, projDiags := projection.BuildWith(ctx, config, merged, provs, projection.Options{
		UndeclaredProvider:  discoProvider,
		UndeclaredProviders: undeclaredProviders,
		Ownership:           statelessOwnershipWith(estate, disco, r.policy, reconcileVerified),
		RecordStore:         r.recordStore,
		// dataResults (statelessDataReads, above) is issue #179's own
		// data-read phase output - already read, already paid for. See
		// [projection.Options.DataResults]'s doc comment for why the
		// projection's own tags/attrs seeding wants it too.
		DataResults: dataResults,
		// GitHub issue #361's crash-window recovery: the deposed objects
		// discovery's collision branch matched against deposedRecords,
		// above. disco.DeposedBindingsList() is nil-safe for a disco this
		// pass never produced.
		DeposedBindings: disco.DeposedBindingsList(),
	})
	// GitHub issue #349's root-output data reads, taken here because this is
	// the last moment the provider instances that read the live system are
	// still open - [projection.ApplyRootOutputValues] runs after PriorState
	// returns, by which point they are closed. Scoped: whatever this cannot
	// read costs one root output its prior value and nothing else, so its
	// diagnostics are collected and the run continues regardless.
	outputData, outputDataDiags := statelessRootOutputDataReads(ctx, config, provs, resourceSchemas, scope)
	diags = diags.Append(outputDataDiags)
	r.rootOutputData = outputData

	// The provider processes started to read the live system have done their
	// job by this point; the plan below starts its own from the same library.
	diags = diags.Append(provs.close(ctx))
	// Kept regardless of what happens next in this function: WriteBack
	// (called later, after a successful apply) needs this run's plan-time
	// versions even if a later step in PriorState fails and the run aborts
	// applying nothing - in which case WriteBack is simply never called,
	// and this is harmless to have set.
	r.recordVersions = projResult.RecordVersions
	r.envelopeVersions = projResult.EnvelopeVersions
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return nil, diags
	}

	r.view.Omissions(statelessOmissions(projResult))
	r.view.Unowned(statelessUnownedReport(projResult, estate))

	// GitHub issue #388's plan-node seam: r.resolver's ownership guard
	// (NodeResolver.Unowned, noderesolver.go step (c)'s own doc comment)
	// can only be set now, not alongside RecordStore/MarkerIndex/Estate
	// above, because projResult - the pre-walk projection that actually
	// decided ownership - did not exist yet at that point in this
	// function. See that field's own doc comment for why leaving it unset
	// would let the node adopt a client-named resource this run does not
	// own.
	if r.nodeResolve {
		r.resolver.Unowned = nodeResolverUnownedSet(projResult.Unowned)
	}

	var classified *foreign.Result
	if disco != nil {
		var foreignDiags tfdiags.Diagnostics
		classified, foreignDiags = foreign.Classify(ctx, foreign.Request{
			Estate:    disco.Estate,
			Config:    config,
			Discovery: disco,
			// The adoption hint carries the region and endpoint the
			// resources were listed through, so that pasting it talks to
			// the same cloud the plan just read.
			Region:      provs.region(discoProvider),
			EndpointURL: provs.endpointURL(discoProvider),
		})
		diags = diags.Append(foreignDiags)
		if foreignDiags.HasErrors() {
			return nil, diags
		}
		r.view.Foreign(statelessForeignReport(classified))
		r.view.GuidedFallback(disco.GuidedFallback)
	}

	// The schemas are read before the plan rather than after it because
	// stamping needs them: which resource types can carry an ownership marker
	// is a question only the provider's schema answers.
	schemas, schemaDiags := core.Schemas(ctx, config, projResult.State)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return nil, diags
	}

	// Marker stamping, by rewriting the configuration the plan is about to
	// read. A marker conflict is fatal: the configuration claims an ownership
	// this run cannot honor. So is a resource whose identity the provider
	// assigns going unstamped - this is the apply path, so an unmarked create
	// here is a resource lost to every future run. policyUntag carries
	// declared_tagged = "untag"'s released keys, worked out from the
	// projection's own policy outcomes now that it has run.
	policyUntag := statelessPolicyUntagMap(projResult.Policy, statelessPolicyTagKey(r.policy))
	recordBackedBlocks, recordBlocksDiags := recordBackedNeedsDiscoveryBlocks(ctx, recordShrinkStore, resolutions.NeedsDiscovery())
	diags = diags.Append(recordBlocksDiags)
	if recordBlocksDiags.HasErrors() {
		return nil, diags
	}
	stampRes, stampDiags := statelessStamp(ctx, config, estate, schemas, disco.SlotTable(), statelessNeedsDiscovery(resolutions), policyUntag, recordBackedBlocks)
	diags = diags.Append(stampDiags)
	if stampDiags.HasErrors() {
		return nil, diags
	}

	r.view.Policy(statelessPolicyReport(projResult, disco, stampRes, reconcile))

	// GitHub issue #67's undeclared_tagged = "untag" verb: the resources
	// applyOrphanPolicy withheld from the sweep because a non-default verb
	// governs them, narrowed to the ones this run's policy actually named
	// "untag" rather than "keep" or "report". Captured here, for
	// AfterApply, rather than acted on now: this method also runs for a
	// plan, and a plan must never write to the live system.
	r.untagTargets = statelessUntagTargets(disco)
	r.untagKey = statelessPolicyTagKey(r.policy)
	r.untagProvider = discoProvider
	r.untagConfig = config

	return projResult.State, diags
}

// WriteBack implements [backendLocal.StatelessRun]: GitHub issue #73's
// write-back, delegated straight to [projection.WriteBack] with the store,
// namespace and plan-time versions PriorState settled. A run with no
// record_store block (r.recordStore nil) is a no-op, the same "nothing
// configured, nothing happens" contract every optional live-block feature
// in this file follows.
func (r *statelessRunner) WriteBack(ctx context.Context, finalState *states.State, schemas *tofu.Schemas) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	// Issue #275's residue classifier is the one write-back half that needs
	// a live provider, and PriorState's are long closed by now, so this
	// opens its own and closes them again - exactly what AfterApply already
	// does below and for the same reason. Only when there is a record
	// store to write to: a run with no record_store pays nothing.
	var provs *statelessProviders
	if r.recordStore != nil && r.liveConfig != nil {
		provs = newStatelessProviders(r.liveConfig, r.lib)
	}

	var provAccess projection.Providers
	if provs != nil {
		provAccess = provs
	}

	diags = diags.Append(projection.WriteBack(ctx, projection.WriteBackRequest{
		Store:            r.recordStore,
		PriorVersions:    r.recordVersions,
		EnvelopeVersions: r.envelopeVersions,
		Providers:        provAccess,
		FinalState:       finalState,
		Schemas:          schemas,

		// Issues #270 and #353's halves both need Config: the located half
		// asks the `markers "record"` selection, and the provisioned half
		// asks whether this instance's resource block declares a
		// create-time provisioner - neither is answerable from the final
		// state alone.
		Config: r.liveConfig,

		// Issue #349's half. The apply just settled these values, and this
		// is the moment stock writes them into its state file.
		RootOutputStore: r.rootOutputStore,
	}))

	if provs != nil {
		diags = diags.Append(provs.close(ctx))
	}
	return diags
}

// AfterApply implements [backendLocal.StatelessRun]: the untag verb's
// apply-time release, run once a real apply - never a plan - has finished
// changing the live system. See this type's untagTargets field for why the
// work was captured during PriorState rather than computed here, and
// internal/live/untag for the release itself.
//
// The providers PriorState listed through are already closed by the time
// this runs (see this file's "The provider double-launch" doc comment), so
// this launches its own, exactly the way [statelessPolicyReconcile]'s
// caller already does for the same reason - and closes it again before
// returning, since nothing after this point needs it.
func (r *statelessRunner) AfterApply(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if len(r.untagTargets) == 0 {
		return diags
	}

	provs := newStatelessProviders(r.untagConfig, r.lib)
	provider, err := provs.ConfiguredProvider(ctx, r.untagProvider)
	if err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Provider unavailable for the apply-time tag release",
			fmt.Sprintf(
				"GitHub issue #67's undeclared_tagged = \"untag\" verb has %d resource(s) to release %q from, but provider %s could not be used to release it: %s. Nothing was changed; the resources involved are still live and still carry the tag.",
				len(r.untagTargets), r.untagKey, r.untagProvider, err,
			),
		))
		diags = diags.Append(provs.close(ctx))
		return diags
	}

	result, releaseDiags := untag.Release(ctx, provider, r.untagKey, r.untagTargets)
	diags = diags.Append(releaseDiags)
	diags = diags.Append(provs.close(ctx))

	r.view.Policy(statelessReleasedReport(result))

	return diags
}

// estateName settles which estate this run is about, from the stateless
// block or from the tofu-estate tags the configuration stamps.
//
// Unlike "choudoufu live-plan", where no estate name is a warning and the run
// degrades into one that finds nothing and stamps nothing, here it is an
// error. The live block is an explicit statement that this configuration
// has no state, and the only thing standing in for state is the markers; a
// run that proceeded without an estate name would create live resources
// carrying no ownership record, which the next run would report as foreign.
// That is not a degraded stateless run, it is a broken estate.
func (r *statelessRunner) estateName(ctx context.Context, config *configs.Config) (string, tfdiags.Diagnostics) {
	estate, declared, diags := statelessEstateFor(ctx, r.settings.Estate, config)
	if diags.HasErrors() {
		return "", diags
	}
	if estate != "" {
		return estate, diags
	}

	subject := r.settingsRange()
	switch len(declared) {
	case 0:
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "No estate named by the live block",
			Detail: fmt.Sprintf(
				"A live-markers run recovers what it owns from the ownership markers on the live resources, so every run needs the estate name those markers carry. Nothing in this configuration stamps a %s tag with a value readable from configuration alone. Name it in the %s sidecar file beside the configuration:\n\n  estate = \"my-estate\"\n\nor in the live block:\n\n  terraform {\n    live {\n      estate = \"my-estate\"\n    }\n  }",
				discovery.TagEstate, configs.LiveSidecarFilename,
			),
			Subject: subject,
		})
	case 1:
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Invalid estate name in configuration",
			Detail: fmt.Sprintf(
				"The configuration stamps %s = %q, which does not match the marker grammar in live/MARKERS.md, so it cannot name this estate. Set a valid name with the live block's estate argument.",
				discovery.TagEstate, declared[0],
			),
			Subject: subject,
		})
	default:
		return "", diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Several estates named by the configuration",
			Detail: fmt.Sprintf(
				"Resources in this configuration stamp %s with %s. An estate is the unit of ownership and a run covers exactly one. Set the live block's estate argument to say which.",
				discovery.TagEstate, joinAnd(quoteAll(declared)),
			),
			Subject: subject,
		})
	}
}

// settingsRange is what a diagnostic about the estate points at: the "estate"
// argument when the block wrote one, and the block header otherwise.
//
// In practice the three callers in estateName always land on the header,
// because an estate argument that named something usable would have settled
// the name before they ran. The argument case is here so that the rule is the
// one stated on [configs.Live.EstateRange] rather than one this file happens
// to get away with.
func (r *statelessRunner) settingsRange() *hcl.Range {
	if r.settings == nil {
		return nil
	}
	if r.settings.EstateSet {
		return r.settings.EstateRange.Ptr()
	}
	return r.settings.DeclRange.Ptr()
}

func joinAnd(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	out := ""
	for i, n := range names[:len(names)-1] {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out + " and " + names[len(names)-1]
}
