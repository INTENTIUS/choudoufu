// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureRows runs the generator over testdata/src, a miniature azurerm
// checkout holding one of each shape the real provider uses.
func fixtureRows(t *testing.T) map[string]entry {
	t.Helper()
	a, err := generate(filepath.Join("testdata", "src"), filepath.Join("testdata", "schema.json"))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if a.Pin.Version != "0.0.1-fixture" {
		t.Errorf("pin version = %q, want the fixture's version/VERSION", a.Pin.Version)
	}
	if a.Pin.Commit != "" {
		t.Errorf("pin commit = %q: a fixture inside this repository must not record this repository's HEAD", a.Pin.Commit)
	}
	out := map[string]entry{}
	for _, e := range a.Types {
		out[e.Type] = e
	}
	return out
}

// mappedAttrs flattens a row's components to "attr" or "attr(arm-id)" or
// "cloud:x", in order: the shape a later unit turns into identity rows.
func mappedAttrs(e entry) []string {
	var out []string
	for _, c := range e.Components {
		switch {
		case c.Cloud != "":
			out = append(out, "cloud:"+c.Cloud)
		case len(c.Attrs) > 0 && c.ARMID:
			out = append(out, c.Attrs[0]+"(arm-id)")
		case len(c.Attrs) > 0:
			out = append(out, c.Attrs[0])
		}
	}
	return out
}

func TestFixtureClassifiesEachShape(t *testing.T) {
	rows := fixtureRows(t)
	cases := []struct {
		typ, class, kind, idType string
		attrs                    []string
		template, reason         string
	}{
		{
			// A hand parser under internal/services/*/parse, read from ID().
			typ: "azurerm_widget", class: classTemplate, kind: "hand-parser", idType: "WidgetId",
			attrs:    []string{"cloud:subscription-id", "resource_group_name", "name"},
			template: "/subscriptions/{subscriptionId}/resourceGroups/{resourceGroup}/providers/Microsoft.Widget/widgets/{name}",
		},
		{
			// A go-azure-sdk resourceids type through a typed
			// IDValidationFunc, two calls deep (Validate -> Parse).
			typ: "azurerm_widget_gizmo", class: classTemplate, kind: "resourceids", idType: "GizmoId",
			attrs: []string{"cloud:subscription-id", "resource_group_name", "widget_name", "name"},
		},
		{
			// The same id where the schema names the parent by id. The
			// computed-only widget_name and resource_group_name must not be
			// mapped to: a configuration cannot set them.
			typ: "azurerm_widget_gizmo_legacy", class: classParentID, kind: "resourceids", idType: "GizmoId",
			attrs: []string{"widget_id(arm-id)", "name"},
		},
		{
			// A commonids reference; the identity schema keeps it, and the
			// template is still recorded beside it.
			typ: "azurerm_widget_storage", class: classIdentitySchema, kind: "resourceids", idType: "StorageAccountId",
			attrs: []string{"cloud:subscription-id", "resource_group_name", "name"},
		},
		{
			// A parser whose result states no segments is reported by its
			// own type, never by the helper id its body happens to parse.
			typ: "azurerm_widget_link", class: classUnmapped, kind: "custom", idType: "LinkId",
			reason: "id type states no segments (a parser with its own grammar)",
		},
		{
			typ: "azurerm_widget_ghost", class: classUnmapped,
			reason: "not found in the provider's registrations",
		},
	}
	if len(rows) != len(cases) {
		t.Errorf("fixture produced %d rows, want %d", len(rows), len(cases))
	}
	for _, c := range cases {
		e, ok := rows[c.typ]
		if !ok {
			t.Errorf("%s: no row", c.typ)
			continue
		}
		if e.Class != c.class {
			t.Errorf("%s: class %q, want %q (reason %q %s)", c.typ, e.Class, c.class, e.Reason, e.Detail)
		}
		if c.kind != "" && (e.Source == nil || e.Source.Kind != c.kind || e.Source.TypeName != c.idType) {
			t.Errorf("%s: source %+v, want %s %s", c.typ, e.Source, c.kind, c.idType)
		}
		if got := mappedAttrs(e); !reflect.DeepEqual(got, c.attrs) && !(len(got) == 0 && len(c.attrs) == 0) {
			t.Errorf("%s: mapped %v, want %v", c.typ, got, c.attrs)
		}
		if c.template != "" && e.Template != c.template {
			t.Errorf("%s: template %q, want %q", c.typ, e.Template, c.template)
		}
		if e.Reason != c.reason {
			t.Errorf("%s: reason %q, want %q", c.typ, e.Reason, c.reason)
		}
	}
	if got := rows["azurerm_widget_storage"].TemplateClass; got != classTemplate {
		t.Errorf("azurerm_widget_storage template_class = %q, want template", got)
	}
}

