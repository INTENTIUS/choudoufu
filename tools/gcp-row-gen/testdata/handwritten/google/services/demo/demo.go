// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Not compiled: read as text by createread_test.go. Each resource is one
// shape a hand-written create builds its id in.
package demo

import (
	"fmt"
	"net/url"

	"example.com/provider/google/services/resourcemanager"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-provider-google/google/registry"
	"github.com/hashicorp/terraform-provider-google/google/tpgresource"
)

func init() {
	registry.Schema{Name: "google_demo_sprintf", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceSprintf()}.Register()
	registry.Schema{Name: "google_demo_helper", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceHelper()}.Register()
	registry.Schema{Name: "google_demo_method", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceMethod()}.Register()
	registry.Schema{Name: "google_demo_folder", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceFolder()}.Register()
	registry.Schema{Name: "google_demo_minted", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceMinted()}.Register()
	registry.Schema{Name: "google_demo_twice", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceTwice()}.Register()
	registry.Schema{Name: "google_demo_escaped", ProductName: "demo", Type: registry.SchemaTypeResource, Schema: ResourceEscaped()}.Register()
}

func ResourceSprintf() *schema.Resource {
	return &schema.Resource{
		Create:   createSprintf,
		Importer: &schema.ResourceImporter{State: schema.ImportStatePassthrough},
	}
}

func createSprintf(d *schema.ResourceData, meta interface{}) error {
	project, err := tpgresource.GetProject(d, config)
	if err != nil {
		return err
	}
	name := d.Get("name").(string)
	d.SetId(fmt.Sprintf("projects/%s/things/%s", project, name))
	return nil
}

func ResourceHelper() *schema.Resource {
	return &schema.Resource{
		Create:   createHelper,
		Importer: &schema.ResourceImporter{State: importHelper},
	}
}

func fullName(project, location, name string) string {
	return fmt.Sprintf("projects/%s/locations/%s/widgets/%s", project, location, name)
}

func createHelper(d *schema.ResourceData, meta interface{}) error {
	project, _ := tpgresource.GetProject(d, config)
	location, _ := tpgresource.GetLocation(d, config)
	d.SetId(fullName(project, location, d.Get("name").(string)))
	return nil
}

func importHelper(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {
	if err := tpgresource.ParseImportId([]string{"projects/(?P<project>[^/]+)/locations/(?P<location>[^/]+)/widgets/(?P<name>[^/]+)"}, d, config); err != nil {
		return nil, err
	}
	project, _ := tpgresource.GetProject(d, config)
	location, _ := tpgresource.GetLocation(d, config)
	d.SetId(fullName(project, location, d.Get("name").(string)))
	return []*schema.ResourceData{d}, nil
}

type gadgetId struct {
	Project string
	Region  string
	Name    string
}

func (g *gadgetId) Id() string {
	return fmt.Sprintf("projects/%s/regions/%s/gadgets/%s", g.Project, g.Region, g.Name)
}

func ResourceMethod() *schema.Resource {
	return &schema.Resource{Create: createMethod}
}

func createMethod(d *schema.ResourceData, meta interface{}) error {
	project, _ := tpgresource.GetProject(d, config)
	region, _ := tpgresource.GetRegion(d, config)
	id := &gadgetId{Project: project, Region: region, Name: tpgresource.GetResourceNameFromSelfLink(d.Get("name").(string))}
	d.SetId(id.Id())
	return nil
}

func ResourceFolder() *schema.Resource {
	return &schema.Resource{Create: createFolder}
}

func createFolder(d *schema.ResourceData, meta interface{}) error {
	folder := resourcemanager.ParseFolderId(d.Get("folder"))
	d.SetId(fmt.Sprintf("folders/%s/notes/%s", folder, d.Get("name")))
	return nil
}

func ResourceMinted() *schema.Resource {
	return &schema.Resource{Create: createMinted}
}

func createMinted(d *schema.ResourceData, meta interface{}) error {
	res, err := client.Things.Create(d.Get("name").(string)).Do()
	if err != nil {
		return err
	}
	d.SetId(res.Name)
	return nil
}

func ResourceTwice() *schema.Resource {
	return &schema.Resource{Create: createTwice}
}

func createTwice(d *schema.ResourceData, meta interface{}) error {
	name := d.Get("name").(string)
	if name == "" {
		name = "default"
	}
	d.SetId(name)
	return nil
}

func ResourceEscaped() *schema.Resource {
	return &schema.Resource{Create: createEscaped}
}

func createEscaped(d *schema.ResourceData, meta interface{}) error {
	d.SetId(url.QueryEscape(d.Get("network").(string)))
	return nil
}
