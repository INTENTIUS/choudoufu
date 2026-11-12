// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// LiveSweepSecretsCommand is the orphan sweep of `strict { secrets = "ssm" }`
// (GitHub issue #2393, follow-up 2): the SSM parameters under the estate's
// namespace that no record references and that are older than the grace
// period. A lost race or a failed commit leaves them, and so does a
// superseded value whose delete failed. By default it lists them and
// changes nothing; -delete deletes them through
// [projection.SSMSecretsStore.SweepOrphans], whose doc comment carries the
// argument that it never deletes a parameter a record references, a
// concurrent writer's included. It writes no record.
type LiveSweepSecretsCommand struct {
	Meta

	// open is the record store opener; nil means [projection.NewRecordStore].
	open recordStoreOpener
	// sweeper finds the sweep under the opened store; nil means
	// [projection.AsSSMSecretsStore]. A seam for the command's tests.
	sweeper func(staterecord.Store) (secretsSweeper, bool)
}

// secretsSweeper is what this command asks of [projection.SSMSecretsStore].
type secretsSweeper interface {
	Namespace() string
	ListOrphans(ctx context.Context) (projection.SweepResult, error)
	SweepOrphans(ctx context.Context) (projection.SweepResult, error)
	TakeDiagnostics() tfdiags.Diagnostics
}

// LiveSweepSecretsCommander is live-sweep-secrets' entry in the new CLI's
// command tree. See [LiveCommanders].
func LiveSweepSecretsCommander() Command {
	return liveSweepSecretsCommander(nil, nil)
}

func liveSweepSecretsCommander(open recordStoreOpener, sweeper func(staterecord.Store) (secretsSweeper, bool)) Command {
	cmd := Command{
		Name:  "live-sweep-secrets",
		Short: (&LiveSweepSecretsCommand{}).Synopsis(),
	}
	args := arguments.BindLiveSweepSecrets(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveSweepSecretsCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveSweepSecretsCommand{Meta: meta, open: open, sweeper: sweeper}).Execute(args)
	}
	return cmd
}

func (c *LiveSweepSecretsCommand) Run(rawArgs []string) int {
	return RunCommand(liveSweepSecretsCommander(c.open, c.sweeper), c.Meta, rawArgs)
}

// liveSweepSecretsReport is the -json document.
type liveSweepSecretsReport struct {
	Estate     string   `json:"estate"`
	Namespace  string   `json:"namespace"`
	DryRun     bool     `json:"dry_run"`
	Referenced int      `json:"referenced"`
	Young      []string `json:"young"`
	Orphans    []string `json:"orphans"`
	Deleted    []string `json:"deleted"`
	Grace      string   `json:"grace"`
}

func (c *LiveSweepSecretsCommand) Execute(args *arguments.LiveSweepSecrets) int {
	ctx := c.CommandContext()
	report, diags := c.sweep(ctx, ".", args)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	c.View.Diagnostics(diags)
	var out string
	if args.JSON {
		b, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			c.View.Diagnostics(tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the report", err.Error())))
			return 1
		}
		out = string(b)
	} else {
		out = report.text()
	}
	views.NewLiveSweepSecrets(c.View).Output(out)
	return 0
}

