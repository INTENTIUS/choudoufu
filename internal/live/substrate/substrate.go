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
// replaced gave. GitHub issue #1589: the projection's ownership read used to
// ask a looser tag question than [SurfaceOf] (markers.HasTagsAttribute (since deleted)),
// kept apart in case the two ever disagreed on a real AWS or Kubernetes
// type. The 2026-09-26 decision package measured the disagreement empty at
// every pinned provider version, so the ownership read now asks [SurfaceOf]
// like every other caller and the second question is gone.
package substrate

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
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
	// PostCreate is a resource this run creates whose create call cannot
	// carry the marker, so it is written onto the object once the create
	// returns (GitHub issue #1587, [WriteTaggingAPI], [WriteNeverNeeded]).
	PostCreate Write
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

	// MarkersOf reads the marker map off an object from wherever surface,
	// one of this family's, keeps it.
	MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool)

	// Writes is how surface's marker, one of this family's, is written.
	Writes(surface markers.Surface) Writes

	// CarriesAddress is whether this family's objects carry the block
	// address beside tofu-estate, so that the sweep can bind a live object
	// back to the block that made it: the AWS tofu-address tag, or the
	// Kubernetes address annotation (GitHub issues #1639 to #1641). Where
	// the address sits is [Substrate.AddressInMarkers]'s question.
	CarriesAddress() bool

	// Sweep is which sweep client the family's provider block builds.
	Sweep() Sweep

	// NewSweeper builds the family's estate-sweep client from its provider
	// block's evaluated configuration (ok false when the run holds none):
	// nil with no error for a family whose sweep runs through the
	// configured provider itself ([SweepTaggingIndex]), and the error for a
	// block the client cannot be built from. The client is a [Sweeper],
	// never a family's concrete type (GitHub issue #1580).
	NewSweeper(providerConfig cty.Value, ok bool) (Sweeper, error)

	// --- Admission (GitHub issue #1586; see identity.go) ---

	// SynthesizeIdentity is how this family's schema identifies an
	// instance of a type the ratified table does not cover, or false when
	// the schema is not one of this family's shapes. Asked in [All]'s
	// order by internal/live/identity's synthesizeTypeIdentity, and the
	// first family to answer decides.
	SynthesizeIdentity(typeName string, schema providers.Schema) (SynthesizedIdentity, bool)

	// surfaceWording is GitHub issue #1584's block, below.
	surfaceWording

	// markerWriting is GitHub issue #1587's block, below.
	markerWriting

	// postCreateNeed is GitHub issue #1642's block, below.
	postCreateNeed
	// markerCarrier is GitHub issue #1649's block, below.
	markerCarrier

	// addressCarrier is GitHub issue #1641's block, below.
	addressCarrier
}

// All is every family, in the order a surface question asks them.
//
// AWS is last, and that is load-bearing for [Substrate.SynthesizeIdentity]
// (GitHub issue #1586): the AWS answer is the identity-schema route, which
// claims every type, so a family with a convention of its own has to be
// asked before it. The surface questions are disjoint and do not care.
var All = []Substrate{Kubernetes, AWS}

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

// CarriesAddress reports whether an object on surface carries its block
// address, which is its family's [Substrate.CarriesAddress]. Both families
// do: AWS in the tofu-address tag, Kubernetes in the address annotation
// beside the estate label (GitHub issue #1641, step 3 of the ruling on
// #1605). False for the zero Surface.
//
// On Kubernetes an object can still lack the annotation - one an older
// build created, or one migrated from stock state before live-import
// stamped it - so a reader that needs the address of one particular
// object asks that object, not this.
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

// ---- GitHub issue #1580: the sweep client behind an interface ----
//
// Kept in its own block: several units of #1579 add methods to this file.

// Sweeper is a family's estate-sweep client as [Substrate.NewSweeper]
// builds it from the provider block. SweepKind is the sweep it serves,
// its family's own [Substrate.Sweep]: internal/live/discovery pairs a
// client with the leg that lists through it by that property, never by
// the family's name, so a third family's client plugs in by naming a
// sweep and a leg serving it.
type Sweeper interface {
	SweepKind() Sweep
}

