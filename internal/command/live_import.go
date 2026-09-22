// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"log"
	"strings"

	"github.com/mitchellh/cli"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
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

func (c *LiveImportCommand) Run(rawArgs []string) int {
	ctx := c.CommandContext()

	common, rawArgs := arguments.ParseView(rawArgs)
	c.View.Configure(common)

	args, diags := arguments.ParseLiveImport(rawArgs)
	if diags.HasErrors() {
		// Argument errors print the usage text, the way state mv and
		// live-mv answer a malformed command line.
		c.View.Diagnostics(diags)
		return cli.RunResultHelp
	}

	var err error
	if c.pluginPath, err = c.loadPluginPath(); err != nil {
		diags = diags.Append(err)
		c.View.Diagnostics(diags)
		return 1
	}
	c.Meta.input = false

	diags = diags.Append(c.providerDevOverrideRuntimeWarnings())

	rat, closeProviders, ratDiags := c.liveImportRatify(ctx, args)
	diags = diags.Append(ratDiags)

	if rat != nil {
		views.NewStatelessImport(c.View).Ratification(liveImportReport(args.StatePath, rat))
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
		views.NewStatelessImport(c.View).Stamped(liveImportStampReport(stampRep))
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
func (c *LiveImportCommand) liveImportRatify(ctx context.Context, args *arguments.LiveImport) (*liveimport.Ratification, func() tfdiags.Diagnostics, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	noop := func() tfdiags.Diagnostics { return nil }

	enc, encDiags := c.Encryption(ctx)
	diags = diags.Append(encDiags)
	if encDiags.HasErrors() {
		return nil, noop, diags
	}

	stateFile, err := getStateFromPath(args.StatePath, enc)
	if err != nil {
		return nil, noop, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot read the state file",
			err.Error(),
		))
	}
	if stateFile == nil || stateFile.State == nil {
		return nil, noop, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Empty state file",
			args.StatePath+" carries no state, so there is nothing to ratify.",
		))
	}

	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, noop, diags
	}

	coreOpts, err := c.contextOpts(ctx)
	if err != nil {
		diags = diags.Append(err)
		return nil, noop, diags
	}

	provs := newStatelessProviders(config, coreOpts.Plugins)
	closer := func() tfdiags.Diagnostics { return provs.close(ctx) }

	// GitHub issue #327: the same record_store a stateless plan or apply
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
			return nil, closer, diags
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
	}

	// GitHub issue #1543: the provider-configuration data-read phase, which
	// the plan paths have run since GitHub issue #313 and this one never
	// did. Placed here because it must be complete before the first
	// [statelessProviders.ConfiguredProvider] call, and Ratify's own first
	// instance makes one.
	liveImportProviderDataReads(ctx, config, provs, recordStore, stateFile.State)

	rat, impDiags := liveimport.Ratify(ctx, liveimport.Request{
		Estate: args.Estate,
		// GitHub issue #372's remainder: the same configuration this
		// command already loaded above to find the record_store block, now
		// also handed to Ratify so migrationSlots can settle a client-named
		// count instance's slot from its own declaration instead of
		// leaving it unsettled. See [liveimport.Request.Config].
		Config:    config,
		State:     stateFile.State,
		Providers: provs,
		// GitHub issue #1109: the cluster client a manifest-shape
		// resource's tofu-estate label is written through. The same
		// statelessProviders the reads go through, so the write lands
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
	})
	diags = diags.Append(impDiags)
	return rat, closer, diags
}

