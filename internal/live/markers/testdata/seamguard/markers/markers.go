// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markers is the fixture's marker package for
// TestSurfaceSeamGuardSeesTheFixture. It is read by go/parser only and
// never built.
package markers

type Surface string

const (
	SurfaceTags        Surface = "tags"
	SurfaceLabels      Surface = "labels"
	SurfaceManifest    Surface = "manifest"
	SurfaceAnnotations Surface = "annotations"
)

//markers:surface tags
func Taggable(block *configschema.Block) bool { return true }

//markers:surface tags
func TagsOf(obj cty.Value) (map[string]string, bool) { return nil, false }

//markers:surface labels
func LabelSurface(block *configschema.Block) (*configschema.Attribute, bool) { return nil, false }

//markers:surface labels
func LabelsOf(obj cty.Value) (map[string]string, bool) { return nil, false }

//markers:surface manifest
func ManifestSurface(block *configschema.Block) bool { return false }

// AnnotationsOf is a surface's reader with no predicate beside it.
//
//markers:surface annotations
func AnnotationsOf(obj cty.Value) (map[string]string, bool) { return nil, false }

// Orphan acts for a surface and says not which.
func Orphan(block *configschema.Block) bool { return false }

// Mystery names a surface nobody declared.
//
//markers:surface gcp
func Mystery(obj cty.Value) bool { return false }

// EscapeAddress takes no marker value and needs no directive.
func EscapeAddress(addr string) string { return addr }
