// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1120 unit 17: the pins on azurerm's two identity routes.
//
// Unit 13 measured, with a harness it did not commit, that for every
// azurerm 5.10.0 type the identity-schema route admits, the identity it
// builds and the ARM id template from live/azurerm-ids.json name the same
// object. Where they agree the route renders the template; where they do
// not it falls back to the identity attributes alone, silently. These
// tests make that agreement a pin, run without the provider source, so a
// provider bump that moves one of them fails here by type and segment.

// azurermIdentityExtractPath is the identity schema and the argument flags
// of every azurerm 5.10.0 type that serves a resource identity schema, as
// `tofu providers schema -json` gives them, reduced to what the
// identity-schema route reads: which arguments the type has and whether
// each is required, optional or computed (never their types), and the
// identity attributes it requires for import (azurerm 5.10.0 marks every
// one required_for_import, and the writer refuses an extract where that
// stops being true rather than drop the distinction). Since GitHub issue
// #2138 it also carries, per type, the checks azurerm's generated resource
// identity test makes (identity_test), read from the provider source, so
// the record rung's generated identity sources are pinned against those
// tests without the source checked out
// ([TestAzurermIdentitySourcesAgreeWithTheIdentityTest]). The generator
// read the same tests to produce the sources, so the pin is a
// self-consistency check of one generator run against them, not an
// independent one ([readAzurermIdentityTests], #2140). Regenerate it
// with -update-azurerm-identity-extract and AZURERM_SCHEMA set, the
// variable internal/live/armtypes' pin test and the substrate's tags
// extract read, and AZURERM_SRC, the azurerm checkout at the same tag
// tools/azurerm-id-gen reads.
const azurermIdentityExtractPath = "testdata/azurerm-5.10.0-identity.json"

var updateAzurermIdentityExtract = flag.Bool("update-azurerm-identity-extract", false, "regenerate "+azurermIdentityExtractPath+" from $AZURERM_SCHEMA and $AZURERM_SRC")

type azurermIdentityExtract struct {
	Provider     string                                `json:"provider"`
	Version      string                                `json:"version"`
	SchemaSHA256 string                                `json:"schema_sha256"`
	Types        map[string]azurermIdentityExtractType `json:"types"`
}

type azurermIdentityExtractType struct {
	// Identity is the identity schema's attributes, every one required for
	// import.
	Identity []string `json:"identity"`
	// Arguments maps each configuration attribute to "required",
	// "optional", "optional+computed" or "computed".
	Arguments map[string]string `json:"arguments"`
	// IdentityTest is azurerm's generated resource identity test for the
	// type, one check per identity attribute, as the test file writes it
	// (GitHub issue #2138). Absent when azurerm publishes none.
	IdentityTest map[string]azurermIdentityTestCheck `json:"identity_test,omitempty"`
}

// azurermIdentityTestCheck is one check of an identity test, read off the
// call in the test file:
//
//   - "matches-state": statecheck.ExpectIdentityValueMatchesStateAtPath,
//     the identity value equals the state value at Path;
//   - "contains": customstatecheck.ExpectStateContainsIdentityValueAtPath,
//     the state value at Path contains the identity value;
//   - "value": statecheck.ExpectIdentityValue with a known value, String
//     for a string literal (knownvalue.StringExact("CNAME")) or Expr for
//     anything else, as written (data.Subscriptions.Primary).
type azurermIdentityTestCheck struct {
	Check  string `json:"check"`
	Path   string `json:"path,omitempty"`
	String string `json:"string,omitempty"`
	Expr   string `json:"expr,omitempty"`
}

