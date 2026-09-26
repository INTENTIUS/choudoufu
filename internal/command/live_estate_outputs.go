// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"sync"

	"github.com/zclconf/go-cty/cty"

	terraformProvider "github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// liveEstateOutputs answers the builtin terraform provider's
// terraform_estate_outputs data source (GitHub issue #1371) for one command.
//
// The provider is constructed when the command builds its provider factories,
// well before a live run has opened its record store, so it is handed this
// holder and the run fills it in once the store is open: in PriorState for
// plain plan and apply under a live block, and after the store opens for
// live-plan. A command that never fills it (any stock run) answers every read
// with the provider's own "needs a live block" refusal.
type liveEstateOutputs struct {
	mu  sync.Mutex
	src *projection.EstateOutputsSource
}

var _ terraformProvider.EstateOutputReader = (*liveEstateOutputs)(nil)

// liveEstateOutputs returns this command's holder, creating it on first use.
// Every provider factory and every live run in one command must see the same
// one, which is why it hangs off Meta rather than being built per call.
func (m *Meta) liveEstateOutputs() *liveEstateOutputs {
	if m.estateOutputs == nil {
		m.estateOutputs = &liveEstateOutputs{}
	}
	return m.estateOutputs
}

// open records the store this run reads other estates' outputs from. store
// may be nil, for live-plan going on past an unreachable store; unavailable
// then says why, and every read refuses with it.
func (l *liveEstateOutputs) open(store staterecord.Store, rs *configs.LiveRecordStore, estate, unavailable string) {
	if l == nil {
		return
	}
	src := &projection.EstateOutputsSource{
		Store:       store,
		Estate:      estate,
		Unavailable: unavailable,
	}
	if rs != nil {
		src.StoreType = rs.Type
		src.Bucket = rs.Bucket
	}
	l.mu.Lock()
	l.src = src
	l.mu.Unlock()
}

// ReadEstateOutputs implements [terraformProvider.EstateOutputReader].
func (l *liveEstateOutputs) ReadEstateOutputs(ctx context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	var src *projection.EstateOutputsSource
	if l != nil {
		l.mu.Lock()
		src = l.src
		l.mu.Unlock()
	}
	if src == nil {
		var diags tfdiags.Diagnostics
		return nil, diags.Append(terraformProvider.EstateOutputsNeedLiveBlock())
	}
	return projection.ReadEstateOutputs(ctx, *src, estate, names)
}
