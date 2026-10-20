// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the node stamp's arm for [markers.SurfaceARMTags] (GitHub
// issue #1120, ruled on #1122 2026-10-19): hashicorp/azurerm's tags
// argument and azure/azapi's.
//
// # azurerm
//
// An azurerm type is stamped exactly as an AWS type is ([NodeResolver.stampedTags]):
// before #1120 azurerm had no family and reached the AWS answer through
// substrate.SurfaceOf's compatible default, and the keys, values and
// conflict rules are unchanged. The one difference is case: ARM tag keys
// are case-insensitive, so a configuration's own Tofu-Estate is the
// tofu-estate marker, is checked against this run as one, and when it
// agrees is kept under its own spelling rather than joined by a second,
// lowercase key ARM would refuse ([NodeResolver.stampMarkerElems]).
//
// # azapi_resource: one surface, two write paths
//
// azapi_resource has a top-level tags argument, and its body argument (the
// ARM request body, dynamic) may carry tags too. azapi 2.13.0 refuses a
// configuration that sets both (validateDuplicatedDefinitions,
// internal/services/azapi_resource.go), and where body carries tags it
// sends those and ignores the top-level argument (line 1722). So the stamp
// writes where the configuration already put its tags: body.tags when body
// has a tags key, the top-level argument otherwise ([armTagTarget]).
//
// That choice is made here, per configuration, and not by a second marker
// surface. A surface is answered from the schema alone (substrate.SurfaceOf
// takes a block, never a value), and the schema is the same for both
// paths; the read side is the same too, since azapi copies the response
// body's tags back into the top-level attribute (markers.ARMTagsOf). What
// differs is one write path, so it is one named function the completeness
// guard sees, not a surface every seam would have to answer twice.
//
// # Which azapi types take tags
//
// Whether an ARM resource takes tags is a property of its type and API
// version, and azapi's own type data says which ([ARMTypeTaggable]). The
// stamp never writes a marker an ARM type cannot hold: a type the table
// says is untaggable, or one it does not know, is left unstamped and the
// plan says so.

// ARMTypeTaggable reports whether the ARM resource type armType (say
// "Microsoft.Network/virtualNetworks") takes tags at apiVersion, the
// version the block's own type argument names after "@", and whether the
// answer is known at all.
//
// It is a seam: the table behind it is generated from the type data azapi
// embeds (internal/azure/generated, bicep-types format: taggable when the
// body type has a non-ReadOnly tags property) by the taggability unit of
// GitHub issue #1120, as internal/live/armtypes.Taggable, which assigns
// itself here. Until it does, every answer is unknown, and an azapi
// resource is never stamped: a marker an ARM type cannot hold is a wrong
// marker, and the safety rule is never to write one.
var ARMTypeTaggable = func(armType, apiVersion string) (taggable, known bool) {
	return false, false
}

// The three summaries this arm raises. They are registered in refusals.go.
const (
	// SummaryARMTagsBodyUnknown is the refusal for an azapi resource whose
	// body is not known at plan time, so the stamp cannot tell whether
	// the configuration carries its tags in body.tags or in the top-level
	// tags argument, and will not guess.
	SummaryARMTagsBodyUnknown = "Cannot tell where an azapi resource carries its tags"

	// SummaryARMTypeNotStamped is the warning for an azapi resource whose
	// ARM type and API version are not known to take tags.
	SummaryARMTypeNotStamped = "Ownership markers not set on an azapi resource type not known to take tags"

	// SummaryARMBodyNotMergeable is the warning for an azapi body, or a
	// body.tags, the stamp cannot add a marker into.
	SummaryARMBodyNotMergeable = "Cannot merge ownership markers into this azapi body value"
)

// armTagsAt is where [armTagTarget] decided the marker goes.
type armTagsAt int

const (
	armTagsNowhere armTagsAt = iota
	armTagsTopLevel
	armTagsInBody
)

// azapiBodyName and azapiTypeName are the two azapi_resource arguments the
// stamp reads.
const (
	azapiBodyName = "body"
	azapiTypeName = "type"
)

