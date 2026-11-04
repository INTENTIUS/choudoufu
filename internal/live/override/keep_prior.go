// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
)

// Equivalence says when a configured value means the same as the prior one
// at Path, though the two are spelled differently.
type Equivalence struct {
	Path  cty.Path
	Equal func(prior, config cty.Value) bool
}

// KeepPriorWhenEquivalent is approach B as a request rewrite (see the
// package doc). For each equivalence that holds, the prior value replaces
// the configured one in Config and ProposedNewState before the provider
// plans, so an equivalent spelling plans as no change. It only acts on an
// update: a create has no prior value and a destroy has no configuration.
func KeepPriorWhenEquivalent(eqs ...Equivalence) PlanFunc {
	return func(ctx context.Context, inner providers.Interface, req providers.PlanResourceChangeRequest) (providers.PlanResourceChangeResponse, bool) {
		if req.PriorState.IsNull() || req.Config.IsNull() {
			return inner.PlanResourceChange(ctx, req), false
		}
		changed := false
		for _, eq := range eqs {
			prior, err := eq.Path.Apply(req.PriorState)
			if err != nil || prior.IsNull() || !prior.IsWhollyKnown() {
				continue
			}
			config, err := eq.Path.Apply(req.Config)
			if err != nil || config.IsNull() || !config.IsWhollyKnown() {
				continue
			}
			if prior.RawEquals(config) || !eq.Equal(prior, config) {
				continue
			}
			cfg, ok1 := setAt(req.Config, eq.Path, prior)
			prop, ok2 := setAt(req.ProposedNewState, eq.Path, prior)
			if !ok1 || !ok2 {
				continue
			}
			req.Config, req.ProposedNewState = cfg, prop
			changed = true
		}
		return inner.PlanResourceChange(ctx, req), changed
	}
}

// setAt returns v with the value at path replaced by nv. It follows
// attribute steps through objects and index steps through lists and tuples,
// which is how a nested block's attribute is addressed. It reports false for
// anything else, and the caller leaves the request alone.
func setAt(v cty.Value, path cty.Path, nv cty.Value) (cty.Value, bool) {
	if len(path) == 0 {
		return nv, true
	}
	if v.IsNull() || !v.IsKnown() {
		return v, false
	}
	switch step := path[0].(type) {
	case cty.GetAttrStep:
		if !v.Type().IsObjectType() || !v.Type().HasAttribute(step.Name) {
			return v, false
		}
		attrs := v.AsValueMap()
		child, ok := setAt(attrs[step.Name], path[1:], nv)
		if !ok {
			return v, false
		}
		attrs[step.Name] = child
		return cty.ObjectVal(attrs), true
	case cty.IndexStep:
		ty := v.Type()
		if (!ty.IsListType() && !ty.IsTupleType()) || step.Key.Type() != cty.Number {
			return v, false
		}
		i, acc := step.Key.AsBigFloat().Int64()
		elems := v.AsValueSlice()
		if acc != 0 || i < 0 || int(i) >= len(elems) {
			return v, false
		}
		child, ok := setAt(elems[i], path[1:], nv)
		if !ok {
			return v, false
		}
		elems[i] = child
		if ty.IsListType() {
			return cty.ListVal(elems), true
		}
		return cty.TupleVal(elems), true
	}
	return v, false
}

// SameStrings is an [Equivalence.Equal] for a list of strings the API treats
// as a set: equal when both hold the same strings, in any order.
func SameStrings(prior, config cty.Value) bool {
	a, okA := sortedStrings(prior)
	b, okB := sortedStrings(config)
	if !okA || !okB || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedStrings(v cty.Value) ([]string, bool) {
	if !v.CanIterateElements() {
		return nil, false
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()
		if e.IsNull() || !e.IsKnown() || e.Type() != cty.String {
			return nil, false
		}
		out = append(out, e.AsString())
	}
	sort.Strings(out)
	return out, true
}
