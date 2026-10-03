// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/terminal"
)

// liveCommandNames are this fork's nine live-* commands. Under OpenTofu
// v1.13.0 the default CLI is commandMain's urfave/cli tree, built from
// command.RootCommander rather than from the legacy commands map, so each
// has to be reachable there as well as in initCommands.
var liveCommandNames = []string{"live-check", "live-plan", "live-mv", "live-import", "live-ls", "live-bucket", "live-cluster", "live-summary", "live-plan-set"}

// TestCommandMainRoutesLiveCommands is the routing half of GitHub issue
// #1778's step 3 test (internal/command's TestNewCLIDispatchesLiveCommands
// is the execution half, with test providers this package cannot inject).
// It goes through commandMain's own pieces: detectSubcommand, which decides
// which command's environment arguments apply, and commandToCli, which
// builds the urfave/cli tree commandMain runs. A name missing from
// RootCommander is the root command with one leftover argument here, which
// commandMain answers with "no command named" and os.Exit, so the routing is
// asserted before anything is run.
func TestCommandMainRoutesLiveCommands(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })

	for _, name := range liveCommandNames {
		var help, version bool
		var chdir string
		os.Args = []string{"choudoufu", name}
		if got := detectSubcommand(command.RootCommander(&help, &version, &chdir)); got != name {
			t.Errorf("the new CLI routes %q to %q, so the default CLI cannot run it", name, got)
		}
	}

	// The legacy CLI (TOFU_EXPERIMENTAL_CLI_ENABLED=false) is v1.13.x's
	// fallback and keeps its own map.
	oldCommands := commands
	t.Cleanup(func() { commands = oldCommands })
	initCommands(command.Meta{})
	for _, name := range liveCommandNames {
		if _, ok := commands[name]; !ok {
			t.Errorf("%s is missing from the legacy CLI's command map", name)
		}
	}
}

// TestCommandMainRunsLiveCommands runs commands end to end through
// commandToCli, the function commandMain hands os.Args to, over the
// live-block fixture: live-check -json, which needs neither a cloud nor a
// provider, and force-unlock, which statelessCommandGuard must refuse before
// any backend is opened. Neither path consults the legacy commands map.
func TestCommandMainRunsLiveCommands(t *testing.T) {
	fixture, err := filepath.Abs(filepath.Join("..", "..", "internal", "command", "testdata", "live-block"))
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, args ...string) (int, *terminal.TestOutput) {
		t.Helper()
		td := t.TempDir()
		data, err := os.ReadFile(filepath.Join(fixture, "main.tf"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(td, "main.tf"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Chdir(td)

		streams, done := terminal.StreamsForTesting(t)
		meta := func() (command.Meta, int) {
			return command.Meta{
				WorkingDir: workdir.NewDir("."),
				View:       views.NewView(streams),
			}, 0
		}

		var help, version bool
		var chdir string
		root := commandToCli("", command.RootCommander(&help, &version, &chdir), meta)
		runErr := root.Run(context.Background(), append([]string{"choudoufu"}, args...))
		output := done(t)
		if runErr == nil {
			return 0, output
		}
		if code, ok := runErr.(ExitCodeError); ok {
			return int(code), output
		}
		t.Fatalf("running %v: %s", args, runErr)
		return 0, nil
	}

	t.Run("live-check -json runs", func(t *testing.T) {
		code, output := run(t, "live-check", "-json")
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if !strings.Contains(output.Stdout(), `"schemas"`) {
			t.Errorf("live-check -json did not print its roster document:\n%s", output.Stdout())
		}
	})

	t.Run("force-unlock is refused under a live block", func(t *testing.T) {
		code, output := run(t, "force-unlock", "-no-color", "-force", "some-lock-id")
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if got := output.Stderr(); !strings.Contains(got, "There is no lock to force open") {
			t.Errorf("force-unlock was not refused by the live-markers guard:\n%s", got)
		}
	})
}
