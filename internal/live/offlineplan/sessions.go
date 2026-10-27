// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package offlineplan

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Sessions is the corpus tools' provider source for the second pass: one
// installed provider plugin per (provider, version), started on first use
// and kept until [Sessions.Close], so a run over many configurations
// installs each release once.
//
// The tools read schemas with [pluginschema.Acquire], which closes its
// plugin and calls go-plugin's CleanupClients, ending every plugin process
// the program started. A [Sessions] survives that because it keeps the
// install, not a process: each [Providers] built over [Sessions.For] starts
// its own configured processes, and the caller closes that [Providers]
// before the next configuration.
type Sessions struct {
	initBin string
	log     io.Writer

	open   map[string]*pluginschema.Session
	failed map[string]error
	dirs   []string
}

// NewSessions returns an empty [Sessions] that installs providers with
// initBin. Work directories are made with [os.MkdirTemp], so TMPDIR decides
// where they go; a long TMPDIR can break go-plugin's socket handshake.
func NewSessions(initBin string, log io.Writer) *Sessions {
	if log == nil {
		log = io.Discard
	}
	return &Sessions{initBin: initBin, log: log, open: map[string]*pluginschema.Session{}, failed: map[string]error{}}
}

// Version names the exact release a configuration measures a provider at,
// as the caller's schema acquisition resolved it. ok false means the
// provider is not available for this configuration.
type Version func(provider addrs.Provider) (version string, ok bool)

// For is a [Source] for one configuration: each provider at the version
// version names.
func (s *Sessions) For(version Version) Source {
	return sessionSource{s: s, version: version}
}

func (s *Sessions) session(ctx context.Context, provider addrs.Provider, version string) (*pluginschema.Session, error) {
	key := provider.String() + "@" + version
	if sess, ok := s.open[key]; ok {
		return sess, nil
	}
	if err, ok := s.failed[key]; ok {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "offlineplan")
	if err != nil {
		s.failed[key] = err
		return nil, err
	}
	s.dirs = append(s.dirs, dir)
	sess, err := pluginschema.AcquireSession(ctx, pluginschema.Request{
		InitBin:  s.initBin,
		WorkDir:  dir,
		Source:   provider.ForDisplay(),
		Version:  version,
		Provider: provider,
		Log:      s.log,
	})
	if err != nil {
		s.failed[key] = err
		return nil, err
	}
	s.open[key] = sess
	return sess, nil
}

// Close stops every session and removes the work directories.
func (s *Sessions) Close(ctx context.Context) {
	for _, sess := range s.open {
		_ = sess.Close(ctx)
	}
	s.open = map[string]*pluginschema.Session{}
	for _, dir := range s.dirs {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Fprintf(s.log, "offlineplan: removing %s: %v\n", dir, err)
		}
	}
	s.dirs = nil
}

type sessionSource struct {
	s       *Sessions
	version Version
}

func (src sessionSource) get(ctx context.Context, provider addrs.Provider) (*pluginschema.Session, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	version, ok := src.version(provider)
	if !ok || version == "" {
		return nil, diags.Append(fmt.Errorf("no installed release of %s for this configuration", provider))
	}
	sess, err := src.s.session(ctx, provider, version)
	if err != nil {
		return nil, diags.Append(err)
	}
	return sess, diags
}

func (src sessionSource) GetProviderSchema(ctx context.Context, provider addrs.Provider) (providers.ProviderSchema, tfdiags.Diagnostics) {
	sess, diags := src.get(ctx, provider)
	if diags.HasErrors() {
		return providers.ProviderSchema{}, diags
	}
	return sess.Schema, diags
}

func (src sessionSource) NewConfiguredProvider(ctx context.Context, provider addrs.Provider, cfgVal cty.Value) (providers.Configured, tfdiags.Diagnostics) {
	sess, diags := src.get(ctx, provider)
	if diags.HasErrors() {
		return nil, diags
	}
	return sess.Configured(ctx, cfgVal)
}
