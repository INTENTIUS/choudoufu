// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/backend"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/foreign"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// LivePlanCommand plans a configuration with no authoritative state: no
// backend, no lock. Prior state is a projection, rebuilt by reading the
// live system at the start of the run and discarded when the run ends -
// this standalone command neither reads nor writes the #685 state cache,
// which belongs to plain plan/apply's StatelessRun seam (live_mode.go's
// runner wires it); a diagnostic command that wrote the cache would
// overwrite the last real run's snapshot with its own.
//
// The pipeline is lint -> identity -> discovery -> projection -> the ordinary
// plan engine:
//
//  1. The configuration is loaded exactly as the plan command loads it, and
//     no backend is prepared. There is deliberately no state manager of any
//     kind, not even an in-memory one, because there is no operation here
//     that reads or writes a state snapshot: the prior state is passed to
//     [tofu.Context.Plan] as a value and the resulting plan is rendered and
//     dropped. Avoiding the backend rather than stubbing it is what makes
//     "no state was read or written" a structural property instead of a
//     promise, and it is why this command works in a directory whose backend
//     was never initialized.
//  2. The providers the configuration names are launched from the ordinary
//     plugin library (the providercache that "choudoufu init" populated),
//     unconfigured, far enough to read their resource identity schemas.
//  3. [lint.CheckWith] decides whether the configuration is in the stateless
//     subset at all, with those schemas in hand: a type absent from the v0
//     admission table still passes when the schemas describe it completely
//     enough (see [identity.SynthesizeTypeIdentity]), and a refused type is
//     explained in the identity layer's own words. Any remaining issue is
//     fatal.
//  4. [identity.Resolve] classifies every instance, from the same schemas
//     lint just used. Error diagnostics are fatal, because a partial
//     identity map plans creates for things that already exist. The
//     providers are configured only later, once a projection needs to make
//     provider calls.
//  5. [discovery.Discover] lists the live resources of every type that has an
//     instance waiting on marker discovery, binds the ones carrying this
//     estate's markers, and - because it is asked for unclaimed resources
//     too - brings back everything of those types that carries no marker at
//     all. A count block's instances are bound as a set rather than one
//     address at a time, so the live members past the declared count come
//     back at the instance addresses above it and the plan below destroys
//     them the way it destroys any shrunken count's leftovers. When the
//     estate's managed resources span more than one provider configuration
//     (aliased providers, typically multi-region), statelessDiscover runs
//     this step once per distinct provider configuration and
//     [discovery.Merge] combines the results into one, so this step still
//     reads as "discovery ran" even though it may have run several times
//     under the hood (issue #69).
//  6. [foreign.Classify] sorts those unclaimed resources into foreign, bind
//     candidate and other-estate, and reports the owned resources sitting at a
//     for_each key the configuration no longer declares as rename candidates.
//     All of it goes into one section.
//     Nothing it finds is ever fed back into the run: an unclaimed resource
//     has no declared address, so it never enters the prior state, and the
//     plan engine has nothing to propose destroying. That is the protection
//     property, and it holds by construction rather than by a filter someone
//     has to remember to apply.
//  7. [projection.BuildFrom] materializes the prior state from the merged
//     resolutions, admitting a live object only when it carries this estate's
//     ownership marker or discovery already bound it by one. That check is
//     what keeps a client-named resource - whose identity comes out of the
//     configuration and which therefore never passes through step 5 or step 6
//     at all - from being adopted on the strength of a name. Whatever it could
//     not read, and whatever it refused, is reported in its own section above
//     the plan, which is the transparency surface for "why does this plan
//     propose a create".
//  8. [stamp.Stamp] injects this estate's ownership markers into every
//     taggable resource whose configuration does not already declare them, by
//     rewriting the resource bodies before the plan reads them. That is what
//     makes markers a property of the tool rather than of the author's
//     discipline: the plan below shows the tags being added, and an apply of
//     it writes them. A marker the configuration declares and this run
//     disagrees with is fatal rather than overwritten. Count instances also
//     get their tofu-slot tag here, from the assignment discovery worked out
//     in step 5 - which is why stamping runs after discovery and not before.
//  9. The plan runs with refresh disabled, because the projection was built
//     from live reads moments earlier and refreshing it would read every
//     object a second time to learn the same thing.
type LivePlanCommand struct {
	Meta
}

func (c *LivePlanCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()

	// Kept for the alias below, which hands the plan command the arguments
	// exactly as they arrived so that it can parse them itself. This must be
	// an independent copy, not just a second slice header over the same
	// backing array: arguments.ParseView compacts recognized flags (like
	// -no-color) out of its argument slice IN PLACE, and without a copy here
	// that compaction silently overwrites originalArgs's later elements too
	// (observed concretely as -target runs reaching the plan-command alias
	// with -no-color gone from originalArgs and the last -target duplicated
	// into the slot -no-color used to occupy - a real, narrow bug, not a
	// hypothetical one).
	originalArgs := append([]string(nil), rawArgs...)

	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	// The stock plan flag set plus -estate, so that -target, -var, -var-file
	// and friends parse and behave identically and this command's own option
	// parses like any of them. See statelessEstateName for what -estate is
	// for and what happens when it is absent. Options this command cannot
	// honor are rejected below rather than silently ignored.
	args, closer, diags := arguments.ParseLivePlan(rawArgs)
	defer closer()
	estateFlag := args.Estate

	c.View.SetShowSensitive(args.ShowSensitive)
	c.View.SetVerbose(args.Verbose)

	view := views.NewPlan(args.ViewOptions, c.View)

	if diags.HasErrors() {
		view.Diagnostics(diags)
		view.HelpPrompt()
		return 1
	}

	var err error
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(err)
		view.Diagnostics(diags)
		return 1
	}

	c.Meta.input = args.ViewOptions.InputEnabled
	c.Meta.parallelism = args.Operation.Parallelism
	c.Meta.variableArgs = args.Vars.All()

	// Alias. When the configuration carries a live block, plain
	// "choudoufu plan" is this pipeline, so this command is that command - down to
	// the flag set, since delegating means the plan command parses the
	// original arguments itself. The only difference is -estate, which the
	// block replaces: accepting it here would let a run name an estate the
	// configuration disagrees with, which is the ambiguity the block exists
	// to remove.
	//
	// This is as early as the check can happen and no earlier. Reading the
	// configuration means resolving the root module call, which is cached and
	// which needs the -var values above; asking before they are set would
	// answer the rest of the run's questions with the wrong variables.
	//
	// Load errors are tolerated rather than reported: a configuration that
	// will not load is not evidence either way, and the ordinary path below
	// reports the problem in its own voice.
	if settings, _ := c.statelessSettings(ctx, true); settings != nil {
		if estateFlag != "" {
			// Half of this conflict is the flag and half is the block; the
			// block is the half with a source range, so it is what the
			// diagnostic points at.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Estate named by both the live block and -estate",
				Detail:   fmt.Sprintf("This configuration's live block is what names its estate, and -estate=%q would name a second one for this run only. Remove the flag; \"choudoufu plan\" and \"choudoufu apply\" run this pipeline directly.", estateFlag),
				Subject:  settings.DeclRange.Ptr(),
			})
			view.Diagnostics(diags)
			return 1
		}
		plan := &PlanCommand{Meta: c.Meta}
		return plan.Run(originalArgs)
	}

	// Past the alias, so this run is the -estate form for certain, and
	// [surfaceEstateFlag] is the surface whose refusals apply. GitHub issue
	// #619: this check used to run BEFORE the alias above, out of one of two
	// refusal sets that had already drifted apart on -destroy. That put the
	// wrong list in front of a live-block configuration - "choudoufu
	// live-plan -destroy" was refused where "choudoufu plan -destroy" in the
	// same directory ran, from a command whose whole contract is that it IS
	// that command - and printed a -out diagnostic asserting "this
	// configuration has no live block" over one that had a live block. Both
	// go away by asking after the surface is known, from the one list.
	//
	// Rendered through the base view rather than the plan view: one of the
	// things rejected here is -json, and reporting "no JSON output" as JSON
	// would be a strange way to say it.
	if moreDiags := statelessRejections(surfaceEstateFlag, args.Operation, args.State, args.ViewOptions, args.OutPath, args.GenerateConfigPath, ""); moreDiags.HasErrors() {
		c.View.Diagnostics(moreDiags)
		return 1
	}

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())
	diags = diags.Append(c.liveStateFileNote())
	diags = diags.Append(c.checkAWSProviderVersionSkew())

	// GitHub issue #587. This is the "-estate" form, which by definition has
	// no live block (a configuration that has one was delegated to
	// PlanCommand above), so the flag is honoured here rather than refused:
	// this pipeline IS a live-markers run. Both halves of the mode are
	// picked here - the ledger renderer, and the wrapper that drops the
	// resource diff.
	statelessView := statelessPlanView(c.View, args.AdoptionOnly)
	if args.AdoptionOnly {
		view = views.NewAdoptionOnlyPlan(view, c.View)
	}

	code, nextStep, moreDiags := c.livePlan(ctx, args.Plan, estateFlag, view, statelessView)
	diags = diags.Append(moreDiags)
	view.Diagnostics(diags)
	if diags.HasErrors() {
		return 1
	}
	// Ordered as a stock plan orders it: the plan, then the diagnostics
	// gathered along the way, then the next-step hint.
	if nextStep {
		view.Operation().PlanNextStep("", "")
	}
	return code
}

