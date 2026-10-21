// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// corpus-azapi-role-assignment's day2_remove: day2_rename renamed the role
// assignment's block (roleAssignments -> role_assignment) with a moved block
// that every later stage keeps, and day2_remove deletes the block. The role
// assignment takes no tags, so the record leg is the only thing that can
// propose its destroy, and it must propose exactly one, at the address its
// block last had. Two records of the one object, one per address, is the
// shape an apply before write-back retired moved-from keys left behind (or
// an apply interrupted between its two writes); one record under the new
// address is what a rename leaves now.

const (
	azapiRoleRG   = "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg"
	azapiRoleType = "Microsoft.Authorization/roleAssignments@2022-04-01"
	azapiRoleName = "6faae21a-0cd6-4536-8c23-a278823d12ed"
	azapiRoleID   = azapiRoleRG + "/providers/Microsoft.Authorization/roleAssignments/" + azapiRoleName
)

func TestRemovedRenamedAzapiRoleAssignmentIsProposedOnce(t *testing.T) {
	rec := projection.LocatedRecord{
		ImportID:   azapiRoleID,
		Components: map[string]string{"type": azapiRoleType, "parent_id": azapiRoleRG, "name": azapiRoleName},
	}
	for name, keys := range map[string][]string{
		"one record, under the new address": {"role_assignment"},
		"two records, under both addresses": {"roleAssignments", "role_assignment"},
		"one record, under the old address": {"roleAssignments"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := `
terraform {
  required_providers {
    azapi = { source = "azure/azapi" }
  }
}
moved {
  from = azapi_resource.roleAssignments
  to   = azapi_resource.role_assignment
}
`
			if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
				t.Fatal(err)
			}
			rawStore, seedStore := recordOrphanHintStore(t)
			for _, k := range keys {
				if _, err := projection.SeedLocatedForInstance(t.Context(), seedStore, mustAddr(t, "azapi_resource."+k), azapiProviderAddr, rec); err != nil {
					t.Fatalf("seeding azapi_resource.%s: %s", k, err)
				}
			}
			res, diags := Discover(t.Context(), Request{
				Estate:        estateName,
				Config:        loadConfig(t, dir),
				Provider:      azapiCloud(),
				HintStore:     rawStore,
				RecordRemoval: true,
			})
			assertNoErrors(t, diags)

			var proposed []string
			for _, r := range res.Resolutions {
				if r.Undeclared && r.Addr.Resource.Resource.Type == "azapi_resource" {
					proposed = append(proposed, r.Addr.String())
					if r.ImportID != azapiRoleID || !r.RecordRooted {
						t.Errorf("%s: ImportID %q RecordRooted %v, want the role assignment's ARM id, record-rooted", r.Addr, r.ImportID, r.RecordRooted)
					}
				}
			}
			if len(proposed) != 1 || proposed[0] != "azapi_resource.role_assignment" {
				t.Fatalf("proposed %v, want exactly [azapi_resource.role_assignment]: one object, one destroy, at the address the moved block names", proposed)
			}
		})
	}
}