// azapiShaped reports whether block is an ARM resource declared by type and
// body, azapi_resource's shape: a settable string type argument naming
// "<ARM type>@<API version>" and a dynamic body argument. Read from the
// schema, never a type name; an azurerm type has neither.
func azapiShaped(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	typ, hasType := block.Attributes[azapiTypeName]
	body, hasBody := block.Attributes[azapiBodyName]
	return hasType && typ != nil && typ.Type == cty.String && (typ.Required || typ.Optional) &&
		hasBody && body != nil && body.Type == cty.DynamicPseudoType && (body.Required || body.Optional)
}

// splitARMType splits azapi's "<ARM type>@<API version>".
func splitARMType(s string) (armType, apiVersion string, ok bool) {
	armType, apiVersion, ok = strings.Cut(s, "@")
	return armType, apiVersion, ok && armType != "" && apiVersion != ""
}

// armTagTarget decides where an ARM marker is written for config: the
// top-level tags argument for an azurerm type, and for an azapi-shaped one
// ([azapiShaped]) the top-level argument or body.tags, whichever the
// configuration itself uses, or nowhere when its ARM type is not known to
// take tags. It is a function of its own so the decision has one name.
func armTagTarget(addr addrs.AbsResourceInstance, block *configschema.Block, config cty.Value) (armTagsAt, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	if !azapiShaped(block) {
		return armTagsTopLevel, diags
	}

	// A config value carrying only some of the schema's attributes is
	// internal/live/check's offline conflict check, which hands the stamp
	// the tags argument alone: no type there reads as an unknown one.
	typeVal := cty.NullVal(cty.String)
	if config.Type().HasAttribute(azapiTypeName) {
		typeVal = config.GetAttr(azapiTypeName)
	}
	if typeVal.IsMarked() || typeVal.IsNull() || !typeVal.IsKnown() {
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMTypeNotStamped, fmt.Sprintf(
			"%s's type argument is not a known value at plan time, so this run cannot tell whether its ARM resource type takes tags and has written no ownership marker on it. The next plan reads it as carrying none.", addr)))
	}
	armType, apiVersion, ok := splitARMType(typeVal.AsString())
	if !ok {
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMTypeNotStamped, fmt.Sprintf(
			"%s's type argument %q does not name an ARM resource type and API version as <type>@<api-version>, so no ownership marker was written on it.", addr, typeVal.AsString())))
	}
	taggable, known := ARMTypeTaggable(armType, apiVersion)
	switch {
	case !known:
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMTypeNotStamped, fmt.Sprintf(
			"This build does not know whether %s at API version %s takes tags, so %s carries no ownership marker: a marker an ARM type cannot hold would be refused by Azure, or written where nothing reads it. The next plan reads it as carrying none.", armType, apiVersion, addr)))
	case !taggable:
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMTypeNotStamped, fmt.Sprintf(
			"%s at API version %s takes no tags, so %s carries no ownership marker. The next plan reads it as carrying none.", armType, apiVersion, addr)))
	}

	body := cty.NullVal(cty.DynamicPseudoType)
	if config.Type().HasAttribute(azapiBodyName) {
		body, _ = config.GetAttr(azapiBodyName).Unmark()
	}
	switch {
	case !body.IsKnown():
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryARMTagsBodyUnknown, fmt.Sprintf(
			"%s's body is not known at plan time, so this run cannot tell whether the configuration carries its tags in body.tags or in the top-level tags argument. azapi refuses a configuration that sets both, and a marker written to the one it ignores is never stored, so this run will not guess. Make body a known object at plan time (its values may still be unknown), or apply what it depends on first.", addr)))
	case body.IsNull():
		return armTagsTopLevel, diags
	case body.Type().IsObjectType():
		if body.Type().HasAttribute(markers.ARMTagsArgument) {
			return armTagsInBody, diags
		}
		return armTagsTopLevel, diags
	case body.Type().IsMapType():
		if body.LengthInt() > 0 && body.HasIndex(cty.StringVal(markers.ARMTagsArgument)).True() {
			return armTagsInBody, diags
		}
		return armTagsTopLevel, diags
	}
	return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMBodyNotMergeable, fmt.Sprintf(
		"%s's body evaluated to a %s, not an object, so this run cannot tell where its tags go and has written no ownership marker on it.", addr, body.Type().FriendlyName())))
}

