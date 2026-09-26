// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package substrate is the contract GitHub issue #1118 asks for between
// this fork's live path and a provider family: one [Substrate] value per
// family (AWS, Kubernetes) that answers which marker surface a schema
// carries, how the marker is read off a live or planned object, how it is
// written, and which sweep client the family's provider block builds.
//
// Before it, those answers were three dispatches that each asked the
// internal/live/markers predicates on their own: the projection's ownership
// read (markerSurfaceOf and markersOf), live-mv's surfaceOf, and
// live-import's ratifyOne carriers. Each was found missing a surface after
// the unit that should have covered it had merged (#1108, #1104, #1109).
// They now ask this package, and this package is the one place a new
// surface has to be taught. The completeness guard in
// internal/live/markers/seams_test.go measures the functions here the same
// way it measures every other seam, so a fourth surface fails every
// dispatch below until it is handled.
//
// It is an extraction: every answer here is the answer the dispatch it
// replaced gave, including the one place two of them disagree (see
// [OwnershipSurfaceOf]).
package substrate

import (
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// Write is how a marker reaches a live object.
type Write string

const (
	// WriteInCreate: the marker rides in the create call's own arguments,
	// put there by the node-path stamp (internal/live/projection).
	WriteInCreate Write = "in-create"

	// WriteTagsPlan: a plan-then-apply through the provider that changes
	// the tags map and nothing else, refused if it would change anything
	// more (internal/live/liveimport's tags.go, internal/live/mv's
	// rewrite.go).
	WriteTagsPlan Write = "tags-only-plan"

	// WriteLabelsPlan: the same, confined to metadata[0].labels
	// ([markers.WithLabels]; liveimport's labels.go, mv's label.go).
	WriteLabelsPlan Write = "labels-only-plan"

	// WriteAPIPatch: one merge patch of the label straight to the API
	// server under the caller's own credential, sent first with
	// dryRun=All and refused if the answer changes anything beyond the
	// labels (kubesweep.LabelPatcher; liveimport's manifest.go, ruled on
	// #1109 and #1104).
	WriteAPIPatch Write = "api-patch"
)

// Writes is how one surface's marker is written, by occasion.
type Writes struct {
	// Create is a resource this run creates.
	Create Write
	// Adopt is an existing object a migration (live-import -approve) or a
	// move between estates (live-mv -from-estate) marks.
	Adopt Write
}

// Sweep is which estate-sweep client a family's provider block builds.
type Sweep string

const (
	// SweepTaggingIndex is the AWS sweep: the discovery legs (the
	// Resource Groups Tagging API index, the provider's own list
	// resources, Cloud Control, direct reads), all driven through the
	// configured provider itself, so no client is built from the block.
	SweepTaggingIndex Sweep = "tagging-index"

	// SweepLabelList is the Kubernetes sweep: one label-selected list per
	// served kind, through a cluster client built from the provider
	// block's own connection arguments (Substrate.NewSweeper).
	SweepLabelList Sweep = "label-list"
)

// Substrate is one provider family's answers. There is one value per
// family ([AWS], [Kubernetes]), each in its own file, and the package-level
// functions below dispatch over [All]. A family answers only for its own
// surfaces; the dispatch is what answers for a schema or a surface whose
// family is not yet known.
//
// The split is also what keeps the completeness guard
// (internal/live/markers/seams_test.go) able to see the callers. The guard
// credits a caller in another package with the surfaces a callee references
// itself, and the dispatch functions here reference none: they reach the
// markers predicates only through the family methods. So a caller that
// asks [SurfaceOf] and then acts per surface handles exactly the
// [markers.Surface] constants it names, and one that forgets a surface is
// red.
type Substrate interface {
	// Name is the family's name, which is also the provider type name
	// [ForProvider] matches.
	Name() string

	// Surfaces are the marker surfaces this family's types carry. No
	// surface belongs to two families.
	Surfaces() []markers.Surface

	// SurfaceOf is the surface a resource type's schema carries when it is
	// one of this family's, read off the schema and never off the type
	// name.
	SurfaceOf(block *configschema.Block) (markers.Surface, bool)

	// OwnershipSurfaceOf is SurfaceOf as the projection's ownership read
	// asks it. See the package-level [OwnershipSurfaceOf].
	OwnershipSurfaceOf(block *configschema.Block) (markers.Surface, bool)

	// MarkersOf reads the marker map off an object from wherever surface,
	// one of this family's, keeps it.
	MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool)

	// Writes is how surface's marker, one of this family's, is written.
	Writes(surface markers.Surface) Writes

	// CarriesAddress is whether this family's marker holds a tofu-address
	// beside tofu-estate.
	CarriesAddress() bool

	// Sweep is which sweep client the family's provider block builds.
	Sweep() Sweep

	// NewSweeper builds the family's estate-sweep client from its provider
	// block's evaluated configuration (ok false when the run holds none):
	// nil with no error for a family whose sweep runs through the
	// configured provider itself ([SweepTaggingIndex]), and the error for a
	// block the client cannot be built from.
	NewSweeper(providerConfig cty.Value, ok bool) (*kubesweep.Client, error)
}

