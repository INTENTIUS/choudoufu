// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/livegraph"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/sweepconfig"
	"github.com/intentius/choudoufu/internal/live/waves"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/pkg/staterecord"
	tfversion "github.com/intentius/choudoufu/version"
)

// LiveGraphCommand prints an estate's graph as one GraphIR version 1
// document in chant#4520's engine graph shape (GitHub issue #2285): block
// nodes, instance nodes, block-grain reference edges and edges to the
// estates it reads, from configuration alone. Live facts come only from a
// live-plan -json document named by -from-plan; -history adds each
// record's versions. It writes nothing, starts no provider, never reads
// the state cache, and prints no value of any resource argument.
type LiveGraphCommand struct {
	Meta

	// open is the record store opener for -history; nil means
	// [projection.NewRecordStore].
	open recordStoreOpener
}

// LiveGraphCommander is live-graph's entry in the new CLI's command tree.
// See [LiveCommanders].
func LiveGraphCommander() Command {
	return liveGraphCommander(nil)
}

func liveGraphCommander(open recordStoreOpener) Command {
	cmd := Command{
		Name:  "live-graph",
		Short: (&LiveGraphCommand{}).Synopsis(),
	}
	args := arguments.BindLiveGraph(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveGraphCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveGraphCommand{Meta: meta, open: open}).Execute(args)
	}
	return cmd
}

func (c *LiveGraphCommand) Run(rawArgs []string) int {
	return RunCommand(liveGraphCommander(c.open), c.Meta, rawArgs)
}

func (c *LiveGraphCommand) Execute(args *arguments.LiveGraph) int {
	ctx := c.CommandContext()
	var diags tfdiags.Diagnostics
	fail := func(summary, detail string) int {
		c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, summary, detail)))
		return 1
	}

	// The static tree: no provider, no init for a local module, nothing
	// read from .terraform but the modules manifest.
	cfg, err := waves.LoadTree(ctx, args.Dir)
	if err != nil {
		return fail("Cannot read the configuration", err.Error())
	}

	estate := args.Estate
	if estate != "" && !discovery.ValidEstateName(estate) {
		return fail("Invalid estate name", fmt.Sprintf("%q does not match the tofu-estate marker grammar in live/MARKERS.md.", estate))
	}
	if estate == "" {
		if e, err := waves.EstateOf(ctx, cfg); err == nil {
			estate = e
		} else {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "No estate name",
				fmt.Sprintf("The configuration %s, so the graph's scope names no estate. Pass -estate=<name> to name one.", err)))
		}
	}

	var plan *livegraph.PlanDoc
	if args.FromPlan != "" {
		raw, err := os.ReadFile(args.FromPlan)
		if err != nil {
			return fail("Cannot read the live plan", err.Error())
		}
		plan = &livegraph.PlanDoc{}
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(plan); err != nil {
			return fail("Cannot read the live plan", fmt.Sprintf("%s is not a live-plan -json document: %s.", args.FromPlan, err))
		}
		switch {
		case estate == "" && plan.Estate != "":
			estate = plan.Estate
		case plan.Estate != "" && plan.Estate != estate:
			return fail("The live plan is of another estate", fmt.Sprintf(
				"%s is a live plan of estate %q, and this graph is of estate %q. Facts from one estate's plan on another's graph would name the wrong owner.", args.FromPlan, plan.Estate, estate))
		}
		if plan.ObservedAt == "" {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, "The live plan does not say when it was read",
				fmt.Sprintf("%s has no observed_at (a live-plan from before #2285), so its facts carry no observedAt.", args.FromPlan)))
		}
	}

	var continuation []string
	for n := 2; n <= markers.MaxContinuations; n++ {
		continuation = append(continuation, markers.ContinuationTag(n))
	}
	built := livegraph.Build(cfg, livegraph.Options{
		Dir:              args.Dir,
		Estate:           estate,
		EngineVersion:    tfversion.Fork,
		Plan:             plan,
		Markers:          sweepconfig.Markers,
		ContinuationKeys: continuation,
	})

	if args.History {
		list, err := c.historyLister(ctx, cfg.Module.Live, estate, args.Dir)
		if err != nil {
			return fail("Cannot read the records' versions", err.Error())
		}
		if err := built.AddHistory(ctx, list); err != nil {
			return fail("Cannot list a record's versions", fmt.Sprintf(
				"%s.\n\nListing a record's versions takes s3:ListBucketVersions on the record store bucket, which the estate's own role does not carry.", err))
		}
	}

	b, err := json.MarshalIndent(built.Doc, "", "  ")
	if err != nil {
		return fail("Cannot render the graph", err.Error())
	}
	// Warnings go to stderr, so stdout always parses (#966's rule).
	c.View.Diagnostics(diags)
	views.NewLiveGraph(c.View).Output(string(b))
	return 0
}