// statelessPlan runs the pipeline and returns the exit code the run earns if
// no diagnostics turn out to be errors, along with whether the caller should
// print the next-step hint. Diagnostics accumulated along the way are returned
// rather than rendered, except for the plan itself, which is rendered here so
// that it appears before the trailing diagnostics exactly as it does in a
// stock plan.
func (c *LivePlanCommand) livePlan(ctx context.Context, args *arguments.Plan, estateFlag string, view views.Plan, statelessView views.StatelessPlan) (int, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// GitHub issue #626's knob, resolved once here and used by both of this
	// function's read paths - the projection built below and the
	// provider-configuration fixpoint's own [projection.ReadInstances] calls,
	// which [statelessProviderDataReads] takes it as an argument for. Once,
	// because resolving it at each construction site would report a bad
	// setting twice; here, because reading an environment variable costs
	// nothing and a setting this run cannot honour should be refused before a
	// provider process is started or a single live call is made. See
	// [readParallelismSetting] for the setting, the refusal and the default
	// decision.
	readPar, readParDiags := readParallelismSetting()
	diags = diags.Append(readParDiags)
	if readParDiags.HasErrors() {
		return 1, false, diags
	}

	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return 1, false, diags
	}

	enc, encDiags := c.Encryption(ctx)
	diags = diags.Append(encDiags)
	if encDiags.HasErrors() {
		return 1, false, diags
	}

	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		diags = diags.Append(err)
		return 1, false, diags
	}
	coreOpts.Hooks = view.Hooks()
	coreOpts.Encryption = enc

	// GitHub issue #388's plan-node seam, on by default since 2026-08-25 and
	// opted out of with CHOUDOUFU_NODE_RESOLVE=0 (see
	// internal/command/live_mode.go's nodeResolveEnabled, which this
	// command shares - the flag is a build-migration switch, not a
	// property of one pipeline). This is the "-estate" flag form's own
	// seam: it never goes through statelessBegin/backend_local.go's
	// StatelessRun at all (see this file's own doc comment), so it needs
	// the identical construct-empty-then-populate wiring live_mode.go
	// uses, independently. Constructed here, before tofu.NewContext, for
	// the same reason: this is the last point coreOpts can still be
	// mutated.
	var resolver *projection.NodeResolver
	if nodeResolveEnabled() {
		resolver = &projection.NodeResolver{}
		coreOpts.ResourceIdentityResolver = resolver
		// GitHub issue #388's stamp half rides the same object and the same
		// flag - see live_mode.go's identical wiring and
		// [projection.NodeResolver.AdjustConfigValue]'s own doc comment for
		// why one resolver serves both interfaces.
		coreOpts.ConfigValueAdjuster = resolver
	}

	// Built here rather than just before the plan, where it used to be,
	// because the targeting scope below is read off the plan graph and the
	// plan graph is this object's to build. Constructing it starts nothing:
	// provider processes are launched lazily, when a schema is first asked
	// for, so an untargeted run pays exactly what it paid before.
	tfCtx, ctxDiags := tofu.NewContext(coreOpts)
	diags = diags.Append(ctxDiags)
	if ctxDiags.HasErrors() {
		return 1, false, diags
	}

	// GitHub issue #352's targeting scope, and nil unless this run passed
	// -target or -exclude. See [statelessTargetScope].
	scope, scopeDiags := statelessTargetScope(ctx, tfCtx, config, args.Operation.Targets, args.Operation.Excludes)
	diags = diags.Append(scopeDiags)
	if scopeDiags.HasErrors() {
		return 1, false, diags
	}

	provs := newStatelessProviders(config, coreOpts.Plugins)

	// Read once and handed to both the subset check and resolution below, so
	// that a type the schemas admit reads the same answer at both points.
	// Named apart from the tofu.Schemas the plan itself reads further down -
	// same provider processes, a different shape - so the two never collide.
	resourceSchemas := provs.resourceSchemas(ctx)

	// Subset check first: a configuration outside the stateless subset has
	// to fail with an explanation rather than as a confusing plan. It runs
	// after the providers are launched now, schemas in hand, so a type with
	// no admission-table row can still pass when the provider's own identity
	// schema describes it completely enough, and a type schemas do refuse is
	// explained in the identity layer's own words rather than only "not in
	// the table". See [lint.CheckWith].
	// Warning-severity issues (GitHub issue #210: [lint.RuleStateBackend] is
	// the first) are rendered but do not stop the run - only an error-
	// severity issue does, via [lint.HasErrors] rather than a bare len check.
	if issues := lint.CheckWith(ctx, config, lint.Context{Schemas: resourceSchemas}); len(issues) > 0 {
		diags = diags.Append(lint.Diagnostics(issues))
		if lint.HasErrors(issues) {
			diags = diags.Append(provs.close(ctx))
			return 1, false, diags
		}
	}
	// GitHub issue #126's ruling: setting a write-only or sensitive argument
	// warns, never refuses, so it rides alongside the subset check rather
	// than gating on it. See [lint.CheckResidueAttributes].
	diags = diags.Append(lint.CheckResidueAttributes(config, resourceSchemas))

	// GitHub issue #179's data-read phase, between the subset check and
	// resolution: when an identity, a count or a for_each needs a data
	// source's value, read it now, from the same configured provider
	// instances the projection uses, so resolution works from the
	// provider's own answer rather than refusing it as dynamic. Free when
	// nothing is demanded, fatal when a demanded source cannot be read.
	dataResults, drDiags := statelessDataReads(ctx, config, provs, resourceSchemas, scope)
	diags = diags.Append(drDiags)
	if drDiags.HasErrors() {
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}

	// Resolution runs ahead of the providers being configured, as it always
	// has, and is handed their schemas: a resource type the hand table has
	// never heard of resolves anyway when the provider's own identity schema
	// describes it completely enough. See [identity.SynthesizeTypeIdentity].
	// A run whose providers will not start gets no schemas and the hand
	// table's answers, which is exactly what it got before.
	//
	// A first pass that refuses is no longer fatal on its own: see
	// [statelessResolve] for the second pass and the bound on it.
	resolutions, idDiags := statelessResolve(ctx, config, provs, resourceSchemas, dataResults, scope)
	if resolver != nil {
		// #364 unit B's landing note (item 3), mirrored from
		// live_mode.go's PriorState: a per-instance static refusal
		// becomes a warning under the flag, and the instance - still
		// absent from resolutions - reaches the node resolver instead of
		// aborting the run. See identity.DowngradeForNodeResolution.
		idDiags = identity.DowngradeForNodeResolution(idDiags)
	}
	diags = diags.Append(idDiags)
	if idDiags.HasErrors() {
		// Fatal on purpose. An identity map with holes in it produces a
		// projection with holes in it, and the plan would propose creating
		// objects that already exist.
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}

	// The estate name, read from the same two sources discovery and stamping
	// read it from. Their diagnostics about it are raised below, in their own
	// voices; this call is for the ownership rule the projection needs, which
	// has to know the estate before anything is materialized. Moved ahead of
	// [statelessProviderDataReads] (it used to sit between that call and
	// [statelessDiscover]) because that fixpoint's own record-rung read
	// needs the record store before it, not after - see recordStoreForReads'
	// own comment below.
	estate, _, _ := statelessEstateFor(ctx, estateFlag, config)

	// The estate's record store, when the live block names one - opened
	// here originally only as guided discovery's hint source (issue #109),
	// now also read from directly by statelessProviderDataReads. A store
	// that will not open is not this command's error to fail on: the hint
	// is a plan-cost cache, so the run proceeds hintless (guided discovery
	// and the record-rung read both stay off) and everything below behaves
	// exactly as it always has.
	var hintStore staterecord.Store
	if config.Module != nil && config.Module.Live != nil && config.Module.Live.RecordStore != nil {
		store, storeErr := projection.NewRecordStore(ctx, config.Module.Live.RecordStore, estate, ".")
		if storeErr != nil {
			log.Printf("[WARN] live: could not open the record store for guided discovery's hint: %s", storeErr)
		} else {
			hintStore = store
		}
	}
	// recordStoreForReads is the same wrapper [statelessDiscover] gets below
	// as recordShrinkStore, built once here and unconditionally (unlike
	// recordShrinkStore, never gated on GitHub issue #388's migration
	// flag): [statelessProviderDataReads] reads only GitHub issue #364's
	// already-resolved ClassRecordBacked instances that a PARENT_DERIVED
	// formula names as a parent, by their own address, which costs nothing
	// when the store holds none and changes nothing about ordinary marker
	// discovery's own demand either way, so there is no byte-identical-
	// flag-off property to preserve here. NewRecordEnvelopeStore(nil, ...)
	// is nil, so a hintStore that would not open leaves this nil and
	// [projection.ReadInstances] omits a record-backed parent exactly as
	// it always did.
	recordStoreForReads := projection.NewRecordEnvelopeStore(hintStore, recordKeyPrefixFor(config, estate))

	// GitHub issue #313's provider-configuration dependency-order fixpoint,
	// now that resolution has settled: a provider block whose own arguments
	// read a data source - which may itself read a managed resource this
	// estate already owns - gets exactly the same value stock OpenTofu's
	// ordinary plan graph would supply once prior state exists. Free when
	// no provider block names a data source at all, which is every estate
	// before this existed. Never fatal on its own: the same "Provider
	// unavailable" diagnostic providerConfigValue has always raised for
	// what this cannot resolve fires unchanged, later, when something
	// actually tries to configure that provider.
	provs.providerDataResults = statelessProviderDataReads(ctx, config, provs, resourceSchemas, resolutions, recordStoreForReads, readPar)

	// Resolved now that lint has passed and the estate name is settled, so
	// that any verb here is already known valid for its quadrant (see
	// internal/live/lint's checkLivePolicy).
	var pol *policy.Policy
	if config.Module != nil {
		pol = statelessPolicy(config.Module.Live, estate)
		log.Printf("[TRACE] live: ownership policy: %s", pol)
	}

	// GitHub issue #388's plan-node seam, edge 3: the same record-store
	// wrapper recordStoreForReads is built above, unconditionally now, so
	// this is a reuse rather than a second construction. Gated on
	// resolver != nil (the migration flag) rather than just on hintStore
	// being non-nil: a flag-off run must see a byte-identical marker-sweep
	// demand no matter what the record store holds, so this stays nil
	// whenever the flag itself is off.
	var recordShrinkStore *projection.RecordStore
	if resolver != nil {
		recordShrinkStore = recordStoreForReads
	}

	// GitHub issue #361's crash-window recovery: one GetDeposed read per
	// needs-discovery address, from recordStoreForReads - the same
	// unconditionally-open envelope store [statelessProviderDataReads]
	// already reads directly, above, and NOT recordShrinkStore, which
	// stays gated on the CHOUDOUFU_NODE_RESOLVE=1 migration flag for edge
	// 3's own unrelated reason. Read here, before [discovery.Discover]
	// ever runs, and handed to it through [discovery.Request.DeposedRecords]
	// - see that field's own doc comment for what consumes it.
	deposedRecords := collectDeposedRecords(ctx, recordStoreForReads, resolutions.NeedsDiscovery())

	// Marker discovery, when anything is waiting on it. Its output is a
	// resolution list with the discovered instances made concrete, plus the
	// unclaimed live resources the classifier below sorts out.
	merged := resolutions.All()
	disco, discoProvider, undeclaredProviders, discoDiags := statelessDiscover(ctx, config, resolutions, estateFlag, provs, pol, hintStore, statelessView, recordShrinkStore, deposedRecords, nil, args.AdoptionOnly)
	diags = diags.Append(discoDiags)
	if discoDiags.HasErrors() {
		// A marker problem means the estate's ownership records disagree with
		// each other, and a projection built on them would act on the wrong
		// resource. Never proceed past one.
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}
	if disco != nil {
		merged = disco.Resolutions
	}

	// GitHub issue #388's plan-node seam: populate the resolver constructed
	// empty above, now that both its data sources exist. hintStore/estate
	// give it the same record store the projection below reads (nil for
	// this command's own "-estate" form whenever the configuration
	// declares no live block at all - see hintStore's own doc comment a
	// few lines up - which is the ordinary case for this flag-only form
	// and simply means step (a) never has anything to find); merged is
	// the marker sweep's own resolutions, snapshotted into an index.
	if resolver != nil {
		resolver.RecordStore = recordShrinkStore
		resolver.MarkerIndex = projection.NewMarkerIndex(merged)
		resolver.NoSourceCreate = strict.CreatesFromNoSource(identity.NoSourceCreateFor(config))
		// GitHub issue #388's stamp half: the same estate name and
		// markers-record selection statelessStamp is about to hand
		// stamp.Request below, and the same disco.SlotTable() its Slots
		// field reads (disco.SlotTable handles a nil disco already).
		resolver.Estate = estate
		resolver.Selection = identity.SelectionFor(config)
		resolver.Slots = disco.SlotTable()
	}

	// GitHub issue #67's undeclared_untagged = "delete" scoped account
	// reconciliation, when the policy asks for it. See live_mode.go's
	// PriorState for why the roster merges in the same way a swept orphan
	// does, and why a threshold refusal stops here after the report has a
	// chance to show what tripped it.
	reconcile, reconcileExtra, reconcileVerified, reconcileDiags := statelessPolicyReconcile(ctx, estate, pol, provs, discoProvider)
	diags = diags.Append(reconcileDiags)
	if len(reconcileExtra) > 0 {
		merged = append(merged, reconcileExtra...)
	}
	if reconcileDiags.HasErrors() {
		statelessView.Policy(statelessPolicyReport(nil, disco, nil, reconcile))
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}

	projResult, projDiags := projection.BuildWith(ctx, config, merged, provs, projection.Options{
		UndeclaredProvider:  discoProvider,
		UndeclaredProviders: undeclaredProviders,
		Ownership:           statelessOwnershipWith(estate, disco, pol, reconcileVerified),
		// GitHub issue #364: one store for GitHub issue #270's record-located
		// instances (the reason this is wired at all - without it nothing
		// can say which live object the instance owns, so live-plan would
		// report the resource as uncreated rather than showing what it is),
		// #275's residue (live-plan never applies, so it never WRITES
		// residue; reading it is what makes live-plan's report agree with
		// what `plan` would show for the same estate) and #353's
		// provisioner taint - the last unreachable today for the same
		// structural reason as before the merge: hintStore is opened only
		// when the root module's live block declares a record_store, and a
		// configuration WITH a live block never reaches this function at
		// all - Run delegates it to PlanCommand, whose own
		// projection.Options (internal/command/live_mode.go) is the one
		// that carries this store for real. What arrives here is the
		// -estate form, which by definition has no live block and
		// therefore no record_store.
		//
		// Kept rather than omitted, and stated rather than left to be
		// rediscovered: the day that delegation stops covering some
		// live-block shape, a missing store here would make this report
		// call a live, marked, half-provisioned object healthy while the
		// real plan proposed replacing it - and this report is what every
		// crossing's stage 3 reads. NewRecordEnvelopeStore(nil, ...) is
		// nil, so a hintStore that would not open leaves this nil and the
		// projection raises its own named refusal for the located half,
		// which is the right answer there, unlike for the hint, where a
		// missing store only costs time.
		RecordStore: projection.NewRecordEnvelopeStore(hintStore, recordKeyPrefixFor(config, estate)),
		// dataResults (statelessDataReads, above) is issue #179's own
		// data-read phase output - already read, already paid for. See
		// [projection.Options.DataResults]'s doc comment for why the
		// projection's own tags/attrs seeding wants it too.
		DataResults: dataResults,
		// GitHub issue #361's crash-window recovery, wired for the same
		// "kept rather than omitted" reason this Options block's own
		// RecordStore comment gives: this form never reaches it today
		// (hintStore is nil whenever this function does), but the day
		// that stops being true, this path should not silently regress.
		DeposedBindings: disco.DeposedBindingsList(),
		// GitHub issue #626's knob, resolved from the environment at the top
		// of this function. This is the read pass issue #585 made concurrent
		// and the bulk of what this command spends its time on, so this is the
		// construction site the variable exists for.
		ReadParallelism: readPar,
	})
	// Issue #349. Same store again, sixth namespace, and unreachable today
	// for the same structural reason ProvisionedStore is: hintStore is
	// opened only when the root module's live block declares a record_store,
	// and a configuration with a live block is delegated to PlanCommand
	// before it ever reaches here. Read and wired anyway rather than left
	// out, so that the day that delegation stops covering some shape, this
	// path answers about root outputs the same way the other one does
	// instead of quietly regressing every estate that reaches it to "every
	// output is new".
	recordedRootOutputs := projection.ReadRootOutputValues(ctx, projection.NewRootOutputStore(hintStore, estate), config)
	// GitHub issue #349's root-output data reads, taken here because this is
	// the last moment the provider instances that read the live system are
	// still open - the output evaluation itself happens after the plan
	// context exists, several steps below. Scoped: whatever this cannot read
	// costs one root output its prior value and nothing else.
	rootOutputData, rootOutputDataDiags := statelessRootOutputDataReads(ctx, config, provs, resourceSchemas, scope)
	diags = diags.Append(rootOutputDataDiags)

	// The provider processes started for the projection have done their job
	// by this point; the plan below starts its own from the same library.
	diags = diags.Append(provs.close(ctx))
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return 1, false, diags
	}

	statelessView.Omissions(statelessOmissions(projResult))
	statelessView.Unowned(statelessUnownedReport(projResult, estate))

	// GitHub issue #388's plan-node seam: resolver's ownership guard
	// (NodeResolver.Unowned, noderesolver.go step (c)'s own doc comment)
	// can only be set now, not alongside RecordStore/MarkerIndex/Estate
	// above, because projResult - the pre-walk projection that actually
	// decided ownership - did not exist yet at that point in this
	// function. See that field's own doc comment for why leaving it unset
	// would let the node adopt a client-named resource this run does not
	// own.
	if resolver != nil {
		resolver.Unowned = nodeResolverUnownedSet(projResult.Unowned)
	}

	// classified and foreignReq are kept in outer scope, past the section
	// they were computed for: the lookalike guard below needs the same
	// classification and the same request (for its region and endpoint) once
	// the plan is in hand and it knows which addresses are actually about to
	// be created.
	var classified *foreign.Result
	var foreignReq foreign.Request
	if disco != nil {
		foreignReq = foreign.Request{
			Estate:    disco.Estate,
			Config:    config,
			Discovery: disco,
			// The adoption hint carries the region and endpoint the
			// resources were listed through, so that pasting it talks to
			// the same cloud the plan just read. discoProvider is the
			// "primary" provider configuration (statelessDiscover's own doc
			// comment): in a multi-provider estate (issue #69) a hint for a
			// candidate or foreign resource found under a different provider
			// configuration may name the wrong region here. That is a known
			// v0 simplification, not a silent wrong answer - internal/live/
			// foreign's Request has no per-item region yet, and splitting it
			// is future work rather than something this fix's acceptance bar
			// (the alias-e2e fixture, which owns both its resources and
			// produces nothing in this section at all) requires.
			Region:      provs.region(discoProvider),
			EndpointURL: provs.endpointURL(discoProvider),
		}
		var foreignDiags tfdiags.Diagnostics
		classified, foreignDiags = foreign.Classify(ctx, foreignReq)
		diags = diags.Append(foreignDiags)
		if foreignDiags.HasErrors() {
			return 1, false, diags
		}
		statelessView.Foreign(statelessForeignReport(classified, disco))
		statelessView.GuidedFallback(disco.GuidedFallback)
	}

	// GitHub issue #587's adoption ledger, built from the three values just
	// rendered above rather than from anything of its own. Called on every
	// run; only the adoption-only view renders it.
	statelessView.Adoption(statelessAdoptionReport(
		projResult,
		statelessForeignReport(classified, disco),
		statelessUnownedReport(projResult, estate),
		resourceSchemas,
		estate,
		disco != nil,
	))

	rawVariables, varDiags := c.collectVariableValues()
	diags = diags.Append(varDiags)
	variables, parseDiags := backend.ParseVariableValues(rawVariables, config.Module.Variables)
	diags = diags.Append(parseDiags)
	if diags.HasErrors() {
		return 1, false, diags
	}

	// The schemas are read before the plan rather than after it because
	// stamping needs them: which resource types can carry an ownership marker
	// is a question only the provider's schema answers. The same object
	// renders the plan below.
	schemas, schemaDiags := tfCtx.Schemas(ctx, config, projResult.State)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return 1, false, diags
	}

	// Marker stamping, before the plan runs, because it works by rewriting
	// the configuration the plan is about to read. A marker conflict is fatal:
	// the configuration claims an ownership this run cannot honor, and
	// planning past it would act on a resource whose owner is in dispute. So
	// is a resource that only its marker could ever find going unstamped,
	// which is why the resolutions travel into the pass. policyUntag carries
	// declared_tagged = "untag"'s released keys, worked out from the
	// projection's own policy outcomes now that it has run.
	policyUntag := statelessPolicyUntagMap(projResult.Policy, statelessPolicyTagKey(pol))
	recordBackedBlocks, recordBlocksDiags := recordBackedNeedsDiscoveryBlocks(ctx, recordShrinkStore, resolutions.NeedsDiscovery())
	diags = diags.Append(recordBlocksDiags)
	if recordBlocksDiags.HasErrors() {
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}
	stampRes, stampDiags := statelessStamp(ctx, config, estateFlag, schemas, disco.SlotTable(), statelessNeedsDiscovery(resolutions), policyUntag, recordBackedBlocks)
	diags = diags.Append(stampDiags)
	if stampDiags.HasErrors() {
		return 1, false, diags
	}

	statelessView.Policy(statelessPolicyReport(projResult, disco, stampRes, reconcile))

	// GitHub issue #348: evaluate the configuration's root-level `output`
	// blocks against the projection now, in place, the same way a real
	// refresh recomputes them before a plan diffs "prior" output values
	// against "planned" ones. Without this, projResult.State carries no
	// output values at all, and every declared output shows as newly
	// created on every run regardless of whether the underlying resources
	// changed. See [projection.ApplyRootOutputValues]. rootOutputData is
	// issue #349's second half: the data sources those outputs reach, read
	// live a few steps above while the projection's providers were open.
	// recordedRootOutputs is #349's remaining half: what the estate
	// remembers each output was, for the ones evaluation cannot reach.
	diags = diags.Append(projection.ApplyRootOutputValues(ctx, tfCtx, config, projResult.State, variables, rootOutputData, recordedRootOutputs))
	if diags.HasErrors() {
		return 1, false, diags
	}

	plan, planDiags := tfCtx.Plan(ctx, config, projResult.State, &tofu.PlanOpts{
		Mode: plans.NormalMode,
		// The projection was built from live reads a moment ago, so a
		// refresh walk would read every object again to learn what it
		// already knows.
		SkipRefresh:  true,
		SetVariables: variables,
		Targets:      args.Operation.Targets,
		Excludes:     args.Operation.Excludes,
		ForceReplace: args.Operation.ForceReplace,
	})
	diags = diags.Append(planDiags)
	if plan == nil {
		return 1, false, diags
	}

	// The lookalike guard: now that the plan is in hand, ask which of its
	// creates might be duplicating a live resource this estate does not own
	// - most often because a resource's tofu-estate and tofu-address tags
	// were stripped out of band, off a server-assigned type no marker means
	// no other way to find. It reuses the classification computed above
	// rather than sweeping a second time, and it never touches the plan: a
	// create beside a genuine lookalike is still a create, and this only
	// makes sure the warning sits right above it.
	if classified != nil {
		statelessView.Lookalikes(statelessLookalikeReport(foreign.Lookalikes(foreignReq, classified, statelessPlannedCreates(plan))))
	}

	view.Operation().Plan(plan, schemas)

	if diags.HasErrors() {
		return 1, false, diags
	}
	if !plan.CanApply() {
		return 0, false, diags
	}
	if args.DetailedExitCode {
		return 2, true, diags
	}
	return 0, true, diags
}

// ---------------------------------------------------------------------------
// Discovery and foreign classification
// ---------------------------------------------------------------------------

// statelessRecordBackedNeedsDiscoveryAddrs is edge 3 of the plan-node seam
// (the foundation-order ruling (#388), ruling 3; GitHub issue #388):
// among needs (a ClassNeedsDiscovery resolution list, ordinarily
// resolutions.NeedsDiscovery()), the subset whose estate record already
// holds an identity - read the same way
// [projection.NodeResolver.ResolveResourceIdentity]'s own step (a) reads
// it - no longer needs the marker sweep to bind it: under
// CHOUDOUFU_NODE_RESOLVE=1, the node resolver answers that instance's
// identity directly from the same record, at plan time, so a scan-and-match
// attempt for it here is wasted work. The returned set is
// [discovery.Request.RecordBackedAddrs]; see that field's own doc comment
// for what it does and does not skip (never the estate-wide sweep).
//
// store is nil whenever the caller has not both opened a record store AND
// turned the migration flag on - see this function's two call sites in
// statelessDiscover's own callers - which is what keeps a flag-off run's
// demand byte-identical: this returns (nil, nil) immediately, without
// reading anything, exactly as if edge 3 did not exist. needs empty is the
// same shortcut for a configuration with nothing waiting on discovery at
// all.
//
// A read error is fatal, the same severity
// [projection.NodeResolver.ResolveResourceIdentity] gives a corrupted
// record: a demand this pass cannot safely shrink is not something to
// guess about, and continuing with a stale or partial exclusion set risks
// skipping the sweep for an instance whose record turns out unusable,
// which would leave it with neither a marker binding nor a resolved
// identity.
func statelessRecordBackedNeedsDiscoveryAddrs(ctx context.Context, store *projection.RecordStore, needs []identity.Resolution) (map[string]bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if store == nil || len(needs) == 0 {
		return nil, diags
	}
	var out map[string]bool
	for _, r := range needs {
		_, _, _, found, err := store.GetIdentity(ctx, r.Addr)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a persisted record", fmt.Sprintf(
				"Reading the record for %s failed: %s.", r.Addr, err,
			)))
			continue
		}
		if found {
			if out == nil {
				out = make(map[string]bool)
			}
			out[r.Addr.String()] = true
		}
	}
	return out, diags
}

// collectDeposedRecords is GitHub issue #361's crash-window recovery: one
// [projection.RecordStore.GetDeposed] read per needs-discovery address,
// from the estate's already-open record envelope store, well before
// [discovery.Discover] ever runs - see discovery.Request.DeposedRecords'
// own doc comment for what consumes the result. A store that will not open
// (nil, the same "the run proceeds hintless" fallback every other read of
// recordStoreForReads in this file already takes) or an address with
// nothing recorded simply contributes nothing: this is a hint discovery's
// collision branch may use to disambiguate, never authority, the same
// discipline [statelessRecordBackedNeedsDiscoveryAddrs] just above already
// follows for the identity half. Unlike that function, a read error here
// is not raised as a diagnostic: this recovery is best-effort by design
// (#361's design comment, section 4 - "a stale Deposed entry ... simply
// fails to match any claimant"), and failing the whole plan over an
// unreadable deposed-object hint would turn a recovery path into a new way
// for an estate to be blocked.
func collectDeposedRecords(ctx context.Context, store *projection.RecordStore, needs []identity.Resolution) map[string]map[string]projection.DeposedRecord {
	if store == nil || len(needs) == 0 {
		return nil
	}
	var out map[string]map[string]projection.DeposedRecord
	for _, r := range needs {
		deposed, _, _, err := store.GetDeposed(ctx, r.Addr)
		if err != nil || len(deposed) == 0 {
			continue
		}
		if out == nil {
			out = make(map[string]map[string]projection.DeposedRecord)
		}
		out[r.Addr.String()] = deposed
	}
	return out
}

// recordBackedNeedsDiscoveryBlocks reduces
// [statelessRecordBackedNeedsDiscoveryAddrs]'s own per-INSTANCE record
// check to block granularity, for [statelessStampGaps]' escalation gate:
// [stamp.Skip.Addr] and [identity.BlockDiscovery] are both keyed by
// [addrs.ConfigResource] (the resource block, module-qualified, with no
// instance key), never by the instance a record is written for. A block is
// exempt from "this resource cannot be found again" only when EVERY one of
// its instances that needs discovery already has a usable identity in the
// record store - a for_each block half migrated, some instances recorded
// and others not, still has to escalate for the ones that are not.
//
// store is nil under the same two conditions
// [statelessRecordBackedNeedsDiscoveryAddrs] documents (no record store
// opened, or the migration flag off), and this returns (nil, nil)
// immediately in that case, which is what keeps a flag-off run's stamp-gap
// diagnostics byte-identical: a nil map's lookup is always false, so
// [statelessStampGaps] escalates exactly as it did before this existed.
func recordBackedNeedsDiscoveryBlocks(ctx context.Context, store *projection.RecordStore, needs []identity.Resolution) (map[string]bool, tfdiags.Diagnostics) {
	recordBacked, diags := statelessRecordBackedNeedsDiscoveryAddrs(ctx, store, needs)
	if store == nil || len(needs) == 0 {
		return nil, diags
	}
	total := make(map[string]int, len(needs))
	covered := make(map[string]int, len(needs))
	for _, r := range needs {
		key := r.Addr.ConfigResource().String()
		total[key]++
		if recordBacked[r.Addr.String()] {
			covered[key]++
		}
	}
	out := make(map[string]bool, len(total))
	for key, n := range total {
		if covered[key] == n {
			out[key] = true
		}
	}
	return out, diags
}

