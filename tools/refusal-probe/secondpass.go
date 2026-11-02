// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"io"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/offlineplan"
	"github.com/intentius/choudoufu/internal/providers"
)

// secondPass is GitHub issue #2297 for this probe: live-plan's second
// identity pass, run offline for an entry whose provider schemas loaded, so
// a site live-plan resolves with planned values (the acm module's route53
// validation records) is not counted as a refusal here. See
// internal/live/offlineplan for the pass and for why it makes no cloud call.
//
// It is on by default under -schemas and off with -second-pass=false; the
// run records which, and -diff refuses a mixed pair.
type secondPass struct {
	sessions *offlineplan.Sessions
}

func newSecondPass(initBin string, log io.Writer) *secondPass {
	return &secondPass{sessions: offlineplan.NewSessions(initBin, log)}
}

// managed returns the planned values to hand [check.Analyze] for cfg, and
// how the pass decided. rows are the entry's own acquisition rows, which
// say which release of each provider this entry is measured at; a provider
// with no row, or one that did not load, is not started.
func (s *secondPass) managed(ctx context.Context, cfg *configs.Config, schemas map[string]providers.Schema, rows []entryProvider) (map[string]cty.Value, offlineplan.Outcome) {
	if s == nil || cfg == nil || len(schemas) == 0 {
		return nil, ""
	}
	versions := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Present && row.Version != "" {
			versions[row.Provider] = row.Version
		}
	}
	provs := offlineplan.New(s.sessions.For(func(p addrs.Provider) (string, bool) {
		v, ok := versions[p.ForDisplay()]
		return v, ok
	}))
	defer provs.Close(ctx)
	return offlineplan.ManagedResults(ctx, cfg, identity.Context{Schemas: schemas}, provs)
}

func (s *secondPass) close(ctx context.Context) {
	if s != nil {
		s.sessions.Close(ctx)
	}
}
