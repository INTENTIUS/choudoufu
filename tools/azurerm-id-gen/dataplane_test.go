// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #2206: the data-plane URL ids. Each family below carries
// azurerm 5.10.0's own ID() code, transcribed with its file named, and the
// test proves the components the artifact records for the family's types
// render to exactly what that code formats, for the same inputs, in all three
// clouds. The artifact is read from live/azurerm-ids.json, so a regeneration
// that changes a family's components fails here until azurerm's format
// agrees.

type dpArtifact struct {
	Types []entry `json:"types"`
}

func dataPlaneRows(t *testing.T) map[string]entry {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "live", "azurerm-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a dpArtifact
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	out := map[string]entry{}
	for _, e := range a.Types {
		out[e.Type] = e
	}
	return out
}

// renderDataPlane renders a row's components as the identity package does:
// a literal, an argument's value (escaped as the component says, empty for an
// absent optional one), one segment of an ARM id argument, or the cloud's
// suffix for the environment.
func renderDataPlane(t *testing.T, e entry, env string, args map[string]string) string {
	t.Helper()
	var b strings.Builder
	for _, c := range e.Components {
		switch {
		case c.Cloud != "":
			s, ok := identity.AzureDNSSuffix(env, identity.CloudValue(c.Cloud))
			if !ok {
				t.Fatalf("no %s for %s", c.Cloud, env)
			}
			b.WriteString(s)
		case c.ARMSegment != nil:
			pieces := strings.Split(args[c.Attrs[0]], "/")
			if len(pieces) <= c.ARMSegment.Index || !strings.EqualFold(pieces[c.ARMSegment.Index-1], c.ARMSegment.After) {
				t.Fatalf("%s: %q has no %s segment at %d", e.Type, args[c.Attrs[0]], c.ARMSegment.After, c.ARMSegment.Index)
			}
			b.WriteString(pieces[c.ARMSegment.Index])
		case len(c.Attrs) > 0:
			v := args[c.Attrs[0]]
			switch c.URLEscape {
			case "path":
				v = url.PathEscape(v)
			case "query":
				v = url.QueryEscape(v)
			}
			b.WriteString(v)
		default:
			b.WriteString(c.Literal)
		}
	}
	return b.String()
}

const (
	dpVault = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv1"
	dpHSM   = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.KeyVault/managedHSMs/hsm1"
	dpStore = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.AppConfiguration/configurationStores/store1"
	dpCont  = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct1/blobServices/default/containers/box"
)

var dpEnvs = []string{"public", "usgovernment", "china"}

func dpSuffix(t *testing.T, env string, which identity.CloudValue) string {
	s, ok := identity.AzureDNSSuffix(env, which)
	if !ok {
		t.Fatalf("no %s for %s", which, env)
	}
	return s
}

// Key Vault. internal/services/keyvault/client/helpers.go's
// BaseUriForKeyVault returns the vault's properties.vaultUri,
// "https://kv1.<suffix>/" for every public cloud, and
// keyvault/parse/certificate_issuer.go, certificate_contacts.go and
// go-azure-helpers' keyvault/nested_item.go format it as below.
func TestDataPlaneKeyVaultIDsEqualAzurermsFormat(t *testing.T) {
	rows := dataPlaneRows(t)
	for _, env := range dpEnvs {
		base := "https://kv1." + dpSuffix(t, env, identity.CloudKeyVaultDNSSuffix) + "/"
		// IssuerId.ID(): fmt.Sprintf("%s/certificates/issuers/%s", strings.TrimSuffix(i.KeyVaultBaseUrl, "/"), i.Name)
		issuer := fmt.Sprintf("%s/certificates/issuers/%s", strings.TrimSuffix(base, "/"), "iss")
		// CertificateContactsId.ID(): TrimSuffix(Join([base trimmed, "certificates", "contacts"], "/"), "/")
		contacts := strings.TrimSuffix(strings.Join([]string{strings.TrimSuffix(base, "/"), "certificates", "contacts"}, "/"), "/")
		// NestedItemID.ID() for a storage item, no version: Join([base trimmed, "storage", name], "/")
		storage := strings.Join([]string{strings.TrimSuffix(base, "/"), "storage", "acct"}, "/")
		// SasDefinitionId.ID(): fmt.Sprintf("%s/storage/%s/sas/%s", "https://"+host, account, name)
		sas := fmt.Sprintf("%s/storage/%s/sas/%s", "https://"+strings.TrimSuffix(strings.TrimPrefix(base, "https://"), "/"), "acct", "def")
		for typ, c := range map[string]struct {
			args map[string]string
			want string
		}{
			"azurerm_key_vault_certificate_issuer":                           {map[string]string{"key_vault_id": dpVault, "name": "iss"}, issuer},
			"azurerm_key_vault_certificate_contacts":                         {map[string]string{"key_vault_id": dpVault}, contacts},
			"azurerm_key_vault_managed_storage_account":                      {map[string]string{"key_vault_id": dpVault, "name": "acct"}, storage},
			"azurerm_key_vault_managed_storage_account_sas_token_definition": {map[string]string{"managed_storage_account_id": storage, "name": "def"}, sas},
		} {
			e := rows[typ]
			if e.Class != classTemplate && e.Class != classParentID {
				t.Errorf("%s is %s (%s), want a derived row", typ, e.Class, e.Reason)
				continue
			}
			if got := renderDataPlane(t, e, env, c.args); got != c.want {
				t.Errorf("%s in %s renders %q, azurerm formats %q", typ, env, got, c.want)
			}
		}
	}
}

