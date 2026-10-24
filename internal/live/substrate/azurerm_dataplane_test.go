// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"encoding/json"
	"strings"
	"testing"
)

// GitHub issue #2206: the data-plane guard admits the family grammars the
// generator writes and refuses a row that is not one, by naming why. Each
// refusal case is a row the guard would have to admit for the identity
// package to render a host from the wrong parent, the wrong suffix or the
// wrong label.
func TestAzurermDataPlaneGuard(t *testing.T) {
	seg := func(after string, idx int, attr string) azurermIDComponent {
		return azurermIDComponent{Attrs: []string{attr}, Segment: "n", ARMSegment: &azurermARMSegment{Index: idx, After: after}}
	}
	lit := func(s string) azurermIDComponent { return azurermIDComponent{Literal: s} }
	cloud := func(c string) azurermIDComponent { return azurermIDComponent{Cloud: c, Segment: "s"} }
	arg := func(a string) azurermIDComponent { return azurermIDComponent{Attrs: []string{a}, Segment: a} }
	kv := func(tail ...azurermIDComponent) []azurermIDComponent {
		return append([]azurermIDComponent{lit("https://"), seg("vaults", 8, "key_vault_id"), lit("."), cloud("key-vault-dns-suffix")}, tail...)
	}
	for name, c := range map[string]struct {
		comps []azurermIDComponent
		want  string // "" admitted, else a fragment of the reason
	}{
		"issuer":                               {kv(lit("/certificates/issuers/"), arg("name")), ""},
		"singleton ends in a literal":          {kv(lit("/certificates/contacts")), ""},
		"hsm":                                  {[]azurermIDComponent{lit("https://"), seg("managedHSMs", 8, "managed_hsm_id"), lit("."), cloud("managed-hsm-dns-suffix"), lit("/keys/"), arg("name")}, ""},
		"blob, container segment":              {[]azurermIDComponent{lit("https://"), seg("storageAccounts", 8, "storage_container_id"), lit(".blob."), cloud("storage-dns-suffix"), lit("/"), seg("containers", 12, "storage_container_id"), lit("/"), arg("name")}, ""},
		"vault name under the HSM suffix":      {[]azurermIDComponent{lit("https://"), seg("vaults", 8, "key_vault_id"), lit("."), cloud("managed-hsm-dns-suffix"), lit("/keys/"), arg("name")}, "named after"},
		"storage label under the vault suffix": {[]azurermIDComponent{lit("https://"), seg("vaults", 8, "key_vault_id"), lit(".blob."), cloud("key-vault-dns-suffix"), lit("/"), arg("name")}, "not a host label"},
		"host from a plain argument":           {[]azurermIDComponent{lit("https://"), arg("name"), lit("."), cloud("key-vault-dns-suffix"), lit("/"), arg("name")}, "not one segment of the ARM id"},
		"no scheme":                            {[]azurermIDComponent{seg("vaults", 8, "key_vault_id"), lit("."), cloud("key-vault-dns-suffix"), lit("/x/"), arg("name")}, "begin with https"},
		"second suffix in the path":            {kv(lit("/"), cloud("storage-dns-suffix"), lit("/"), arg("name")), "cloud value"},
		"path reads another id":                {kv(lit("/"), seg("containers", 12, "other_id"), lit("/"), arg("name")), "later segment"},
		"two arguments for a path segment":     {kv(lit("/"), azurermIDComponent{Attrs: []string{"a", "b"}, Segment: "x"}), "whichever"},
		"unknown escape":                       {kv(lit("/"), azurermIDComponent{Attrs: []string{"k"}, Segment: "k", URLEscape: "html"}), "escaped as"},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(azurermIDsFile{Types: []azurermIDType{{Type: "azurerm_probe", Class: azurermClassTemplate, Template: "t", Components: c.comps}}})
			if err != nil {
				t.Fatal(err)
			}
			withheld, err := AzurermWithheldIn(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, isWithheld := withheld["azurerm_probe"]
			switch {
			case c.want == "" && isWithheld:
				t.Errorf("withheld: %s", got)
			case c.want != "" && !isWithheld:
				t.Errorf("admitted, want a refusal naming %q", c.want)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Errorf("refused as %q, want it to name %q", got, c.want)
			}
		})
	}
}
