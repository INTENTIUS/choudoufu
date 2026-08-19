// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"strings"

	"github.com/mitchellh/cli"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/liveimport"
	"github.com/intentius/choudoufu/internal/live/projection"
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
	if config.Module != nil && config.Module.Live != nil {
		recordStoreCfg = config.Module.Live.RecordStore
	}
	var residueStore *projection.ResidueStore
	if recordStoreCfg != nil {
		store, storeErr := projection.NewRecordStore(ctx, recordStoreCfg, args.Estate, ".")
		if storeErr != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot open the record store", fmt.Sprintf(
				"The live block's record_store %q could not be opened: %s.", recordStoreCfg.Type, storeErr,
			)))
			return nil, closer, diags
		}
		residueStore = projection.NewResidueStore(store, args.Estate)
	}

	rat, impDiags := liveimport.Ratify(ctx, liveimport.Request{
		Estate:       args.Estate,
		State:        stateFile.State,
		Providers:    provs,
		ResidueStore: residueStore,
	})
	diags = diags.Append(impDiags)
	return rat, closer, diags
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
	out := views.StatelessImportStamped{Estate: rep.Estate}
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
  tofu-estate and tofu-address markers onto every resource the report showed
  as VERIFIED or DRIFTED. Every other status - MISSING, UNTAGGABLE,
  UNADMITTED_TYPE - is never stamped; the report says why for each one.

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
  whose provider schema carries a tags argument can carry a marker. Every
  module is considered, root and child alike, and a stamped tofu-address
  carries the resource's full module path.

Options:

  -state=path             The tfstate file to read. Required.

  -estate=name            The estate this run verifies against and, with
                          -approve, stamps. Required: unlike live-plan and
                          live-mv, there is no configuration to derive it
                          from.

  -approve                Stamp every VERIFIED or DRIFTED resource from the
                          ratification report. Without it, the report prints
                          and nothing is written.

  -no-color               If specified, output won't contain any color.

  -compact-warnings       Show warnings in a more compact form.
`
	return strings.TrimSpace(helpText)
}

func (c *LiveImportCommand) Synopsis() string {
	return "Migrate a state-backed estate to live resource markers in bulk (experimental)"
}
