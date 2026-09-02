// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package mv

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
)

// checkAddresses had zero unit coverage before issue #317 (only
// TestMvAgainstFloci, a floci e2e test, exercised this package at all), so
// this is the first table-driven test of it. It exists for two reasons: to
// pin the refusals checkAddresses still legitimately raises, and to prove
// #317's fix - a rename through a count-keyed module step is now admitted,
// where it used to be refused on a premise issue #195 already retired for a
// plain scalar module count.

// resInst builds an AbsResourceInstance for these tests: a resource of
// typeName/name, keyed by resKey, inside the module instance mod (nil for
// root).
func resInst(mod addrs.ModuleInstance, typeName, name string, resKey addrs.InstanceKey) addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: name}.
		Instance(resKey).Absolute(mod)
}

// dataInst is resInst for a data source, used by the "not a managed
// resource" case.
func dataInst(mod addrs.ModuleInstance, typeName, name string) addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.DataResourceMode, Type: typeName, Name: name}.
		Instance(addrs.NoKey).Absolute(mod)
}

func TestCheckAddresses(t *testing.T) {
	countedOld := addrs.ModuleInstance{{Name: "counted", InstanceKey: addrs.IntKey(0)}}
	countedNew := addrs.ModuleInstance{{Name: "counted", InstanceKey: addrs.IntKey(1)}}
	staticMod := addrs.ModuleInstance{{Name: "net", InstanceKey: addrs.NoKey}}
	keyedMod := addrs.ModuleInstance{{Name: "keyed", InstanceKey: addrs.StringKey("a")}}

	tests := []struct {
		name     string
		old, new addrs.AbsResourceInstance
		wantErr  bool
		wantMsg  string // substring the error must contain, when wantErr
	}{
		{
			name:    "identical addresses is refused",
			old:     resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:     resInst(nil, "aws_vpc", "main", addrs.NoKey),
			wantErr: true,
			wantMsg: "Identical source and destination addresses",
		},
		{
			name:    "mismatched resource types is refused",
			old:     resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:     resInst(nil, "aws_subnet", "main", addrs.NoKey),
			wantErr: true,
			wantMsg: "Mismatched resource types in a rename",
		},
		{
			name:    "a data source on the old side is refused",
			old:     dataInst(nil, "aws_vpc", "main"),
			new:     resInst(nil, "aws_vpc", "other", addrs.NoKey),
			wantErr: true,
			wantMsg: "Unsupported address for a rename",
		},
		{
			name:    "a data source on the new side is refused",
			old:     resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:     dataInst(nil, "aws_vpc", "other"),
			wantErr: true,
			wantMsg: "Unsupported address for a rename",
		},
		{
			name: "a plain root rename is admitted",
			old:  resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(nil, "aws_vpc", "other", addrs.NoKey),
		},
		{
			name: "a static module rename is admitted",
			old:  resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(staticMod, "aws_vpc", "main", addrs.NoKey),
		},
		{
			name: "a for_each-keyed module rename is admitted (59c)",
			old:  resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(keyedMod, "aws_vpc", "main", addrs.NoKey),
		},
		{
			// This is #317's fix: hasCountKeyedModuleStep used to refuse
			// any address passing through a count-keyed module instance
			// here, citing the same "count renumbers everything beneath it"
			// premise issue #195 already retired for a plain scalar count.
			// Lint's RuleChildModule proves the count static and
			// count.index-leak-free before mv.Move ever runs, so nothing
			// here needs to re-derive that; this asserts no error.
			name: "a count-keyed module step on the old address is admitted (#317)",
			old:  resInst(countedOld, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(nil, "aws_vpc", "main", addrs.NoKey),
		},
		{
			name: "a count-keyed module step on the new address is admitted (#317)",
			old:  resInst(nil, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(countedOld, "aws_vpc", "main", addrs.NoKey),
		},
		{
			name: "a count-keyed module step on both addresses is admitted (#317)",
			old:  resInst(countedOld, "aws_vpc", "main", addrs.NoKey),
			new:  resInst(countedNew, "aws_vpc", "main", addrs.NoKey),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := checkAddresses(Request{Old: tt.old, New: tt.new})
			if tt.wantErr {
				if !diags.HasErrors() {
					t.Fatalf("checkAddresses(%s -> %s) returned no error, want one containing %q", tt.old, tt.new, tt.wantMsg)
				}
				var found bool
				for _, d := range diags {
					if d.Description().Summary == tt.wantMsg {
						found = true
					}
				}
				if !found {
					t.Errorf("checkAddresses(%s -> %s) diagnostics do not contain summary %q: %v", tt.old, tt.new, tt.wantMsg, diags)
				}
				return
			}
			if diags.HasErrors() {
				t.Fatalf("checkAddresses(%s -> %s) returned an error, want none: %v", tt.old, tt.new, diags)
			}
		})
	}
}
