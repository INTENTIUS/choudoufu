// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
	"github.com/intentius/choudoufu/pkg/adoption"
)

// LiveImportCommand is the bulk migration path from a state-backed estate to
// live resource markers (issue #61): read an existing tfstate file once,
// verify every root-module managed resource it names against the live
// system, print the ratification report, and - only on a second run given
// -approve - stamp this estate's markers onto everything that verified.
//
// The pipeline is the observe/stamp split
// [github.com/intentius/choudoufu/internal/live/liveimport]'s package doc
// describes, adapted from chant's carve: liveImportRatify is entirely
// read-only (it opens the state file exactly once, and calls only
// GetProviderSchema and ReadResource on every provider it reaches), and its
// report is what gets printed whether or not -approve was given. Only when
// it was does this command call [liveimport.Ratification.Approve], which is
// the one thing in the whole command that writes.
type LiveImportCommand struct {
	Meta
}

// LiveImportCommander is live-import's entry in the new CLI's command tree. See
// [LiveCommanders].
func LiveImportCommander() Command {
	cmd := Command{
		Name:  "live-import",
		Short: (&LiveImportCommand{}).Synopsis(),
	}

	args := arguments.BindLiveImport(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveImportCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveImportCommand{Meta: meta}).Execute(args)
	}
	return cmd
}

func (c *LiveImportCommand) Run(rawArgs []string) int {
	return RunCommand(LiveImportCommander(), c.Meta, rawArgs)
}

func (c *LiveImportCommand) Execute(args *arguments.LiveImport) int {
	// The adopt stage's root span: "live-adopt", whose children are the
	// "live-adopt.ratify" and, under -approve, "live-adopt.approve" spans
	// opened in internal/live/liveimport.
	ctx, span := tracing.Tracer().Start(c.CommandContext(), "live-adopt",
		tracing.SpanAttributes(traceattrs.Bool("live.approve", args.Approve)),
	)
	defer span.End()
	var diags tfdiags.Diagnostics

	var err error
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(err)
		c.View.Diagnostics(diags)
		return 1
	}
	c.Meta.input = false

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())

	rat, adopted, closeProviders, ratDiags := c.liveImportRatify(ctx, args)
	diags = diags.Append(ratDiags)

	switch {
	case adopted != nil:
		views.NewLiveImport(c.View).Ratification(liveImportAdoptReport(args, adopted))
	case rat != nil:
		views.NewLiveImport(c.View).Ratification(liveImportReport(args.StatePath, rat))
	}
	if diags.HasErrors() || rat == nil {
		if closeProviders != nil {
			diags = diags.Append(closeProviders())
		}
		c.View.Diagnostics(diags)
		return 1
	}

	if !args.Approve {
		diags = diags.Append(closeProviders())
		c.View.Diagnostics(diags)
		return 0
	}

	// The providers Ratify configured are still running - approveOne reuses
	// them rather than reconnecting, so Approve's write is the only new call
	// this run makes. They are closed only now, once Approve has had its
	// chance to use them.
	stampRep, stampDiags := rat.Approve(ctx)
	diags = diags.Append(stampDiags)
	diags = diags.Append(closeProviders())
	if stampRep != nil {
		views.NewLiveImport(c.View).Stamped(liveImportStampReport(stampRep))
	}
	c.View.Diagnostics(diags)
	if diags.HasErrors() {
		return 1
	}
	return 0
}

