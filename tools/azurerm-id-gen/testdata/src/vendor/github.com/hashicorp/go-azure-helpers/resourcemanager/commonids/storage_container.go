package commonids

import "github.com/hashicorp/go-azure-helpers/resourcemanager/resourceids"

// StorageContainerId is a child of StorageAccountId whose resource states
// its parent only as the parent's id (#2138).
type StorageContainerId struct {
	SubscriptionId     string
	ResourceGroupName  string
	StorageAccountName string
	ContainerName      string
}

func (id StorageContainerId) Segments() []resourceids.Segment {
	return []resourceids.Segment{
		resourceids.StaticSegment("staticSubscriptions", "subscriptions", "subscriptions"),
		resourceids.SubscriptionIdSegment("subscriptionId", "12345678-1234-9876-4563-123456789012"),
		resourceids.StaticSegment("staticResourceGroups", "resourceGroups", "resourceGroups"),
		resourceids.ResourceGroupSegment("resourceGroupName", "example"),
		resourceids.StaticSegment("staticProviders", "providers", "providers"),
		resourceids.ResourceProviderSegment("staticMicrosoftStorage", "Microsoft.Storage", "Microsoft.Storage"),
		resourceids.StaticSegment("staticStorageAccounts", "storageAccounts", "storageAccounts"),
		resourceids.UserSpecifiedSegment("storageAccountName", "name"),
		resourceids.StaticSegment("staticContainers", "containers", "containers"),
		resourceids.UserSpecifiedSegment("containerName", "name"),
	}
}
