// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/workdir"
)

// TestValidate_DirArgumentResolvesItsOwnProviders is GitHub issue #989, the
// third command to carry the defect #973 named: "validate DIR" loaded DIR's
// configuration and then asked the PROCESS working directory which provider
// plugins to validate it against.
//
// The repro is #988's, unchanged: one directory that "choudoufu init" has
// already been run in, read by a process standing somewhere else entirely
// (this package's own directory, which is where "go test" runs it from).
// Before the fix the run failed because hashicorp/simple was nowhere the
// lookup searched; after it, the configuration validates against the
// provider schema installed beside it.
//
// The assertion is the exit code and the absence of a provider-availability
// diagnostic rather than a string match on success, because a validate run
// that finds its providers has nothing to say.
func TestValidate_DirArgumentResolvesItsOwnProviders(t *testing.T) {
	dir := initializedDirWithSimpleProvider(t)

	view, done := testView(t)
	c := &ValidateCommand{Meta: Meta{View: view, WorkingDir: workdir.NewDir(".")}}

	code := c.Run([]string{"-no-color", dir})
	out := done(t)

	if code != 0 {
		t.Errorf("validate %s exited %d, want 0\n"+
			"the plugin lookup did not follow DIR\n--- stdout ---\n%s\n--- stderr ---\n%s",
			dir, code, out.Stdout(), out.Stderr())
	}
	if got := out.Stderr(); strings.Contains(got, "provider registry.opentofu.org/hashicorp/simple") {
		t.Errorf("validate %s complained about the provider that is installed in it:\n%s", dir, got)
	}
}

// TestValidate_DirArgumentWithoutProvidersStillRefuses is the other side of
// the field the test above pins, and the reason that one is a check rather
// than a formality: a "fix" that pointed the lookup at something more
// permissive, or that stopped consulting the lock file at all, would pass
// the test above and fail here.
//
// The directory carries the identical configuration with nothing installed
// beside it, which is the ordinary un-initialized case, and validate has to
// keep saying so.
func TestValidate_DirArgumentWithoutProvidersStillRefuses(t *testing.T) {
	dir := t.TempDir()
	config := `terraform {
  required_providers {
    simple = {
      source = "hashicorp/simple"
    }
  }
}

resource "simple_resource" "example" {
  value = "seven"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(config), 0o600); err != nil {
		t.Fatalf("writing the configuration: %v", err)
	}

	view, done := testView(t)
	c := &ValidateCommand{Meta: Meta{View: view, WorkingDir: workdir.NewDir(".")}}

	code := c.Run([]string{"-no-color", dir})
	out := done(t)

	if code == 0 {
		t.Fatalf("validate %s exited 0 for a directory with no providers installed\n"+
			"--- stdout ---\n%s\n--- stderr ---\n%s", dir, out.Stdout(), out.Stderr())
	}
	if got := out.Stderr(); !strings.Contains(got, "Missing required provider") {
		t.Errorf("stderr does not report the missing provider:\n%s", got)
	}
}
