// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	goPlugin "github.com/hashicorp/go-plugin"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/plugincache"
	"github.com/intentius/choudoufu/internal/logging"
	tfplugin "github.com/intentius/choudoufu/internal/plugin"
	tfplugin6 "github.com/intentius/choudoufu/internal/plugin6"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// acquireSchemas downloads the pinned provider into workdir and reads its
// full GetProviderSchema response by launching the plugin in-process.
//
// This is tools/survey-gen/schemas.go's acquireSchemas, verbatim except for
// its log lines: both tools need the same two-step (terraform init, then
// go-plugin in-process) for the same reason - the required-argument shapes
// this tool renders HCL from live only in the full GetProviderSchema
// response, not in the committed live/survey-full.json artifact, which
// carries identity composition but not each type's block schema (issue
// #56). Duplicated rather than shared because tools/survey-gen is package
// main, same as every other tool in this repository - see that file's own
// comment for why go-plugin in-process is used instead of `providers
// schema -json`.
func acquireSchemas(initBin, workdir string, log io.Writer) (providers.GetProviderSchemaResponse, error) {
	var none providers.GetProviderSchemaResponse

	fixture := fmt.Sprintf(`terraform {
  required_providers {
    aws = {
      source  = %q
      version = "= %s"
    }
  }
}
`, providerSource, providerVersion)
	if err := os.WriteFile(filepath.Join(workdir, "main.tf"), []byte(fixture), 0o600); err != nil {
		return none, err
	}

	args, offline := initArgs(initBin)
	if offline != "" {
		fmt.Fprintf(log, "estate-gen: %s init from the plugin cache %s (%s %s is there; no registry lookup)\n", initBin, offline, providerSource, providerVersion)
	} else {
		fmt.Fprintf(log, "estate-gen: %s init (downloading %s %s: %s does not hold it)\n", initBin, providerSource, providerVersion, plugincache.EnvDir)
	}
	cmd := exec.Command(initBin, args...) //nolint:gosec // caller-provided binary name, see main.go's -init-bin
	cmd.Dir = workdir
	cmd.Env = initEnv(os.Environ(), offline)
	if out, err := cmd.CombinedOutput(); err != nil {
		return none, fmt.Errorf("%s init: %w\n%s", initBin, err, out)
	}

	exe, err := findProviderBinary(workdir)
	if err != nil {
		return none, err
	}
	fmt.Fprintf(log, "estate-gen: launching provider plugin %s\n", exe)

	awsAddr := addrs.NewDefaultProvider("aws")
	lib := plugins.NewLibrary(plugins.ProviderFactories{awsAddr: pluginFactory(exe)}, nil)
	mgr := lib.NewProviderManager()
	defer func() {
		if err := mgr.CloseAll(context.Background()); err != nil {
			fmt.Fprintf(log, "estate-gen: closing the provider: %v\n", err)
		}
		goPlugin.CleanupClients()
	}()

	// The launch is retried a bounded number of times on go-plugin's own
	// transient handshake failure - "Failed to read any lines from
	// plugin's stdout" with no fault in the provider binary itself, a
	// repeat launch of the identical binary the very next moment
	// ordinarily succeeds. internal/live/pluginschema.Acquire hit this
	// first, across corpus-gen's ~75 back-to-back subprocess spawns
	// (issue #222); a single-shot acquisition like this one spawns far
	// fewer subprocesses per run and so hits it far more rarely, but the
	// failure mode is identical and unretried it would surface here as
	// exactly the same silent nondeterminism: two runs over an unchanged
	// tree disagreeing because the schema read had a provider's schema on
	// one run and an error on the other. See
	// tools/survey-gen/schemas.go's copy of this same fix.
	const maxAttempts = 4
	var schema providers.GetProviderSchemaResponse
	var diags tfdiags.Diagnostics
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		schema, diags = mgr.GetProviderSchema(context.Background(), awsAddr)
		if !diags.HasErrors() || !isTransientLaunchError(diags.Err()) {
			break
		}
		fmt.Fprintf(log, "estate-gen: transient launch failure for %s (attempt %d/%d), retrying: %v\n",
			awsAddr, attempt, maxAttempts, diags.Err())
		time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
	}
	if diags.HasErrors() {
		return none, fmt.Errorf("reading the provider schema: %w", explainStartTimeout(diags.Err()))
	}
	fmt.Fprintf(log, "estate-gen: %d resource types\n", len(schema.ResourceTypes))
	return schema, nil
}

