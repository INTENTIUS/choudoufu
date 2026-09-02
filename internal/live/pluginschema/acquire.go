// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package pluginschema reads a provider's schemas by launching its plugin
// in-process, with no cloud calls and no provider configuration.
//
// It exists because a fourth program now needs this. tools/survey-gen and
// tools/estate-gen each carry a verbatim copy of it, and estate-gen's copy
// says why: "Duplicated rather than shared because tools/survey-gen is
// package main, same as every other tool in this repository." That reason
// is real but it has a cheap fix, which is this package - an internal
// package both can import. Those two are deliberately left alone here,
// since each has gated tests that a refactor would have to re-run against a
// real provider; collapsing them into this is follow-up work, not a
// prerequisite for the program that needed a third copy.
//
// The two-step - "init" in a scratch directory, then go-plugin in-process -
// is copied rather than reinvented, including the reason for going
// in-process: the JSON dump from "providers schema -json" carries resource
// schemas and resource identity schemas but no list-resource section, while
// the GetProviderSchema response carries all three.
package pluginschema

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
	"github.com/intentius/choudoufu/internal/logging"
	tfplugin "github.com/intentius/choudoufu/internal/plugin"
	tfplugin6 "github.com/intentius/choudoufu/internal/plugin6"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Request is one provider to read schemas from.
type Request struct {
	// InitBin is the choudoufu (or terraform) binary used to install the
	// provider.
	InitBin string

	// WorkDir is a directory this may write a configuration into and run
	// init in. The caller owns it, including removing it.
	WorkDir string

	// Source and Version pin the provider, as a registry source address
	// and an exact version. Version wins when both it and Constraint are
	// set.
	Source  string
	Version string

	// Constraint is a raw version constraint expression, written verbatim
	// into the fixture's "version" argument exactly as a required_providers
	// block would carry it (e.g. "~> 5.0", ">= 3.0, < 4.0"). It exists for
	// a caller that knows what a configuration under measurement declared
	// but not which exact release satisfies it - init resolves that itself,
	// the same way it would for the configuration's own author. Empty means
	// no constraint at all: any published version is acceptable, and init
	// picks the latest. Ignored when Version is set.
	Constraint string

	// Provider is the address the schemas come back under. It must be the
	// provider Source names.
	Provider addrs.Provider

	// Log receives progress lines. Nil discards them.
	Log io.Writer
}

// ResourceTypes reads one provider's managed resource type schemas, in the
// shape internal/live/lint and internal/live/identity both take.
//
// The provider is launched but never configured: a schema read needs no
// cloud and no credentials.
func ResourceTypes(ctx context.Context, req Request) (map[string]providers.Schema, error) {
	schema, err := Acquire(ctx, req)
	if err != nil {
		return nil, err
	}
	return schema.ResourceTypes, nil
}

// Acquire installs the pinned provider into the work directory and reads its
// full GetProviderSchema response.
// Acquire reads a provider's schemas and closes the plugin before returning.
// It is [AcquireSession] for the caller that wants nothing but the schemas,
// which is most of them.
func Acquire(ctx context.Context, req Request) (providers.GetProviderSchemaResponse, error) {
	sess, err := AcquireSession(ctx, req)
	if err != nil {
		return providers.GetProviderSchemaResponse{}, err
	}
	defer sess.Close(ctx)
	return sess.Schema, nil
}

// Session is an acquired provider whose plugin is still running.
//
// Acquire launches a plugin, reads the schemas and closes it, which is all a
// caller wanting schemas needs. A caller that wants the provider to COMPUTE
// something needs the process to outlive the schema read, and the difference
// matters for one specific reason: values a configuration cannot state
// statically because the provider derives them at plan time.
//
// aws_acm_certificate.domain_validation_options is the case that motivated
// this. Its elements - one per domain in domain_name plus
// subject_alternative_names - are filled by the provider during
// PlanResourceChange, before any cloud call, which is how stock OpenTofu can
// plan the canonical ACM/Route53 validation pattern that this fork's static
// evaluator refuses. Nothing in the configuration or in any schema states the
// relationship; only the provider knows it.
//
// A Session is NOT a cloud connection. PlanResourceChange for a create needs
// the provider configured, not credentialed, and the local test target is
// floci. Keep that distinction: "offline" here has always meant no cloud
// calls, and a plan call makes none.
type Session struct {
	// Schema is what [Acquire] would have returned.
	Schema providers.GetProviderSchemaResponse

	mgr      plugins.ProviderManager
	provider addrs.Provider
	log      io.Writer
	closed   bool
}