// liveImportRatify is the whole observe half: read the state file exactly
// once, launch the configuration's providers, and hand both to
// [liveimport.Ratify]. Nothing after this function returns ever reads the
// state file again - the state.State value returned inside rat came from
// this one read, and rat carries forward everything Approve could possibly
// need instead.
//
// The provider processes it starts are not shut down here: Approve, called
// later in Run only when -approve was given, reuses the exact same
// connections rather than reconnecting for one write. The returned closer is
// what shuts them down, and the caller is responsible for calling it exactly
// once, after Approve has had its chance to run (or immediately, when there
// is no Approve call to wait for).
func (c *LiveImportCommand) liveImportRatify(ctx context.Context, args *arguments.LiveImport) (*liveimport.Ratification, *liveimport.AdoptRatification, func() tfdiags.Diagnostics, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	noop := func() tfdiags.Diagnostics { return nil }

	enc, encDiags := c.Encryption(ctx)
	diags = diags.Append(encDiags)
	if encDiags.HasErrors() {
		return nil, nil, noop, diags
	}

	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, nil, noop, diags
	}

	// GitHub issue #2293: with no -estate, the name a live run of this
	// configuration resolves; with no -state and no -adopt, the state the
	// backend block names, read once through the backend's own client, so
	// a migration needs no "state pull" first.
	if args.Estate == "" {
		estate, _, estateDiags := liveEstateFor(ctx, "", config)
		diags = diags.Append(estateDiags)
		if estate == "" {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"No estate named",
				"live-import resolves the estate the way a live run does (the live block's estate argument, the configuration's tofu-estate tags, the backend key, then the root's path), and nothing here resolved one. Pass -estate=<name>.",
			))
		}
		args.Estate = estate
	}
	var backendState *states.State
	if args.StatePath == "" && args.AdoptPath == "" {
		st, where, stDiags := liveImportStateFromBackend(ctx, config, enc)
		diags = diags.Append(stDiags)
		if !stDiags.HasErrors() {
			backendState = st
			args.StatePath = where
		}
	}
	if diags.HasErrors() {
		return nil, nil, noop, diags
	}

	// GitHub issue #2287: -adopt reads an adoption file and the index
	// version it names in place of a state file. Both are checked against
	// each other (scope key, version name, digest, every selected node)
	// before any provider is started.
	var priorState *states.State
	var adopt *adoption.Adoption
	if args.AdoptPath != "" {
		a, err := adoption.Open(args.AdoptPath, args.IndexStore)
		if err != nil {
			return nil, nil, noop, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Cannot read the adoption file",
				err.Error(),
			))
		}
		adopt = a
	} else if backendState != nil {
		priorState = backendState
	} else {
		stateFile, err := getStateFromPath(args.StatePath, enc)
		if err != nil {
			return nil, nil, noop, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Cannot read the state file",
				err.Error(),
			))
		}
		if stateFile == nil || stateFile.State == nil {
			return nil, nil, noop, diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Empty state file",
				args.StatePath+" carries no state, so there is nothing to ratify.",
			))
		}
		priorState = stateFile.State
	}

	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		diags = diags.Append(err)
		return nil, nil, noop, diags
	}

	provs := newProjectionProviders(config, coreOpts.Plugins)
	closer := func() tfdiags.Diagnostics { return provs.close(ctx) }

	// GitHub issue #327: the same record_store a live plan or apply
	// would open, opened here too, so Approve can classify and record
	// residue (issue #275) from the real object this run reads - see
	// [projection.RecordResidueForInstance]'s doc comment for why a migrate
	// is the one other place besides an apply that has a real, non-null
	// prior to classify with. A configuration with no live block, or a live
	// block with no record_store, yields a nil store, which every consumer
	// below already treats as "nothing to record" rather than an error -
	// the same as a plan or apply run with no record_store declared.
	var recordStoreCfg *configs.LiveRecordStore
	var retryCfg *configs.LiveRetry
	if config.Module != nil && config.Module.Live != nil {
		recordStoreCfg = config.Module.Live.RecordStore
		retryCfg = config.Module.Live.Retry
	}
	// GitHub issue #364: one store now for GitHub issue #340's record-backed
	// half (a record-backed resource's whole object lives directly under
	// projection.RecordKey, and a migration is the only thing that can seed
	// it for an estate that has never been applied by choudoufu), issue
	// #275's residue and issue #365 slice 2's located-identity half (see
	// [liveimport.Request.RecordStore]'s doc comment for the gap a nil
	// value here used to leave open).
	var recordStore *projection.RecordStore
	var rootOutputStore *projection.RootOutputStore
	if recordStoreCfg != nil {
		// The waiver warnings and, when this run will stamp, the store's
		// contract: see [openRecordStoreForImport], GitHub issues #1340,
		// #1376 and #1448.
		store, storeDiags := openRecordStoreForImport(ctx, projection.NewRecordStore, recordStoreCfg, retryCfg, args.Estate, args.Approve)
		diags = diags.Append(storeDiags)
		if storeDiags.HasErrors() {
			return nil, nil, closer, diags
		}
		recordStore = projection.NewRecordEnvelopeStore(store, projection.RecordStoreKeyPrefix(recordStoreCfg, args.Estate))
		// GitHub issue #349: the same underlying store again, its own
		// namespace rather than a member of the envelope - an output names
		// no live object at all and orphan discovery never needs to see
		// it, so it keeps the ESTATE rather than the envelope's key prefix
		// for the reason it always did: a key_prefix override must not be
		// able to move it under the record root, where orphan discovery's
		// listing would find it and the plan would propose destroying
		// whatever it names.
		rootOutputStore = projection.NewRootOutputStore(store, args.Estate)
		// GitHub issue #1859: terraform_estate_outputs reads another
		// estate's recorded outputs through the same store, and the
		// identity data-read phase below reads it before resolution
		// (GitHub issue #1575). Without this the builtin provider answered
		// every such read "Estate outputs need a live block" on a
		// configuration that has one. A configuration with no live block
		// never reaches here, and that refusal stays true for it.
		c.liveEstateOutputs().open(store, recordStoreCfg, args.Estate, "")
	}

	// GitHub issue #1543: the provider-configuration data-read phase, which
	// the plan paths have run since GitHub issue #313 and this one never
	// did. Placed here because it must be complete before the first
	// [projectionProviders.ConfiguredProvider] call, and Ratify's own first
	// instance makes one.
	liveImportProviderDataReads(ctx, config, provs, recordStore, priorState)

	req := liveimport.Request{
		Estate: args.Estate,
		// GitHub issue #372's remainder: the same configuration this
		// command already loaded above to find the record_store block, now
		// also handed to Ratify so migrationSlots can settle a client-named
		// count instance's slot from its own declaration instead of
		// leaving it unsettled. See [liveimport.Request.Config].
		Config: config,
		// GitHub issue #2135: what an identity record's subscription_id is
		// answered from when the provider block names none, as for a live
		// plan's identity resolution (live_mode.go's liveRecordCloud).
		Cloud:     liveRecordCloud(),
		State:     priorState,
		Providers: provs,
		// GitHub issue #1109: the cluster client a manifest-shape
		// resource's tofu-estate label is written through. The same
		// projectionProviders the reads go through, so the write lands
		// under the credential the provider block names. See
		// live_import_kubernetes.go.
		Clusters: provs,
		// GitHub issue #365: the strict block's secrets setting, resolved
		// here rather than left to the zero value, because the zero value is
		// "refuse" and an OMITTED argument means "store". This is the one
		// place in the migrate path that resolution happens; see
		// identity.SecretsFor.
		Secrets:         identity.SecretsFor(config),
		RecordStore:     recordStore,
		RootOutputStore: rootOutputStore,
		// GitHub issue #583: -parallelism, spelled and defaulted as stock's
		// apply spells it. Ratify ignores it; Approve is what spends it.
		Parallelism: args.Parallelism,
		// GitHub issue #2063: one stderr line per resource as -approve's
		// write for it returns. Ratify ignores it.
		Progress: liveImportProgress(c.View, args),
	}
	if adopt != nil {
		adopted, adoptDiags := liveimport.RatifyAdoption(ctx, liveimport.AdoptRequest{Request: req, Adoption: adopt})
		diags = diags.Append(adoptDiags)
		if adopted == nil {
			return nil, nil, closer, diags
		}
		return adopted.Ratification, adopted, closer, diags
	}
	rat, impDiags := liveimport.Ratify(ctx, req)
	diags = diags.Append(impDiags)
	return rat, nil, closer, diags
}

