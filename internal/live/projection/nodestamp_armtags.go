// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
	"github.com/intentius/choudoufu/internal/live/identity"
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
// paths; the read side is the same too, since azapi puts body.tags into
// the top-level attribute on every path that writes state, so the marker
// reader reads top-level tags in both cases (markers.ARMTagsOf). GitHub
// issue #2133, finding 5 checked each path in azapi 2.13.0's
// internal/services/azapi_resource.go:
//
//   - plan: ModifyPlan sets plan.Tags from tagsWithDefaultTags (line 724),
//     which takes the top-level argument when set and otherwise body.tags
//     (lines 1555-1568, its steps 1 and 2);
//   - create and update: CreateUpdate writes that plan into state
//     (responseState.Set(ctx, plan), line 1103; Create at 802-809 and
//     Update at 811-833 both go through it), so state's top-level tags
//     after an apply are the planned body.tags, marker included;
//   - read (refresh): the response body's tags replace state's top-level
//     tags whenever either is non-empty (lines 1182-1184);
//   - import: the response body's tags become top-level tags when
//     non-empty (lines 1419-1421).
//
// So body.tags needs no read-back path of its own.
//
// # azapi's default_tags
//
// azapi's provider block takes default_tags, and azapi never merges them:
// tagsWithDefaultTags (internal/services/azapi_resource.go lines
// 1555-1590 at 2.13.0) plans the resource's own top-level tags when they
// are not null (step 1), body.tags when the body carries them (step 2),
// and only then, for a type whose body takes tags by
// canResourceHaveProperty (internal/services/resource.go lines 135-149),
// the provider's default_tags (step 3). A marker written into a null
// top-level tags would make step 1 win and drop the default tags stock
// sends, so the stamp writes default_tags merged with the markers there
// instead ([NodeResolver.azapiDefaultTags], GitHub issue #2147), the way
// AWS's provider merges its own default_tags into tags_all and GCP's
// merges default_labels: the plan is stock's plan plus the marker. Tags the
// configuration sets, at top level or in body, already beat default_tags
// in azapi, so those are stamped as they always were. hashicorp/azurerm has
// no default_tags (its 5.10.0 provider schema has no such argument), so
// this is azapi's alone.
// What differs is one
// write path, so it is one named function the completeness guard sees,
// not a surface every seam would have to answer twice.
//
// # Which azapi types take tags
//
// Whether an ARM resource takes tags is a property of its type and API
// version, and azapi's own type data says which ([armtypes.Taggable], a
// table generated from the type data azapi embeds, keyed by type and API
// version). The
// stamp never writes a marker an ARM type cannot hold: a type the table
// says is untaggable, or one it does not know, is left unstamped and the
// plan says so.

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

	// SummaryARMDefaultTagsUnknown is the warning for an azapi resource
	// that sets no tags of its own under a provider block whose
	// default_tags this run cannot read before it starts, so the stamp
	// cannot write the merge azapi would have sent and writes nothing.
	SummaryARMDefaultTagsUnknown = "Ownership markers not set on an azapi resource whose provider default_tags are not known"

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
	armType, apiVersion, ok := armtypes.ParseAzapiType(typeVal.AsString())
	if !ok {
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMTypeNotStamped, fmt.Sprintf(
			"%s's type argument %q does not name an ARM resource type and API version as <type>@<api-version>, so no ownership marker was written on it.", addr, typeVal.AsString())))
	}
	// The block's own API version, never the latest: taggability is per
	// type and version (84 types differ between versions at azapi 2.13.0).
	taggable, known := armtypes.Taggable(armType, apiVersion)
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
		body = config.GetAttr(azapiBodyName)
	}
	if body.IsMarked() {
		// Refused rather than unmarked (internal/live/marksafe): which
		// keys a sensitive body holds is not this pass's to read.
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMBodyNotMergeable, fmt.Sprintf(
			"%s's body is marked as a whole, which this pass will not unmark to see whether it carries tags, so no ownership marker was written on it.", addr)))
	}
	switch {
	case !body.IsKnown() && armTopLevelTagsSet(config):
		// The body is unknown, but the configuration sets the top-level
		// tags argument to a known, non-null value, and that settles where
		// azapi sends tags (GitHub issue #2142's audit of Azure refusals):
		// azapi 2.13.0 plans the top-level argument whenever it is not null
		// (tagsWithDefaultTags step 1, internal/services/azapi_resource.go
		// lines 1555-1559), sends it whenever body has no tags (expandBody,
		// line 1722), and refuses a known body that also carries tags
		// (validateDuplicatedDefinitions, lines 1738-1747, which ModifyPlan
		// runs again once the body is known). So either the marker is sent
		// from the top-level argument, or azapi refuses the configuration
		// itself; it is never written to an argument azapi ignores.
		return armTagsTopLevel, diags
	case !body.IsKnown():
		return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryARMTagsBodyUnknown, fmt.Sprintf(
			"%s's body is not known at plan time, so this run cannot tell whether the configuration carries its tags in body.tags or in the top-level tags argument. azapi refuses a configuration that sets both, and a marker written to the one it ignores is never stored, so this run will not guess. Make body a known object at plan time (its values may still be unknown), set the top-level tags argument to a known value (azapi then sends it, and refuses a body that also carries tags), or apply what it depends on first.", addr)))
	case body.IsNull():
		return armTagsTopLevel, diags
	case body.Type().IsObjectType():
		if body.Type().HasAttribute(markers.ARMTagsArgument) {
			return armTagsInBody, diags
		}
		return armTagsTopLevel, diags
	case body.Type().IsMapType():
		if _, has := body.AsValueMap()[markers.ARMTagsArgument]; has {
			return armTagsInBody, diags
		}
		return armTagsTopLevel, diags
	}
	return armTagsNowhere, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMBodyNotMergeable, fmt.Sprintf(
		"%s's body evaluated to a %s, not an object, so this run cannot tell where its tags go and has written no ownership marker on it.", addr, body.Type().FriendlyName())))
}

