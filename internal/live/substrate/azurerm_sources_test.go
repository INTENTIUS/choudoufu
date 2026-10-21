// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import "testing"

// TestARMIDSegment pins ARMIDSegment to go-azure-helpers' resourceids
// Parser.Parse (GitHub issue #2140): exact segment count, fixed and
// constant segments equal to what the id type declares (case-sensitively
// for ParseXxxID, folded for ParseXxxIDInsensitively), any non-empty
// value elsewhere.
func TestARMIDSegment(t *testing.T) {
	fixed := func(v string) ARMIDPart { return ARMIDPart{Kind: ARMIDFixed, Value: v} }
	value := func(n string) ARMIDPart { return ARMIDPart{Kind: ARMIDValue, Name: n} }
	// recordsets.RecordTypeId at the version azurerm 5.10.0 vendors.
	recordType := []ARMIDPart{
		fixed("subscriptions"), value("subscriptionId"),
		fixed("resourceGroups"), value("resourceGroupName"),
		fixed("providers"), fixed("Microsoft.Network"),
		fixed("dnsZones"), value("dnsZoneName"),
		{Kind: ARMIDConstant, Name: "recordType", Values: []string{"A", "AAAA", "CAA", "CNAME", "MX", "NS", "PTR", "SOA", "SRV", "TXT"}},
		value("relativeRecordSetName"),
	}
	const good = "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/CNAME/www"
	for _, c := range []struct {
		name        string
		id          string
		insensitive bool
		segment     string
		want        string
		ok          bool
	}{
		{"correct id", good, false, "dnsZoneName", "example.com", true},
		{"correct id, constant segment", good, false, "recordType", "CNAME", true},
		{"one trailing slash, as Parse accepts", good + "/", false, "relativeRecordSetName", "www", true},
		{"wrong constant", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/BOGUS/www", false, "dnsZoneName", "", false},
		{"wrong constant, insensitively", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/BOGUS/www", true, "dnsZoneName", "", false},
		{"constant in other case, strictly", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/cname/www", false, "recordType", "", false},
		{"constant in other case, insensitively: the declared spelling", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com/cname/www", true, "recordType", "CNAME", true},
		{"fixed segment in other case, strictly", "/subscriptions/sub/resourcegroups/rg/providers/Microsoft.Network/dnsZones/example.com/CNAME/www", false, "dnsZoneName", "", false},
		{"fixed segment in other case, insensitively", "/subscriptions/sub/resourcegroups/rg/providers/microsoft.network/dnsZones/example.com/CNAME/www", true, "dnsZoneName", "example.com", true},
		{"wrong fixed segment", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/example.com/CNAME/www", false, "dnsZoneName", "", false},
		{"wrong fixed segment, insensitively", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/privateDnsZones/example.com/CNAME/www", true, "dnsZoneName", "", false},
		{"too short", "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Network/dnsZones/example.com", false, "dnsZoneName", "", false},
		{"too long", good + "/extra", false, "dnsZoneName", "", false},
		{"empty value segment", "/subscriptions/sub/resourceGroups//providers/Microsoft.Network/dnsZones/example.com/CNAME/www", false, "dnsZoneName", "", false},
		{"no leading slash", good[1:], false, "dnsZoneName", "", false},
		{"no such segment", good, false, "storageAccountName", "", false},
	} {
		got, ok := ARMIDSegment(recordType, c.insensitive, c.id, c.segment)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: ARMIDSegment(%q, %q) = %q, %v; want %q, %v", c.name, c.id, c.segment, got, ok, c.want, c.ok)
		}
	}
	if _, ok := ARMIDSegment(nil, false, good, "dnsZoneName"); ok {
		t.Error("no parts parsed an id")
	}
}

// TestAzurermIdentitySourcesCarryTheirParse: every parent-id source in the
// embedded table records its segments by kind and how azurerm parses the
// argument, and parses an id of its own shape (#2140).
func TestAzurermIdentitySourcesCarryTheirParse(t *testing.T) {
	tab, err := loadAzurermIDs()
	if err != nil {
		t.Fatal(err)
	}
	rows, insensitive := 0, 0
	for _, row := range tab.rows {
		for attr, src := range row.identitySources() {
			if src.Within == "" {
				continue
			}
			rows++
			if src.ParentInsensitive {
				insensitive++
			}
			if len(src.ParentSegments) == 0 {
				t.Errorf("%s.%s: parent %s has no segments", row.Type, attr, src.ParentTemplate)
				continue
			}
			id := ""
			for _, p := range src.ParentSegments {
				switch p.Kind {
				case ARMIDFixed:
					id += "/" + p.Value
				case ARMIDConstant:
					id += "/" + p.Values[0]
				case ARMIDValue:
					id += "/v-" + p.Name
				default:
					t.Fatalf("%s.%s: part kind %q", row.Type, attr, p.Kind)
				}
			}
			if got, ok := ARMIDSegment(src.ParentSegments, src.ParentInsensitive, id, src.ParentSegment); !ok || got != "v-"+src.ParentSegment {
				t.Errorf("%s.%s: parsing %s gives %q, %v", row.Type, attr, id, got, ok)
			}
		}
	}
	if rows == 0 {
		t.Fatal("no parent-id identity sources in the table")
	}
	// azurerm_kubernetes_cluster_node_pool's kubernetes_cluster_id (three
	// attributes) and azurerm_mssql_database's server_id (two) are
	// validated by ParseXxxIDInsensitively at 5.10.0; every other is strict.
	if insensitive != 5 {
		t.Errorf("%d parent-id sources parse insensitively, want 5", insensitive)
	}
}
