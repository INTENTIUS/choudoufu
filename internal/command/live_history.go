// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// LiveHistoryCommand lists the past versions of one resource instance's
// record, newest first, from the record store bucket's noncurrent versions
// (GitHub issue #1954, part of #834). It prints when each version was
// written and never what it held. The local and Kubernetes stores replace a
// record in place, so for them it says that no past versions are kept.
type LiveHistoryCommand struct {
	Meta

	// open is the record store opener; nil means [projection.NewRecordStore].
	open recordStoreOpener
}

// LiveHistoryCommander is live-history's entry in the new CLI's command
// tree. See [LiveCommanders].
func LiveHistoryCommander() Command {
	return liveHistoryCommander(nil)
}

func liveHistoryCommander(open recordStoreOpener) Command {
	cmd := Command{
		Name:  "live-history",
		Short: (&LiveHistoryCommand{}).Synopsis(),
	}
	args := arguments.BindLiveHistory(&cmd.CommandLine)
	applyLegacyHelp(&cmd, (&LiveHistoryCommand{}).Help())
	cmd.Run = func(meta Meta) int {
		return (&LiveHistoryCommand{Meta: meta, open: open}).Execute(args)
	}
	return cmd
}

func (c *LiveHistoryCommand) Run(rawArgs []string) int {
	return RunCommand(liveHistoryCommander(c.open), c.Meta, rawArgs)
}

// liveHistory is the -json document. Kept is false for a store that keeps
// no past versions, and Versions is then empty.
type liveHistory struct {
	Address  string               `json:"address"`
	Estate   string               `json:"estate"`
	Store    string               `json:"store"`
	Kept     bool                 `json:"kept"`
	Versions []liveHistoryVersion `json:"versions"`
}

type liveHistoryVersion struct {
	VersionID    string `json:"version_id"`
	LastModified string `json:"last_modified"`
	Current      bool   `json:"current"`
	Deleted      bool   `json:"deleted"`
}

func (c *LiveHistoryCommand) Execute(args *arguments.LiveHistory) int {
	ctx := c.CommandContext()
	var diags tfdiags.Diagnostics

	addr, addrDiags := addrs.ParseAbsResourceInstanceStr(args.RawAddr)
	diags = diags.Append(addrDiags)
	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}

	config, cfgDiags := c.loadConfig(ctx, ".")
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	if config.Module == nil || config.Module.Live == nil || config.Module.Live.RecordStore == nil {
		c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "No record store",
			"live-history reads the record store the live block's record_store block names, and this configuration has none.")))
		return 1
	}
	live := config.Module.Live
	rs := live.RecordStore

	estate, _, estateDiags := liveEstateFor(ctx, args.Estate, config)
	diags = diags.Append(estateDiags)
	if estate == "" && !estateDiags.HasErrors() && live.Estate != "" {
		if !discovery.ValidEstateName(live.Estate) {
			estateDiags = estateDiags.Append(tfdiags.Sourceless(tfdiags.Error, "Invalid estate name",
				fmt.Sprintf("The live block names estate %q, which does not match the tofu-estate marker grammar in live/MARKERS.md.", live.Estate)))
			diags = diags.Append(estateDiags)
		} else {
			estate = live.Estate
		}
	}
	if estateDiags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	if estate == "" {
		c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "No estate name",
			"A record is kept under its estate's name, and this configuration names no single estate. Pass -estate=<name>.")))
		return 1
	}

	h := liveHistory{Address: addr.String(), Estate: estate, Store: rs.Type, Versions: []liveHistoryVersion{}}
	if rs.Type == "s3" {
		open := c.open
		if open == nil {
			open = projection.NewRecordStore
		}
		storeOpts, err := recordStoreOpenOptions()
		if err != nil {
			c.View.Diagnostics(diags.Append(recordStoreOpenDiag(rs.Type, err)))
			return 1
		}
		store, err := open(ctx, rs, live.Retry, estate, ".", storeOpts...)
		if err != nil {
			c.View.Diagnostics(diags.Append(recordStoreOpenDiag(rs.Type, err)))
			return 1
		}
		if lister, ok := staterecord.AsVersionLister(store); ok {
			key := projection.RecordKey(projection.RecordStoreKeyPrefix(rs, estate), addr)
			versions, err := lister.ListVersions(ctx, key)
			if err != nil {
				c.View.Diagnostics(diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot list the record's versions", fmt.Sprintf(
					"%s.\n\nListing a record's versions takes s3:ListBucketVersions on the record store bucket, which the estate's own role does not carry. It is one of the grants a recovery needs: see \"What a recovery needs\" on the bucket page.", err))))
				return 1
			}
			h.Kept = true
			for _, v := range versions {
				h.Versions = append(h.Versions, liveHistoryVersion{
					VersionID:    v.VersionID,
					LastModified: v.LastModified.UTC().Format(time.RFC3339),
					Current:      v.Current,
					Deleted:      v.Deleted,
				})
			}
		}
	}

	if diags.HasErrors() {
		c.View.Diagnostics(diags)
		return 1
	}
	c.View.Diagnostics(diags)

	var out string
	if args.JSON {
		b, err := json.MarshalIndent(h, "", "  ")
		if err != nil {
			c.View.Diagnostics(tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot render the history", err.Error())))
			return 1
		}
		out = string(b)
	} else {
		out = h.text()
	}
	views.NewLiveHistory(c.View).Output(out)
	return 0
}

func (h liveHistory) text() string {
	if !h.Kept {
		return fmt.Sprintf("The %s record store keeps no past versions: it replaces a record in place, so only the current one exists. An s3 record store keeps them as the bucket's noncurrent versions.", h.Store)
	}
	if len(h.Versions) == 0 {
		return fmt.Sprintf("%s has no record in estate %s's record store, current or past.", h.Address, h.Estate)
	}
	var b strings.Builder
	noun := "versions"
	if len(h.Versions) == 1 {
		noun = "version"
	}
	fmt.Fprintf(&b, "%s: %d %s of its record, newest first\n", h.Address, len(h.Versions), noun)
	for _, v := range h.Versions {
		state := ""
		switch {
		case v.Deleted:
			state = "  deleted"
		case v.Current:
			state = "  current"
		}
		fmt.Fprintf(&b, "  %s  %s%s\n", v.LastModified, v.VersionID, state)
	}
	return b.String()
}

func (c *LiveHistoryCommand) Help() string {
	return strings.TrimSpace(`
Usage: choudoufu [global options] live-history [options] ADDRESS

  Lists the versions of one resource instance's record, newest first: when
  each was written, its version id, and whether it is the current one or a
  delete marker. It never prints what a version holds. Reading one is
  aws s3api get-object --version-id, with s3:GetObjectVersion.

  The versions are the record store bucket's own: the bucket contract
  refuses a bucket without versioning, so every write and delete leaves the
  one before it as a noncurrent version until the bucket's lifecycle
  expires it. Listing them takes s3:ListBucketVersions, which the estate's
  own role does not carry.

  A local or kubernetes record store replaces a record in place and keeps
  no past versions, and live-history says so.

Options:

  -estate=name   The estate whose record store is read. Defaults to the
                 live block's estate.
  -json          The history as one JSON document.
`)
}

func (c *LiveHistoryCommand) Synopsis() string {
	return "List the past versions of a resource's record"
}
