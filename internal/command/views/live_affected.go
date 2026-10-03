// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveAffected is "choudoufu live-affected"'s output: one already-rendered
// document on stdout. Same shape as [LiveBucket], for the same reason.
type LiveAffected struct {
	view *View
}

func NewLiveAffected(view *View) *LiveAffected {
	return &LiveAffected{view: view}
}

// Output writes the rendered answer to stdout.
func (v *LiveAffected) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
