package resourceids

type Segment struct{}

func StaticSegment(name, value, example string) Segment           { return Segment{} }
func SubscriptionIdSegment(name, example string) Segment          { return Segment{} }
func ResourceGroupSegment(name, example string) Segment           { return Segment{} }
func ResourceProviderSegment(name, value, example string) Segment { return Segment{} }
func UserSpecifiedSegment(name, example string) Segment           { return Segment{} }