// statelessDiscover runs the marker discovery pass, wide enough to see the
// resources nobody owns, and returns its result. It returns a nil result
// with no error diagnostics in the two cases where discovery is not run at
// all: nothing in the configuration is waiting on it, or the estate's name
// could not be established (a warning, not an error - see
// statelessEstateName).
//
// [discovery.Request.CollectUnclaimed] is always set here. It trades the
// server-side estate filter for a wider list, which is the price of being
// able to say anything at all about resources that carry no marker, and
// stateless mode's central safety claim is a claim about exactly those.
// The pass also sweeps: every admitted resource type the configuration does
// not declare is listed for this estate's markers, which is the only way a
// resource whose block was deleted can be seen at all. That is why discovery
// now runs even when nothing is waiting to be found - a configuration of
// entirely client-named resources still has an estate, and deleting one of
// those blocks is exactly as much a removal as deleting a marker-discovered
// one.
//
// Provider selection runs one pass per provider configuration
// ([statelessDiscoveryPassProviders]: the estate-wide sweep's candidates,
// [statelessManagedResourceProviders], union the ones the needs-discovery
// resolutions themselves use, [statelessNeedsDiscoveryProviders]), and
// [discovery.Merge] combines the results. Every pass carries
// [discovery.Request.ScopeProvider], which is what keeps it from listing
// for - and so from ever binding - a resource whose own block names a
// different provider configuration: an estate spanning two regions gets each
// of its resources looked for in the region that resource's own
// configuration names, rather than all of them looked for in one. A
// single-provider configuration takes the direct path with no merge step at
// all, which is what keeps that case byte-identical to every run before any
// of this existed.
//
// Issue #69 built that mechanism for the sweep alone and left the
// needs-discovery scan refused outright whenever it spanned more than one
// configuration; issue #283 is that refusal being lifted onto the same
// mechanism, because a scoped per-configuration pass answers the hazard the
// refusal existed for - see [statelessNeedsDiscoveryProviders].
//
// The second return value is the "primary" provider configuration: the first
// of the needs-discovery resolutions' own configurations in address order,
// or - when nothing needed discovery - the first pass's. It is what a caller uses
// for the one thing this pass cannot honestly split by provider without a
// larger refactor of internal/live/foreign: the adoption hint's --region
// and --endpoint-url flags. In a multi-provider estate that hint may name
// the wrong region for a foreign resource found under a different provider
// configuration - a known, documented v0 simplification, not a silent wrong
// answer, and the third return value is what a caller uses instead for
// materializing undeclared instances correctly, per-address, regardless of
// which provider found them.
func statelessDiscover(ctx context.Context, config *configs.Config, resolutions *identity.Result, estateFlag string, provs *statelessProviders, pol *policy.Policy, hintStore staterecord.Store, statelessView views.StatelessPlan, recordShrinkStore *projection.RecordStore, deposedRecords map[string]map[string]projection.DeposedRecord, cacheVouchTypes []string, adoptionOnly bool) (*discovery.Result, addrs.AbsProviderConfig, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var noProvider addrs.AbsProviderConfig

	// GitHub issue #612's knob, resolved once here and passed down. Here,
	// because this function is the single funnel every entry point that
	// sweeps goes through - live-plan's "-estate" form above, and the
	// live-block path plain "choudoufu plan" and "choudoufu apply" run
	// (live_mode.go's own call). Once, because statelessDiscoverOne runs a
	// pass per provider configuration, and resolving it there would report a
	// bad setting once per pass. See [sweepParallelismSetting] for the
	// setting, the refusal and the default decision.
	sweepPar, sweepParDiags := sweepParallelismSetting()
	diags = diags.Append(sweepParDiags)
	if sweepParDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}

	// the CollectUnclaimed ruling (#604),
	// resolved here for the same two reasons the parallelism knob above is:
	// this function is the single funnel every sweeping entry point goes
	// through, and a bad setting must be reported once rather than once per
	// provider pass. See [collectUnclaimedSetting].
	collectUnclaimed, collectDiags := collectUnclaimedSetting(adoptionOnly)
	diags = diags.Append(collectDiags)
	if collectDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}

	needs := resolutions.NeedsDiscovery()

	estate, estateDiags := statelessEstateName(ctx, estateFlag, config, needs)
	diags = diags.Append(estateDiags)
	if estate == "" {
		return nil, noProvider, nil, diags
	}

	// Edge 3 of the plan-node seam (the foundation-order ruling (#388),
	// ruling 3; issue #388): recordShrinkStore is nil unless the caller has
	// both opened a record store AND turned CHOUDOUFU_NODE_RESOLVE=1 on -
	// see [statelessRecordBackedNeedsDiscoveryAddrs]'s own doc comment for
	// why the flag has to gate it here, at the call site, rather than
	// inside this function: a byte-identical demand with the flag off is
	// the migration contract, and a nil store is what keeps this call a
	// no-op whenever that contract applies.
	recordBacked, recordDiags := statelessRecordBackedNeedsDiscoveryAddrs(ctx, recordShrinkStore, needs)
	diags = diags.Append(recordDiags)
	if recordDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}

	needsProviders, needsDiags := statelessNeedsDiscoveryProviders(config, needs)
	diags = diags.Append(needsDiags)
	if needsDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}
	// needsSet is [needsProviders] as a membership test, built once: it is
	// what tells [statelessDiscoverProviderUnavailable] whether a provider
	// that could not be configured is one some declared instance's own
	// IDENTITY depends on (fatal - see that function's doc comment) or one
	// the estate-wide sweep alone would have used (downgradable).
	needsSet := make(map[string]bool, len(needsProviders))
	for _, addr := range needsProviders {
		needsSet[addr.String()] = true
	}

	sweepProviders := statelessManagedResourceProviders(config)
	if len(sweepProviders) == 0 {
		// No managed resources at all: nothing to find, nothing that could
		// be undeclared.
		return nil, noProvider, nil, diags
	}

	passProviders := statelessDiscoveryPassProviders(sweepProviders, needsProviders)

	if len(passProviders) == 1 {
		providerAddr := passProviders[0]
		// No ScopeProvider: the single-provider path is the exact call
		// every caller made before issue #69 existed.
		res, discoDiags := statelessDiscoverOne(ctx, config, resolutions.All(), estate, providerAddr, addrs.AbsProviderConfig{}, provs, pol, hintStore, statelessView, recordBacked, deposedRecords, cacheVouchTypes, sweepPar, collectUnclaimed)
		if warn, ok := statelessDiscoverProviderUnavailable(providerAddr, needsSet, discoDiags); ok {
			diags = diags.Append(warn)
			return nil, noProvider, nil, diags
		}
		diags = diags.Append(discoDiags)
		if discoDiags.HasErrors() {
			return nil, noProvider, nil, diags
		}
		return res, providerAddr, nil, diags
	}

	// More than one provider configuration among the estate's managed
	// resources: issue #69. Every pass sees the *whole* estate's
	// resolutions - not a filtered subset - because a live resource's
	// marker can be visible through more than one provider configuration
	// even when its declared instance is not: a type whose list operation is
	// account-global rather than region-scoped (aws_s3_bucket, the
	// alias-e2e fixture's own choice; also IAM and Route53) hands every
	// pass every account's population of it, including objects declared
	// under a different provider configuration. Request.ScopeProvider is
	// what keeps a pass from *binding* through the wrong account while
	// still letting it recognize (via [declared.declares], built from every
	// resolution regardless of provider) that such an object is somebody
	// else's declared, owned resource rather than an orphan to remove.
	passes := make([]discovery.Pass, 0, len(passProviders))
	for _, providerAddr := range passProviders {
		res, discoDiags := statelessDiscoverOne(ctx, config, resolutions.All(), estate, providerAddr, providerAddr, provs, pol, hintStore, statelessView, recordBacked, deposedRecords, cacheVouchTypes, sweepPar, collectUnclaimed)
		if warn, ok := statelessDiscoverProviderUnavailable(providerAddr, needsSet, discoDiags); ok {
			// Sweep-only provider, unusable for the same reason stock never
			// asks this question in one shot either: its own configuration
			// depends on a managed resource this run has not created yet.
			// Nothing under it could have been swept for before this
			// moment - there is no way to have listed a Kubernetes object
			// in a cluster that does not exist - so this pass contributes
			// no orphans and binds nothing, exactly like a pass over an
			// estate with zero managed resources of its own. The real
			// resource graph configures this provider for real once its
			// dependency is known, the same deferred order stock's own
			// graph already gives it; skipping the pass here does not
			// change what gets created, only when its identity is verified.
			diags = diags.Append(warn)
			continue
		}
		diags = diags.Append(discoDiags)
		if discoDiags.HasErrors() {
			return nil, noProvider, nil, diags
		}
		passes = append(passes, discovery.Pass{
			Provider: providerAddr,
			Region:   provs.region(providerAddr),
			Result:   res,
		})
	}
	if len(passes) == 0 {
		// Every provider configuration among the sweep's candidates was
		// downgraded above: none of them could be reached, and none of
		// them was needed for any declared instance's own identity either,
		// or this loop would have returned fatally already. [discovery.
		// Merge] with zero passes hands back a Result whose Resolutions is
		// nil, and the caller's `if disco != nil { merged =
		// disco.Resolutions }` would then overwrite the estate's whole,
		// already-config-derived resolution set with nothing - so this
		// case is reported exactly like "nothing waiting on discovery"
		// (len(sweepProviders) == 0 above) rather than handed to Merge.
		return nil, noProvider, nil, diags
	}

	merged, providerOf, mergeDiags := discovery.Merge(estate, passes)
	diags = diags.Append(mergeDiags)
	if mergeDiags.HasErrors() {
		return merged, noProvider, providerOf, diags
	}

	// The primary is the first needs-discovery configuration in address
	// order, or the first pass's when nothing needs discovery. It is only
	// ever used for the one thing this pass cannot split by provider - the
	// adoption hint's --region and --endpoint-url flags, see this function's
	// own doc comment - and taking the first in a sorted order rather than
	// whichever the map happened to yield is what keeps a multi-provider
	// estate's rendered hint identical from run to run.
	primary := passProviders[0]
	if len(needsProviders) > 0 {
		primary = needsProviders[0]
	}
	return merged, primary, providerOf, diags
}

// summaryProviderConfigNotEvaluableForSweep is [statelessDiscoverOne]'s
// diagnostic Summary specifically for a [projection.
// ProviderConfigNotEvaluable] failure - never for a schema-read or
// plugin-launch failure, which keep the generic "Provider unavailable for
// marker discovery" summary and stay fatal regardless of needsSet. It
// exists so [statelessDiscoverProviderUnavailable] matches on a summary
// this file alone produces for exactly this typed error, rather than on
// rendered error text.
const summaryProviderConfigNotEvaluableForSweep = "Provider configuration not evaluable for marker discovery"

// statelessDiscoverProviderUnavailable is [statelessDiscover]'s single
// downgrade rule, generic over every provider a config can name rather than
// specific to any one of them: a provider configuration that could not be
// built for the marker-discovery pass is fatal for the whole estate UNLESS
// every one of these holds -
//
//   - no declared instance's own IDENTITY resolution depends on this
//     provider (providerAddr is absent from needsSet, [statelessDiscover]'s
//     own needsProviders membership test) - if it did, "could not verify"
//     really does mean "cannot tell whether this instance already exists",
//     which stays the fatal case ratifyOne (internal/live/liveimport/
//     ratify.go) is the migrate-time analogue of, per instance rather than
//     per pass;
//   - discoDiags carries errors at all;
//   - and the diagnostic is [statelessDiscoverOne]'s own "Provider
//     unavailable for marker discovery" - not some other failure (a bad
//     schema read, a plugin crash) this function must not paper over.
//
// The estate-wide sweep exists to find live objects a declared block does
// not mention; a provider whose own configuration cannot yet be evaluated
// because it reads a managed resource this same run has not created has
// necessarily never been reachable by any tool before this moment either -
// there is no way to have listed a Kubernetes object in a cluster that does
// not exist - so skipping its pass loses no coverage anything could have
// had. This is [statelessDiscover]'s side of GitHub issue #313's
// provider-configuration dependency-order boundary: [statelessProviderData
// Reads] already resolves such a provider's config once its dependency has
// SOME prior identity to read (a record, a migrated marker); when it has
// none - the first-ever create, corpus-eks-basic's own greenfield stage -
// the real resource graph still configures the provider for real once its
// dependency is known, the same deferred order stock's own graph gives it,
// and this function is what keeps the stateless PRE-pass from refusing a
// question stock never has to answer either.
//
// ok is false whenever discoDiags carries no error, or carries an error
// this function does not recognize as downgradable; the caller's existing
// fatal handling is unchanged in both cases. warn is meaningful only when
// ok is true: the same information as a Warning instead of an Error, so an
// operator still sees exactly what could not be swept and why.
func statelessDiscoverProviderUnavailable(providerAddr addrs.AbsProviderConfig, needsSet map[string]bool, discoDiags tfdiags.Diagnostics) (tfdiags.Diagnostics, bool) {
	if needsSet[providerAddr.String()] {
		return nil, false
	}
	if !discoDiags.HasErrors() {
		return nil, false
	}
	for _, d := range discoDiags {
		if d.Severity() != tfdiags.Error || d.Description().Summary != summaryProviderConfigNotEvaluableForSweep {
			return nil, false
		}
	}
	var warn tfdiags.Diagnostics
	for _, d := range discoDiags {
		desc := d.Description()
		warn = warn.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Provider unavailable for the estate-wide sweep",
			fmt.Sprintf(
				"%s No declared instance's identity depends on this provider configuration, so this is not fatal: nothing under it could have been swept before now either, since the provider itself could not be reached. Its declared instances proceed; the real apply configures this provider once its own dependency is known, the same order stock's plan graph already gives it.",
				desc.Detail,
			),
		))
	}
	return warn, true
}

// statelessDiscoverOne runs [discovery.Discover] through one provider
// configuration: the shared body of both statelessDiscover's single-provider
// path and each iteration of its multi-provider loop. scopeProvider is
// [discovery.Request.ScopeProvider]; its zero value means unscoped, which is
// what the single-provider path passes.
// recordKeyPrefixFor is the key namespace this estate's one record envelope
// store (GitHub issue #364) lives under - [projection.RecordKeyPrefix](estate)
// by default, or a record_store block's key_prefix override - or "" for a
// configuration with no live block or no record_store, matching hintStore
// being nil in the same case.
func recordKeyPrefixFor(config *configs.Config, estate string) string {
	if config == nil || config.Module == nil || config.Module.Live == nil || config.Module.Live.RecordStore == nil {
		return ""
	}
	return projection.RecordStoreKeyPrefix(config.Module.Live.RecordStore, estate)
}

// sweepPar is [discovery.Request.SweepParallelism] for this pass, already
// resolved and validated by [statelessDiscover] - see
// [sweepParallelismSetting].
func statelessDiscoverOne(ctx context.Context, config *configs.Config, resolutions []identity.Resolution, estate string, providerAddr, scopeProvider addrs.AbsProviderConfig, provs *statelessProviders, pol *policy.Policy, hintStore staterecord.Store, statelessView views.StatelessPlan, recordBacked map[string]bool, deposedRecords map[string]map[string]projection.DeposedRecord, cacheVouchTypes []string, sweepPar int, collectUnclaimed bool) (*discovery.Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	provider, err := provs.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		// A distinct Summary for the one class [statelessDiscoverProvider
		// Unavailable] may downgrade ([projection.ProviderConfigNotEvaluable]:
		// this provider's own block could not be statically evaluated, not
		// a broken plugin or missing credentials) so that function's match
		// is on the typed error, not on rendered text; every other failure
		// keeps the summary it has always had and stays fatal.
		summary := "Provider unavailable for marker discovery"
		var notEvaluable *projection.ProviderConfigNotEvaluable
		if errors.As(err, &notEvaluable) {
			summary = summaryProviderConfigNotEvaluableForSweep
		}
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			summary,
			fmt.Sprintf("Finding the live resources of this estate needs provider %s, which could not be used: %s.", providerAddr, err),
		))
	}

	req := discovery.Request{
		Estate:            estate,
		Config:            config,
		RecordBackedAddrs: recordBacked,
		DeposedRecords:    deposedRecords,
		Resolutions:       resolutions,
		Provider:          provider,
		Region:            provs.region(providerAddr),
		// the stale-state ruling's (#604) ruling: this is the
		// account-inventory question ("what is in my account that this
		// estate does not know about"), and it does not stay
		// unconditional. [collectUnclaimedSetting] is where the run picks
		// it; internal/live/discovery/nativesweep.go is what it costs and
		// what leaving it unset gives up. Sweep itself is untouched -
		// every removal leg runs on every plan, which is the correctness
		// half and is not what the charter reopened.
		CollectUnclaimed: collectUnclaimed,
		// Issue #692 increment 2: one list per type the state cache holds
		// concrete-declared candidates for, so a -refresh=false hit's
		// existence-and-identity evidence comes from this run rather than
		// from trust. Nil on every path that is not serving from cache.
		CacheVouchTypes: cacheVouchTypes,
		Sweep:           true,
		// GitHub issue #612. The estate-wide sweep's list calls run
		// concurrently (issue #605), and this is the only place in the
		// command layer that says how many at once: without this line the
		// engine's default is the only reachable setting, which is the
		// defect #612 reports. [sweepParallelismSetting] resolved it.
		SweepParallelism: sweepPar,
		Policy:           pol,
		ScopeProvider:    scopeProvider,
		Progress:         statelessProgress(statelessView),
		// Independent of the Guided cost decision below: HintStore also
		// backs discovery's per-instance located-record fallback for a type
		// with no tags argument and no list route at all
		// (scanTypeLocatedFallback), which has nothing to do with the
		// estate-wide sweep's cost and must not wait on
		// statelessApplyGuidedDiscovery's "was a record_store DECLARED,
		// not merely implied" gate - an implied store (#364) is still a
		// real store an earlier migration may have written a located
		// identity into. A nil hintStore (no live block, or one that could
		// not open its store) leaves this nil too, exactly as before.
		HintStore: hintStore,
		// GitHub issue #364: the located identity scanTypeLocatedFallback
		// reads now lives in the same envelope, and the same namespace, an
		// ordinary record-backed instance's value does - see
		// [discovery.Request.KeyPrefix]. Empty when this config declares no
		// record_store (recordKeyPrefixFor's own nil-safety), which matches
		// hintStore being nil in the same case.
		KeyPrefix: recordKeyPrefixFor(config, estate),
	}
	statelessApplyGuidedDiscovery(config, hintStore, &req)

	// The Cloud Control fallback (issue #47): a type with no native provider
	// list resource can still be enumerated when its mapped CFN type is
	// listable. The engine has carried this since #47 landed, but no command
	// ever constructed the client or the roster, so every run kept the
	// pre-#47 refusal - found when #124's media cohort cleared apply and
	// then refused on aws_medialive_multiplex at replan.
	//
	// The client is built only when the run names an endpoint override
	// (AWS_ENDPOINT_URL*, every emulator and acceptance run) or opts in
	// explicitly for real AWS (cloudControlOptInEnvVar). An unconditional
	// client turned every offline run into per-type network attempts -
	// credential-chain IMDS probes plus a real HTTPS call for each
	// roster-mapped type the mock provider cannot list - which is how the
	// command package's own unit suite blew its 10-minute timeout the first
	// time this wiring landed.
	if ep, on := cloudControlTarget(); on {
		if roster, err := registry.Embedded(); err != nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Cloud Control fallback unavailable",
				fmt.Sprintf("The embedded mapping/registry artifacts did not parse (%s), so types without a native list resource cannot fall back to Cloud Control enumeration this run.", err),
			))
		} else {
			req.Roster = roster
			req.CloudControl = cloudcontrol.New(cloudcontrol.Config{
				Endpoint: ep,
				Region:   provs.region(providerAddr),
			})
			// The tagging sweep (issue #51) rides the same gate (#128): one
			// estate-filtered GetResources call replaces the sweep's
			// per-type listing. Absence of either client falls back to the
			// pre-#51 per-type sweep, so the gate's off state is unchanged
			// behavior, same as the Cloud Control fallback above.
			req.Tagging = cloudcontrol.NewTagging(cloudcontrol.Config{
				Endpoint: ep,
				Region:   provs.region(providerAddr),
			})
			// TaggingSweep is on for every endpoint, real AWS and
			// emulator alike.
			//
			// It was conditional for a while (issue #229): through
			// ghcr.io/lex00/floci@sha256:1362e856..., the emulator served
			// GetResources on the wire but answered from an index fed by
			// only 2 of its 64 services, so a tagged resource came back
			// from its own service's describe call and not from the
			// tagging API. On such an endpoint TaggingSweep=true does not
			// degrade removal detection, it silently disables it: every
			// type the sweep would have covered comes back with zero
			// candidates and no diagnostic says so. The gate that bought
			// safety from that was "off against any loopback endpoint",
			// which is a premise about an emulator dressed up as a
			// property of a host.
			//
			// It cost the whole emulator tier its coverage of this branch.
			// Loopback is exactly what live/e2e/run.sh and
			// internal/live/flocitest.Endpoint both use, so with the gate
			// in place every e2e run, every cohort-acceptance run and
			// every local run took the per-type List fallback, and
			// discovery.go's sweepViaTagging leg was never reached outside
			// a unit test. Then the emulator was fixed (lex00/floci#229
			// unions the private tag map with a live read of every
			// service's stores), the pin moved, and the gate outlived its
			// reason with a test pinning it.
			//
			// So the premise moved out of this comment and into a
			// measurement: TestTaggingSweepPremiseHoldsForThePinnedEmulator
			// (tagging_sweep_premise_test.go) reads
			// live/floci-capabilities.json's tagging-sweep rows for
			// whatever digest live/floci-image pins and fails if any of
			// them is not "implemented" with no recorded exception - and
			// fails the other way too, if an exception is recorded while
			// this line stays unconditional. A future pin that regresses
			// says so on its own rather than waiting for someone to
			// re-read a comment.
			//
			// The residual case a gate here would have covered is a
			// third-party emulator whose own tagging index is blind. The
			// lever for that is cloudControlEnvVar ("off"), which skips
			// this whole block and returns the run to the pre-#51 per-type
			// sweep. The Cloud Control client above is untouched either
			// way: its own per-candidate GetResource refinement does not
			// go through resourcegroupstaggingapi at all, and #47's
			// fallback for types with no native list resource is
			// unaffected.
			req.TaggingSweep = true
		}
	}

	res, discoDiags := discovery.Discover(ctx, req)
	diags = diags.Append(discoDiags)
	return res, diags
}

