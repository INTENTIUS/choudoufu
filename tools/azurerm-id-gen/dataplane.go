// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// GitHub issue #2206, the maintainer's ruling of 2026-10-23 on #2201 item 5:
// the data-plane URL ids derive, because the cloud's DNS suffix is a
// provider-block value like the subscription. These ids are not ARM paths and
// not one shape. Their host is the name segment of a parent's ARM id plus a
// service suffix (Key Vault, Managed HSM, App Configuration, Storage), and
// what follows is the family's own path. A resolver that reads a type's id
// from one Segments() list or one Sprintf cannot say that, and the id types
// here are written five different ways (a resourceids struct, a Sprintf, a
// strings.Join, a url.URL builder, a giovanni AccountId), so each family is a
// grammar stated in [dataPlaneSpecs] and held to azurerm 5.10.0's own code:
//
//   - the id type the importer parses has to be the one the spec names;
//   - the string literals of that type's ID() method (and of the id types it
//     formats through) have to be the ones the spec pins, so a format azurerm
//     changes is refused, not silently re-derived;
//   - the resource's own code has to use the names that source the host and
//     suffix (BaseUriForKeyVault, StorageDomainSuffix, the managed HSM
//     DomainSuffix), which is the statement that the host is the parent's
//     name plus the environment's suffix;
//   - each ARM-id argument is read to the id type its validator parses
//     ([loader.argIDType]), and the segment taken out of it is the value that
//     follows the named static segment of that type.
//
// A type whose format cannot be matched stays refused, with the reason, and
// the reasons are recorded here as data so the artifact names them.
//
// One caveat holds for every host here and is the ruling's, not a proof: the
// Key Vault, Managed HSM and App Configuration hosts azurerm uses are the
// URI the service reports for the parent (properties.vaultUri, hsmUri,
// endpoint), which for the three public clouds is always name.suffix.

// dataPlaneInfo is the evidence a derived row carries.
type dataPlaneInfo struct {
	// Family is the spec's family name.
	Family string `json:"family"`
	// IDLiterals are the string literals of the id type's ID() (and of the id
	// types it formats through) that were compared with azurerm's source.
	IDLiterals map[string][]string `json:"id_literals"`
	// Uses are the names the resource's code was found to use.
	Uses []string `json:"uses"`
	// Parents are, per ARM-id argument, the id type its validator parses and
	// the ARM id's segment position taken.
	Parents map[string]dataPlaneParent `json:"parents,omitempty"`
}

type dataPlaneParent struct {
	IDType string `json:"id_type"`
	// Segments are the positions taken: index in the id split on "/" and the
	// static segment before it, by the name of the segment.
	Segments map[string]armSegment `json:"segments"`
}

// armSegment is the position of one path segment of an ARM id argument's value.
type armSegment struct {
	Index int    `json:"index"`
	After string `json:"after"`
}

// dpLits pins the string literals of one id method.
type dpLits struct {
	pkg, typ string
	lits     []string
}

// dpPart is one piece of the id. Exactly one of lit, cloud, attrs and arm is
// set (a leading whole-id argument is wholeID).
type dpPart struct {
	lit    string
	cloud  string
	attrs  []string
	seg    string
	escape string
	omit   bool
	// armArg and armAfter: the segment of the ARM id the argument armArg
	// holds that follows the static segment named armAfter.
	armArg, armAfter string
	// wholeID: the argument's value is a whole parent id, the id's prefix as
	// written.
	wholeID bool
}

type dpSpec struct {
	family string
	idType string
	// uses are identifiers the resource's files must mention.
	uses  []string
	lits  []dpLits
	parts []dpPart
	// refuse, when set, keeps the type refused with this reason.
	refuse string
}

const (
	kvSuffix  = "key-vault-dns-suffix"
	hsmSuffix = "managed-hsm-dns-suffix"
	acSuffix  = "app-configuration-dns-suffix"
	stSuffix  = "storage-dns-suffix"

	kvParse    = "github.com/hashicorp/terraform-provider-azurerm/internal/services/keyvault/parse"
	hsmParse   = "github.com/hashicorp/terraform-provider-azurerm/internal/services/managedhsm/parse"
	acParse    = "github.com/hashicorp/terraform-provider-azurerm/internal/services/appconfiguration/parse"
	kvHelpers  = "github.com/hashicorp/go-azure-helpers/resourcemanager/keyvault"
	gioAccount = "github.com/jackofallops/giovanni/storage/2023-11-03/blob/accounts"
	gioBlob    = "github.com/jackofallops/giovanni/storage/2023-11-03/blob/blobs"
)

