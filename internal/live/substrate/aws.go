// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// AWS is the hashicorp/aws family: tofu-estate and tofu-address in a
// settable top-level tags map.
var AWS Substrate = aws{}

type aws struct{}

func (aws) Name() string { return "aws" }

func (aws) Surfaces() []markers.Surface { return []markers.Surface{markers.SurfaceTags} }

func (aws) SurfaceOf(block *configschema.Block) (markers.Surface, bool) {
	if markers.Taggable(block) {
		return markers.SurfaceTags, true
	}
	return "", false
}

func (aws) MarkersOf(surface markers.Surface, obj cty.Value) (map[string]string, bool) {
	if surface == markers.SurfaceTags {
		return markers.TagsOf(obj)
	}
	return nil, false
}

// Writes: the tags map is set in the create call, and an existing object's
// is rewritten by a plan-then-apply that may change nothing else
// (internal/live/liveimport's tags.go, internal/live/mv's rewrite.go). A
// type whose create call cannot carry tags is marked through the Tagging
// API once the create returns (GitHub issue #1084, #1587).
func (aws) Writes(surface markers.Surface) Writes {
	if surface == markers.SurfaceTags {
		return Writes{Create: WriteInCreate, Adopt: WriteTagsPlan, PostCreate: WriteTaggingAPI}
	}
	return Writes{}
}

func (aws) CarriesAddress() bool { return true }

// AddressInMarkers: the address is the tofu-address tag (#1641).
func (aws) AddressInMarkers() bool { return true }

// AddressCarrier: the tofu-address tag, inside the tag map.
func (aws) AddressCarrier(surface markers.Surface) (key, noun string) {
	if surface == markers.SurfaceTags {
		return markers.TagAddress, "tag"
	}
	return "", ""
}

func (aws) Sweep() Sweep { return SweepTaggingIndex }

func (aws) NewSweeper(cty.Value, bool) (Sweeper, error) { return nil, nil }

// ---- GitHub issue #1584: the answers the projection's shadow enum held ----

// CreateCollidesOnKey is false: AWS is out of scope of #1546's ruling.
// There a create of an existing object often succeeds, renames or is
// idempotent rather than conflicting, and that is per type and unmeasured.
func (aws) CreateCollidesOnKey(markers.Surface) bool { return false }

// CarrierPhrase: the wording the ownership read used before a second
// surface existed, unchanged.
func (aws) CarrierPhrase(surface markers.Surface) string {
	if surface == markers.SurfaceTags {
		return "tags attribute"
	}
	return ""
}

// NotACarrier is [markers.NotAMarkerSurface]: no settable tags map, or one
// whose keys the marker vocabulary cannot round-trip.
func (aws) NotACarrier(block *configschema.Block, typeName string) string {
	return markers.NotAMarkerSurface(block, typeName)
}

// ---- GitHub issue #1587: the post-create marker write ----

// MarkerWriter is [WriteTaggingAPI] for every AWS provider configuration:
// internal/command builds the Tagging API client signed as that
// configuration's own principal.
func (aws) MarkerWriter(addrs.AbsProviderConfig) Write { return WriteTaggingAPI }

// ---- GitHub issue #1642: whether a create needs the post-create write ----

// AWSCreateTagFacts is the AWS family's entry in [Facts] (GitHub issue
// #1708): live/mapping.json's Terraform-to-CloudFormation join and
// live/registry.json's tagging.tag_on_create. *internal/live/registry.Roster
// implements it, nil included (every answer false); internal/command puts
// the embedded roster under [AWS]'s name. An interface so this package
// stays below the registry.
type AWSCreateTagFacts interface {
	CloudControlTypeOrService(tfType string) (string, bool)
	TagsAfterCreate(cfnType string) bool
}

// awsFacts is the AWS entry of facts, or nil when the run holds none.
func awsFacts(facts Facts) AWSCreateTagFacts {
	f, _ := facts.Of(AWS.Name()).(AWSCreateTagFacts)
	return f
}

