// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/hashicorp/hcl/v2"

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
	return buildResultsIndex(results, addrs.DataResourceMode)
}

func buildResultsIndex(results map[string]cty.Value, mode addrs.ResourceMode) (dataResultsIndex, []string) {
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
		if diags.HasErrors() || addr.Resource.Resource.Mode != mode {
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

// DataLookupFor is [resolver.dataLookupFor] for a caller outside this
// package's own resolution pipeline: it turns a flat, absolute-instance-
// keyed result map - [dataread.Read]'s or a sibling entry point's own
// output shape, "shaped for [Context.DataResults]" per that package's own
// doc comment - into a [configs.StaticDataLookup] scoped to one module, the
// same seam [configs.StaticEvaluator.WithDataResults] documents.
//
// module is assumed unkeyed ([addrs.Module.UnkeyedInstanceShim]) - the same
// "no repeated module" simplification this package's own second pass
// ([Context.ManagedResults] via [projection.PlanInstances]) and
// [dataread]'s managed-live fallback both already carry, stated here rather
// than invented here. GitHub issue #313's provider-configuration
// dependency-order fixpoint is the first caller: a provider block is
// almost always declared in the root module, and internal/command's own
// providerConfigValue already restricts a non-root provider block to one
// that declares no count/for_each/enabled/depends_on anywhere in its call
// chain (issue #201), which is exactly the shape this assumption holds for.
//
// bad names every result key that did not parse as an absolute resource
// instance address or could not be shaped into an aggregate - the same
// caller-error [buildDataResultsIndex] already distinguishes from "nothing
// was demanded," for a caller that wants to report it as a defect in its
// own read rather than silently answer nothing.
func DataLookupFor(results map[string]cty.Value, module addrs.Module) (lookup configs.StaticDataLookup, bad []string) {
	idx, bad := buildDataResultsIndex(results)
	if idx == nil {
		return nil, bad
	}
	byRes := idx[module.UnkeyedInstanceShim().String()]
	if len(byRes) == 0 {
		return nil, bad
	}
	return func(addr addrs.Resource) (cty.Value, bool) {
		val, ok := byRes[addr.String()]
		return val, ok
	}, bad
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

// managedCovered reports whether trav names an ATTRIBUTE of a managed
// resource the caller's live read covers in the module instance being
// resolved (issue #187, [Context.ManagedResults]). It is what
// [resolver.isSymbolic] consults before deciding a managed reference has to
// take the resource-expansion route.
//
// It is deliberately coarser than [configs.lookupCoversTraversal], which
// decides the same question against the value itself and runs a moment
// later inside the evaluator: a reference this admits and that refuses ends
// up refused either way, with the evaluator's own message rather than the
// expansion path's. What it must not do is admit a bare whole-resource
// reference, which is the one shape the expansion path answers and an
// evaluated value cannot.
func (r *resolver) managedCovered(trav hcl.Traversal) bool {
	if r.dataIndex == nil {
		return false
	}
	byRes := r.dataIndex[r.modInst.String()]
	if len(byRes) == 0 {
		return false
	}
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() || len(ref.Remaining) == 0 {
		// A bare whole-resource reference - for_each = aws_subnet.this -
		// keeps the route it has always had, where the instance keys come
		// from the parent block's own expansion. Only a reference that goes
		// on to name an attribute has anything a read could answer.
		return false
	}
	var res addrs.Resource
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		res = subj
	case addrs.ResourceInstance:
		res = subj.Resource
	default:
		return false
	}
	if res.Mode != addrs.ManagedResourceMode {
		return false
	}
	_, ok := byRes[res.String()]
	return ok
}
