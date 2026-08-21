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
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/logging"
	tfplugin "github.com/intentius/choudoufu/internal/plugin"
	tfplugin6 "github.com/intentius/choudoufu/internal/plugin6"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// acquireSchemas downloads the pinned provider into workdir, reads its full
// GetProviderSchema response by launching the plugin in-process, and probes
// every resource type's classic Importer over the same connection.
//
// This is the same two-step the gated test tier uses (see
// internal/live/stamp's TestTaggableSetAgainstRealSchemas for the init
// half and internal/live/projection's floci test for the go-plugin
// half): `terraform init` in a directory that requires the pinned release,
// then go-plugin launches the executable init unpacked. Going in-process
// rather than shelling out to `providers schema -json` is deliberate - the
// JSON dump carries resource schemas and resource_identity_schemas but no
// list-resource section (SURVEY.md's 2026-08-12 re-run notes exactly that
// gap), while the GetProviderSchema response carries all three.
//
// The schema read itself needs no configuration, no cloud and no
// credentials - but issue #331 found that a resource identity schema is not
// proof a classic Importer exists behind it: six wire-only types included
// two, aws_iam_policy_attachment and aws_acm_certificate_validation, whose
// helper/schema Provider.ImportState returns "doesn't support import" before
// any API call, the moment internal/live/projection calls
// ImportResourceState during a real migrate. Nothing in the schema itself
// says so; the only way to ask is to call ImportResourceState and read
// whether the error is that fixed string (or terraform-plugin-framework's
// "Resource Import Not Implemented") rather than a downstream parse or API
// error. probeImportability does exactly that, over the one already-launched
// plugin connection, with a syntactically invalid dummy ID that never
// reaches AWS either way - so this function now configures the provider
// (skip_* flags, fake keys, the same shape tools/importer-probe already
// proved safe) purely to unlock that one extra RPC per type, and the sweep
// itself makes no network call.
func acquireSchemas(initBin, workdir string, log io.Writer) (providers.GetProviderSchemaResponse, map[string]bool, error) {
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
		return none, nil, err
	}

	fmt.Fprintf(log, "survey-gen: %s init (downloading %s %s if not cached)\n", initBin, providerSource, providerVersion)
	cmd := exec.Command(initBin, "init", "-backend=false", "-input=false", "-no-color")
	cmd.Dir = workdir
	if out, err := cmd.CombinedOutput(); err != nil {
		return none, nil, fmt.Errorf("%s init: %w\n%s", initBin, err, out)
	}

	exe, err := findProviderBinary(workdir)
	if err != nil {
		return none, nil, err
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
	// one run and an error on the other.
	const maxAttempts = 4
	var schema providers.GetProviderSchemaResponse
	var diags tfdiags.Diagnostics
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		schema, diags = mgr.GetProviderSchema(context.Background(), awsAddr)
		if !diags.HasErrors() || !isTransientLaunchError(diags.Err()) {
			break
		}
		fmt.Fprintf(log, "survey-gen: transient launch failure for %s (attempt %d/%d), retrying: %v\n",
			awsAddr, attempt, maxAttempts, diags.Err())
		time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
	}
	if diags.HasErrors() {
		return none, nil, fmt.Errorf("reading the provider schema: %w", diags.Err())
	}
	fmt.Fprintf(log, "survey-gen: %d resource types, %d list resource types\n",
		len(schema.ResourceTypes), len(schema.ListResourceTypes))

	importable, err := probeImportability(context.Background(), mgr, awsAddr, schema, log)
	if err != nil {
		return none, nil, fmt.Errorf("probing importability: %w", err)
	}
	return schema, importable, nil
}

// probeImportability asks every resource type the schema carries whether it
// has a classic Importer, over the provider connection acquireSchemas
// already launched - see that function's doc comment for why a resource
// identity schema alone cannot answer this.
//
// The method is tools/importer-probe's (issue #331's audit branch,
// 930af51744): helper/schema's Provider.ImportState checks Importer == nil
// and terraform-plugin-framework answers "Resource Import Not Implemented"
// before either makes an API call, so a syntactically invalid import ID
// discriminates the two populations with no cloud and no credentials. The
// provider still has to be CONFIGURED to reach ImportResourceState at all
// (ConfigureProvider gates it), so this builds the same skip_*/fake-key
// configuration importer-probe uses - it unlocks the RPC, it does not enable
// a real API call.
//
// mgr.NewConfiguredProvider launches a FRESH plugin subprocess rather than
// reusing the one GetProviderSchema talked to, so the whole sweep runs over
// that one configured instance rather than one launch per type: 1699 RPCs
// over a single connection, not 1699 subprocess spawns.
func probeImportability(ctx context.Context, mgr plugins.ProviderManager, addr addrs.Provider, schema providers.GetProviderSchemaResponse, log io.Writer) (map[string]bool, error) {
	block := listclient.TypeSchema{TypeName: "provider", Config: schema.Provider.Block}
	cfg, diags := block.BuildConfig(map[string]cty.Value{
		"skip_credentials_validation": cty.True,
		"skip_metadata_api_check":     cty.True,
		"skip_requesting_account_id":  cty.True,
		"skip_region_validation":      cty.True,
		"region":                      cty.StringVal("us-east-1"),
		"access_key":                  cty.StringVal("survey-gen-probe"),
		"secret_key":                  cty.StringVal("survey-gen-probe"),
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("building the probe provider configuration: %w", diags.Err())
	}

	configured, diags := mgr.NewConfiguredProvider(ctx, addr, cfg)
	if diags.HasErrors() {
		return nil, fmt.Errorf("configuring the provider for the import probe: %w", diags.Err())
	}

	names := make([]string, 0, len(schema.ResourceTypes))
	for t := range schema.ResourceTypes {
		names = append(names, t)
	}
	sort.Strings(names)

	start := time.Now()
	out := make(map[string]bool, len(names))
	for _, t := range names {
		ir := configured.ImportResourceState(ctx, providers.ImportResourceStateRequest{
			TypeName: t,
			Target:   providers.ImportTarget{ID: "survey-gen-probe-dummy-id"},
		})
		msg := ""
		if ir.Diagnostics.HasErrors() {
			msg = ir.Diagnostics.Err().Error()
		}
		out[t] = !strings.Contains(msg, "doesn't support import") && !strings.Contains(msg, "Import Not Implemented")
	}
	fmt.Fprintf(log, "survey-gen: import-probed %d resource types in %s\n", len(names), time.Since(start).Round(time.Millisecond))
	return out, nil
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

// isTransientLaunchError reports whether err is go-plugin's own handshake
// failure signature - "Failed to read any lines from plugin's stdout", with
// no provider name anywhere in the match - rather than a genuine schema
// error the provider itself returned. Copied from
// internal/live/pluginschema.isTransientLaunchError, which this file
// otherwise duplicates in full (see that package's doc comment); kept in
// step by hand since collapsing the two is the documented follow-up, not
// this fix.
func isTransientLaunchError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Failed to read any lines from plugin's stdout")
}
