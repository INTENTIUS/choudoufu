// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestReadHandwrittenShapes runs the create reader over a small package of
// hand-written resources in testdata, one per shape, so the reader is
// tested without the pinned provider checkout -extract needs.
func TestReadHandwrittenShapes(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("testdata", "handwritten"))
	if err != nil {
		t.Fatal(err)
	}
	loader := newPkgLoader(repo)
	dir := filepath.Join(repo, "google", "services", "demo")
	read := func(typ string) HandwrittenID {
		t.Helper()
		h, err := readHandwritten(loader, dir, typ)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}

	for _, c := range []struct {
		typ, id, importer string
		short, raw        []string
		trim              map[string]string
		located           []string
	}{
		{typ: "google_demo_sprintf", id: "projects/{{project}}/things/{{name}}", importer: "passthrough"},
		{typ: "google_demo_helper", id: "projects/{{project}}/locations/{{location}}/widgets/{{name}}", importer: "func", located: []string{"location"}},
		{typ: "google_demo_method", id: "projects/{{project}}/regions/{{region}}/gadgets/{{name}}", importer: "none", short: []string{"name"}},
		{typ: "google_demo_folder", id: "folders/{{folder}}/notes/{{name}}", importer: "none", trim: map[string]string{"folder": "folders/"}},
	} {
		h := read(c.typ)
		if h.CreateID != c.id || h.CreateUnread != "" {
			t.Errorf("%s: read %q (unread %q), want %q", c.typ, h.CreateID, h.CreateUnread, c.id)
		}
		if h.Importer != c.importer {
			t.Errorf("%s: importer %q, want %q", c.typ, h.Importer, c.importer)
		}
		if !reflect.DeepEqual(h.CreateShort, c.short) || !reflect.DeepEqual(h.CreateRaw, c.raw) ||
			!reflect.DeepEqual(h.CreateTrim, c.trim) || !reflect.DeepEqual(h.CreateLocated, c.located) {
			t.Errorf("%s: short %v raw %v trim %v located %v, want %v %v %v %v", c.typ,
				h.CreateShort, h.CreateRaw, h.CreateTrim, h.CreateLocated, c.short, c.raw, c.trim, c.located)
		}
		if len(h.CreateIDAt) != 1 || !strings.Contains(h.CreateIDAt[0], "google/services/demo/demo.go:") {
			t.Errorf("%s: id read at %v", c.typ, h.CreateIDAt)
		}
	}

	helper := read("google_demo_helper")
	if helper.ImporterID != helper.CreateID || len(helper.ImporterFormats) != 1 || helper.ImporterHash == "" {
		t.Errorf("helper importer: id %q formats %v hash %q", helper.ImporterID, helper.ImporterFormats, helper.ImporterHash)
	}
	if !importerReadsBack(Source{CreateID: helper.CreateID, ImporterFormats: helper.ImporterFormats, ImporterID: helper.ImporterID}) {
		t.Error("the helper's ParseImportId importer is not read as reading its id back")
	}

	// Never guessed: each of these leaves the id unread and says why.
	for typ, why := range map[string]string{
		"google_demo_minted":  "what the API returned",
		"google_demo_twice":   "name is assigned 2 times",
		"google_demo_escaped": "escapes the value",
	} {
		h := read(typ)
		if h.CreateID != "" || !strings.Contains(h.CreateUnread, why) {
			t.Errorf("%s: read %q, unread %q; want it unread because %q", typ, h.CreateID, h.CreateUnread, why)
		}
		if minted := typ == "google_demo_minted"; h.CreateMinted != minted {
			t.Errorf("%s: minted = %v", typ, h.CreateMinted)
		}
	}
}

