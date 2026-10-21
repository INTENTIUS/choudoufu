// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// The two places a generated Create sends the new object's id, as
// hashicorp/google 8.6.0 writes them (#2145 item 3): a query parameter on
// the create URL (dialogflow_generator) and a field the encoder adds to
// the request body (data_loss_prevention_inspect_template).
const createQueryText = `
func resourceDialogflowGeneratorCreate(d *schema.ResourceData, meta interface{}) error {
	url, err := tpgresource.ReplaceVars(d, config, transport_tpg.BaseUrl(Product, config)+"projects/{{project}}/locations/{{location}}/generators?generatorId={{generator_id}}")
	if err != nil {
		return err
	}
	id, err := tpgresource.ReplaceVars(d, config, "{{name}}")
	return nil
}

func flattenDialogflowGeneratorName(v interface{}, d *schema.ResourceData, config *transport_tpg.Config) interface{} {
	return v
}
`

const createEncoderText = `
func resourceDataLossPreventionInspectTemplateCreate(d *schema.ResourceData, meta interface{}) error {
	obj, err = resourceDataLossPreventionInspectTemplateEncoder(d, meta, obj)
	if err != nil {
		return err
	}

	url, err := tpgresource.ReplaceVars(d, config, transport_tpg.BaseUrl(Product, config)+"{{parent}}/inspectTemplates")
	id, err := tpgresource.ReplaceVars(d, config, "{{parent}}/inspectTemplates/{{name}}")
	return nil
}

func flattenDataLossPreventionInspectTemplateName(v interface{}, d *schema.ResourceData, config *transport_tpg.Config) interface{} {
	if v == nil {
		return v
	}
	return tpgresource.GetResourceNameFromSelfLink(v.(string))
}

func resourceDataLossPreventionInspectTemplateEncoder(d *schema.ResourceData, meta interface{}, obj map[string]interface{}) (map[string]interface{}, error) {
	newObj := make(map[string]interface{})
	newObj["inspectTemplate"] = obj
	templateIdProp, ok := d.GetOk("template_id")
	if ok && templateIdProp != nil {
		newObj["templateId"] = templateIdProp
	}
	return newObj, nil
}
`

func TestReadCreateName(t *testing.T) {
	q := Source{IDFormat: "{{name}}"}
	readCreateName(&q, createQueryText)
	if q.CreateCollection != "projects/{{project}}/locations/{{location}}/generators" || q.CreateNameParam != "generatorId" || q.CreateNameArg != "generator_id" || q.NameFlatten["name"] != NameWhole {
		t.Errorf("query parameter read as %q %q %q %v", q.CreateCollection, q.CreateNameParam, q.CreateNameArg, q.NameFlatten)
	}
	e := Source{IDFormat: "{{parent}}/inspectTemplates/{{name}}"}
	readCreateName(&e, createEncoderText)
	if e.CreateCollection != "{{parent}}/inspectTemplates" || e.CreateNameParam != "templateId" || e.CreateNameArg != "template_id" || e.NameFlatten["name"] != NameLastSegment {
		t.Errorf("encoder field read as %q %q %q %v", e.CreateCollection, e.CreateNameParam, e.CreateNameArg, e.NameFlatten)
	}

	// A parent's id on the create URL names no object: compute's
	// firewallPolicies?parentId={{parent}}.
	p := Source{IDFormat: "locations/global/firewallPolicies/{{name}}"}
	readCreateName(&p, strings.ReplaceAll(createQueryText, "projects/{{project}}/locations/{{location}}/generators?generatorId={{generator_id}}", "locations/global/firewallPolicies?parentId={{parent}}"))
	if p.CreateNameArg != "" {
		t.Errorf("parentId read as naming the object: %q", p.CreateNameArg)
	}
	// An id argument the id format already names leaves nothing to read.
	r := Source{IDFormat: "projects/{{project}}/locations/{{location}}/generators/{{generator_id}}"}
	readCreateName(&r, createQueryText)
	if r.CreateNameArg != "" {
		t.Errorf("an id format naming the argument still recorded %q", r.CreateNameArg)
	}
	// monitoring's base URL stops at the host, so its create path carries
	// the API version, which is no part of the name.
	m := Source{IDFormat: "{{name}}"}
	readCreateName(&m, strings.ReplaceAll(createQueryText, "projects/{{project}}/locations/{{location}}/generators?generatorId={{generator_id}}", "v3/projects/{{project}}/services?serviceId={{service_id}}"))
	if m.CreateCollection != "projects/{{project}}/services" || m.CreateNameArg != "service_id" {
		t.Errorf("versioned create path read as %q %q", m.CreateCollection, m.CreateNameArg)
	}
}