// cloudControlEnvVar turns the Cloud Control enumeration fallback (and the
// #51 tagging sweep riding the same gate) OFF: set it to "off", "0" or
// "false". The default is ON - against an endpoint override and against
// real AWS alike - because a user with unlistable types is exactly who the
// fallback exists for, and both clients' real-AWS paths (SigV4 through the
// default credential chain, the 30s per-attempt bound) are validated by
// tools/cloudcontrol-probe against a live account (2026-08-14, both the
// Cloud Control and -tag-key modes). Offline test suites that must never
// touch the network set the off value in their TestMain - see
// internal/command's - since a discovery run over roster-mapped types
// otherwise turns into per-type HTTPS calls.
const cloudControlEnvVar = "TOFU_LIVE_CLOUDCONTROL"

// cloudControlTarget resolves where the Cloud Control fallback client
// should point, and whether one should exist at all: the SDK's
// service-specific override first, then the all-services override every
// emulator run (floci, the acceptance tier) sets, then real AWS unless
// cloudControlEnvVar switches the fallback off. The AWS provider resolves
// its own endpoints from the same variables, so a run pointed at an
// emulator sends both the provider's list calls and the fallback's to the
// same place without extra configuration.
func cloudControlTarget() (endpoint string, on bool) {
	switch os.Getenv(cloudControlEnvVar) {
	case "off", "0", "false":
		return "", false
	}
	if v := os.Getenv("AWS_ENDPOINT_URL_CLOUDCONTROL"); v != "" {
		return v, true
	}
	return os.Getenv("AWS_ENDPOINT_URL"), true
}

// guidedDiscoveryDisableEnvVar opts every estate this process plans or
// applies out of the default-on guided discovery
// [statelessApplyGuidedDiscovery] turns on, forcing today's full sweep
// regardless of what the "live" block configures. Set it to any non-empty
// value.
//
// The name and shape follow this fork's existing convention for a
// default-on behavior with an environment-variable escape hatch -
// TF_DISABLE_PLUGIN_TLS in meta_providers.go is the precedent - rather than
// a new "live" block attribute: turning guided discovery off is an
// operational lever for a run that is misbehaving, not a decision a team
// checks in and reviews the way estate and record_store are, so it does not
// belong beside them in configuration.
const guidedDiscoveryDisableEnvVar = "TOFU_DISABLE_GUIDED_DISCOVERY"

// defaultAutoGuidedMaxAge is the GuidedMaxAge [statelessApplyGuidedDiscovery]
// sets when it turns guided discovery on automatically: a stored hint has
// up to a week to keep narrowing the estate-wide sweep before it is treated
// as though it were never written at all, and discovery falls all the way
// back to full enumeration (see [discovery.Result.GuidedFallback]). A week
// is deliberately generous compared to [defaultGuidedMaxAge] in
// internal/live/discovery/guided.go (24h, the fallback for a direct API
// caller that sets Request.Guided with no GuidedMaxAge of its own): an
// operator who configured a record store already pays for the hint's
// carrier, and defaultAutoGuidedVerifyAge below is what keeps a week-old
// hint from also meaning "drift can hide for a week" - the freshness ceiling
// here only decides when a hint is too old to be worth reading at all.
const defaultAutoGuidedMaxAge = 7 * 24 * time.Hour

// defaultAutoGuidedVerifyAge is the GuidedVerifyAge
// [statelessApplyGuidedDiscovery] sets: a hint that is still trusted (younger
// than defaultAutoGuidedMaxAge) but has gone a full day without a fresh sweep
// runs this pass as a full, verifying sweep anyway, exactly as
// Request.GuidedVerify would. This is the policy's own safety valve - a
// standing orphan of a hinted type can surface no later than a day after it
// appears, under these defaults, no matter how generous defaultAutoGuidedMaxAge
// is about trusting the hint for narrowing.
const defaultAutoGuidedVerifyAge = 24 * time.Hour

// statelessApplyGuidedDiscovery is issue #64's default-on policy: it turns
// req.Guided on, with req.HintStore, req.GuidedMaxAge and
// req.GuidedVerifyAge populated, whenever all of the following hold -
//
//   - the configuration's "live" block DECLARES a record_store block, the
//     hint's one carrier since issue #109 removed the observational snapshot;
//   - the caller actually opened that store (hintStore non-nil - the same
//     handle the run's record-backed resources and hint write go through,
//     see [statelessRunner.PriorState] and [projection.Manager.EnableHint]);
//   - guidedDiscoveryDisableEnvVar is not set to a non-empty value.
//
// Nothing here invents a hint source: a configuration with no record_store
// leaves req untouched, and discovery.Discover behaves exactly as it always
// has for it (Request.Guided's own zero value is false). A run that writes
// the hint and a run that reads one back always agree on where it lives,
// because both go through the one store the record_store block names and
// the one key [projection.HintKey] derives.
//
// See internal/live/discovery/guided.go's file doc comment for the full
// policy statement from the discovery package's own side, including why its
// defaults (defaultGuidedMaxAge, and no automatic GuidedVerifyAge at all) stay
// unchanged for a direct caller of Discover.
func statelessApplyGuidedDiscovery(config *configs.Config, hintStore staterecord.Store, req *discovery.Request) {
	if os.Getenv(guidedDiscoveryDisableEnvVar) != "" {
		return
	}
	if config == nil || config.Module == nil || config.Module.Live == nil {
		return
	}
	if config.Module.Live.RecordStore == nil || config.Module.Live.RecordStore.Implied || hintStore == nil {
		// No record store DECLARED (or none could be opened): there is no
		// hint carrier this run was asked to use, and today's full
		// enumeration is exactly right.
		//
		// The Implied clause is issue #364's blast radius held where the
		// issue put it. Every live block now carries a record store
		// (internal/configs.impliedRecordStore), so without this clause
		// guided discovery - an opt-in cost optimization that was reached
		// by declaring a store - would silently become the default for
		// every estate that has a live block at all, which is not
		// something #364 asked for and not something any estate's crossing
		// has measured. It also visibly diverges the two entry points:
		// `plan` under a live block would print discovery's
		// hint-fallback stanza on every first run while
		// `live-plan -estate=<name>` on the same configuration would not,
		// and TestStatelessMode_planParity holds those two outputs
		// identical.
		//
		// Whether the implied store should carry the hint too is a real
		// question and a separate one: it is a pure cost decision (guided
		// discovery is defined to produce byte-identical output to a full
		// sweep), so it can be turned on later without changing any plan.
		return
	}

	req.Guided = true
	req.HintStore = hintStore
	req.GuidedMaxAge = defaultAutoGuidedMaxAge
	req.GuidedVerifyAge = defaultAutoGuidedVerifyAge
}

// statelessProgressInterval is the minimum time between two heartbeat lines
// [statelessProgress] forwards. Discovery reports every single type it
// scans, which for a fast-listing provider can be many per second; printing
// all of them would turn a heartbeat into a scrolling log, which is the
// opposite of unobtrusive. Half a second is frequent enough that a large,
// slow-listing estate never looks hung, and coarse enough that a small,
// fast one prints once or twice and gets out of the way.
const statelessProgressInterval = 500 * time.Millisecond

// statelessProgress adapts a [views.StatelessPlan] into a
// [discovery.ProgressFunc], throttling how often it actually renders a
// heartbeat: [discovery.Discover] calls back after every resource type it
// scans, which is finer-grained than anything worth printing, so this is
// where the "how often" decision is made rather than in the discovery
// package or the view. The first event always passes through unthrottled,
// since that is a reader's first evidence the run has not hung, which
// matters more than any timing rule.
//
// The view renders to stderr (see [views.StatelessPlanHuman.Progress]),
// never stdout, so nothing this prints can end up in output a script reads
// from this command - today that is everything live-plan writes on
// success, since it has no -json mode yet.
func statelessProgress(statelessView views.StatelessPlan) discovery.ProgressFunc {
	var last time.Time
	return func(ev discovery.ProgressEvent) {
		now := time.Now()
		if !last.IsZero() && now.Sub(last) < statelessProgressInterval {
			return
		}
		last = now
		statelessView.Progress(views.StatelessProgress{
			TypeName:       ev.TypeName,
			TypesScanned:   ev.TypesScanned,
			ResourcesFound: ev.ResourcesFound,
		})
	}
}

// statelessEstateName establishes which estate this run is about.
//
// Two sources, in order. An explicit -estate=<name> wins and is checked
// against the marker grammar. Otherwise the name is read out of the
// configuration itself: every resource that stamps a tofu-estate tag is
// asked what value it stamps, and if they all agree on one, that is the
// estate. That derivation is not a guess about ownership - it is reading the
// same statement the configuration already makes to the cloud on every
// apply, and requiring the whole configuration to agree is what keeps it
// from becoming one.
//
// When neither source answers, the return is an empty name and a warning:
// discovery does not run, the instances that needed it stay unresolved, and
// the omissions section already explains each one. That is the pre-discovery
// behaviour of this command rather than a new failure mode, and turning it
// into an error would make a configuration that never used markers
// unplannable.
func statelessEstateName(ctx context.Context, flagValue string, config *configs.Config, needs []identity.Resolution) (string, tfdiags.Diagnostics) {
	name, found, diags := statelessEstateFor(ctx, flagValue, config)
	if diags.HasErrors() || name != "" {
		return name, diags
	}

	switch len(found) {
	case 1:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Invalid estate name in configuration",
			fmt.Sprintf(
				"The configuration stamps tofu-estate = %q, which does not match the marker grammar in live/MARKERS.md, so it cannot be used to find this estate's resources. %s Pass -estate=<name> to name the estate explicitly.",
				found[0], statelessUndiscoveredNote(needs),
			),
		))
	case 0:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"No estate name to search by",
			fmt.Sprintf(
				"%s Finding them means listing live resources and reading their tofu-estate markers, and this run has no estate name to look for: no resource in the configuration stamps a tofu-estate tag with a value that can be read from configuration alone. Pass -estate=<name> to name it. Nothing was read from the live system for those instances, and nothing was classified as foreign - which is not a report that no foreign resources exist.",
				statelessUndiscoveredNote(needs),
			),
		))
	default:
		return "", diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Several estates named by the configuration",
			fmt.Sprintf(
				"Resources in this configuration stamp tofu-estate with %s. An estate is the unit of ownership and a run covers exactly one, so which of these to search for is not something this command will pick. %s Pass -estate=<name> to say which.",
				strings.Join(quoteAll(found), " and "), statelessUndiscoveredNote(needs),
			),
		))
	}
}

// statelessEstateFor resolves the estate name without deciding what a run
// should do when there is not one.
//
// The two sources are the same two [statelessEstateName] documents, and this
// is where they are actually read: an explicit -estate=<name>, checked against
// the marker grammar, then the distinct tofu-estate values the configuration
// stamps. The second return is those values, so that a caller with no answer
// can say why - "the configuration names no estate" and "the configuration
// names three" call for different sentences, and only the caller knows what
// its half of the run gave up.
func statelessEstateFor(ctx context.Context, flagValue string, config *configs.Config) (string, []string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if flagValue != "" {
		if !discovery.ValidEstateName(flagValue) {
			return "", nil, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Invalid estate name",
				fmt.Sprintf("-estate=%q does not match the tofu-estate marker grammar in live/MARKERS.md: a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters.", flagValue),
			))
		}
		return flagValue, nil, diags
	}

	found, evalDiags := statelessEstateFromConfig(ctx, config)
	diags = diags.Append(evalDiags)

	if len(found) == 1 && discovery.ValidEstateName(found[0]) {
		return found[0], found, diags
	}
	return "", found, diags
}

// statelessStamp injects this estate's ownership markers into every taggable
// resource that does not already declare them, so that the plan below shows
// the tags being added and an apply of it would write them.
//
// It runs after the estate-name derivation and after discovery on purpose:
// stamping rewrites resource bodies, and the derivation reads tofu-estate
// values back out of those same bodies. Deriving from a configuration this
// function had already stamped would be the tool reading its own handwriting
// and calling it the author's.
//
// With no estate name there is nothing to stamp with, and this is a warning
// rather than an error - the same graceful degradation discovery makes. A
// configuration that has never used markers still plans; it just does not
// gain them, and the warning says what to pass.
// slotTable is the slot each count instance carries, as discovery worked it
// out from the live set. It is nil when discovery did not run or found no
// count blocks, which is what tells the stamping pass to write no tofu-slot
// tags: a slot is a fact about the live estate, and a run that did not read
// the live estate has no business inventing one.
//
// needsDiscovery names the resource blocks whose instances can only be found
// by their ownership marker. It travels in because the severity of "this
// resource did not get its markers" depends on it: a bucket named by its own
// configuration survives being unmarked, and a subnet does not - it becomes a
// resource no later run can see, and every later plan proposes creating
// another one. See [stamp.Request.NeedsDiscovery].
//
// The pass's result comes back rather than being dropped on the floor: what a
// run stamped, and what it did not, is the record of whether the estate's
// ownership is intact after this plan, and a caller that cannot see it cannot
// check anything about it (audit finding C2).
//
// recordBackedBlocks is [recordBackedNeedsDiscoveryBlocks]'s result, or nil
// for a flag-off run - see that function's own doc comment for what it
// means and why [statelessStampGaps] is where it has to apply.
//
// GitHub issue #451: with [nodeResolveEnabled] true (the default since
// #388's own flip), this whole pass - the HCL rewrite in
// internal/live/stamp - does not run at all, and this function returns nil
// with no diagnostic, the same nil [stamp.Result] every caller already
// tolerates from the no-estate-name branch below. The node path
// (internal/live/projection.NodeResolver.AdjustConfigValue, wired in as
// tofu.ConfigValueAdjuster) writes the same two tags per instance, and as
// of this issue also carries the marker-conflict refusal and the #380
// ignore_changes protection this pass used to be the only source of - see
// nodestamp.go and nodestamp_ignorechanges.go. This is the redo of the
// gate the branch live/retire-stamp-gate (sha bb4299bc1e) attempted and
// reverted: that attempt gated this pass with neither capability ported
// yet, and TestLivePlan_markerConflictIsFatal and
// TestLivePlan_markersRecordPreservesExistingMarker (plus its
// _NodeResolve twin) failed. Both are green with the gate in place now.
func statelessStamp(ctx context.Context, config *configs.Config, estateFlag string, schemas *tofu.Schemas, slotTable map[string]string, needsDiscovery map[string]identity.BlockDiscovery, policyUntag map[string]string, recordBackedBlocks map[string]bool) (*stamp.Result, tfdiags.Diagnostics) {
	estate, declared, diags := statelessEstateFor(ctx, estateFlag, config)
	if diags.HasErrors() {
		return nil, diags
	}

	if estate == "" {
		switch {
		case len(declared) == 0:
			return nil, diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Ownership markers not stamped",
				fmt.Sprintf(
					"This run has no estate name, so the %s and %s tags from live/MARKERS.md were not added to the resources that do not already carry them: nothing in the configuration stamps a tofu-estate tag with a value readable from configuration alone, and no -estate=<name> was given. Resources this configuration creates or updates will carry no ownership marker, which means a later run cannot find them by marker and will report them as foreign. Pass -estate=<name> to stamp them.",
					discovery.TagEstate, discovery.TagAddress,
				),
			))
		case len(declared) == 1:
			return nil, diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Ownership markers not stamped",
				fmt.Sprintf(
					"The configuration stamps %s = %q, which does not match the marker grammar in live/MARKERS.md, so this run has no estate name to stamp missing markers with. Pass -estate=<name> to name the estate explicitly.",
					discovery.TagEstate, declared[0],
				),
			))
		default:
			return nil, diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Ownership markers not stamped",
				fmt.Sprintf(
					"Resources in this configuration stamp %s with %s. An estate is the unit of ownership and a run covers exactly one, so which of these to stamp the unmarked resources with is not something this command will pick. Pass -estate=<name> to say which.",
					discovery.TagEstate, strings.Join(quoteAll(declared), " and "),
				),
			))
		}
	}

	if nodeResolveEnabled() {
		// GitHub issue #451: the plan-node seam's own marker-conflict
		// detection and #380's ignore_changes protection
		// (internal/live/projection/nodestamp.go and
		// nodestamp_ignorechanges.go) now cover what this pass's HCL
		// rewrite used to, on the node path - see the revert this redoes,
		// issuecomment-5406571644 on #388, for why a blanket gate here was
		// not safe until those two capabilities existed. Both writer paths
		// (this one and NodeResolver.AdjustConfigValue) still agree on the
		// two marker keys they write, so nothing downstream that reads
		// res's tags needs to change - there is simply no res on this
		// path, exactly as the no-estate-name branch above already
		// produces nil, and every caller already tolerates that.
		return nil, diags
	}

	res, stampDiags := stamp.Stamp(ctx, stamp.Request{
		Estate:             estate,
		Config:             config,
		Schemas:            schemas,
		Slots:              slotTable,
		NeedsDiscovery:     needsDiscovery,
		PolicyUntag:        policyUntag,
		RecordBackedBlocks: recordBackedBlocks,
	})
	diags = diags.Append(stampDiags)
	return res, diags.Append(statelessStampGaps(res, needsDiscovery, recordBackedBlocks))
}

