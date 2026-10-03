// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveWaves is "choudoufu live-waves"' output: one already-rendered
// document on stdout. Same shape as [LiveBucket], for the same reason.
type LiveWaves struct {
	view *View
}

func NewLiveWaves(view *View) *LiveWaves {
	return &LiveWaves{view: view}
}

// Output writes the rendered waves to stdout.
func (v *LiveWaves) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
