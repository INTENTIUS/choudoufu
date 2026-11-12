// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import "strings"

// LiveSweepSecrets is "choudoufu live-sweep-secrets"' output: one
// already-rendered report on stdout. Same shape as [LiveHistory].
type LiveSweepSecrets struct {
	view *View
}

func NewLiveSweepSecrets(view *View) *LiveSweepSecrets {
	return &LiveSweepSecrets{view: view}
}

// Output writes the rendered report to stdout.
func (v *LiveSweepSecrets) Output(document string) {
	v.view.streams.Println(strings.TrimRight(document, "\n"))
}
