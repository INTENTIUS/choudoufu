// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package gcpsweeptest is a fake GCP sweep for tests in any module
// (GitHub issue #2281): an in-memory index that answers [gcpsweep.Searcher],
// [gcpsweep.AddressSearcher] and [gcpsweep.TypeLister] by the same label
// match the real client makes, under the caller's marker vocabulary, and
// records what it was asked.
package gcpsweeptest

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/pkg/gcpsweep"
	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// Call is one question the fake was asked.
type Call struct {
	// Method is "Search", "SearchAddress" or "ListType".
	Method  string
	Project string
	Estate  string
	// Address is SearchAddress's address; TypeName and Scope are
	// ListType's.
	Address  string
	TypeName string
	Scope    gcpsweep.ListScope
}

// Index is the fake. Assets are keyed by project id. A project in Errs
// answers that error instead, every method alike. The zero Index answers
// nothing found; set Keys before use.
type Index struct {
	Keys   markerkeys.Keys
	Assets map[string][]gcpsweep.Asset
	Errs   map[string]error

	mu    sync.Mutex
	calls []Call
}

var (
	_ gcpsweep.Searcher        = (*Index)(nil)
	_ gcpsweep.AddressSearcher = (*Index)(nil)
	_ gcpsweep.TypeLister      = (*Index)(nil)
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
	return x.Errs[c.Project]
}

func (x *Index) match(project string, keep func(gcpsweep.Asset) bool) []gcpsweep.Asset {
	var out []gcpsweep.Asset
	for _, a := range x.Assets[project] {
		if keep(a) {
			out = append(out, a)
		}
	}
	return out
}

func (x *Index) owned(a gcpsweep.Asset, estate string) bool {
	return a.Labels[x.Keys.Owner] == x.Keys.OwnerValue(estate)
}

// Search implements [gcpsweep.Searcher]: every asset in project whose
// owner label is exactly estate.
func (x *Index) Search(_ context.Context, project, estate string) ([]gcpsweep.Asset, error) {
	if err := x.record(Call{Method: "Search", Project: project, Estate: estate}); err != nil {
		return nil, err
	}
	return x.match(project, func(a gcpsweep.Asset) bool { return x.owned(a, estate) }), nil
}

// SearchAddress implements [gcpsweep.AddressSearcher]: Search narrowed to
// the assets whose address label is exactly address.
func (x *Index) SearchAddress(_ context.Context, project, estate, address string) ([]gcpsweep.Asset, error) {
	if err := x.record(Call{Method: "SearchAddress", Project: project, Estate: estate, Address: address}); err != nil {
		return nil, err
	}
	return x.match(project, func(a gcpsweep.Asset) bool {
		return x.owned(a, estate) && a.Labels[x.Keys.Address] == x.Keys.AddressValue(address)
	}), nil
}

// ListType implements [gcpsweep.TypeLister]: the owned assets of
// typeName's asset type in scope's project and, when scope names one, its
// location. A type [gcpsweep.Covered] does not name is an error, as it is
// for the real client.
func (x *Index) ListType(_ context.Context, typeName string, scope gcpsweep.ListScope, estate string) ([]gcpsweep.Asset, error) {
	if err := x.record(Call{Method: "ListType", Project: scope.Project, Estate: estate, TypeName: typeName, Scope: scope}); err != nil {
		return nil, err
	}
	t, ok := gcpsweep.Covered(typeName)
	if !ok {
		return nil, fmt.Errorf("%s has no per-type lister", typeName)
	}
	return x.match(scope.Project, func(a gcpsweep.Asset) bool {
		if !x.owned(a, estate) || !t.Names(a) {
			return false
		}
		return scope.Location == "" || strings.EqualFold(a.Location, scope.Location)
	}), nil
}