// TestNoComponentNamesAnUnsettableArgument is the schema check stated over
// the whole fixture: every argument a component maps to is one the type
// declares as required or optional.
func TestNoComponentNamesAnUnsettableArgument(t *testing.T) {
	schemas, _, err := readSchema(filepath.Join("testdata", "schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, e := range fixtureRows(t) {
		for _, c := range e.Components {
			for _, a := range c.Attrs {
				if !schemas[name].Attrs[a].configurable() {
					t.Errorf("%s maps %s, which its schema does not let a configuration set", name, a)
				}
			}
		}
	}
}

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"storageAccountName": "storage_account_name",
		"VMName":             "vm_name",
		"serverGroupsv2Name": "server_groupsv2_name",
		"name":               "name",
		"roleDefinitionId":   "role_definition_id",
	} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSubscriptionFormFromValidator: a type's own subscription_id is read
// as a GUID or as a full id from the validator its schema declares, and an
// unrecognised validator decides nothing.
func TestSubscriptionFormFromValidator(t *testing.T) {
	for src, want := range map[string]subscriptionForm{
		`package x; var s = map[string]int{"subscription_id": {ValidateFunc: validation.IsUUID}}`:                "guid",
		`package x; var s = map[string]int{"subscription_id": {ValidateFunc: commonids.ValidateSubscriptionID}}`: "arm-id",
		`package x; var s = map[string]int{"subscription_id": {ValidateFunc: validation.StringIsNotEmpty}}`:      "",
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := subscriptionFormIn(f); got != want {
			t.Errorf("%s: form %q, want %q", src, got, want)
		}
	}
}

// TestRegeneratingFromThePinReproducesTheArtifact proves the committed
// live/azurerm-ids.json is what this generator writes from the source it
// names. It needs the azurerm checkout at the pinned tag and a providers
// schema for the same version, which CI does not have; it skips, saying
// so, when they are absent, and refuses a checkout or schema that is not
// the recorded pin rather than reporting a meaningless difference.
func TestRegeneratingFromThePinReproducesTheArtifact(t *testing.T) {
	src, schema := os.Getenv("AZURERM_SRC"), os.Getenv("AZURERM_SCHEMA")
	if src == "" || schema == "" {
		t.Skip("set AZURERM_SRC to a terraform-provider-azurerm checkout at the pinned tag and AZURERM_SCHEMA to " +
			"`tofu providers schema -json` output for the same version to run this pin check")
	}
	for _, p := range []string{src, schema} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("%s is absent (%v); the pin check needs it", p, err)
		}
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "live", "azurerm-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded artifact
	if err := json.Unmarshal(committed, &recorded); err != nil {
		t.Fatal(err)
	}
	a, err := generate(src, schema)
	if err != nil {
		t.Fatal(err)
	}
	if a.Pin != recorded.Pin {
		t.Fatalf("the source is not the recorded pin: got %+v, artifact records %+v", a.Pin, recorded.Pin)
	}
	got, err := encode(a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, committed) {
		gl, cl := strings.Split(string(got), "\n"), strings.Split(string(committed), "\n")
		for i := 0; i < len(gl) && i < len(cl); i++ {
			if gl[i] != cl[i] {
				t.Fatalf("regenerating from the pin differs from live/azurerm-ids.json at line %d:\n got: %s\nwant: %s\n"+
					"rerun go run ./tools/azurerm-id-gen -src $AZURERM_SRC -schema $AZURERM_SCHEMA and review the diff", i+1, gl[i], cl[i])
			}
		}
		t.Fatalf("regenerating from the pin differs from live/azurerm-ids.json in length (%d vs %d lines)", len(gl), len(cl))
	}
}