// armTopLevelTagsSet reports whether config sets azapi_resource's top-level
// tags argument to a known, non-null value. An unknown value does not
// count: it may still turn out null at apply, and then azapi would send
// body.tags instead.
func armTopLevelTagsSet(config cty.Value) bool {
	if !config.Type().IsObjectType() || !config.Type().HasAttribute(markers.ARMTagsArgument) {
		return false
	}
	tags := config.GetAttr(markers.ARMTagsArgument)
	return !tags.IsMarked() && tags.IsKnown() && !tags.IsNull()
}

// ARMTagPlacement is [armTagTarget] for a write onto an object that already
// exists, outside the node stamp: live-import's adopt write and live-mv's
// rewrite (GitHub issue #1120 unit 19), handed the object as the provider
// read it. inBody says the marker goes in body.tags, where the object's own
// configuration keeps its tags; otherwise in the top-level tags argument.
// ok false says nowhere, and why is the sentence the stamp would have
// warned with: an azapi ARM type that takes no tags at its API version, one
// this build does not know, or a body no marker can be merged into. Both
// writers therefore decline exactly the objects a plan declines to stamp.
func ARMTagPlacement(addr addrs.AbsResourceInstance, block *configschema.Block, obj cty.Value) (inBody, ok bool, why string) {
	at, diags := armTagTarget(addr, block, obj)
	switch {
	case at == armTagsInBody:
		return true, true, ""
	case at == armTagsTopLevel:
		return false, true, ""
	case len(diags) > 0:
		return false, false, diags[0].Description().Detail
	}
	return false, false, fmt.Sprintf("%s has nowhere to carry an ownership marker.", addr)
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
		tags, ok, defDiags := n.azapiDefaultTags(addr, block, config)
		diags = diags.Append(defDiags)
		if !ok {
			return "", cty.NilVal, false, diags
		}
		newTags, tagDiags := n.stampedTagMap(addr, tags, address, true)
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

// azapiDefaultTags is the tags value the markers are merged into when
// [armTagTarget] chose the top-level argument: the configuration's own,
// except for an azapi resource that leaves it null under a provider block
// that sets default_tags, where it is those default tags, because that is
// what azapi would have sent (this file's doc, "azapi's default_tags").
// Marker keys among the default tags are dropped, compared
// case-insensitively, so the markers win on their own keys and no second
// spelling of one reaches ARM; every other key is kept as written.
//
// ok false means no marker is written, with a warning: the provider block
// sets default_tags to something this run cannot read before it starts, or
// to a value that is not a map of strings. A marker alone there would
// replace the default tags stock sends.
func (n *NodeResolver) azapiDefaultTags(addr addrs.AbsResourceInstance, block *configschema.Block, config cty.Value) (cty.Value, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	tags := config.GetAttr(markers.ARMTagsArgument)
	if !azapiShaped(block) || !tags.IsNull() {
		return tags, true, diags
	}
	notKnown := func(why string) (cty.Value, bool, tfdiags.Diagnostics) {
		return cty.NilVal, false, diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryARMDefaultTagsUnknown, fmt.Sprintf(
			"%s sets no tags of its own, so azapi sends its provider block's default_tags, and %s. A marker written alone would replace those default tags, so this run has written no ownership marker on it. Set the provider's default_tags from values known before the run (literals, locals, or variables given -var or a tfvars file), or set the resource's own tags argument. The next plan reads it as carrying none.", addr, why)))
	}

	// armTagTarget reached the top level only with a known type that
	// parses, so this is the same type and version it checked. A marked
	// type is refused rather than unmarked: no marker is written.
	typ := config.GetAttr(azapiTypeName)
	if typ.IsMarked() {
		return notKnown("its type argument is sensitive, so this run cannot tell whether that type takes them")
	}
	armType, apiVersion, _ := armtypes.ParseAzapiType(typ.AsString())
	if applies, _ := armtypes.DefaultTagsApply(armType, apiVersion); !applies {
		return tags, true, diags
	}
	val, set, ok := identity.ProviderStaticArgument(context.TODO(), n.Config, addr, "default_tags")
	switch {
	case !ok:
		return notKnown("this run cannot read that provider configuration's default_tags before it starts")
	case !set || val.IsNull():
		return tags, true, diags
	}
	m, err := convert.Convert(val, cty.Map(cty.String))
	if err != nil || m.IsNull() || !m.IsWhollyKnown() {
		return notKnown("its default_tags is not a map of strings")
	}
	if m.ContainsMarked() {
		return notKnown("its default_tags holds a sensitive value, which must not become a cloud tag")
	}

	elems := map[string]cty.Value{}
	for it := m.ElementIterator(); it.Next(); {
		k, v := it.Element()
		if v.IsNull() || isMarkerKeyFolded(k.AsString()) {
			continue
		}
		elems[k.AsString()] = v
	}
	if len(elems) == 0 {
		// azapi skips step 3 on an empty default_tags (len == 0), and
		// default tags that were all marker keys leave nothing to keep.
		return tags, true, diags
	}
	return cty.MapVal(elems), true, diags
}

// isMarkerKeyFolded reports whether key is a marker key in any spelling:
// tofu-estate, tofu-slot, tofu-address or one of its continuations.
func isMarkerKeyFolded(key string) bool {
	if strings.EqualFold(key, markers.TagEstate) || strings.EqualFold(key, markers.TagSlot) {
		return true
	}
	for i := 0; i < markers.MaxContinuations; i++ {
		if strings.EqualFold(key, markers.AddressTagKey(i)) {
			return true
		}
	}
	return false
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

	diags = diags.Append(n.stampMarkerElems(addr, elems, address, address, true))
	if diags.HasErrors() {
		return cty.NilVal, false, diags
	}
	bodyElems[markers.ARMTagsArgument] = cty.ObjectVal(elems).WithMarks(tagsMarks)
	return cty.ObjectVal(bodyElems).WithMarks(bodyMarks), true, diags
}
