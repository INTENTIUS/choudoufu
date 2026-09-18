// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

// LiveBucket is "choudoufu live-bucket"'s output: one already-rendered
// document, a table or JSON, on stdout. The command renders it because the
// table and the JSON are two spellings of one report and belong side by
// side; this type only owns where it goes.
type LiveBucket struct {
	view *View
}

func NewLiveBucket(view *View) *LiveBucket {
	return &LiveBucket{view: view}
}

// Output writes the rendered report to stdout.
func (v *LiveBucket) Output(document string) {
	v.view.streams.Println(document)
}
