// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
)

// This file is the Azure Resource Manager marker surface (GitHub issue
// #1120, ruled on #1122 2026-10-19): the AWS marker keys, tofu-estate,
// tofu-address with its continuations and tofu-slot, in the tags argument
// hashicorp/azurerm and azure/azapi already accept.
//
// # Why it is not SurfaceTags
//
// The schema is the same shape as AWS's, a settable top-level tags map of
// strings, and before #1120 azurerm was stamped through the AWS answer by
// substrate.SurfaceOf's compatible default. What differs is the key
// grammar: ARM tag keys are case-insensitive. ARM keeps the spelling a
// write used and refuses two keys that differ only by case, so a
// configuration that already sets Tofu-Estate cannot have a second
// tofu-estate added beside it, and a live object may hand back a marker key
// in any spelling an earlier write (an operator's az tag update, an Azure
// Policy modify effect) chose. Every reader here therefore matches marker
// keys case-insensitively and reports them in the canonical lowercase
// spelling the rest of the tree compares against, and the stamp writes under
// the configuration's own spelling when it has one ([ARMTagKeySpellings]).
//
// There is no tags_all: neither provider merges provider-level default tags
// into a second attribute the way hashicorp/aws does.

// ARMTagsArgument is the attribute both providers carry their tags in, and
// the key azapi's body carries them under.
const ARMTagsArgument = "tags"

// ARMTagSurface reports whether a resource type carries the ARM marker
// surface, with the tag map's attribute schema: a settable top-level "tags"
// map of strings, and no "tags_all" beside it. It is the shape of every one
// of azurerm 5.10.0's 390 tag-carrying types except the three whose "tags" is
// a list (API Management named values, a Sentinel indicator), and of
// azapi_resource's top-level tags argument.
//
// It is [TagSurface] with the tags_all clause added, the vocabulary refusal
// included, so a block that answers true here always answers true there:
// before GitHub issue #1120 every azurerm type was stamped through
// [TagSurface], and none that was stamped then is left unstamped now.
//
// Whether one azapi_resource can take tags at all is a property of its ARM
// type and API version, not of this schema; the stamp asks that separately
// (internal/live/projection's armTypeTaggable).
//
//markers:surface arm-tags
func ARMTagSurface(block *configschema.Block) (*configschema.Attribute, bool) {
	if block == nil {
		return nil, false
	}
	if _, merged := block.Attributes["tags_all"]; merged {
		return nil, false
	}
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return nil, false
	}
	if !attr.Optional && !attr.Required {
		return nil, false
	}
	if !attr.Type.IsMapType() {
		return nil, false
	}
	if et := attr.Type.ElementType(); et != cty.String && et != cty.DynamicPseudoType {
		return nil, false
	}
	if _, refused := VocabularyRefusal(attr.Description); refused {
		return nil, false
	}
	return attr, true
}

// ARMTaggable is [ARMTagSurface] as a bare predicate.
//
//markers:surface arm-tags
func ARMTaggable(block *configschema.Block) bool {
	_, ok := ARMTagSurface(block)
	return ok
}

// CanonicalMarkerKey reports whether key names one of the marker keys,
// compared case-insensitively, and returns that marker's canonical
// lowercase spelling: [TagEstate], [TagAddress], a continuation tag
// ([ContinuationTag] 2 to [MaxContinuations]) or [TagSlot]. ARM tag keys
// are case-insensitive, so Tofu-Estate on an Azure object is tofu-estate.
func CanonicalMarkerKey(key string) (string, bool) {
	lower := strings.ToLower(key)
	switch lower {
	case TagEstate, TagAddress, TagSlot:
		return lower, true
	}
	for n := 2; n <= MaxContinuations; n++ {
		if lower == ContinuationTag(n) {
			return lower, true
		}
	}
	return "", false
}

// ARMTagsOf reads an Azure object's tags, with every marker key normalised
// to its canonical lowercase spelling ([CanonicalMarkerKey]) and every other
// key as the object spelled it. The second return is false when the object
// has no tags attribute at all, as [TagsOf]'s is.
//
// It reads the top-level tags attribute alone, for both of azapi_resource's
// write paths. A configuration that puts its tags in body.tags has the stamp
// write the marker there too (azapi refuses a configuration that sets both),
// and azapi 2.13.0 still copies the response body's tags into the top-level
// tags attribute of state: in Read (internal/services/azapi_resource.go
// lines 1182-1184) and in ImportState (lines 1419-1421). azurerm's tags
// attribute is always top-level.
//
// A marked tags map reads as false: unreadable rather than empty.
//
//markers:surface arm-tags
func ARMTagsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() {
		return nil, false
	}
	ty := obj.Type()
	if !ty.IsObjectType() || !ty.HasAttribute("tags") {
		return nil, false
	}
	tags := make(map[string]string)
	v := obj.GetAttr("tags")
	if v.IsMarked() {
		// Unreadable, never empty: iterating a marked map would drop its
		// mark (internal/live/marksafe), and reading it as carrying no
		// marker would be a claim this reader cannot make.
		return nil, false
	}
	if v.IsNull() || !v.IsKnown() || !v.CanIterateElements() {
		return tags, true
	}
	raw := map[string]string{}
	for it := v.ElementIterator(); it.Next(); {
		k, val := it.Element()
		if k.Type() != cty.String || k.IsNull() || k.IsMarked() || val.IsMarked() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
			continue
		}
		raw[k.AsString()] = val.AsString()
	}
	return CanonicalARMMarkers(raw), true
}

