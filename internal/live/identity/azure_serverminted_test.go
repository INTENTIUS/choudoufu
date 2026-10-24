// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/providers"
)

func TestAzureServerMintedRosterIsServed(t *testing.T) {
	if len(AzureServerMintedTypes) != 17 {
		t.Fatalf("roster has %d members, #2147 traced 17", len(AzureServerMintedTypes))
	}
	for name, m := range AzureServerMintedTypes {
		if _, ok := LookupType(name); ok {
			t.Errorf("%s has a table row, so the roster must not claim it", name)
		}
		if IsMarkerless(name) != !m.Taggable {
			t.Errorf("%s: IsMarkerless=%v with Taggable=%v", name, IsMarkerless(name), m.Taggable)
		}
		// The ARM tags map is what makes a type taggable here; the three
		// azurerm types whose `tags` is something else are listed apart.
		_, hasTags := armtypes.AzurermTagRules(name)
		if m.Taggable != hasTags {
			t.Errorf("%s: Taggable=%v but armtypes' azurerm tags table says tags=%v", name, m.Taggable, hasTags)
		}
	}
}

func azureMintedFixture(t *testing.T, store bool) (string, map[string]providers.Schema) {
	t.Helper()
	// Any live block implies a record store, so "no store" is no live block.
	live := ""
	if store {
		live = `terraform {
  live {
    estate = "test-estate"
  }
}`
	}
	src := live + `

resource "azurerm_key_vault_secret" "tag" {
  name         = "s"
  value        = "v"
  key_vault_id = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
}

resource "azurerm_synapse_role_assignment" "bare" {
  synapse_workspace_id = "x"
  role_name            = "r"
  principal_id         = "p"
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, map[string]providers.Schema{
		"azurerm_key_vault_secret":        locatedSchema(nil),
		"azurerm_synapse_role_assignment": locatedSchema(nil),
	}
}

func TestAzureServerMintedLocatedType(t *testing.T) {
	_, schemas := azureMintedFixture(t, true)
	for name := range AzureServerMintedTypes {
		want := schemas[name].Block != nil
		if got := LocatedType(name, schemas); got != want {
			t.Errorf("LocatedType(%s) = %v, want %v (only types with a schema can be admitted)", name, got, want)
		}
	}
	if LocatedType("azurerm_key_vault_secret", nil) {
		t.Error("LocatedType admitted with no schemas")
	}
}

func TestAzureServerMintedResolves(t *testing.T) {
	cases := []struct {
		name  string
		store bool
		addr  string
		want  Class
	}{
		{"store: taggable is record-located", true, "azurerm_key_vault_secret.tag", ClassRecordLocated},
		{"store: untaggable is record-located", true, "azurerm_synapse_role_assignment.bare", ClassRecordLocated},
		{"no store: taggable is marker discovery", false, "azurerm_key_vault_secret.tag", ClassNeedsDiscovery},
	}
	only := func(dir, keep string) {
		// The other member would be refused without a store; resolve one at a time.
		b, _ := os.ReadFile(filepath.Join(dir, "main.tf"))
		src := string(b)
		cut := "resource \"azurerm_synapse_role_assignment\""
		if keep == "synapse" {
			cut = "resource \"azurerm_key_vault_secret\""
		}
		i := strings.Index(src, cut)
		j := strings.Index(src[i+1:], "resource \"")
		if j < 0 {
			src = src[:i]
		} else {
			src = src[:i] + src[i+1+j:]
		}
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, schemas := azureMintedFixture(t, c.store)
			if strings.HasPrefix(c.addr, "azurerm_synapse") {
				only(dir, "synapse")
			} else {
				only(dir, "kv")
			}
			result, diags := ResolveWith(context.Background(), loadConfig(t, dir, nil), Context{Schemas: schemas})
			assertNoErrors(t, diags)
			res := resolutionAt(t, result, c.addr)
			if res.Class != c.want {
				t.Fatalf("%s resolved %s, want %s", c.addr, res.Class, c.want)
			}
			if c.want == ClassNeedsDiscovery && res.Cause != DiscoveryServerAssigned {
				t.Errorf("cause = %v, want DiscoveryServerAssigned", res.Cause)
			}
		})
	}
	t.Run("no store: untaggable stays refused", func(t *testing.T) {
		dir, schemas := azureMintedFixture(t, false)
		only(dir, "synapse")
		_, diags := ResolveWith(context.Background(), loadConfig(t, dir, nil), Context{Schemas: schemas})
		if !diags.HasErrors() || !hasDiag(diags, "Resource type outside the live-markers subset", "azurerm_synapse_role_assignment") {
			t.Fatalf("expected the untaggable member refused without a store:\n%s", renderDiags(diags))
		}
	})
}
