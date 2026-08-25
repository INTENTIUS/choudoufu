// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/intentius/choudoufu/internal/live/strict"
)

// renderToggleSpan writes the `strict` block argument table into
// site/content/docs/use/reference.md's strict-toggles span.
func renderToggleSpan(root string, toggles []strict.Toggle) error {
	path := filepath.Join(root, referenceMDRel)
	doc, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		return fmt.Errorf("reading %s: %w", referenceMDRel, err)
	}

	body := renderToggleTable(toggles)

	out, err := markers.Replace(referenceMDRel, string(doc), spanStrictTable, body)
	if err != nil {
		return err
	}
	if out == string(doc) {
		return nil
	}
	return os.WriteFile(path, []byte(out), 0o644) //nolint:gosec // a committed doc
}

// renderToggleTable builds the span's body from toggles alone - no file I/O
// - so docspan_test.go's drift guard can render the same bytes
// renderToggleSpan would write without touching the filesystem.
//
// Each cell reads straight off [strict.Toggle]: Values (not this fork's
// full HCL grammar - see that field's own doc comment for where the two
// differ) for the Values column, Default for the Default column, Meaning
// for the Meaning column. A toggle with an empty Values or Meaning is a
// registry bug, not a renderable table row - see
// internal/live/strict.TestToggleValuesAreRecognizedSpellings, which is
// what would have to break for one to reach this function.
func renderToggleTable(toggles []strict.Toggle) string {
	var b strings.Builder
	b.WriteString("| Argument | Values | Default | Meaning |\n|---|---|---|---|\n")
	for _, tg := range toggles {
		quoted := make([]string, len(tg.Values))
		for i, v := range tg.Values {
			quoted[i] = fmt.Sprintf("`%q`", v)
		}
		fmt.Fprintf(&b, "| `%s` | %s | `%q` | %s |\n",
			tg.Name, strings.Join(quoted, ", "), tg.Default, tg.Meaning)
	}
	return b.String()
}
