// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"
)

// TestDeriveCompositeWithSeparator_OrderFromString pins
// importprecedence.go's central safety property: argument order is always
// recovered from the documented example string's own left-to-right shape,
// never trusted from the candidate list's own order - the property that
// keeps aws_api_gateway_method (registry/grammar argument order
// alphabetical, string order reversed) from being proposed with the wrong
// order, and resolves aws_networkmanager_link_association's separator and
// order correctly even though the registry's own primaryIdentifier order
// (GlobalNetworkId, DeviceId, LinkId) disagrees with the documented string
// (global_network_id, link_id, device_id).
func TestDeriveCompositeWithSeparator_OrderFromString(t *testing.T) {
	tests := []struct {
		name       string
		example    string
		sep        string
		candidates []string
		wantOK     bool
		wantOrder  []string
	}{
		{
			name:       "order recovered from the string, not the candidate list",
			example:    "global-network-0d47f6t230mz46dy4,link-444555aaabbb11223,device-07f6fd08867abc123",
			sep:        ",",
			candidates: []string{"global_network_id", "device_id", "link_id"}, // registry order: device before link
			wantOK:     true,
			wantOrder:  []string{"global_network_id", "link_id", "device_id"}, // string order: link before device
		},
		{
			name:       "arity mismatch fails closed (the aws_route trap)",
			example:    "rtb-656C65616E6F72_10.42.0.0/16",
			sep:        "_",
			candidates: []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id", "route_table_id"},
			wantOK:     false,
		},
		{
			name:       "opaque placeholder values carry no name token: fails closed rather than guessing",
			example:    "12345abcde/67890fghij/GET",
			sep:        "/",
			candidates: []string{"http_method", "resource_id", "rest_api_id"},
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc, ok := deriveCompositeWithSeparator(tt.example, tt.sep, tt.candidates)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (dc=%+v)", ok, tt.wantOK, dc)
			}
			if !ok {
				return
			}
			if len(dc.ArgsInOrder) != len(tt.wantOrder) {
				t.Fatalf("ArgsInOrder = %v, want %v", dc.ArgsInOrder, tt.wantOrder)
			}
			for i := range dc.ArgsInOrder {
				if dc.ArgsInOrder[i] != tt.wantOrder[i] {
					t.Errorf("ArgsInOrder[%d] = %q, want %q (full: %v)", i, dc.ArgsInOrder[i], tt.wantOrder[i], dc.ArgsInOrder)
				}
			}
		})
	}
}

// TestLabelForOpaqueValue pins the ARN-vs-short-id label rule
// tryArnVsIDOverride and tryOpaqueOverride both share.
func TestLabelForOpaqueValue(t *testing.T) {
	tests := []struct {
		example      string
		wantSyntax   string
		wantIdentity string
	}{
		{"arn:aws:networkmanager::123456789012:device/global-network-x/device-y", "ARN", "arn"},
		{"svc-06728e2357ea55f8a", "ID", "id"},
		{"s-12345678", "ID", "id"},
	}
	for _, tt := range tests {
		syntax, attr := labelForOpaqueValue(tt.example)
		if syntax != tt.wantSyntax || attr != tt.wantIdentity {
			t.Errorf("labelForOpaqueValue(%q) = (%q, %q), want (%q, %q)", tt.example, syntax, attr, tt.wantSyntax, tt.wantIdentity)
		}
	}
}
