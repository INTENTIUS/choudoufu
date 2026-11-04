// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Wrap returns inner with every override in byType applied to the calls
// that reach it. byType is [ForProvider]'s result for inner's provider. With
// nothing to apply, Wrap returns inner itself, so a provider no override
// touches is exactly the provider it was.
func Wrap(inner providers.Interface, byType map[string][]Override) providers.Interface {
	if len(byType) == 0 {
		return inner
	}
	return &wrapped{Interface: inner, byType: byType}
}

type wrapped struct {
	providers.Interface
	byType map[string][]Override

	schemaOnce sync.Once
	schema     map[string]providers.Schema
}

// usable splits a type's overrides into those whose declared attributes
// match the loaded schema and a warning for each that does not.
func (w *wrapped) usable(ctx context.Context, typeName string) ([]Override, tfdiags.Diagnostics) {
	set := w.byType[typeName]
	if len(set) == 0 {
		return nil, nil
	}
	w.schemaOnce.Do(func() {
		w.schema = w.Interface.GetProviderSchema(ctx).ResourceTypes
	})
	var ok []Override
	var diags tfdiags.Diagnostics
	for _, o := range set {
		if problem := schemaProblem(w.schema[typeName].Block, o); problem != "" {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning,
				"Provider override skipped",
				fmt.Sprintf("The provider override %q was not applied to %s: %s. It was written against %s %s, and stays off until it is updated for this schema.",
					o.Name, typeName, problem, o.Provider, o.ProvenOn)))
			continue
		}
		ok = append(ok, o)
	}
	return ok, diags
}

// schemaProblem says why block does not carry what o declares, or "".
func schemaProblem(block *configschema.Block, o Override) string {
	if block == nil {
		return "the provider has no schema for this resource type"
	}
	for path, want := range o.Requires {
		b := block
		steps := strings.Split(path, ".")
		for i, step := range steps {
			if i < len(steps)-1 {
				nb, ok := b.BlockTypes[step]
				if !ok {
					return fmt.Sprintf("%s is not a nested block", strings.Join(steps[:i+1], "."))
				}
				b = &nb.Block
				continue
			}
			attr, ok := b.Attributes[step]
			if !ok {
				return fmt.Sprintf("%s is not an attribute", path)
			}
			if got := attr.ImpliedType(); !got.Equals(want) {
				return fmt.Sprintf("%s is %s, not %s", path, got.FriendlyName(), want.FriendlyName())
			}
		}
	}
	return ""
}

func (w *wrapped) PlanResourceChange(ctx context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	set, skipped := w.usable(ctx, req.TypeName)
	resp, applied := planChain(ctx, w.Interface, set, req)
	resp.Diagnostics = resp.Diagnostics.Append(skipped)
	for _, o := range applied {
		resp.Diagnostics = resp.Diagnostics.Append(appliedWarning(o))
	}
	return resp
}

// planChain runs set's Plan hooks in order, each over the next, ending at
// inner. It returns the response and the overrides that changed it.
func planChain(ctx context.Context, inner providers.Interface, set []Override, req providers.PlanResourceChangeRequest) (providers.PlanResourceChangeResponse, []Override) {
	for len(set) > 0 && set[0].Plan == nil {
		set = set[1:]
	}
	if len(set) == 0 {
		return inner.PlanResourceChange(ctx, req), nil
	}
	next := &chained{Interface: inner, rest: set[1:]}
	resp, changed := set[0].Plan(ctx, next, req)
	applied := next.applied
	if changed {
		applied = append([]Override{set[0]}, applied...)
	}
	return resp, applied
}

func (w *wrapped) ApplyResourceChange(ctx context.Context, req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
	set, skipped := w.usable(ctx, req.TypeName)
	resp, applied := applyChain(ctx, w.Interface, set, req)
	resp.Diagnostics = resp.Diagnostics.Append(skipped)
	for _, o := range applied {
		resp.Diagnostics = resp.Diagnostics.Append(appliedWarning(o))
	}
	return resp
}

// applyChain is [planChain] for ApplyResourceChange.
func applyChain(ctx context.Context, inner providers.Interface, set []Override, req providers.ApplyResourceChangeRequest) (providers.ApplyResourceChangeResponse, []Override) {
	for len(set) > 0 && set[0].Apply == nil {
		set = set[1:]
	}
	if len(set) == 0 {
		return inner.ApplyResourceChange(ctx, req), nil
	}
	next := &chained{Interface: inner, rest: set[1:]}
	resp, changed := set[0].Apply(ctx, next, req)
	applied := next.applied
	if changed {
		applied = append([]Override{set[0]}, applied...)
	}
	return resp, applied
}

// chained is the provider one override's hook sees: the real provider with
// the remaining overrides still applied.
type chained struct {
	providers.Interface
	rest    []Override
	applied []Override
}

func (c *chained) PlanResourceChange(ctx context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	resp, applied := planChain(ctx, c.Interface, c.rest, req)
	c.applied = append(c.applied, applied...)
	return resp
}

func (c *chained) ApplyResourceChange(ctx context.Context, req providers.ApplyResourceChangeRequest) providers.ApplyResourceChangeResponse {
	resp, applied := applyChain(ctx, c.Interface, c.rest, req)
	c.applied = append(c.applied, applied...)
	return resp
}

func appliedWarning(o Override) tfdiags.Diagnostic {
	return tfdiags.Sourceless(tfdiags.Warning,
		"Provider override applied",
		fmt.Sprintf("choudoufu's provider override %q acted on this resource. %s (%s). Set %s=-%s to run without it, or %s=off to run without any override.",
			o.Name, o.Summary, o.Upstream, EnvVar, o.Name, EnvVar))
}