// TestHandwrittenVerdicts pins what the committed evidence decides for the
// hand-written types (#2145 item 1): a row for each id the create builds
// from configuration and the importer reads back, and a named refusal for
// each way that fails.
func TestHandwrittenVerdicts(t *testing.T) {
	_, ev, facts := loadForTest(t)
	out, err := Generate(ev, facts, loadNamesForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Decision{}
	for _, d := range out.Decisions {
		got[d.Type] = d
	}
	rowed := map[string]string{
		"google_app_engine_application":          "{{project}}",
		"google_container_cluster":               "projects/{{project}}/locations/{{location}}/clusters/{{name}}",
		"google_cloudfunctions_function":         "projects/{{project}}/locations/{{region}}/functions/{{name}}",
		"google_logging_folder_sink":             "folders/{{folder}}/sinks/{{name}}",
		"google_logging_project_exclusion":       "projects/{{project}}/exclusions/{{name}}",
		"google_dataproc_workflow_template":      "projects/{{project}}/locations/{{location}}/workflowTemplates/{{name}}",
		"google_gke_hub_feature_membership":      "projects/{{project}}/locations/{{location}}/features/{{feature}}/membershipId/{{membership}}",
		"google_project_organization_policy":     "{{project}}:{{constraint}}",
		"google_organization_policy":             "{{org_id}}/{{constraint}}",
		"google_compute_shared_vpc_host_project": "{{project}}",
	}
	for typ, tmpl := range rowed {
		d := got[typ]
		if d.Verdict != VerdictRowed || d.Template != tmpl {
			t.Errorf("%s: %s %q (%s), want rowed %q", typ, d.Verdict, d.Template, d.Reason, tmpl)
		}
	}
	refused := map[string]struct {
		verdict Verdict
		why     string
	}{
		"google_compute_instance_from_template": {VerdictUnrowed, "has no importer"},
		"google_compute_router_interface":       {VerdictUnrowed, "carries no {{project}}"},
		"google_sql_user":                       {VerdictUnrowed, "carries no {{project}}"},
		"google_billing_subaccount":             {VerdictMarkerless, "what the API returns"},
		"google_monitoring_dashboard":           {VerdictMarkerless, "what the API returns"},
		"google_sql_ssl_cert":                   {VerdictUnrowed, "what the API returns"},
		"google_dataflow_job":                   {VerdictUnrowed, "resource_dataflow_job.go:"},
		"google_service_networking_connection":  {VerdictUnrowed, "escapes the value"},
		"google_logging_folder_bucket_config":   {VerdictUnrowed, "assigned 2 times"},
	}
	for typ, w := range refused {
		d := got[typ]
		if d.Verdict != w.verdict || !strings.Contains(d.Reason, w.why) {
			t.Errorf("%s: %s (%s), want %s naming %q", typ, d.Verdict, d.Reason, w.verdict, w.why)
		}
	}
}

// TestImporterRulingsFollowTheSource proves the ruling guard can fail: a
// fingerprint, an id or an importer kind that moves under a ruling stops
// the generator.
func TestImporterRulingsFollowTheSource(t *testing.T) {
	_, ev, _ := loadForTest(t)
	if err := checkImporterRulings(ev); err != nil {
		t.Fatalf("the committed evidence fails its own rulings: %v", err)
	}
	mutate := func(typ string, f func(*Source)) Evidence {
		cp := Evidence{Types: map[string]map[string]Source{}}
		for k, v := range ev.Types {
			cp.Types[k] = v
		}
		byProv := map[string]Source{}
		for p, s := range ev.Types[typ] {
			f(&s)
			byProv[p] = s
		}
		cp.Types[typ] = byProv
		return cp
	}
	for name, cp := range map[string]Evidence{
		"fingerprint": mutate("google_logging_project_sink", func(s *Source) { s.ImporterHash = "000000000000" }),
		"id":          mutate("google_organization_policy", func(s *Source) { s.CreateID = "{{org_id}}" }),
		"kind":        mutate("google_project_usage_export_bucket", func(s *Source) { s.Importer = "passthrough" }),
	} {
		if err := checkImporterRulings(cp); err == nil {
			t.Errorf("a moved %s passes the ruling guard", name)
		}
	}
}
