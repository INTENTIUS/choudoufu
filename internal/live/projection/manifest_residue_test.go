// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1190. The whole file is about one instance that recorded
// NOTHING - not a wrong value, nothing at all - and the two separate
// reasons it recorded nothing, each of which is enough on its own.
//
// The measurement behind it, on kind v1.36.1 with hashicorp/kubernetes
// 3.2.1: a `field_manager` block on a kubernetes_manifest planned as an
// in-place update on every plan, for ever, because the store held no record
// for the type at all and so the mechanism that already carries a
// config-only nested block (an aws type's `timeouts`) never ran for it.

// manifestResidueSchema is hashicorp/kubernetes 3.2.1's kubernetes_manifest
// as this file needs it: the two dynamic attributes [markers.ManifestSurface]
// is defined by, and the one config-only block the API server can never
// answer for. No "id", and no identity schema - which is the whole point,
// and what [residueIdentityAttrs] alone has no answer for.
func manifestResidueSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest":        {Type: cty.DynamicPseudoType, Required: true},
			"object":          {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
			"computed_fields": {Type: cty.List(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"field_manager": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":            {Type: cty.String, Optional: true},
						"force_conflicts": {Type: cty.Bool, Optional: true},
					},
				},
			},
		},
	}}
}

// manifestApplied is what an apply of one ConfigMap through
// kubernetes_manifest leaves in state, with a field_manager block whose
// other leaf is left unset - the shape the cluster measurement used.
func manifestApplied() cty.Value {
	manifest := cty.ObjectVal(map[string]cty.Value{
		"apiVersion": cty.StringVal("v1"),
		"kind":       cty.StringVal("ConfigMap"),
		"metadata": cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("fm-repro"),
			"namespace": cty.StringVal("meas"),
		}),
	})
	return cty.ObjectVal(map[string]cty.Value{
		"manifest":        manifest,
		"object":          manifest,
		"computed_fields": cty.NullVal(cty.List(cty.String)),
		"field_manager": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":            cty.StringVal("choudoufu:fm-repro"),
			"force_conflicts": cty.NullVal(cty.Bool),
		})}),
	})
}

// manifestProviderRead is hashicorp/kubernetes's own ReadResource for a
// kubernetes_manifest, to the three behaviours this classification depends
// on, each observed on the cluster:
//
//   - it REFUSES a prior whose `object` is null, with the diagnostic this
//     fake reproduces verbatim. That is why the identity-only prior has to
//     carry `object` and not just the manifest;
//   - it never sources field_manager from the API server (the server
//     records the manager that wrote a field, not the manager a request
//     asked for), so a prior holding none comes back holding none;
//   - being a plugin-framework provider, it echoes a prior's field_manager
//     back EXACTLY, unset leaf still null - it has no legacy SDK's
//     inability to say "unset".
func manifestProviderRead(prior cty.Value) (cty.Value, error) {
	if prior.GetAttr("object").IsNull() {
		return cty.NilVal, fmt.Errorf("Current state of resource has no 'object' attribute: This should not happen. The state may be incomplete or corrupted.")
	}
	live := manifestApplied().GetAttr("object")
	attrs := prior.AsValueMap()
	attrs["object"] = live
	return cty.ObjectVal(attrs), nil
}

// TestResidueStubIdentityAttrsNamesTheManifestAndTheLiveObject pins the two
// names [classifyResidue]'s identity-only prior has to keep for a
// manifest-shaped type, and that an ordinary type is untouched.
//
// Proven red by returning [residueIdentityAttrs] unchanged from
// [residueStubIdentityAttrs]: both manifest assertions fail.
func TestResidueStubIdentityAttrsNamesTheManifestAndTheLiveObject(t *testing.T) {
	got := residueStubIdentityAttrs(manifestResidueSchema())
	for _, name := range []string{"manifest", "object"} {
		if !got[name] {
			t.Errorf("%q is not named in the residue stub's identity attributes, so identityOnly nulls it and the provider's read either addresses nothing or refuses outright; got %v", name, got)
		}
	}

	// An ordinary type answers exactly as it did before this existed: the
	// widening is the manifest shape's and nothing else's.
	plain := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id":   {Type: cty.String, Computed: true},
			"name": {Type: cty.String, Required: true},
		},
	}}
	if before, after := residueIdentityAttrs(plain), residueStubIdentityAttrs(plain); len(before) != len(after) {
		t.Errorf("an ordinary type's stub identity attributes changed: %v -> %v", before, after)
	}
}

