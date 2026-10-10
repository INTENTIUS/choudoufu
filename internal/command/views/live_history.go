// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveHistory is "choudoufu live-history"'s output: one already-rendered
// history on stdout. Same shape as [LiveSummary].
type LiveHistory struct {
	view *View
}

func NewLiveHistory(view *View) *LiveHistory {
	return &LiveHistory{view: view}
}

// Output writes the rendered history to stdout.
func (v *LiveHistory) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