// historyLister opens the record store for -history. Only an s3 store
// keeps versions; for the others every record says none are kept, and
// the store is not opened at all.
func (c *LiveGraphCommand) historyLister(ctx context.Context, live *configs.Live, estate, dir string) (livegraph.HistoryLister, error) {
	if live == nil || live.RecordStore == nil {
		return nil, fmt.Errorf("-history reads the record store the live block's record_store block names, and this configuration has none")
	}
	if estate == "" {
		return nil, fmt.Errorf("a record is kept under its estate's name, and this graph names no estate. Pass -estate=<name>")
	}
	rs := live.RecordStore
	if rs.Type != "s3" {
		return func(context.Context, addrs.AbsResourceInstance) (bool, []livegraph.RecordVersion, error) {
			return false, nil, nil
		}, nil
	}
	open := c.open
	if open == nil {
		open = projection.NewRecordStore
	}
	opts, err := recordStoreOpenOptions()
	if err != nil {
		return nil, err
	}
	store, err := open(ctx, rs, live.Retry, estate, dir, opts...)
	if err != nil {
		return nil, err
	}
	lister, ok := staterecord.AsVersionLister(store)
	if !ok {
		return func(context.Context, addrs.AbsResourceInstance) (bool, []livegraph.RecordVersion, error) {
			return false, nil, nil
		}, nil
	}
	prefix := projection.RecordStoreKeyPrefix(rs, estate)
	return func(ctx context.Context, addr addrs.AbsResourceInstance) (bool, []livegraph.RecordVersion, error) {
		versions, err := lister.ListVersions(ctx, projection.RecordKey(prefix, addr))
		if err != nil {
			return true, nil, err
		}
		out := make([]livegraph.RecordVersion, 0, len(versions))
		for _, v := range versions {
			out = append(out, livegraph.RecordVersion{
				ID:        v.VersionID,
				WrittenAt: v.LastModified.UTC().Format(time.RFC3339),
				Current:   v.Current,
				Deleted:   v.Deleted,
			})
		}
		return true, out, nil
	}, nil
}

func (c *LiveGraphCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-graph [options] [DIR] -json

  Prints the estate in DIR (default ".") as one JSON document: a chant
  GraphIR, version 1, in the engine graph shape of INTENTIUS/chant#4520,
  which chant's terraform lexicon and behold's graph pane read.

  Nodes are the configuration's resource and data blocks, keyed by
  address, plus one node per instance that a literal count or for_each,
  or the live plan, names. Edges are the references between blocks, found
  by reading the configuration without any provider schema: every edge
  has grain "block", and an instance's edge points at the block it reads.
  A data block that reads another estate (terraform_estate_outputs, a
  tag:tofu-estate filter or tag) has an edge to that estate's one node.
  A block the read cannot describe (a .tf.json file) is listed under
  meta.engineGraph.gaps.

  Without -from-plan nothing live is read: no provider starts and no
  cloud is called. Nothing is ever written, and the state cache is never
  read. No resource argument's value is printed.

Options:

  -json            Required. The graph as one JSON document; warnings go
                   to stderr.
  -from-plan=file  A "choudoufu live-plan -json" document of this estate.
                   Its rows become each node's facts: ownership, markers,
                   ownedBy, the import id, observedAt.
  -history         Add each record's versions (ids and times, never
                   values), read from the record store. Takes
                   s3:ListBucketVersions.
  -estate=name     The estate the graph is of. Defaults to the live
                   block's, or the one tofu-estate value the
                   configuration stamps.
`)
}

func (c *LiveGraphCommand) Synopsis() string {
	return "Print the estate's graph as one JSON document"
}