func lit(s string) dpPart { return dpPart{lit: s} }

func kvHost(arg string) []dpPart {
	return []dpPart{lit("https://"), {armArg: arg, armAfter: "vaults", seg: "keyVaultName"}, lit("."), {cloud: kvSuffix, seg: "keyVaultDnsSuffix"}}
}

func hsmHost(arg string) []dpPart {
	return []dpPart{lit("https://"), {armArg: arg, armAfter: "managedHSMs", seg: "managedHsmName"}, lit("."), {cloud: hsmSuffix, seg: "managedHsmDnsSuffix"}}
}

func parts(a []dpPart, b ...dpPart) []dpPart { return append(append([]dpPart(nil), a...), b...) }

const versionedKV = "its id is the versioned id the Key Vault service returns from the create (azurerm sets it from the response's id), and a version is minted by the server, so the id is located by record, not derived (#2205)"

// dataPlaneSpecs is every azurerm 5.10.0 type whose id is a URL on a service's
// data plane, derived or refused with its reason.
var dataPlaneSpecs = map[string]dpSpec{
	"azurerm_key_vault_certificate_contacts": {
		family: "key-vault", idType: "CertificateContactsId", uses: []string{"BaseUriForKeyVault"},
		lits:  []dpLits{{kvParse, "CertificateContactsId", []string{"/", "certificates", "contacts", "/", "/"}}},
		parts: parts(kvHost("key_vault_id"), lit("/certificates/contacts")),
	},
	"azurerm_key_vault_certificate_issuer": {
		family: "key-vault", idType: "IssuerId", uses: []string{"BaseUriForKeyVault"},
		lits:  []dpLits{{kvParse, "IssuerId", []string{"%s/certificates/issuers/%s", "/"}}},
		parts: parts(kvHost("key_vault_id"), lit("/certificates/issuers/"), dpPart{attrs: []string{"name"}, seg: "name"}),
	},
	"azurerm_key_vault_managed_storage_account": {
		family: "key-vault", idType: "NestedItemID", uses: []string{"BaseUriForKeyVault", "NestedItemTypeStorage"},
		lits:  []dpLits{{kvHelpers, "NestedItemID", []string{"", "/"}}},
		parts: parts(kvHost("key_vault_id"), lit("/storage/"), dpPart{attrs: []string{"name"}, seg: "name"}),
	},
	"azurerm_key_vault_managed_storage_account_sas_token_definition": {
		family: "key-vault", idType: "SasDefinitionId", uses: []string{"NestedItemTypeStorage"},
		lits: []dpLits{{kvParse, "SasDefinitionId", []string{"%s/storage/%s/sas/%s"}}},
		// The storage account's id is the id the managed storage account
		// resource set: https://{vault}.{suffix}/storage/{account}, so the
		// SAS definition's is that id and /sas/{name}.
		parts: []dpPart{{attrs: []string{"managed_storage_account_id"}, seg: "managedStorageAccountId", wholeID: true}, lit("/sas/"), {attrs: []string{"name"}, seg: "name"}},
	},
	"azurerm_key_vault_key":         {refuse: versionedKV},
	"azurerm_key_vault_secret":      {refuse: versionedKV},
	"azurerm_key_vault_certificate": {refuse: versionedKV},

	"azurerm_key_vault_managed_hardware_security_module_key": {
		family: "managed-hsm", idType: "ManagedHSMDataPlaneVersionlessKeyId", uses: []string{"DomainSuffix", "NewManagedHSMDataPlaneVersionlessKeyID"},
		lits:  []dpLits{{hsmParse, "ManagedHSMDataPlaneVersionlessKeyId", []string{"https://%s.%s/keys/%s"}}},
		parts: parts(hsmHost("managed_hsm_id"), lit("/keys/"), dpPart{attrs: []string{"name"}, seg: "keyName"}),
	},
	"azurerm_key_vault_managed_hardware_security_module_key_rotation_policy": {
		refuse: "its id is its managed_hsm_key_id argument, parsed and formatted again, so it names the key the policy configures and not an object the policy owns",
	},
	"azurerm_key_vault_managed_hardware_security_module_role_assignment": {
		family: "managed-hsm", idType: "ManagedHSMDataPlaneRoleAssignmentId", uses: []string{"DomainSuffix", "NewManagedHSMDataPlaneRoleAssignmentID"},
		lits: []dpLits{
			{hsmParse, "ManagedHSMDataPlaneRoleAssignmentId", []string{"https://%s.%s%s"}},
			{hsmParse, "managedHSMRoleAssignmentPathId", []string{"/%s/providers/Microsoft.Authorization/roleAssignments/%s"}},
		},
		parts: parts(hsmHost("managed_hsm_id"), lit("/"), dpPart{attrs: []string{"scope"}, seg: "scope"}, lit("/providers/Microsoft.Authorization/roleAssignments/"), dpPart{attrs: []string{"name"}, seg: "roleAssignmentName"}),
	},
	"azurerm_key_vault_managed_hardware_security_module_role_definition": {
		family: "managed-hsm", idType: "ManagedHSMDataPlaneRoleDefinitionId", uses: []string{"DomainSuffix", "NewManagedHSMDataPlaneRoleDefinitionID", "RoleScopeGlobal"},
		lits: []dpLits{
			{hsmParse, "ManagedHSMDataPlaneRoleDefinitionId", []string{"https://%s.%s%s"}},
			{hsmParse, "managedHSMRoleDefinitionPathId", []string{"/%s/providers/Microsoft.Authorization/roleDefinitions/%s"}},
		},
		// The scope is keyvault.RoleScopeGlobal, "/": "/" + "/" + "/providers/...".
		parts: parts(hsmHost("managed_hsm_id"), lit("///providers/Microsoft.Authorization/roleDefinitions/"), dpPart{attrs: []string{"name"}, seg: "roleDefinitionName"}),
	},

	"azurerm_app_configuration_key": {
		family: "app-configuration", idType: "NestedItemId", uses: []string{"EndpointForConfigurationStore", "NewNestedItemID"},
		lits: []dpLits{{acParse, "NestedItemId", []string{"kv/%s", "kv/%s", "label=%s"}}},
		parts: []dpPart{lit("https://"), {armArg: "configuration_store_id", armAfter: "configurationStores", seg: "configurationStoreName"}, lit("."), {cloud: acSuffix, seg: "appConfigurationDnsSuffix"},
			lit("/kv/"), {attrs: []string{"key"}, seg: "key", escape: "path"}, lit("?label="), {attrs: []string{"label"}, seg: "label", escape: "query", omit: true}},
	},
	"azurerm_app_configuration_feature": {
		refuse: "its key is the key argument when set and the name argument otherwise, prefixed with .appconfig.featureflag/, which is a choice between two arguments this table does not make",
	},

	"azurerm_storage_blob": {
		family: "storage", idType: "BlobId", uses: []string{"StorageDomainSuffix", "BlobSubDomainType", "NewBlobID"},
		lits: []dpLits{{gioBlob, "BlobId", []string{"%s/%s/%s"}}, {gioAccount, "AccountId", []string{"https://%s", "."}}},
		parts: []dpPart{lit("https://"), {armArg: "storage_container_id", armAfter: "storageAccounts", seg: "storageAccountName"}, lit(".blob."), {cloud: stSuffix, seg: "storageDnsSuffix"},
			lit("/"), {armArg: "storage_container_id", armAfter: "containers", seg: "containerName"}, lit("/"), {attrs: []string{"name"}, seg: "name"}},
	},
	"azurerm_storage_data_lake_gen2_filesystem": {refuse: storageEndpoint},
	"azurerm_storage_data_lake_gen2_path":       {refuse: storageEndpoint},
	"azurerm_storage_table_entity":              {refuse: storageEndpoint},
	"azurerm_storage_share_directory":           {refuse: storageShareURL},
	"azurerm_storage_share_file":                {refuse: storageShareURL},
}

