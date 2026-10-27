// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// gcpLabelMax is the most labels GCP lets one resource carry.
const gcpLabelMax = 64

// gcpAttributionLabel is the label hashicorp/google adds in the create call
// while add_terraform_attribution_label is true, its default.
const gcpAttributionLabel = "goog-terraform-provisioned"

// checkGCPLabelCount refuses a google resource whose labels, once this run
// adds its markers, come to more than GCP's 64. The markers are
// tofu-estate and tofu-address, and tofu-slot too on a count block, whose
// instances may be given a slot. A configuration stock creates with 62 to
// 64 labels would otherwise fail its create only because of the markers,
// so it is refused by name here rather than left to the API.
//
// The count is the union of the keys the block's own labels argument
// states, the keys its provider block's default_labels states (a resource
// key overrides a default of the same name, so neither is counted twice),
// the attribution label unless add_terraform_attribution_label is
// statically false, and the markers. A key whose VALUE is unknown is
// counted as present: the key is what takes a slot, whatever its value
// turns out to be. Only a map whose keys themselves cannot be read from
// configuration alone is skipped, silently, the default every lint rule
// gives an unknown.
//
// With provider schemas, only a type whose labels map carries the marker
// ([substrate.SurfaceOf]) is counted, so an instance template, whose labels
// propagate and are never stamped, is not. Without them, a google block
// that sets labels is counted; such a run refuses every google type as
// unadmitted-type anyway.
//
// AWS has the same cap at 50 tags, guarded by aws-tag-count (aws_tag_count.go).
func checkGCPLabelCount(ctx context.Context, cfg *configs.Config, schemas map[string]providers.Schema, scope identity.Scope, issues *[]Issue) {
	cfg.DeepEach(func(c *configs.Config) {
		if c.Module == nil {
			return
		}
		names := make([]string, 0, len(c.Module.ManagedResources))
		for name := range c.Module.ManagedResources {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rc := c.Module.ManagedResources[name]
			if !isGoogleProvider(rc.Provider) || rc.Config == nil {
				continue
			}
			if scopeExcludes(scope, c.Path, rc.Addr()) {
				continue
			}
			if issue, ok := gcpLabelCountIssue(ctx, cfg, c, rc, schemas); ok {
				*issues = append(*issues, issue)
			}
		}
	})
}

// gcpLabelCountIssue is [checkGCPLabelCount]'s verdict on one block.
func gcpLabelCountIssue(ctx context.Context, root, c *configs.Config, rc *configs.Resource, schemas map[string]providers.Schema) (Issue, bool) {
	labelsExpr, hasLabels := bodyAttrExpr(rc.Config, markers.GoogleLabelsAttr)
	if schema, ok := schemas[rc.Type]; ok && schema.Block != nil {
		if _, carries := substrate.SurfaceOf(rc.Provider.Type, schema.Block); !carries {
			return Issue{}, false
		}
	} else if !hasLabels {
		return Issue{}, false
	}

	keys := map[string]bool{}
	unknownValues := false
	if hasLabels {
		own, unknown, ok := staticLabelKeys(ctx, c.Module, labelsExpr)
		if !ok {
			return Issue{}, false
		}
		unknownValues = unknownValues || unknown
		for k := range own {
			keys[k] = true
		}
	}

	defaults, attribution, ok := gcpProviderLabelDefaults(ctx, root, c, rc)
	if !ok {
		return Issue{}, false
	}
	for k := range defaults {
		keys[k] = true
	}
	if attribution {
		keys[gcpAttributionLabel] = true
	}
	stock := len(keys)

	added := []string{markers.TagEstate, markers.TagAddress}
	if rc.Count != nil {
		added = append(added, markers.TagSlot)
	}
	for _, k := range added {
		keys[k] = true
	}
	if len(keys) <= gcpLabelMax {
		return Issue{}, false
	}

	addr := addrs.ConfigResource{Module: c.Path, Resource: rc.Addr()}.String()
	detail := fmt.Sprintf(
		"%s would carry %d labels once this run adds its ownership markers (%d from its labels, its provider's default_labels and the attribution label, which stock would send, plus the markers), and GCP refuses more than %d on one resource. "+
			"The create would fail at the API because of the markers alone if stock would send %d or fewer. Remove labels until the block carries at most %d before the markers, or set add_terraform_attribution_label = false on its provider to free one.",
		addr, len(keys), stock, gcpLabelMax, gcpLabelMax, gcpLabelMax-len(added))
	if unknownValues {
		detail += " Some labels' values are not known until apply; each of those keys is counted, since the key takes a slot whatever its value."
	}
	return Issue{
		Rule:      RuleGCPLabelCount,
		Construct: addr,
		Module:    c.Path,
		Detail:    detail,
		Subject:   rc.DeclRange,
	}, true
}