// statelessStampGaps re-checks the stamping pass's own report against the
// instances that can only be found by their marker.
//
// The pass already refuses to leave one of those unstamped, so this finds
// nothing in a working build - which is the point of it. The bug this fixes
// was not a missing rule but a discarded result: nothing downstream ever
// looked at what stamping did, so a resource silently skipped stayed silently
// skipped all the way into the cloud (audit finding C2). One reader of the
// report, checking the one property that matters, is what makes that
// impossible to reintroduce quietly.
//
// recordBackedBlocks is [recordBackedNeedsDiscoveryBlocks]'s result: a
// block this run would otherwise escalate, but whose every needs-discovery
// instance already has a usable identity in the estate's record store
// (GitHub issue #364's write half - liveimport and write-back record an
// untaggable instance's identity the same way a taggable one's marker
// covers it). Such a block is not "lost to every future run" the way this
// function's whole warning describes: the record is exactly the other way
// to find it again, so escalating on top of it would refuse an estate
// #364 already made safe. nil for a flag-off run - see that function's own
// doc comment for why, and why this stays a no-op (a nil map's lookup is
// always false) whenever it is.
func statelessStampGaps(res *stamp.Result, needsDiscovery map[string]identity.BlockDiscovery, recordBackedBlocks map[string]bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if res == nil || len(needsDiscovery) == 0 {
		return diags
	}
	for _, skip := range res.Skipped {
		// SkipModuleKeyedTrusted is exempt for the same reason
		// SkipAlreadyStamped is: the resource HAS its markers. It is inside a
		// for_each'd module and declares its own tags argument, which is the
		// hand-stamped idiom live/LIMITATIONS.md documents, and stamping
		// deliberately leaves it alone rather than failing to write anything.
		// Treating it as a gap tells an operator their marker is missing
		// while it sits in the file above the error. See #111.
		//
		// That exemption is only as sound as the reason it trusts, and until
		// GitHub issue #379 it was not sound at all for the population reached
		// here: this loop runs over resources that can ONLY be found by their
		// marker, and stamping reported MODULE_KEYED_TRUSTED for any of them
		// that merely SET a tags argument - `tags = var.tags` included - so a
		// server-assigned resource about to be created with no marker on it
		// was exempted by name. [stamp.SkipModuleKeyedTrusted] now requires
		// tofu-address to be written as a literal key in the body before it
		// claims the marker as the operator's, and GitHub issue #378 narrowed
		// it further in the other direction: a keyed-module resource that
		// declares no tofu-address is now STAMPED, through the module-prefix
		// symbol, rather than skipped at all. So the population reaching this
		// exemption is exactly the one that really does carry a hand-written
		// marker. Do not re-derive either check in this function: one
		// decision, in the pass that read the body, is the shape #111 taught.
		//
		// [stamp.SkipReason.Unknown] is exempt for a different reason, and
		// it is GitHub issue #230's: that skip records that this run could
		// not READ the type's schema, so whether the resource can carry a
		// marker at all is unknown. Reporting an unknown as "applying this
		// would create a resource this configuration can never find again"
		// states a fact nothing established. tofu.Schemas can come back
		// without a given type in three ordinary ways - a provider release
		// that predates the type, two providers serving one type name (which
		// statelessProviders.resourceSchemas drops rather than resolves), a
		// partial acquisition - and each one fails the run later with a
		// message that names the real problem.
		//
		// disco.Cause.BindsByName() is exempt for the reason
		// [stamp.stamper.mustStamp] already exempts it from the ERROR this
		// same skip would otherwise escalate to at stamp time: an untaggable
		// instance whose name AWS itself refuses to issue twice is found by
		// that name, marker or no marker (see [identity.DiscoveryUniqueName]).
		// This function re-derives severity from res.Skipped independently of
		// mustStamp's own verdict, and until this check existed it did not
		// consult BindsByName at all - so every unique-name type (
		// aws_cloudfront_cache_policy and siblings, issue #274) failed here
		// on its very first apply, unconditionally, before discovery ever
		// got a chance to bind it. mustStamp got this right from the start;
		// this reader had silently regressed the same population it exists
		// to protect.
		disco, marked := needsDiscovery[skip.Addr.String()]
		if skip.Reason == stamp.SkipAlreadyStamped || skip.Reason == stamp.SkipModuleKeyedTrusted || skip.Reason.Unknown() || !marked || disco.Cause.BindsByName() || recordBackedBlocks[skip.Addr.String()] {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unstamped marker-only resource",
			fmt.Sprintf(
				"Marker stamping reported %s for %s: %s %s",
				skip.Reason, skip.Addr, skip.Detail, stamp.UnmarkedDiscoveryDetail(skip.Addr, disco)),
		))
	}
	return diags
}

// statelessNeedsDiscovery is the set of resource blocks whose instances can
// only be found by their ownership marker, keyed by module-qualified block
// address, taken from identity resolution rather than from what discovery
// managed to bind: an instance discovery found is still one that nothing but
// its marker could have found.
func statelessNeedsDiscovery(resolutions *identity.Result) map[string]identity.BlockDiscovery {
	// The keying this needs - .Config(), not the keyed
	// [addrs.AbsResourceInstance] resolution walks - and the reason it is
	// load-bearing are both in
	// [identity.Result.DiscoveryCausesByBlock]'s own doc comment, along with
	// #111, the bug that came of this package and internal/live/check each
	// keeping their own copy of it.
	return resolutions.DiscoveryCausesByBlock()
}

// statelessUndiscoveredNote names what a run without discovery leaves
// unresolved, so that every warning above says what it costs.
func statelessUndiscoveredNote(needs []identity.Resolution) string {
	addrs := make([]string, 0, len(needs))
	for _, r := range needs {
		addrs = append(addrs, r.Addr.String())
	}
	sort.Strings(addrs)
	if len(addrs) > 6 {
		addrs = append(addrs[:6], fmt.Sprintf("and %d more", len(addrs)-6))
	}
	if len(needs) == 0 {
		return "Nothing in this configuration needs an ownership marker to be found, but the estate-wide sweep does: it is what finds resources this estate owns and no longer declares, and it did not run."
	}
	noun := "instances have"
	if len(needs) == 1 {
		noun = "instance has"
	}
	return fmt.Sprintf(
		"%d resource %s an identity the provider assigned at create time (%s), which only an ownership marker can recover; the plan below proposes creating them.",
		len(needs), noun, strings.Join(addrs, ", "))
}

// statelessEstateFromConfig reads the distinct tofu-estate values the
// configuration stamps, sorted, over the whole static module tree.
//
// The walk itself is [discovery.DeclaredEstateNames]. It used to be written
// out here and again, body for body, in internal/live/check - the two have
// to give the same answer, since they report on the same run, and nothing
// was watching either of them. Issue #285.
func statelessEstateFromConfig(ctx context.Context, config *configs.Config) ([]string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	return discovery.DeclaredEstateNames(ctx, config), diags
}

// statelessNeedsDiscoveryProviders is the set of provider configurations
// marker discovery's config-driven scan has to list through: every distinct
// one among the needs-discovery resolutions' own resource blocks, sorted so
// the caller's loop and its choice of primary are the same on every run.
//
// GitHub issue #283. This used to return exactly one, and refuse outright
// when the needs-discovery resolutions spanned more than one - "Marker
// discovery across several provider configurations" - on the reasoning that
// a list issued against the wrong account or region reports an estate as
// missing rather than as unreachable, which is a worse failure than a
// refusal. That reasoning is still exactly right; what changed is that it no
// longer implies a single configuration per run. Issue #69 had already built
// the mechanism that satisfies it: [discovery.Request.ScopeProvider] narrows
// which resolutions a pass treats as waiting to be found to the ones whose
// own resource block uses that pass's provider configuration, so a pass
// never lists for - and can never bind - a resource belonging to another
// account or region. Running one scoped pass per configuration and merging
// ([discovery.Merge]) issues every list against the configuration the
// resource itself names, which is stronger than the old rule rather than a
// relaxation of it: the old rule guaranteed one right answer and refused
// everything else, this one gives every resource its own right answer.
//
// The estate that made the difference matter is a CloudFront estate. WAFv2
// web ACLs for CloudFront and ACM certificates for CloudFront must live in
// one particular region while the rest of the estate does not, so AWS's own
// guidance produces a default provider configuration plus an aliased one,
// with discovery-needing resources on both sides. That is the ordinary shape
// of a CDN estate, not an exotic one.
//
// An empty result alongside no error means nothing needs discovery at all -
// a configuration entirely of client-named resources, for instance - which
// tells the caller the sweep alone gets to pick a provider.
func statelessNeedsDiscoveryProviders(config *configs.Config, needs []identity.Resolution) ([]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	seen := make(map[string]addrs.AbsProviderConfig)
	for _, r := range needs {
		modCfg, ok := identity.ConfigForModule(config, r.Addr.Module)
		if !ok || modCfg.Module == nil {
			continue
		}
		rc, ok := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]
		if !ok {
			continue
		}
		addr := providerConfigAddr(modCfg, rc)
		seen[addr.String()] = addr
	}

	if len(seen) == 0 {
		if len(needs) == 0 {
			return nil, diags
		}
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider for marker discovery",
			"Resource instances are waiting on marker discovery, but none of them could be traced back to a resource block in the configuration. The resolutions and the configuration come from different runs; this is a bug.",
		))
	}

	return sortedProviderConfigs(seen), diags
}

// sortedProviderConfigs flattens a set of provider configurations keyed by
// their own address string into a slice in address order, so every loop over
// them and every "first one" taken from them is the same on every run
// regardless of Go's map iteration order.
func sortedProviderConfigs(seen map[string]addrs.AbsProviderConfig) []addrs.AbsProviderConfig {
	out := make([]addrs.AbsProviderConfig, 0, len(seen))
	for _, addr := range seen {
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// statelessManagedResourceProviders is the estate-wide sweep's candidate
// set: every distinct provider configuration among the configuration's
// managed resources, sorted for a deterministic loop order. A configuration
// with one entry here behaves exactly as it always has - a single call to
// [discovery.Discover], no merge step at all. More than one is issue #69's
// case: statelessDiscover loops the sweep once per entry and
// [discovery.Merge] combines the results, rather than refusing the way this
// package used to whenever an estate's managed resources spanned more than
// one provider configuration regardless of whether marker discovery was
// even implicated.
func statelessManagedResourceProviders(config *configs.Config) []addrs.AbsProviderConfig {
	seen := make(map[string]addrs.AbsProviderConfig)
	walkManagedResources(config, func(rc *configs.Resource, modCfg *configs.Config) {
		if ti, ok := identity.LookupType(rc.Type); ok && ti.RecordBacked {
			// GitHub issue #73's record-backed resources (null_resource,
			// terraform_data, and the time_*, random_*, tls_* and local_*
			// families - including the secret-bearing ones since issue #365
			// slice 3, which are record-backed exactly as their siblings are
			// and differ only in whether the operator asked for the record to
			// be written) have no
			// cloud object and no marker of any kind, so they are never a
			// candidate for the estate-wide sweep's provider set: there is
			// nothing for a sweep issued through their provider to find,
			// and no discovery.Discover call makes sense against a
			// provider with no listable, taggable resources at all.
			return
		}
		addr := providerConfigAddr(modCfg, rc)
		seen[addr.String()] = addr
	})
	if len(seen) == 0 {
		// day2_remove's own shape (live/GAUNTLET.md #7), not a configuration
		// with nothing to sweep: this walk answers "which providers does a
		// managed resource use right now", and a block that WAS declared
		// and got deleted this run is exactly the case a removal sweep
		// exists to catch - it can never show up in this walk by
		// construction, because the walk only ever sees what is still
		// declared. Before this fallback, an estate whose last non-record-
		// backed resource had just been removed (this walk's only
		// remaining candidate) skipped statelessDiscover's sweep entirely,
		// silently: the caller's own "nothing to find, nothing that could
		// be undeclared" comment was true of the walk's OWN candidate set
		// but false of the estate, whose orphan the sweep exists precisely
		// to find. Falling back to the root module's own declared provider
		// blocks - present whether or not anything currently uses them -
		// keeps the estate-wide sweep running against the same provider(s)
		// this estate has always planned through, the same way stock's own
		// state-based orphan handling never depends on the CURRENT config
		// still declaring an instance of the removed block's type. Found
		// crossing corpus-lambda-simple's own day2_remove: the estate's
		// sole module call is its only non-record-backed resource source,
		// so deleting it left ONLY random_pet.this declared - record-
		// backed, and excluded above by construction - and the sweep for
		// the function, the role and the log group never ran at all.
		for _, pc := range config.Module.ProviderConfigs {
			addr := config.ResolveAbsProviderAddr(addrs.LocalProviderConfig{LocalName: pc.Name, Alias: pc.Alias}, addrs.RootModule)
			seen[addr.String()] = addr
		}
	}
	return sortedProviderConfigs(seen)
}

// statelessDiscoveryPassProviders is the set of provider configurations
// [statelessDiscover] runs one scoped [discovery.Discover] pass through: the
// union of the estate-wide sweep's candidates and the configurations the
// needs-discovery resolutions themselves use.
//
// The union rather than the sweep set alone is what makes "every
// needs-discovery resolution has exactly one pass scoped to it" a property
// of the construction. Both sets are derived the same way - every managed
// resource block's own [providerConfigAddr] - so in every case that can
// actually arise the needs set is already contained in the sweep set and the
// union changes nothing. The one difference between them is
// [statelessManagedResourceProviders]'s record-backed filter, and taking the
// union means a resolution that ever ends up both record-backed and waiting
// on marker discovery still gets a pass instead of being silently skipped by
// every one of them, which would leave it unbound with nothing said about
// why. A pass that finds nothing is visible in the run's own report; a
// resolution with no pass at all is a resource nobody looked for, and
// nothing says so.
func statelessDiscoveryPassProviders(sweep, needs []addrs.AbsProviderConfig) []addrs.AbsProviderConfig {
	seen := make(map[string]addrs.AbsProviderConfig, len(sweep)+len(needs))
	for _, addr := range sweep {
		seen[addr.String()] = addr
	}
	for _, addr := range needs {
		seen[addr.String()] = addr
	}
	return sortedProviderConfigs(seen)
}

// statelessForeignReport converts the classification into the view's wire
// format. It carries data across, never rendered text: the wording of the
// section is the view's business, and this function producing sentences
// would put half the output in the wrong package.
func statelessForeignReport(res *foreign.Result, disco *discovery.Result) views.StatelessForeign {
	if res == nil {
		return views.StatelessForeign{}
	}

	rep := views.StatelessForeign{
		Estate:       res.Estate,
		Swept:        res.Swept,
		SweepCovered: res.SweepCovered,
	}
	if disco != nil {
		// the CollectUnclaimed ruling (#604):
		// a run that did not ask the account-inventory question must say
		// so rather than let "nothing was swept" read as "there is
		// nothing". See [discovery.Result.NativeSweepSkipped].
		rep.NativeSweepSkipped = disco.NativeSweepSkipped
	}
	for _, rm := range res.Removals {
		rep.Removals = append(rep.Removals, views.StatelessRemoval{
			Addr:        rm.Addr.String(),
			TypeName:    rm.TypeName,
			LiveID:      rm.LiveID,
			DisplayName: rm.DisplayName,
			Marker:      rm.Normalized,
			BlockGone:   rm.BlockGone,
			Swept:       rm.Swept,
			Why:         rm.Why,
		})
	}
	for _, g := range res.SweepGaps {
		rep.SweepGaps = append(rep.SweepGaps, views.StatelessSweepGap{
			TypeName: g.TypeName,
			Reason:   string(g.Reason),
			Detail:   g.Detail,
		})
	}
	for _, f := range res.Foreign {
		rep.Items = append(rep.Items, views.StatelessForeignItem{
			TypeName:    f.TypeName,
			LiveID:      f.LiveID,
			DisplayName: f.DisplayName,
			Tags:        statelessTags(f.Tags),
			Why:         f.Why,
		})
	}
	for _, c := range res.Candidates {
		matched := make([]views.StatelessTag, 0, len(c.Matched))
		for _, m := range c.Matched {
			matched = append(matched, views.StatelessTag{Key: m.Attr, Value: m.Value})
		}
		rep.Candidates = append(rep.Candidates, views.StatelessBindCandidate{
			Addr:          c.Addr.String(),
			TypeName:      c.TypeName,
			LiveID:        c.LiveID,
			DisplayName:   c.DisplayName,
			Tags:          statelessTags(c.Tags),
			Matched:       matched,
			MarkerEstate:  c.MarkerEstate,
			MarkerAddress: c.MarkerAddress,
			Hint:          c.Hint,
		})
	}
	for _, r := range res.Renames {
		rep.Renames = append(rep.Renames, views.StatelessRename{
			OldAddr:     r.Old.String(),
			NewAddr:     r.New.String(),
			TypeName:    r.TypeName,
			LiveID:      r.LiveID,
			DisplayName: r.DisplayName,
			Command:     r.Command,
		})
	}
	for _, a := range res.Ambiguous {
		live := make([]string, 0, len(a.Live))
		for _, l := range a.Live {
			id := l.LiveID
			if id == "" {
				id = "no identity"
			}
			if l.DisplayName != "" && l.DisplayName != l.LiveID {
				id += ", " + l.DisplayName
			}
			live = append(live, fmt.Sprintf("%s (%s)", l.Marker, id))
		}
		rep.AmbiguousRenames = append(rep.AmbiguousRenames, views.StatelessRenameAmbiguity{
			Block:    a.Block,
			Live:     live,
			Declared: a.Declared,
			Detail:   a.Detail,
		})
	}
	for _, e := range res.OtherEstates {
		rep.OtherEstates = append(rep.OtherEstates, views.StatelessEstateCount{
			Estate: e.Estate,
			Count:  e.Count,
			Types:  e.Types,
		})
	}
	for _, u := range res.Unswept {
		rep.Unswept = append(rep.Unswept, views.StatelessUnsweptType{
			TypeName: u.TypeName,
			Reason:   string(u.Reason),
			Detail:   u.Detail,
		})
	}
	for _, f := range res.ParentReads {
		rep.ParentReads = append(rep.ParentReads, views.StatelessParentRead{
			TypeName:    f.TypeName,
			Parent:      f.Parent,
			ParentAddr:  f.ParentAddr.String(),
			ParentValue: f.ParentValue,
			LiveID:      f.LiveID,
			DisplayName: f.DisplayName,
			Removal:     f.Removal,
			Withheld:    f.Withheld,
		})
	}
	return rep
}

// statelessPlannedCreates is every address the plan actually proposes to
// create, in the order [plans.Changes.Resources] carries them - the input
// the lookalike guard needs, since it warns about the plan's own actions
// rather than about what discovery merely left unbound. A replace (however
// it is spelled: [plans.Replace], [plans.DeleteThenCreate],
// [plans.CreateThenDelete], [plans.ForgetThenCreate]) is deliberately
// excluded: the address already has a prior-state entry, so there is no
// question of it duplicating a live resource nobody owns.
func statelessPlannedCreates(plan *plans.Plan) []addrs.AbsResourceInstance {
	if plan == nil || plan.Changes == nil {
		return nil
	}
	var out []addrs.AbsResourceInstance
	for _, rc := range plan.Changes.Resources {
		if rc.Action == plans.Create {
			out = append(out, rc.Addr)
		}
	}
	return out
}

// statelessLookalikeReport converts the lookalike guard's findings into the
// view's wire format, the same split [statelessForeignReport] and
// [statelessUnownedReport] keep: data across the package boundary, never
// rendered text.
func statelessLookalikeReport(warnings []foreign.Lookalike) []views.StatelessLookalike {
	if len(warnings) == 0 {
		return nil
	}
	out := make([]views.StatelessLookalike, 0, len(warnings))
	for _, w := range warnings {
		matched := make([]views.StatelessTag, 0, len(w.Matched))
		for _, m := range w.Matched {
			matched = append(matched, views.StatelessTag{Key: m.Attr, Value: m.Value})
		}
		out = append(out, views.StatelessLookalike{
			Addr:          w.Addr.String(),
			TypeName:      w.TypeName,
			LiveID:        w.LiveID,
			DisplayName:   w.DisplayName,
			Matched:       matched,
			MarkerEstate:  w.MarkerEstate,
			MarkerAddress: w.MarkerAddress,
			Hint:          w.Hint,
		})
	}
	return out
}

func statelessTags(tags []foreign.Tag) []views.StatelessTag {
	out := make([]views.StatelessTag, 0, len(tags))
	for _, t := range tags {
		out = append(out, views.StatelessTag{Key: t.Key, Value: t.Value})
	}
	return out
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}

// providerConfigAddr is the absolute provider configuration a resource block
// uses: [providerscope.ResolveResource] walking every ancestor module call's
// `providers = {...}` mapping between modCfg (the static module the block
// itself is declared in) and the root, honouring an aliased mapping instead
// of ignoring it. GitHub issue #188; the resolution core is
// internal/live/providerscope, built and tested separately from this
// wiring.
func providerConfigAddr(modCfg *configs.Config, rc *configs.Resource) addrs.AbsProviderConfig {
	return providerscope.ResolveResource(modCfg, rc)
}

// walkManagedResources calls fn once for every managed resource in cfg's
// whole static module tree, root first, then children in name order - the
// command-layer counterpart of the five walkers' own traversal, for the
// handful of places here that still need to look a resource block up by
// hand rather than through an identity.Resolution's already module-
// qualified address. fn is given the [configs.Config] node the resource is
// declared in, not just its path, because [providerConfigAddr] needs the
// node itself to walk ancestor module calls' providers mappings.
func walkManagedResources(cfg *configs.Config, fn func(rc *configs.Resource, modCfg *configs.Config)) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	for _, rc := range cfg.Module.ManagedResources {
		fn(rc, cfg)
	}
	for _, name := range identity.SortedChildNames(cfg.Children) {
		walkManagedResources(cfg.Children[name], fn)
	}
}

// statelessUnownedReport converts the projection's refused live resources
// into the view's wire format. estate is this run's estate name: with one, an
// unmarked resource is adoptable and the exact tag values to write travel
// along; without one there is nothing to offer, and the entry renders as in
// the way with the reason. Like statelessForeignReport, this carries data
// across and never rendered text.
func statelessUnownedReport(res *projection.Result, estate string) []views.StatelessUnowned {
	if res == nil {
		return nil
	}
	items := make([]views.StatelessUnowned, 0, len(res.Unowned))
	for _, u := range res.Unowned {
		item := views.StatelessUnowned{
			Addr:     u.Addr.String(),
			TypeName: u.TypeName,
			LiveID:   u.ImportID,
			HeldBy:   u.Estate,
		}
		if u.Estate == "" && estate != "" {
			item.MarkerEstate = estate
			item.MarkerAddress = markers.EscapeAddress(u.Addr.String())
		}
		items = append(items, item)
	}
	return items
}

// statelessOmissions converts the projection's omissions into the view's
// wire format, preserving the builder's ordering.
func statelessOmissions(res *projection.Result) []views.StatelessOmission {
	if res == nil {
		return nil
	}
	oms := make([]views.StatelessOmission, 0, len(res.Omitted))
	for _, om := range res.Omitted {
		oms = append(oms, views.StatelessOmission{
			Addr:   om.Addr.String(),
			Reason: string(om.Reason),
			Detail: om.Detail,
		})
	}
	return oms
}

// liveStateFileNote reports a state file sitting in the working
// directory. Stateless mode does not read it, does not write it, and does not
// care what it says, but silently ignoring a file that every other OpenTofu
// command treats as authoritative would be a nasty surprise.
func (c *LivePlanCommand) liveStateFileNote() tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	path := filepath.Join(c.WorkingDir.RootModuleDir(), arguments.DefaultStateFilename)
	if _, err := os.Stat(path); err != nil {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		"State file present but not consulted",
		fmt.Sprintf(
			"%s exists in this directory and was not read. A live-markers run has no authoritative state: prior state for this plan was built by reading the live system, and nothing was written back. Whatever that file records has no effect on the plan below, and the file itself is left untouched.",
			path,
		),
	))
}