// TestClassifyResidueRecordsAManifestFieldManager is the whole of #1190 at
// the seam that failed: a manifest-shaped instance produces a residue
// record for its config-only block.
//
// Proven red twice, once for each half of the fix:
//
//   - with [residueStubIdentityAttrs] returning [residueIdentityAttrs]
//     unchanged, identityOnly refuses to build a prior at all ("the applied
//     object carries no identity to read by") and this returns ok=false
//     having issued no read;
//   - with read B's comparison back to normalizing only the applied side,
//     the read happens and field_manager is dropped anyway, because the
//     provider echoed force_conflicts = null where the normalized applied
//     value says false.
func TestClassifyResidueRecordsAManifestFieldManager(t *testing.T) {
	schema := manifestResidueSchema()
	applied := manifestApplied()

	attrs, ok := classifyResidueAll(schema, applied, strict.DefaultSecrets, manifestProviderRead, cty.NilVal)
	if !ok {
		t.Fatalf("classified nothing for a manifest-shaped instance, so its envelope is empty and no record is written at all - which is the perpetual field_manager diff of #1190")
	}
	got, held := attrs["field_manager"]
	if !held {
		t.Fatalf("field_manager was not recorded; got %v", residueAttrNames(attrs))
	}
	if want := applied.GetAttr("field_manager"); !got.RawEquals(want) {
		t.Errorf("field_manager recorded as %#v, want the applied value %#v", got, want)
	}

	// The two attributes the identity-only prior keeps are excluded from
	// being recorded, which for the manifest is also what keeps a
	// kind: Secret's body out of the record store.
	for _, name := range []string{"manifest", "object"} {
		if _, held := attrs[name]; held {
			t.Errorf("%q was recorded as residue; the whole live object belongs in the cluster, not in the record store", name)
		}
	}
}

// TestClassifyResidueAcceptsAnExactEchoWithANullLeaf is the second half on
// its own, away from the manifest shape: read B is satisfied by a provider
// echoing back exactly what was applied, unset leaf still null.
//
// [residueNormalizeSDKZeroLeaves] exists for a legacy-SDK provider that
// CANNOT say "unset" inside a block and answers "" / false / 0 instead. A
// plugin-framework provider has no such inability, and normalizing only the
// applied side turned the most faithful answer a provider can give into a
// mismatch. Proven red by dropping the `bv.RawEquals(want)` disjunct.
func TestClassifyResidueAcceptsAnExactEchoWithANullLeaf(t *testing.T) {
	schema := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"opts": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"name":  {Type: cty.String, Optional: true},
						"force": {Type: cty.Bool, Optional: true},
					},
				},
			},
		},
	}}
	optsTy := cty.List(cty.Object(map[string]cty.Type{"name": cty.String, "force": cty.Bool}))
	opts := cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"name":  cty.StringVal("chosen"),
		"force": cty.NullVal(cty.Bool),
	})})
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("i-1"),
		"opts": opts,
	})

	read := func(prior cty.Value) (cty.Value, error) {
		// Never sourced from the remote: whatever the prior held comes
		// back, byte for byte, null leaves included.
		out := prior.AsValueMap()
		if out["opts"].IsNull() {
			out["opts"] = cty.NullVal(optsTy)
		}
		out["id"] = cty.StringVal("i-1")
		return cty.ObjectVal(out), nil
	}

	attrs, ok := classifyResidueAll(schema, applied, strict.DefaultSecrets, read, cty.NilVal)
	if !ok {
		t.Fatalf("classified nothing at all")
	}
	got, held := attrs["opts"]
	if !held {
		t.Fatalf("opts was not recorded; got %v - a provider that answers a prior's block back exactly is the strongest possible read B, not a mismatch", residueAttrNames(attrs))
	}
	if !got.RawEquals(opts) {
		t.Errorf("opts recorded as %#v, want %#v", got, opts)
	}
}

// residueAttrNames renders a classified map for a failure message.
func residueAttrNames(attrs map[string]cty.Value) []string {
	out := make([]string, 0, len(attrs))
	for name := range attrs {
		out = append(out, name)
	}
	return out
}