// initArgs is the init command line for initBin, and the plugin cache
// directory it installs from when that is offline.
//
// When TF_PLUGIN_CACHE_DIR already holds the pinned release for this
// platform, init runs with -plugin-dir over the cache: every installation
// source is replaced by that one directory, so init asks no registry
// anything. With the cache merely set, init still queries the registry for
// the version list before linking the cached package, which is how
// TestIdentityGolden went red on "lookup registry.terraform.io: no such
// host" with the release sitting in the cache (#1509). The install is a
// symlink to the cached package, the same bytes a registry install unpacks
// (its h1: hash is the one a network install records), so the schema read
// from it - and every file rendered from the schema - is unchanged.
//
// When the cache does not hold it, init runs exactly as before and downloads
// it; with no network that fails, loudly, as init's own error.
func initArgs(initBin string) (args []string, offline string) {
	args = []string{"init", "-backend=false", "-input=false", "-no-color"}
	namespace, typ, _ := strings.Cut(providerSource, "/")
	dir, ok := plugincache.FromEnv(plugincache.DefaultHost(initBin), namespace, typ, providerVersion)
	if !ok {
		return args, ""
	}
	return append(args, "-plugin-dir="+dir), dir
}

// initEnv is init's environment. Installing from the cache with
// -plugin-dir while TF_PLUGIN_CACHE_DIR names that same directory makes
// terraform try to copy the cached package into the cache, and it refuses:
// "cannot install existing provider directory ... to itself". So the
// offline init runs with TF_PLUGIN_CACHE_DIR removed, and the install is a
// symlink straight to the cached package. (With
// TF_PLUGIN_CACHE_MAY_BREAK_DEPENDENCY_LOCK_FILE set, as the test tier sets
// it, terraform happens to take a path that does not hit this; the tool
// must not depend on that.)
func initEnv(environ []string, offline string) []string {
	if offline == "" {
		return environ
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if !strings.HasPrefix(kv, plugincache.EnvDir+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// pluginStartTimeout bounds how long go-plugin waits for the provider to
// print its handshake line, in place of go-plugin's own default of one
// minute.
//
// One minute is what fired in #1509 ("timeout while waiting for plugin to
// start") while seven `just ci` runs shared an 18-core machine. The time is
// not the provider's own startup, which is under 0.1s for an executable the
// machine has run before. It is the first exec of an executable at a new
// path: macOS scans the 812MB hashicorp/aws binary before running it, about
// ten seconds each on an idle machine, and concurrent first execs queue (three
// fresh copies launched together measured 13s, 21s and 29s to the handshake).
// Every render used to download a fresh copy into a temp directory, so every
// render paid that scan and queued behind every other gate's. initArgs
// removes the cause where the cache is warm, since the install is then a
// symlink to one file the machine scans once. This bound covers the cold
// path: five minutes admits about thirty queued first-exec scans at the
// measured rate, which is more than several concurrent gates launch at once,
// and still fails a provider that genuinely never starts well inside go
// test's default ten-minute package timeout.
const pluginStartTimeout = 5 * time.Minute

// explainStartTimeout names machine load in go-plugin's start-timeout error,
// so the failure reads as what it is rather than as a schema or golden
// mismatch further down.
func explainStartTimeout(err error) error {
	if err == nil || !strings.Contains(err.Error(), "timeout while waiting for plugin to start") {
		return err
	}
	return fmt.Errorf("%w (the provider did not complete go-plugin's handshake within %s; that is machine load or a stuck first-exec scan of a freshly downloaded provider, not the tree: see pluginStartTimeout)", err, pluginStartTimeout)
}

// isTransientLaunchError reports whether err is go-plugin's own handshake
// failure signature - "Failed to read any lines from plugin's stdout", with
// no provider name anywhere in the match - rather than a genuine schema
// error the provider itself returned. Copied from
// internal/live/pluginschema.isTransientLaunchError, which this file
// otherwise duplicates in full (see tools/survey-gen/schemas.go's copy of
// this same function); kept in step by hand since collapsing the two is the
// documented follow-up, not this fix.
func isTransientLaunchError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Failed to read any lines from plugin's stdout")
}

// findProviderBinary locates the plugin executable init unpacked under the
// work directory. See tools/survey-gen/schemas.go's copy of this function
// for why the walk follows directory symlinks (a shared TF_PLUGIN_CACHE_DIR
// links rather than unpacks).
func findProviderBinary(dir string) (string, error) {
	var found []string
	root := filepath.Join(dir, ".terraform", "providers")
	err := walkFollowingLinks(root, func(path string, d fs.DirEntry) error {
		if !strings.HasPrefix(d.Name(), "terraform-provider-aws") {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Mode()&0o111 == 0 {
			return nil
		}
		found = append(found, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("looking for the provider plugin under %s: %w", root, err)
	}
	if len(found) == 0 {
		return "", fmt.Errorf("init left no AWS provider plugin under %s", root)
	}
	sort.Strings(found)
	return found[0], nil
}

// walkFollowingLinks walks root depth-first, descending through symlinked
// directories, and calls visit for every non-directory entry.
func walkFollowingLinks(root string, visit func(path string, d fs.DirEntry) error) error {
	seen := map[string]bool{}

	var walk func(dir string) error
	walk = func(dir string) error {
		real, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return err
		}
		if seen[real] {
			return nil
		}
		seen[real] = true

		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			info, err := os.Stat(path)
			if err != nil {
				// A broken link is not this walk's problem to report.
				continue
			}
			if info.IsDir() {
				if err := walk(path); err != nil {
					return err
				}
				continue
			}
			if err := visit(path, e); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root)
}

// pluginFactory is internal/command's providerFactory minus the
// providercache lookup, the same shape tools/survey-gen/schemas.go's copy
// spells out: go-plugin launches the executable and dispenses whichever
// protocol version it negotiated.
func pluginFactory(exe string) providers.Factory {
	schemaCache := providers.NewSchemaCache()

	return func() (providers.Interface, error) {
		config := &goPlugin.ClientConfig{
			HandshakeConfig:  tfplugin.Handshake,
			Logger:           logging.NewProviderLogger(""),
			AllowedProtocols: []goPlugin.Protocol{goPlugin.ProtocolGRPC},
			Managed:          true,
			Cmd:              exec.Command(exe), //nolint:gosec // the path comes from init in a temp dir
			AutoMTLS:         true,
			StartTimeout:     pluginStartTimeout,
			VersionedPlugins: tfplugin.VersionedPlugins,
			SyncStdout:       logging.PluginOutputMonitor("aws:stdout"),
			SyncStderr:       logging.PluginOutputMonitor("aws:stderr"),
		}

		client := goPlugin.NewClient(config)
		rpcClient, err := client.Client()
		if err != nil {
			return nil, err
		}
		raw, err := rpcClient.Dispense(tfplugin.ProviderPluginName)
		if err != nil {
			return nil, err
		}

		switch protoVer := client.NegotiatedVersion(); protoVer {
		case 5:
			p := raw.(*tfplugin.GRPCProvider)
			p.PluginClient = client
			p.SchemaCache = schemaCache
			return p, nil
		case 6:
			p := raw.(*tfplugin6.GRPCProvider)
			p.PluginClient = client
			p.SchemaCache = schemaCache
			return p, nil
		default:
			return nil, fmt.Errorf("the AWS provider negotiated unsupported plugin protocol version %d", protoVer)
		}
	}
}
