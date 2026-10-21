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

// The snippets below are cut from the v8.6.0 files they name, trimmed to
// what the readers match.

// A generated updater with camelCase qualifier groups
// (iam_iap_app_engine_version.go), including the trailing group the
// struct never reads.
const iapVersionText = `
func IapAppEngineVersionIamUpdaterProducer(d tpgresource.TerraformResourceData, config *transport_tpg.Config) (tpgiamresource.ResourceIamUpdater, error) {
	values := make(map[string]string)

	project, _ := tpgresource.GetProject(d, config)
	values["project"] = project
	if v, ok := d.GetOk("app_id"); ok {
		values["appId"] = v.(string)
	}

	if v, ok := d.GetOk("service"); ok {
		values["service"] = v.(string)
	}

	if v, ok := d.GetOk("version_id"); ok {
		values["versionId"] = v.(string)
	}

	m, err := tpgresource.GetImportIdQualifiers([]string{"projects/(?P<project>[^/]+)/iap_web/appengine-(?P<appId>[^/]+)/services/(?P<service>[^/]+)/versions/(?P<versionId>[^/]+)", "(?P<version>[^/]+)"}, d, config, d.Get("version_id").(string))

	u := &IapAppEngineVersionIamUpdater{
		project:   values["project"],
		appId:     values["appId"],
		service:   values["service"],
		versionId: values["versionId"],
		d:         d,
		Config:    config,
	}
	return u, nil
}

func IapAppEngineVersionIdParseFunc(d *schema.ResourceData, config *transport_tpg.Config) error {
	m, err := tpgresource.GetImportIdQualifiers([]string{"projects/(?P<project>[^/]+)/iap_web/appengine-(?P<appId>[^/]+)/services/(?P<service>[^/]+)/versions/(?P<versionId>[^/]+)", "(?P<version>[^/]+)"}, d, config, d.Id())
	d.SetId(u.GetResourceId())
	return nil
}

func (u *IapAppEngineVersionIamUpdater) GetResourceId() string {
	return fmt.Sprintf("projects/%s/iap_web/appengine-%s/services/%s/versions/%s", u.project, u.appId, u.service, u.versionId)
}
`

// A hand-written updater over a parse helper whose id struct's method is
// the id (iam_spanner_instance.go).
const spannerInstanceText = `
func NewSpannerInstanceIamUpdater(d tpgresource.TerraformResourceData, config *transport_tpg.Config) (tpgiamresource.ResourceIamUpdater, error) {
	project, err := tpgresource.GetProject(d, config)
	if err != nil {
		return nil, err
	}

	return &SpannerInstanceIamUpdater{
		project:  project,
		instance: d.Get("instance").(string),
		d:        d,
		Config:   config,
	}, nil
}

func SpannerInstanceIdParseFunc(d *schema.ResourceData, config *transport_tpg.Config) error {
	id, err := extractSpannerInstanceId(d.Id())
	if err != nil {
		return err
	}
	if err := d.Set("instance", id.Instance); err != nil {
		return fmt.Errorf("Error setting instance: %s", err)
	}
	if err := d.Set("project", id.Project); err != nil {
		return fmt.Errorf("Error setting project: %s", err)
	}
	d.SetId(id.TerraformId())
	return nil
}

func (u *SpannerInstanceIamUpdater) GetResourceId() string {
	return SpannerInstanceId{
		Project:  u.project,
		Instance: u.instance,
	}.TerraformId()
}

func (s SpannerInstanceId) TerraformId() string {
	return fmt.Sprintf("%s/%s", s.Project, s.Instance)
}

func extractSpannerInstanceId(id string) (*SpannerInstanceId, error) {
	return nil, nil
}
`