const (
	storageEndpoint = "its host is the account's reported data plane endpoint, parsed back into the id (accounts.ParseAccountID), and an account with dns_endpoint_type AzureDnsZone reports a server-assigned zone label the child's configuration does not state"
	storageShareURL = "its id formats storage_share_url again through ParseShareID, which normalizes the host and path, so the argument is not the id's prefix as written"
)

// dataPlaneType reports whether name is one of the data-plane types.
func dataPlaneType(name string) (dpSpec, bool) {
	s, ok := dataPlaneSpecs[name]
	return s, ok
}

// classifyDataPlane builds the row for a data-plane type: derived when every
// check against azurerm's code holds, refused with the reason otherwise.
func (l *loader) classifyDataPlane(name string, ts typeSchema, r registration, src *idSource, spec dpSpec) entry {
	e := entry{Type: name, IdentitySchema: ts.Identity, Via: r.Via, Source: src, Class: classUnmapped, Route: ""}
	refuse := func(reason, detail string) entry {
		e.Reason, e.Detail = reason, detail
		return e
	}
	if ts.Identity != nil {
		return refuse("a data-plane type serving an identity schema is not read here", "")
	}
	if spec.refuse != "" {
		return refuse("data-plane id not derived: "+spec.refuse, "")
	}
	if src == nil || src.TypeName != spec.idType {
		got := "none"
		if src != nil {
			got = src.TypeName
		}
		return refuse("data-plane id not derived: the importer parses id type "+got+", the grammar was read for "+spec.idType, "")
	}
	info := &dataPlaneInfo{Family: spec.family, IDLiterals: map[string][]string{}, Parents: map[string]dataPlaneParent{}}

	// The id types' ID() literals.
	for _, c := range spec.lits {
		p := l.load(c.pkg)
		if p == nil {
			return refuse("data-plane id not derived: "+c.pkg+" is not in the checkout", "")
		}
		m := p.method(c.typ, "ID")
		if m == nil || m.Body == nil {
			return refuse("data-plane id not derived: "+c.typ+" has no ID() to read", "")
		}
		got := stringLiterals(m.Body)
		if strings.Join(got, "\x00") != strings.Join(c.lits, "\x00") {
			return refuse("data-plane id not derived: "+c.typ+".ID() has the literals "+fmt.Sprintf("%q", got)+", the grammar was read for "+fmt.Sprintf("%q", c.lits), "")
		}
		info.IDLiterals[c.typ] = got
	}

	// The names the resource's code uses.
	used := map[string]bool{}
	for _, f := range []*ast.File{r.File, r.CreateFile} {
		if f == nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				used[v.Name] = true
			case *ast.SelectorExpr:
				used[v.Sel.Name] = true
			}
			return true
		})
	}
	for _, u := range spec.uses {
		if !used[u] {
			return refuse("data-plane id not derived: the resource's code does not use "+u+", which is where the host comes from", "")
		}
		info.Uses = append(info.Uses, u)
	}

	var comps []component
	for _, p := range spec.parts {
		switch {
		case p.lit != "":
			comps = append(comps, component{Literal: p.lit})
		case p.cloud != "":
			comps = append(comps, component{Cloud: p.cloud, Segment: p.seg, Rule: ruleDataPlane})
		case p.armArg != "":
			if !ts.Attrs[p.armArg].configurable() {
				return refuse("data-plane id not derived: the schema does not let a configuration set "+p.armArg, "")
			}
			parent, seg, why := l.armSegmentOf(r, p.armArg, p.armAfter)
			if why != "" {
				return refuse("data-plane id not derived: "+why, "")
			}
			pi := info.Parents[p.armArg]
			pi.IDType = parent
			if pi.Segments == nil {
				pi.Segments = map[string]armSegment{}
			}
			pi.Segments[p.seg] = seg
			info.Parents[p.armArg] = pi
			comps = append(comps, component{Attrs: []string{p.armArg}, Segment: p.seg, Rule: ruleDataPlane, ARMSegment: &seg})
		default:
			for _, a := range p.attrs {
				if !ts.Attrs[a].configurable() {
					return refuse("data-plane id not derived: the schema does not let a configuration set "+a, "")
				}
			}
			comps = append(comps, component{Attrs: p.attrs, ARMID: p.wholeID, Segment: p.seg, Rule: ruleDataPlane, URLEscape: p.escape, OmitIfAbsent: p.omit})
		}
	}
	e.Components = comps
	e.Template = dataPlaneTemplate(comps)
	e.DataPlane = info
	if comps[0].ARMID {
		e.Class = classParentID
	} else {
		e.Class = classTemplate
	}
	return e
}

