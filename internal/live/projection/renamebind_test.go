// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
)

// GitHub issue #2136, the projection's half. The discovery half is
// internal/live/discovery/renamebind_test.go.
//
// A block's identity argument renamed (rt-old -> rt-new, b-old -> b-new)
// while the old object still carries tofu-address = the unchanged address.
// [builder.checkOwnershipAt]'s own.verified case returns ownershipOK before
// reading the tags of the object the projection fetched at the NEW id, so
// whether that object is judged depends entirely on whether discovery hands
// the address over in Ownership.Verified. Before #2136 the Azure sweep did
// (the old object's sighting vouched); it now reports that object displaced
// and vouches for nothing, as AWS's sweep always did. These tests pin that
// the projection's half is platform-neutral: given the vouch it admits the
// object at the new id unread on either platform, and without it, which is
// what both platforms' discovery now leaves after a rename, it reads the
// tags and refuses.

func renameBuilder(t *testing.T, own *Ownership) *builder {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm" }
    aws     = { source = "hashicorp/aws" }
  }
}
resource "azurerm_route_table" "rt" {
  name                = "rt-new"
  resource_group_name = "rg"
  location            = "westeurope"
}
resource "aws_s3_bucket" "b" {
  bucket = "b-new"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	b := k8sOwnershipBuilder(own)
	b.cfg = loadConfig(t, dir)
	return b
}

type renameCase struct {
	typ, name, newID string
}

func (c renameCase) addr() addrs.AbsResourceInstance {
	return addrs.Resource{Mode: addrs.ManagedResourceMode, Type: c.typ, Name: c.name}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
}

var renameCases = []renameCase{
	{"azurerm_route_table", "rt", "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg/providers/Microsoft.Network/routeTables/rt-new"},
	{"aws_s3_bucket", "b", "b-new"},
}

// renameLive is the object found at the new id, carrying tags (nil: none).
func renameLive(c renameCase, tags map[string]string) cty.Value {
	obj := armObject(tags)
	return cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal(c.newID),
		"name": cty.StringVal(c.name),
		"tags": obj.GetAttr("tags"),
	})
}

// renameShapes is what can sit at the new name: nothing of this estate's.
var renameShapes = map[string]map[string]string{
	"no tags":             nil,
	"another team's tags": {"team": "other"},
	"another estate":      {"tofu-estate": "platform", "tofu-address": "azurerm_route_table.x"},
}

// TestRenameBind_verifiedAdmitsTheNewIDUnread: with the address in
// Ownership.Verified (what discovery handed over on Azure after a rename
// before #2136), the object at the new id is admitted - ownershipOK, no
// diagnostic, no omission - whatever its tags say. Platform-neutral. It is
// why discovery must not vouch on the old object's sighting.
func TestRenameBind_verifiedAdmitsTheNewIDUnread(t *testing.T) {
	for _, c := range renameCases {
		for name, tags := range renameShapes {
			t.Run(c.typ+"/"+name, func(t *testing.T) {
				b := renameBuilder(t, &Ownership{Estate: aksEstate, Verified: map[string]bool{c.addr().String(): true}})
				if got := b.checkOwnership(c.addr(), c.typ, c.newID, armTagsSchema(), renameLive(c, tags), true, false, false); got != ownershipOK {
					t.Fatalf("verdict %d, want ownershipOK (a vouched address is admitted unread)", got)
				}
				if len(b.diags) != 0 {
					t.Errorf("diagnostics, want none for a vouched address:\n%s", renderDiags(b.diags))
				}
				if _, ok := b.omitted[c.addr().String()]; ok {
					t.Errorf("the instance was omitted, want it admitted")
				}
			})
		}
	}
}

// TestRenameBind_unverifiedReadsAndRefuses: the same objects without the
// vouch (what both sweeps leave after the rename, the old object reported
// displaced instead) are read and left out of the prior state as unowned.
func TestRenameBind_unverifiedReadsAndRefuses(t *testing.T) {
	for _, c := range renameCases {
		for name, tags := range renameShapes {
			t.Run(c.typ+"/"+name, func(t *testing.T) {
				b := renameBuilder(t, &Ownership{Estate: aksEstate})
				if got := b.checkOwnership(c.addr(), c.typ, c.newID, armTagsSchema(), renameLive(c, tags), true, false, false); got != ownershipUnowned {
					t.Fatalf("verdict %d, want ownershipUnowned\n%s", got, renderDiags(b.diags))
				}
				if len(b.diags) == 0 {
					t.Errorf("no diagnostic for an unmarked object at the declared id")
				}
				if om, ok := b.omitted[c.addr().String()]; !ok || om.Reason != ReasonUnowned {
					t.Errorf("omission = %+v (present %v), want ReasonUnowned", om, ok)
				}
			})
		}
	}
}