// A hand-written updater over tpgresource.ParseRegionalFieldValue
// (iam_dataproc_cluster.go), and the tpgresource text it reads.
const dataprocClusterText = `
func NewDataprocClusterUpdater(d tpgresource.TerraformResourceData, config *transport_tpg.Config) (tpgiamresource.ResourceIamUpdater, error) {
	project, err := tpgresource.GetProject(d, config)
	region, err := tpgresource.GetRegion(d, config)
	return &DataprocClusterIamUpdater{
		project: project,
		region:  region,
		cluster: d.Get("cluster").(string),
		d:       d,
		Config:  config,
	}, nil
}

func DataprocClusterIdParseFunc(d *schema.ResourceData, config *transport_tpg.Config) error {
	fv, err := tpgresource.ParseRegionalFieldValue("clusters", d.Id(), "project", "region", "zone", d, config, true)
	if err := d.Set("project", fv.Project); err != nil {
		return fmt.Errorf("Error setting project: %s", err)
	}
	if err := d.Set("region", fv.Region); err != nil {
		return fmt.Errorf("Error setting region: %s", err)
	}
	if err := d.Set("cluster", fv.Name); err != nil {
		return fmt.Errorf("Error setting cluster: %s", err)
	}
	d.SetId(fv.RelativeLink())
	return nil
}

func (u *DataprocClusterIamUpdater) GetResourceId() string {
	return fmt.Sprintf("projects/%s/regions/%s/clusters/%s", u.project, u.region, u.cluster)
}
`

const tpgresourceFieldHelpers = `
const (
	RegionalLinkTemplate           = "projects/%s/regions/%s/%s/%s"
)

func (f RegionalFieldValue) RelativeLink() string {
	if len(f.Name) == 0 {
		return ""
	}

	return fmt.Sprintf(RegionalLinkTemplate, f.Project, f.Region, f.resourceType, f.Name)
}

func ParseRegionalFieldValue(resourceType, fieldValue, projectSchemaField, regionSchemaField, zoneSchemaField string, d TerraformResourceData, config *transport_tpg.Config, isEmptyValid bool) (*RegionalFieldValue, error) {
	return nil, nil
}
`

// A hand-written updater that canonicalises its argument through a parse
// helper (iam_kms_key_ring.go).
const kmsKeyRingText = `
func NewKmsKeyRingIamUpdater(d tpgresource.TerraformResourceData, config *transport_tpg.Config) (tpgiamresource.ResourceIamUpdater, error) {
	keyRing := d.Get("key_ring_id").(string)
	keyRingId, err := parseKmsKeyRingId(keyRing, config)

	return &KmsKeyRingIamUpdater{
		resourceId: keyRingId.KeyRingId(),
		d:          d,
		Config:     config,
	}, nil
}

func KeyRingIdParseFunc(d *schema.ResourceData, config *transport_tpg.Config) error {
	keyRingId, err := parseKmsKeyRingId(d.Id(), config)
	if err := d.Set("key_ring_id", keyRingId.KeyRingId()); err != nil {
		return fmt.Errorf("Error setting key_ring_id: %s", err)
	}
	d.SetId(keyRingId.KeyRingId())
	return nil
}

func (u *KmsKeyRingIamUpdater) GetResourceId() string {
	return u.resourceId
}
`

