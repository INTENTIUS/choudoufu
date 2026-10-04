// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestNodeResolver_FieldGranularNeverResolvesAnImport (epic #1885, measured
// on kind by reference-k8s-shared-objects' plan_approval): a field-granular
// instance that reaches the node with no prior state is one the pre-walk
// projection read under this estate's field manager and found owning
// nothing - a new block on an object that exists, here kubernetes_labels on
// the cluster's "default" Namespace, where another estate owns a label. Its
// identity is the patched object, which exists, so every step of the
// resolver could name it; an import there asks the provider for an Importer
// five of the six types do not have ("Resource type has no classic
// Importer") and, for kubernetes_env, reads under the provider's default
// manager rather than the estate's. The instance's create is the
// server-side-apply patch of its own fields, which the field-owner pass
// then judges, so the resolver must answer not-found at every step.
func TestNodeResolver_FieldGranularNeverResolvesAnImport(t *testing.T) {
	ctx := context.Background()
	stringMap := &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
	schema := clusterScopedFieldGranularSchema(true, true, map[string]*configschema.Attribute{"labels": stringMap})
	addr := locatedTestAddr(t, "kubernetes_labels", "over_platform")
	const id = "apiVersion=v1,kind=Namespace,name=default"

	config := cty.ObjectVal(map[string]cty.Value{
		"api_version":   cty.StringVal("v1"),
		"kind":          cty.StringVal("Namespace"),
		"labels":        cty.MapVal(map[string]cty.Value{"shared-objects/platform": cty.StringVal("app")}),
		"field_manager": cty.StringVal("choudoufu:shared-app"),
		"force":         cty.NullVal(cty.Bool),
		"id":            cty.UnknownVal(cty.String),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("default"),
			"namespace": cty.NullVal(cty.String),
		})}),
	})

	store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("shared-app"))
	if _, err := store.mergeEnvelope(ctx, addr, "", func(env *recordEnvelope) {
		env.Identity = &identityPayload{ImportID: id}
	}); err != nil {
		t.Fatalf("mergeEnvelope: %s", err)
	}

	cases := map[string]*NodeResolver{
		"identity table over the evaluated value": {Estate: "shared-app"},
		"marker index": {Estate: "shared-app", MarkerIndex: map[string]providers.ImportTarget{
			addr.String(): {ID: id},
		}},
		"record": {Estate: "shared-app", RecordStore: store},
	}
	for name, resolver := range cases {
		t.Run(name, func(t *testing.T) {
			target, found, diags := resolver.ResolveResourceIdentity(ctx, addr, config, schema)
			if diags.HasErrors() {
				t.Fatalf("unexpected diagnostics: %s", diags.Err())
			}
			if found {
				t.Fatalf("resolved %#v for a field-granular instance with no prior state; want not-found, so the node plans its create", target)
			}
		})
	}

	// The control: the same resolver over a whole-object type still finds
	// the marker index's answer, so the gate is the field-granular shape
	// and not the resolver being switched off.
	whole := locatedTestAddr(t, "aws_instance", "web")
	resolver := &NodeResolver{MarkerIndex: map[string]providers.ImportTarget{whole.String(): {ID: "i-0123456789abcdef0"}}}
	if _, found, _ := resolver.ResolveResourceIdentity(ctx, whole, cty.EmptyObjectVal, providers.Schema{}); !found {
		t.Fatalf("a whole-object type's marker index hit is no longer found")
	}
}
