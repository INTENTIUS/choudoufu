// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
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
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/foreign"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// LivePlanCommand plans a configuration with no authoritative state: no
// backend, no state file, no lock. Prior state is a projection, rebuilt by
// reading the live system at the start of the run and discarded when the run
// ends.
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
	// exactly as they arrived so that it can parse them itself.
	originalArgs := rawArgs

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

	if moreDiags := livePlanRejectUnsupported(args.Plan); moreDiags.HasErrors() {
		// Rendered through the base view rather than the plan view: one of
		// the things rejected here is -json, and reporting "no JSON output"
		// as JSON would be a strange way to say it.
		c.View.Diagnostics(moreDiags)
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

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())
	diags = diags.Append(c.liveStateFileNote())
	diags = diags.Append(c.checkAWSProviderVersionSkew())

	statelessView := views.NewStatelessPlan(c.View)

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
	if issues := lint.CheckWith(ctx, config, lint.Context{Schemas: resourceSchemas}); len(issues) > 0 {
		diags = diags.Append(lint.Diagnostics(issues))
		diags = diags.Append(provs.close(ctx))
		return 1, false, diags
	}
	// Resolution runs ahead of the providers being configured, as it always
	// has, and is handed their schemas: a resource type the hand table has
	// never heard of resolves anyway when the provider's own identity schema
	// describes it completely enough. See [identity.SynthesizeTypeIdentity].
	// A run whose providers will not start gets no schemas and the hand
	// table's answers, which is exactly what it got before.
	resolutions, idDiags := identity.ResolveWith(ctx, config, identity.Context{
		Schemas: resourceSchemas,
	})
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
	// has to know the estate before anything is materialized.
	estate, _, _ := statelessEstateFor(ctx, estateFlag, config)

	// Resolved now that lint has passed and the estate name is settled, so
	// that any verb here is already known valid for its quadrant (see
	// internal/live/lint's checkLivePolicy).
	var pol *policy.Policy
	if config.Module != nil {
		pol = statelessPolicy(config.Module.Live, estate)
		log.Printf("[TRACE] live: ownership policy: %s", pol)
	}

	// The estate's record store, when the live block names one - opened
	// here only as guided discovery's hint source (issue #109). A store
	// that will not open is not this command's error to fail on: the hint
	// is a plan-cost cache, so the run proceeds hintless (guided discovery
	// stays off) and the projection below behaves exactly as it always has.
	var hintStore staterecord.Store
	if config.Module != nil && config.Module.Live != nil && config.Module.Live.RecordStore != nil {
		store, storeErr := projection.NewRecordStore(ctx, config.Module.Live.RecordStore, estate, ".")
		if storeErr != nil {
			log.Printf("[WARN] live: could not open the record store for guided discovery's hint: %s", storeErr)
		} else {
			hintStore = store
		}
	}

	// Marker discovery, when anything is waiting on it. Its output is a
	// resolution list with the discovered instances made concrete, plus the
	// unclaimed live resources the classifier below sorts out.
	merged := resolutions.All()
	disco, discoProvider, undeclaredProviders, discoDiags := statelessDiscover(ctx, config, resolutions, estateFlag, provs, pol, hintStore, statelessView)
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
	})
	// The provider processes started for the projection have done their job
	// by this point; the plan below starts its own from the same library.
	diags = diags.Append(provs.close(ctx))
	diags = diags.Append(projDiags)
	if projDiags.HasErrors() {
		return 1, false, diags
	}

	statelessView.Omissions(statelessOmissions(projResult))
	statelessView.Unowned(statelessUnownedReport(projResult, estate))

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
		statelessView.Foreign(statelessForeignReport(classified))
		statelessView.GuidedFallback(disco.GuidedFallback)
	}

	rawVariables, varDiags := c.collectVariableValues()
	diags = diags.Append(varDiags)
	variables, parseDiags := backend.ParseVariableValues(rawVariables, config.Module.Variables)
	diags = diags.Append(parseDiags)
	if diags.HasErrors() {
		return 1, false, diags
	}

	tfCtx, ctxDiags := tofu.NewContext(coreOpts)
	diags = diags.Append(ctxDiags)
	if ctxDiags.HasErrors() {
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
	stampRes, stampDiags := statelessStamp(ctx, config, estateFlag, schemas, disco.SlotTable(), statelessNeedsDiscovery(resolutions), policyUntag)
	diags = diags.Append(stampDiags)
	if stampDiags.HasErrors() {
		return 1, false, diags
	}

	statelessView.Policy(statelessPolicyReport(projResult, disco, stampRes, reconcile))

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
// Provider selection is two separate questions, and issue #69 is about
// keeping them separate rather than answering both with the same rule. The
// needs-discovery scan - the resolutions actually waiting to be found by
// marker - has to go through one provider configuration, unconditionally:
// see [statelessNeedsDiscoveryProvider]'s own doc comment for why a list
// against the wrong account or region would misreport an estate as missing
// rather than as merely unreachable. The estate-wide sweep has no such
// hazard - a sweep against the wrong account only ever narrows what a run
// can see, it never mis-reports what exists as absent - so when the
// configuration's managed resources span more than one provider
// configuration, the sweep runs once per one of them
// ([statelessManagedResourceProviders]) and [discovery.Merge] combines the
// results. A single-provider configuration - true of every estate before
// this existed - takes the old direct path with no merge step at all, which
// is what keeps that case byte-identical.
//
// The second return value is the "primary" provider configuration: the one
// the needs-discovery scan used, or - when nothing needed discovery - the
// first of the sweep's providers in sorted order. It is what a caller uses
// for the one thing this pass cannot honestly split by provider without a
// larger refactor of internal/live/foreign: the adoption hint's --region
// and --endpoint-url flags. In a multi-provider estate that hint may name
// the wrong region for a foreign resource found under a different provider
// configuration - a known, documented v0 simplification, not a silent wrong
// answer, and the third return value is what a caller uses instead for
// materializing undeclared instances correctly, per-address, regardless of
// which provider found them.
func statelessDiscover(ctx context.Context, config *configs.Config, resolutions *identity.Result, estateFlag string, provs *statelessProviders, pol *policy.Policy, hintStore staterecord.Store, statelessView views.StatelessPlan) (*discovery.Result, addrs.AbsProviderConfig, map[string]addrs.AbsProviderConfig, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var noProvider addrs.AbsProviderConfig

	needs := resolutions.NeedsDiscovery()

	estate, estateDiags := statelessEstateName(ctx, estateFlag, config, needs)
	diags = diags.Append(estateDiags)
	if estate == "" {
		return nil, noProvider, nil, diags
	}

	needsProvider, needsDiags := statelessNeedsDiscoveryProvider(config, needs)
	diags = diags.Append(needsDiags)
	if needsDiags.HasErrors() {
		return nil, noProvider, nil, diags
	}

	sweepProviders := statelessManagedResourceProviders(config)
	if len(sweepProviders) == 0 {
		// No managed resources at all: nothing to find, nothing that could
		// be undeclared.
		return nil, noProvider, nil, diags
	}

	if len(sweepProviders) == 1 {
		providerAddr := sweepProviders[0]
		// No ScopeProvider: the single-provider path is the exact call
		// every caller made before issue #69 existed.
		res, discoDiags := statelessDiscoverOne(ctx, config, resolutions.All(), estate, providerAddr, addrs.AbsProviderConfig{}, provs, pol, hintStore, statelessView)
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
	passes := make([]discovery.Pass, 0, len(sweepProviders))
	for _, providerAddr := range sweepProviders {
		res, discoDiags := statelessDiscoverOne(ctx, config, resolutions.All(), estate, providerAddr, providerAddr, provs, pol, hintStore, statelessView)
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

	merged, providerOf, mergeDiags := discovery.Merge(estate, passes)
	diags = diags.Append(mergeDiags)
	if mergeDiags.HasErrors() {
		return merged, noProvider, providerOf, diags
	}

	primary := needsProvider
	if primary.Provider.Type == "" {
		primary = sweepProviders[0]
	}
	return merged, primary, providerOf, diags
}

// statelessDiscoverOne runs [discovery.Discover] through one provider
// configuration: the shared body of both statelessDiscover's single-provider
// path and each iteration of its multi-provider loop. scopeProvider is
// [discovery.Request.ScopeProvider]; its zero value means unscoped, which is
// what the single-provider path passes.
func statelessDiscoverOne(ctx context.Context, config *configs.Config, resolutions []identity.Resolution, estate string, providerAddr, scopeProvider addrs.AbsProviderConfig, provs *statelessProviders, pol *policy.Policy, hintStore staterecord.Store, statelessView views.StatelessPlan) (*discovery.Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	provider, err := provs.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Provider unavailable for marker discovery",
			fmt.Sprintf("Finding the live resources of this estate needs provider %s, which could not be used: %s.", providerAddr, err),
		))
	}

	req := discovery.Request{
		Estate:           estate,
		Config:           config,
		Resolutions:      resolutions,
		Provider:         provider,
		Region:           provs.region(providerAddr),
		CollectUnclaimed: true,
		Sweep:            true,
		Policy:           pol,
		ScopeProvider:    scopeProvider,
		Progress:         statelessProgress(statelessView),
	}
	statelessApplyGuidedDiscovery(config, hintStore, &req)

	res, discoDiags := discovery.Discover(ctx, req)
	diags = diags.Append(discoDiags)
	return res, diags
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
//   - the configuration's "live" block has a record_store block, the hint's
//     one carrier since issue #109 removed the observational snapshot;
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
	if config.Module.Live.RecordStore == nil || hintStore == nil {
		// No record store configured (or none could be opened): there is no
		// hint carrier, and today's full enumeration is exactly right.
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
func statelessStamp(ctx context.Context, config *configs.Config, estateFlag string, schemas *tofu.Schemas, slotTable map[string]string, needsDiscovery map[string]bool, policyUntag map[string]string) (*stamp.Result, tfdiags.Diagnostics) {
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

	res, stampDiags := stamp.Stamp(ctx, stamp.Request{
		Estate:         estate,
		Config:         config,
		Schemas:        schemas,
		Slots:          slotTable,
		NeedsDiscovery: needsDiscovery,
		PolicyUntag:    policyUntag,
	})
	diags = diags.Append(stampDiags)
	return res, diags.Append(statelessStampGaps(res, needsDiscovery))
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
func statelessStampGaps(res *stamp.Result, needsDiscovery map[string]bool) tfdiags.Diagnostics {
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
		if skip.Reason == stamp.SkipAlreadyStamped || skip.Reason == stamp.SkipModuleKeyedTrusted || !needsDiscovery[skip.Addr.String()] {
			continue
		}
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Unstamped marker-only resource",
			fmt.Sprintf(
				"%s has an identity the provider assigns at create time, so its ownership marker is the only handle any later run will have on it, and marker stamping reported %s: %s Applying it in this state would create a resource this configuration can never find again.",
				skip.Addr, skip.Reason, skip.Detail),
		))
	}
	return diags
}

