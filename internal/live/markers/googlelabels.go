// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"regexp"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is the hashicorp/google marker surface, ruled on GitHub issue
// #1121 (2026-10-11, option 4, and the address carrier 2026-10-19): the
// marker is labels, written in the create call: tofu-estate, an encoded
// tofu-address, and tofu-slot where AWS would write one. Resource Manager
// tag bindings, the only GCP carrier an IAM condition can match, are ruled
// later.
//
// # What a label can spell
//
// A GCP label value matches [\p{Ll}\p{Lo}\p{N}_-]{0,63}: no '.', no ':' and
// no capitals, so no escaped block address fits in one. [TagAddress] is
// therefore carried encoded (the address label, below), and [TagSlot],
// which is digits, as AWS writes it. An estate name ([ValidEstateName]) is
// [a-z0-9-] and up to 128 characters, so a name over 63 characters is a
// legal estate and an illegal label; [ValidGoogleLabelValue] is the check.
//
// # Which labels map
//
// The provider's KeyValueLabels pattern (hashicorp/google 5.0 onward) gives
// a labelled type three maps: "labels", the configuration's,
// non-authoritative; "terraform_labels", labels merged with the provider's
// default_labels; and "effective_labels", every label on the object in GCP,
// whoever set it. The marker is written to "labels" and read back from all
// three, the explicit map last, the way [TagsOf] reads tags_all and tags.
// A type is this surface only when it has the computed effective_labels
// map beside a settable labels map: at google 8.6.0 that is 299 of the 301
// types with a settable labels map, and the two without it are the ones
// whose "labels" is not resource labelling (google_cloud_identity_group,
// google_monitoring_notification_channel; #1121's measurement).

// GoogleLabelMaxValue is the longest label value GCP accepts.
const GoogleLabelMaxValue = 63

