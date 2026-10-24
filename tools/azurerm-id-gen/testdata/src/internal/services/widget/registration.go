package widget

import (
	"github.com/hashicorp/go-azure-helpers/resourcemanager/commonids"
	"github.com/hashicorp/go-azure-sdk/resource-manager/widget/2024-01-01/gizmos"
	"github.com/hashicorp/terraform-provider-azurerm/internal/sdk"
	"github.com/hashicorp/terraform-provider-azurerm/internal/services/widget/parse"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
)

type Registration struct{}

func (r Registration) SupportedResources() map[string]*pluginsdk.Resource {
	return map[string]*pluginsdk.Resource{
		"azurerm_widget":              resourceWidget(),
		"azurerm_widget_storage":      resourceWidgetStorage(),
		"azurerm_widget_link":         resourceWidgetLink(),
		"azurerm_widget_gizmo_legacy": resourceGizmoLegacy(),
		"azurerm_widget_container":    resourceWidgetContainer(),
		"azurerm_widget_record":       resourceWidgetRecord(),
		"azurerm_widget_zonerecord":   resourceWidgetZoneRecord(),
		"azurerm_widget_zoneecho":     resourceWidgetZoneEcho(),
	}
}

func (r Registration) Resources() []sdk.Resource {
	resources := []sdk.Resource{
		GizmoResource{},
	}
	return resources
}

// A hand parser, reached through an inline validating func.
func resourceWidget() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := parse.WidgetID(id)
			return err
		}),
	}
}

// A commonids reference.
func resourceWidgetStorage() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingIdentity(&commonids.StorageAccountId{}),
	}
}

// Unmappable: the parser's own result type states no segments.
func resourceWidgetLink() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := parse.LinkID(id)
			return err
		}),
	}
}

// The same gizmo id, but the schema names the parent by id: parent-id.
func resourceGizmoLegacy() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := gizmos.ParseGizmoID(id)
			return err
		}),
	}
}

// A typed resource: a go-azure-sdk resourceids type through IDValidationFunc.
type GizmoResource struct{}

func (r GizmoResource) ResourceType() string { return "azurerm_widget_gizmo" }

func (r GizmoResource) IDValidationFunc() pluginsdk.SchemaValidateFunc {
	return gizmos.ValidateGizmoID
}

// A child whose parent is stated only as the parent's id, with the
// validator that says which id (#2138).
func resourceWidgetContainer() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingIdentity(&commonids.StorageContainerId{}),
		Schema: map[string]*pluginsdk.Schema{
			"storage_account_id": {
				Type:         pluginsdk.TypeString,
				Required:     true,
				ValidateFunc: commonids.ValidateStorageAccountID,
			},
		},
	}
}

// A record set whose zone argument is named apart from its segment, and
// whose record type is a constant (#2138).
func resourceWidgetRecord() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := gizmos.ParseRecordID(id)
			return err
		}),
	}
}

// #2201 item 1: no segment name finds zone_name, and there is no identity
// schema; the Create call builds the id from arguments, the provider's
// subscription and a constant.
func resourceWidgetZoneRecord() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Create: resourceWidgetZoneRecordCreate,
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := gizmos.ParseRecordID(id)
			return err
		}),
	}
}

func resourceWidgetZoneRecordCreate(d *pluginsdk.ResourceData, meta interface{}) error {
	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	id := gizmos.NewRecordID(subscriptionId, d.Get("resource_group_name").(string), d.Get("zone_name").(string), gizmos.RecordTypeCNAME, d.Get("name").(string))
	d.SetId(id.ID())
	return nil
}

// The same Create, but the id it stores is read back from the server, so
// the constructor's arguments are not what azurerm stores.
func resourceWidgetZoneEcho() *pluginsdk.Resource {
	return &pluginsdk.Resource{
		Create: resourceWidgetZoneEchoCreate,
		Importer: pluginsdk.ImporterValidatingResourceId(func(id string) error {
			_, err := gizmos.ParseRecordID(id)
			return err
		}),
	}
}

func resourceWidgetZoneEchoCreate(d *pluginsdk.ResourceData, meta interface{}) error {
	subscriptionId := meta.(*clients.Client).Account.SubscriptionId
	id := gizmos.NewRecordID(subscriptionId, d.Get("resource_group_name").(string), d.Get("zone_name").(string), gizmos.RecordTypeCNAME, d.Get("name").(string))
	resp, err := client.Get(ctx, id)
	if err != nil {
		return err
	}
	d.SetId(*resp.Model.Id)
	return nil
}
