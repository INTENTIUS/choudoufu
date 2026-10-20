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
