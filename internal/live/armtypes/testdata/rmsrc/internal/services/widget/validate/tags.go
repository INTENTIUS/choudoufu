package validate

import "fmt"

// WidgetTags allows 10 tags, 64-character keys, 128-character values, and
// refuses the key "reserved". The trailing len(errors) check must not be
// read as a value length.
func WidgetTags(v any, _ string) (warnings []string, errors []error) {
	tagsMap := v.(map[string]any)
	if len(tagsMap) > 10 {
		errors = append(errors, fmt.Errorf("too many tags"))
	}
	for k, v := range tagsMap {
		if len(k) > 64 {
			errors = append(errors, fmt.Errorf("key too long"))
		}
		value := fmt.Sprint(v)
		if len(value) > 128 {
			errors = append(errors, fmt.Errorf("value too long"))
		}
	}
	if _, ok := tagsMap["reserved"]; ok {
		errors = append(errors, fmt.Errorf("reserved"))
	}
	if len(errors) > 0 {
		return warnings, errors
	}
	return warnings, errors
}