// armSegmentOf is the position, in the ARM id the argument arg holds, of the
// value segment that follows the static segment after, read from the id type
// arg's validator parses.
func (l *loader) armSegmentOf(r registration, arg, after string) (idType string, seg armSegment, why string) {
	psrc, _, problem := l.argIDType(r, arg)
	if problem != "" || psrc == nil || psrc.Segments == nil {
		// An argument with no validator is still parsed by the code that
		// reads it: commonids.ParseStorageContainerID(d.Get("storage_container_id")).
		if alt := l.parsedArgIDType(r, arg); alt != nil && alt.Segments != nil {
			psrc, problem = alt, ""
		}
	}
	if problem != "" || psrc == nil || psrc.Segments == nil {
		if problem == "" {
			problem = "no id type with segments"
		}
		return "", seg, arg + ": " + problem
	}
	for i, s := range psrc.Segments {
		if s.Kind != segStatic || !strings.EqualFold(s.Value, after) {
			continue
		}
		if i+1 >= len(psrc.Segments) {
			break
		}
		next := psrc.Segments[i+1]
		if next.Kind != segUser {
			return "", seg, fmt.Sprintf("%s parses as %s, whose segment after %q is a %s one, not a name", arg, psrc.TypeName, after, next.Kind)
		}
		// The id's leading "/" makes the first segment position 1.
		return psrc.TypeName, armSegment{Index: i + 2, After: s.Value}, ""
	}
	return "", seg, fmt.Sprintf("%s parses as %s, which has no segment %q", arg, psrc.TypeName, after)
}

