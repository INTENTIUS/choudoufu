// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1262, which is #1178's mechanism one case further out.
//
// [configuredAttrSeed] evaluates an argument with a strict evaluator:
// [configs.StaticEvaluator.EvalContext] fails outright the moment one
// reference it was asked to resolve cannot be, so the argument is seeded
// whole or not at all. #1178 taught that evaluator an instance's own
// count.index and each.key. A reference to ANOTHER RESOURCE is the same shape
// of expression and still refuses, so a manifest with one such leaf anywhere
// in it - an annotation naming a sibling, which is the issue's reproduction -
// lost the whole `manifest` argument from the seed.
//
// On a manifest-surface type that is not a missing optimisation. No read
// answers for `manifest` (seedrepetition.go says why at length), the
// estate's marker is read from manifest.metadata.labels, and a prior with no
// manifest is a prior with no marker map: [builder.checkOwnership] refuses
// the instance UNOWNED and the plan proposes creating an object this estate
// applied one command earlier.
//
// # What the seed is when part of the manifest cannot be resolved
//
// The resolvable skeleton, with every unresolvable leaf left OPEN.
//
// [partialManifestSeed] evaluates the argument a second time with
// [configs.StaticEvaluator.WithUnknownForRefusedReferences], which is stock's
// own plan-time rule: a reference that cannot be answered yet is an unknown,
// and everything the configuration wrote down beside it still has a value.
// The substitution is an unknown and never a guess, so every KNOWN part of
// the result is independent of every reference that was refused. That gives
// the manifest's literal structure - which is where the labels map, and so
// the marker, lives.
//
// A prior state has no unknowns in it, so each unknown leaf goes to the
// provider as a null and its path is remembered. After the read,
// [fillManifestOpenPaths] gives each remembered path the LIVE object's value
// at the same path. That is the argument #1177's mirror already makes for
// labels and annotations, extended to the leaves configuration could not
// answer: a state file holds what was last applied, and for a leaf this run
// cannot evaluate the live object is the only witness to what that was. When
// the object is as it was applied, the filled prior equals what the plan-time
// evaluator makes of the block and the provider plans nothing; when it has
// drifted, the prior differs from the configuration and the provider plans
// the correction, which is what a plan is for. A leaf the live object does
// not answer with a plain string, number or bool stays null, and costs at
// worst an in-place update proposing the configured value. It never costs a
// create.
//
// # What it refuses, and what happens then
//
// Identity. apiVersion, kind, metadata.name and metadata.namespace are what
// the object IS (GitHub issue #1016), and this pass fills leaves from
// whatever object the read returned. Filling an identity leaf that way would
// make the prior manifest agree with the live object by construction, which
// is the one thing a seed must never be in a position to do
// ([configuredAttrsSeed]'s own rule for identity attributes). So a partial
// manifest whose identity is not wholly known from configuration is not
// seeded at all, and the instance behaves exactly as it did before this file
// existed: it reads back without a marker map, is refused UNOWNED with the
// warning that names it, and stays out of the prior. Which object an instance
// binds to is decided by the resolver's import id alone, before and after
// this change; nothing here can move it.
//
// count.index, each.key, each.value and provider functions keep refusing
// under the tolerant evaluator (see its own doc comment), so an expanded
// block still needs [builder.seedRepetition] and gets no guess in its place.
// A partial value carrying a sensitivity mark is declined too: the known-seed
// path returns such marks to be put back after the read, and an open path
// under a mark is not something this pass takes apart.
//
// # Why it is keyed on the schema shape and nothing else
//
// [markers.ManifestSurface] is the one definition of the shape, the same one
// the stamp, the mirror and identity resolution use. The fill reads the
// computed live attribute beside the dynamic argument, which only that shape
// has; on any other type an unresolvable argument is left to the provider's
// own read, as it always was.

// partialManifestSeed returns the manifest-surface argument's resolvable
// skeleton with every unknown leaf nulled, and the paths of those leaves
// relative to the argument's own value. ok is false whenever the argument is
// not one this pass will seed; see this file's doc comment for each reason.
//
// It is called only when the strict seed produced no manifest, so it can
// widen what is seeded and never change a value that was already seeded.
func partialManifestSeed(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (seed cty.Value, open []cty.Path, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			seed, open, ok = cty.NilVal, nil, false
		}
	}()

	if eval == nil || rc == nil || !markers.ManifestSurface(schema.Block) {
		return cty.NilVal, nil, false
	}
	name := markers.ManifestSurfaceAttr
	attr := schema.Block.Attributes[name]

	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	spec := hcldec.ObjectSpec{
		name: &hcldec.AttrSpec{Name: name, Type: attr.Type, Required: false},
	}
	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(rc.Config, spec))
	if refDiags.HasErrors() {
		return cty.NilVal, nil, false
	}
	hclCtx, ctxDiags := eval.WithUnknownForRefusedReferences(nil).EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, nil, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	configVal, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || configVal == cty.NilVal || configVal.IsNull() || !configVal.Type().HasAttribute(name) {
		return cty.NilVal, nil, false
	}
	manifest := configVal.GetAttr(name)
	if manifest.IsNull() || !manifest.IsKnown() || manifest.ContainsMarked() || !manifest.Type().IsObjectType() {
		return cty.NilVal, nil, false
	}
	if !manifestIdentityKnown(manifest) {
		return cty.NilVal, nil, false
	}

	nulled, err := cty.Transform(manifest, func(p cty.Path, v cty.Value) (cty.Value, error) {
		if v.IsKnown() {
			return v, nil
		}
		open = append(open, p.Copy())
		return cty.NullVal(v.Type()), nil
	})
	if err != nil || !nulled.IsWhollyKnown() {
		return cty.NilVal, nil, false
	}
	return nulled, open, true
}

