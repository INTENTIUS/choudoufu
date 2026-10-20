// A miniature azurerm service for internal/live/armtypes's always-on test
// of the create-path analysis: one resource per class the analysis must
// tell apart. It is parsed, never built.
package widget

import (
	"github.com/hashicorp/terraform-provider-azurerm/internal/sdk"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
)

type Registration struct{}

func (r Registration) SupportedResources() map[string]*pluginsdk.Resource {
	return map[string]*pluginsdk.Resource{
		"azurerm_widget_inline":    resourceWidgetInline(),
		"azurerm_widget_after":     resourceWidgetAfter(),
		"azurerm_widget_restore":   resourceWidgetRestore(),
		"azurerm_widget_viaupdate": resourceWidgetViaUpdate(),
		"azurerm_widget_untraced":  resourceWidgetUntraced(),
	}
}

func (r Registration) Resources() []sdk.Resource {
	return []sdk.Resource{
		GizmoResource{},
	}
}
