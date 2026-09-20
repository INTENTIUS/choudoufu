// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

// LiveCluster is "choudoufu live-cluster"'s output: one already-rendered
// document, a table or JSON, on stdout. The command renders it because the
// table and the JSON are two spellings of one report and belong side by
// side; this type only owns where it goes. Same shape as [LiveBucket], for
// the same reason.
type LiveCluster struct {
	view *View
}

func NewLiveCluster(view *View) *LiveCluster {
	return &LiveCluster{view: view}
}

// Output writes the rendered report to stdout.
func (v *LiveCluster) Output(document string) {
	v.view.streams.Println(document)
}