// All is every family, in the order a surface question asks them.
var All = []Substrate{AWS, Kubernetes}

// ForProvider is the family a provider type name belongs to ("aws",
// "kubernetes"). It matches the type name alone, which is what every
// provider-family check it replaced asked.
func ForProvider(providerType string) (Substrate, bool) {
	for _, s := range All {
		if s.Name() == providerType {
			return s, true
		}
	}
	return nil, false
}

// For is the family a surface belongs to, or nil for the zero Surface.
func For(surface markers.Surface) Substrate {
	for _, s := range All {
		for _, own := range s.Surfaces() {
			if own == surface {
				return s
			}
		}
	}
	return nil
}

// SurfaceOf is the marker surface a resource type's schema carries, or
// false when it has none. The families' predicates are disjoint by
// construction ([markers.LabelSurface] and [markers.ManifestSurface] each
// refuse a [markers.Taggable] type, and the manifest shape refuses a
// metadata block), so the order they are asked in cannot decide an answer.
//
// This is the question live-mv's surface switch and live-import's carrier
// choice asked.
func SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	for _, s := range All {
		if surface, ok := s.SurfaceOf(block); ok {
			return surface, true
		}
	}
	return "", false
}

// OwnershipSurfaceOf is [SurfaceOf] as the projection's ownership read has
// always asked it, and differs in the tag arm alone: any "tags" or
// "tags_all" attribute at all counts ([markers.HasTagsAttribute]), settable
// or not, where [SurfaceOf] asks for a settable tag map the marker
// vocabulary can round-trip ([markers.Taggable]). Narrowing it would change
// which AWS types the ownership rule covers, so the two questions stay two
// and this extraction keeps each caller on the one it asked.
func OwnershipSurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	for _, s := range All {
		if surface, ok := s.OwnershipSurfaceOf(block); ok {
			return surface, true
		}
	}
	return "", false
}

// MarkersOf reads the marker map off an object from wherever surface keeps
// it. The second return is the surface reader's own: false means the object
// has no such map at all, which on a type whose schema declares one is a
// provider bug and never a licence to adopt. False for the zero Surface.
func MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	s := For(surface)
	if s == nil {
		return nil, false
	}
	return s.MarkersOf(surface, obj)
}

// CarriesAddress reports whether surface's marker holds a tofu-address,
// which is its family's [Substrate.CarriesAddress]. Only the AWS tag map
// does: #1016's ruling is that the Kubernetes marker is the estate label
// alone, because the object's group, kind, namespace and name are the join
// key back to configuration. False for the zero Surface.
func CarriesAddress(surface markers.Surface) bool {
	s := For(surface)
	return s != nil && s.CarriesAddress()
}

// WritesOf is how surface's marker is written. Every surface is written in
// the create call; what differs is how an existing object is marked. The
// zero Writes for the zero Surface.
func WritesOf(surface markers.Surface) Writes {
	s := For(surface)
	if s == nil {
		return Writes{}
	}
	return s.Writes(surface)
}
