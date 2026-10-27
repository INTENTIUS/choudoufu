// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// Investigate is "choudoufu investigate"'s output: one already-rendered
// answer on stdout. Same shape as [LiveHistory].
type Investigate struct {
	view *View
}

func NewInvestigate(view *View) *Investigate {
	return &Investigate{view: view}
}

// Output writes the rendered answer to stdout.
func (v *Investigate) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
