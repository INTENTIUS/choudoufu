// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/mitchellh/cli"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/depsfile"
	"github.com/intentius/choudoufu/internal/getproviders"
	tfversion "github.com/intentius/choudoufu/version"
)

func TestVersionCommand_implements(t *testing.T) {
	var _ cli.Command = &VersionCommand{}
}

func TestVersion(t *testing.T) {
	td := t.TempDir()
	t.Chdir(td)

	// We'll create a fixed dependency lock file in our working directory
	// so we can verify that the version command shows the information
	// from it.
	locks := depsfile.NewLocks()
	locks.SetProvider(
		addrs.NewDefaultProvider("test2"),
		getproviders.MustParseVersion("1.2.3"),
		nil,
		nil,
	)
	locks.SetProvider(
		addrs.NewDefaultProvider("test1"),
		getproviders.MustParseVersion("7.8.9-beta.2"),
		nil,
		nil,
	)

	view, done := testView(t)
	c := &VersionCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
		},
		Version:           "4.5.6",
		VersionPrerelease: "foo",
		Platform:          getproviders.Platform{OS: "aros", Arch: "riscv64"},
	}
	if err := c.replaceLockedDependencies(context.Background(), locks); err != nil {
		t.Fatal(err)
	}
	code := c.Run([]string{})
	output := done(t)
	if code != 0 {
		t.Fatalf("bad: \n%s", output.Stderr())
	}

	actual := strings.TrimSpace(output.Stdout())
	expected := "OpenTofu v4.5.6-foo\non aros_riscv64\n+ provider registry.opentofu.org/hashicorp/test1 v7.8.9-beta.2\n+ provider registry.opentofu.org/hashicorp/test2 v1.2.3"
	if actual != expected {
		t.Fatalf("wrong output\ngot:\n%s\nwant:\n%s", actual, expected)
	}

}

func TestVersion_flags(t *testing.T) {
	view, done := testView(t)
	m := Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
	}

	// `tofu version`
	c := &VersionCommand{
		Meta:              m,
		Version:           "4.5.6",
		VersionPrerelease: "foo",
		Platform:          getproviders.Platform{OS: "aros", Arch: "riscv64"},
	}

	code := c.Run([]string{"-v", "-version"})
	output := done(t)
	if code != 0 {
		t.Fatalf("bad: \n%s", output.Stderr())
	}

	actual := strings.TrimSpace(output.Stdout())
	expected := "OpenTofu v4.5.6-foo\non aros_riscv64"
	if actual != expected {
		t.Fatalf("wrong output\ngot: %#v\nwant: %#v", actual, expected)
	}
}

// TestVersion_jsonFork is TestVersion_json on a release-shaped build, where
// the linker has set tfversion.Fork to the release tag. The two documents
// TestVersion_json asserts are both development builds and so both carry
// "choudoufu_version" empty; this one proves the command actually passes the
// tag through to the rendered document rather than the field being
// unconditionally blank (#968).
func TestVersion_jsonFork(t *testing.T) {
	td := t.TempDir()
	t.Chdir(td)

	prior := tfversion.Fork
	t.Cleanup(func() { tfversion.Fork = prior })
	tfversion.Fork = "v0.15.0"

	view, done := testView(t)
	c := &VersionCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
		},
		Version:  "4.5.6",
		Platform: getproviders.Platform{OS: "aros", Arch: "riscv64"},
	}
	code := c.Run([]string{"-json"})
	output := done(t)
	if code != 0 {
		t.Fatalf("bad: \n%s", output.Stderr())
	}

	expected := strings.TrimSpace(`
{
  "choudoufu_version": "v0.15.0",
  "terraform_version": "4.5.6",
  "platform": "aros_riscv64",
  "provider_selections": {}
}
`)
	if diff := cmp.Diff(expected, strings.TrimSpace(output.Stdout())); diff != "" {
		t.Fatalf("wrong output\n%s", diff)
	}
}

func TestVersion_json(t *testing.T) {
	td := t.TempDir()
	t.Chdir(td)

	view, done := testView(t)
	meta := Meta{
		WorkingDir: workdir.NewDir("."),
		View:       view,
	}

	// `tofu version -json` without prerelease
	c := &VersionCommand{
		Meta:     meta,
		Version:  "4.5.6",
		Platform: getproviders.Platform{OS: "aros", Arch: "riscv64"},
	}
	code := c.Run([]string{"-json"})
	output := done(t)
	if code != 0 {
		t.Fatalf("bad: \n%s", output.Stderr())
	}

	actual := strings.TrimSpace(output.Stdout())
	expected := strings.TrimSpace(`
{
  "choudoufu_version": "",
  "terraform_version": "4.5.6",
  "platform": "aros_riscv64",
  "provider_selections": {}
}
`)
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Fatalf("wrong output\n%s", diff)
	}

	// reset view
	view, done = testView(t)
	meta.View = view

	// Now we'll create a fixed dependency lock file in our working directory
	// so we can verify that the version command shows the information
	// from it.
	locks := depsfile.NewLocks()
	locks.SetProvider(
		addrs.NewDefaultProvider("test2"),
		getproviders.MustParseVersion("1.2.3"),
		nil,
		nil,
	)
	locks.SetProvider(
		addrs.NewDefaultProvider("test1"),
		getproviders.MustParseVersion("7.8.9-beta.2"),
		nil,
		nil,
	)

	// `tofu version -json` with prerelease and provider dependencies
	c = &VersionCommand{
		Meta:              meta,
		Version:           "4.5.6",
		VersionPrerelease: "foo",
		Platform:          getproviders.Platform{OS: "aros", Arch: "riscv64"},
	}
	if err := c.replaceLockedDependencies(context.Background(), locks); err != nil {
		t.Fatal(err)
	}
	code = c.Run([]string{"-json"})
	output = done(t)
	if code != 0 {
		t.Fatalf("bad: \n%s", output.Stderr())
	}

	actual = strings.TrimSpace(output.Stdout())
	expected = strings.TrimSpace(`
{
  "choudoufu_version": "",
  "terraform_version": "4.5.6-foo",
  "platform": "aros_riscv64",
  "provider_selections": {
    "registry.opentofu.org/hashicorp/test1": "7.8.9-beta.2",
    "registry.opentofu.org/hashicorp/test2": "1.2.3"
  }
}
`)
	if diff := cmp.Diff(expected, actual); diff != "" {
		t.Fatalf("wrong output\n%s", diff)
	}
}
