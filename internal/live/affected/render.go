// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package affected

import (
	"fmt"
	"strings"
)

// short is a commit hash cut to the length a reader compares by eye.
func short(sha string) string {
	if len(sha) > 10 {
		return sha[:10]
	}
	return sha
}

// Text is the result as live-affected prints it without -json: one line per
// affected root with every reason, then what made the answer indeterminate,
// then the changed paths that name nothing.
func (r *Result) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Range %s..%s: ", short(r.Range.Base), short(r.Range.Head))
	switch {
	case r.Outcome == Indeterminate:
		fmt.Fprintf(&b, "indeterminate. Plan every root (%d); %d of them are attributed below.\n", r.RootsTotal, len(r.Roots))
	case len(r.Roots) == 0:
		fmt.Fprintf(&b, "no estate root of %d is affected.\n", r.RootsTotal)
	default:
		fmt.Fprintf(&b, "%d of %d estate roots affected.\n", len(r.Roots), r.RootsTotal)
	}

	if len(r.Roots) > 0 {
		b.WriteString("\n")
		for _, root := range r.Roots {
			texts := make([]string, 0, len(root.Reasons))
			for _, w := range root.Reasons {
				texts = append(texts, w.Text)
			}
			label := root.Dir
			if root.Estate != "" {
				label += " (" + root.Estate + ")"
			}
			if root.Removed {
				label += " [removed]"
			}
			fmt.Fprintf(&b, "  %s: %s\n", label, strings.Join(texts, "; "))
		}
	}
	if len(r.Indeterminate) > 0 {
		b.WriteString("\nIndeterminate:\n")
		for _, i := range r.Indeterminate {
			fmt.Fprintf(&b, "  %s: %s\n", i.Kind, i.Text)
		}
	}
	if len(r.Unplaced) > 0 {
		b.WriteString("\nNaming no root:\n")
		for _, u := range r.Unplaced {
			fmt.Fprintf(&b, "  %s: %s\n", u.Path, u.Text)
		}
	}
	return b.String()
}
