// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

// Surface names one place a resource type can carry the ownership marker.
// GitHub issue #1118: until it the surfaces were three predicates
// ([TagSurface], [LabelSurface], [ManifestSurface]) that every seam reading
// a marker consulted on its own, and three seams (the ownership read, #1108;
// live-import's carriers, #1109; live-mv's surface switch, #1104) were each
// found missing one after the unit that should have covered it had merged.
//
// Each exported function in this package that asks a schema, reads an
// object or names a path on behalf of one surface carries a directive
//
//	//markers:surface <name>
//
// naming the constant below it belongs to. seams_test.go reads the
// constants and the directives off this package's source, measures every
// function in the tree that references a surface's members, and fails when
// one handles some surfaces and not others without saying why. Adding a
// fourth surface is therefore one constant and one directive per new
// function here, and every seam that has not learned it goes red.
type Surface string

const (
	// SurfaceTags is the AWS shape: tofu-estate and tofu-address in a
	// settable top-level tags map.
	SurfaceTags Surface = "tags"

	// SurfaceLabels is the Kubernetes object-metadata shape: tofu-estate
	// alone, in metadata[0].labels.
	SurfaceLabels Surface = "labels"

	// SurfaceManifest is the kubernetes_manifest shape: the same one
	// label, inside the dynamic manifest argument at
	// manifest.metadata.labels.
	SurfaceManifest Surface = "manifest"
)
