// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The committed azurerm facts' pin. TO CHANGE A NUMBER HERE, regenerate
// with tools/azurerm-tags-gen against the new azurerm tag and say in the
// commit message what moved; the after-create list is what substrate's
// PostCreateNeeded answers for Azure.
const (
	pinnedAzurermVersion      = "5.10.0"
	pinnedAzurermCommit       = "b24dfdc1dafe6d0721572b0f6debecb7ab5e81c6"
	pinnedAzurermSchemaSHA256 = "e74450ee2895faa3d654f1324af6fa0e99f24ad2fe04fa3332a5efa7cfe158b3"
)

var pinnedRMTagCounts = RMTagCounts{Tagged: 387, InCreate: 385, AfterCreate: 2, Unknown: 0, Limited: 4}

// pinnedAfterCreate is the whole after-create list at the pin, each with
// the call that writes the tags.
var pinnedAfterCreate = map[string]string{
	// "An API issue prevents setting tags using the PUT operation, so
	// we'll patch them in after" (Azure/azure-rest-api-specs#14529).
	"azurerm_app_service_managed_certificate": "client.Update",
	// The alias PUT makes the subscription; its tags go on the
	// subscription scope through Microsoft.Resources/tags.
	"azurerm_subscription": "tagsClient.CreateOrUpdateAtScope",
}

// pinnedNotARMTags are the types whose `tags` is a list of labels.
var pinnedNotARMTags = []string{
	"azurerm_api_management_named_value",
	"azurerm_api_management_workspace_named_value",
	"azurerm_sentinel_threat_intelligence_indicator",
}

// TestCommittedRMTagsParse pins the committed facts' source and counts,
// the whole after-create list, and that the file survives a parse and
// render unchanged.
func TestCommittedRMTagsParse(t *testing.T) {
	facts, err := ParseRMTags(committedRMTags)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Version != pinnedAzurermVersion || facts.Commit != pinnedAzurermCommit || facts.SchemaSHA256 != pinnedAzurermSchemaSHA256 {
		t.Errorf("facts are from azurerm %s (%s, schema %s), pinned %s (%s, schema %s)",
			facts.Version, facts.Commit, facts.SchemaSHA256, pinnedAzurermVersion, pinnedAzurermCommit, pinnedAzurermSchemaSHA256)
	}
	if got := facts.Counts(); got != pinnedRMTagCounts {
		t.Errorf("counts %+v, pinned %+v", got, pinnedRMTagCounts)
	}
	if !bytes.Equal(facts.Render(), committedRMTags) {
		t.Errorf("%s does not round-trip through ParseRMTags and Render; it was edited by hand", RMTagsPath)
	}
	if !reflect.DeepEqual(facts.NotARMTags, pinnedNotARMTags) {
		t.Errorf("types whose tags are not ARM's tags: %v, pinned %v", facts.NotARMTags, pinnedNotARMTags)
	}
	after := map[string]string{}
	for _, r := range facts.Rows() {
		if r.Create == CreateAfterCreate {
			after[r.Type] = r.Call
		}
	}
	if !reflect.DeepEqual(after, pinnedAfterCreate) {
		t.Errorf("after-create rows %v, pinned %v", after, pinnedAfterCreate)
	}
}

// TestPostCreateTagsKnownRows pins answers known from reading the
// provider source, including the conditional-restore shape that a "first
// create call" rule gets wrong.
func TestPostCreateTagsKnownRows(t *testing.T) {
	cases := []struct {
		tfType       string
		after, known bool
	}{
		{"azurerm_app_service_managed_certificate", true, true},
		{"azurerm_subscription", true, true},
		{"azurerm_virtual_network", false, true},
		{"azurerm_resource_group", false, true},
		{"azurerm_storage_account", false, true},
		// A soft-deleted service is restored with an untagged PUT on
		// one path; every run then PUTs it with its tags.
		{"azurerm_api_management", false, true},
		// The typed SDK: a decoded model mapped by a receiver method.
		{"azurerm_dev_center", false, true},
		// No tags argument, a labels list, and no such type.
		{"azurerm_subnet", false, false},
		{"azurerm_sentinel_threat_intelligence_indicator", false, false},
		{"azurerm_nope", false, false},
	}
	for _, c := range cases {
		after, known := PostCreateTags(c.tfType)
		if after != c.after || known != c.known {
			t.Errorf("PostCreateTags(%q) = (%t, %t), want (%t, %t)", c.tfType, after, known, c.after, c.known)
		}
	}
}

// TestAzurermTagRulesKnownRows pins the four types whose azurerm
// validation is tighter than ARM's defaults, and a default one.
func TestAzurermTagRulesKnownRows(t *testing.T) {
	cases := []struct {
		tfType string
		want   TagRules
	}{
		{"azurerm_virtual_network", TagRules{Validator: "commonschema.Tags()", MaxTags: 50, MaxKeyLen: 512, MaxValueLen: 256}},
		{"azurerm_key_vault_secret", TagRules{Validator: "commonschema.TagsWithMaximumElements(15)", MaxTags: 15, MaxKeyLen: 512, MaxValueLen: 256}},
		{"azurerm_storage_account", TagRules{Validator: "schema literal, ValidateFunc validate.StorageAccountTags", MaxTags: 50, MaxKeyLen: 128, MaxValueLen: 256}},
		{"azurerm_data_share_account", TagRules{Validator: "commonschema.TagsWithLowerCaseKeys()", MaxTags: 50, MaxKeyLen: 512, MaxValueLen: 256, LowercaseKeys: true}},
		{"azurerm_application_insights_workbook", TagRules{Validator: "schema literal, ValidateFunc validate.WorkbookTags", MaxTags: 50, MaxKeyLen: 512, MaxValueLen: 256, ForbiddenKeys: []string{"hidden-title"}}},
	}
	for _, c := range cases {
		got, ok := AzurermTagRules(c.tfType)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("AzurermTagRules(%q) = %+v (%t), want %+v", c.tfType, got, ok, c.want)
		}
	}
	if _, ok := AzurermTagRules("azurerm_subnet"); ok {
		t.Error("azurerm_subnet has no tags argument, yet has rules")
	}
}

