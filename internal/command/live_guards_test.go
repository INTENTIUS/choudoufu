// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// TestStatelessGuards_escapeHatchesRefused covers the commands that reach a
// state manager without going through plan or apply. Each of them would
// otherwise open a Filesystem state manager in a stateless working directory
// and write the state file the live block says does not exist, so each
// is refused before a backend is prepared.
//
// The check is the same for every row: exit code 1, the command's own summary
// in the output, the sentence that says what to do instead, and no state
// artifact anywhere under the working directory.
func TestStatelessGuards_escapeHatchesRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		// summary is the diagnostic heading the command must produce.
		summary string
		// names is the quoted command name, which the detail has to carry
		// now that the state family shares one summary between them all.
		names string
		// replacement is a fragment of the detail, so that a command wired to
		// the wrong entry in the replacement table is caught here rather than
		// by reading it.
		replacement string
		run         func(m Meta) int
	}{
		{
			name:        "import",
			summary:     "Import is not available under live resource markers",
			names:       `"choudoufu import"`,
			replacement: "live/MARKERS.md",
			run: func(m Meta) int {
				c := &ImportCommand{Meta: m}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data", "tofu-stateless-unit-data"})
			},
		},
		{
			name:        "taint",
			summary:     "Taint is not available under live resource markers",
			names:       `"choudoufu taint"`,
			replacement: `"choudoufu plan -replace=ADDRESS"`,
			run: func(m Meta) int {
				c := &TaintCommand{Meta: m}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data"})
			},
		},
		{
			name:        "untaint",
			summary:     "Untaint is not available under live resource markers",
			names:       `"choudoufu untaint"`,
			replacement: "nothing in a live-markers run can leave an object tainted",
			run: func(m Meta) int {
				c := &UntaintCommand{Meta: m}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data"})
			},
		},
		{
			name:        "state list",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state list"`,
			replacement: "builds the projection from the live system",
			run: func(m Meta) int {
				c := &StateListCommand{Meta: m}
				return c.Run([]string{"-no-color"})
			},
		},
		{
			name:        "state show",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state show"`,
			replacement: "builds the projection from the live system",
			run: func(m Meta) int {
				c := &StateShowCommand{Meta: m}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data"})
			},
		},
		{
			name:        "state mv",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state mv"`,
			replacement: `"choudoufu live-mv OLD NEW"`,
			run: func(m Meta) int {
				c := &StateMvCommand{StateMeta: StateMeta{Meta: m}}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data", "aws_s3_bucket.renamed"})
			},
		},
		{
			name:        "state rm",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state rm"`,
			replacement: "remove its tofu-estate and tofu-address tags",
			run: func(m Meta) int {
				c := &StateRmCommand{StateMeta: StateMeta{Meta: m}}
				return c.Run([]string{"-no-color", "aws_s3_bucket.data"})
			},
		},
		{
			name:        "state pull",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state pull"`,
			replacement: "There is no state to pull",
			run: func(m Meta) int {
				c := &StatePullCommand{Meta: m}
				return c.Run([]string{"-no-color"})
			},
		},
		{
			name:        "state push",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state push"`,
			replacement: "the record it would install is exactly the authority live resource markers remove",
			run: func(m Meta) int {
				c := &StatePushCommand{Meta: m}
				// The source file does not exist: the refusal has to come
				// before the command reads it.
				return c.Run([]string{"-no-color", "pushed.tfstate"})
			},
		},
		{
			name:        "state replace-provider",
			summary:     "Command not available under live resource markers",
			names:       `"choudoufu state replace-provider"`,
			replacement: "derives each resource's provider from the configuration on every run",
			run: func(m Meta) int {
				c := &StateReplaceProviderCommand{StateMeta: StateMeta{Meta: m}}
				return c.Run([]string{"-no-color", "-auto-approve", "hashicorp/aws", "registry.example.com/hashicorp/aws"})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			code := tc.run(liveBlockMeta(view, liveBlockCloud()))
			output := done(t)

			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			// -json sends diagnostics to stdout and everything else to
			// stderr, so both are searched. The renderer wraps the detail to
			// the terminal width, so the search is over the text with its
			// runs of whitespace flattened.
			got := strings.Join(strings.Fields(output.Stderr()+output.Stdout()), " ")
			if !strings.Contains(got, tc.summary) {
				t.Errorf("missing summary %q\nstderr:\n%s\nstdout:\n%s", tc.summary, output.Stderr(), output.Stdout())
			}
			if !strings.Contains(got, tc.names) {
				t.Errorf("the refusal does not name %s\nstderr:\n%s\nstdout:\n%s", tc.names, output.Stderr(), output.Stdout())
			}
			if !strings.Contains(got, tc.replacement) {
				t.Errorf("missing replacement %q\nstderr:\n%s\nstdout:\n%s", tc.replacement, output.Stderr(), output.Stdout())
			}
			assertNoStateArtifacts(t, td)
		})
	}
}

// TestStatelessGuards_escapeHatchesUnguarded is the other half: without a
// live block, none of the guarded commands say anything about stateless
// mode. The fixture has no state file, so each command fails or reports
// nothing for its own ordinary reasons; what matters is which reason.
func TestStatelessGuards_escapeHatchesUnguarded(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(m Meta) int
	}{
		{"state list", func(m Meta) int {
			c := &StateListCommand{Meta: m}
			return c.Run([]string{"-no-color"})
		}},
		{"state pull", func(m Meta) int {
			c := &StatePullCommand{Meta: m}
			return c.Run([]string{"-no-color"})
		}},
		{"untaint", func(m Meta) int {
			c := &UntaintCommand{Meta: m}
			return c.Run([]string{"-no-color", "test_instance.foo"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("apply"), td)
			t.Chdir(td)

			view, done := testView(t)
			tc.run(liveBlockMeta(view, newStatelessTestCloud()))
			output := done(t)

			if strings.Contains(output.Stderr()+output.Stdout(), "live resource markers") {
				t.Errorf("a configuration with no live block was refused:\nstderr:\n%s\nstdout:\n%s", output.Stderr(), output.Stdout())
			}
		})
	}
}
