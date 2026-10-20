package parse

import "fmt"

// WidgetId is a hand parser: its ID() format string is the template.
type WidgetId struct {
	SubscriptionId string
	ResourceGroup  string
	Name           string
}

func (id WidgetId) ID() string {
	fmtString := "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Widget/widgets/%s"
	return fmt.Sprintf(fmtString, id.SubscriptionId, id.ResourceGroup, id.Name)
}

func WidgetID(input string) (*WidgetId, error) { return nil, nil }

// LinkId is a hand parser whose type has no Segments() or ID(): a parser
// with its own grammar, which must be reported, not walked past.
type LinkId struct {
	Left  WidgetId
	Right WidgetId
}

func LinkID(input string) (*LinkId, error) {
	_, err := WidgetID(input)
	return nil, err
}
