// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveGraph is "choudoufu live-graph"'s output: one JSON document on
// stdout (GitHub issue #2285). Same shape as [LiveHistory].
type LiveGraph struct {
	view *View
}

func NewLiveGraph(view *View) *LiveGraph {
	return &LiveGraph{view: view}
}

// Output writes the document to stdout.
func (v *LiveGraph) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
