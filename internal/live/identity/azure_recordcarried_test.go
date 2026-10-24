// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestAzureRecordCarriedTypesAreLocated pins GitHub issue #2203: the 37
// parent-id settings objects and azapi_data_plane_resource are admitted as
// record-located by declaration, and only while they carry no marker surface.
func TestAzureRecordCarriedTypesAreLocated(t *testing.T) {
	if got := len(AzureRecordCarriedTypes); got != 38 {
		t.Fatalf("AzureRecordCarriedTypes has %d members, want 37 azurerm types and azapi_data_plane_resource", got)
	}
	if _, ok := AzureRecordCarriedTypes["azapi_data_plane_resource"]; !ok {
		t.Fatal("azapi_data_plane_resource is not declared record-carried")
	}
	for name := range AzureRecordCarriedTypes {
		if _, row := LookupType(name); row {
			t.Errorf("%s has a ratified row, so the declaration is shadowed", name)
		}
		if !IsMarkerless(name) {
			t.Errorf("IsMarkerless(%s) = false", name)
		}
		if MarkerlessReasonFor(name) != AzureRecordCarriedReason {
			t.Errorf("%s does not carry the Azure reason", name)
		}
		if NotImportable(name) {
			continue
		}
		schemas := map[string]providers.Schema{name: locatedSchema(nil)}
		if !LocatedType(name, schemas) {
			t.Errorf("LocatedType(%s) = false with a string id and no tags", name)
		}
		tagged := locatedSchema(map[string]*configschema.Attribute{
			"tags": {Type: cty.Map(cty.String), Optional: true},
		})
		if LocatedType(name, map[string]providers.Schema{name: tagged}) {
			t.Errorf("LocatedType(%s) = true with a tags argument; a taggable type leaves the route", name)
		}
		if LocatedType(name, nil) {
			t.Errorf("LocatedType(%s) = true with no schema", name)
		}
	}
}