// loadAzurermIdentityExtract reads the extract as provider schemas.
func loadAzurermIdentityExtract(t *testing.T) (azurermIdentityExtract, map[string]providers.Schema) {
	t.Helper()
	if *updateAzurermIdentityExtract {
		writeAzurermIdentityExtract(t)
	}
	data, err := os.ReadFile(azurermIdentityExtractPath)
	if err != nil {
		t.Fatal(err)
	}
	var ex azurermIdentityExtract
	if err := json.Unmarshal(data, &ex); err != nil {
		t.Fatal(err)
	}
	schemas := make(map[string]providers.Schema, len(ex.Types))
	for name, ty := range ex.Types {
		block := &configschema.Block{Attributes: map[string]*configschema.Attribute{}}
		for arg, flags := range ty.Arguments {
			a := &configschema.Attribute{Type: cty.String}
			switch flags {
			case "required":
				a.Required = true
			case "optional":
				a.Optional = true
			case "optional+computed":
				a.Optional, a.Computed = true, true
			case "computed":
				a.Computed = true
			default:
				t.Fatalf("%s.%s: flags %q", name, arg, flags)
			}
			block.Attributes[arg] = a
		}
		ident := &configschema.Object{Nesting: configschema.NestingSingle, Attributes: map[string]*configschema.Attribute{}}
		for _, attr := range ty.Identity {
			ident.Attributes[attr] = &configschema.Attribute{Type: cty.String, Required: true}
		}
		schemas[name] = providers.Schema{Block: block, IdentitySchema: ident}
	}
	return ex, schemas
}

