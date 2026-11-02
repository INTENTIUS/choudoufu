// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package azsweeptest is a fake Azure sweep for tests in any module
// (GitHub issue #2281): an in-memory index that answers [azsweep.Searcher]
// and [azsweep.TypeSearcher] by the same tag match the real client makes
// (the owner key in any case, the value exactly), under the caller's
// marker vocabulary, and records what it was asked.
package azsweeptest

import (
	"context"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/pkg/azsweep"
	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Call is one question the fake was asked.
type Call struct {
	// Method is "Search" or "SearchTypes".
	Method       string
	Subscription string
	Estate       string
	ARMTypes     []string
}

// Index is the fake. Resources are keyed by subscription id. A
// subscription in Errs answers that error instead. Set Keys before use.
type Index struct {
	Keys      markerkeys.Keys
	Resources map[string][]azsweep.Resource
	Errs      map[string]error

	mu    sync.Mutex
	calls []Call
}

var (
	_ azsweep.Searcher     = (*Index)(nil)
	_ azsweep.TypeSearcher = (*Index)(nil)
)

// Calls is every question asked so far, in order.
func (x *Index) Calls() []Call {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]Call(nil), x.calls...)
}

func (x *Index) record(c Call) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.calls = append(x.calls, c)
	return x.Errs[c.Subscription]
}

// Search implements [azsweep.Searcher].
func (x *Index) Search(ctx context.Context, subscription, estate string) ([]azsweep.Resource, error) {
	if err := x.record(Call{Method: "Search", Subscription: subscription, Estate: estate}); err != nil {
		return nil, err
	}
	return x.owned(subscription, estate, nil), nil
}

// SearchTypes implements [azsweep.TypeSearcher]: Search narrowed to
// armTypes, compared case-insensitively. An empty armTypes is Search.
func (x *Index) SearchTypes(ctx context.Context, subscription, estate string, armTypes []string) ([]azsweep.Resource, error) {
	if err := x.record(Call{Method: "SearchTypes", Subscription: subscription, Estate: estate, ARMTypes: append([]string(nil), armTypes...)}); err != nil {
		return nil, err
	}
	return x.owned(subscription, estate, armTypes), nil
}

func (x *Index) owned(subscription, estate string, armTypes []string) []azsweep.Resource {
	var out []azsweep.Resource
	for _, r := range x.Resources[subscription] {
		v, ok := azsweep.OwnerTag(x.Keys, r.Tags)
		if !ok || v != x.Keys.OwnerValue(estate) {
			continue
		}
		if len(armTypes) > 0 && !oneOf(r.Type, armTypes) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func oneOf(t string, types []string) bool {
	for _, u := range types {
		if strings.EqualFold(t, u) {
			return true
		}
	}
	return false
}
