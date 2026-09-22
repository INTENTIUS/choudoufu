// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestInitArgsInstallFromAWarmCacheOffline is #1509's guard. A render whose
// plugin cache already holds the pinned release must init with -plugin-dir
// over that cache, because TF_PLUGIN_CACHE_DIR alone still sends init to the
// registry for the version list and a DNS failure then fails the golden. A
// cache that does not hold it, under the host this init binary resolves
// against, must leave init to download (and to fail loudly without network).
func TestInitArgsInstallFromAWarmCacheOffline(t *testing.T) {
	cache := t.TempDir()
	platform := filepath.Join(cache, "registry.terraform.io", "hashicorp", "aws", providerVersion, runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(platform, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(platform, "terraform-provider-aws_v"+providerVersion+"_x5"), []byte("x"), 0o755); err != nil { //nolint:gosec // an executable stand-in is the point
		t.Fatal(err)
	}
	base := []string{"init", "-backend=false", "-input=false", "-no-color"}

	t.Setenv("TF_PLUGIN_CACHE_DIR", cache)
	args, offline := initArgs("terraform")
	if want := append(slices.Clone(base), "-plugin-dir="+cache); !slices.Equal(args, want) || offline != cache {
		t.Errorf("warm cache: initArgs(terraform) = %q, %q; want %q, %q", args, offline, want, cache)
	}

	// tofu resolves hashicorp/aws against registry.opentofu.org, which this
	// cache does not hold: -plugin-dir there would fail an init that a
	// download would have passed.
	if args, offline := initArgs("tofu"); !slices.Equal(args, base) || offline != "" {
		t.Errorf("cache for another host: initArgs(tofu) = %q, %q; want %q with no plugin dir", args, offline, base)
	}

	t.Setenv("TF_PLUGIN_CACHE_DIR", t.TempDir())
	if args, offline := initArgs("terraform"); !slices.Equal(args, base) || offline != "" {
		t.Errorf("cold cache: initArgs = %q, %q; want %q with no plugin dir", args, offline, base)
	}
}

// TestOfflineInitDropsTheCacheVariable: -plugin-dir and TF_PLUGIN_CACHE_DIR
// naming one directory fail init with "cannot install existing provider
// directory ... to itself", so the offline init must not carry the cache
// variable, and the downloading init must keep it (it is what fills the
// cache).
func TestOfflineInitDropsTheCacheVariable(t *testing.T) {
	environ := []string{"HOME=/h", "TF_PLUGIN_CACHE_DIR=/c", "PATH=/p"}
	if got := initEnv(environ, "/c"); slices.Contains(got, "TF_PLUGIN_CACHE_DIR=/c") || len(got) != 2 {
		t.Errorf("offline init env = %q; want TF_PLUGIN_CACHE_DIR removed and the rest kept", got)
	}
	if got := initEnv(environ, ""); !slices.Equal(got, environ) {
		t.Errorf("downloading init env = %q; want it unchanged", got)
	}
}

// TestPluginStartTimeoutIsNamedAsLoad holds the start-timeout error to a
// message that says what it is. go-plugin's own text reads like a provider
// fault, and #1509's gate reported it as the golden failing.
func TestPluginStartTimeoutIsNamedAsLoad(t *testing.T) {
	got := explainStartTimeout(errors.New("failed to instantiate provider: timeout while waiting for plugin to start"))
	if !strings.Contains(got.Error(), "machine load") || !strings.Contains(got.Error(), pluginStartTimeout.String()) {
		t.Errorf("start timeout explained as %q; want it to name machine load and the %s bound", got, pluginStartTimeout)
	}
	other := errors.New("provider returned an error")
	if explainStartTimeout(other) != other {
		t.Error("an error that is not the start timeout was rewritten")
	}
}