// statelessNeedsDiscovery is the set of resource blocks whose instances can
// only be found by their ownership marker, keyed by module-qualified block
// address, taken from identity resolution rather than from what discovery
// managed to bind: an instance discovery found is still one that nothing but
// its marker could have found.
func statelessNeedsDiscovery(resolutions *identity.Result) map[string]bool {
	if resolutions == nil {
		return nil
	}
	needs := resolutions.NeedsDiscovery()
	out := make(map[string]bool, len(needs))
	for _, r := range needs {
		// .Config(), not .ContainingResource(): both consumers key on an
		// addrs.ConfigResource, which carries no instance keys.
		// [stamp.Request.NeedsDiscovery] documents the contract as
		// "module-qualified block address", stamp.mustStamp builds its
		// lookup from addrs.ConfigResource, and stamp.Skip.Addr is one.
		//
		// Identity resolution walks KEYED module instances, so
		// AbsResource.String() rendered module.wrapped["a"].aws_eip.app
		// while both readers looked up module.wrapped.aws_eip.app. Inside a
		// for_each'd module the two could never match, which silently
		// downgraded the must-stamp error to a warning and let a
		// server-assigned resource be created with no ownership marker on
		// it - unfindable by any later run, with every subsequent plan
		// proposing another one. live/LIMITATIONS.md documented the
		// guarantee as holding. See #111.
		out[r.Addr.ContainingResource().Config().String()] = true
	}
	return out
}