// statelessDataReads is GitHub issue #179's pre-resolution data-read phase,
// shared by every command that resolves identity: live-plan, plain
// plan/apply under a live block (live_mode.go), and live-mv. It analyzes
// offline which data sources identity resolution demands, and when there
// are any, reads the eligible ones through the same configured provider
// instances the projection builder uses. The returned map feeds
// [identity.Context.DataResults].
//
// An error is fatal to the run: either a demanded data source is not
// readable before the plan (the analysis's class-specific refusal says
// why), or a read itself failed (the provider's error, quoted). Proceeding
// past one would just re-refuse the same sites with the generic wording.
//
// The common case - a configuration whose identities need no data source -
// takes the analysis's probe and nothing else: no provider configured, no
// network call, no behavior change.
//
// # The provider boundary, and why this path has one now
//
// This call site handed the read phase the UNRESTRICTED provider seam until
// an adversarial audit on 2026-08-21. The root-output class one function
// down had had [liveProviderReads] since the class was built, and the
// reasoning written down for leaving this one alone was that identity's
// contract is fatal, so confining it would turn a read into a refusal.
//
// That reasoning had the sign backwards. HANDOFF.md's "a wrong marker
// outranks a missing one" says a refusal is the better of the two outcomes,
// and the thing being refused here is running an arbitrary local program -
// data "external"'s whole contract - to work out where to write a marker,
// before discovery, before lint, before anything in the run could stop it.
// This is also the path with the reach: live/LIMITATIONS.md counts the
// identity read class in the thousands of sites, against the root-output
// class's handful.
//
// So both classes now pass through the same seam, built from the same
// derivation ([dataread.ReadableProviders]), and the analysis draws the same
// line a second time ([dataread.Options.ProviderManagedTypes]). What still
// differs between them is only what an excluded source COSTS: here it is
// [dataread.Read]'s existing fatal refusal, naming the data source and the
// provider; there it is one output rendering as "+".
func statelessDataReads(ctx context.Context, config *configs.Config, provs livePlanProviders, resourceSchemas map[string]providers.Schema, scope identity.Scope) (map[string]cty.Value, tfdiags.Diagnostics) {
	managedTypes := provs.managedTypesByProvider(ctx)
	analysis := dataread.Analyze(ctx, config, dataread.Options{
		Schemas:              resourceSchemas,
		Scope:                scope,
		ProviderManagedTypes: managedTypes,
	})
	if analysis.Empty() {
		return nil, nil
	}
	return dataread.Read(ctx, config, analysis, liveProviderReads{
		inner: provs,
		live:  dataread.ReadableProviders(config, analysis, managedTypes),
	})
}

// statelessRootOutputDataReads is the same phase's SECOND demand class,
// GitHub issue #349's sub-problem 2: the data sources a root-level `output`
// block's value reaches, read live so that [projection.ApplyRootOutputValues]
// can compute the output's prior value instead of leaving it unset and
// letting every run render it as newly created.
//
// It is scoped, not fatal, in both halves. The analysis
// ([dataread.AnalyzeRootOutputs]) skips what it cannot read rather than
// refusing the configuration, and the read ([dataread.ReadForOutputs])
// carries on past a source that fails. Nothing this function returns can
// stop a run: the worst case is the one an operator already had, an output
// showing "+".
//
// The provider seam it hands the read phase is [liveProviderReads], the same
// one the identity class above uses since the boundary was widened to cover
// both.
//
// Cost: one ReadDataSource per data BLOCK a root output reaches, shared
// across that block's instances, and none at all for a configuration whose
// outputs reach no data source - which is every estate the plan-call budget
// ratchet (live/plan-budget.json) measures, since tools/estate-gen's
// generated estates declare no root outputs.
//
// # Known limitation: the same data source is read twice per run
//
// This pre-plan read and the plan graph's own read of the same data block are
// two separate ReadDataSource calls to the provider. For an ordinary data
// source that costs one extra remote GET. For a data source whose read has a
// SIDE EFFECT - data.vault_aws_access_credentials mints a fresh STS
// credential per call is the clearest example - it means two credentials get
// minted where an operator running stock OpenTofu would see one.
//
// It is not cheaply avoidable, and the reason is upstream rather than here.
// internal/tofu's planDataSource sets priorVal to cty.NullVal
// unconditionally: the plan graph never consults prior state for a data
// source, so there is no value to seed and no cache to prime. Suppressing the
// second read would mean changing stock OpenTofu's own data-source planning,
// which is exactly what HANDOFF.md's "parity is the bar" rules out - and the
// failure mode of getting it wrong is a plan rendered against a stale read.
//
// Nor can the phase avoid it by declining to read side-effecting sources:
// nothing in a provider schema says a data source has a side effect, so the
// only way to know is a hand-list of type names, which the standing bar
// ("everything must be derived") refuses and which would buy exactly the
// types someone thought of.
//
// The identity read class has had the same property since #179 and for the
// same reason; this is a note about both, written here because this is the
// class that widened the population it applies to.
func statelessRootOutputDataReads(ctx context.Context, config *configs.Config, provs livePlanProviders, resourceSchemas map[string]providers.Schema, scope identity.Scope) (map[string]cty.Value, tfdiags.Diagnostics) {
	managedTypes := provs.managedTypesByProvider(ctx)
	analysis := dataread.AnalyzeRootOutputs(ctx, config, dataread.Options{
		Schemas:              resourceSchemas,
		Scope:                scope,
		ProviderManagedTypes: managedTypes,
	})
	if analysis.Empty() {
		return nil, nil
	}
	return dataread.ReadForOutputs(ctx, config, analysis, liveProviderReads{
		inner: provs,
		live:  dataread.ReadableProviders(config, analysis, managedTypes),
	})
}

// livePlanProviders is what both read classes need from the command layer's
// provider pool: a configured provider per provider configuration, plus which
// managed resource types each provider's own schema declares.
//
// It is an interface rather than [*statelessProviders] for the reason
// [statelessResolve] takes one: the whole hazard these two functions carry is
// WHICH provider answers, and a test that cannot substitute one cannot see
// that. Before this existed, the two seams had no command-layer test at all
// and a revert of the confinement below would have gone unnoticed by CI.
type livePlanProviders interface {
	dataread.Providers

	// managedTypesByProvider feeds [dataread.Options.ProviderManagedTypes]
	// and [dataread.LiveProviders]. Unexported so the interface stays this
	// package's own seam rather than a general one.
	managedTypesByProvider(ctx context.Context) map[addrs.Provider]map[string]bool
}

// liveProviderReads is the structural half of the data-read phase's safety
// boundary: a [dataread.Providers] seam that can only ever hand back a
// provider this configuration manages live objects through
// ([dataread.LiveProviders]), or one of the two separately-ruled cross-stack
// read classes' own providers ([dataread.ReadableProviders]).
//
// The analysis draws the same line, and this draws it again at the one point
// where it becomes a real process doing a real thing. That redundancy is the
// point. The rule being enforced is that live-plan's pre-plan phase makes
// read-only calls to the same remote APIs the projection is already reading
// and does nothing else - and the shape that would violate it,
// data "external", runs a program named by its own arguments on the machine
// running the plan. A boundary that exists only as a classification is one
// refactor away from being bypassed by a caller that constructs an analysis
// some other way; a boundary that also owns the provider handle cannot be.
//
// Both demand classes pass through it. The identity class did not until an
// adversarial audit found it unconfined - see [statelessDataReads] - which is
// the reason the redundancy paragraph above is not decoration: the
// classification half of the boundary had been correct all along for a class
// whose call site did not use it.
type liveProviderReads struct {
	inner dataread.Providers
	live  map[addrs.Provider]bool
}

func (p liveProviderReads) ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	if !p.live[addr.Provider] {
		return nil, fmt.Errorf(
			"%s manages no live object in this configuration, so the pre-plan data-read phase will not configure it: this phase reads only the remote APIs this run is already reading the estate through",
			addr.Provider)
	}
	return p.inner.ConfiguredProvider(ctx, addr)
}

// statelessTargetScope is GitHub issue #352: which resource blocks a
// -target or -exclude run still evaluates, for the passes that run in front
// of the plan and so have no graph of their own to prune.
//
// It is nil - and costs nothing at all - for a run that passed neither flag,
// which is every run this fork has made until now and every run of an estate
// that does not scope itself.
//
// For a run that did pass one, the answer comes from the plan graph, through
// [tofu.Context.TargetedResources], rather than from a rule this package
// works out for itself. Targeting includes a targeted resource's
// dependencies, transitively, over the same reference edges the graph is
// built from; re-deriving that here would be a second set of targeting
// semantics, and the failure mode when two such sets drift is a projection
// missing a resource the plan does act on, which plans a create over
// something that already exists.
//
// The state passed to the graph build is empty on purpose. This asks which
// CONFIGURATION blocks survive, and the projection that would fill a state in
// is three passes further down - it is the thing this scope exists to let run
// at all.
func statelessTargetScope(ctx context.Context, tfCtx *tofu.Context, config *configs.Config, targets, excludes []addrs.Targetable) (identity.Scope, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if len(targets) == 0 && len(excludes) == 0 {
		return nil, diags
	}

	kept, graphDiags := tfCtx.TargetedResources(ctx, config, states.NewState(), &tofu.PlanOpts{
		Mode:        plans.NormalMode,
		SkipRefresh: true,
		Targets:     targets,
		Excludes:    excludes,
	})
	diags = diags.Append(graphDiags)
	if graphDiags.HasErrors() {
		return nil, diags
	}
	log.Printf("[TRACE] live: -target/-exclude leaves %d resource block(s) in the plan graph", len(kept))

	return func(addr addrs.ConfigResource) bool {
		_, ok := kept[addr.String()]
		return ok
	}, diags
}

// statelessResolve is the identity resolution every stateless command runs -
// live-plan, plain plan/apply under a live block, and live-mv - with GitHub
// issue #284's second pass folded in. All three call it rather than resolving
// for themselves, because a rename computed over a different identity map
// from the plan's would rewrite a marker the plan then disputes.
//
// # The two passes
//
// A first pass refuses the ACM/Route53 validation shape, and every shape like
// it, for a value the PROVIDER knows and the configuration cannot state:
//
//	resource "aws_route53_record" "cert_validation" {
//	  for_each = { for dvo in aws_acm_certificate.cert.domain_validation_options : ... }
//	}
//
// [identity.DemandedManagedReads] reads that pass's own refusals back as the
// managed resource blocks whose values would settle them.
// [projection.PlanInstances] then asks each block's own provider what it
// would fill in for a create - no cloud, no marker, no discovery, only a
// configured provider process - and a second pass resolves with those values
// in hand.
//
// # The bound: exactly one retry, and it is structural
//
// This is straight-line code rather than a loop, and the bound is a property
// of PlanInstances rather than a budget someone could raise.
// [projection.PlanInstances] plans a CREATE from a null prior state and never
// consults the resolution at all, so a third pass would be handed the
// identical value map the second was, and would produce the identical answer.
// A counter would suggest otherwise.
//
// # Why the second pass has to earn its place
//
// The first pass's diagnostics are kept unless the second pass raises
// strictly fewer errors. A second pass is given MORE information, so it
// ordinarily refuses less; but supplying [identity.Context.ManagedResults]
// also changes which references resolution treats as symbolic, and a
// reference that took the symbolic formula route on the first pass can take
// the evaluate-and-refuse route on the second. Without this test, wiring the
// second pass in would trade a class of refusals away for another class of
// refusals somewhere else, and the totals would hide it.
//
// The common case pays nothing at all: a first pass that resolved cleanly
// returns immediately, and one that refused for reasons naming no managed
// block never configures a provider.
// provs is the interface rather than [*statelessProviders] on purpose: the
// second pass's whole hazard is which provider answers, and a test that
// cannot substitute one cannot see that.
func statelessResolve(ctx context.Context, config *configs.Config, provs projection.Providers, resourceSchemas map[string]providers.Schema, dataResults map[string]cty.Value, scope identity.Scope) (*identity.Result, tfdiags.Diagnostics) {
	ictx := identity.Context{
		Schemas:     resourceSchemas,
		DataResults: dataResults,
		// GitHub issue #352, and nil for an untargeted run. See
		// [statelessTargetScope] and [identity.Scope].
		Scope: scope,
	}
	first, firstDiags := identity.ResolveWith(ctx, config, ictx)
	if !firstDiags.HasErrors() {
		return first, firstDiags
	}

	// Offline, and free: this reads the first pass's own refusals. Nothing
	// demanded means a second pass has nothing new to work from, so no
	// provider is configured and no plan call is made.
	if len(identity.DemandedManagedReads(first, firstDiags)) == 0 {
		return first, firstDiags
	}

	planned, planDiags := projection.PlanInstances(ctx, config, provs)
	// PlanInstances never fails its caller - a resource it cannot plan is
	// simply absent - so these are logged rather than raised. Raising them
	// would turn a run that refuses today into a run that refuses today plus
	// an aside about a provider.
	for _, d := range planDiags {
		log.Printf("[TRACE] live: planning managed values for the second resolution pass: %s", d.Description().Summary)
	}
	if len(planned) == 0 {
		return first, firstDiags
	}

	ictx.ManagedResults = planned
	second, secondDiags := identity.ResolveWith(ctx, config, ictx)
	if errorCount(secondDiags) >= errorCount(firstDiags) {
		log.Printf("[TRACE] live: the second resolution pass refused %d site(s) against the first pass's %d; keeping the first pass",
			errorCount(secondDiags), errorCount(firstDiags))
		return first, firstDiags
	}
	if downgraded := downgradedToDiscovery(first, second); downgraded != "" {
		log.Printf("[TRACE] live: the second resolution pass downgraded %s to needs-discovery; keeping the first pass", downgraded)
		return first, firstDiags
	}
	return second, secondDiags
}

