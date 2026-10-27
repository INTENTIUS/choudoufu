// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/sweepconfig"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/pkg/gcpsweep"
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

// SweepFindsUnadmitted is false. The Asset Inventory search does see every
// labelled asset type, rowed or not, but it only reports what it finds: it
// never plans a removal, and it cannot see a type with no labels map at
// all (GitHub issue #2134, item 5). A deleted block is removed from its
// record instead, by the record-orphan leg, which on this family admits an
// unrowed type through the provider's identity schema ([RecordsRemoveUnrowed]).
// That is why the identity package's no-orphan-recovery warning is off for
// google types even though this answers false.
func (google) SweepFindsUnadmitted() bool { return false }

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

// NameHeldDetail is #1546's refusal in GCP's terms: the name is held in the
// type's project or location, and the marker is two labels, the estate and
// the encoded address.
func (google) NameHeldDetail(typeName, importID string, addr addrs.AbsResourceInstance, estate string) string {
	return fmt.Sprintf(
		"A live %s already exists at %q, the name %s declares, and carries no %s label, so this estate does not own it. Google Cloud refuses a create at a name already held in the same project or location with 409 alreadyExists, so the create this configuration declares would fail while that object holds the name; this plan stops here instead of proposing it. To adopt the object, set policy { declared_untagged = \"adopt\" } in the live block and re-run, or write the labels %s=%q and %s=%q onto it and re-run (choudoufu live-import -approve writes both for an object a stock state file records). To keep it out of this estate, point this resource at a name nobody is using.",
		typeName, importID, addr, markers.TagEstate, markers.TagEstate, estate, markers.TagAddress, markers.GoogleAddressValue(addr))
}

// CreateOverwritesAtKey is false: a GCP create at a name already held is
// refused with 409 alreadyExists ([google.CreateCollidesOnKey]), never
// written over the way an ARM PUT at an existing id is. Answered ahead of
// the azure branch's interface method so the two branches agree when they
// meet.
func (google) CreateOverwritesAtKey(markers.Surface) bool { return false }

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

// NewSweeper is the Cloud Asset Inventory client the provider block's own
// credential arguments build, read the way hashicorp/google reads them
// ([gcpsweep.ProviderAttrs], [gcpsweep.Attrs.Auth]). On an error the
// client is a nil [Sweeper], never an [AssetSearchSweeper] holding a nil
// client.
func (google) NewSweeper(providerConfig cty.Value, ok bool) (Sweeper, error) {
	client, err := sweepconfig.GCPSweep(context.Background(), gcpsweep.ProviderAttrs(providerConfig, ok))
	if err != nil {
		return nil, err
	}
	return AssetSearchSweeper{Client: client, Project: client.Project()}, nil
}

// AssetSearchSweeper is the GCP sweep client: the searcher, and the
// project the provider block names, searched with every project its
// resources name.
type AssetSearchSweeper struct {
	Client  gcpsweep.Searcher
	Project string
}

func (AssetSearchSweeper) SweepKind() Sweep { return SweepAssetSearch }

// ---- GitHub issue #1706 ----

// The asset types a GCP controller creates copies of a declared resource as,
// and the label keys each controller reserves on its copies. Measured from
// Google's documentation and hashicorp/google v8.6.0 for GitHub issue #1119:
// GKE copies a cluster's resource_labels onto its node VMs and their disks,
// Dataproc a cluster's labels onto its VMs and disks, Batch a job's
// allocation-policy labels onto its VMs and disks, and Cloud Functions gen2
// a function's labels onto the Cloud Run service it runs on. Exact keys,
// never a goog- prefix: the provider writes goog-terraform-provisioned on
// everything it creates, and the declared Dataproc cluster itself carries
// goog-dataproc-* keys, so a prefix would hold a leaked declared resource.
const (
	assetComputeInstance = "compute.googleapis.com/Instance"
	assetComputeDisk     = "compute.googleapis.com/Disk"
	assetRunService      = "run.googleapis.com/Service"
)

// googleHeldKeys is each reserved key on a Compute instance or disk, with
// the controller that writes it.
var googleHeldKeys = []struct{ key, controller string }{
	{"goog-gke-node", "GKE"},
	{"goog-gke-volume", "GKE"},
	{"goog-k8s-cluster-name", "GKE"},
	{"goog-k8s-node-pool-name", "GKE"},
	{"goog-dataproc-cluster-uuid", "Dataproc"},
	{"goog-dataproc-cluster-name", "Dataproc"},
	{"batch-job-uid", "Batch"},
}

// googleManagedByKey is the Cloud Run service label naming the product that
// manages it. An empty value means the service was detached from its
// function and is the operator's own again, so only a non-empty value holds.
const googleManagedByKey = "goog-managed-by"

// ControllerHeld: a Compute instance or disk carrying a key GKE, Dataproc or
// Batch reserves for what it creates, or a Cloud Run service whose
// goog-managed-by label names a product.
func (google) ControllerHeld(ev HoldEvidence) (Hold, bool) {
	switch ev.AssetType {
	case assetComputeInstance, assetComputeDisk:
		for _, k := range googleHeldKeys {
			if v, ok := ev.Labels[k.key]; ok {
				return Hold{Controller: k.controller, HeldBy: fmt.Sprintf("%s (%s=%s)", k.controller, k.key, v)}, true
			}
		}
	case assetRunService:
		if v := ev.Labels[googleManagedByKey]; v != "" {
			return Hold{Controller: v, HeldBy: fmt.Sprintf("%s (%s=%s)", v, googleManagedByKey, v)}, true
		}
	}
	return Hold{}, false
}

func (google) HoldRecognition() HoldRecognition {
	keys := make([]string, 0, len(googleHeldKeys)+1)
	for _, k := range googleHeldKeys {
		keys = append(keys, k.key)
	}
	keys = append(keys, googleManagedByKey)
	sort.Strings(keys)
	return HoldRecognition{
		Mechanism: "a Compute Engine instance or disk carrying a label key GKE, Dataproc or Batch reserves for the VMs and disks they create, or a Cloud Run service whose goog-managed-by label names a product; never a goog- prefix, which the provider writes on everything it creates",
		Keys:      keys,
	}
}

// Unrecorded answers per type from what the type's Create sends at the
// provider pin (google_unrecorded_generated.go, written by
// tools/gcp-row-gen): [UnrecordedCreates] where a create at an identity
// something already holds cannot collide (an IAM member, binding or policy,
// whose create is a setIamPolicy read-modify-write; a project service,
// whose Create takes a service it finds already enabled; a create sent as
// PATCH or PUT), and [UnrecordedNameHeld] everywhere else, where Google
// answers 409 alreadyExists or the source shows nothing either way. A type
// the table has never seen is name-held too, the safe side.
func (google) Unrecorded(typeName string) Unrecorded {
	if _, ok := googleUnrecordedCreates[typeName]; ok {
		return UnrecordedCreates
	}
	return UnrecordedNameHeld
}

// UnrecordedCounts is how many labelless types take each answer, for
// live/SUBSTRATES.md (tools/substrates-gen).
func (google) UnrecordedCounts() map[Unrecorded]int {
	return map[Unrecorded]int{
		UnrecordedCreates:  len(googleUnrecordedCreates),
		UnrecordedNameHeld: len(googleUnrecordedNameHeld),
	}
}
