// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package plugincache answers one question: does a terraform/tofu plugin
// cache directory already hold an unpacked provider release for this
// platform, so that an init can install from it with -plugin-dir and never
// ask a registry anything.
//
// An init with TF_PLUGIN_CACHE_DIR set still queries the registry for the
// version list before it links the cached package, so a cache alone does not
// make an init offline (#1509: TestIdentityGolden went red on
// "lookup registry.terraform.io: no such host" with hashicorp/aws 6.59.0 in
// the cache). -plugin-dir does, because it replaces every installation
// source with the one directory. The plugin cache's unpacked layout,
// <host>/<namespace>/<type>/<version>/<os>_<arch>/, is one -plugin-dir reads
// as a local mirror, and the install it produces is a symlink to the cached
// package rather than a copy.
package plugincache

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// EnvDir is the environment variable terraform and tofu both read for the
// shared plugin cache.
const EnvDir = "TF_PLUGIN_CACHE_DIR"

// Package reports whether dir holds an unpacked host/namespace/typ at
// version for the running platform: the version directory exists and holds
// an executable whose name starts with terraform-provider-<typ>. It returns
// the platform directory it found. An empty dir is never a hit.
func Package(dir, host, namespace, typ, version string) (string, bool) {
	if dir == "" {
		return "", false
	}
	platform := filepath.Join(dir, host, namespace, typ, version, runtime.GOOS+"_"+runtime.GOARCH)
	entries, err := os.ReadDir(platform)
	if err != nil {
		return "", false
	}
	prefix := "terraform-provider-" + typ
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		info, err := os.Stat(filepath.Join(platform, e.Name()))
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return platform, true
	}
	return "", false
}

// FromEnv is [Package] over the directory TF_PLUGIN_CACHE_DIR names. It
// returns that directory (the one to pass as -plugin-dir), not the platform
// directory inside it.
func FromEnv(host, namespace, typ, version string) (string, bool) {
	dir := os.Getenv(EnvDir)
	if _, ok := Package(dir, host, namespace, typ, version); !ok {
		return "", false
	}
	return dir, true
}

// DefaultHost is the registry host an init binary resolves an unqualified
// provider source like "hashicorp/aws" against: registry.terraform.io for
// stock terraform, registry.opentofu.org for tofu and for choudoufu, which
// is a tofu fork. Keyed on the binary's base name, the only thing a caller
// passing -init-bin tells us.
func DefaultHost(initBin string) string {
	if strings.HasPrefix(filepath.Base(initBin), "terraform") {
		return "registry.terraform.io"
	}
	return "registry.opentofu.org"
}