func (c *LiveSweepSecretsCommand) sweep(ctx context.Context, dir string, args *arguments.LiveSweepSecrets) (*liveSweepSecretsReport, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	config, cfgDiags := c.loadConfig(ctx, dir)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, diags
	}
	if config.Module == nil || config.Module.Live == nil || config.Module.Live.RecordStore == nil || !config.Module.Live.RecordStore.SecretsInSSM {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No secrets in SSM",
			"live-sweep-secrets sweeps the SSM parameters strict { secrets = \"ssm\" } keeps an estate's secret values in, and this configuration does not set it. Under any other setting there are no such parameters."))
	}
	live := config.Module.Live
	rs := live.RecordStore

	estate, _, estateDiags := liveEstateFor(ctx, args.Estate, config)
	diags = diags.Append(estateDiags)
	if estate == "" && !estateDiags.HasErrors() && live.Estate != "" && discovery.ValidEstateName(live.Estate) {
		estate = live.Estate
	}
	if estateDiags.HasErrors() {
		return nil, diags
	}
	if estate == "" {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No estate name",
			"An estate's parameters are kept under its records' namespace, and this configuration names no single estate. Pass -estate=<name>."))
	}

	open := c.open
	if open == nil {
		open = projection.NewRecordStore
	}
	storeOpts, err := recordStoreOpenOptions()
	if err != nil {
		return nil, diags.Append(recordStoreOpenDiag(rs.Type, err))
	}
	store, err := open(ctx, rs, live.Retry, estate, dir, storeOpts...)
	if err != nil {
		return nil, diags.Append(recordStoreOpenDiag(rs.Type, err))
	}
	find := c.sweeper
	if find == nil {
		find = func(s staterecord.Store) (secretsSweeper, bool) { return projection.AsSSMSecretsStore(s) }
	}
	sw, ok := find(store)
	if !ok {
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "No secrets in SSM",
			"The record store opened without the SSM secrets store under it, so there is nothing to sweep."))
	}

	var res projection.SweepResult
	if args.Delete {
		res, err = sw.SweepOrphans(ctx)
	} else {
		res, err = sw.ListOrphans(ctx)
	}
	diags = diags.Append(sw.TakeDiagnostics())
	if err != nil {
		detail := err.Error()
		if len(res.Deleted) > 0 {
			detail += fmt.Sprintf("\n\nDeleted before the failure: %s.", strings.Join(res.Deleted, ", "))
		}
		return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, "The orphan sweep did not finish", detail))
	}
	report := &liveSweepSecretsReport{
		Estate:     estate,
		Namespace:  sw.Namespace(),
		DryRun:     !args.Delete,
		Referenced: res.Referenced,
		Young:      nonNil(res.Young),
		Orphans:    nonNil(res.Orphans),
		Deleted:    nonNil(res.Deleted),
		Grace:      projection.DefaultSSMSweepGrace.String(),
	}
	return report, diags
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (r *liveSweepSecretsReport) text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Secret parameters under %s (estate %s):\n", r.Namespace, r.Estate)
	fmt.Fprintf(&b, "  %d referenced by a record, kept\n", r.Referenced)
	fmt.Fprintf(&b, "  %d unreferenced and younger than the %s grace period, kept: a write may be about to commit them\n", len(r.Young), r.Grace)
	fmt.Fprintf(&b, "  %d orphans: unreferenced and older than the grace period\n", len(r.Orphans))
	if r.DryRun {
		for _, o := range r.Orphans {
			fmt.Fprintf(&b, "    %s\n", o)
		}
		if len(r.Orphans) > 0 {
			b.WriteString("\nNothing was deleted. Run again with -delete to delete the orphans.\n")
		}
		return b.String()
	}
	for _, d := range r.Deleted {
		fmt.Fprintf(&b, "    deleted %s\n", d)
	}
	if n := len(r.Orphans) - len(r.Deleted); n > 0 {
		fmt.Fprintf(&b, "\n%d orphans were already gone when the sweep reached them.\n", n)
	}
	return b.String()
}

func (c *LiveSweepSecretsCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-sweep-secrets [options]

  Lists, and with -delete deletes, the orphaned SSM parameters of an
  estate that keeps its secret values in SSM (strict { secrets = "ssm" }).
  An orphan is a parameter under the estate's namespace that no record
  references and that is older than the 24h grace period: what a write
  that lost a race or failed to commit leaves, and a superseded value
  whose delete failed. Orphans hold secret values nothing reads and count
  against the account's 10,000 standard parameters.

  It never deletes a parameter a record references, including one a
  concurrent apply is about to commit: the grace period and two time bounds
  rule that out, and the argument is in SweepOrphans' doc comment
  (internal/live/projection/ssmsweep.go). It reads every record before
  deleting, stops before deleting anything if a record cannot be read, and
  writes no record.

  It needs what the estate's apply role already holds:
  ssm:GetParametersByPath and ssm:DeleteParameter under the path, and read
  on the record store. It never decrypts a value.

Options:

  -delete        Delete the orphans. Without it nothing is changed.
  -estate=name   The estate whose parameters are swept. Defaults to the
                 live block's estate.
  -json          The report as one JSON document.
`)
}

func (c *LiveSweepSecretsCommand) Synopsis() string {
	return "List or delete orphaned SSM secret parameters"
}
