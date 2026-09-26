// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package seams

import "example.com/fixture/markers"

// actOn acts on a Surface value a dispatch returned, and names two of the
// four constants: the #1104 shape one layer below a Substrate dispatch.
func actOn(s markers.Surface) string {
	switch s {
	case markers.SurfaceTags:
		return "rewrite tags"
	case markers.SurfaceLabels:
		return "rewrite labels"
	}
	return ""
}