// PostCreateNeeded is the answer #1084 read in the projection, unchanged:
// the tags surface of a type whose CloudFormation counterpart
// (live/mapping.json) is taggable with tag_on_create false
// (live/registry.json). A type the mapping never joined, or the registry
// cannot vouch for, takes the create-call path.
func (aws) PostCreateNeeded(surface markers.Surface, created Created, facts Facts) (string, bool) {
	reg := awsFacts(facts)
	if surface != markers.SurfaceTags || reg == nil {
		return "", false
	}
	cfnType, ok := reg.CloudControlTypeOrService(created.Type())
	if !ok || !reg.TagsAfterCreate(cfnType) {
		return "", false
	}
	return fmt.Sprintf("%s does not take tags in its create call (live/registry.json: tag_on_create false)", cfnType), true
}

// ---- GitHub issue #1653: the manual-mark hint ----

// ManualMarkFix is the text GitHub issue #1084 first printed, unchanged
// byte-for-byte, now asked through the family rather than built inline by
// the projection: the aws CLI's resourcegroupstaggingapi command by ARN
// when the applied object carries one; otherwise the CloudFormation type's
// own tag write, when the roster names one; otherwise a sentence naming
// just the markers. The arn is read here (GitHub issue #1708), not by the
// shared node path.
func (aws) ManualMarkFix(created Created, want map[string]string, facts Facts) string {
	tagsArg := markers.TagsArgument(want)
	if arn := objectString(created.Object, "arn"); arn != "" {
		return fmt.Sprintf("Mark it, then plan again:\n\n  aws resourcegroupstaggingapi tag-resources --resource-arn-list %s --tags %s", arn, tagsArg)
	}
	if reg := awsFacts(facts); reg != nil {
		if cfnType, ok := reg.CloudControlTypeOrService(created.Type()); ok {
			return fmt.Sprintf("Mark it by hand with the tag write %s takes, with the tags %s, then plan again.", cfnType, tagsArg)
		}
	}
	return genericMarkFix(want)
}

// ---- GitHub issue #1708: the created object, named ----

// CreatedObject is the wording #1084 built inline in the projection,
// unchanged: the arn, with the id beside it when that differs; the id
// alone when there is no arn; a sentence when there is neither.
func (aws) CreatedObject(created Created) string {
	arn := objectString(created.Object, "arn")
	id := objectString(created.Object, "id")
	object := arn
	if id != "" && id != arn {
		object = fmt.Sprintf("%s [id=%s]", arn, id)
	}
	if arn == "" && id != "" {
		object = fmt.Sprintf("[id=%s]", id)
	}
	if object == "" {
		object = "an object with no arn and no id in what the provider returned"
	}
	return object
}

// ---- GitHub issue #1649: the carrier's wholly-known read ----

// CarrierPaths: both maps [markers.TagsOf] reads.
func (aws) CarrierPaths(surface markers.Surface) []cty.Path {
	if surface == markers.SurfaceTags {
		return []cty.Path{cty.GetAttrPath("tags"), cty.GetAttrPath("tags_all")}
	}
	return nil
}

func (aws) MarkerNoun(surface markers.Surface) string {
	if surface == markers.SurfaceTags {
		return "tag"
	}
	return ""
}

// ---- GitHub issue #1706: controller-held ----

// ControllerHeld is [markers.ControllerHeld] on the resource's tags: one of
// the fixed controller tag keys ACK and Crossplane write.
func (aws) ControllerHeld(ev HoldEvidence) (Hold, bool) {
	h, ok := markers.ControllerHeld(ev.Tags)
	if !ok {
		return Hold{}, false
	}
	return Hold{Controller: string(h.Controller), HeldBy: h.Describe()}, true
}

func (aws) HoldRecognition() HoldRecognition {
	keys := make([]string, 0, len(markers.ControllerTagKeys))
	for _, k := range markers.ControllerTagKeys {
		keys = append(keys, k.Key)
	}
	sort.Strings(keys)
	return HoldRecognition{
		Mechanism: "the resource's own tags carry one of the fixed controller tag keys (markers.ControllerTagKeys: ACK, Crossplane)",
		Keys:      keys,
	}
}