// writeAzurermIdentityExtract regenerates the extract from $AZURERM_SCHEMA,
// with the digest taken the way internal/live/armtypes and
// tools/azurerm-id-gen take it: over a canonical re-encoding of the
// azurerm provider's schema.
func writeAzurermIdentityExtract(t *testing.T) {
	path := os.Getenv("AZURERM_SCHEMA")
	if path == "" {
		t.Fatal("-update-azurerm-identity-extract needs AZURERM_SCHEMA")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		ProviderSchemas map[string]json.RawMessage `json:"provider_schemas"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	raw, ok := doc.ProviderSchemas["registry.opentofu.org/hashicorp/azurerm"]
	if !ok {
		t.Fatal("no azurerm schema")
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatal(err)
	}
	canon, err := json.Marshal(generic)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(canon)
	var p struct {
		ResourceSchemas map[string]struct {
			Block struct {
				Attributes map[string]struct {
					Required bool `json:"required"`
					Optional bool `json:"optional"`
					Computed bool `json:"computed"`
				} `json:"attributes"`
			} `json:"block"`
		} `json:"resource_schemas"`
		ResourceIdentitySchemas map[string]struct {
			Attributes map[string]struct {
				RequiredForImport bool `json:"required_for_import"`
			} `json:"attributes"`
		} `json:"resource_identity_schemas"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	version, _ := substrate.AzurermIDSource()
	ex := azurermIdentityExtract{
		Provider:     "hashicorp/azurerm",
		Version:      version,
		SchemaSHA256: hex.EncodeToString(sum[:]),
		Types:        map[string]azurermIdentityExtractType{},
	}
	for name, is := range p.ResourceIdentitySchemas {
		ty := azurermIdentityExtractType{Arguments: map[string]string{}}
		for attr, a := range is.Attributes {
			if !a.RequiredForImport {
				t.Fatalf("%s's identity attribute %s is optional for import; the extract records required ones only and has to grow a field for this", name, attr)
			}
			ty.Identity = append(ty.Identity, attr)
		}
		sort.Strings(ty.Identity)
		for arg, a := range p.ResourceSchemas[name].Block.Attributes {
			switch {
			case a.Required:
				ty.Arguments[arg] = "required"
			case a.Optional && a.Computed:
				ty.Arguments[arg] = "optional+computed"
			case a.Optional:
				ty.Arguments[arg] = "optional"
			default:
				ty.Arguments[arg] = "computed"
			}
		}
		ex.Types[name] = ty
	}
	tests := readAzurermIdentityTests(t, os.Getenv("AZURERM_SRC"))
	for name, ty := range ex.Types {
		if checks, ok := tests[name]; ok {
			ty.IdentityTest = checks
			ex.Types[name] = ty
		}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ex); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(azurermIdentityExtractPath, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// azurermIDsRow is the part of a live/azurerm-ids.json row these tests
// read: the per-segment detail the embedded table does not carry.
type azurermIDsRow struct {
	Type          string `json:"type"`
	Class         string `json:"class"`
	TemplateClass string `json:"template_class"`
	Route         string `json:"route"`
	Template      string `json:"template"`
	Components    []struct {
		Literal string   `json:"literal"`
		Attrs   []string `json:"attrs"`
		Cloud   string   `json:"cloud"`
		ARMID   bool     `json:"arm_id"`
		Segment string   `json:"segment"`
	} `json:"components"`
	// IdentitySources and IdentitySourcesProblem are GitHub issue #2138's
	// record sources ([TestAzurermIdentitySourcesAgreeWithTheIdentityTest]).
	IdentitySources        map[string]azurermIDsSource `json:"identity_sources"`
	IdentitySourcesProblem string                      `json:"identity_sources_problem"`
}

type azurermIDsSource struct {
	Segment        string                `json:"segment"`
	Argument       string                `json:"argument"`
	Within         string                `json:"within"`
	ParentIDType   string                `json:"parent_id_type"`
	ParentTemplate string                `json:"parent_template"`
	ParentSegment  string                `json:"parent_segment"`
	ParentSegments []substrate.ARMIDPart `json:"parent_segments"`
	ParentParse    string                `json:"parent_parse"`
	Provider       string                `json:"provider"`
	Constant       string                `json:"constant"`
}

func loadAzurermIDsRows(t *testing.T) (schemaDigest string, rows map[string]azurermIDsRow) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "live", "azurerm-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var a struct {
		Pin struct {
			SchemaSHA256 string `json:"schema_sha256"`
		} `json:"pin"`
		Types []azurermIDsRow `json:"types"`
	}
	if err := json.Unmarshal(data, &a); err != nil {
		t.Fatal(err)
	}
	rows = make(map[string]azurermIDsRow, len(a.Types))
	for _, r := range a.Types {
		rows[r.Type] = r
	}
	return a.Pin.SchemaSHA256, rows
}

// azurermTemplateDisagreement compares the identity the identity-schema
// route builds for a type (one component per identity attribute, the
// subscription read from the provider block) with the ARM id template the
// row records, segment by segment and case-insensitively. It returns one
// line per difference, naming the segment, and nothing when every value
// segment of the template reads exactly one identity attribute (or the
// cloud value that answers it) and every identity attribute is read by
// exactly one segment.
func azurermTemplateDisagreement(row azurermIDsRow, identityAttrs []string, cloud map[string]CloudValue) []string {
	want := map[string]int{}
	for _, a := range identityAttrs {
		want[strings.ToLower(a)] = 0
	}
	var out []string
	for _, c := range row.Components {
		var reads string
		switch {
		case c.Cloud != "":
			for attr, v := range cloud {
				if string(v) == c.Cloud {
					reads = strings.ToLower(attr)
				}
			}
			if reads == "" {
				out = append(out, fmt.Sprintf("segment {%s} is the provider block's %s, which answers no identity attribute", c.Segment, c.Cloud))
				continue
			}
		case c.ARMID:
			out = append(out, fmt.Sprintf("segments up to {%s} are read whole from %s, a parent's ARM id, which is no identity attribute", c.Segment, strings.Join(c.Attrs, " or ")))
			continue
		case len(c.Attrs) == 1:
			reads = strings.ToLower(c.Attrs[0])
		case len(c.Attrs) > 1:
			out = append(out, fmt.Sprintf("segment {%s} reads any of %s, not one identity attribute", c.Segment, strings.Join(c.Attrs, ", ")))
			continue
		default:
			continue // a literal
		}
		n, ok := want[reads]
		if !ok {
			out = append(out, fmt.Sprintf("segment {%s} reads %s, which the identity schema does not name", c.Segment, reads))
			continue
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("segment {%s} reads %s a second time", c.Segment, reads))
		}
		want[reads] = n + 1
	}
	var missing []string
	for a, n := range want {
		if n == 0 {
			missing = append(missing, a)
		}
	}
	sort.Strings(missing)
	for _, a := range missing {
		out = append(out, fmt.Sprintf("identity attribute %s is read by no segment of %s", a, row.Template))
	}
	return out
}

// azurermIdentityRouteAdmitted is how many azurerm 5.10.0 identity-schema
// types the identity-schema route admits on the schemas alone. TO CHANGE
// IT, say in the commit which types moved and why.
const azurermIdentityRouteAdmitted = 103

