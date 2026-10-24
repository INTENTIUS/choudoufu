package gizmos

import "github.com/hashicorp/go-azure-helpers/resourcemanager/resourceids"

// RecordId has a constant segment, the record type, which no argument
// holds (#2138).
type RecordId struct {
	SubscriptionId        string
	ResourceGroupName     string
	DnsZoneName           string
	RecordType            string
	RelativeRecordSetName string
}

func ParseRecordID(input string) (*RecordId, error) { return nil, nil }

func (id RecordId) Segments() []resourceids.Segment {
	return []resourceids.Segment{
		resourceids.StaticSegment("staticSubscriptions", "subscriptions", "subscriptions"),
		resourceids.SubscriptionIdSegment("subscriptionId", "12345678-1234-9876-4563-123456789012"),
		resourceids.StaticSegment("staticResourceGroups", "resourceGroups", "resourceGroups"),
		resourceids.ResourceGroupSegment("resourceGroupName", "example"),
		resourceids.StaticSegment("staticProviders", "providers", "providers"),
		resourceids.ResourceProviderSegment("staticMicrosoftWidget", "Microsoft.Widget", "Microsoft.Widget"),
		resourceids.StaticSegment("staticZones", "zones", "zones"),
		resourceids.UserSpecifiedSegment("dnsZoneName", "name"),
		resourceids.ConstantSegment("recordType", PossibleValuesForRecordType(), "A"),
		resourceids.UserSpecifiedSegment("relativeRecordSetName", "name"),
	}
}

func NewRecordID(subscriptionId string, resourceGroupName string, dnsZoneName string, recordType RecordType, relativeRecordSetName string) RecordId {
	return RecordId{
		SubscriptionId:        subscriptionId,
		ResourceGroupName:     resourceGroupName,
		DnsZoneName:           dnsZoneName,
		RecordType:            string(recordType),
		RelativeRecordSetName: relativeRecordSetName,
	}
}