// errorCount is the number of error-severity diagnostics, which is what
// [statelessResolve] compares its two passes on. Warnings are excluded
// deliberately: a pass that resolved more and warned more about what it
// resolved is the better pass.
func errorCount(diags tfdiags.Diagnostics) int {
	n := 0
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			n++
		}
	}
	return n
}

// downgradedToDiscovery names an instance the first pass resolved to a
// computable identity and the second pass demoted to [identity.
// ClassNeedsDiscovery], or "" when there is none. It is the half of the
// ratchet that counting error diagnostics cannot see, and it is measured
// rather than hypothetical.
//
// Supplying [identity.Context.ManagedResults] makes a reference to a covered
// managed resource evaluable, so [identity.resolver.isSymbolic] stops routing
// it down the symbolic-formula path. For an argument whose covered value is
// UNKNOWN that trades a working formula for a discovery request. Measured on
// simpleinfra's shared acm-certificate module against the real AWS provider
// 6.59.0: aws_acm_certificate_validation.cert resolves
// "PARENT_DERIVED ${aws_acm_certificate.cert.arn}" on a first pass and
// NEEDS_DISCOVERY/SIBLING_APPLY on a second. That type is untaggable, so the
// demotion turns into a hard stamp refusal one stage later - a refusal this
// function's caller never sees, because internal/live/stamp runs downstream
// of it. Counting identity's own errors would therefore have called a net
// LOSS a net win.
//
// The right fix is in internal/live/identity: a covered reference whose value
// is unknown should fall back to the symbolic path, so the formula survives.
// Until it does, this keeps the second pass from being enabled at a cost.
func downgradedToDiscovery(first, second *identity.Result) string {
	if first == nil || second == nil {
		return ""
	}
	was := make(map[string]identity.Class, first.Len())
	for _, r := range first.All() {
		was[r.Addr.String()] = r.Class
	}
	for _, r := range second.All() {
		if r.Class != identity.ClassNeedsDiscovery {
			continue
		}
		if prior, ok := was[r.Addr.String()]; ok && prior != identity.ClassNeedsDiscovery {
			return r.Addr.String()
		}
	}
	return ""
}

// statelessProviderDataReads is GitHub issue #313's provider-configuration
// dependency-order fixpoint: whatever a PROVIDER BLOCK's own arguments need
// - `provider "kubernetes" { host = data.aws_eks_cluster.cluster.endpoint }`
// is the corpus-eks-basic shape this exists for - read ahead of time, so
// [statelessProviders.providerConfigValue] can configure a provider whose
// configuration reads another provider's already-existing live object, the
// same value stock OpenTofu's ordinary plan graph would supply once prior
// state exists.
//
// It is [statelessResolve]'s own two-pass shape run one layer over, and it
// reuses that fixpoint's exact machinery rather than a new one:
// [dataread.AnalyzeProviderConfigs] classifies (a demand class of its own,
// never probed by identity resolution because a provider block is not an
// identity-bearing position); [identity.DemandedManagedReads] - already
// built for identity's own refusals - reads [dataread.Analysis.
// ManagedRefusals] just as well, because both raise the identical
// [configs.RefusedReference]-tagged diagnostic; [projection.ReadInstances] -
// #187's read half, built and tested, never wired to a production caller
// until this one - performs the one narrow live read the demand names,
// using resolutions' own already-settled identity for it; a second analyze-
// and-read pass with that value supplied closes the loop.
//
// A source neither pass can read costs nothing new: [dataread.
// AnalyzeProviderConfigs] is SCOPED, so an unreadable one is simply absent
// from the result, and providerConfigValue sees exactly the same "Provider
// unavailable" diagnostic it always has for that provider configuration -
// unchanged, not a new refusal. Bounded to maxProviderDataReadPasses passes,
// never unbounded: a pass that gains nothing new over the pass before it
// has nothing left to offer a further one, so the loop always stops itself
// well before the cap in the overwhelmingly common case (zero or one
// provider-config data source at all), and the cap is a backstop for a
// demand chain deeper than anything measured yet.
//
// A pass beyond the first closes a read-side chain more than one hop deep:
// corpus-eks-basic's own provider "kubernetes" block reads
// data.aws_eks_cluster.cluster, which reads module.eks.cluster_id (a module
// output), which reads aws_eks_cluster.this[0].id - the shape the original
// single retry closed. That instance's own identity is GitHub issue #364's
// PARENT_DERIVED shape (name = local.cluster_name =
// "test-eks-${random_string.suffix.result}"), so reading it needs its own
// parent's value first - [expandFormulaParents] pulls that parent
// (random_string.suffix, record-backed, no live object at all) into the
// SAME [projection.ReadInstances] call, and recordStore is what lets that
// call materialize it (see [ReadInstances]'s own recordBacked branch)
// instead of omitting it as before. Once that parent is read, a pass this
// loop's cap makes possible - not the original single retry - is what
// reads aws_eks_cluster.this[0]'s own live attributes in turn.
//
// provs is confined through [liveProviderReads] for the data-read half,
// exactly as [statelessDataReads] confines its own - the 2026-08-21 audit's
// finding was that this package's OWN wiring, not internal/live/dataread's
// classification, is where an unconfined seam let an ordinary configuration
// run a local-execution provider from an identity-bearing position, and a
// second demand class built the same way inherits the same duty to draw
// the line again at this call site rather than trust classification alone.
// [projection.ReadInstances]'s own call is not similarly wrapped, matching
// [statelessResolve]'s identical, already-audited call to
// [projection.PlanInstances]: a managed resource's provider comes from its
// own declared block, never from this phase's data-source boundary.
// readPar is GitHub issue #626's knob, resolved once by the caller (both
// callers do it at the top of the run, before any provider process starts) and
// taken as an argument rather than read from the environment here, so that one
// run cannot use two different bounds and a bad setting is reported once.
//
// It is inert on this path today, and stated rather than left to be
// rediscovered: [projection.ReadInstances] reads its concrete instances through
// the same sequential materialize loop it always has - only
// [projection.BuildWith]'s own concrete phase starts a read prefetch - so
// nothing here is concurrent at any setting. It is passed anyway because this
// is a projection read pass built from a [projection.Options], and the day
// ReadInstances grows the same prefetch, it should inherit the bound the
// operator set for the run rather than silently take ten.
func statelessProviderDataReads(ctx context.Context, config *configs.Config, provs livePlanProviders, resourceSchemas map[string]providers.Schema, resolutions *identity.Result, recordStore *projection.RecordStore, readPar int) map[string]cty.Value {
	managedTypes := provs.managedTypesByProvider(ctx)
	opts := dataread.Options{Schemas: resourceSchemas, ProviderManagedTypes: managedTypes}
	confined := func(a *dataread.Analysis) dataread.Providers {
		return liveProviderReads{inner: provs, live: dataread.ReadableProviders(config, a, managedTypes)}
	}

	analysis := dataread.AnalyzeProviderConfigs(ctx, config, opts)
	results, diags := dataread.ReadProviderConfigs(ctx, config, analysis, confined(analysis))
	for _, d := range diags {
		log.Printf("[TRACE] live: provider-configuration data reads: %s", d.Description().Summary)
	}

	live := map[string]cty.Value{}
	readOpts := projection.Options{RecordStore: recordStore, ReadParallelism: readPar}
	const maxProviderDataReadPasses = 5
	for pass := 1; pass < maxProviderDataReadPasses; pass++ {
		demand := identity.DemandedManagedReads(resolutions, analysis.ManagedRefusals())
		var instances []identity.Resolution
		for _, d := range demand {
			if !d.Complete {
				continue
			}
			for _, inst := range d.Instances {
				if _, already := live[inst.Addr.String()]; !already {
					instances = append(instances, inst)
				}
			}
		}
		instances = expandFormulaParents(resolutions, instances)
		if len(instances) == 0 {
			// Nothing new demanded that a prior pass has not already read;
			// see [projection.ReadInstances]'s own completeness rule for
			// why a block already fully read never re-appears here.
			break
		}

		read, readDiags := projection.ReadInstances(ctx, config, instances, provs, readOpts)
		for _, d := range readDiags {
			log.Printf("[TRACE] live: reading managed values for provider-configuration data reads (pass %d): %s", pass, d.Description().Summary)
		}
		if read == nil || len(read.Values) == 0 {
			break
		}
		gained := false
		for k, v := range read.Values {
			if _, already := live[k]; !already {
				live[k] = v
				gained = true
			}
		}
		if !gained {
			// Every value this pass could name was already known; another
			// pass would ask the identical question and get the identical
			// answer.
			break
		}

		opts.LiveManagedResults = live
		nextAnalysis := dataread.AnalyzeProviderConfigs(ctx, config, opts)
		nextResults, nextDiags := dataread.ReadProviderConfigs(ctx, config, nextAnalysis, confined(nextAnalysis))
		for _, d := range nextDiags {
			log.Printf("[TRACE] live: provider-configuration data reads, pass %d: %s", pass+1, d.Description().Summary)
		}
		if len(nextResults) < len(results) {
			// This pass answered fewer sources than the pass before it
			// somehow; never regress on the strength of a later attempt.
			// See [statelessResolve]'s own errorCount comparison for the
			// same rule applied to its own passes.
			break
		}
		analysis, results = nextAnalysis, nextResults
	}
	return results
}

// expandFormulaParents adds every [identity.ClassParentDerived] instance's
// formula parents, transitively, to instances - the closure
// [projection.ReadInstances] needs already in hand before it runs, since it
// materializes strictly from what it is given and never looks a parent up
// itself (see its own doc comment: "only the caller knows the block's real
// expansion"). [identity.DemandedManagedReads] names only the instance a
// refused reference directly named, never the parents ITS OWN identity
// formula depends on, so without this a demanded PARENT_DERIVED instance's
// formula has nothing in [projection.ReadInstances]'s b.live to render
// against and is silently omitted - the corpus-eks-basic shape this exists
// for, aws_eks_cluster.this[0] needing random_string.suffix's own value
// read alongside it, in the same call, before its own live attributes can
// be read at all.
//
// Parents are looked up in resolutions - already fully resolved, no I/O -
// so this costs nothing beyond a few map reads even when every instance is
// [identity.ClassConcrete] and the loop below never executes. A visited set
// keyed by address makes this safe against a formula that names the same
// parent twice, or - should identity resolution ever admit one - a cycle;
// [projection.ReadInstances]'s own orderWork raises a proper diagnostic for
// an actual cycle in whatever this hands it.
func expandFormulaParents(resolutions *identity.Result, instances []identity.Resolution) []identity.Resolution {
	seen := make(map[string]bool, len(instances))
	out := make([]identity.Resolution, 0, len(instances))
	var add func(r identity.Resolution)
	add = func(r identity.Resolution) {
		key := r.Addr.String()
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, r)
		if r.Class != identity.ClassParentDerived || r.Formula == nil {
			return
		}
		for _, p := range r.Formula.Parents {
			parentRes, ok := resolutions.Get(p)
			if !ok {
				continue
			}
			add(parentRes)
		}
	}
	for _, r := range instances {
		add(r)
	}
	return out
}

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

// statelessProviders implements [projection.Providers] over the ordinary
// plugin library, which is the same library the plan afterwards uses: real
// plugin executables from the providercache that "choudoufu init" populated, or
// whatever the test overrides supplied.
//
// The projection builder needs providers that are already configured, because
// it calls ImportResourceState and ReadResource on them. Configuring a
// provider means evaluating its configuration block, which this type does
// through the module's static evaluator: constants, variables, locals and
// functions all work, and anything that would need a full evaluation context
// (a reference to a resource or a data source in a provider block) fails with
// the evaluator's own diagnostic. That is the v0 line, and it is where the
// estate fixture sits: its provider block carries only literal flags, with
// region, credentials and endpoint reaching the plugin through the process
// environment, which the plugin inherits.
//
// One plugin process is started per provider configuration, on demand, and
// they are closed as soon as the projection is built. That is a second set of
// provider processes for a run that will also start the plan's own set; a
// single set would mean handing configured instances to tofu.Context, which
// has no seam for that today.
type statelessProviders struct {
	config *configs.Config
	mgr    plugins.ProviderManager

	mu    sync.Mutex
	cache map[string]providers.Interface

	// configVals holds the value each provider configuration was configured
	// with, so that discovery can ask what region a list should go to
	// without evaluating the block a second time.
	configVals map[string]cty.Value

	// providerDataResults is GitHub issue #313's provider-configuration
	// dependency-order fixpoint's own answer: the data-source values a
	// provider block's own arguments need, read by
	// [statelessProviderDataReads] once resolution has settled, keyed by
	// absolute instance address exactly as [dataread.Read]'s own result is.
	// providerConfigValue consults it the same way [liveModuleEvaluator]
	// consults [dataread]'s own results for a data source's own argument.
	//
	// Nil until [statelessProviderDataReads] runs (every provider
	// configuration behaves exactly as before until then), and nil is also
	// what most estates keep it at: a provider block whose arguments are
	// var/local/literal - the overwhelming common case - never has an
	// entry here to consult, so this field costs nothing when it is not
	// needed.
	providerDataResults map[string]cty.Value
}

var _ projection.Providers = (*statelessProviders)(nil)

// providerCacheKey is what a configured provider instance is cached and
// looked up under: the provider, its alias, AND the module
// [providerscope.Resolve] settled on - addr.Module.String(), empty for
// root. GitHub issue #201: before it, [providerscope.Resolve] returned
// addrs.RootModule on every path, so the module dimension was redundant
// (always "") and omitting it changed nothing; now that Resolve can
// terminate at a non-root module's own local provider block (see its own
// doc comment), two AbsProviderConfig values with the same provider and
// alias but different modules are genuinely different configurations - a
// root default aws config and module.compute's own local aws block both key
// as (aws, "") without the module dimension, and the second lookup would
// silently reuse the first's already-configured instance: whichever
// resource asked first would decide the account and region for every
// resource in both modules, no diagnostic, no cache miss. Two distinct
// provider scopes cannot collide here because addrs.Module.String() is
// exactly the dotted "module.a.module.b" path [providerscope.Resolve]
// returned, unique per module node, and this key changes for root
// (addr.Module.String() == "") only in that it now carries one more
// trailing separator - every existing root-only cache entry still lands on
// a distinct key from any non-root one, never a different key from its own
// previous root-only self.
func providerCacheKey(addr addrs.AbsProviderConfig) string {
	return addr.Provider.String() + "\x00" + addr.Alias + "\x00" + addr.Module.String()
}

func newStatelessProviders(config *configs.Config, lib plugins.Library) *statelessProviders {
	return &statelessProviders{
		config:     config,
		mgr:        lib.NewProviderManager(),
		cache:      make(map[string]providers.Interface),
		configVals: make(map[string]cty.Value),
	}
}

// resourceSchemas is every managed resource type schema the providers this
// configuration requires serve, merged, for identity resolution's schema
// fallback ([identity.Context.Schemas]).
//
// It reads them from unconfigured plugins, which is the only reason it can
// run this early: GetProviderSchema needs a plugin process, not a configured
// one, and the manager memoizes the answer, so the projection's own schema
// read a few steps later is the same one rather than a second launch.
// Resolution itself stays what it has always been - no provider, no cloud,
// nothing evaluated against a running plugin - and is handed a map.
//
// Every failure here is silent, and the result may be partial or empty. That
// is the fallback's contract: absent schemas mean the hand table is all this
// run knows, which is what every run did before the fallback existed. A
// provider that will not start is reported properly a few steps later, with
// the resource it was reading named, rather than as an aside about schemas.
//
// A type name two providers both serve is dropped rather than resolved by
// order. Resolution is handed one map with no provider attached to each
// entry, so it has nothing to choose with, and a schema from the wrong
// provider would describe some other cloud's idea of the same name.
func (p *statelessProviders) resourceSchemas(ctx context.Context) map[string]providers.Schema {
	out := make(map[string]providers.Schema)
	ambiguous := make(map[string]bool)

	for _, addr := range p.config.ProviderTypes() {
		schema, diags := p.mgr.GetProviderSchema(ctx, addr)
		if diags.HasErrors() {
			log.Printf("[TRACE] live: no schemas from %s for identity resolution: %s", addr, diags.Err())
			continue
		}
		for name, resourceSchema := range schema.ResourceTypes {
			if _, seen := out[name]; seen {
				ambiguous[name] = true
				continue
			}
			out[name] = resourceSchema
		}
	}
	for name := range ambiguous {
		delete(out, name)
	}
	return out
}

// managedTypesByProvider is which managed resource types each provider this
// configuration requires actually declares, attributed to the provider that
// declares it - which is the one thing [statelessProviders.resourceSchemas]'
// merged map cannot say, since it drops the provider and drops a name two
// providers both serve.
//
// It feeds [dataread.LiveProviders]' third half. That boundary asks "does
// this estate manage live objects through this provider", answers it from
// the configuration's own managed resource blocks, and resolves each block to
// a provider through [configs.Module.ProviderForLocalConfig] - which returns
// whatever source address a `required_providers` entry bound the local name
// to, checking nothing. Without this map, a configuration could bind the
// local name "aws" to hashicorp/external, declare an aws_ resource under it,
// and vote the local-execution provider into the set on the strength of a
// type it does not serve.
//
// It costs nothing beyond what resourceSchemas already spent: the same
// GetProviderSchema calls, memoized by the provider manager.
//
// A provider whose schema will not load is ABSENT from the result rather than
// present and empty, and [dataread.LiveProviders] reads that as "no evidence
// either way" and skips the cross-check for it. Empty would read as "declares
// nothing", which would silently un-live every provider whose plugin failed
// to start and turn a plugin problem into a wall of data-read refusals.
func (p *statelessProviders) managedTypesByProvider(ctx context.Context) map[addrs.Provider]map[string]bool {
	out := make(map[addrs.Provider]map[string]bool)
	for _, addr := range p.config.ProviderTypes() {
		schema, diags := p.mgr.GetProviderSchema(ctx, addr)
		if diags.HasErrors() {
			log.Printf("[TRACE] live: no schemas from %s for the data-read provider boundary: %s", addr, diags.Err())
			continue
		}
		types := make(map[string]bool, len(schema.ResourceTypes))
		for name := range schema.ResourceTypes {
			types[name] = true
		}
		out[addr] = types
	}
	return out
}