func TestNamesResource(t *testing.T) {
	for _, c := range []struct {
		param, resource string
		want            bool
	}{
		{"generatorId", "generator", true},
		{"templateId", "inspectTemplate", true},
		{"appVersionId", "version", true},
		{"configId", "notificationConfig", true},
		{"serviceLevelObjectiveId", "serviceLevelObjective", true},
		{"parentId", "firewallPolicy", false},
		{"templateId", "attemplate", false},
		{"Id", "thing", false},
	} {
		if got := namesResource(c.param, c.resource); got != c.want {
			t.Errorf("namesResource(%q, %q) = %v, want %v", c.param, c.resource, got, c.want)
		}
	}
}

// TestDecideCreateNamed: a minted name the create fixes from an argument is
// a row whose component is that argument, server-assigned when absent. A
// name the API writes back whole is kept only where Google documents the
// service's project as an ID.
func TestDecideCreateNamed(t *testing.T) {
	// The last segment: the id's path is configuration's own.
	tf := TypeFacts{Attrs: map[string]string{"id": "OC", "parent": "R", "name": "C", "template_id": "OC"}}
	src := Source{Kind: "resource", IDFormat: "{{parent}}/inspectTemplates/{{name}}", ImportIDFormat: "{{parent}}/inspectTemplates/{{name}}"}
	readCreateName(&src, createEncoderText)
	d := Decide("google_tmpl", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictRowed || d.Row.ImportSyntax != "{{parent}}/inspectTemplates/{{template_id}}" {
		t.Fatalf("verdict %s %q (%s), want a row over template_id", d.Verdict, d.Template, d.Reason)
	}
	last := d.Row.Components[len(d.Row.Components)-1]
	if last.Attrs[0] != "template_id" || !last.ServerAssignedIfAbsent || last.IdentityAttr != "name" {
		t.Errorf("template_id component %#v, want server-assigned when absent, supplying name", last)
	}

	// The whole name, written back by the API.
	tf = TypeFacts{Attrs: map[string]string{"id": "OC", "project": "OC", "location": "R", "name": "C", "generator_id": "O"}}
	whole := Source{Kind: "resource", IDFormat: "{{name}}", ImportWhole: "name", APIService: "dialogflow.googleapis.com"}
	readCreateName(&whole, createQueryText)
	id := AssetNames{Types: map[string][]string{"dialogflow.googleapis.com/Agent": {"//dialogflow.googleapis.com/projects/{PROJECT_ID}/locations/{LOCATION}/agents/{AGENT}"}}}
	d = DecideWith("google_gen", map[string]Source{ProviderGoogle: whole}, map[string]TypeFacts{ProviderGoogle: tf}, id)
	if d.Verdict != VerdictRowed || d.Row.ImportSyntax != "projects/{{project}}/locations/{{location}}/generators/{{generator_id}}" {
		t.Fatalf("verdict %s %q (%s), want the collection path and generator_id", d.Verdict, d.Template, d.Reason)
	}
	number := AssetNames{Types: map[string][]string{"dialogflow.googleapis.com/Generator": {"//dialogflow.googleapis.com/projects/{PROJECT_NUMBER}/locations/{LOCATION}/generators/{GENERATOR}"}}}
	d = DecideWith("google_gen", map[string]Source{ProviderGoogle: whole}, map[string]TypeFacts{ProviderGoogle: tf}, number)
	if d.Verdict != VerdictMarkerless || !strings.Contains(d.Reason, "PROJECT_NUMBER") {
		t.Errorf("verdict %s (%s), want markerless: the API spells the project as a number", d.Verdict, d.Reason)
	}
	d = Decide("google_gen", map[string]Source{ProviderGoogle: whole}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictMarkerless {
		t.Errorf("verdict %s with no Asset names page, want markerless", d.Verdict)
	}
	// An importer that does not take the whole name leaves it minted.
	noImport := whole
	noImport.ImportWhole = ""
	d = DecideWith("google_gen", map[string]Source{ProviderGoogle: noImport}, map[string]TypeFacts{ProviderGoogle: tf}, id)
	if d.Verdict != VerdictMarkerless || !strings.Contains(d.Reason, "importer") {
		t.Errorf("verdict %s (%s), want markerless for the importer", d.Verdict, d.Reason)
	}
}
