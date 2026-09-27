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
	"fmt"

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

	// surfaceWording is GitHub issue #1584's block, below.
	surfaceWording
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

// Sweeps reports whether providerType names a family whose own sweep leg
// finds its objects independently of internal/live/identity's admission
// table (GitHub issue #1581): a type belonging to such a family needs no
// row there to be found again once its last block is removed.
//
// Only Kubernetes qualifies today ([SweepLabelList]): its leg lists every
// kind the cluster serves and joins the result against the estate's
// objects, drawing its universe from the provider and the cluster rather
// than from the table. AWS's own sweep ([SweepTaggingIndex]) is that same
// admission table read a different way, so a type with no row gets nothing
// extra from it, and neither does an unregistered provider ForProvider
// does not recognise at all.
//
// This is the question [internal/live/identity]'s no-orphan-recovery
// warning needs, and it is asked by provider - the resource's own resolved
// provider configuration, never by a type's schema shape. A type can share
// a Kubernetes-shaped schema (an object-metadata block, say) with an
// unrelated provider's type by coincidence; only the provider says which
// sweep leg, if any, will actually look for it again.
func Sweeps(providerType string) bool {
	s, ok := ForProvider(providerType)
	return ok && s.Sweep() == SweepLabelList
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

// ---- GitHub issue #1584: one surface enum ----
//
// Until #1584 the projection kept its own marker-surface enum
// (markerSurface, with createCollidesOnKey and carrierPhrase switching on
// it) and live-mv kept another (mv.Surface), each mapped from
// [markers.Surface] by hand. A surface was then declared in three places,
// and the answers switching on the shadow enums were invisible to the
// completeness guard, which counts only [markers.Surface] constants: a
// fourth surface would have read as the tags attribute and as never
// colliding on its key, with nothing going red. The answers now live on the
// family that owns the surface, beside the rest of its answers.

// surfaceWording is the part of [Substrate] #1584 added.
type surfaceWording interface {
	// CreateCollidesOnKey reports whether, for a declared resource on
	// surface (one of this family's), an object read at its identity means
	// the resource's own create would be refused by the server as a
	// duplicate. See the package-level [CreateCollidesOnKey].
	CreateCollidesOnKey(surface markers.Surface) bool

	// CarrierPhrase names where surface's marker map lives on an object,
	// for a message telling an operator the provider returned no such map.
	CarrierPhrase(surface markers.Surface) string

	// NotACarrier explains, for one of this family's resource types whose
	// schema carries none of the family's surfaces, why there is nowhere on
	// it to carry a marker. It is a sentence that names the type.
	NotACarrier(block *configschema.Block, typeName string) string
}

// CreateCollidesOnKey reports whether, for a declared resource on surface,
// an object read at its identity means the resource's own create would be
// refused by the server as a duplicate: the first half of GitHub issue
// #1546's ruling, "its identity is a server-enforced unique key". False for
// the zero Surface.
//
// True for the Kubernetes label surface only, and each family's answer
// says why its other surfaces are excluded ([kubernetes.CreateCollidesOnKey],
// [aws.CreateCollidesOnKey]).
func CreateCollidesOnKey(surface markers.Surface) bool {
	s := For(surface)
	return s != nil && s.CreateCollidesOnKey(surface)
}

// CarrierPhrase names where surface's marker map lives ("tags attribute",
// "metadata.labels map"), for the one refusal that has to tell an operator
// the provider returned no such map. Empty for the zero Surface, which has
// no map to be missing: a caller asks this only of a surface it read.
func CarrierPhrase(surface markers.Surface) string {
	s := For(surface)
	if s == nil {
		return ""
	}
	return s.CarrierPhrase(surface)
}

// NotACarrier explains why a resource type of provider providerType has
// nowhere to carry an ownership marker, in its family's own words: the AWS
// "no tags map this configuration can set" (or the tags map the marker
// vocabulary cannot round-trip, [markers.NotAMarkerSurface]), and the
// Kubernetes "no metadata.labels map". A provider with no family gets a
// sentence that names no carrier rather than the AWS one. The caller has
// already established the type carries no surface ([SurfaceOf] false).
func NotACarrier(providerType string, block *configschema.Block, typeName string) string {
	if s, ok := ForProvider(providerType); ok {
		return s.NotACarrier(block, typeName)
	}
	return fmt.Sprintf("%s is from a provider this fork has no marker surface for, so there is nowhere to carry an ownership marker.", typeName)
}