// region is the region a list call for one provider configuration should go
// to: whatever the provider block sets, or the region the environment
// supplies, which is how the estate fixture configures itself. An empty
// answer leaves the list configuration's region unset, and the provider's
// own resolution stands.
func (p *statelessProviders) region(addr addrs.AbsProviderConfig) string {
	p.mu.Lock()
	val, ok := p.configVals[providerCacheKey(addr)]
	p.mu.Unlock()

	// ContainsMarked, not IsMarked, and before anything reads inside the
	// value: the mark lands on the ATTRIBUTE rather than on the object the
	// provider block decodes to, and AsString below panics rather than errors
	// on a marked receiver.
	//
	// The value cached here comes from StaticEvaluator.DecodeBlock over the
	// provider block (see providerConfigValue), and DecodeBlock has no guard
	// refusing a sensitive value the way DecodeExpression does - so
	// `provider "aws" { region = var.region }` for a `sensitive = true`
	// region arrives marked, and this used to panic the whole command. The
	// RPC leg of the same value is already safe: internal/plugins/provider.go
	// unmarks before ConfigureProvider. This is the non-RPC leg of the same
	// defect, and it sits in the one tree internal/live/marksafe does not
	// scan.
	//
	// Refused rather than unmarked, unlike the seams that put a value to a
	// provider: this answer becomes an operator-facing hint string, and a
	// secret does not belong in one. Falling through to the environment is
	// what an unset region already does.
	if ok && val != cty.NilVal && !val.ContainsMarked() && !val.IsNull() && val.Type().IsObjectType() && val.Type().HasAttribute("region") {
		region := val.GetAttr("region")
		if !region.IsNull() && region.IsKnown() && region.Type() == cty.String && region.AsString() != "" {
			return region.AsString()
		}
	}
	for _, name := range []string{"AWS_REGION", "AWS_DEFAULT_REGION"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// endpointURL is the custom endpoint one provider configuration reaches EC2
// through, for the adoption hint's --endpoint-url flag: whatever the provider
// block's endpoints block sets for ec2, or the endpoint the environment
// supplies, which is how the estate fixture reaches its emulator. An empty
// answer means no custom endpoint is in play and the flag is left off.
func (p *statelessProviders) endpointURL(addr addrs.AbsProviderConfig) string {
	p.mu.Lock()
	val, ok := p.configVals[addr.String()]
	p.mu.Unlock()

	// ContainsMarked for the reason [statelessProviders.region] states above,
	// and for one more: IsWhollyKnown and ElementIterator just below both
	// iterate, and both panic on a marked receiver. Unreachable today only
	// because this lookup misses the cache - it reads addr.String() while
	// ConfiguredProvider writes providerCacheKey(addr), so the endpoint hint
	// always falls through to the environment. That key mismatch is a real
	// defect and is deliberately NOT fixed here, because fixing it is what
	// would make this branch live; the guard goes in first so that whoever
	// fixes the key does not inherit a panic with it.
	if ok && val != cty.NilVal && !val.ContainsMarked() && !val.IsNull() && val.Type().IsObjectType() && val.Type().HasAttribute("endpoints") {
		endpoints := val.GetAttr("endpoints")
		if !endpoints.IsNull() && endpoints.IsWhollyKnown() && endpoints.CanIterateElements() {
			for it := endpoints.ElementIterator(); it.Next(); {
				_, ep := it.Element()
				if ep.IsNull() || !ep.Type().IsObjectType() || !ep.Type().HasAttribute("ec2") {
					continue
				}
				ec2 := ep.GetAttr("ec2")
				if !ec2.IsNull() && ec2.Type() == cty.String && ec2.AsString() != "" {
					return ec2.AsString()
				}
			}
		}
	}
	// The AWS CLI resolves the service-specific variable ahead of the
	// general one, so the hint is read from them in the same order.
	for _, name := range []string{"AWS_ENDPOINT_URL_EC2", "AWS_ENDPOINT_URL"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}

// ConfiguredProvider implements [projection.Providers].
func (p *statelessProviders) ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// The cache and every lookup below key on addr.Module too, not just the
	// provider and its alias (see [providerCacheKey]'s own comment for why:
	// GitHub issue #201). The overwhelmingly common case is still one
	// configured instance per provider+alias regardless of which module a
	// resource sits in: a static module call with no provider block of its
	// own inherits the root's default (unaliased) configuration for its
	// implied provider, and [providerscope.Resolve] returns
	// addrs.RootModule for every one of those, so they all still land on
	// the same key. The exception is a module that declares its own
	// content-bearing provider block, admitted now that
	// [internal/live/lint.checkModuleProviderBlocks] refuses it only when
	// the call chain reaching it uses count, for_each, enabled or
	// depends_on: Resolve returns that module's own path for those, and
	// [statelessProviders.providerConfigValue] below reads addr.Module's
	// own provider block rather than the root's unconditionally.
	key := providerCacheKey(addr)
	if provider, ok := p.cache[key]; ok {
		return provider, nil
	}

	schema, schemaDiags := p.mgr.GetProviderSchema(ctx, addr.Provider)
	if schemaDiags.HasErrors() {
		return nil, fmt.Errorf("cannot read the schema of provider %s: %w", addr.Provider, schemaDiags.Err())
	}
	// A nil Provider.Block means the provider declares no provider-level
	// configuration schema at all - the builtin "terraform" provider
	// (terraform_data's provider, admitted by GitHub issue #73's
	// record-backed types) is the one this fork has ever needed to
	// configure that shape, since every other provider a live estate has
	// used until now was AWS. An empty block is the correct
	// nothing-to-configure substitute: decoding an empty provider body
	// against it produces the same all-null/empty value an ordinary
	// provider with zero configuration arguments would get from
	// providerConfigValue below, rather than treating "nothing to
	// configure" as a fatal error.
	block := schema.Provider.Block
	if block == nil {
		block = &configschema.Block{}
	}

	configVal, cfgDiags := p.providerConfigValue(ctx, addr, block.DecoderSpec())
	if cfgDiags.HasErrors() {
		// A typed error, not just a wrapped one: [projection.
		// ProviderConfigNotEvaluable]'s own doc comment is the caller-side
		// half of this - it is what lets statelessDiscoverProviderUnavailable
		// and internal/live/projection's materialize family tell "this
		// provider's own block depends on a value nothing has yet" apart
		// from a genuinely broken plugin or missing credentials (the two
		// error paths below, both left as plain errors on purpose).
		return nil, &projection.ProviderConfigNotEvaluable{Provider: addr, Err: cfgDiags.Err()}
	}

	provider, diags := p.mgr.NewConfiguredProvider(ctx, addr.Provider, configVal)
	if diags.HasErrors() {
		return nil, fmt.Errorf("cannot configure provider %s: %w", addr, diags.Err())
	}

	p.cache[key] = provider
	p.configVals[key] = configVal
	return provider, nil
}

// providerConfigValue evaluates the provider block for the given address, or
// - for the default (unaliased) configuration only - produces the all-null
// value that an absent provider block implies, which is how a provider that
// takes everything from the environment is configured.
//
// addr.Module names which module's own provider blocks to search - almost
// always addrs.RootModule, since [providerscope.Resolve] only ever returns a
// non-root module when that module declares its own content-bearing
// provider block (GitHub issue #201; [checkModuleProviderBlocks] in
// internal/live/lint is what admits that shape, and only when no call in
// the chain reaching it uses count, for_each, enabled or depends_on). The
// evaluator used below is addr.Module's own, not the root's: a non-root
// block's expressions - simpleinfra/terraform/shared/modules/gha-iam-user's
// `provider "github" { owner = var.org }` is the corpus site that drove
// this - reference that module's own variables, which only its own
// configs.Module.StaticEvaluator has bound to the values its caller passed,
// not the root's.
//
// An ALIASED address with no matching provider block in that module is an
// error, never an empty body. GitHub issue #123: the fallback used to cover
// that miss too, so a root resource with provider = aws.nope - which stock
// OpenTofu's graph refuses outright - had its provider configured from the
// environment alone, silently, and discovery read the live system through
// it. lint.RuleUndeclaredProviderAlias refuses the root-resource route
// before any cloud read and checkModuleProviderMapping the module-mapping
// one, so this diagnostic is the backstop for any address those rules did
// not see, not a message a user is expected to reach.
func (p *statelessProviders) providerConfigValue(ctx context.Context, addr addrs.AbsProviderConfig, spec hcldec.Spec) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	cfg := p.config
	moduleText := "the root module"
	if !addr.Module.IsRoot() {
		moduleText = addr.Module.String()
		if c := p.config.Descendent(addr.Module); c != nil && c.Module != nil {
			cfg = c
		}
		// A miss here means addr names a module this configuration tree
		// does not have - [providerscope.Resolve] was given a *configs.Config
		// from some other tree, or the tree changed between resolution and
		// this call. Falling back to cfg = p.config (root) rather than
		// returning early keeps the found/not-found logic below as the one
		// place that decides between a real block, the environment
		// fallback, and the "not declared" diagnostic, instead of adding a
		// second, differently-worded error path for what is already an
		// internal inconsistency the diagnostic below still reports
		// correctly (as "not declared in <module>", just checked against
		// the wrong module's blocks).
	}
	mod := cfg.Module

	// Find the provider block for this address by resolving each block's
	// own local name to a provider FQN, not by round-tripping the FQN
	// through LocalNameForProvider: when required_providers gives one
	// provider two local names, ProviderLocalNames holds one winner chosen
	// by Go map order, and the first version of this lookup refused a
	// configuration stock terraform accepts - at random, one parse in a
	// few - claiming a block that exists under the other name was not
	// declared. Keys are scanned in sorted order so two blocks that both
	// resolve here pick the same one every run.
	keys := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var found *configs.Provider
	for _, k := range keys {
		pc := mod.ProviderConfigs[k]
		if pc.Alias != addr.Alias {
			continue
		}
		if mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: pc.Name}) != addr.Provider {
			continue
		}
		found = pc
		break
	}

	displayName := mod.LocalNameForProvider(addr.Provider)
	if addr.Alias != "" {
		displayName = displayName + "." + addr.Alias
	}

	body := hcl.EmptyBody()
	ident := configs.StaticIdentifier{
		Module:  addr.Module,
		Subject: fmt.Sprintf("provider.%s", displayName),
	}
	switch {
	case found != nil:
		body = found.Config
		ident.DeclRange = found.DeclRange
		subject := found.Name
		if found.Alias != "" {
			subject = subject + "." + found.Alias
		}
		ident.Subject = fmt.Sprintf("provider.%s", subject)
	case addr.Alias != "":
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Provider configuration is not declared",
			fmt.Sprintf(
				"This run needs the provider configuration %q, and %s declares no such provider block. "+
					"Configuring it from the environment instead would read and write the live system against whatever "+
					"account and region the environment names, which is not what the configuration says. Declare "+
					"provider %q with that alias, or remove the reference to it.",
				displayName, moduleText, mod.LocalNameForProvider(addr.Provider),
			),
		))
		return cty.NilVal, diags
	}

	// GitHub issue #313: a data-resource reference this provider block's own
	// arguments make - `host = data.aws_eks_cluster.cluster.endpoint` - is
	// answered from [statelessProviderDataReads]'s own read, exactly the
	// seam [liveModuleEvaluator] already gives a data source's own
	// arguments in internal/live/dataread. addr.Module is the block's own
	// declaring module (see this function's doc comment on which module's
	// blocks apply), matching [identity.DataLookupFor]'s own "provider
	// blocks are almost always root, and #201 already restricts a non-root
	// one to a shape with no repeated call chain" assumption. Nil
	// providerDataResults - every estate before this fixpoint existed, and
	// every provider block whose arguments name no data source - leaves
	// this evaluator byte-identical to the bare one.
	eval := mod.StaticEvaluator
	if lookup, _ := identity.DataLookupFor(p.providerDataResults, addr.Module); lookup != nil {
		eval = eval.WithDataResults(lookup)
	}

	val, hclDiags := eval.DecodeBlock(ctx, body, spec, ident)
	diags = diags.Append(hclDiags)
	return val, diags
}

// close shuts down every plugin process this type started.
func (p *statelessProviders) close(ctx context.Context) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if err := p.mgr.Shutdown(ctx); err != nil {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Failed to shut down a provider plugin",
			fmt.Sprintf("The providers used to read the live system could not all be shut down cleanly: %s.", err),
		))
	}
	return diags
}

func (c *LivePlanCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] live-plan [options]

  EXPERIMENTAL. Plans against the live system rather than a stored record.
  Prior state is a projection, built by reading the live system at the start
  of the run and discarded at the end.

  A terraform.tfstate in the working directory is never read and never
  written. The working directory does still need "choudoufu init" to have
  installed the providers.

  Above the plan, this command prints the resource instances it could not
  read from the live system, with a reason for each. Those instances are
  missing from the prior state, which is why the plan proposes to create
  them.

  A live resource found at an identity this configuration declares, carrying
  no ownership marker for this estate, gets its own Unowned section as well
  as its [UNOWNED] omission: each entry says whether a deliberate tag write
  adopts it, with the exact tofu-estate and tofu-address values to write, or
  whether it belongs elsewhere and is simply in the way of the create the
  plan proposes.

  Every taggable resource that does not already declare the ownership markers
  from live/MARKERS.md gets them injected into its planned tags, so the
  plan shows them being added rather than a later run finding the resource
  unowned. A marker already there and naming another estate or another address
  is an error: renaming is "choudoufu live-mv", not a side effect of planning.

  Below that it prints the live resources this estate does not own: resources
  carrying no ownership marker, the ones that exactly match a resource this
  configuration declares and could be adopted, and which resource types were
  swept at all. Nothing unowned is ever part of the plan, and no plan this
  command produces can destroy any of it. The types a provider version simply
  cannot list or tag are a standing fact rather than run-specific news, so
  "Not swept for removal" names only the count by default; pass -verbose for
  every type by name.

  The same section pairs a changed for_each key with the live resource still
  marked with the old one: the plan proposes creating the new key either way,
  and beside it you get the "choudoufu live-mv" command that rewrites the
  marker instead. Nothing is renamed for you - a pairing this run cannot make
  one-to-one is reported as ambiguous, with no command.

  During a migration the only question is which live resources this estate
  can claim, and the sections above answer it in pieces inside a report that
  is mostly about other things. Pass -adoption-only for that question alone:
  one ledger splitting every declared instance into the half whose identity
  comes from an ownership marker and the half whose identity comes from its
  own declaration, then a section per thing you can act on. On a real estate
  the declaration half is usually the larger of the two, and it needs nothing
  done to it.

Options:

  -adoption-only          Print only the adoption ledger: what can be
                          adopted, what cannot, and why. The resource diff
                          and the sections above are suppressed, and each
                          warning is compacted to one line naming it, with a
                          pointer to this same command without the flag.
                          Errors are never touched. The plan itself is
                          unchanged and every verdict in it is the one an
                          ordinary run would have printed. What does change
                          is what the run goes and looks at: this asks the
                          estate-wide sweep which live resources carry no
                          ownership marker at all, a question bounded by the
                          account rather than by the estate, so it costs more
                          than an ordinary plan rather than less. Set
                          TOFU_LIVE_COLLECT_UNCLAIMED=1 to ask it on an
                          ordinary plan, or 0 to skip it here.

  -estate=name            The estate whose ownership markers this run looks
                          for, matching the tofu-estate tag grammar in
                          live/MARKERS.md. Defaults to the value the
                          configuration itself stamps in its tofu-estate
                          tags, when every resource that stamps one agrees.
                          Without either, resources whose identity is
                          assigned by the provider are not looked for, the
                          plan proposes creating them, and no markers are
                          stamped.

  -target=resource        Limit the planning operation to only the given
                          module, resource, or resource instance and all of
                          its dependencies. You can use this option multiple
                          times to include more than one object.

  -target-file=filename   Similar to -target, but specifies zero or more
                          resource addresses from a file.

  -exclude=resource       Limit the planning operation to not operate on the
                          given module, resource, or resource instance and all
                          of the resources and modules that depend on it.

  -exclude-file=filename  Similar to -exclude, but specifies zero or more
                          resource addresses from a file.

  -replace=resource       Force replacement of a particular resource instance
                          using its resource address.

  -var 'foo=bar'          Set a value for one of the input variables in the
                          root module of the configuration. Use this option
                          more than once to set more than one variable.

  -var-file=filename      Load variable values from the given file, in
                          addition to the default files terraform.tfvars and
                          *.auto.tfvars.

  -detailed-exitcode      Return detailed exit codes when the command exits.
                          This will change the meaning of exit codes to:
                          0 - Succeeded, diff is empty (no changes)
                          1 - Errored
                          2 - Succeeded, there is a diff

  -compact-warnings       Show warnings in a more compact form.

  -input=true             Ask for input for variables if not directly set.

  -no-color               If specified, output won't contain any color.

  -verbose                Print every type the removal sweep could not cover
                          by name, rather than the default one-line count.
                          See "Not swept for removal" above the plan.

  -parallelism=n          Limit the number of concurrent operations as the
                          plan graph is walked, exactly as it does for a
                          stock plan. Defaults to 10. It does not bound the
                          marker sweep, which runs before there is a graph;
                          that is TOFU_LIVE_SWEEP_PARALLELISM below. Nor does
                          it bound the read pass that builds prior state,
                          which also runs before there is a graph; that is
                          TOFU_LIVE_READ_PARALLELISM below.

  The following stock plan options are rejected rather than ignored, because
  live resource markers remove what they operate on or have not built them yet:
  -out, -state, -state-out, -backup, -refresh-only, -generate-config-out,
  -json and -json-into. That list is the same one plain "choudoufu plan" and
  "choudoufu apply" use under a live block; there is one list, not one per
  command. -refresh is accepted and has no effect: the projection is already
  fresh, so the plan never refreshes.

  -destroy is the single option the two entry points answer differently, and
  only this command's -estate form refuses it. Under a live block, "choudoufu
  plan -destroy" and "choudoufu destroy" run this same pipeline in destroy
  mode. The -estate form builds its plan by calling the planner directly in
  the normal planning mode, so accepting -destroy here would hand you a normal
  plan labelled as a destroy.

Environment variables:

  TOFU_LIVE_SWEEP_PARALLELISM=n
                          How many of the estate-wide marker sweep's list
                          calls run at once. Defaults to 10. Turn it down if
                          a real account throttles the sweep: 1 makes it
                          sequential, one list call at a time. A value below
                          1 is refused rather than read as "no limit". It is
                          a variable rather than a flag because the same
                          pipeline runs under plain "choudoufu plan" and
                          "choudoufu apply" whenever the configuration has a
                          live block, and one name has to reach all three.

  TOFU_LIVE_READ_PARALLELISM=n
                          How many of the read pass's per-instance provider
                          round trips run at once. Defaults to 10. This is
                          the phase between the sweep and the graph: one
                          import and one read per managed instance, which are
                          the same calls a stock refresh of this estate would
                          make, at the same 10. Turn it down if a real
                          account throttles those reads: 1 makes the pass
                          sequential, one instance at a time in loop order. A
                          value below 1 is refused rather than read as "no
                          limit". A variable rather than a flag for the same
                          reason as the sweep's.

  The three bounds are separate on purpose: -parallelism is the graph walk,
  TOFU_LIVE_SWEEP_PARALLELISM is the marker sweep, TOFU_LIVE_READ_PARALLELISM
  is the read pass that builds prior state. Setting one does not move the
  others.
`
	return strings.TrimSpace(helpText)
}

func (c *LivePlanCommand) Synopsis() string {
	return "Show changes required by the configuration, read from the live system (experimental)"
}
