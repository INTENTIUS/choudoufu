// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveSummary is "choudoufu live-summary"'s output: one already-rendered
// summary on stdout, in whichever format was asked for. Same shape as
// [LiveBucket], for the same reason.
type LiveSummary struct {
	view *View
}

func NewLiveSummary(view *View) *LiveSummary {
	return &LiveSummary{view: view}
}

// Output writes the rendered summary to stdout.
func (v *LiveSummary) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
