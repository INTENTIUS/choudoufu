// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/plugins"
)

// pluginsForDir is the plugin library for the configuration in dir, rather
// than for the configuration in the process working directory.
//
// GitHub issue #973. Almost every command in this fork reaches its root
// module through the global -chdir option, which moves the process itself,
// so "where the tool runs" and "what the tool reads" are the same directory
// and [Meta.contextOpts] can resolve plugins from Meta.WorkingDir without
// anybody noticing. live-check and live-ls are the exceptions: both take
// the directory as a positional argument, deliberately, so that a caller
// can point them at a repository it is not standing in - which is behold's
// access pattern (INTENTIUS/behold#366) and CI's. For those two, resolving
// the plugins against the process working directory reads one directory's
// providers while reading another directory's configuration, and the answer
// silently degrades to the built-in admission table.
//
// The error is returned rather than swallowed, but it is not fatal by
// itself: [Meta.providerFactoriesIn] reports a lock file naming a provider
// that is not installed here as an error while still returning every
// provider that IS installed, and both callers are analyses that already
// degrade rather than refuse when a schema is missing. A directory carrying
// a lock file but no .terraform - a repository checked out fresh, never
// initialized - is exactly that case, and it is the ordinary case for
// live-check, whose own help text says it is meant to be run on a
// repository that has never used this fork.
//
// The provisioner half of the library is untouched: provisioner discovery
// has never been per-configuration (see [Meta.pluginDirs]), and neither of
// these two commands runs a provisioner.
func (m *Meta) pluginsForDir(dir string) (plugins.Library, error) {
	// The same escape hatch contextOpts has, and for the same reason: with
	// testing overrides in place there is no plugin discovery to point at
	// anything, so pointing it at dir would be pointing nothing anywhere.
	if m.testingOverrides != nil {
		return plugins.NewLibrary(
			m.testingOverrides.Providers,
			m.testingOverrides.Provisioners,
		), nil
	}

	factories, err := m.providerFactoriesIn(dir, m.providerLocalCacheDirFor(dir))
	return plugins.NewLibrary(factories, m.provisionerFactories()), err
}

// providerLocalCacheDirFor is dir's own provider cache: the ".terraform"
// directory "choudoufu init" would have written inside dir.
//
// TF_DATA_DIR is honoured the way it is everywhere else, by winning: an
// explicitly overridden data directory is one directory for the whole
// process, not one per configuration, so a run that set it gets the same
// cache for dir that it gets for everything else. That is the pre-existing
// meaning of the variable, and #973 is about the default case, where the
// data directory is a dot-prefixed directory inside the root module.
func (m *Meta) providerLocalCacheDirFor(dir string) string {
	wd := workdir.NewDir(dir)
	if m.WorkingDir != nil && m.WorkingDir.DataDirOverridden() {
		wd.OverrideDataDir(m.WorkingDir.DataDir())
	}
	return wd.ProviderLocalCacheDir()
}