// CanonicalARMMarkers is tags with every marker key in its canonical
// lowercase spelling ([CanonicalMarkerKey]) and every other key unchanged:
// the reading of an ARM tag map that [ARMTagsOf] gives an object, for a
// caller that already holds the map. When one marker appears under two
// spellings, which ARM itself refuses to store, the value under the
// lexically first spelling wins, so the answer does not depend on map
// order.
//
//markers:surface arm-tags
func CanonicalARMMarkers(tags map[string]string) map[string]string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(tags))
	for _, k := range keys {
		canon, marker := CanonicalMarkerKey(k)
		if !marker {
			out[k] = tags[k]
			continue
		}
		if _, seen := out[canon]; !seen {
			out[canon] = tags[k]
		}
	}
	return out
}

// ARMTagKeySpellings returns every key of keys that names marker (one of the
// canonical marker keys) case-insensitively, sorted, so the stamp can check
// a configuration's own Tofu-Estate against this run and then write under
// that spelling rather than add a second key ARM would refuse. Empty when
// the configuration sets no spelling of marker.
//
//markers:surface arm-tags
func ARMTagKeySpellings(keys []string, marker string) []string {
	var out []string
	for _, k := range keys {
		if strings.EqualFold(k, marker) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// SetARMMarkerTag sets the marker key (a canonical marker key) to value in
// an ARM tag map, under the spelling the map already uses for it when it
// has one, and removes every other spelling, so the map ends with exactly
// one key for the marker: ARM keeps a write's spelling and refuses two keys
// that differ only by case.
//
//markers:surface arm-tags
func SetARMMarkerTag(tags map[string]string, key, value string) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	spelling := key
	if existing := ARMTagKeySpellings(keys, key); len(existing) > 0 {
		spelling = existing[0]
		for _, k := range existing[1:] {
			delete(tags, k)
		}
	}
	tags[spelling] = value
}

// DeleteARMMarkerTag deletes every spelling of the marker key from an ARM
// tag map.
//
//markers:surface arm-tags
func DeleteARMMarkerTag(tags map[string]string, key string) {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	for _, k := range ARMTagKeySpellings(keys, key) {
		delete(tags, k)
	}
}

// ARMBodyArgument is azapi_resource's request body argument, which carries
// the object's tags under [ARMTagsArgument] when its configuration puts them
// there (GitHub issue #1120: azapi sends body.tags and ignores the top-level
// argument then, internal/services/azapi_resource.go's expandBody at 2.13.0).
const ARMBodyArgument = "body"

// WithARMBodyTags returns an azapi body with its tags key set to tags, as an
// object of strings, and every other key unchanged: the write a tags-only
// change to an existing object makes when its configuration keeps its tags
// in the body. ok is false when body is not a known, unmarked object or map
// a tags key can be set in.
//
//markers:surface arm-tags
func WithARMBodyTags(body cty.Value, tags map[string]string) (cty.Value, bool) {
	if body == cty.NilVal || body.IsNull() || !body.IsKnown() || body.IsMarked() {
		return cty.NilVal, false
	}
	if !body.Type().IsObjectType() && !body.Type().IsMapType() {
		return cty.NilVal, false
	}
	elems := map[string]cty.Value{}
	for it := body.ElementIterator(); it.Next(); {
		k, v := it.Element()
		elems[k.AsString()] = v
	}
	tagVals := make(map[string]cty.Value, len(tags))
	for k, v := range tags {
		tagVals[k] = cty.StringVal(v)
	}
	elems[ARMTagsArgument] = cty.ObjectVal(tagVals)
	return cty.ObjectVal(elems), true
}

// ARMBodyWithoutTags returns an azapi body with its tags key removed, so two
// bodies can be compared on everything a tags-only write must leave alone.
// Any value that is not a known, unmarked object or map comes back as it is.
//
//markers:surface arm-tags
func ARMBodyWithoutTags(body cty.Value) cty.Value {
	if body == cty.NilVal || body.IsNull() || !body.IsKnown() || body.IsMarked() {
		return body
	}
	if !body.Type().IsObjectType() && !body.Type().IsMapType() {
		return body
	}
	elems := map[string]cty.Value{}
	for it := body.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if k.AsString() == ARMTagsArgument {
			continue
		}
		elems[k.AsString()] = v
	}
	return cty.ObjectVal(elems)
}
