package widget

import (
	"context"

	"github.com/hashicorp/go-azure-helpers/resourcemanager/commonschema"
	"github.com/hashicorp/terraform-provider-azurerm/internal/sdk"
	"github.com/hashicorp/terraform-provider-azurerm/internal/tf/pluginsdk"
)

// GizmoResource is a typed resource: in-create through a decoded model
// and a mapping method on the resource's own receiver.
type GizmoResource struct{}

type GizmoModel struct {
	Name   string            `tfschema:"name"`
	Labels map[string]string `tfschema:"tags"`
}

func (r GizmoResource) ResourceType() string { return "azurerm_gizmo" }

func (r GizmoResource) Arguments() map[string]*pluginsdk.Schema {
	return map[string]*pluginsdk.Schema{
		"name": {Type: pluginsdk.TypeString},
		"tags": commonschema.TagsWithMaximumElements(7),
	}
}

func (r GizmoResource) Create() sdk.ResourceFunc {
	return sdk.ResourceFunc{
		Func: func(ctx context.Context, metadata sdk.ResourceMetaData) error {
			client := metadata.Client.Widget.Gizmos
			var model GizmoModel
			if err := metadata.Decode(&model); err != nil {
				return err
			}
			var payload Gizmo
			r.mapModel(model, &payload)
			return client.CreateOrUpdateThenPoll(ctx, model.Name, payload)
		},
	}
}

func (r GizmoResource) mapModel(input GizmoModel, output *Gizmo) {
	output.Name = input.Name
	output.Tags = input.Labels
}