// stampedARMTags returns the argument to replace and its stamped value for
// an instance on the ARM surface, or stamped false when nothing is written
// (an azapi type not known to take tags, or a value the stamp cannot merge
// into, each with its own diagnostic).
func (n *NodeResolver) stampedARMTags(addr addrs.AbsResourceInstance, block *configschema.Block, config cty.Value) (attr string, val cty.Value, stamped bool, diags tfdiags.Diagnostics) {
	address := markers.EscapeAddress(addr.String())
	at, diags := armTagTarget(addr, block, config)
	if diags.HasErrors() {
		return "", cty.NilVal, false, diags
	}
	switch at {
	case armTagsTopLevel:
		newTags, tagDiags := n.stampedTagMap(addr, config.GetAttr(markers.ARMTagsArgument), address, true)
		diags = diags.Append(tagDiags)
		if tagDiags.HasErrors() {
			return "", cty.NilVal, false, diags
		}
		return markers.ARMTagsArgument, newTags, true, diags
	case armTagsInBody:
		newBody, ok, bodyDiags := n.stampedBodyTags(addr, config.GetAttr(azapiBodyName), address)
		diags = diags.Append(bodyDiags)
		if !ok || bodyDiags.HasErrors() {
			return "", cty.NilVal, false, diags
		}
		return azapiBodyName, newBody, true, diags
	}
	return "", cty.NilVal, false, diags
}

// stampedBodyTags returns body with the markers added to its tags key,
// which [armTagTarget] found present. body is dynamic: an HCL object
// constructor is an object, and its tags an object of strings, so the
// result is rebuilt as objects (a map body would have to keep every element
// one type, which a stamped tags value need not share). The outer marks of
// body and of its tags are kept, as [NodeResolver.stampedTags] keeps the
// tags map's.
func (n *NodeResolver) stampedBodyTags(addr addrs.AbsResourceInstance, body cty.Value, address string) (cty.Value, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	body, bodyMarks := body.Unmark()

	bodyElems := map[string]cty.Value{}
	for it := body.ElementIterator(); it.Next(); {
		k, v := it.Element()
		bodyElems[k.AsString()] = v
	}
	tagsVal, tagsMarks := bodyElems[markers.ARMTagsArgument].Unmark()

	elems := map[string]cty.Value{}
	switch {
	case tagsVal.IsNull():
	case !tagsVal.IsKnown():
		return cty.NilVal, false, diags.Append(tfdiags.Sourceless(tfdiags.Warning, "Cannot set ownership markers on an unresolved tags value",
			fmt.Sprintf("%s's body.tags is not yet known, so its ownership markers could not be set at the node; the configuration's own value is used unchanged.", addr)))
	case tagsVal.Type().IsObjectType() || tagsVal.Type().IsMapType():
		for it := tagsVal.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if v.Type() != cty.String && v.Type() != cty.DynamicPseudoType {
				return cty.NilVal, false, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMBodyNotMergeable,
					fmt.Sprintf("%s's body.tags holds a %s at key %q, not a string; the configuration's own value is used unchanged and no ownership marker was written.", addr, v.Type().FriendlyName(), k.AsString())))
			}
			elems[k.AsString()] = v
		}
	default:
		return cty.NilVal, false, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMBodyNotMergeable,
			fmt.Sprintf("%s's body.tags evaluated to a %s, not an object of strings; the configuration's own value is used unchanged and no ownership marker was written.", addr, tagsVal.Type().FriendlyName())))
	}

	diags = diags.Append(n.stampMarkerElems(addr, elems, address, true))
	if diags.HasErrors() {
		return cty.NilVal, false, diags
	}
	bodyElems[markers.ARMTagsArgument] = cty.ObjectVal(elems).WithMarks(tagsMarks)
	return cty.ObjectVal(bodyElems).WithMarks(bodyMarks), true, diags
}