// liveImportProviderDataReads runs GitHub issue #313's provider-
// configuration data-read phase on the migrate path, which until GitHub
// issue #1543 only live-plan (live_plan.go:678) and a plan or apply under a
// live block (live_mode.go:1164) ran. Without it
// [projectionProviders.providerConfigValue] decodes a provider block through
// the module's bare static evaluator, so `provider "kubernetes" { host =
// data.aws_eks_cluster.cluster.endpoint }` - corpus-eks-basic's own shape -
// refuses with "Dynamic value in static context", [ratifyOne]'s
// ConfiguredProvider call fails, and every instance that provider serves is
// reported MISSING. An estate could therefore be planned and not migrated,
// and since GitHub issue #1108 made an unlabelled declared Kubernetes object
// read UNOWNED rather than bind by natural key, the marker that migration
// never wrote turned into a proposed create of an object that already
// exists.
//
// Nothing here raises a diagnostic. Every phase it runs is fatal on the plan
// path and best-effort here, for the reason [liveimport.Ratify] already
// drops its own [identity.ResolveWith] diagnostics: this is an input to
// configuring a provider, not a verdict about the estate, and a migration
// that refused where it used to report would be a new refusal on the one
// command whose whole job is to get an existing estate onto markers. What a
// phase cannot supply leaves the provider exactly as unconfigurable as it is
// today, with the same diagnostic ratifyOne has always printed for it.
//
// # What it costs, and who pays it
//
// The gate is offline and exact: [dataread.AnalyzeProviderConfigs] over the
// bare options walks the provider blocks' own argument expressions and
// records every declared data resource they reach, before any eligibility
// rule that would want a schema (see [dataread.Analysis.Demands] and
// analyzer.classify, which stores its record on every path that gets past
// "no such data resource"), and since GitHub issue #1113 every managed value
// they read that their own configuration cannot answer. A configuration whose
// provider blocks name neither - every estate that migrated before this existed - returns
// here having started no plugin, read nothing, and resolved nothing, so its
// report is unchanged by construction rather than by measurement.
//
// A configuration that does pay it pays what a live-plan of the same
// configuration already pays: every provider plugin started for schemas,
// one ReadDataSource per data block identity demands, a second resolution
// pass's PlanResourceChange calls when the first pass refused and named a
// managed block, and then the fixpoint's own reads. Read-only, and the same
// calls the plan the operator is migrating towards makes anyway.
//
// The read-parallelism setting is read for its value and not for its
// refusal: [projection.ReadInstances] materializes sequentially at every
// setting (see [liveProviderDataReads]'s own note), so raising it here
// would add a refusal to live-import over a knob that cannot change what
// live-import does. The plan paths still refuse it, where it is load-bearing.
//
// The nil [identity.Scope] is live-import having no -target or -exclude flag
// to honour, and nil means every block is in scope - the same value
// live-mv and live-ls pass for the same reason.
func liveImportProviderDataReads(ctx context.Context, config *configs.Config, provs *projectionProviders, recordStore *projection.RecordStore, state *states.State) {
	if !dataread.AnalyzeProviderConfigs(ctx, config, dataread.Options{}).Demands() {
		return
	}

	resourceSchemas := provs.resourceSchemas(ctx)

	// GitHub issue #179's identity data-read class, ahead of resolution
	// exactly as the plan paths run it: the fixpoint below reads a managed
	// resource a provider-configuration data source names, and it finds
	// that resource through the resolution map, so an identity that needs a
	// data source of its own has to be resolvable before the chain can be
	// followed.
	dataResults, drDiags := liveDataReads(ctx, config, provs, resourceSchemas, nil)
	for _, d := range drDiags {
		log.Printf("[TRACE] live-import: identity data reads: %s", d.Description().Summary)
	}

	resolutions, idDiags := liveResolve(ctx, config, provs, resourceSchemas, dataResults, nil)
	for _, d := range idDiags {
		log.Printf("[TRACE] live-import: identity resolution for the provider-configuration data reads: %s", d.Description().Summary)
	}

	readPar, parDiags := readParallelismSetting()
	for _, d := range parDiags {
		log.Printf("[TRACE] live-import: %s", d.Description().Summary)
	}

	// GitHub issue #1113's managed half rides along: a provider block that
	// reads a managed value itself is answered from the same prior state,
	// so the cluster a migration's kubernetes provider reads is the one the
	// state file names, read from nothing. The failures that are fatal on
	// the plan paths are traced here with everything else, for the reason
	// this function's doc comment gives; a state-held instance is seeded,
	// never read, so it cannot produce one.
	var pdDiags tfdiags.Diagnostics
	provs.providerDataResults, provs.providerManagedResults, pdDiags = liveProviderDataReads(ctx, config, provs, resourceSchemas, resolutions, recordStore, readPar, nil, liveImportPriorManagedValues(state, resourceSchemas))
	for _, d := range pdDiags {
		log.Printf("[TRACE] live-import: provider-configuration reads: %s", d.Description().Summary)
	}
}

