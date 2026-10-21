// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"reflect"
	"strings"
	"testing"
)

// locationCreateText is a generated Create in the shape hashicorp/google
// 8.6.0 writes, with its encoder (resource_artifact_registry_repository.go).
const locationCreateText = `
func resourceArtifactRegistryRepositoryCreate(d *schema.ResourceData, meta interface{}) error {
	obj, err = resourceArtifactRegistryRepositoryEncoder(d, meta, obj)
	if err != nil {
		return err
	}
	url, err := tpgresource.ReplaceVars(d, config, transport_tpg.BaseUrl(Product, config)+"projects/{{project}}/locations/{{location}}/repositories?repository_id={{repository_id}}")
	id, err := tpgresource.ReplaceVars(d, config, "projects/{{project}}/locations/{{location}}/repositories/{{repository_id}}")
	d.SetId(id)
	return nil
}

func resourceArtifactRegistryRepositoryEncoder(d *schema.ResourceData, meta interface{}, obj map[string]interface{}) (map[string]interface{}, error) {
	config := meta.(*transport_tpg.Config)
	if _, ok := d.GetOk("location"); !ok {
		location, err := tpgresource.GetRegion(d, config)
		if err != nil {
			return nil, fmt.Errorf("Cannot determine location: set in this resource, or set provider-level 'region' or 'zone'.")
		}
		if err := d.Set("location", location); err != nil {
			return nil, fmt.Errorf("Error setting location: %s", err)
		}
	}
	return obj, nil
}
`

// TestReadLocationSet reads each shape of a Create's {{location}} rule.
func TestReadLocationSet(t *testing.T) {
	const tmpl = "projects/{{project}}/locations/{{location}}/repositories/{{repository_id}}"
	read := func(text string) Source {
		s := Source{IDFormat: tmpl}
		readLocationSet(&s, text)
		return s
	}

	if s := read(locationCreateText); s.LocationSet != LocationSetRegion || s.LocationSetLine != 20 {
		t.Errorf("GetRegion encoder = %q at line %d, want %q at 20", s.LocationSet, s.LocationSetLine, LocationSetRegion)
	}

	none := strings.Replace(locationCreateText, `if err := d.Set("location", location); err != nil {`, `if err := nil; err != nil {`, 1)
	none = strings.Replace(none, `if _, ok := d.GetOk("location"); !ok {`, `if false {`, 1)
	if s := read(none); s.LocationSet != LocationSetNone {
		t.Errorf("a Create that sets no location = %q, want %q", s.LocationSet, LocationSetNone)
	}

	// The default from the zone instead: the statement is recorded, and
	// decideResource refuses it by name.
	zone := strings.Replace(locationCreateText, "tpgresource.GetRegion(d, config)", "tpgresource.GetZone(d, config)", 1)
	if s := read(zone); s.LocationSet != `d.Set("location", location)` || s.LocationSetLine != 20 {
		t.Errorf("a zone default = %q at %d, want the d.Set statement at 20", s.LocationSet, s.LocationSetLine)
	}

	extract := strings.Replace(locationCreateText, "location, err := tpgresource.GetRegion(d, config)",
		`location, err := tpgresource.ExtractFieldByPattern("location", location, bucket, "locations/([a-z0-9-]*)/buckets/.*")`, 1)
	if s := read(extract); !strings.HasPrefix(s.LocationSet, `location, err := tpgresource.ExtractFieldByPattern("location", location, bucket,`) || s.LocationSetLine != 16 {
		t.Errorf("an extracted location = %q at %d, want the ExtractFieldByPattern statement at 16", s.LocationSet, s.LocationSetLine)
	}

	// The template matched only in the importer: the Create's id is built
	// some other way, and nothing is read about its location.
	elsewhere := strings.Replace(locationCreateText, `id, err := tpgresource.ReplaceVars(d, config, "projects/{{project}}/locations/{{location}}/repositories/{{repository_id}}")`, `var id = fmt.Sprintf("x")`, 1)
	if s := read(elsewhere); s.LocationSet != LocationSetElsewhere {
		t.Errorf("a Create that builds its id elsewhere = %q, want %q", s.LocationSet, LocationSetElsewhere)
	}

	if s := (Source{IDFormat: "projects/{{project}}/topics/{{name}}"}); func() string { readLocationSet(&s, locationCreateText); return s.LocationSet }() != "" {
		t.Error("an id format with no {{location}} recorded a location rule")
	}
}