// statelessOwnership is the rule the projection admits live objects by: this
// run's estate, plus whatever marker discovery already proved ownership of.
// It is never nil, because "this run established no estate" is a verdict
// about ownership (nothing can be verified) rather than an absence of one.
func statelessOwnership(estate string, disco *discovery.Result) *projection.Ownership {
	return &projection.Ownership{
		Estate:   estate,
		Verified: disco.MarkerVerified(),
	}
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
// Only tag values that evaluate from configuration alone count. A tag built
// from another resource's attribute is not readable here, and is not treated
// as a partial answer: it is simply not one of the values, which at worst
// costs the operator an -estate flag.
func statelessEstateFromConfig(ctx context.Context, config *configs.Config) ([]string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	seen := make(map[string]bool)
	statelessEstateFromModule(ctx, config, seen)

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, diags
}

// statelessEstateFromModule is [statelessEstateFromConfig]'s recursive step:
// one module's resources, then its children in name order.
func statelessEstateFromModule(ctx context.Context, cfg *configs.Config, seen map[string]bool) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	mod := cfg.Module
	if mod.StaticEvaluator == nil {
		return
	}

	for _, name := range sortedResourceKeys(mod.ManagedResources) {
		rc := mod.ManagedResources[name]

		content, _, contentDiags := rc.Config.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "tags"}},
		})
		if contentDiags.HasErrors() {
			continue
		}
		attr, ok := content.Attributes["tags"]
		if !ok {
			continue
		}
		pairs, pairDiags := hcl.ExprMap(attr.Expr)
		if pairDiags.HasErrors() {
			// A tags argument that is not written as an object literal - a
			// merge() call, a variable - cannot be picked apart here.
			continue
		}
		for _, pair := range pairs {
			key, keyDiags := pair.Key.Value(nil)
			if keyDiags.HasErrors() || key.IsNull() || key.Type() != cty.String || key.AsString() != discovery.TagEstate {
				continue
			}
			val, valDiags := mod.StaticEvaluator.Evaluate(ctx, pair.Value, configs.StaticIdentifier{
				Module:    cfg.Path,
				Subject:   fmt.Sprintf("%s.tags", rc.Addr()),
				DeclRange: attr.Range,
			})
			if valDiags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() || val.Type() != cty.String {
				continue
			}
			if s := val.AsString(); s != "" {
				seen[s] = true
			}
		}
	}

	for _, name := range identity.SortedChildNames(cfg.Children) {
		statelessEstateFromModule(ctx, cfg.Children[name], seen)
	}
}