// Configured returns the running provider, configured with configVal. The
// caller owns nothing: closing the Session closes this too.
func (s *Session) Configured(ctx context.Context, configVal cty.Value) (providers.Configured, tfdiags.Diagnostics) {
	return s.mgr.NewConfiguredProvider(ctx, s.provider, configVal)
}

// Close stops the plugin. Safe to call twice, because the ordinary shape here
// is a defer beside an error path that already closed.
func (s *Session) Close(ctx context.Context) error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	err := s.mgr.CloseAll(ctx)
	if err != nil {
		fmt.Fprintf(s.log, "pluginschema: closing the provider: %v\n", err)
	}
	goPlugin.CleanupClients()
	return err
}

// AcquireSession installs the provider, launches its plugin, reads the
// schemas, and leaves the plugin RUNNING for the caller to use. The caller
// must Close the returned Session.
func AcquireSession(ctx context.Context, req Request) (*Session, error) {
	if req.WorkDir == "" {
		return nil, fmt.Errorf("pluginschema: Request.WorkDir must be set; a caller that has not chosen a work directory should not have this package pick one for it (the current directory is the worst available guess, since that may be the checkout itself)")
	}

	log := req.Log
	if log == nil {
		log = io.Discard
	}

	versionLine := ""
	switch {
	case req.Version != "":
		versionLine = fmt.Sprintf("\n      version = \"= %s\"", req.Version)
	case req.Constraint != "":
		versionLine = fmt.Sprintf("\n      version = %q", req.Constraint)
	}
	fixture := fmt.Sprintf(`terraform {
  required_providers {
    %s = {
      source  = %q%s
    }
  }
}
`, req.Provider.Type, req.Source, versionLine)
	if err := os.WriteFile(filepath.Join(req.WorkDir, "main.tf"), []byte(fixture), 0o600); err != nil {
		return nil, err
	}

	wantVersion := req.Version
	if wantVersion == "" {
		wantVersion = req.Constraint
	}
	if wantVersion == "" {
		wantVersion = "latest"
	}
	fmt.Fprintf(log, "pluginschema: %s init (downloading %s %s if not cached)\n", req.InitBin, req.Source, wantVersion)
	cmd := exec.CommandContext(ctx, req.InitBin, "init", "-backend=false", "-input=false", "-no-color")
	cmd.Dir = req.WorkDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("%s init: %w\n%s", req.InitBin, err, out)
	}

	exe, err := findProviderBinary(req.WorkDir, req.Provider.Type)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(log, "pluginschema: launching provider plugin %s\n", exe)

	lib := plugins.NewLibrary(plugins.ProviderFactories{req.Provider: pluginFactory(exe)}, nil)
	mgr := lib.NewProviderManager()
	sess := &Session{mgr: mgr, provider: req.Provider, log: log}

	// The launch is retried a bounded number of times on go-plugin's own
	// transient handshake failure, generic to ANY provider rather than
	// matched by name: a corpus-gen run over ~75 distinct providers
	// launches that many short-lived subprocesses in quick succession, and
	// under load the go-plugin handshake occasionally comes back empty
	// ("Failed to read any lines from plugin's stdout") with no fault in
	// the provider binary itself - a repeat launch of the identical binary
	// the very next moment ordinarily succeeds. Left unretried, this is a
	// second, version-independent source of the exact symptom #222 exists
	// to close: two runs over an unchanged tree at the identical locked
	// version disagreeing anyway, because the schema fallback silently had
	// a provider's schema on one run and not the other.
	//
	// [plugins.providerManager.GetProviderSchema] already anticipates
	// this - its own comment on the launch failure path reads "Might be a
	// transient error. Don't memoize this result" - so calling it again
	// on the same manager re-invokes the provider factory and spawns a
	// fresh subprocess rather than replaying a cached failure.
	const maxAttempts = 4
	var schema providers.GetProviderSchemaResponse
	var diags tfdiags.Diagnostics
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		schema, diags = mgr.GetProviderSchema(ctx, req.Provider)
		if !diags.HasErrors() || !isTransientLaunchError(diags.Err()) {
			break
		}
		fmt.Fprintf(log, "pluginschema: transient launch failure for %s (attempt %d/%d), retrying: %v\n",
			req.Provider, attempt, maxAttempts, diags.Err())
		time.Sleep(time.Duration(attempt) * 250 * time.Millisecond)
	}
	if diags.HasErrors() {
		sess.Close(ctx)
		return nil, fmt.Errorf("reading the provider schema: %w", diags.Err())
	}
	fmt.Fprintf(log, "pluginschema: %d resource types, %d list resource types\n",
		len(schema.ResourceTypes), len(schema.ListResourceTypes))
	sess.Schema = schema
	return sess, nil
}

