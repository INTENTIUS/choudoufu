// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"
)

// dispatchNewCLI runs a command line through the new CLI's command tree the
// way cmd/choudoufu's commandToCli does: the subcommand is looked up in
// [RootCommander] by name or alias, its options are parsed by urfave/cli
// through the command's own CommandLine ([arguments.CommandLine.ParseDirect]
// builds the same cli.Command flags and arguments commandToCli does), and
// the result goes through [RunCli]. Nothing here consults the legacy
// command map or a command's Run method, so a command reachable only that
// way is reported missing.
//
// It stops short of commandToCli itself because that lives in package main
// and cannot be handed a Meta carrying test providers; the routing half is
// pinned there, in cmd/choudoufu's TestCommandMainRoutesLiveCommands.
func dispatchNewCLI(t *testing.T, meta Meta, args ...string) int {
	t.Helper()
	var help, version bool
	var chdir string
	cmd := RootCommander(&help, &version, &chdir)

	namespace := ""
	for len(args) > 0 {
		var next *Command
		for i := range cmd.Commands {
			sub := &cmd.Commands[i]
			if sub.Name == args[0] {
				next = sub
				break
			}
			for _, alias := range sub.Aliases {
				if alias == args[0] {
					next = sub
				}
			}
		}
		if next == nil {
			break
		}
		if cmd.Name != "" {
			namespace += cmd.Name + " "
		}
		cmd = *next
		args = args[1:]
	}
	if cmd.Name == "" {
		t.Fatalf("the new CLI's command tree has no command named %q", args)
	}
	if cmd.Run == nil {
		t.Fatalf("%q is a command group in the new CLI, not a command", cmd.Name)
	}

	diags := cmd.CommandLine.ParseDirect(context.Background(), args)
	return RunCli(namespace, cmd, meta, diags)
}

// TestNewCLIDispatchesLiveCommands is GitHub issue #1778's step 3 test.
// OpenTofu v1.13.0 made the urfave/cli tree in [RootCommander] the default
// CLI, and that tree is built in this package rather than read from
// cmd/choudoufu's legacy command map. Before the port every live-* command
// was "no command named" under the default CLI, -verbose and -adoption-only
// were not flags of "plan", and the state family's and force-unlock's
// guards had been dropped by the merge. Each half below fails on that tree.
func TestNewCLIDispatchesLiveCommands(t *testing.T) {
	t.Run("all eight live commands are in the tree", func(t *testing.T) {
		var help, version bool
		var chdir string
		root := RootCommander(&help, &version, &chdir)
		have := map[string]bool{}
		for _, cmd := range root.Commands {
			have[cmd.Name] = cmd.Run != nil
		}
		for _, name := range []string{"live-check", "live-plan", "live-mv", "live-import", "live-ls", "live-bucket", "live-cluster", "live-plan-set"} {
			if !have[name] {
				t.Errorf("%s is not a runnable command in RootCommander, so the default CLI cannot reach it", name)
			}
		}
	})

	t.Run("live-plan runs", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		cloud := liveBlockCloud()
		view, done := testView(t)
		code := dispatchNewCLI(t, liveBlockMeta(view, cloud), "live-plan", "-no-color")
		output := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if !strings.Contains(output.Stdout(), "No changes.") {
			t.Errorf("live-plan over an applied estate did not plan empty:\n%s", output.Stdout())
		}
		if !cloud.imported("aws_vpc", "vpc-owned") {
			t.Errorf("live-plan never read the VPC from the live system; imports were %v", cloud.imports)
		}
		assertNoStateArtifacts(t, td)
	})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"plan -adoption-only", []string{"plan", "-no-color", "-adoption-only"}, "declared resource instances"},
		{"plan -verbose", []string{"plan", "-no-color", "-verbose"}, "No changes."},
		{"plan -filter", []string{"plan", "-no-color", "-filter=unowned"}, "No changes."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			code := dispatchNewCLI(t, liveBlockMeta(view, liveBlockCloud()), tc.args...)
			output := done(t)
			if code != 0 {
				t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			if !strings.Contains(output.Stdout(), tc.want) {
				t.Errorf("output does not contain %q:\n%s", tc.want, output.Stdout())
			}
		})
	}

	for _, tc := range []struct {
		name    string
		args    []string
		summary string
		names   string
	}{
		{"force-unlock", []string{"force-unlock", "-no-color", "-force", "some-lock-id"}, "There is no lock to force open", `"choudoufu force-unlock"`},
		{"state list", []string{"state", "list", "-no-color"}, "Command not available under live resource markers", `"choudoufu state list"`},
		{"state ls alias", []string{"state", "ls", "-no-color"}, "Command not available under live resource markers", `"choudoufu state list"`},
		{"state pull", []string{"state", "pull", "-no-color"}, "Command not available under live resource markers", `"choudoufu state pull"`},
		{"state push", []string{"state", "push", "-no-color", "pushed.tfstate"}, "Command not available under live resource markers", `"choudoufu state push"`},
		{"state rm", []string{"state", "rm", "-no-color", "aws_s3_bucket.data"}, "Command not available under live resource markers", `"choudoufu state rm"`},
		{"state show", []string{"state", "show", "-no-color", "aws_s3_bucket.data"}, "Command not available under live resource markers", `"choudoufu state show"`},
		{"state replace-provider", []string{"state", "replace-provider", "-no-color", "-auto-approve", "hashicorp/aws", "registry.example.com/hashicorp/aws"}, "Command not available under live resource markers", `"choudoufu state replace-provider"`},
		{"state mv", []string{"state", "mv", "-no-color", "aws_s3_bucket.data", "aws_s3_bucket.renamed"}, "Command not available under live resource markers", `"choudoufu state mv"`},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			code := dispatchNewCLI(t, liveBlockMeta(view, liveBlockCloud()), tc.args...)
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			got := strings.Join(strings.Fields(output.Stderr()+output.Stdout()), " ")
			if !strings.Contains(got, tc.summary) || !strings.Contains(got, tc.names) {
				t.Errorf("not refused by the live-markers guard: want %q naming %s\nstderr:\n%s\nstdout:\n%s", tc.summary, tc.names, output.Stderr(), output.Stdout())
			}
			assertNoStateArtifacts(t, td)
		})
	}
}