// LabelListSweeper is the Kubernetes family's client: the cluster client
// built from the provider block, whose methods it carries
// (kubesweep.Sweeper, kubesweep.LabelPatcher).
type LabelListSweeper struct {
	*kubesweep.Client
}

// SweepKind is [SweepLabelList].
func (LabelListSweeper) SweepKind() Sweep { return SweepLabelList }

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

// ---- GitHub issue #1587: the post-create marker write ----
//
// Kept in its own block: several units of #1579 add methods to this file.
//
// Before #1587 the post-create write (internal/live/projection's
// nodetagoncreate.go) asked nobody which writer to use: the command layer
// built a Resource Groups Tagging API client when the provider type string
// was "aws" and nil otherwise, and [Writes] had no reader outside tests. A
// family whose marker is a side resource written after the create (GCP's
// tag bindings are one) would have had its creates left unmarked with
// nothing saying why. Now the surface's [Writes.PostCreate] names the
// write, the family's [Substrate.MarkerWriter] names the writer its
// provider configurations build, and internal/command builds the client
// from a table keyed on the [Write], refusing by name a write it has no
// client for.

const (
	// WriteTaggingAPI: the Resource Groups Tagging API's TagResources,
	// addressed by the arn the provider returned, issued after the create
	// of a type whose create call cannot carry tags (live/registry.json's
	// tag_on_create false, GitHub issue #1084).
	WriteTaggingAPI Write = "tagging-api"

	// WriteNeverNeeded: the create call always carries the marker, so no
	// write follows it. Named rather than left empty so that a family that
	// has not answered is distinguishable from one that answered "never".
	WriteNeverNeeded Write = "never-needed"
)

// markerWriting is the part of [Substrate] #1587 added.
type markerWriting interface {
	// MarkerWriter is the post-create write a provider configuration of
	// this family builds a client for: [WriteNeverNeeded] for a family
	// whose every surface rides the create call.
	MarkerWriter(provider addrs.AbsProviderConfig) Write
}

// ---- GitHub issue #1642: whether a create needs the post-create write ----
//
// Kept in its own block, like #1587's above.
//
// #1587 let the family name the post-create writer and #1638 handed that
// writer the created instance, but whether a create needs the write at all
// was still asked of the AWS CloudFormation registry alone
// (internal/live/projection's tagsAfterCreate read live/mapping.json and
// live/registry.json's tag_on_create). A type with no CloudFormation
// counterpart read false, so no other family's type ever reached its
// writer. The question is now the family's: AWS answers from the registry
// exactly as before, Kubernetes answers never, and a family whose marker is
// written after the create (a GCP tag binding, say) answers for its own
// types.

// CreateTagFacts is the registry read the AWS family answers from:
// live/mapping.json's Terraform-to-CloudFormation join and
// live/registry.json's tagging.tag_on_create. *internal/live/registry.Roster
// implements it, nil included (every answer false). An interface so this
// package stays below the registry.
type CreateTagFacts interface {
	CloudControlTypeOrService(tfType string) (string, bool)
	TagsAfterCreate(cfnType string) bool
}

// postCreateNeed is the part of [Substrate] #1642 added.
type postCreateNeed interface {
	// PostCreateNeeded reports whether a create of typeName, whose schema
	// carries surface (one of this family's), cannot carry the marker in
	// its create call, so the marker is withheld from the create and
	// written once it returns through [Writes.PostCreate]. The reason is
	// the sentence an operator reads when that write fails, naming the
	// fact the answer came from; empty when the answer is false. facts may
	// hold nothing for a family that does not read it.
	PostCreateNeeded(surface markers.Surface, typeName string, facts CreateTagFacts) (reason string, needed bool)
}

// PostCreateNeeded is [Substrate.PostCreateNeeded] asked of surface's
// family. False for the zero Surface.
func PostCreateNeeded(surface markers.Surface, typeName string, facts CreateTagFacts) (string, bool) {
	s := For(surface)
	if s == nil {
		return "", false
	}
	return s.PostCreateNeeded(surface, typeName, facts)
}