// isTransientLaunchError reports whether err is go-plugin's own handshake
// failure signature - "Failed to read any lines from plugin's stdout",
// with no provider name anywhere in the match - rather than a genuine
// schema error the provider itself returned. See the retry loop in
// [Acquire] for why this, specifically, is worth one more try instead of
// being reported as this provider's real outcome.
func isTransientLaunchError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Failed to read any lines from plugin's stdout")
}

// InstalledVersion reports the exact version init resolved for provider
// inside workDir, when a caller acquired schemas without pinning an exact
// version (an empty [Request.Version], a [Request.Constraint] or neither -
// #211's per-provider acquisition, which cannot know in advance which
// release satisfies an unconstrained or ranged requirement the way a
// hardcoded pin can).
//
// The answer is read from the unpacked layout itself -
// workDir/.terraform/providers/<host>/<namespace>/<type>/<version>/<os_arch>/<binary>
// - via the same symlink-following walk [Acquire] needed to find the
// plugin executable in the first place ([findProviderBinary]'s doc comment
// explains why a plain walk cannot see it under TF_PLUGIN_CACHE_DIR), so
// this only ever disagrees with what Acquire itself launched if the layout
// changes out from under both.
//
// ok is false when no provider plugin is present, which after a successful
// Acquire call should not happen; a caller more interested in "was this
// acquired at all" should be checking Acquire's own error instead.
func InstalledVersion(workDir string, provider addrs.Provider) (string, bool) {
	exe, err := findProviderBinary(workDir, provider.Type)
	if err != nil {
		return "", false
	}
	version := filepath.Base(filepath.Dir(filepath.Dir(exe)))
	if version == "" || version == "." || version == string(filepath.Separator) {
		return "", false
	}
	return version, true
}

// findProviderBinary locates the plugin executable init unpacked under the
// work directory.
//
// The walk follows directory symlinks, which matters for one case worth
// stating: with TF_PLUGIN_CACHE_DIR set - the ordinary way to keep a
// provider download off a run's critical path - init does not unpack
// anything here at all. It links the version directory to the shared cache,
// and filepath.WalkDir does not descend through a symlink, so a plain walk
// reports "no provider plugin" on a working directory that has one.
func findProviderBinary(dir, providerType string) (string, error) {
	prefix := "terraform-provider-" + providerType
	var found []string
	root := filepath.Join(dir, ".terraform", "providers")
	err := walkFollowingLinks(root, func(path string, d fs.DirEntry) error {
		if !strings.HasPrefix(d.Name(), prefix) {
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
		return "", fmt.Errorf("init left no %s provider plugin under %s", providerType, root)
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
// providercache lookup: go-plugin launches the executable and dispenses
// whichever protocol version it negotiated.
func pluginFactory(exe string) providers.Factory {
	schemaCache := providers.NewSchemaCache()

	return func() (providers.Interface, error) {
		config := &goPlugin.ClientConfig{
			HandshakeConfig:  tfplugin.Handshake,
			Logger:           logging.NewProviderLogger(""),
			AllowedProtocols: []goPlugin.Protocol{goPlugin.ProtocolGRPC},
			Managed:          true,
			Cmd:              exec.Command(exe), //nolint:gosec // the path comes from init in a directory the caller owns
			AutoMTLS:         true,
			VersionedPlugins: tfplugin.VersionedPlugins,
			SyncStdout:       logging.PluginOutputMonitor("provider:stdout"),
			SyncStderr:       logging.PluginOutputMonitor("provider:stderr"),
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
			return nil, fmt.Errorf("the provider negotiated unsupported plugin protocol version %d", protoVer)
		}
	}
}