// Managed HSM. internal/services/managedhsm/parse: the endpoint is
// https://<name>.<Environment.ManagedHSM.DomainSuffix()>/, and the ids format
// it as below (managed_hsm_data_plane_versionless_key.go,
// ..._role_assignment.go and ..._role_definition.go, whose path types
// format "/%s/providers/Microsoft.Authorization/roleAssignments/%s" and
// ".../roleDefinitions/%s" around the scope, "/" for a definition).
func TestDataPlaneManagedHSMIDsEqualAzurermsFormat(t *testing.T) {
	rows := dataPlaneRows(t)
	for _, env := range dpEnvs {
		suffix := dpSuffix(t, env, identity.CloudManagedHSMDNSSuffix)
		key := fmt.Sprintf("https://%s.%s/keys/%s", "hsm1", suffix, "k")
		assignmentFor := func(scope string) string {
			return fmt.Sprintf("https://%s.%s%s", "hsm1", suffix, fmt.Sprintf("/%s/providers/Microsoft.Authorization/roleAssignments/%s", scope, "ra"))
		}
		definition := fmt.Sprintf("https://%s.%s%s", "hsm1", suffix, fmt.Sprintf("/%s/providers/Microsoft.Authorization/roleDefinitions/%s", "/", "rd"))
		for name, c := range map[string]struct {
			typ  string
			args map[string]string
			want string
		}{
			"key":                   {"azurerm_key_vault_managed_hardware_security_module_key", map[string]string{"managed_hsm_id": dpHSM, "name": "k"}, key},
			"assignment, scope /":   {"azurerm_key_vault_managed_hardware_security_module_role_assignment", map[string]string{"managed_hsm_id": dpHSM, "scope": "/", "name": "ra"}, assignmentFor("/")},
			"assignment, scope key": {"azurerm_key_vault_managed_hardware_security_module_role_assignment", map[string]string{"managed_hsm_id": dpHSM, "scope": "/keys/k", "name": "ra"}, assignmentFor("/keys/k")},
			"definition":            {"azurerm_key_vault_managed_hardware_security_module_role_definition", map[string]string{"managed_hsm_id": dpHSM, "name": "rd"}, definition},
		} {
			e := rows[c.typ]
			if e.Class != classTemplate {
				t.Errorf("%s is %s (%s), want template", c.typ, e.Class, e.Reason)
				continue
			}
			if got := renderDataPlane(t, e, env, c.args); got != c.want {
				t.Errorf("%s (%s) in %s renders %q, azurerm formats %q", name, c.typ, env, got, c.want)
			}
		}
	}
}

