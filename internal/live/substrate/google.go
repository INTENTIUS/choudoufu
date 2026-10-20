// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// Google is the hashicorp/google family, google and google-beta alike
// (GitHub issue #1119): the tofu-estate label, an encoded tofu-address
// label and, on a slotted count member, tofu-slot, all in the top-level
// labels map, ruled on #1121 (2026-10-11, and the address carrier
// 2026-10-19).
var Google Substrate = google{}

type google struct{}

func (google) Name() string { return "google" }

// ProviderTypes: google-beta serves the same types under the same
// identities, and a block can switch between the two (#1119 item 6).
func (google) ProviderTypes() []string { return []string{"google", "google-beta"} }

func (google) Surfaces() []markers.Surface {
	return []markers.Surface{markers.SurfaceGoogleLabels}
}

func (google) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if _, ok := markers.GoogleLabelSurface(block); ok {
		return markers.SurfaceGoogleLabels, true
	}
	return "", false
}

func (google) MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	if surface == markers.SurfaceGoogleLabels {
		return markers.GoogleLabelsOf(obj)
	}
	return nil, false
}

// Writes: the labels map is set in the create call. The six compute types
// whose create cannot take labels (addresses, forwarding rules, VPN
// tunnels, interconnects) have the provider set them inside its own create
// step (Magic Modules' post_create labels.tmpl), so nothing follows the
// create here. An existing object is labelled by a plan-then-apply that
// may change the labels map and nothing else ([WriteGoogleLabelsPlan]).
func (google) Writes(surface markers.Surface) Writes {
	if surface == markers.SurfaceGoogleLabels {
		return Writes{Create: WriteInCreate, Adopt: WriteGoogleLabelsPlan, PostCreate: WriteNeverNeeded}
	}
	return Writes{}
}

// WriteGoogleLabelsPlan: a plan-then-apply through the provider that
// changes the top-level labels map and nothing else, refused if it would
// change anything more: [WriteTagsPlan]'s write and refusal, on the map a
// hashicorp/google type carries its labels in. Its own value rather than
// [WriteLabelsPlan], which is confined to Kubernetes's metadata[0].labels.
const WriteGoogleLabelsPlan Write = "google-labels-plan"

// CarriesAddress: the tofu-address label, an encoding of the block address
// matched against the configuration's addresses and never decoded
// (live/MARKERS.md, "GCP: labels").
func (google) CarriesAddress() bool { return true }

// AddressInMarkers: the address label is in the labels map MarkersOf reads,
// as the AWS tag is in the tags map.
func (google) AddressInMarkers() bool { return true }

func (google) AddressCarrier(surface markers.Surface) (key, noun string) {
	if surface == markers.SurfaceGoogleLabels {
		return markers.TagAddress, "label"
	}
	return "", ""
}

// SweepAssetSearch is the GCP sweep: Cloud Asset Inventory's
// searchAllResources over each project the estate resolves to, through a
// client built from the provider block's own credentials.
const SweepAssetSearch Sweep = "asset-search"

func (google) Sweep() Sweep { return SweepAssetSearch }

// SweepFindsUnadmitted is true: the search's universe is every asset type
// carrying the estate's label, not the admission table's rows.
func (google) SweepFindsUnadmitted() bool { return true }

// MarkerOutlivesObject is true: Asset Inventory is an index, and it lags a
// delete as AWS's tagging index does, so a record tombstone is how the
// estate tells a lingering hit from a second claimant.
func (google) MarkerOutlivesObject() bool { return true }

// SynthesizeIdentity claims nothing: a google type with no generated row
// takes the identity-schema route every unrowed type takes
// ([aws.SynthesizeIdentity]), with no fallback of the family's own.
func (google) SynthesizeIdentity(string, providers.Schema) (SynthesizedIdentity, bool) {
	return SynthesizedIdentity{}, false
}

// ---- GitHub issue #1584 ----

// CreateCollidesOnKey is true: GCP refuses a create at a name already held
// in its scope with 409 alreadyExists, as the Kubernetes API server does.
// The projection asks it only for an unlabelled object it read at the
// declared key (internal/live/projection's ownership.go, atDeclaredKey), and
// a type whose name the server assigns has no declared key to read one at.
func (google) CreateCollidesOnKey(surface markers.Surface) bool {
	return surface == markers.SurfaceGoogleLabels
}

func (google) CarrierPhrase(surface markers.Surface) string {
	if surface == markers.SurfaceGoogleLabels {
		return "labels map"
	}
	return ""
}

// NotACarrier: a google type with no settable labels map beside a computed
// effective_labels has nowhere to carry the labels. It never says "tags":
// on GCP that word means Resource Manager tags, which are not this marker.
func (google) NotACarrier(_ *configschema.Block, typeName string) string {
	return fmt.Sprintf("%s has no labels map this configuration can set, so there is nowhere on it to carry an ownership marker.", typeName)
}

// ---- GitHub issue #1587 ----

// MarkerWriter is [WriteNeverNeeded]: the labels ride the create call.
func (google) MarkerWriter() Write { return WriteNeverNeeded }

// ---- GitHub issue #1642 ----

// PostCreateNeeded is never ([google.Writes]).
func (google) PostCreateNeeded(markers.Surface, Created, Facts) (string, bool) {
	return "", false
}

// ---- GitHub issue #1653 ----

// ManualMarkFix is never asked, since PostCreateNeeded never answers true.
// It names the gcloud-free remedy: the labels, set by hand.
func (google) ManualMarkFix(_ Created, want map[string]string, _ Facts) string {
	return genericMarkFix(want)
}

// ---- GitHub issue #1708 ----

// CreatedObject is the self_link when the object has one, the id beside it
// when that differs, else the id.
func (google) CreatedObject(created Created) string {
	link := ObjectString(created.Object, "self_link")
	if link == "" {
		return idPhrase(created.Object)
	}
	if id := ObjectString(created.Object, "id"); id != "" && id != link {
		return fmt.Sprintf("%s [id=%s]", link, id)
	}
	return link
}

// ---- GitHub issue #1649 ----

// CarrierPaths: the three maps [markers.GoogleLabelsOf] reads.
func (google) CarrierPaths(surface markers.Surface) []cty.Path {
	if surface == markers.SurfaceGoogleLabels {
		return []cty.Path{
			cty.GetAttrPath(markers.GoogleEffectiveLabelsAttr),
			cty.GetAttrPath(markers.GoogleTerraformLabelsAttr),
			cty.GetAttrPath(markers.GoogleLabelsAttr),
		}
	}
	return nil
}

func (google) MarkerNoun(surface markers.Surface) string {
	if surface == markers.SurfaceGoogleLabels {
		return "label"
	}
	return ""
}

// NewSweeper: placeholder until the Asset Inventory client lands.
func (google) NewSweeper(cty.Value, bool) (Sweeper, error) {
	return nil, fmt.Errorf("the GCP sweep client is not built yet")
}

// ---- GitHub issue #1706 ----

func (google) ControllerHeld(HoldEvidence) (Hold, bool) { return Hold{}, false }

func (google) HoldRecognition() HoldRecognition { return HoldRecognition{} }