// TestCheckTag pins that a stamped marker is checked against the type's
// azurerm rules and ARM's forbidden key characters, and that a long
// tofu-address value is caught before plan.
func TestCheckTag(t *testing.T) {
	long := strings.Repeat("a", MaxValueLength+1)
	cases := []struct {
		tfType, key, value string
		errs               int
	}{
		{"azurerm_virtual_network", "tofu-address", "module.net.azurerm_virtual_network.main", 0},
		{"azurerm_virtual_network", "tofu-address", long, 1},
		{"azurerm_virtual_network", "tofu/address", "x", 1},
		{"azurerm_storage_account", strings.Repeat("k", 129), "x", 1},
		{"azurerm_virtual_network", strings.Repeat("k", 129), "x", 0},
		{"azurerm_data_share_account", "Tofu-Estate", "x", 1},
		{"azurerm_data_share_account", "tofu-estate", "x", 0},
		{"azurerm_application_insights_workbook", "hidden-title", "x", 1},
		{"azurerm_nope", "tofu-slot", "x", 0},
	}
	for _, c := range cases {
		if got := CheckTag(c.tfType, c.key, c.value); len(got) != c.errs {
			t.Errorf("CheckTag(%q, %q, %d chars) = %v, want %d errors", c.tfType, c.key, len(c.value), got, c.errs)
		}
	}
}

// TestAnalysisOnFixture runs the generator on a miniature provider under
// testdata, one resource per class, so the analysis itself is proven in
// CI where the real source is absent.
func TestAnalysisOnFixture(t *testing.T) {
	src := filepath.Join("testdata", "rmsrc")
	facts, err := GenerateAzurermTags(src, filepath.Join(src, "schema.json"), "0.0.1", strings.Repeat("0", 40))
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		class  CreateClass
		call   string
		reason string
	}
	cases := map[string]want{
		"azurerm_widget_inline":       {CreateInCreate, "client.CreateOrUpdate", ""},
		"azurerm_widget_after":        {CreateAfterCreate, "client.UpdateTags", ""},
		"azurerm_widget_restore":      {CreateInCreate, "client.CreateOrUpdate", ""},
		"azurerm_widget_viaupdate":    {CreateAfterCreate, "client.Update", ""},
		"azurerm_widget_untraced":     {CreateUnknown, "", "tags are read but not traced into an API call"},
		"azurerm_gizmo":               {CreateInCreate, "client.CreateOrUpdateThenPoll", ""},
		"azurerm_widget_unregistered": {CreateUnknown, "", "not found in the provider's registrations"},
	}
	rows := facts.Rows()
	if len(rows) != len(cases) {
		t.Errorf("%d rows, want %d (the labels-list and untagged types are not rows)", len(rows), len(cases))
	}
	for _, r := range rows {
		w, ok := cases[r.Type]
		if !ok {
			t.Errorf("unexpected row %s", r.Type)
			continue
		}
		if r.Create != w.class || r.Call != w.call || r.Reason != w.reason {
			t.Errorf("%s = (%s, %q, %q), want (%s, %q, %q)", r.Type, r.Create, r.Call, r.Reason, w.class, w.call, w.reason)
		}
	}
	if r, _ := facts.Row("azurerm_widget_restore"); !strings.HasSuffix(r.At, "widget_resources.go:80") {
		t.Errorf("restore's tagged PUT is at %s; want the unconditional one, widget_resources.go:80", r.At)
	}
	if !reflect.DeepEqual(facts.NotARMTags, []string{"azurerm_widget_labels"}) {
		t.Errorf("NotARMTags = %v", facts.NotARMTags)
	}
	wantRules := TagRules{Validator: "schema literal, ValidateFunc validate.WidgetTags", MaxTags: 10, MaxKeyLen: 64, MaxValueLen: 128, ForbiddenKeys: []string{"reserved"}}
	if r, _ := facts.Row("azurerm_widget_inline"); !reflect.DeepEqual(r.Rules, wantRules) {
		t.Errorf("widget rules = %+v, want %+v", r.Rules, wantRules)
	}
	// The rendered fixture must parse: the generator and the parser agree.
	if _, err := ParseRMTags(facts.Render()); err != nil {
		t.Errorf("the fixture's facts do not parse: %s", err)
	}
}

// TestParseRMTagsRefusesDamage proves ParseRMTags's own checks can fail.
func TestParseRMTagsRefusesDamage(t *testing.T) {
	good := string(committedRMTags)
	for name, bad := range map[string]string{
		"counts disagree": strings.Replace(good, `"after_create": 2`, `"after_create": 3`, 1),
		"no commit":       strings.Replace(good, pinnedAzurermCommit, "", 1),
		"unknown class":   strings.Replace(good, `"create": "after-create"`, `"create": "later"`, 1),
		"unknown field":   strings.Replace(good, `"create": "after-create"`, `"create": "after-create", "extra": 1`, 1),
		"unsorted":        strings.Replace(good, `"type": "azurerm_aadb2c_directory"`, `"type": "azurerm_zzz"`, 1),
	} {
		if bad == good {
			t.Fatalf("%s: the damage did not apply", name)
		}
		if _, err := ParseRMTags([]byte(bad)); err == nil {
			t.Errorf("%s: ParseRMTags accepted it", name)
		}
	}
}