// liveImportPriorManagedValues is the state file being migrated, decoded into
// [projection.ReadInstances]' own output shape - every managed instance in it,
// keyed by absolute instance address - for [liveProviderDataReads]'
// priorManaged argument.
//
// It is the migrate path's whole answer to a question the plan path never has
// to ask. The fixpoint reads a managed instance a provider-configuration data
// source names, and it reads a record-backed one out of the estate's record
// store; a migration is what WRITES that store, so during Ratify the store is
// empty and such an instance cannot be materialized at all. The state file has
// had the value the whole time. corpus-eks-basic is the measured case:
// data.aws_eks_cluster.cluster needs module.eks.aws_eks_cluster.this[0], whose
// identity is parent-derived from random_string.suffix, which is record-backed
// - the plan path read both and the migrate path read neither.
//
// The state is also the RIGHT source rather than a convenient one. It is the
// prior state stock OpenTofu would hand its own plan graph for this
// configuration, which is exactly what [liveProviderDataReads]' doc
// comment says the phase reproduces, and it is the file this command's whole
// job is to migrate from.
//
// Silent and partial on purpose, like every other input to that phase: a type
// this run has no schema for, an instance with only a deposed object, an
// object that will not decode against the schema it was written with, are each
// left out rather than raised. What is missing costs the one provider
// configuration that wanted it, which then fails to configure with the
// diagnostic it already had.
func liveImportPriorManagedValues(state *states.State, schemas map[string]providers.Schema) map[string]cty.Value {
	if state == nil || len(schemas) == 0 {
		return nil
	}
	out := make(map[string]cty.Value)
	for _, mod := range state.Modules {
		for _, res := range mod.Resources {
			if res.Addr.Resource.Mode != addrs.ManagedResourceMode {
				continue
			}
			schema, ok := schemas[res.Addr.Resource.Type]
			if !ok || schema.Block == nil {
				continue
			}
			ty := schema.Block.ImpliedType()
			for key, inst := range res.Instances {
				if inst == nil || inst.Current == nil {
					continue
				}
				obj, err := inst.Current.Decode(ty)
				if err != nil || obj == nil || obj.Value == cty.NilVal {
					continue
				}
				out[res.Addr.Instance(key).String()] = obj.Value
			}
		}
	}
	return out
}

