// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
)

// LiveCommanders are this fork's eight live-* commands, as entries in the
// new CLI's command tree. [RootCommander] appends them after the stock
// commands; the legacy CLI's command map (cmd/choudoufu/commands.go) reaches
// the same Execute methods through each command's Run.
//
// OpenTofu v1.13.0 made the urfave/cli tree the default CLI. That tree is
// static and built here, not from the legacy map, so a command registered
// only in the map is "no command named" under the default CLI. The test
// TestNewCLIDispatchesLiveCommands walks RootCommander and fails if any of
// the eight is missing.
func LiveCommanders() []Command {
	return []Command{
		LiveBucketCommander(),
		LiveCheckCommander(),
		LiveClusterCommander(),
		LiveImportCommander(),
		LiveLsCommander(),
		LiveMvCommander(),
		LivePlanCommander(),
		LiveWavesCommander(),
	}
}

// applyLegacyHelp makes a live-* command's help under the new CLI the same
// text its Help method gives the legacy CLI, so that there is one copy of it
// to keep true.
//
// The stock commands' Commanders carry a short Long description and let
// [CommandUsage] list each flag from its own usage string. The live-*
// commands' help is longer and structured differently - an Options section
// whose entries run to a paragraph, then environment variables and the
// options a command rejects - and none of it fits a one-line flag usage. So
// the Usage line becomes the usage override, the rest becomes Long, and the
// flags are hidden from the generated Options list because Long already
// documents them. Hidden flags still parse and still complete.
//
// It must run after the command's options are bound: hiding is per flag.
func applyLegacyHelp(cmd *Command, help string) {
	lines := strings.Split(strings.TrimSpace(help), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "Usage: ") {
		cmd.UsageOverride.Usage = strings.TrimPrefix(lines[0], "Usage: ")
		lines = lines[1:]
	}
	// The legacy text is indented two spaces, and CommandUsage indents Long
	// by two spaces of its own.
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, "  ")
	}
	cmd.Long = strings.TrimSpace(strings.Join(lines, "\n"))
	for _, flag := range cmd.CommandLine.Flags {
		flag.SetHidden(true)
	}
}
