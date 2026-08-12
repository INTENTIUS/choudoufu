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

	goPlugin "github.com/hashicorp/go-plugin"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/logging"
	tfplugin "github.com/opentofu/opentofu/internal/plugin"
	tfplugin6 "github.com/opentofu/opentofu/internal/plugin6"
	"github.com/opentofu/opentofu/internal/plugins"
	"github.com/opentofu/opentofu/internal/providers"
)

// acquireSchemas downloads the pinned provider into workdir and reads its
// full GetProviderSchema response by launching the plugin in-process.
//
// This is the same two-step the gated test tier uses (see
// internal/stateless/stamp's TestTaggableSetAgainstRealSchemas for the init
// half and internal/stateless/projection's floci test for the go-plugin
// half): `terraform init` in a directory that requires the pinned release,
// then go-plugin launches the executable init unpacked. Going in-process
// rather than shelling out to `providers schema -json` is deliberate - the
// JSON dump carries resource schemas and resource_identity_schemas but no
// list-resource section (SURVEY.md's 2026-08-12 re-run notes exactly that
// gap), while the GetProviderSchema response carries all three. The provider
// is never configured: a schema read needs no cloud and no credentials.
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

	fmt.Fprintf(log, "survey-gen: %s init (downloading %s %s if not cached)\n", initBin, providerSource, providerVersion)
	cmd := exec.Command(initBin, "init", "-backend=false", "-input=false", "-no-color")
	cmd.Dir = workdir
	if out, err := cmd.CombinedOutput(); err != nil {
		return none, fmt.Errorf("%s init: %w\n%s", initBin, err, out)
	}

	exe, err := findProviderBinary(workdir)
	if err != nil {
		return none, err
	}
	fmt.Fprintf(log, "survey-gen: launching provider plugin %s\n", exe)

	awsAddr := addrs.NewDefaultProvider("aws")
	lib := plugins.NewLibrary(plugins.ProviderFactories{awsAddr: pluginFactory(exe)}, nil)
	mgr := lib.NewProviderManager()
	defer func() {
		if err := mgr.CloseAll(context.Background()); err != nil {
			fmt.Fprintf(log, "survey-gen: closing the provider: %v\n", err)
		}
		goPlugin.CleanupClients()
	}()

	schema, diags := mgr.GetProviderSchema(context.Background(), awsAddr)
	if diags.HasErrors() {
		return none, fmt.Errorf("reading the provider schema: %w", diags.Err())
	}
	fmt.Fprintf(log, "survey-gen: %d resource types, %d list resource types\n",
		len(schema.ResourceTypes), len(schema.ListResourceTypes))
	return schema, nil
}

// findProviderBinary locates the plugin executable init unpacked under the
// work directory, exactly as the projection floci test does.
//
// The walk follows directory symlinks, which matters for one case worth
// stating: with TF_PLUGIN_CACHE_DIR set - the ordinary way to keep the
// provider download off a run's critical path - terraform does not unpack
// anything here at all. It links the version directory to the shared cache,
// and filepath.WalkDir does not descend through a symlink, so a plain walk
// reports "init left no AWS provider plugin" on a working directory that has
// one.
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
// directories, and calls visit for every non-directory entry. Cycles are cut
// by refusing to revisit a resolved path, which a provider cache cannot
// produce but a hand-made link farm can.
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
// providercache lookup, the same shape the projection floci test spells
// out: go-plugin launches the executable and dispenses whichever protocol
// version it negotiated.
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