// liveImportProgress is -progress-json's callback for Approve, or nil when
// the flag is absent or the run will not stamp.
func liveImportProgress(view *views.View, args *arguments.LiveImport) func(liveimport.Entry, liveimport.StampOutcome) {
	if !args.ProgressJSON || !args.Approve {
		return nil
	}
	progress := views.NewLiveWriteProgress(view)
	return func(entry liveimport.Entry, out liveimport.StampOutcome) {
		progress.Line(liveImportProgressLine(args.Estate, entry, out))
	}
}

// liveImportProgressLine maps one Approve outcome onto the four the
// -progress-json line reports. A record write is the marker for a
// record-backed or located instance, so it is "stamped" like a tag write.
func liveImportProgressLine(estate string, entry liveimport.Entry, out liveimport.StampOutcome) views.LiveProgressLine {
	line := views.LiveProgressLine{
		EstateTo: estate,
		Address:  entry.Addr.String(),
		LiveID:   entry.LiveID,
	}
	switch out.Outcome {
	case liveimport.OutcomeStamped, liveimport.OutcomeRecorded, liveimport.OutcomeSensitivityRecorded:
		line.Outcome = views.LiveProgressStamped
	case liveimport.OutcomeFailed:
		line.Outcome = views.LiveProgressRefused
		line.Reason = out.Detail
	case liveimport.OutcomeAlreadyStamped, liveimport.OutcomeAlreadyRecorded:
		line.Outcome = views.LiveProgressSkipped
		line.Reason = string(out.Outcome)
		if out.Detail != "" {
			line.Reason += ": " + out.Detail
		}
	default:
		line.Outcome = views.LiveProgressSkipped
		line.Reason = out.Detail
	}
	return line
}

func liveImportReport(statePath string, rat *liveimport.Ratification) views.LiveImportReport {
	rep := views.LiveImportReport{
		Estate:    rat.Estate,
		StatePath: statePath,
	}
	for _, e := range rat.Entries {
		rep.Entries = append(rep.Entries, views.LiveImportEntry{
			Addr:     e.Addr.String(),
			TypeName: e.TypeName,
			Status:   string(e.Status),
			Detail:   e.Detail,
			LiveID:   e.LiveID,
			Drifted:  e.Drifted,
		})
	}
	return rep
}