// ---- GitHub issue #1649: the carrier's wholly-known read ----
//
// The stateful un-migration guard (internal/live/markerstrip) compares the
// marker map on a planned update's prior and planned objects. A surface
// reader answers "this object carries no markers" for an unknown map
// ([markers.LabelsOf] returns an empty map, ok true), which is right for the
// question it answers and wrong for that comparison: a planned object whose
// labels are not yet known would read as one whose marker was removed. So
// the known-ness check sits outside the reader, on the carrier alone.

// markerCarrier is the part of [Substrate] #1649 added.
type markerCarrier interface {
	// CarrierPaths are the paths, from a resource object's root, of the
	// maps surface's marker lives in: tags and tags_all, metadata[0].labels,
	// manifest.metadata.labels.
	CarrierPaths(surface markers.Surface) []cty.Path

	// MarkerNoun is what one entry of surface's marker map is called, for
	// wording that names it: "tag" or "label".
	MarkerNoun(surface markers.Surface) string
}

// KnownMarkersOf is [MarkersOf] for a caller that must not read an unknown
// marker map as an empty one. It reports false when the object is null or
// unknown, when any carrier path reaches an unknown value (the map, or
// anything on the way to it), or when the surface reader itself reports
// false. A value beside the carrier being unknown, such as a planned
// object's metadata.resource_version, does not hide a marker that is known.
//
// A carrier path that ends early at a null, or indexes past an empty list,
// is not unknown: the reader decides what that object carries.
func KnownMarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	s := For(surface)
	if s == nil || obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return nil, false
	}
	for _, path := range s.CarrierPaths(surface) {
		if !carrierKnown(obj, path) {
			return nil, false
		}
	}
	return s.MarkersOf(surface, obj)
}

// carrierKnown walks path from obj and reports whether it reaches a wholly
// known value, or stops at a null or a missing step before it.
func carrierKnown(obj cty.Value, path cty.Path) bool {
	// Known-ness is all this asks, and a mark never changes it; the
	// reader keeps its own discipline about reading a marked value.
	v, _ := obj.UnmarkDeep()
	for _, step := range path {
		if !v.IsKnown() {
			return false
		}
		if v.IsNull() {
			return true
		}
		next, err := step.Apply(v)
		if err != nil {
			// A missing attribute, a non-object, an index past the end:
			// no carrier here, which the reader answers for itself.
			return true
		}
		v = next
	}
	return v.IsWhollyKnown()
}

// MarkerNoun is what one entry of surface's marker map is called ("tag",
// "label"), or "marker" for the zero Surface.
func MarkerNoun(surface markers.Surface) string {
	if s := For(surface); s != nil {
		if n := s.MarkerNoun(surface); n != "" {
			return n
		}
	}
	return "marker"
}

// ---- GitHub issue #1641: where the address rides ----
//
// Until #1641, [Substrate.CarriesAddress] answered two questions at once,
// because only one family carried an address: whether an object carries
// its block address at all, and whether that address is a key of the
// marker map [MarkersOf] reads (tofu-address and its continuation tags).
// Kubernetes carries the address in an annotation, outside the label map
// that is its marker, so the two answers part there. #1617's refusal asks
// the first; the ownership read's address check, the stale-record check,
// the adoption hint and live-mv's tag path ask the second, and each of
// those still reads or writes the tofu-address tag key specifically.

// addressCarrier is the part of [Substrate] #1641 added.
type addressCarrier interface {
	// AddressInMarkers is whether the block address is a key of the
	// marker map [Substrate.MarkersOf] reads, written as tofu-address
	// beside tofu-estate: true for the AWS tag map. False for Kubernetes,
	// whose marker map is the labels and whose address is the
	// markers.AddressAnnotation annotation beside them.
	AddressInMarkers() bool
}

// AddressInMarkers reports whether surface's marker map holds the
// tofu-address key, which is its family's [Substrate.AddressInMarkers].
// False for the zero Surface.
func AddressInMarkers(surface markers.Surface) bool {
	s := For(surface)
	return s != nil && s.AddressInMarkers()
}