// TestDecideLocationRule derives each {{location}} shape into its row or
// its refusal.
func TestDecideLocationRule(t *testing.T) {
	facts := map[string]TypeFacts{ProviderGoogle: {Attrs: map[string]string{
		"project": "OC", "location": "OC", "repository_id": "R", "name": "C",
	}}}
	src := Source{
		Kind:           "resource",
		File:           "google/services/artifactregistry/resource_artifact_registry_repository.go",
		IDFormat:       "projects/{{project}}/locations/{{location}}/repositories/{{repository_id}}",
		ImportIDFormat: "projects/{{project}}/locations/{{location}}/repositories/{{repository_id}}",
	}
	decide := func(s Source, f map[string]TypeFacts) Decision {
		return Decide("google_artifact_registry_repository", map[string]Source{ProviderGoogle: s}, f)
	}

	region := src
	region.LocationSet, region.LocationSetLine = LocationSetRegion, 3120
	d := decide(region, facts)
	if d.Verdict != VerdictRowed {
		t.Fatalf("GetRegion default = %s (%s), want rowed", d.Verdict, d.Reason)
	}
	if c := d.Row.Components[3]; !reflect.DeepEqual(c.Attrs, []string{"location"}) || c.Cloud != "region" {
		t.Errorf("location component = %+v, want the argument with the provider's region behind it", c)
	}

	// GetRegion reads the type's own region or zone first.
	withZone := map[string]TypeFacts{ProviderGoogle: {Attrs: map[string]string{
		"project": "OC", "location": "OC", "repository_id": "R", "zone": "O",
	}}}
	if d := decide(region, withZone); d.Verdict != VerdictUnrowed || !strings.Contains(d.Reason, "resource_artifact_registry_repository.go:3120") {
		t.Errorf("GetRegion beside a zone argument = %s %q, want refused citing the encoder", d.Verdict, d.Reason)
	}

	none := src
	none.LocationSet = LocationSetNone
	if d := decide(none, facts); d.Verdict != VerdictRowed || d.Row.Components[3].Cloud != "" || d.Row.Components[3].ServerAssignedIfAbsent {
		t.Errorf("no location rule = %s %+v, want rowed from the argument alone", d.Verdict, d.Row)
	}

	other := src
	other.LocationSet, other.LocationSetLine = `d.Set("location", zone)`, 795
	if d := decide(other, facts); d.Verdict != VerdictUnrowed || !strings.Contains(d.Reason, "resource_artifact_registry_repository.go:795") || !strings.Contains(d.Reason, `d.Set("location", zone)`) {
		t.Errorf("an unread rule = %s %q, want refused naming the statement and its line", d.Verdict, d.Reason)
	}

	if d := decide(src, facts); d.Verdict != VerdictUnrowed {
		t.Errorf("no recorded rule = %s, want refused", d.Verdict)
	}
}

// TestRendersEmpty: a placeholder the schema lacks renders empty unless
// ReplaceVars answers it itself or reads it off transport_tpg.Config.
func TestRendersEmpty(t *testing.T) {
	src := Source{IDFormat: "projects/{{project}}/global/snapshotSettings/{{name}}"}
	if !rendersEmpty(src, "", "name") {
		t.Error("{{name}} with no attribute does not render empty")
	}
	if rendersEmpty(src, "R", "name") || rendersEmpty(src, "", "project") {
		t.Error("an attribute, or a placeholder ReplaceVars answers itself, renders empty")
	}
	readConfigPlaceholders(&src, []string{"AccessToken", "name"})
	if rendersEmpty(src, "", "name") {
		t.Error("{{name}} renders empty although transport_tpg.Config has a field by that name")
	}
}

// TestParseUnsetKeys reads the values keys an IdParseFunc's updater reads
// and never sets (iam_service_directory_namespace.go), and none where it
// sets them from a provider default (iam_vertex_ai_feature_online_store_featureview.go).
func TestParseUnsetKeys(t *testing.T) {
	sd := `func ServiceDirectoryNamespaceIdParseFunc(d *schema.ResourceData, config *transport_tpg.Config) error {
	values := make(map[string]string)

	m, err := tpgresource.GetImportIdQualifiers([]string{"projects/(?P<project>[^/]+)/locations/(?P<location>[^/]+)/namespaces/(?P<namespace_id>[^/]+)", "(?P<location>[^/]+)/(?P<namespace_id>[^/]+)"}, d, config, d.Id())
	for k, v := range m {
		values[k] = v
	}

	u := &ServiceDirectoryNamespaceIamUpdater{
		name:   values["name"],
		d:      d,
		Config: config,
	}
`
	if got := parseUnsetKeys(sd); !reflect.DeepEqual(got, []string{"name"}) {
		t.Errorf("service directory = %v, want [name]", got)
	}
	vx := `	values := make(map[string]string)
	region, _ := tpgresource.GetRegion(d, config)
	if region != "" {
		values["region"] = region
	}
	m, err := tpgresource.GetImportIdQualifiers([]string{"(?P<feature_online_store>[^/]+)/featureViews/(?P<feature_view>[^/]+)"}, d, config, d.Id())
	u := &VertexIamUpdater{
		region:             values["region"],
		featureOnlineStore: values["feature_online_store"],
		featureView:        values["feature_view"],
	}
`
	if got := parseUnsetKeys(vx); len(got) != 0 {
		t.Errorf("vertex featureview = %v, want none", got)
	}
}
