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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// The arguments a field-granular read takes from its prior and nowhere else
// (#1885: reference-k8s-shared-objects' and corpus-govuk-cluster-services'
// test_plan, a stateless plan after a migration off stock state files).
//
// hashicorp/kubernetes 3.2.1's reads of kubernetes_labels,
// kubernetes_annotations, kubernetes_config_map_v1_data,
// kubernetes_secret_v1_data, kubernetes_env and kubernetes_node_taint read
// the live object and set only the fields they write. Everything else in
// the state they return is the prior's own value, and some of it steers the
// read:
//
//   - field_manager (all six; node_taint's read ignores it) picks the
//     managedFields entry whose fields are kept. [fieldGranularSeed] sets it
//     to the estate's manager. A residue record live-import wrote off the
//     stock state holds "Terraform", and because the read only echoes the
//     seed, [fillResidue] took the echo for "no information" and put the
//     stock manager back - a perpetual field_manager change on every block.
//     [withoutEstateManagerResidue] keeps the record out of that one
//     argument: the estate's manager is the marker, not a value a previous
//     apply chose.
//   - force, api_version, kind, container and init_container are flat and
//     Optional or Required without Computed, so [configuredAttrsSeed]
//     already seeds them from configuration.
//   - kubernetes_env's env names: the read keeps exactly the env vars the
//     prior names, because 3.2.1's getManagedEnvs returns nil on the first
//     managedFields entry it matches and so never reports a var as managed.
//     [configuredEnvNamesSeed] seeds the configured names. Without it every
//     stateless read of kubernetes_env came back empty and planned a create.
//     This reads back the configured vars whoever owns them - stock's read
//     with a state file in hand does the same, and the plan-time boundary
//     (discovery.CheckKubernetesFieldOwners) is what names another owner.
//   - the metadata block (name, namespace). None of the reads sets it. The
//     five types without an Importer get it from [fieldGranularStub];
//     kubernetes_env has ImportStatePassthroughContext, whose stub is the id
//     alone, so its prior's metadata was empty and the plan proposed a new
//     object (name is ForceNew). [withFieldGranularMetadata] places it from
//     the resolved identity, the same values [fieldGranularStub] uses.

// withoutEstateManagerResidue returns attrs without a field_manager entry,
// copying rather than editing: the record store's run cache may hand the
// same map to another caller.
func withoutEstateManagerResidue(attrs map[string]cty.Value) map[string]cty.Value {
	if _, ok := attrs[substrate.FieldManagerAttr]; !ok {
		return attrs
	}
	out := make(map[string]cty.Value, len(attrs))
	for k, v := range attrs {
		if k != substrate.FieldManagerAttr {
			out[k] = v
		}
	}
	return out
}

// fieldGranularEnvBlock is the written env block's name.
const fieldGranularEnvBlock = "env"

// configuredEnvNamesSeed statically evaluates the names of the
// configuration's env blocks and returns them as the env list the read's
// prior would have carried, each element its block's empty value with the
// name set. ok is false when the schema has no env block or any env name is
// not statically known: a partial list would read back a partial env and
// plan the rest as additions, which is no better than reading none.
func configuredEnvNamesSeed(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (seed cty.Value, ok bool) {
	if eval == nil || rc == nil || rc.Config == nil || schema.Block == nil {
		return cty.NilVal, false
	}
	nested, has := schema.Block.BlockTypes[fieldGranularEnvBlock]
	if !has || nested == nil || nested.Nesting != configschema.NestingList {
		return cty.NilVal, false
	}
	if a, has := nested.Block.Attributes["name"]; !has || a == nil || a.Type != cty.String {
		return cty.NilVal, false
	}
	defer func() {
		if rec := recover(); rec != nil {
			seed, ok = cty.NilVal, false
		}
	}()

	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{{Type: fieldGranularEnvBlock}},
	})
	if diags.HasErrors() || len(content.Blocks) == 0 {
		return cty.NilVal, false
	}
	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	spec := hcldec.ObjectSpec{"name": &hcldec.AttrSpec{Name: "name", Type: cty.String}}
	empty := nested.Block.EmptyValue()
	elems := make([]cty.Value, 0, len(content.Blocks))
	for _, blk := range content.Blocks {
		refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(blk.Body, spec))
		if refDiags.HasErrors() {
			return cty.NilVal, false
		}
		hclCtx, ctxDiags := eval.EvalContext(ctx, ident, refs)
		if ctxDiags.HasErrors() {
			return cty.NilVal, false
		}
		if hclCtx == nil {
			hclCtx = &hcl.EvalContext{}
		}
		val, _, valDiags := hcldec.PartialDecode(blk.Body, spec, hclCtx)
		if valDiags.HasErrors() || val == cty.NilVal || val.IsNull() {
			return cty.NilVal, false
		}
		// Unmarked: the seed crosses the plugin channel, which refuses a
		// marked value. An env var's name is never the secret.
		name, _ := val.GetAttr("name").UnmarkDeep()
		if name.IsNull() || !name.IsKnown() {
			return cty.NilVal, false
		}
		el := map[string]cty.Value{}
		for it := empty.ElementIterator(); it.Next(); {
			k, v := it.Element()
			el[k.AsString()] = v
		}
		el["name"] = name
		elems = append(elems, cty.ObjectVal(el))
	}
	return cty.ListVal(elems), true
}

// fieldGranularMetadata is the single metadata item a field-granular
// prior carries: the patched object's name and, when the identity has one,
// its namespace, both from the resolved identity values. Every other leaf
// is null.
func fieldGranularMetadata(nested *configschema.NestedBlock, values map[string]string) cty.Value {
	metaAttrs := map[string]cty.Value{}
	for name, ty := range nested.Block.ImpliedType().AttributeTypes() {
		v, has := values[name]
		if has && (name == "name" || name == "namespace") && ty == cty.String {
			metaAttrs[name] = cty.StringVal(v)
			continue
		}
		metaAttrs[name] = cty.NullVal(ty)
	}
	return cty.ListVal([]cty.Value{cty.ObjectVal(metaAttrs)})
}

// withFieldGranularMetadata places [fieldGranularMetadata] on an imported
// field-granular stub whose metadata is null or empty - kubernetes_env's,
// whose importer sets the id alone. A stub that already carries a metadata
// item keeps it. ok is false when nothing was placed.
func withFieldGranularMetadata(v cty.Value, schema providers.Schema, values map[string]string) (cty.Value, bool) {
	if schema.Block == nil || values["name"] == "" || v == cty.NilVal || v.IsNull() || !v.IsKnown() || v.IsMarked() || !v.Type().IsObjectType() || !v.Type().HasAttribute("metadata") {
		return v, false
	}
	nested, ok := schema.Block.BlockTypes["metadata"]
	if !ok || nested == nil {
		return v, false
	}
	cur := v.GetAttr("metadata")
	if cur.IsMarked() || !cur.IsKnown() || (!cur.IsNull() && cur.LengthInt() > 0) {
		return v, false
	}
	attrs := map[string]cty.Value{}
	for name := range v.Type().AttributeTypes() {
		attrs[name] = v.GetAttr(name)
	}
	attrs["metadata"] = fieldGranularMetadata(nested, values)
	return cty.ObjectVal(attrs), true
}
