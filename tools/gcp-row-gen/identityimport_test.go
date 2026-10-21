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

// TestResourceIdentityImport reads each importer shape at hashicorp/google
// 8.6.0 from a trimmed copy of its code (INTENTIUS/choudoufu#2145 item 4).
func TestResourceIdentityImport(t *testing.T) {
	const decl = "\tIdentity: &schema.ResourceIdentity{\n\t},\n\tImporter: &schema.ResourceImporter{\n\t\tState: imp,\n\t},\n"
	for _, tc := range []struct {
		name, body string
		want       *IdentityImport
	}{
		{
			// resource_scc_v2_folder_mute_config.go
			name: "parse_import_id",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config := meta.(*transport_tpg.Config)
	if err := tpgresource.ParseImportId([]string{
		"^projects/(?P<project>[^/]+)/folders/(?P<folder>[^/]+)/muteConfigs/(?P<mute_config_id>[^/]+)$",
		"^(?P<mute_config_id>[^/]+)$",
	}, d, config); err != nil {
		return nil, err
	}
	id, err := tpgresource.ReplaceVars(d, config, "projects/{{project}}/folders/{{folder}}/muteConfigs/{{mute_config_id}}")
	if err != nil {
		return nil, fmt.Errorf("Error constructing id for %s: %s", d.Get("name"), err)
	}
	d.SetId(id)
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Via: "parse_import_id", Required: []string{"folder", "mute_config_id"}, Defaulted: []string{"project"}},
		},
		{
			// resource_apigee_endpoint_attachment.go: parses name apart.
			name: "reads an argument the first regex does not set",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	config := meta.(*transport_tpg.Config)
	if err := tpgresource.ParseImportId([]string{"(?P<name>.+)"}, d, config); err != nil {
		return nil, err
	}
	nameParts := strings.Split(d.Get("org_id").(string), "/")
	id, err := tpgresource.ReplaceVars(d, config, "{{name}}")
	d.SetId(id)
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Unread: `after tpgresource.ParseImportId the importer reads d.Get("org_id"), which its first regex does not set`},
		},
		{
			name: "id format names a placeholder the first regex does not set",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	if err := tpgresource.ParseImportId([]string{"(?P<name>.+)"}, d, config); err != nil {
		return nil, err
	}
	id, err := tpgresource.ReplaceVars(d, config, "{{parent}}/templates/{{name}}")
	d.SetId(id)
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Unread: "the importer's id format {{parent}}/templates/{{name}} names {{parent}}, which its first regex does not set"},
		},
		{
			name: "first regex matches an empty ID",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	if err := tpgresource.ParseImportId([]string{"^(?P<name>[^/]*)$"}, d, config); err != nil {
		return nil, err
	}
	id, err := tpgresource.ReplaceVars(d, config, "{{name}}")
	d.SetId(id)
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Unread: "the importer's first regex matches an empty ID, so ParseImportId never reaches its identity branch"},
		},
		{
			// resource_sql_user.go
			name: "importer",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	if d.Id() == "" {
		identity, err := d.Identity()
		if v, ok := identity.GetOk("instance"); ok {
			instanceStr = v.(string)
		}
		serviceV, ok := identity.GetOk("name")
		if !ok {
			return nil, fmt.Errorf("import via identity requires identity attribute %q", "name")
		}
		return []*schema.ResourceData{d}, nil
	}
	parts := strings.Split(d.Id(), "/")
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Via: "importer", Required: []string{"name"}, Optional: []string{"instance"}},
		},
		{
			name: "custom importer with no identity branch",
			body: `func imp(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	parts := strings.Split(d.Id(), "/")
	return []*schema.ResourceData{d}, nil
}
`,
			want: &IdentityImport{Unread: "the importer imp reads no identity and calls no tpgresource.ParseImportId"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resourceIdentityImport(decl+"\n"+tc.body, nil); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}

	// resource_compute_backend_bucket_signed_url_key.go registers no importer.
	if got := resourceIdentityImport("\tIdentity: &schema.ResourceIdentity{\n\t},\n", nil); got == nil || got.Via != "" || got.Unread != "the resource registers no importer" {
		t.Errorf("no importer: got %+v", got)
	}
	// A type with no identity schema has no evidence at all.
	if got := resourceIdentityImport(strings.TrimPrefix(decl, "\tIdentity: &schema.ResourceIdentity{\n\t},\n"), nil); got != nil {
		t.Errorf("no identity schema: got %+v", got)
	}
}

// TestIAMIdentityImport reads an IAM type's parent parser and its kind's
// framework reads.
func TestIAMIdentityImport(t *testing.T) {
	kinds := map[string]IdentityImport{
		"iam_member": {Via: "iam_parent", Required: []string{"role", "member"}, Optional: []string{"condition_title"}},
	}
	// iam_vertex_ai_feature_online_store_featureview.go
	text := `		tpgiamresource.IamWithParentResourceIdentity(FeatureviewParser),
func FeatureviewParser(d *schema.ResourceData, identity *schema.IdentityData, transportConfig *transport_tpg.Config) (string, error) {
	return tpgiamresource.ParseIamResourceIdentity(d, identity, transportConfig, tpgiamresource.IamResourceIdentityConfig{
		Params: []tpgiamresource.IamIdentityParam{
			{Key: "project", IdentityKey: "project"},
			{Key: "featureView", IdentityKey: "feature_view"},
		},
		UriFormat: "projects/%s/featureViews/%s",
	})
}
`
	want := &IdentityImport{Via: "iam_parent", Required: []string{"feature_view", "role", "member"}, Defaulted: []string{"project"}, Optional: []string{"condition_title"}}
	if got := iamIdentityImport(text, "iam_member", nil, kinds); !reflect.DeepEqual(got, want) {
		t.Errorf("member: got %+v, want %+v", got, want)
	}
	if got := iamIdentityImport(text, "iam_audit_config", nil, kinds); got == nil || got.Via != "" {
		t.Errorf("audit config: got %+v; want a refusal, its framework importer reads no identity", got)
	}
	if got := iamIdentityImport("no parser here", "iam_member", nil, kinds); got != nil {
		t.Errorf("no parser: got %+v", got)
	}
}
