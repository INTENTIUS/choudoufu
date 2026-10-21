// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120's first Azure lane run: corpus-azapi-role-assignment's
// test_plan refused azapi_resource.roleAssignments with "Unmarked live
// object at the declared id would be overwritten", telling the operator to
// write tofu-estate and tofu-address onto a Microsoft.Authorization/
// roleAssignments object, which takes no tags at any API version. Such an
// object's ownership is its record, as an untaggable AWS or azurerm type's
// is: a record that binds it admits it, and the refusal without one names
// the record, not tags.

const armRoleID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Authorization/roleAssignments/6faae21a-0cd6-4536-8c23-a278823d12ed"

// azapiShapedSchema is azapi_resource narrowed to what armTagTarget reads:
// a settable type, a dynamic body and a tags map.
func azapiShapedSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":        {Type: cty.String, Computed: true},
		"type":      {Type: cty.String, Required: true},
		"name":      {Type: cty.String, Optional: true, Computed: true},
		"parent_id": {Type: cty.String, Optional: true, Computed: true},
		"body":      {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
		"tags":      {Type: cty.Map(cty.String), Optional: true, Computed: true},
	}}}
}

func azapiReadObject(id, typ string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(id),
		"type":      cty.StringVal(typ),
		"name":      cty.StringVal("z"),
		"parent_id": cty.StringVal("/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"),
		"body":      cty.EmptyObjectVal,
		// azapi's read of an object with no tags: an empty map.
		"tags": cty.MapValEmpty(cty.String),
	})
}

func TestARMOverwrite_untaggableAzapiType(t *testing.T) {
	const roleType = "Microsoft.Authorization/roleAssignments@2022-04-01"
	addr := armOverwriteAddr(t, "azapi_resource")

	t.Run("a record binding it admits it", func(t *testing.T) {
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		// recordFirst, at the declared key: the binding live-import's
		// record gives the next plan.
		got := b.checkOwnershipAt(addr, "azapi_resource", armRoleID, azapiShapedSchema(), azapiReadObject(armRoleID, roleType), true, false, true, true)
		if got != ownershipOK || len(b.diags) != 0 {
			t.Errorf("verdict %d, want ownershipOK with nothing said; diags:\n%s", got, renderDiags(b.diags))
		}
	})

	for name, withStore := range map[string]bool{"no record in the store": true, "no record_store": false} {
		t.Run(name, func(t *testing.T) {
			b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
			if withStore {
				raw, err := staterecord.NewLocalStore(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				b.opts.RecordStore = NewRecordEnvelopeStore(raw, RecordKeyPrefix(k8sOwnershipEstate))
			}
			got := b.checkOwnership(addr, "azapi_resource", armRoleID, azapiShapedSchema(), azapiReadObject(armRoleID, roleType), true, false, false)
			if got != ownershipUnowned {
				t.Fatalf("verdict %d, want ownershipUnowned", got)
			}
			errs := errorDiagsWith(b.diags, overwriteSummary)
			if len(errs) != 1 {
				t.Fatalf("want exactly one Error %q, got:\n%s", overwriteSummary, renderDiags(b.diags))
			}
			detail := errs[0].Description().Detail
			wants := []string{
				armRoleID, addr.String(), "PUT",
				"Microsoft.Authorization/roleAssignments at API version 2022-04-01, takes no tags",
				`policy { declared_untagged = "adopt" }`,
				"live-import", "-estate=" + k8sOwnershipEstate, "writes its record",
			}
			if withStore {
				wants = append(wants, "there is none for "+addr.String())
			} else {
				wants = append(wants, "declares no record_store")
			}
			for _, want := range wants {
				if !strings.Contains(detail, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, detail)
				}
			}
			for _, wrong := range []string{"write the tags", markers.TagEstate + `="`, "carries no tofu-estate tag"} {
				if strings.Contains(detail, wrong) {
					t.Errorf("the refusal for a type that takes no tags says %q:\n%s", wrong, detail)
				}
			}
			if om, ok := b.omitted[addr.String()]; !ok || om.Reason != ReasonUnowned {
				t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
			}
		})
	}

	t.Run("a taggable azapi type keeps the tag wording and the stale-record check", func(t *testing.T) {
		const workerID = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/z"
		const workerType = "Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31"
		b := armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		b.checkOwnership(addr, "azapi_resource", workerID, azapiShapedSchema(), azapiReadObject(workerID, workerType), true, false, false)
		errs := errorDiagsWith(b.diags, overwriteSummary)
		if len(errs) != 1 || !strings.Contains(errs[0].Description().Detail, "write the tags "+markers.TagEstate+`="`+k8sOwnershipEstate+`"`) {
			t.Errorf("want the tag wording for a taggable azapi type, got:\n%s", renderDiags(b.diags))
		}
		// A record is not proof for a type that carries its marker: an
		// object with no tofu-address does not confirm it.
		b = armOverwriteBuilder(t, &Ownership{Estate: k8sOwnershipEstate})
		if got := b.checkOwnershipAt(addr, "azapi_resource", workerID, azapiShapedSchema(), azapiReadObject(workerID, workerType), true, false, true, true); got != ownershipStale {
			t.Errorf("verdict %d for a taggable type's unconfirmed record, want ownershipStale", got)
		}
	})
}