// gcpProviderLabelDefaults is the default_labels keys and the attribution
// setting of the provider block serving rc. No block, or a block that sets
// neither, is no defaults and attribution on. ok is false when the block
// has its own for_each or its default_labels keys cannot be read.
func gcpProviderLabelDefaults(ctx context.Context, root, c *configs.Config, rc *configs.Resource) (map[string]bool, bool, bool) {
	abs := providerscope.ResolveResource(c, rc)
	owner := root.Descendent(abs.Module)
	if owner == nil || owner.Module == nil {
		return nil, true, true
	}
	mod := owner.Module
	keys := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pc *configs.Provider
	for _, k := range keys {
		cand := mod.ProviderConfigs[k]
		if cand.Alias == abs.Alias && mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: cand.Name}) == abs.Provider {
			pc = cand
			break
		}
	}
	if pc == nil || pc.Config == nil {
		return nil, true, true
	}
	if pc.ForEach != nil {
		return nil, false, false
	}

	var defaults map[string]bool
	if expr, ok := bodyAttrExpr(pc.Config, "default_labels"); ok {
		got, _, ok := staticLabelKeys(ctx, mod, expr)
		if !ok {
			return nil, false, false
		}
		defaults = got
	}

	attribution := true
	if expr, ok := bodyAttrExpr(pc.Config, "add_terraform_attribution_label"); ok {
		// Counted unless statically false: an unknown setting may add the
		// label, and a key that may be present is counted as present.
		if val, ok := staticeval.EvaluateOK(ctx, mod.StaticEvaluator.Pure(), expr, "provider."+pc.Name+".add_terraform_attribution_label"); ok && val.IsWhollyKnown() && !val.IsNull() && !val.IsMarked() {
			if b, err := convert.Convert(val, cty.Bool); err == nil {
				attribution = b.True()
			}
		}
	}
	return defaults, attribution, true
}

// bodyAttrExpr is body's argument name, when it sets one.
func bodyAttrExpr(body hcl.Body, name string) (hcl.Expression, bool) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return nil, false
	}
	attr, ok := content.Attributes[name]
	if !ok {
		return nil, false
	}
	return attr.Expr, true
}

// staticLabelKeys is the set of keys a labels expression states, read from
// configuration alone. An object constructor is read key by key, so a key
// whose value is a reference to another resource still counts; unknown
// reports that one did. A merge() call is the union of its arguments. Any
// other expression is evaluated whole. ok is false when any key cannot be
// read.
func staticLabelKeys(ctx context.Context, mod *configs.Module, expr hcl.Expression) (keys map[string]bool, unknown bool, ok bool) {
	if mod == nil || mod.StaticEvaluator == nil {
		return nil, false, false
	}
	eval := mod.StaticEvaluator.Pure()
	keys = map[string]bool{}

	switch e := expr.(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			k, kok := staticeval.EvaluateOK(ctx, eval, item.KeyExpr, "labels key")
			if !kok || !k.IsWhollyKnown() || k.IsNull() || k.IsMarked() {
				return nil, false, false
			}
			ks, err := convert.Convert(k, cty.String)
			if err != nil {
				return nil, false, false
			}
			keys[ks.AsString()] = true
			if _, vok := staticeval.EvaluateOK(ctx, eval, item.ValueExpr, "labels value"); !vok {
				unknown = true
			}
		}
		return keys, unknown, true
	case *hclsyntax.FunctionCallExpr:
		if e.Name == "merge" && !e.ExpandFinal {
			for _, arg := range e.Args {
				got, u, aok := staticLabelKeys(ctx, mod, arg)
				if !aok {
					return nil, false, false
				}
				unknown = unknown || u
				for k := range got {
					keys[k] = true
				}
			}
			return keys, unknown, true
		}
	}

	val, vok := staticeval.EvaluateOK(ctx, eval, expr, "labels")
	if !vok || val.IsMarked() || !val.IsKnown() {
		return nil, false, false
	}
	if val.IsNull() {
		return keys, false, true
	}
	if !val.Type().IsMapType() && !val.Type().IsObjectType() {
		return nil, false, false
	}
	for it := val.ElementIterator(); it.Next(); {
		k, v := it.Element()
		keys[k.AsString()] = true
		if !v.IsWhollyKnown() {
			unknown = true
		}
	}
	return keys, unknown, true
}