// liveImportProviderDataReads runs GitHub issue #313's provider-
// configuration data-read phase on the migrate path, which until GitHub
// issue #1543 only live-plan (live_plan.go:678) and a plan or apply under a
// live block (live_mode.go:1164) ran. Without it
// [statelessProviders.providerConfigValue] decodes a provider block through
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
// rule that would want a schema (see [dataread.Analysis.Empty] and
// analyzer.classify, which stores its record on every path that gets past
// "no such data resource"). A configuration whose provider blocks name no
// data source - every estate that migrated before this existed - returns
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
// setting (see [statelessProviderDataReads]'s own note), so raising it here
// would add a refusal to live-import over a knob that cannot change what
// live-import does. The plan paths still refuse it, where it is load-bearing.
//
// The nil [identity.Scope] is live-import having no -target or -exclude flag
// to honour, and nil means every block is in scope - the same value
// live-mv and live-ls pass for the same reason.
func liveImportProviderDataReads(ctx context.Context, config *configs.Config, provs *statelessProviders, recordStore *projection.RecordStore, state *states.State) {
	if dataread.AnalyzeProviderConfigs(ctx, config, dataread.Options{}).Empty() {
		return
	}

	resourceSchemas := provs.resourceSchemas(ctx)

	// GitHub issue #179's identity data-read class, ahead of resolution
	// exactly as the plan paths run it: the fixpoint below reads a managed
	// resource a provider-configuration data source names, and it finds
	// that resource through the resolution map, so an identity that needs a
	// data source of its own has to be resolvable before the chain can be
	// followed.
	dataResults, drDiags := statelessDataReads(ctx, config, provs, resourceSchemas, nil)
	for _, d := range drDiags {
		log.Printf("[TRACE] live-import: identity data reads: %s", d.Description().Summary)
	}

	resolutions, idDiags := statelessResolve(ctx, config, provs, resourceSchemas, dataResults, nil)
	for _, d := range idDiags {
		log.Printf("[TRACE] live-import: identity resolution for the provider-configuration data reads: %s", d.Description().Summary)
	}

	readPar, parDiags := readParallelismSetting()
	for _, d := range parDiags {
		log.Printf("[TRACE] live-import: %s", d.Description().Summary)
	}

	provs.providerDataResults = statelessProviderDataReads(ctx, config, provs, resourceSchemas, resolutions, recordStore, readPar, nil, liveImportPriorManagedValues(state, resourceSchemas))
}

// liveImportPriorManagedValues is the state file being migrated, decoded into
// [projection.ReadInstances]' own output shape - every managed instance in it,
// keyed by absolute instance address - for [statelessProviderDataReads]'
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
// configuration, which is exactly what [statelessProviderDataReads]' doc
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

func liveImportReport(statePath string, rat *liveimport.Ratification) views.StatelessImportReport {
	rep := views.StatelessImportReport{
		Estate:    rat.Estate,
		StatePath: statePath,
	}
	for _, e := range rat.Entries {
		rep.Entries = append(rep.Entries, views.StatelessImportEntry{
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

func liveImportStampReport(rep *liveimport.StampReport) views.StatelessImportStamped {
	out := views.StatelessImportStamped{Estate: rep.Estate, IdentitiesRecorded: rep.IdentitiesRecorded}
	for _, o := range rep.Outcomes {
		out.Outcomes = append(out.Outcomes, views.StatelessImportOutcome{
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

  EXPERIMENTAL. Migrates an existing, state-backed estate to live resource
  markers in bulk: reads a tfstate file (v4 format) once, read-only, verifies
  every root-module managed resource it names against the live system, and
  prints a ratification report. No tag is written on this run.

  Rerun with the same two flags plus -approve to stamp this estate's
  ownership marker onto every resource the report showed as VERIFIED or
  DRIFTED. On AWS that marker is two tags, tofu-estate and tofu-address. On
  Kubernetes it is one label, tofu-estate, and no address: an object is
  re-bound by its own group, kind, namespace and name, so the address never
  goes onto it. Every other status - MISSING, UNTAGGABLE, UNADMITTED_TYPE -
  is never stamped; the report says why for each one.

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

Options:

  -state=path             The tfstate file to read. Required.

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

  -no-color               If specified, output won't contain any color.

  -compact-warnings       Show warnings in a more compact form.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveImportCommand) Synopsis() string {
	return "Migrate a state-backed estate to live resource markers in bulk (experimental)"
}