// TestAzurermIdentityExtractIsThePinnedSchema ties the extract to the
// artifact: the same azurerm version and schema digest, and exactly the
// artifact's identity-schema rows.
func TestAzurermIdentityExtractIsThePinnedSchema(t *testing.T) {
	ex, _ := loadAzurermIdentityExtract(t)
	digest, rows := loadAzurermIDsRows(t)
	version, _ := substrate.AzurermIDSource()
	if ex.Version != version || ex.SchemaSHA256 != digest {
		t.Fatalf("the extract is azurerm %s (schema %s); live/azurerm-ids.json is %s (schema %s): regenerate one of them", ex.Version, ex.SchemaSHA256, version, digest)
	}
	for name, row := range rows {
		_, inExtract := ex.Types[name]
		if (row.Class == "identity-schema") != inExtract {
			t.Errorf("%s: class %s in the artifact, in the extract %v", name, row.Class, inExtract)
		}
	}
	for name := range ex.Types {
		if _, ok := rows[name]; !ok {
			t.Errorf("%s is in the extract and not in the artifact", name)
		}
	}
}

// TestAzurermIdentityRouteAgreesWithTheTemplate is condition (a) of the
// template fallback's approval (#1120 unit 17). For every azurerm type the
// identity-schema route admits, on the schemas alone or under any
// configuration that names it, the identity the route builds and the ARM
// id template have to name the same object: each value segment of the
// template reads exactly one identity attribute (by its argument, or the
// provider block's subscription for subscription_id), and each identity
// attribute is read by exactly one segment, compared case-insensitively.
// The route then renders the template ([armTemplateEntry]), and this test
// also requires that it did. A provider bump that makes the two disagree
// would otherwise drop the type, silently, to an identity that is no ARM
// id; here it fails by type and segment.
func TestAzurermIdentityRouteAgreesWithTheTemplate(t *testing.T) {
	_, schemas := loadAzurermIdentityExtract(t)
	_, rows := loadAzurermIDsRows(t)
	cloud := substrate.AzurermCloudIdentityAttrs()
	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	admitted := 0
	for _, name := range names {
		reduced, cloudAttrs := withoutCloudIdentityAttrs(schemas[name], cloud)
		_, _, cohort := cohortAttrs(schemas, name, reduced)
		strict := IdentitySchemaRouteAdmits(schemas, name, cloud)
		if !strict && !cohort {
			continue
		}
		admitted++
		row := rows[name]
		if len(row.Components) == 0 {
			t.Errorf("%s: the identity-schema route admits it and it has no ARM id template (%s) to agree with", name, row.TemplateClass)
			continue
		}
		required, _ := identityAttrs(schemas[name].IdentitySchema)
		for _, line := range azurermTemplateDisagreement(row, required, cloudAttrs) {
			t.Errorf("%s: %s", name, line)
		}
		ti, refusal := synthesizeTypeIdentity(name, schemas, nil)
		if refusal != "" || !ti.ARMResourceID || ti.ImportSyntax != row.Template {
			t.Errorf("%s: the route did not render the template %s: %+v, %q", name, row.Template, ti, refusal)
		}
	}
	if admitted != azurermIdentityRouteAdmitted {
		t.Errorf("the identity-schema route admits %d azurerm types, pinned %d", admitted, azurermIdentityRouteAdmitted)
	}
}

// TestAzurermTemplateDisagreementNamesTheSegment is the comparison above
// shown failing: a template whose segments are not the identity's.
func TestAzurermTemplateDisagreementNamesTheSegment(t *testing.T) {
	_, rows := loadAzurermIDsRows(t)
	row := rows["azurerm_storage_container"]
	cloud := map[string]CloudValue{"subscription_id": CloudSubscriptionID}
	got := strings.Join(azurermTemplateDisagreement(row, []string{"name", "resource_group_name", "storage_account_name", "subscription_id"}, cloud), "\n")
	for _, want := range []string{
		"segments up to {storageAccountName} are read whole from storage_account_id",
		"identity attribute storage_account_name is read by no segment",
		"identity attribute subscription_id is read by no segment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disagreement %q does not say %q", got, want)
		}
	}
	row = rows["azurerm_resource_group"]
	if got := azurermTemplateDisagreement(row, []string{"Name", "subscription_id"}, cloud); len(got) != 0 {
		t.Errorf("azurerm_resource_group disagrees with itself, letter case aside: %v", got)
	}
	if got := azurermTemplateDisagreement(row, []string{"name", "location", "subscription_id"}, cloud); len(got) != 1 || !strings.Contains(got[0], "location") {
		t.Errorf("an identity attribute no segment reads went unreported: %v", got)
	}
}

