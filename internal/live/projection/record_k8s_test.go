// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1188 asked whether a Kubernetes instance getting no record
// is a defect. Measured on kind (Kubernetes v1.36.1, hashicorp/kubernetes
// 3.2.1) it is not, and these tests pin the two halves of why.
//
// The measurement the tests reproduce: eleven instances over ten types
// applied under one live block with a local record store. Eight got a
// record file; seven of those carried a residue member and only three an
// identity member. The three were kubernetes_config_map, kubernetes_namespace
// and kubernetes_storage_class - three of the four hand rows #326 wrote into
// [identity.DefaultTable]. kubernetes_service_account, which is spelled
// without a version suffix exactly like those three and is admitted by the
// same object-metadata rule (#1064), got none, which is what rules out the
// version suffix as the thing that decides.

// k8sObjectMetaSchema is hashicorp/kubernetes 3.2.1's object-metadata shape,
// the same fixture internal/live/identity's metadata_test.go builds, with a
// top-level "id" and a flat argument beside it so the block is not identity
// attributes alone. Every metadata-shaped type in the provider shares it;
// the type NAME is the only thing that varies between the cases below, which
// is the point.
func k8sObjectMetaSchema(namespaced bool) providers.Schema {
	meta := map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Optional: true, Computed: true},
		"generation":       {Type: cty.Number, Computed: true},
		"labels":           {Type: cty.Map(cty.String), Optional: true},
		"annotations":      {Type: cty.Map(cty.String), Optional: true},
		"generate_name":    {Type: cty.String, Optional: true},
		"resource_version": {Type: cty.String, Computed: true},
		"uid":              {Type: cty.String, Computed: true},
	}
	if namespaced {
		meta["namespace"] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Optional: true, Computed: true},
				"data": {Type: cty.Map(cty.String), Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: configschema.Block{Attributes: meta}, Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

// k8sObjectMetaValue is what the provider hands back for such a type. An
// empty namespace makes it the cluster-scoped shape.
func k8sObjectMetaValue(namespace, name string) cty.Value {
	meta := map[string]cty.Value{
		"name":             cty.StringVal(name),
		"generation":       cty.NumberIntVal(1),
		"labels":           cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("rec1188")}),
		"annotations":      cty.NullVal(cty.Map(cty.String)),
		"generate_name":    cty.NullVal(cty.String),
		"resource_version": cty.StringVal("101"),
		"uid":              cty.StringVal("6f1d0b1e-0000-4000-8000-000000000001"),
	}
	id := name
	if namespace != "" {
		meta["namespace"] = cty.StringVal(namespace)
		id = namespace + "/" + name
	}
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal(id),
		"data":     cty.MapVal(map[string]cty.Value{"greeting": cty.StringVal("hello")}),
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(meta)}),
	})
}

// TestKubernetesIdentityRecordFollowsTheRatifiedRowNotTheVersionSuffix is
// GitHub issue #1188's first half. The issue read the split as versioned
// against unversioned - "a _v1 type is recorded differently from its
// unversioned twin". It is not: every case below is handed the SAME schema
// value, so the resource type name is the only input that differs, and the
// ones that record an identity are exactly the ones [identity.DefaultTable]
// has a hand row for.
//
// Proved red by adding a row for any of the refused names to
// internal/live/identity/table_generated.go, and equally by deleting one of
// the four - which is what makes this worth pinning: whether a Kubernetes
// type's record carries an identity at all is decided by a generated table
// nobody edits on purpose, and #1118's capability matrix needs to know it.
func TestKubernetesIdentityRecordFollowsTheRatifiedRowNotTheVersionSuffix(t *testing.T) {
	for _, tc := range []struct {
		typeName   string
		namespaced bool
		// wantImportID is "" when no identity is recordable at all.
		wantImportID string
	}{
		// The four hand rows, all spelled without a version suffix.
		{"kubernetes_config_map", true, "rec1188-a/cm"},
		{"kubernetes_namespace", false, "rec1188-a"},
		{"kubernetes_cluster_role_binding", false, "rec1188-a"},
		{"kubernetes_storage_class", false, "rec1188-a"},

		// Their _v1 twins, which are the same provider resource under its
		// current name and get nothing.
		{"kubernetes_config_map_v1", true, ""},
		{"kubernetes_namespace_v1", false, ""},

		// And an unversioned spelling with no row, which is the case that
		// rules the version suffix out: measured on the cluster,
		// kubernetes_service_account.sa_unver's record carried a residue
		// member and no identity member.
		{"kubernetes_service_account", true, ""},
		{"kubernetes_secret_v1", true, ""},
		{"kubernetes_deployment_v1", true, ""},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			_, ratified := identity.DefaultTable[tc.typeName]
			if ratified != (tc.wantImportID != "") {
				t.Fatalf("this case's premise moved: %s has a DefaultTable row = %v, but the case expects an identity record = %v",
					tc.typeName, ratified, tc.wantImportID != "")
			}

			schema := k8sObjectMetaSchema(tc.namespaced)
			ns := ""
			if tc.namespaced {
				ns = "rec1188-a"
			}
			name := "cm"
			if !tc.namespaced {
				name = "rec1188-a"
			}

			rec, ok := LocatedRecordFrom(tc.typeName, schema, k8sObjectMetaValue(ns, name))
			if tc.wantImportID == "" {
				if ok {
					t.Fatalf("%s recorded an identity %+v; measured on kind it records none, and nothing on the Kubernetes path reads one", tc.typeName, rec)
				}
				return
			}
			if !ok {
				t.Fatalf("%s recorded no identity; it has a ratified row and measured on kind it records %q", tc.typeName, tc.wantImportID)
			}
			if rec.ImportID != tc.wantImportID {
				t.Errorf("%s import id %q, want %q", tc.typeName, rec.ImportID, tc.wantImportID)
			}
		})
	}
}

// TestNoKubernetesTypeIsRecordBacked is issue #1188's second half, and the
// reason the first half costs nothing. `kind: object` - the only record kind
// that is delete authority, and the only one holding a value the cloud never
// had - is written for #73's record-backed types, which have no cloud object
// at all. A Kubernetes object is a cloud object, so no kubernetes_* type is
// in that population and the object member can never be non-empty for one.
//
// Proved red by setting RecordBacked on any kubernetes_* row.
func TestNoKubernetesTypeIsRecordBacked(t *testing.T) {
	for typeName, ti := range identity.DefaultTable {
		if len(typeName) < 11 || typeName[:11] != "kubernetes_" {
			continue
		}
		if ti.RecordBacked {
			t.Errorf("%s is RecordBacked; a Kubernetes object is a cloud object, so it has no residue for a kind=object record to hold", typeName)
		}
	}
}