// stringLiterals are the string literals under n, in source order.
func stringLiterals(n ast.Node) []string {
	var out []string
	ast.Inspect(n, func(x ast.Node) bool {
		if b, ok := x.(*ast.BasicLit); ok && b.Kind == token.STRING {
			if s, err := strconv.Unquote(b.Value); err == nil {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// dataPlaneTemplate spells the id with value segments as {name}.
func dataPlaneTemplate(comps []component) string {
	var b strings.Builder
	for _, c := range comps {
		switch {
		case c.Literal != "":
			b.WriteString(c.Literal)
		default:
			b.WriteString("{" + c.Segment + "}")
		}
	}
	return b.String()
}

// dataPlaneSummary lists the data-plane types by outcome, for the report.
func dataPlaneSummary(entries []entry) (derived, refused []string) {
	for _, e := range entries {
		if _, ok := dataPlaneSpecs[e.Type]; !ok {
			continue
		}
		if e.Class == classUnmapped {
			refused = append(refused, e.Type)
		} else {
			derived = append(derived, e.Type)
		}
	}
	sort.Strings(derived)
	sort.Strings(refused)
	return
}

// parsedArgIDType is the id type the resource's code parses the argument arg
// with when its schema declares no validator: the ParseXxxID call whose
// argument reads d.Get(arg). A call that names Insensitively is not taken,
// which is a different grammar.
func (l *loader) parsedArgIDType(r registration, arg string) *idSource {
	if r.File == nil || r.Pkg == nil {
		return nil
	}
	var found *idSource
	ast.Inspect(r.File, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found != nil || len(call.Args) == 0 {
			return found == nil
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(sel.Sel.Name, "Parse") || !strings.HasSuffix(sel.Sel.Name, "ID") {
			return true
		}
		reads := false
		ast.Inspect(call.Args[0], func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Get" && len(c.Args) == 1 {
					if v, ok := stringLit(c.Args[0]); ok && v == arg {
						reads = true
					}
				}
			}
			return true
		})
		x, ok := sel.X.(*ast.Ident)
		if !reads || !ok {
			return true
		}
		path := r.Pkg.imports[r.File][x.Name]
		p := l.load(path)
		if p == nil {
			return true
		}
		typ := strings.TrimSuffix(strings.TrimPrefix(sel.Sel.Name, "Parse"), "ID") + "Id"
		if _, ok := p.types[typ]; !ok {
			return true
		}
		if src, problem := l.segmentsOf(p, typ); problem == "" {
			found = src
		}
		return true
	})
	return found
}