// manifestIdentityKnown reports whether configuration alone states which
// object this manifest is: apiVersion, kind and metadata.name as known
// strings, and metadata.namespace known wherever the manifest writes one (a
// cluster-scoped kind writes none, which is an answer and not a gap).
func manifestIdentityKnown(manifest cty.Value) bool {
	str := func(v cty.Value, name string) bool {
		if !v.Type().HasAttribute(name) {
			return false
		}
		got := v.GetAttr(name)
		return got.IsKnown() && !got.IsNull() && got.Type() == cty.String
	}
	if !str(manifest, "apiVersion") || !str(manifest, "kind") || !manifest.Type().HasAttribute(markers.LabelSurfaceBlock) {
		return false
	}
	meta := manifest.GetAttr(markers.LabelSurfaceBlock)
	if meta.IsNull() || !meta.IsKnown() || !meta.Type().IsObjectType() || !str(meta, "name") {
		return false
	}
	if meta.Type().HasAttribute("namespace") && !meta.GetAttr("namespace").IsWhollyKnown() {
		return false
	}
	return true
}

// fillManifestOpenPaths is the read side of [partialManifestSeed]: every
// path the seed left open takes the live object's value at the same path,
// when the live object answers it with a plain string, number or bool. A
// path it does not answer that way stays null. It runs before
// [mirrorManifestComputedFields], which keeps the last word on
// metadata.labels and metadata.annotations.
//
// v is returned as it was for every value that is not a manifest-surface
// object read back whole, and whenever there is nothing open - which is every
// read that is not one of these.
func fillManifestOpenPaths(v cty.Value, block *configschema.Block, open []cty.Path) (out cty.Value) {
	out = v
	defer func() {
		if rec := recover(); rec != nil {
			out = v
		}
	}()

	if len(open) == 0 || !markers.ManifestSurface(block) || v == cty.NilVal || v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsObjectType() {
		return v
	}
	if !v.Type().HasAttribute(markers.ManifestSurfaceAttr) || !v.Type().HasAttribute(markers.ManifestLiveAttr) {
		return v
	}
	manifest := v.GetAttr(markers.ManifestSurfaceAttr)
	live := v.GetAttr(markers.ManifestLiveAttr)
	if manifest.IsNull() || !manifest.IsWhollyKnown() || manifest.ContainsMarked() || !manifest.Type().IsObjectType() {
		return v
	}
	if live.IsNull() || !live.IsKnown() {
		return v
	}

	fills := make(map[int]cty.Value, len(open))
	for i, p := range open {
		cur, err := p.Apply(manifest)
		if err != nil || !cur.IsNull() {
			// Not the leaf the seed left open any more: a provider that
			// rewrote the manifest it was handed owns what it wrote.
			continue
		}
		got, ok := liveValueAt(live, p)
		if !ok {
			continue
		}
		if want := cur.Type(); want != cty.DynamicPseudoType {
			conv, err := convert.Convert(got, want)
			if err != nil || conv.IsNull() || !conv.IsKnown() {
				continue
			}
			got = conv
		}
		fills[i] = got
	}
	if len(fills) == 0 {
		return v
	}

	filled, err := cty.Transform(manifest, func(p cty.Path, cur cty.Value) (cty.Value, error) {
		for i, val := range fills {
			if p.Equals(open[i]) {
				return val, nil
			}
		}
		return cur, nil
	})
	if err != nil {
		return v
	}
	attrs := v.AsValueMap()
	attrs[markers.ManifestSurfaceAttr] = filled
	return cty.ObjectVal(attrs)
}

// liveValueAt walks the live object along a path written against the
// CONFIGURED manifest. The two are typed differently on purpose - an object
// constructor's attributes on one side, the kind's OpenAPI maps and lists on
// the other - so an attribute step is also tried as a string key and an
// index step is taken on whatever collection is there. Only a known,
// unmarked, non-null primitive is an answer.
func liveValueAt(live cty.Value, path cty.Path) (cty.Value, bool) {
	cur := live
	for _, step := range path {
		if cur.IsNull() || !cur.IsKnown() || cur.IsMarked() {
			return cty.NilVal, false
		}
		var key cty.Value
		switch s := step.(type) {
		case cty.GetAttrStep:
			if cur.Type().IsObjectType() {
				if !cur.Type().HasAttribute(s.Name) {
					return cty.NilVal, false
				}
				cur = cur.GetAttr(s.Name)
				continue
			}
			key = cty.StringVal(s.Name)
		case cty.IndexStep:
			key = s.Key
			if key.IsMarked() {
				return cty.NilVal, false
			}
			if cur.Type().IsObjectType() && key.Type() == cty.String && key.IsKnown() && !key.IsNull() {
				name := key.AsString()
				if !cur.Type().HasAttribute(name) {
					return cty.NilVal, false
				}
				cur = cur.GetAttr(name)
				continue
			}
		default:
			return cty.NilVal, false
		}
		if cur.Type().IsSetType() || !cur.CanIterateElements() || key.IsNull() || !key.IsKnown() || key.IsMarked() {
			return cty.NilVal, false
		}
		has := cur.HasIndex(key)
		if has.IsMarked() || !has.IsKnown() || has.False() {
			return cty.NilVal, false
		}
		cur = cur.Index(key)
	}
	if cur.IsNull() || !cur.IsKnown() || cur.IsMarked() || !cur.Type().IsPrimitiveType() {
		return cty.NilVal, false
	}
	return cur, true
}
