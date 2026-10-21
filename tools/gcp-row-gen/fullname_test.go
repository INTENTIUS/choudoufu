// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// fullNameSource is a Magic Modules type whose id and Asset name are its
// whole `name` (#2145 item 5).
func fullNameSource() Source {
	return Source{
		Kind: "resource", Generation: "mmv1", IDFormat: "{{name}}", APIService: "things.googleapis.com",
		DocImportFormats: []string{"projects/{{project}}/locations/{{location}}/things/{{thing_id}}", "{{location}}/{{thing_id}}"},
	}
}

func fullNameFacts() map[string]TypeFacts {
	return map[string]TypeFacts{ProviderGoogle: {Attrs: map[string]string{
		"name": "C", "project": "OC", "location": "R", "thing_id": "R", "labels": "O",
	}}}
}

func fullNamePage(projectVar string) AssetNames {
	return AssetNames{Types: map[string][]string{
		"things.googleapis.com/Thing": {"//things.googleapis.com/projects/{" + projectVar + "}/locations/{LOCATION}/things/{THING}"},
	}}
}

func TestReadImporterShapes(t *testing.T) {
	text := `
func resourceThingImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config := meta.(*transport_tpg.Config)
	if err := tpgresource.ParseImportId([]string{
		"^projects/(?P<project>[^/]+)/locations/(?P<location>[^/]+)/things/(?P<thing_id>[^/]+)$",
		"^(?P<location>[^/]+)/(?P<thing_id>[^/]+)$",
	}, d, config); err != nil {
		return nil, err
	}
	if err := d.Set("name", "projects/{{project}}/locations/{{location}}/things/{{thing_id}}"); err != nil {
		return nil, err
	}
	id, err := tpgresource.ReplaceVars(d, config, d.Get("name").(string))
	if err != nil {
		return nil, err
	}
	d.SetId(id)
	return []*schema.ResourceData{d}, nil
}
`
	var s Source
	readImporterShapes(&s, text)
	if s.ImportNameSet == nil || s.ImportNameSet.Attr != "name" || s.ImportNameSet.Template != "projects/{{project}}/locations/{{location}}/things/{{thing_id}}" || len(s.ImportNameSet.Regexes) != 2 {
		t.Errorf("import name set %+v", s.ImportNameSet)
	}
	if s.ImportWhole != "" || s.ImportPassthrough {
		t.Errorf("whole %q, passthrough %v; want neither", s.ImportWhole, s.ImportPassthrough)
	}

	s = Source{}
	readImporterShapes(&s, "\nfunc resourceThingImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {\n\tif err := tpgresource.ParseImportId([]string{\"(?P<name>.+)\"}, d, config); err != nil {\n\t\treturn nil, err\n\t}\n}\n")
	if s.ImportWhole != "name" {
		t.Errorf("whole %q, want name", s.ImportWhole)
	}

	s = Source{}
	readImporterShapes(&s, "Importer: &schema.ResourceImporter{\n\t\t\tState: schema.ImportStatePassthrough,\n\t\t},")
	if !s.ImportPassthrough {
		t.Error("passthrough not read")
	}
}

// TestDecideFullNameSetByImporter: an Asset name that is `name` whole, for
// an importer that sets name to its first regex's template, takes that
// template as the Asset path, as it is.
func TestDecideFullNameSetByImporter(t *testing.T) {
	src := fullNameSource()
	before := decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), fullNamePage("PROJECT_ID"))
	if before.Verdict != AssetUnknown {
		t.Fatalf("without the importer shape: verdict %s, want unknown", before.Verdict)
	}

	src.ImportNameSet = &ImportNameSet{
		Attr:     "name",
		Template: "projects/{{project}}/locations/{{location}}/things/{{thing_id}}",
		Regexes:  []string{"^projects/(?P<project>[^/]+)/locations/(?P<location>[^/]+)/things/(?P<thing_id>[^/]+)$", "^(?P<location>[^/]+)/(?P<thing_id>[^/]+)$"},
	}
	d := decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), fullNamePage("PROJECT_ID"))
	if d.Verdict != AssetAsIs || d.Row.Host != "things.googleapis.com" || d.Row.Asset != src.ImportNameSet.Template || d.Row.Import.Regex != src.ImportNameSet.Regexes[0] {
		t.Fatalf("verdict %s, row %+v (%s)", d.Verdict, d.Row, d.Reason)
	}

	d = decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), fullNamePage("PROJECT_NUMBER"))
	if d.Verdict != AssetUnknown || !strings.Contains(d.Reason, "project number") {
		t.Errorf("page with a project number: verdict %s (%s), want unknown for the project number", d.Verdict, d.Reason)
	}

	d = decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), AssetNames{})
	if d.Verdict != AssetUnknown || !strings.Contains(d.Reason, "no Asset name Google documents") {
		t.Errorf("no page entry: verdict %s (%s), want unknown", d.Verdict, d.Reason)
	}
}

