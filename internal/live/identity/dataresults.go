// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// This file is the resolution side of issue #179's data-read phase: the
// values a caller read before resolution began, indexed so the static
// evaluator can answer a data-resource reference with the provider's own
// answer instead of refusing it as dynamic.
//
// Nothing here reads anything. The package's founding promise - no provider
// process, no cloud, no state - holds exactly as it does for
// [Context.Schemas]: the reads happened in internal/live/dataread, in front
// of a caller that owns provider processes, and resolution consumes a map.

// dataResultsIndex is [Context.DataResults] regrouped for the resolver: one
// aggregated value per (module instance, data resource), shaped the way the
// plan-time evaluator shapes a whole-resource reference so that instance
// indexing in expressions works unchanged. See [buildDataResultsIndex].
type dataResultsIndex map[string]map[string]cty.Value

// buildDataResultsIndex parses and regroups the caller's per-instance
// results. A key that does not parse as an absolute resource instance
// address, names a managed resource, or expands into an aggregate this
// index cannot shape (mixed integer and string keys, or a count sequence
// with holes) is a caller error: it is reported as a diagnostic rather than
// dropped, because a silently dropped result would resurface later as the
// generic dynamic-value refusal, pointing the user at their configuration
// for a defect in the calling code.
func buildDataResultsIndex(results map[string]cty.Value) (dataResultsIndex, []string) {
	if len(results) == 0 {
		return nil, nil
	}

	type group struct {
		noKey   *cty.Value
		intKeys map[int]cty.Value
		strKeys map[string]cty.Value
	}
	groups := make(map[string]map[string]*group)
	var bad []string

	keys := make([]string, 0, len(results))
	for key := range results {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		addr, diags := addrs.ParseAbsResourceInstanceStr(key)
		if diags.HasErrors() || addr.Resource.Resource.Mode != addrs.DataResourceMode {
			bad = append(bad, key)
			continue
		}
		modKey := addr.Module.String()
		resKey := addr.Resource.Resource.String()
		if groups[modKey] == nil {
			groups[modKey] = make(map[string]*group)
		}
		g := groups[modKey][resKey]
		if g == nil {
			g = &group{}
			groups[modKey][resKey] = g
		}
		val := results[key]
		switch k := addr.Resource.Key.(type) {
		case nil:
			g.noKey = &val
		case addrs.IntKey:
			if g.intKeys == nil {
				g.intKeys = make(map[int]cty.Value)
			}
			g.intKeys[int(k)] = val
		case addrs.StringKey:
			if g.strKeys == nil {
				g.strKeys = make(map[string]cty.Value)
			}
			g.strKeys[string(k)] = val
		default:
			bad = append(bad, key)
		}
	}

	idx := make(dataResultsIndex, len(groups))
	for modKey, byRes := range groups {
		idx[modKey] = make(map[string]cty.Value, len(byRes))
		for resKey, g := range byRes {
			val, ok := aggregateGroup(g.noKey, g.intKeys, g.strKeys)
			if !ok {
				bad = append(bad, fmt.Sprintf("%s (in %s)", resKey, modKey))
				continue
			}
			idx[modKey][resKey] = val
		}
	}
	sort.Strings(bad)
	return idx, bad
}

// aggregateGroup shapes one data resource's instance values into the value a
// whole-resource reference evaluates to: the single object for an
// unexpanded block, a tuple in index order for count, an object keyed by
// string for for_each. A resource with a mix of key kinds, or a count
// sequence with a hole, has no honest aggregate and reports false.
func aggregateGroup(noKey *cty.Value, intKeys map[int]cty.Value, strKeys map[string]cty.Value) (cty.Value, bool) {
	kinds := 0
	if noKey != nil {
		kinds++
	}
	if len(intKeys) > 0 {
		kinds++
	}
	if len(strKeys) > 0 {
		kinds++
	}
	if kinds != 1 {
		return cty.NilVal, false
	}
	switch {
	case noKey != nil:
		return *noKey, true
	case len(intKeys) > 0:
		vals := make([]cty.Value, len(intKeys))
		for i := range vals {
			v, ok := intKeys[i]
			if !ok {
				return cty.NilVal, false
			}
			vals[i] = v
		}
		return cty.TupleVal(vals), true
	default:
		vals := make(map[string]cty.Value, len(strKeys))
		for k, v := range strKeys {
			vals[k] = v
		}
		return cty.ObjectVal(vals), true
	}
}

// dataLookupFor is the [configs.StaticDataLookup] bound to one module
// instance: it covers exactly the data resources the phase read within that
// instance, and nothing else. Nil when the index carries nothing for the
// instance, so an evaluator without results behaves exactly as it always
// has.
func (r *resolver) dataLookupFor(modInst addrs.ModuleInstance) configs.StaticDataLookup {
	if r.dataIndex == nil {
		return nil
	}
	byRes := r.dataIndex[modInst.String()]
	if len(byRes) == 0 {
		return nil
	}
	return func(addr addrs.Resource) (cty.Value, bool) {
		val, ok := byRes[addr.String()]
		return val, ok
	}
}
