// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/getproviders"
)

// GitHub issue #973: live-check and live-ls both take a DIR argument
// precisely so they can be pointed at a repository the caller is not
// standing in, and both resolved everything EXCEPT the provider plugins
// against it. The plugin lookup came from Meta.WorkingDir - the process
// working directory - so "choudoufu init" in DIR followed by a read of DIR
// from anywhere else got the degraded, built-in-table answer, which #966's
// "schemas" field now reports as "builtin" while naming a remedy the caller
// has already followed.
//
// The tests below are the issue's own repro: one directory that has been
// initialized, read from a process standing somewhere else entirely (this
// package's own directory, which is where "go test" runs it from).

// installSimpleProviderIn makes dir look to [Meta.providerFactories]
// exactly like a directory "choudoufu init" has already run in: a
// dependency lock file naming one provider, and that provider's package
// unpacked in dir's own provider cache.
//
// The package is internal/provider-simple, compiled here rather than
// downloaded, so this needs no network, no registry and no credentials -
// and, more importantly, the provider it installs is a real plugin process
// that serves a real schema, which is the only thing that can move the
// "schemas" field to "provider". A stub file would satisfy the cache
// lookup and then fail to launch, which is the same answer as no provider
// at all.
//
// The lock file carries no "hashes" block on purpose: with no preferred
// hashes recorded, providerFactories skips package verification, which is
// what lets a locally-built binary stand in for a released package.
func installSimpleProviderIn(t *testing.T, dir string) addrs.Provider {
	t.Helper()

	provider := addrs.NewDefaultProvider("simple")
	version := getproviders.MustParseVersion("1.0.0")

	pkgDir := getproviders.UnpackedDirectoryPathForPackage(
		filepath.Join(dir, ".terraform", "providers"),
		provider, version, getproviders.CurrentPlatform,
	)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("creating the provider cache directory: %v", err)
	}

	exe := filepath.Join(pkgDir, "terraform-provider-simple")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	absExe, err := filepath.Abs(exe)
	if err != nil {
		t.Fatalf("resolving the provider executable path: %v", err)
	}
	build := exec.Command("go", "build", "-o", absExe, "github.com/intentius/choudoufu/internal/provider-simple/main")
	// The module root, relative to this package's directory, which is
	// where "go test" runs. PWD is dropped because the go command reads it
	// when it is set and this process never chdir'd to it.
	build.Dir = filepath.Join("..", "..")
	build.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return len(kv) >= 4 && kv[:4] == "PWD="
	})
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the test provider: %v\n%s", err, out)
	}

	lock := fmt.Sprintf(`# This file is maintained automatically by "choudoufu init".
provider %q {
  version = %q
}
`, provider.String(), version.String())
	if err := os.WriteFile(filepath.Join(dir, ".terraform.lock.hcl"), []byte(lock), 0o644); err != nil {
		t.Fatalf("writing .terraform.lock.hcl: %v", err)
	}

	return provider
}

// initializedDirWithSimpleProvider returns a directory carrying one
// configuration that requires the test provider, with that provider
// installed in the directory's own cache. Nothing in it refers to the
// process working directory, which is the whole point.
func initializedDirWithSimpleProvider(t *testing.T) string {
	t.Helper()

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
	installSimpleProviderIn(t, dir)
	return dir
}

// TestLiveCheckJSON_InitializedDirectorySaysProvider is issue #973's repro
// for live-check: DIR is initialized, the process working directory is not,
// and the answer has to come from DIR.
//
// Its counterpart is TestLiveCheckJSON_UninitializedDirectorySaysBuiltin,
// which pins the other side of the same field - so a fix that simply always
// reported "provider" would be caught there rather than here.
func TestLiveCheckJSON_InitializedDirectorySaysProvider(t *testing.T) {
	dir := initializedDirWithSimpleProvider(t)

	c, done := newLiveCheckCommand(t)
	c.Run([]string{"-json", dir})
	out := done(t)

	var doc liveCheckJSONDoc
	if err := json.Unmarshal([]byte(out.Stdout()), &doc); err != nil {
		t.Fatalf("stdout is not valid JSON: %s\nstdout:\n%s", err, out.Stdout())
	}
	if doc.Schemas != "provider" {
		t.Errorf("schemas = %q for a directory that carries an installed provider, want \"provider\"\n"+
			"the plugin lookup did not follow DIR\n--- stdout ---\n%s\n--- stderr ---\n%s",
			doc.Schemas, out.Stdout(), out.Stderr())
	}
}

// TestLiveLsCommand_Run_json_initializedDirectorySaysProvider is the same
// repro for live-ls, whose comparison reads schemas through the identical
// path.
//
// It asserts only the schema source, not the gap list: what this
// configuration's one resource type resolves to is a separate question,
// and the defect #973 reports is entirely about which library the schema
// came from.
func TestLiveLsCommand_Run_json_initializedDirectorySaysProvider(t *testing.T) {
	dir := initializedDirWithSimpleProvider(t)

	doc, stdout, stderr := liveLsJSONRun(t, dir)

	if doc.Schemas != "provider" {
		t.Errorf("schemas = %q for a directory that carries an installed provider, want \"provider\"\n"+
			"the plugin lookup did not follow DIR\n--- stdout ---\n%s\n--- stderr ---\n%s",
			doc.Schemas, stdout, stderr)
	}
}