// TestAzurermRoutesMatchTheRecord is condition (c)'s guard on the
// identity side: the route tools/azurerm-id-gen recorded for each
// identity-schema type is the one this package takes today, read off
// [synthesizeTypeIdentity] and the family's answer, with no configuration.
// No such type is in the cohort a configuration's naming could admit
// ([cohortAttrs]), so the record does not depend on a configuration; if a
// provider bump puts one there, this fails rather than let the record
// claim a route a configuration can change.
func TestAzurermRoutesMatchTheRecord(t *testing.T) {
	_, schemas := loadAzurermIdentityExtract(t)
	_, rows := loadAzurermIDsRows(t)
	cloud := substrate.AzurermCloudIdentityAttrs()
	counts := map[string]int{}
	for name, schema := range schemas {
		reduced, _ := withoutCloudIdentityAttrs(schema, cloud)
		if _, _, cohort := cohortAttrs(schemas, name, reduced); cohort {
			t.Errorf("%s: a configuration that names it could move it to the identity-schema route, so its recorded route %q is not the type's alone", name, rows[name].Route)
		}
		family, _ := substrate.Azure.SynthesizeIdentity(name, schema)
		_, refusal := synthesizeTypeIdentity(name, schemas, nil)
		var route string
		switch {
		case IdentitySchemaRouteAdmits(schemas, name, cloud):
			route = substrate.AzurermRouteIdentitySchema
		case family.ARMTemplate != nil && refusal == "":
			route = substrate.AzurermRouteTemplateFallback
		case family.TemplateWithheld != "" && strings.Contains(refusal, "withheld"):
			route = substrate.AzurermRouteWithheld
		case refusal != "":
			route = substrate.AzurermRouteUnmapped
		default:
			t.Errorf("%s: admitted by neither the route nor a template", name)
		}
		counts[route]++
		if rows[name].Route != route {
			t.Errorf("%s: recorded route %q, this package takes %q", name, rows[name].Route, route)
		}
	}
	t.Logf("identity-schema types by route: %v", counts)
}

// TestAzurermFallbackRefusesAnUnresolvableSegment is condition (b) seen
// from admission: a type whose template a guard withholds is refused, and
// the refusal names the segment or the missing evidence rather than the
// route's own reason.
func TestAzurermFallbackRefusesAnUnresolvableSegment(t *testing.T) {
	_, schemas := loadAzurermIdentityExtract(t)

	// No identity test from azurerm: nothing says volume_id holds the
	// identity's account, pool and volume names.
	_, refusal := synthesizeTypeIdentity("azurerm_netapp_volume_bucket", schemas, nil)
	if !strings.Contains(refusal, "withheld") || !strings.Contains(refusal, "no resource identity test") {
		t.Errorf("azurerm_netapp_volume_bucket refusal = %q", refusal)
	}

	// A provider whose storage_account_id is computed-only: the segment
	// no configuration can supply is named.
	served := schemas["azurerm_storage_container"]
	block := *served.Block
	block.Attributes = map[string]*configschema.Attribute{}
	for k, v := range served.Block.Attributes {
		block.Attributes[k] = v
	}
	block.Attributes["storage_account_id"] = &configschema.Attribute{Type: cty.String, Computed: true}
	served.Block = &block
	skewed := map[string]providers.Schema{"azurerm_storage_container": served}
	_, refusal = synthesizeTypeIdentity("azurerm_storage_container", skewed, nil)
	if !strings.Contains(refusal, "segment {storageAccountName} reads storage_account_id") {
		t.Errorf("azurerm_storage_container with a computed storage_account_id: refusal = %q", refusal)
	}
	if _, refusal := synthesizeTypeIdentity("azurerm_storage_container", schemas, nil); refusal != "" {
		t.Errorf("azurerm_storage_container as served at 5.10.0 is refused: %q", refusal)
	}

	// No identity schema at all: the segment guard's reason.
	analytics := map[string]providers.Schema{"azurerm_application_insights_analytics_item": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"application_insights_id": {Type: cty.String, Required: true},
		"scope":                   {Type: cty.String, Required: true},
		"name":                    {Type: cty.String, Required: true},
	}}}}
	_, refusal = synthesizeTypeIdentity("azurerm_application_insights_analytics_item", analytics, nil)
	if !strings.Contains(refusal, "segment {scopePath} is read from scope") {
		t.Errorf("azurerm_application_insights_analytics_item refusal = %q", refusal)
	}
}