// TestDecideFullNameWholeImportID: an importer that takes the whole import
// ID as name accepts the Asset path when the page's name lines up with one
// of its documented forms, and is refused when that name has a project
// number.
func TestDecideFullNameWholeImportID(t *testing.T) {
	src := fullNameSource()
	src.ImportWhole = "name"
	d := decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), fullNamePage("PROJECT_ID"))
	if d.Verdict != AssetAsIs || d.Row.Asset != "projects/{{project}}/locations/{{location}}/things/{{thing_id}}" {
		t.Fatalf("verdict %s, row %+v (%s)", d.Verdict, d.Row, d.Reason)
	}
	d = decideAsset("google_thing", map[string]Source{ProviderGoogle: src}, fullNameFacts(), fullNamePage("PROJECT_NUMBER"))
	if d.Verdict != AssetUnknown || !strings.Contains(d.Reason, "project number") || d.Row.Host != "" {
		t.Errorf("verdict %s, row %+v (%s); want unknown for the project number", d.Verdict, d.Row, d.Reason)
	}
}

// TestItem5Types decides the three types #2145 item 5 names from the
// committed evidence.
func TestItem5Types(t *testing.T) {
	_, ev, facts := loadForTest(t)
	names := loadNamesForTest(t)
	byType := func(typ string) map[string]TypeFacts {
		out := map[string]TypeFacts{}
		for p, pf := range facts.Providers {
			if tf, ok := pf.Types[typ]; ok {
				out[p] = tf
			}
		}
		return out
	}
	for typ, want := range map[string]struct {
		verdict AssetVerdict
		reason  string
	}{
		"google_apihub_api_hub_instance":     {AssetAsIs, ""},
		"google_service_directory_namespace": {AssetUnknown, "a project number is not the project ID"},
		"google_dataflow_job":                {AssetUnknown, "schema.ImportStatePassthrough"},
	} {
		d := decideAsset(typ, ev.Types[typ], byType(typ), names)
		if d.Verdict != want.verdict || !strings.Contains(d.Reason, want.reason) {
			t.Errorf("%s: verdict %s (%s), want %s with %q", typ, d.Verdict, d.Reason, want.verdict, want.reason)
		}
	}
}

// TestImportIDThenNameSet: vertex_ai_tensorboard's importer builds the id
// from a template and then sets name to that id. It is the same rule as
// [TestReadImporterShapes]'s, and its Asset name, which is that name
// whole, is decided by the template rather than by its last import form
// "^(?P<name>[^/]+)$", whose {{name}} is a short name that rejects every
// full name an Asset hit carries.
func TestImportIDThenNameSet(t *testing.T) {
	text := `
func resourceVertexAITensorboardImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config := meta.(*transport_tpg.Config)
	if err := tpgresource.ParseImportId([]string{
		"^projects/(?P<project>[^/]+)/locations/(?P<region>[^/]+)/tensorboards/(?P<name>[^/]+)$",
		"^(?P<project>[^/]+)/(?P<region>[^/]+)/(?P<name>[^/]+)$",
		"^(?P<region>[^/]+)/(?P<name>[^/]+)$",
		"^(?P<name>[^/]+)$",
	}, d, config); err != nil {
		return nil, err
	}

	// Replace import id for the resource id
	id, err := tpgresource.ReplaceVars(d, config, "projects/{{project}}/locations/{{region}}/tensorboards/{{name}}")
	if err != nil {
		return nil, fmt.Errorf("Error constructing id: %s", err)
	}
	d.SetId(id)

	if err := d.Set("name", id); err != nil {
		return nil, fmt.Errorf("Error setting name for import: %s", err)
	}

	return []*schema.ResourceData{d}, nil
}
`
	tmpl := "projects/{{project}}/locations/{{region}}/tensorboards/{{name}}"
	src := Source{Kind: "resource", Generation: "mmv1", IDFormat: "{{name}}", APIService: "aiplatform.googleapis.com"}
	readImporterShapes(&src, text)
	if src.ImportNameSet == nil || src.ImportNameSet.Attr != "name" || src.ImportNameSet.Template != tmpl || len(src.ImportNameSet.Regexes) != 4 {
		t.Fatalf("import name set %+v", src.ImportNameSet)
	}
	src.ImportFormats = src.ImportNameSet.Regexes
	src.ImportIDFormat = tmpl
	facts := map[string]TypeFacts{ProviderGoogle: {Attrs: map[string]string{"name": "C", "project": "OC", "region": "OC", "labels": "O"}}}
	page := func(projectVar string) AssetNames {
		return AssetNames{Types: map[string][]string{
			"aiplatform.googleapis.com/Tensorboard": {"//aiplatform.googleapis.com/projects/{" + projectVar + "}/locations/{LOCATION}/tensorboards/{TENSORBOARD}"},
		}}
	}
	d := decideAsset("google_vertex_ai_tensorboard", map[string]Source{ProviderGoogle: src}, facts, page("PROJECT_NUMBER"))
	if d.Verdict != AssetUnknown || !strings.Contains(d.Reason, "a project number is not the project ID") || d.Row.Host != "" {
		t.Errorf("project number page: verdict %s, row %+v (%s); want unknown for the project number", d.Verdict, d.Row, d.Reason)
	}
	d = decideAsset("google_vertex_ai_tensorboard", map[string]Source{ProviderGoogle: src}, facts, page("PROJECT_ID"))
	if d.Verdict != AssetAsIs || d.Row.Asset != tmpl || d.Row.Import.Template != tmpl {
		t.Errorf("project id page: verdict %s, row %+v (%s); want the template as is", d.Verdict, d.Row, d.Reason)
	}
}