// liveImportAdoptReport is the report for a -adopt run (GitHub issue
// #2287): the ratification of what was adopted, the refused selections, and
// every marker write -approve would make, each tags only.
func liveImportAdoptReport(args *arguments.LiveImport, a *liveimport.AdoptRatification) views.LiveImportReport {
	rep := liveImportReport("", a.Ratification)
	ad := &views.LiveImportAdoption{
		AdoptPath:    args.AdoptPath,
		ScopeKey:     a.Index.ScopeKey,
		IndexVersion: a.Index.Version,
	}
	for _, r := range a.Refused {
		ad.Refused = append(ad.Refused, views.LiveImportAdoptRefusal{
			Node:     r.Node,
			As:       r.As,
			TypeName: r.TypeName,
			ImportID: r.ImportID,
			Reason:   r.Reason,
		})
	}
	for _, w := range a.MarkerWrites() {
		tags := make([]string, 0, len(w.Tags))
		for k, v := range w.Tags {
			tags = append(tags, k+"="+v)
		}
		sort.Strings(tags)
		ad.Writes = append(ad.Writes, views.LiveImportMarkerWrite{
			Addr:     w.Addr.String(),
			TypeName: w.TypeName,
			Node:     w.Node,
			ImportID: w.ImportID,
			Tags:     tags,
		})
	}
	rep.Adopt = ad
	return rep
}

func liveImportStampReport(rep *liveimport.StampReport) views.LiveImportStamped {
	out := views.LiveImportStamped{Estate: rep.Estate, IdentitiesRecorded: rep.IdentitiesRecorded}
	for _, o := range rep.Outcomes {
		out.Outcomes = append(out.Outcomes, views.LiveImportOutcome{
			Addr:     o.Addr.String(),
			TypeName: o.TypeName,
			Outcome:  string(o.Outcome),
			Detail:   o.Detail,
		})
	}
	return out
}