// App Configuration. internal/services/appconfiguration/parse/nested_item.go
// NestedItemId.ID() builds a url.URL from the store's endpoint
// (https://store1.<suffix>, the properties.endpoint) with Path "kv/<key>",
// RawPath "kv/<PathEscape(key)>" and RawQuery "label=<QueryEscape(label)>".
func TestDataPlaneAppConfigurationIDsEqualAzurermsFormat(t *testing.T) {
	rows := dataPlaneRows(t)
	e := rows["azurerm_app_configuration_key"]
	if e.Class != classTemplate {
		t.Fatalf("azurerm_app_configuration_key is %s (%s)", e.Class, e.Reason)
	}
	for _, env := range dpEnvs {
		endpoint := "https://store1." + dpSuffix(t, env, identity.CloudAppConfigurationDNSSuffix)
		for _, c := range []struct{ key, label string }{{"k", ""}, {"k", "prod"}, {"app/setting", "blue green"}, {"a b?c", "x&y=z"}, {"a:b@c", ""}} {
			base, _ := url.ParseRequestURI(endpoint)
			u := &url.URL{
				Scheme:   base.Scheme,
				Host:     base.Host,
				Path:     fmt.Sprintf("kv/%s", c.key),
				RawPath:  fmt.Sprintf("kv/%s", url.PathEscape(c.key)),
				RawQuery: fmt.Sprintf("label=%s", url.QueryEscape(c.label)),
			}
			args := map[string]string{"configuration_store_id": dpStore, "key": c.key, "label": c.label}
			if got := renderDataPlane(t, e, env, args); got != u.String() {
				t.Errorf("key %q label %q in %s renders %q, azurerm formats %q", c.key, c.label, env, got, u.String())
			}
		}
	}
}

// Storage. giovanni's blob/accounts AccountId.ID() joins name, sub-domain
// type and the environment's storage suffix after "https://", and
// blob/blobs BlobId.ID() is "<account>/<container>/<blob>"; azurerm_storage_blob
// builds the account id from the container id's account name and
// storageClient.StorageDomainSuffix (storage_blob_resource.go).
func TestDataPlaneStorageBlobIDEqualsAzurermsFormat(t *testing.T) {
	rows := dataPlaneRows(t)
	e := rows["azurerm_storage_blob"]
	if e.Class != classTemplate {
		t.Fatalf("azurerm_storage_blob is %s (%s)", e.Class, e.Reason)
	}
	for _, env := range dpEnvs {
		components := []string{"acct1", "blob", dpSuffix(t, env, identity.CloudStorageDNSSuffix)}
		account := fmt.Sprintf("https://%s", strings.Join(components, "."))
		want := fmt.Sprintf("%s/%s/%s", account, "box", "dir/file.txt")
		got := renderDataPlane(t, e, env, map[string]string{"storage_container_id": dpCont, "name": "dir/file.txt"})
		if got != want {
			t.Errorf("%s renders %q, azurerm formats %q", env, got, want)
		}
	}
}

// The refusals: every other data-plane type stays unmapped with its reason,
// and each reason says why (so a reader never has to guess it is a gap).
func TestDataPlaneRefusalsNameTheirReason(t *testing.T) {
	rows := dataPlaneRows(t)
	for typ, spec := range dataPlaneSpecs {
		e, ok := rows[typ]
		if !ok {
			t.Errorf("%s is not in the artifact", typ)
			continue
		}
		if spec.refuse == "" {
			if e.Class == classUnmapped {
				t.Errorf("%s is spec'd to derive and is unmapped: %s", typ, e.Reason)
			}
			continue
		}
		if e.Class != classUnmapped || !strings.Contains(e.Reason, spec.refuse) {
			t.Errorf("%s = %s %q, want unmapped with %q", typ, e.Class, e.Reason, spec.refuse)
		}
	}
	for _, typ := range []string{"azurerm_key_vault_key", "azurerm_key_vault_secret", "azurerm_key_vault_certificate"} {
		if !strings.Contains(rows[typ].Reason, "#2205") {
			t.Errorf("%s's reason does not point at #2205: %q", typ, rows[typ].Reason)
		}
	}
}

// A format azurerm changes is refused, not re-derived: the literal pin reads
// the id type's own ID() body.
func TestStringLiteralsReadAnIDMethodsFormat(t *testing.T) {
	l, err := newLoader(filepath.Join("testdata", "src"))
	if err != nil {
		t.Fatal(err)
	}
	p := l.load("github.com/hashicorp/terraform-provider-azurerm/internal/services/widget/parse")
	if p == nil {
		t.Fatal("fixture parse package not found")
	}
	m := p.method("WidgetId", "ID")
	if m == nil {
		t.Fatal("fixture WidgetId has no ID() method")
	}
	want := "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Widget/widgets/%s"
	if got := stringLiterals(m.Body); len(got) != 1 || got[0] != want {
		t.Fatalf("literals of WidgetId.ID() = %q, want [%q]", got, want)
	}
}