func TestIAMSourceParseShapes(t *testing.T) {
	text := func(s string) func() string { return func() string { return s } }

	iap := iamSource("google_iap_app_engine_version_iam_member", iapVersionText, nil, text(iapVersionText), text(""))
	tmpl := "projects/{{project}}/iap_web/appengine-{{app_id}}/services/{{service}}/versions/{{version_id}}"
	if iap.IAMParent != tmpl || iap.IAMResourceID != tmpl {
		t.Errorf("iap parent %q, resource id %q, want both %q", iap.IAMParent, iap.IAMResourceID, tmpl)
	}
	wantQ := []string{
		"projects/(?P<project>[^/]+)/iap_web/appengine-(?P<app_id>[^/]+)/services/(?P<service>[^/]+)/versions/(?P<version_id>[^/]+)",
		"([^/]+)",
	}
	if iap.IAMQualified != "version_id" || !reflect.DeepEqual(iap.IAMQualifiers, wantQ) {
		t.Errorf("iap qualified %q %q, want version_id %q", iap.IAMQualified, iap.IAMQualifiers, wantQ)
	}

	sp := iamSource("google_spanner_instance_iam_member", spannerInstanceText, nil, text(spannerInstanceText), text(""))
	if sp.IAMParent != "{{project}}/{{instance}}" || sp.IAMResourceID != "{{project}}/{{instance}}" {
		t.Errorf("spanner parent %q, resource id %q", sp.IAMParent, sp.IAMResourceID)
	}

	dp := iamSource("google_dataproc_cluster_iam_member", dataprocClusterText, nil, text(dataprocClusterText), text(tpgresourceFieldHelpers))
	tmpl = "projects/{{project}}/regions/{{region}}/clusters/{{cluster}}"
	if dp.IAMParent != tmpl || dp.IAMResourceID != tmpl {
		t.Errorf("dataproc parent %q, resource id %q, want both %q", dp.IAMParent, dp.IAMResourceID, tmpl)
	}

	kms := iamSource("google_kms_key_ring_iam_member", kmsKeyRingText, nil, text(kmsKeyRingText), text(""))
	if kms.IAMParent != "" || kms.IAMResourceID != "" || kms.IAMResourceIDUnread != "u.resourceId is set from keyRingId.KeyRingId()" {
		t.Errorf("kms parent %q, resource id %q, unread %q", kms.IAMParent, kms.IAMResourceID, kms.IAMResourceIDUnread)
	}
	kms.Kind, kms.IAMGrammar = "iam_member", "resource_name role member"
	tf := TypeFacts{Attrs: map[string]string{"key_ring_id": "R", "role": "R", "member": "R"}}
	d := Decide("google_kms_key_ring_iam_member", map[string]Source{ProviderGoogle: kms}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictUnrowed || !strings.Contains(d.Reason, "keyRingId.KeyRingId()") {
		t.Errorf("kms = %s (%s), want unrowed naming the helper", d.Verdict, d.Reason)
	}
}

// TestDecideIAMResourceIDNotInParent: a GetResourceId reading an argument
// the IdParseFunc's parent does not carry is refused by name, not rowed
// over the import string. Two shapes at v8.6.0: service_directory's
// updater reads values["name"], which no qualifier group captures, and
// vertex_ai featureview's reads project and region, which its qualifiers
// leave to the provider's defaults.
func TestDecideIAMResourceIDNotInParent(t *testing.T) {
	tf := TypeFacts{Attrs: map[string]string{"feature_online_store": "R", "feature_view": "R", "project": "OC", "region": "OC", "role": "R", "member": "R"}}
	src := Source{
		Kind:          "iam_member",
		IAMGrammar:    "resource_name role member",
		IAMParent:     "{{feature_online_store}}/featureViews/{{feature_view}}",
		IAMResourceID: "projects/{{project}}/locations/{{region}}/featureOnlineStores/{{feature_online_store}}/featureViews/{{feature_view}}",
		IAMDefaults:   []string{"project", "region"},
	}
	d := Decide("google_vertex_ai_feature_online_store_featureview_iam_member", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf})
	if d.Verdict != VerdictUnrowed || !strings.Contains(d.Reason, "{{project}} and {{region}}") {
		t.Errorf("featureview = %s (%s), want unrowed naming project and region", d.Verdict, d.Reason)
	}

	// The same parent with a URL prefix GetResourceId adds and the
	// importer's unanchored regex skips is still the same parent.
	src.IAMParent = "projects/{{project}}/locations/{{region}}/featureOnlineStores/{{feature_online_store}}/featureViews/{{feature_view}}"
	src.IAMResourceID = "v1/" + src.IAMParent
	if d := Decide("google_x_iam_member", map[string]Source{ProviderGoogle: src}, map[string]TypeFacts{ProviderGoogle: tf}); d.Verdict != VerdictRowed {
		t.Errorf("a prefixed resource id = %s (%s), want rowed", d.Verdict, d.Reason)
	}
}
