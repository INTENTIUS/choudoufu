// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// destroyFlagProof is red-then-green for a destroy-time boolean: applied as
// false, flipped to true in configuration. An unwrapped provider's destroy
// keeps false; the wrapped one sends true and names the override.
func destroyFlagProof(flag string) proof {
	schema := &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
		flag: {Type: cty.Bool, Optional: true},
	}}
	prior := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("x"), flag: cty.False})
	config := cty.ObjectVal(map[string]cty.Value{"id": cty.NullVal(cty.String), flag: cty.True})
	return proof{check: func(t *testing.T, o Override) {
		ctx := context.Background()
		// The fake's schema must carry every attribute the override
		// requires, or usable() skips it.
		full := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
		for k, v := range schema.Attributes {
			full.Attributes[k] = v
		}
		for path, ty := range o.Requires {
			if _, ok := full.Attributes[path]; !ok {
				full.Attributes[path] = &configschema.Attribute{Type: ty, Optional: true}
			}
		}
		p, c := widen(prior, full), widen(config, full)
		fake := &fakeProvider{typeName: o.Type, proof: proof{schema: full}}
		off, _ := DestroyConfig(ctx, fake, o.Type, p, c)
		if !off.GetAttr(flag).RawEquals(cty.False) {
			t.Fatalf("without the override the destroy already used the configured %s", flag)
		}
		on, diags := DestroyConfig(ctx, Wrap(fake, map[string][]Override{o.Type: {o}}), o.Type, p, c)
		if !on.GetAttr(flag).RawEquals(cty.True) {
			t.Errorf("with the override the destroy still sends %s = %#v", flag, on.GetAttr(flag))
		}
		if !hasWarning(diags, "Provider override applied", o.Name) {
			t.Errorf("the destroy does not name the override that changed it: %#v", diags)
		}
	}}
}

// widen adds every attribute of block that v lacks, as null.
func widen(v cty.Value, block *configschema.Block) cty.Value {
	attrs := v.AsValueMap()
	for name, a := range block.Attributes {
		if _, ok := attrs[name]; !ok {
			attrs[name] = cty.NullVal(a.Type)
		}
	}
	return cty.ObjectVal(attrs)
}

func init() {
	proofs["aws-db-instance-destroy-from-config"] = destroyFlagProof("skip_final_snapshot")
	proofs["aws-s3-bucket-destroy-from-config"] = destroyFlagProof("force_destroy")
	proofs["aws-iam-user-destroy-from-config"] = destroyFlagProof("force_destroy")
	proofs["aws-route53-zone-destroy-from-config"] = destroyFlagProof("force_destroy")
	proofs["aws-ecr-repository-destroy-from-config"] = destroyFlagProof("force_delete")
}
