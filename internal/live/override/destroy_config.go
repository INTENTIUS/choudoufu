// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// DestroyAttributes returns the attributes of typeName whose configured
// value a destroy should use, from the overrides wrapped around p. It is
// empty for a provider no override wraps, so the caller can ask before it
// spends anything evaluating configuration.
func DestroyAttributes(ctx context.Context, p providers.Interface, typeName string) []string {
	w, ok := asWrapped(p)
	if !ok {
		return nil
	}
	set, _ := w.usable(ctx, typeName)
	var out []string
	for _, o := range set {
		out = append(out, o.DestroyFromConfig...)
	}
	return out
}

// DestroyConfig is the DestroyFromConfig hook. core's planDestroy calls it
// for a resource instance that is being destroyed while its configuration
// still exists, with the unmarked prior state and the evaluated
// configuration. For each attribute an override names, a known, non-null,
// unmarked configured value replaces the prior one, and the returned prior
// is what the destroy plan records and the delete is sent. A configured
// value that is unknown, null or sensitive leaves the prior value alone:
// stock behaviour, never a guess.
func DestroyConfig(ctx context.Context, p providers.Interface, typeName string, prior, config cty.Value) (cty.Value, tfdiags.Diagnostics) {
	w, ok := asWrapped(p)
	if !ok || prior.IsNull() || !prior.IsKnown() || prior.IsMarked() || config.IsNull() || !config.IsKnown() || config.IsMarked() {
		return prior, nil
	}
	if !prior.Type().IsObjectType() || !config.Type().IsObjectType() {
		return prior, nil
	}
	set, diags := w.usable(ctx, typeName)
	attrs := prior.AsValueMap()
	for _, o := range set {
		changed := false
		for _, name := range o.DestroyFromConfig {
			if !prior.Type().HasAttribute(name) || !config.Type().HasAttribute(name) {
				continue
			}
			cv := config.GetAttr(name)
			if cv.IsNull() || !cv.IsKnown() || cv.ContainsMarked() || !cv.Type().Equals(attrs[name].Type()) {
				continue
			}
			if cv.RawEquals(attrs[name]) {
				continue
			}
			attrs[name] = cv
			changed = true
		}
		if changed {
			diags = diags.Append(appliedWarning(o))
		}
	}
	return cty.ObjectVal(attrs), diags
}

// Unwrapper is a provider wrapper that hands back the provider it wraps,
// so the overrides below it are still found. internal/tofu's
// parallelProvider (the new runtime's -parallelism) is one.
type Unwrapper interface {
	UnwrapProvider() providers.Configured
}

// asWrapped finds the override wrapper under p. Wrap returns *wrapped, or
// *wrappedLister for a provider that can list, which every plugin provider
// can (#2324); a type assertion on *wrapped alone missed every real
// provider, so destroy-from-config never acted outside tests.
func asWrapped(p providers.Configured) (*wrapped, bool) {
	for i := 0; i < 8 && p != nil; i++ {
		switch v := p.(type) {
		case *wrapped:
			return v, true
		case *wrappedLister:
			return v.wrapped, true
		case Unwrapper:
			p = v.UnwrapProvider()
		default:
			return nil, false
		}
	}
	return nil, false
}