// TestTemplatesMeet (#1120 unit 13): Read's id type confirms the
// importer's when some id is an id of both, and replaces it when none is.
func TestTemplatesMeet(t *testing.T) {
	st := func(v string) segment { return segment{Kind: segStatic, Value: v} }
	user := segment{Kind: segUser}
	scope := segment{Kind: segScope}
	lb := []segment{st("subscriptions"), user, st("resourceGroups"), user, st("providers"), st("Microsoft.Network"), st("loadBalancers"), user}
	rule := append(append([]segment(nil), lb...), st("inboundNatRules"), user)
	scoped := []segment{scope, st("providers"), st("Microsoft.CostManagement"), st("exports"), user}
	billing := []segment{st("providers"), st("Microsoft.Billing"), st("billingAccounts"), user, st("providers"), st("microsoft.costmanagement"), st("exports"), user}
	tableDefault := []segment{st("tableServices"), st("default"), st("tables"), user}
	tableAny := []segment{st("tableServices"), user, st("tables"), user}
	for _, c := range []struct {
		a, b []segment
		want bool
	}{
		{lb, rule, false},
		{rule, rule, true},
		{scoped, billing, true},
		{tableDefault, tableAny, true},
		{scoped, lb[:2], false},
	} {
		if got := templatesMeet(c.a, c.b); got != c.want {
			t.Errorf("templatesMeet(%s, %s) = %v, want %v", template(c.a), template(c.b), got, c.want)
		}
	}
}

// TestValidatorForm: a validator naming an id parses a full ARM id, and
// such an argument never fills one segment.
func TestValidatorForm(t *testing.T) {
	for text, want := range map[string]subscriptionForm{
		"validation.IsUUID.":                "guid",
		"commonids.ValidateSubscriptionID.": "arm-id",
		"validate.ManagementGroupID.":       "arm-id",
		"api.ValidateApiID.":                "arm-id",
		"validation.StringIsNotEmpty.":      "",
		"validate.ApiManagementChildName.":  "",
	} {
		if got := validatorForm(text); got != want {
			t.Errorf("validatorForm(%q) = %q, want %q", text, got, want)
		}
	}
}

// TestFixtureRecordsContractsAndRoutes (#1120 unit 17): an identity-schema
// row carries azurerm's identity test, and every row the route
// internal/live/substrate and internal/live/identity give it.
func TestFixtureRecordsContractsAndRoutes(t *testing.T) {
	rows := fixtureRows(t)
	want := map[string]string{
		"azurerm_widget":              "template",
		"azurerm_widget_gizmo":        "template",
		"azurerm_widget_gizmo_legacy": "parent-id",
		"azurerm_widget_storage":      "identity-schema",
		"azurerm_widget_link":         "unmapped",
		"azurerm_widget_ghost":        "unmapped",
	}
	for typ, route := range want {
		if got := rows[typ].Route; got != route {
			t.Errorf("%s: route %q, want %q", typ, got, route)
		}
	}
	c := rows["azurerm_widget_storage"].IdentityContract
	if c == nil {
		t.Fatal("azurerm_widget_storage carries no identity contract")
	}
	if c.Source != "internal/services/widget/widget_storage_resource_identity_gen_test.go" {
		t.Errorf("contract source %q", c.Source)
	}
	if !reflect.DeepEqual(c.Attributes, map[string]contractAttr{
		"name":                {Argument: "name"},
		"resource_group_name": {Argument: "resource_group_name"},
		"subscription_id":     {ProviderSubscription: true},
	}) {
		t.Errorf("contract attributes %+v", c.Attributes)
	}
	if rows["azurerm_widget"].IdentityContract != nil {
		t.Error("a type with no identity schema carries a contract")
	}
}

// TestContractChecks reads each check azurerm's identity tests make.
func TestContractChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x_resource_identity_gen_test.go")
	src := `package x_test
func TestX(t *testing.T) {
	_ = []statecheck.StateCheck{
		statecheck.ExpectIdentityValueMatchesStateAtPath("azurerm_x.test", tfjsonpath.New("name"), tfjsonpath.New("name")),
		customstatecheck.ExpectStateContainsIdentityValueAtPath("azurerm_x.test", tfjsonpath.New("account_name"), tfjsonpath.New("account_id")),
		statecheck.ExpectIdentityValue("azurerm_x.test", tfjsonpath.New("subscription_id"), knownvalue.StringExact(data.Subscriptions.Secondary)),
		statecheck.ExpectIdentityValue("azurerm_x.test", tfjsonpath.New("record_type"), knownvalue.StringExact("TXT")),
		statecheck.ExpectIdentityValue("azurerm_x.test", tfjsonpath.New("odd"), knownvalue.NotNull()),
	}
}
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := contractsIn(path, "x_resource_identity_gen_test.go")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]contractAttr{
		"name":            {Argument: "name"},
		"account_name":    {Within: "account_id"},
		"subscription_id": {ProviderSubscription: true},
		"record_type":     {Constant: "TXT"},
		"odd":             {Other: "knownvalue.NotNull."},
	}
	if got["azurerm_x"] == nil || !reflect.DeepEqual(got["azurerm_x"].Attributes, want) {
		t.Errorf("contracts = %+v, want %+v", got["azurerm_x"], want)
	}
}
