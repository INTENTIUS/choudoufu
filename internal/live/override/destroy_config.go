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
	w, ok := p.(*wrapped)
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
	w, ok := p.(*wrapped)
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