// statelessNeedsDiscoveryProvider is the provider configuration marker
// discovery's config-driven scan lists through: the one every needs-discovery
// resolution's resource block agrees on. A configuration whose
// needs-discovery resources span several provider configurations is refused
// rather than listed through whichever one came first, since a list against
// the wrong account or region would report an estate as missing rather than
// as merely unreachable.
//
// This is deliberately unconditional, unlike the estate-wide sweep's own
// provider selection (see [statelessManagedResourceProviders] and
// [discovery.Merge]): a wrong-account sweep only narrows what a run can see,
// but a wrong-account needs-discovery scan actively lies about whether a
// resource exists, which is why issue #69 leaves this rule exactly as it
// was rather than generalizing it too.
//
// The zero value, returned alongside no error, means nothing needs
// discovery at all - a configuration entirely of client-named resources, for
// instance - which tells the caller the sweep alone gets to pick a provider.
func statelessNeedsDiscoveryProvider(config *configs.Config, needs []identity.Resolution) (addrs.AbsProviderConfig, tfdiags.Diagnostics) {
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
		addr := providerConfigAddr(rc, r.Addr.Module.Module())
		seen[addr.String()] = addr
	}

	switch len(seen) {
	case 0:
		if len(needs) == 0 {
			return addrs.AbsProviderConfig{}, diags
		}
		return addrs.AbsProviderConfig{}, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider for marker discovery",
			"Resource instances are waiting on marker discovery, but none of them could be traced back to a resource block in the configuration. The resolutions and the configuration come from different runs; this is a bug.",
		))
	case 1:
		for _, addr := range seen {
			return addr, diags
		}
	}

	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return addrs.AbsProviderConfig{}, diags.Append(tfdiags.Sourceless(
		tfdiags.Error,
		"Marker discovery across several provider configurations",
		fmt.Sprintf(
			"The resources waiting on marker discovery use %s. Marker discovery goes through one provider configuration per run, because a list issued against the wrong account or region would report an estate as missing rather than as unreachable. Split the configuration so the resources waiting on marker discovery all use one provider configuration. -target does not help here: this check runs over the whole configuration during discovery, before any target filter applies.",
			strings.Join(names, " and ")),
	))
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
	walkManagedResources(config, func(rc *configs.Resource, modPath addrs.Module) {
		if ti, ok := identity.LookupType(rc.Type); ok && ti.RecordBacked {
			// GitHub issue #73's record-backed resources (null_resource,
			// terraform_data, time_*, non-sensitive random_*) have no
			// cloud object and no marker of any kind, so they are never a
			// candidate for the estate-wide sweep's provider set: there is
			// nothing for a sweep issued through their provider to find,
			// and no discovery.Discover call makes sense against a
			// provider with no listable, taggable resources at all.
			return
		}
		addr := providerConfigAddr(rc, modPath)
		seen[addr.String()] = addr
	})
	out := make([]addrs.AbsProviderConfig, 0, len(seen))
	for _, addr := range seen {
		out = append(out, addr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// statelessForeignReport converts the classification into the view's wire
// format. It carries data across, never rendered text: the wording of the
// section is the view's business, and this function producing sentences
// would put half the output in the wrong package.
func statelessForeignReport(res *foreign.Result) views.StatelessForeign {
	if res == nil {
		return views.StatelessForeign{}
	}

	rep := views.StatelessForeign{
		Estate:       res.Estate,
		Swept:        res.Swept,
		SweepCovered: res.SweepCovered,
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

func sortedResourceKeys(m map[string]*configs.Resource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
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
// uses, modPath being the static module the block itself is declared in
// (addrs.RootModule for a root resource).
func providerConfigAddr(rc *configs.Resource, modPath addrs.Module) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   modPath,
		Provider: rc.Provider,
		Alias:    rc.ProviderConfigAddr().Alias,
	}
}

// walkManagedResources calls fn once for every managed resource in cfg's
// whole static module tree, root first, then children in name order - the
// command-layer counterpart of the five walkers' own traversal, for the
// handful of places here that still need to look a resource block up by
// hand rather than through an identity.Resolution's already module-
// qualified address.
func walkManagedResources(cfg *configs.Config, fn func(rc *configs.Resource, modPath addrs.Module)) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	for _, rc := range cfg.Module.ManagedResources {
		fn(rc, cfg.Path)
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

// livePlanRejectUnsupported turns the plan options this command cannot
// honor into errors. Everything rejected here is rejected because stateless
// mode removes the thing the option operates on, or because v0 has not built
// it yet; nothing is silently ignored.
func livePlanRejectUnsupported(args *arguments.Plan) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	reject := func(summary, detail string) {
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail))
	}

	if args.ViewOptions.ViewType != arguments.ViewHuman || args.ViewOptions.JSONInto != nil {
		reject("Machine-readable output is not available under live resource markers yet",
			"live-plan prints a section describing what it could not read from the live system, and that section has no JSON representation yet. Rerun without -json or -json-into.")
	}
	if args.OutPath != "" {
		reject("Saved plan files are not available under live resource markers",
			"A saved plan file records the state snapshot the plan was made against so that apply can check the state has not moved since. A live-markers run has no state snapshot to record. Rerun without -out. Note that this configuration has no live block, so plain \"choudoufu plan\" and \"choudoufu apply\" here are ORDINARY state-backed commands, not live-markers ones - they would write a state file and propose creating resources this estate already owns. A live-markers apply exists only for a configuration carrying a live block, where plain plan and apply run on markers and an approval gate between them approves the intent rather than a frozen diff.")
	}
	if args.GenerateConfigPath != "" {
		reject("Config generation is not available under live resource markers yet",
			"-generate-config-out writes generated configuration for import blocks into a file, and that generated form has not been checked against the live-markers configuration subset yet. Rerun without -generate-config-out.")
	}
	if args.Operation.PlanMode != plans.NormalMode {
		reject("Only the normal planning mode is available under live resource markers yet",
			"live-plan produces a normal plan. -destroy is not verified against a live-markers apply yet; deleting a resource block from the configuration is the tested way to have its live resource destroyed, since the estate sweep plans an owned-but-undeclared resource as a destroy. -refresh-only compares a stored record against the live system, which is the comparison a live-markers run has no stored side for. Rerun without -destroy and -refresh-only.")
	}
	if args.State.StatePath != "" || args.State.StateOutPath != "" || args.State.BackupPath != "" {
		reject("State file options are not available under live resource markers",
			"There is no state file to read, write or back up: prior state is a projection built from the live system and discarded when the run ends. Rerun without -state, -state-out and -backup.")
	}

	return diags
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
}

var _ projection.Providers = (*statelessProviders)(nil)

// providerCacheKey is what a configured provider instance is cached and
// looked up under: the provider and its alias, deliberately not the module
// path a caller's [addrs.AbsProviderConfig] carries. See
// [statelessProviders.ConfiguredProvider]'s own comment for why a static
// module's resources correctly share the root's configured instance rather
// than each module getting - or needing - one of its own.
func providerCacheKey(addr addrs.AbsProviderConfig) string {
	return addr.Provider.String() + "\x00" + addr.Alias
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

// region is the region a list call for one provider configuration should go
// to: whatever the provider block sets, or the region the environment
// supplies, which is how the estate fixture configures itself. An empty
// answer leaves the list configuration's region unset, and the provider's
// own resolution stands.
func (p *statelessProviders) region(addr addrs.AbsProviderConfig) string {
	p.mu.Lock()
	val, ok := p.configVals[providerCacheKey(addr)]
	p.mu.Unlock()

	if ok && val != cty.NilVal && !val.IsNull() && val.Type().IsObjectType() && val.Type().HasAttribute("region") {
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

	if ok && val != cty.NilVal && !val.IsNull() && val.Type().IsObjectType() && val.Type().HasAttribute("endpoints") {
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

	// The cache and every lookup below key on the provider and its alias
	// alone, not on addr.Module: a static module call with no provider
	// block of its own inherits the root's default (unaliased)
	// configuration for its implied provider, which is the only shape a
	// static module's resources ever need (RuleChildModule refuses a
	// module block's own count/for_each, not a provider block inside one,
	// but nothing this fork generates or admits declares a provider inside
	// a child module either). One configured instance per provider+alias
	// is therefore correct root or not, and
	// [statelessProviders.providerConfigValue] already reads the provider
	// block from the root module unconditionally - addr.Module was never
	// consulted there even when this guard refused every non-root value
	// outright.
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
		return nil, fmt.Errorf("cannot evaluate the configuration of provider %s: %w", addr, cfgDiags.Err())
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
// An ALIASED address with no root provider block is an error, never an empty
// body. GitHub issue #123: the fallback used to cover that miss too, so a
// root resource with provider = aws.nope - which stock OpenTofu's graph
// refuses outright - had its provider configured from the environment alone,
// silently, and discovery read the live system through it.
// lint.RuleUndeclaredProviderAlias refuses the root-resource route before any
// cloud read and checkModuleProviderMapping the module-mapping one, so this
// diagnostic is the backstop for any address those rules did not see, not a
// message a user is expected to reach.
func (p *statelessProviders) providerConfigValue(ctx context.Context, addr addrs.AbsProviderConfig, spec hcldec.Spec) (cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	mod := p.config.Module

	// Find the root provider block for this address by resolving each
	// block's own local name to a provider FQN, not by round-tripping the
	// FQN through LocalNameForProvider: when required_providers gives one
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
		Module:  addrs.RootModule,
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
				"This run needs the provider configuration %q, and the root module declares no such provider block. "+
					"Configuring it from the environment instead would read and write the live system against whatever "+
					"account and region the environment names, which is not what the configuration says. Declare "+
					"provider %q with that alias, or remove the reference to it.",
				displayName, mod.LocalNameForProvider(addr.Provider),
			),
		))
		return cty.NilVal, diags
	}

	val, hclDiags := mod.StaticEvaluator.DecodeBlock(ctx, body, spec, ident)
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

  EXPERIMENTAL. Generates a plan with no authoritative state: no state file,
  no backend, and no lock. Prior state is a projection, built by reading the
  live system at the start of the run and discarded at the end.

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

Options:

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

  -parallelism=n          Limit the number of concurrent operations. Defaults
                          to 10.

  The following stock plan options are rejected rather than ignored, because
  live resource markers remove what they operate on or have not built them yet:
  -out, -state, -state-out, -backup, -destroy, -refresh-only,
  -generate-config-out, -json and -json-into. -refresh is accepted and has no
  effect: the projection is already fresh, so the plan never refreshes.
`
	return strings.TrimSpace(helpText)
}

func (c *LivePlanCommand) Synopsis() string {
	return "Show changes required by the configuration, with no state file (experimental)"
}
