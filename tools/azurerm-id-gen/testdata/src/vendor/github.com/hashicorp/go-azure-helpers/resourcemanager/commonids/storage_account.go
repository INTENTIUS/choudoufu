package commonids

import "github.com/hashicorp/go-azure-helpers/resourcemanager/resourceids"

// StorageAccountId is a commonids type: shared across services, reached
// through ParseStorageAccountID.
type StorageAccountId struct {
	SubscriptionId     string
	ResourceGroupName  string
	StorageAccountName string
}

func ParseStorageAccountID(input string) (*StorageAccountId, error) { return nil, nil }

func ValidateStorageAccountID(input interface{}, key string) (warnings []string, errors []error) {
	_, err := ParseStorageAccountID(input.(string))
	return nil, []error{err}
}

func (id StorageAccountId) Segments() []resourceids.Segment {
	return []resourceids.Segment{
		resourceids.StaticSegment("staticSubscriptions", "subscriptions", "subscriptions"),
		resourceids.SubscriptionIdSegment("subscriptionId", "12345678-1234-9876-4563-123456789012"),
		resourceids.StaticSegment("staticResourceGroups", "resourceGroups", "resourceGroups"),
		resourceids.ResourceGroupSegment("resourceGroupName", "example"),
		resourceids.StaticSegment("staticProviders", "providers", "providers"),
		resourceids.ResourceProviderSegment("staticMicrosoftStorage", "Microsoft.Storage", "Microsoft.Storage"),
		resourceids.StaticSegment("staticStorageAccounts", "storageAccounts", "storageAccounts"),
		resourceids.UserSpecifiedSegment("storageAccountName", "name"),
	}
}
