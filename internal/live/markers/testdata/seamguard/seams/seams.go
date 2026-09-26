// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package seams

import "example.com/fixture/markers"

func SurfaceOf(b *configschema.Block) string {
	if markers.Taggable(b) {
		return "tags"
	}
	if _, ok := markers.LabelSurface(b); ok {
		return "labels"
	}
	return "tags"
}

func complete(b *configschema.Block, obj cty.Value) {
	_ = markers.Taggable(b)
	_, _ = markers.LabelsOf(obj)
	_ = markers.ManifestSurface(b)
	_, _ = markers.AnnotationsOf(obj)
}

func ownership(obj cty.Value) bool {
	tags, _ := markers.TagsOf(obj)
	return unrelated(tags)
}

func unrelated(m map[string]string) bool { return len(m) > 0 }

func taggable(b *configschema.Block) bool { return markers.Taggable(b) }

func labelled(b *configschema.Block) bool {
	_, ok := markers.LabelSurface(b)
	return ok
}

func manifested(b *configschema.Block) bool { return markers.ManifestSurface(b) }

func ratify(b *configschema.Block) string {
	switch {
	case taggable(b):
		return "tags"
	case labelled(b):
		return "labels"
	}
	return "untaggable"
}

type reader struct{ b *configschema.Block }

func (r reader) read(obj cty.Value) {
	_, _ = markers.TagsOf(obj)
	r.labels(obj)
}

func (r reader) labels(obj cty.Value) {
	_, _ = markers.LabelsOf(obj)
	_, _ = markers.AnnotationsOf(obj)
	_ = manifested(r.b)
}
