package gizmos

import "github.com/hashicorp/go-azure-helpers/resourcemanager/resourceids"

// GizmoId is a go-azure-sdk resourceids type nested under a widget.
type GizmoId struct {
	SubscriptionId    string
	ResourceGroupName string
	WidgetName        string
	GizmoName         string
}

func ParseGizmoID(input string) (*GizmoId, error) { return nil, nil }

func ValidateGizmoID(input interface{}, key string) (warnings []string, errors []error) {
	if _, err := ParseGizmoID(input.(string)); err != nil {
		errors = append(errors, err)
	}
	return
}

func (id GizmoId) Segments() []resourceids.Segment {
	return []resourceids.Segment{
		resourceids.StaticSegment("staticSubscriptions", "subscriptions", "subscriptions"),
		resourceids.SubscriptionIdSegment("subscriptionId", "12345678-1234-9876-4563-123456789012"),
		resourceids.StaticSegment("staticResourceGroups", "resourceGroups", "resourceGroups"),
		resourceids.ResourceGroupSegment("resourceGroupName", "example"),
		resourceids.StaticSegment("staticProviders", "providers", "providers"),
		resourceids.ResourceProviderSegment("staticMicrosoftWidget", "Microsoft.Widget", "Microsoft.Widget"),
		resourceids.StaticSegment("staticWidgets", "widgets", "widgets"),
		resourceids.UserSpecifiedSegment("widgetName", "name"),
		resourceids.StaticSegment("staticGizmos", "gizmos", "gizmos"),
		resourceids.UserSpecifiedSegment("gizmoName", "name"),
	}
}
