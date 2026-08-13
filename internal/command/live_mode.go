// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/hcl/v2"

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
	"github.com/intentius/choudoufu/internal/live/projection"
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

	mgr := projection.NewManager()
	if settings.SnapshotPath != "" {
		// The estate name is not settled yet - it may still need to be
		// derived from the configuration's own tags, which happens later in
		// PriorState - so this only turns the snapshot on; the estate field
		// it records is filled in by statelessRunner.estateName once known.
		// See [projection.Manager.EnableSnapshot] for why "nobody called
		// this" is what "no snapshot_path attribute" has to mean.
		mgr.EnableSnapshot(settings.SnapshotPath, settings.Estate, time.Now)
	}
	if settings.Snapshots {
		// The branch carrier: one commit per apply on the repository
		// enclosing the module directory, which is this process's working
		// directory - the same "." every command's config load reads from.
		// When snapshot_path is set too, the manager treats the file as the
		// fallback for a directory with no enclosing repository; see
		// [projection.Manager.EnableSnapshotBranch].
		mgr.EnableSnapshotBranch(".", settings.Estate, time.Now)
	}

	runner := &statelessRunner{
		settings: settings,
		lib:      local.ContextOpts.Plugins,
		mgr:      mgr,
		view:     views.NewStatelessPlan(view),
	}
	if testStatelessRunner != nil {
		testStatelessRunner(runner)
	}
	local.Stateless = runner

	// The manager's Lock is already a no-op, so this is redundant on purpose.
	// It also spares the operator the "Acquiring state lock" message for a
	// lock that does not exist.
	opReq.StateLocker = clistate.NewNoopLocker()

	return diags
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
			"A saved plan file records the state snapshot the plan was made against, and a live-markers run has no state snapshot: its prior state is rebuilt from the live system every time. Rerun without -out, and apply directly - a live-markers apply plans against the live system at the moment it runs.")
	}
	if planFile != "" {
		diags = diags.Append(statelessRejectPlanFile(planFile))
	}
	if generateConfigOut != "" {
		reject("Config generation is not available under live resource markers yet",
			"-generate-config-out generates configuration for import blocks, which a live-markers run does not process yet. Rerun without -generate-config-out.")
	}
	if op != nil && op.PlanMode != plans.NormalMode {
		reject("Only the normal planning mode is available under live resource markers",
			"Live resource markers v0 produce and apply normal plans. -refresh-only compares a stored record against the live system, which is the comparison a live-markers run has no stored side for; -destroy is not verified against a live-markers apply yet, and removing a resource from the configuration is the tested way to have it destroyed. Rerun without -destroy and -refresh-only.")
	}
	if state != nil && (state.StatePath != "" || state.StateOutPath != "" || state.BackupPath != "") {
		reject("State file options are not available under live resource markers",
			"There is no state file to read, write or back up: prior state is a projection built from the live system and discarded when the run ends. Rerun without -state, -state-out and -backup.")
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

	lib  plugins.Library
	mgr  *projection.Manager
	view views.StatelessPlan
}

var _ backendLocal.StatelessRun = (*statelessRunner)(nil)

// StateMgr implements [backendLocal.StatelessRun].
func (r *statelessRunner) StateMgr() statemgr.Full {
	return r.mgr
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

	estate, estateDiags := r.estateName(ctx, config)
	diags = diags.Append(estateDiags)
	if estateDiags.HasErrors() {
		return nil, diags
	}
	// The estate the snapshot (P4.2) records may have been unknown when the
	// manager was constructed - the live block can leave it to be
	// derived from configuration tags, which is exactly what estateName just
	// did - so it is set here, now that it is settled. A no-op when the
	// snapshot was never enabled. Settling the estate name and recording it
	// on the manager touches nothing outside this process - no cloud read, no
	// cloud write - so doing it ahead of lint below is harmless: see
	// [projection.Manager.SetSnapshotEstate].
	r.mgr.SetSnapshotEstate(estate)

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

	// Resolution runs ahead of the providers being configured and is handed
	// their schemas, so that a type with no hand-written table row resolves
	// when the provider's own identity schema describes it completely enough.
	// See [identity.SynthesizeTypeIdentity].
	resolutions, idDiags := identity.ResolveWith(ctx, config, identity.Context{
		Schemas: resourceSchemas,
	})
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		// Fatal on purpose. An identity map with holes in it produces a
		// projection with holes in it, and the run would create objects that
		// already exist.
		diags = diags.Append(provs.close(ctx))
		return nil, diags
	}

	merged := resolutions.All()
	disco, discoProvider, discoDiags := statelessDiscover(ctx, config, resolutions, estate, provs)
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

	// The provider the sweep listed through is the one a resource whose block
	// was deleted is read back through: it has no block to name one, and the
	// account and region it was found in are the account and region it is in.
	//
	// Ownership is the admission rule for the prior state itself: a live
	// object enters it only when it carries this estate's marker, or when
	// discovery already bound it by one. An apply is the path where that
	// matters most - the prior state here is what a destroy is planned
	// against - and it is why a resource this configuration names but this
	// estate has never owned is left alone rather than adopted.
	projResult, projDiags := projection.BuildWith(ctx, config, merged, provs, projection.Options{
		UndeclaredProvider: discoProvider,
		Ownership:          statelessOwnership(estate, disco),
	})
	// The provider processes started to read the live system have done their
	// job by this point; the plan below starts its own from the same library.
	diags = diags.Append(provs.close(ctx))
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return nil, diags
	}

	r.view.Omissions(statelessOmissions(projResult))
	r.view.Unowned(statelessUnownedReport(projResult, estate))

	if disco != nil {
		classified, foreignDiags := foreign.Classify(ctx, foreign.Request{
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
	// here is a resource lost to every future run.
	_, stampDiags := statelessStamp(ctx, config, estate, schemas, disco.SlotTable(), statelessNeedsDiscovery(resolutions))
	diags = diags.Append(stampDiags)
	if stampDiags.HasErrors() {
		return nil, diags
	}

	return projResult.State, diags
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
				"A live-markers run recovers what it owns from the ownership markers on the live resources, so every run needs the estate name those markers carry. Nothing in this configuration stamps a %s tag with a value readable from configuration alone. Name it in the block:\n\n  terraform {\n    live {\n      estate = \"my-estate\"\n    }\n  }",
				discovery.TagEstate,
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