// googleLabelValuePattern is GCP's documented grammar for a label value,
// less the empty string, which is a legal label value and never a marker.
var googleLabelValuePattern = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{N}_-]{1,63}$`)

// ValidGoogleLabelValue reports whether s can be written as a GCP label
// value and be a marker: one to [GoogleLabelMaxValue] characters of
// lowercase letters, digits, '_' and '-'.
func ValidGoogleLabelValue(s string) bool {
	return googleLabelValuePattern.MatchString(s)
}

// The three maps of the provider's KeyValueLabels pattern.
const (
	GoogleLabelsAttr          = "labels"
	GoogleTerraformLabelsAttr = "terraform_labels"
	GoogleEffectiveLabelsAttr = "effective_labels"
)

// GoogleLabelSurface reports whether a resource type carries its marker as
// a hashicorp/google label: a settable top-level "labels" map of strings
// with a computed "effective_labels" map beside it. Read from the schema,
// never from a list of type names, for the same reason [Taggable] is.
//
// A type that is [Taggable] or a Kubernetes [LabelSurface] is never also
// this surface: neither has a top-level labels map with effective_labels
// beside it. The returned attribute is the labels map's schema.
//
//markers:surface google-labels
func GoogleLabelSurface(block *configschema.Block) (*configschema.Attribute, bool) {
	if block == nil {
		return nil, false
	}
	attr, ok := block.Attributes[GoogleLabelsAttr]
	if !ok || attr == nil || (!attr.Optional && !attr.Required) || !stringMap(attr.Type) {
		return nil, false
	}
	eff, ok := block.Attributes[GoogleEffectiveLabelsAttr]
	if !ok || eff == nil || !eff.Computed || eff.Optional || eff.Required || !stringMap(eff.Type) {
		return nil, false
	}
	return attr, true
}

func stringMap(ty cty.Type) bool {
	if !ty.IsMapType() {
		return false
	}
	et := ty.ElementType()
	return et == cty.String || et == cty.DynamicPseudoType
}

// GoogleLabelsOf reads the labels off a hashicorp/google object:
// effective_labels, then terraform_labels, then labels, a later map's key
// winning, so an explicitly configured label wins over the same key
// arriving through the provider's default_labels or set by another client.
// The second return is false when the object has none of the three maps.
//
//markers:surface google-labels
func GoogleLabelsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() {
		return nil, false
	}
	ty := obj.Type()
	var found bool
	labels := make(map[string]string)
	for _, name := range []string{GoogleEffectiveLabelsAttr, GoogleTerraformLabelsAttr, GoogleLabelsAttr} {
		if !ty.HasAttribute(name) {
			continue
		}
		found = true
		v := obj.GetAttr(name)
		if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() {
			continue
		}
		for it := v.ElementIterator(); it.Next(); {
			k, val := it.Element()
			if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
				continue
			}
			labels[k.AsString()] = val.AsString()
		}
	}
	if !found {
		return nil, false
	}
	return labels, true
}

// # The address label
//
// GitHub issue #1121's address ruling (2026-10-19): a label cannot spell a
// block address, so GCP carries [TagAddress] as a fixed-length encoding of
// one, which is matched against addresses and never decoded. The value is
// two parts joined by '-':
//
//	<block>-<instance>
//
// <instance> encodes the full instance address as OpenTofu prints it
// (module.net["a"].google_compute_subnetwork.this[2]); <block> encodes the
// same address with the resource's own instance key removed
// (module.net["a"].google_compute_subnetwork.this), module keys kept. Each
// is the SHA-256 of the UTF-8 string, base32-encoded (RFC 4648 alphabet,
// lowercase, no padding) and cut to [GoogleAddressPartLen] characters, 130
// bits. The block part is what lets a slotted count member whose address
// names a stale index still match its block, the slot then settling the
// index (live/MARKERS.md, "tofu-slot").

// GoogleAddressPartLen is the length of each part of a GCP address label.
const GoogleAddressPartLen = 26

// GoogleAddressLen is the length of a whole GCP address label value.
const GoogleAddressLen = 2*GoogleAddressPartLen + 1

var googleAddressEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func googleAddressPart(s string) string {
	sum := sha256.Sum256([]byte(s))
	return strings.ToLower(googleAddressEncoding.EncodeToString(sum[:]))[:GoogleAddressPartLen]
}

// GoogleAddressValue is the tofu-address label value for addr.
func GoogleAddressValue(addr addrs.AbsResourceInstance) string {
	return GoogleBlockValue(addr.ContainingResource()) + "-" + googleAddressPart(addr.String())
}

// GoogleBlockValue is the block part of the tofu-address label value of
// every instance of block.
func GoogleBlockValue(block addrs.AbsResource) string {
	return googleAddressPart(block.String())
}

// googleAddressPattern is the whole grammar of a GCP address label value.
var googleAddressPattern = regexp.MustCompile(`^[a-z2-7]{26}-[a-z2-7]{26}$`)

// ValidGoogleAddressValue reports whether s has the shape of a GCP address
// label value. It says nothing about which address s encodes.
func ValidGoogleAddressValue(s string) bool {
	return googleAddressPattern.MatchString(s)
}

// GoogleAddressBlock is the block part of a GCP address label value, or
// false when s is not one.
func GoogleAddressBlock(s string) (string, bool) {
	if !ValidGoogleAddressValue(s) {
		return "", false
	}
	return s[:GoogleAddressPartLen], true
}

// NotAGoogleLabelValue explains why estate cannot be a GCP label value, as
// a clause.
func NotAGoogleLabelValue(estate string) string {
	return fmt.Sprintf("the estate name %q is not a legal GCP label value (1 to %d lowercase letters, digits, '-' and '_')", estate, GoogleLabelMaxValue)
}

// GoogleLabelPath is the cty.Path for labels["<key>"] on a google-labels
// surface type.
//
//markers:surface google-labels
func GoogleLabelPath(key string) cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: GoogleLabelsAttr},
		cty.IndexStep{Key: cty.StringVal(key)},
	}
}
