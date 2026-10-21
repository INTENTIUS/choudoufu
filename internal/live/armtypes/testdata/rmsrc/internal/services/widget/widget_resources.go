package widget

import (
	"github.com/hashicorp/go-azure-helpers/resourcemanager/tags"
	"github.com/hashicorp/go-azure-sdk/sdk/client"
	"github.com/hashicorp/terraform-provider-azurerm/internal/clients"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/widget/validate"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
)

var _ = client.Options{}

func widgetSchema() map[string]*pluginsdk.Schema {
	return map[string]*pluginsdk.Schema{
		"name": {Type: pluginsdk.TypeString},
		"tags": {
			Type:         pluginsdk.TypeMap,
			Optional:     true,
			ValidateFunc: validate.WidgetTags,
		},
	}
}

// in-create: the PUT's payload carries the tags. The local `client`
// shadows the sdk/client import.
func resourceWidgetInline() *pluginsdk.Resource {
	return &pluginsdk.Resource{Create: resourceWidgetInlineCreate, Schema: widgetSchema()}
}

func resourceWidgetInlineCreate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	existing, _ := client.Get(ctx, d.Get("name").(string))
	_ = existing
	payload := Widget{
		Name: d.Get("name").(string),
		Tags: tags.Expand(d.Get("tags").(map[string]any)),
	}
	if _, err := client.CreateOrUpdate(ctx, d.Get("name").(string), payload); err != nil {
		return err
	}
	return nil
}

// after-create: the PUT has no tags, a PATCH of only tags follows.
func resourceWidgetAfter() *pluginsdk.Resource {
	return &pluginsdk.Resource{Create: resourceWidgetAfterCreate, Schema: widgetSchema()}
}

func resourceWidgetAfterCreate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	if _, err := client.CreateOrUpdate(ctx, d.Get("name").(string), Widget{Name: d.Get("name").(string)}); err != nil {
		return err
	}
	t := TagsPatch{Tags: tags.Expand(d.Get("tags").(map[string]any))}
	if _, err := client.UpdateTags(ctx, d.Get("name").(string), t); err != nil {
		return err
	}
	return nil
}

// in-create: a conditional, untagged restore PUT comes first, but every
// run makes the tagged PUT.
func resourceWidgetRestore() *pluginsdk.Resource {
	return &pluginsdk.Resource{Create: resourceWidgetRestoreCreate, Schema: widgetSchema()}
}

func resourceWidgetRestoreCreate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	if d.Get("restore").(bool) {
		if _, err := client.CreateOrUpdate(ctx, d.Get("name").(string), Widget{Restore: true}); err != nil {
			return err
		}
	}
	t := d.Get("tags").(map[string]any)
	payload := Widget{Name: d.Get("name").(string)}
	payload.Tags = tags.Expand(t)
	_, err := client.CreateOrUpdate(ctx, d.Get("name").(string), payload)
	return err
}

// after-create: the create returns into the update function, which
// patches the tags.
func resourceWidgetViaUpdate() *pluginsdk.Resource {
	return &pluginsdk.Resource{Create: resourceWidgetViaUpdateCreate, Schema: widgetSchema()}
}

func resourceWidgetViaUpdateCreate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	if _, err := client.CreateOrUpdate(ctx, d.Get("name").(string), Widget{Name: d.Get("name").(string)}); err != nil {
		return err
	}
	return resourceWidgetViaUpdateUpdate(d, meta)
}

func resourceWidgetViaUpdateUpdate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	var patch WidgetPatch
	setPatchTags(&patch, d)
	_, err := client.Update(ctx, d.Get("name").(string), patch)
	return err
}

func setPatchTags(p *WidgetPatch, d *pluginsdk.ResourceData) {
	p.Tags = tags.Expand(d.Get("tags").(map[string]any))
}

// unknown: the tags are read and never reach an API call.
func resourceWidgetUntraced() *pluginsdk.Resource {
	return &pluginsdk.Resource{Create: resourceWidgetUntracedCreate, Schema: widgetSchema()}
}

func resourceWidgetUntracedCreate(d *pluginsdk.ResourceData, meta any) error {
	client := meta.(*clients.Client).Widget.Widgets
	ctx := meta.(*clients.Client).StopContext
	t := d.Get("tags").(map[string]any)
	_ = t
	_, err := client.CreateOrUpdate(ctx, d.Get("name").(string), Widget{Name: d.Get("name").(string)})
	return err
}