func (c *LiveImportCommand) Help() string {
	helpText := `
Usage: choudoufu [global options] live-import -state=PATH -estate=NAME [-approve]
       choudoufu [global options] live-import -adopt=PATH -index-store=DIR -estate=NAME [-approve]

  Migrates an existing, state-backed estate to live resource
  markers in bulk: reads a tfstate file (v4 format) once, read-only, verifies
  every root-module managed resource it names against the live system, and
  prints a ratification report. No tag is written on this run.

  Rerun with the same two flags plus -approve to stamp this estate's
  ownership marker onto every resource the report showed as VERIFIED or
  DRIFTED. On AWS that marker is two tags, tofu-estate and tofu-address. On
  Kubernetes it is one label, tofu-estate, and the address in the
  choudoufu.intentius.io/tofu-address annotation. Every other status -
  MISSING, UNTAGGABLE, UNADMITTED_TYPE - is never stamped; the report says
  why for each one.

  The state file is opened exactly once, at the start of the run, and is
  never opened again - not to write it, and not to read it a second time,
  whether or not -approve was given. Nothing about the file changes: it
  remains exactly as usable by ordinary state-backed OpenTofu after a stamp
  as it was before. A marker write is additive, not a migration the state
  file's own authority depends on, so there is no separate rollback step -
  the state file is simply still there, untouched, for as long as you keep
  using it.

  A resource's identity for verification comes entirely from what the state
  file already recorded - its own attributes and the provider's private data -
  never from a resource block in configuration. Providers still need to be
  configured to reach the live system, though, so run this in a working
  directory "choudoufu init" has already prepared, alongside the same
  provider configuration the state was last applied with.

  Only resource types with a row in the live-markers admission table
  (live/LIMITATIONS.md) can be verified or stamped at all, and only those
  whose provider schema offers somewhere to write the marker can carry one:
  an AWS tags argument, a Kubernetes metadata.labels map, or the manifest a
  kubernetes_manifest holds, which is labelled by one merge patch against
  the API server. Every module is considered, root and child alike, and a
  stamped tofu-address carries the resource's full module path.

  An estate name may be up to 128 characters, but a Kubernetes label value
  is capped at 63 and has its own character rules. When the state holds any
  object whose marker is a label, a name that cannot be written as a label
  value is refused once, by the read-only run, rather than once per object
  at -approve.

  With -adopt, the run starts from an adoption file (adopt: v1) instead of
  a state file: an index version, named by scope key, version and digest,
  and the index nodes to adopt, each with the Terraform address to adopt it
  as and, where its listed type maps to more than one Terraform type, which
  one. The index only names each object. Every object is imported and read
  again through its provider, and that read decides: an object carrying
  another estate's marker, a plunge stack's or chant's is refused, and so
  is one whose tags can be set only at create (it would be replaced to mark
  it), a type that cannot be imported, and an ambiguous type the selection
  does not name. The report lists every marker write -approve would make,
  each a tags-only write.

Options:

  -state=path             The tfstate file to read. Required unless -adopt
                          is given.

  -adopt=path             An adoption file (adopt: v1) to adopt indexed
                          objects from, in place of -state.

  -index-store=dir        With -adopt, the index store directory: the
                          version is read from
                          <dir>/index/<scopeKey>/<version>. Required with
                          -adopt.

  -estate=name            The estate this run verifies against and, with
                          -approve, stamps. Required: unlike live-plan and
                          live-mv, there is no configuration to derive it
                          from.

  -approve                Stamp every VERIFIED or DRIFTED resource from the
                          ratification report. Without it, the report prints
                          and nothing is written.

  -parallelism=n          Limit the number of resources stamped at once.
                          Defaults to 10, the same default an apply of this
                          configuration already runs at, over the same kind
                          of work: one provider plan+apply round trip per
                          resource. Lower it if the account's tagging APIs
                          or the cluster's API server push back. No effect
                          without -approve.

  -progress-json          With -approve, write one JSON line to stderr for
                          each resource as its marker write returns, in the
                          order the writes return: {at, estate_from,
                          estate_to, address, live_id, outcome, reason}.
                          outcome is stamped, refused or skipped, and
                          estate_from is empty, since the source is a state
                          file. Every resource in the report gets a line,
                          the ones never stamped included. No effect
                          without -approve. Stdout is unchanged.

  -no-color               If specified, output won't contain any color.

  -compact-warnings       Show warnings in a more compact form.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveImportCommand) Synopsis() string {
	return "Migrate a state-backed estate to live resource markers in bulk"
}

// liveImportStateFromBackend reads the old state through the configuration's
// backend block, once, read-only (GitHub issue #2293): the ignored one under
// a live block, or the active one on a root not yet converted. where names
// it for the report in place of a -state path.
//
// It takes no lock. The read is the same one "state pull" makes, and a
// migration that waited on someone else's lock, or held one, would be a
// second writer's behaviour in a command that never writes state.
func liveImportStateFromBackend(ctx context.Context, config *configs.Config, enc encryption.Encryption) (*states.State, string, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	var be *configs.Backend
	if config != nil && config.Module != nil {
		be = config.Module.IgnoredBackend
		if be == nil {
			be = config.Module.Backend
		}
	}
	if be == nil {
		return nil, "", diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state file named",
			"live-import reads an existing state once, read-only, and this configuration has no backend block to read it from. Pass -state=<path> naming a tfstate file, or -adopt=<path> naming an adoption file.",
		))
	}
	where := fmt.Sprintf("backend %q at %s", be.Type, configs.RangeFileLine(be.DeclRange))
	fail := func(detail string) (*states.State, string, tfdiags.Diagnostics) {
		return nil, "", diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot read the state from the backend",
			fmt.Sprintf("live-import read no -state, so it reads the state %s names. %s Pass -state=<path> with a file from \"state pull\" instead.", where, detail),
		))
	}
	if be.Type == "local" {
		path, ok := configs.BackendLiteral(be, "path")
		if !ok || path == "" {
			path = "terraform.tfstate"
		}
		stateFile, err := getStateFromPath(path, enc)
		if err != nil {
			return fail(err.Error())
		}
		if stateFile == nil || stateFile.State == nil {
			return fail(path + " carries no state.")
		}
		return stateFile.State, path, diags
	}
	b, problem := configureBlockBackend(ctx, be, config.Module.StaticEvaluator, enc.State())
	if problem != "" {
		return fail(problem)
	}
	stateFile, err := getStateFromBackend(ctx, b, currentWorkspaceName())
	if err != nil {
		return fail(err.Error())
	}
	if stateFile == nil || stateFile.State == nil {
		return fail("It holds no state, so there is nothing to ratify.")
	}
	return stateFile.State, where, diags
}