// TestBareNameByCollection: dataproc_session_template's id format and its
// import form are both "{{name}}", so its Magic Modules default Asset name
// //dataproc.googleapis.com/{{name}} matched an import form textually and
// was taken as is. The name is the one the API gives under the Create's
// collection, and the row stands only where Google documents an Asset
// name of that shape: none under sessionTemplates, a project number under
// it, or a project ID under it.
func TestBareNameByCollection(t *testing.T) {
	text := `
func resourceDataprocSessionTemplateCreate(d *schema.ResourceData, meta interface{}) error {
	url, err := tpgresource.ReplaceVars(d, config, transport_tpg.BaseUrl(Product, config)+"projects/{{project}}/locations/{{location}}/sessionTemplates")
	if err != nil {
		return err
	}

	// Store the ID now
	id, err := tpgresource.ReplaceVars(d, config, "{{name}}")
	if err != nil {
		return fmt.Errorf("Error constructing id: %s", err)
	}
	d.SetId(id)
	return nil
}
`
	src := resourceSource(text)
	src.Generation, src.APIService, src.DocImportFormats = "mmv1", "dataproc.googleapis.com", []string{"{{name}}"}
	if src.IDFormat != "{{name}}" || src.NameCollection != "projects/{{project}}/locations/{{location}}/sessionTemplates" {
		t.Fatalf("id format %q, name collection %q", src.IDFormat, src.NameCollection)
	}
	facts := map[string]TypeFacts{ProviderGoogle: {Attrs: map[string]string{"name": "R", "project": "OC", "location": "R", "labels": "O"}}}
	page := func(formats ...string) AssetNames {
		return AssetNames{Types: map[string][]string{"dataproc.googleapis.com/Session": formats}}
	}
	other := page("//dataproc.googleapis.com/projects/{PROJECT_ID}/locations/{LOCATION}/sessions/{SESSION}")
	d := decideAsset("google_dataproc_session_template", map[string]Source{ProviderGoogle: src}, facts, other)
	if d.Verdict != AssetUnknown || d.Row.Host != "" || !strings.Contains(d.Reason, "no Asset name Google documents under dataproc.googleapis.com has that shape") {
		t.Errorf("undocumented collection: verdict %s, row %+v (%s); want unknown", d.Verdict, d.Row, d.Reason)
	}
	numbered := page("//dataproc.googleapis.com/projects/{PROJECT_NUMBER}/locations/{LOCATION}/sessionTemplates/{SESSION_TEMPLATE}")
	d = decideAsset("google_dataproc_session_template", map[string]Source{ProviderGoogle: src}, facts, numbered)
	if d.Verdict != AssetUnknown || !strings.Contains(d.Reason, "a project number is not the project ID") {
		t.Errorf("project number page: verdict %s, row %+v (%s); want unknown for the project number", d.Verdict, d.Row, d.Reason)
	}
	documented := page("//dataproc.googleapis.com/projects/{PROJECT_ID}/locations/{LOCATION}/sessionTemplates/{SESSION_TEMPLATE}")
	d = decideAsset("google_dataproc_session_template", map[string]Source{ProviderGoogle: src}, facts, documented)
	if d.Verdict != AssetAsIs || d.Row.Asset != "{{name}}" || d.Row.Import.Template != "{{name}}" {
		t.Errorf("project id page: verdict %s, row %+v (%s); want {{name}} as is", d.Verdict, d.Row, d.Reason)
	}
}
