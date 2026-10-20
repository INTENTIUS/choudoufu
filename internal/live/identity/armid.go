// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
)

// This file is the Azure half of identity resolution that a concatenation
// of components cannot say on its own (GitHub issue #1120, ruled on #1122):
// how azapi_resource's `type` argument joins its `parent_id` to its `name`,
// and how two ARM ids are compared.
//
// # azapi_resource's id, from azapi's own builder
//
// azapi 2.13.0 builds the id of the object an azapi_resource creates in
// internal/services/parse/resource.go's newResourceID (lines 79-149),
// called from CreateUpdate (internal/services/azapi_resource.go line 844)
// with the planned name, parent_id and type. The type is first split at its
// one "@" (utils/helper.go lines 113-124, GetAzureResourceTypeApiVersion;
// a type without exactly one "@" is an error), and the API version takes no
// part in the id. Then, on the ARM type alone:
//
//   - no "/" (a resource provider namespace, "Microsoft.Network"):
//     scope + "/providers/" + name (lines 91-98).
//   - two segments, a top-level type (utils/helper.go line 126-128,
//     IsTopLevelResourceType), switched on the exact strings of azcore's
//     arm.*ResourceType (azcore arm/internal/resource/resource_type.go
//     lines 12-21, namespace "Microsoft.Resources"):
//     "Microsoft.Resources/resourceGroups": parent_id + "/resourceGroups/"
//     + name (line 110); "Microsoft.Resources/subscriptions":
//     "/subscriptions/" + name, parent_id unused (line 112);
//     "Microsoft.Resources/tenants": "/", no name at all (line 114);
//     "Microsoft.Resources/providers": scope + "/providers/" + name (lines
//     115-121); any other: scope + "/providers/" + type + "/" + name
//     (lines 122-128). That last is also an extension resource: parent_id
//     is then the id of the resource the extension is added to, and the
//     id is that resource's id with /providers/<type>/<name> appended.
//   - three or more segments, a child type: parent_id + "/" + the last
//     type segment + "/" + name (lines 130-138).
//
// "scope" is parent_id, except that a parent_id of exactly "/" (the tenant
// root) is the empty string there, "avoid duplicated `/`" (lines 94-97,
// 117-120, 123-126); the resource-group and child branches do not apply
// that rule.
//
// The switch on the special types is case-sensitive, exactly as azapi's Go
// switch is: a type spelled "microsoft.resources/resourcegroups" takes the
// default branch in azapi, so it does here, because the id this package
// renders has to be the one azapi will actually write.

// armParentUse says what the segment rule does with the parent_id that
// precedes it.
type armParentUse int

const (
	// armParentKept: parent_id is the id's prefix as written.
	armParentKept armParentUse = iota
	// armParentRootDropped: parent_id is the prefix, except that the tenant
	// root "/" contributes nothing.
	armParentRootDropped
	// armParentDropped: parent_id takes no part in the id.
	armParentDropped
)

// armTypeSegment is the text azapi puts between parent_id and name for one
// `type` value, what it does with parent_id, and, when the type cannot
// identify an object at all, why. See this file's comment for the rule and
// its source lines.
func armTypeSegment(typ string) (segment string, parent armParentUse, problem string) {
	halves := strings.Split(typ, "@")
	if len(halves) != 2 {
		return "", 0, fmt.Sprintf("the type %q is not of the form <resource type>@<api version>, which azapi requires", typ)
	}
	armType := halves[0]
	segs := strings.Split(armType, "/")
	switch {
	case !strings.Contains(armType, "/"):
		return "/providers/", armParentRootDropped, ""
	case len(segs) == 2:
		switch armType {
		case "Microsoft.Resources/resourceGroups":
			return "/resourceGroups/", armParentKept, ""
		case "Microsoft.Resources/subscriptions":
			return "/subscriptions/", armParentDropped, ""
		case "Microsoft.Resources/tenants":
			return "", 0, "azapi gives a Microsoft.Resources/tenants object the id \"/\", which carries no name and is not an object this estate can own"
		case "Microsoft.Resources/providers":
			return "/providers/", armParentRootDropped, ""
		}
		return "/providers/" + armType + "/", armParentRootDropped, ""
	}
	return "/" + segs[len(segs)-1] + "/", armParentKept, ""
}

// armCollisionKey is what [resolver.checkCollisions] compares an ARM id by
// (GitHub issue #1120): the id with every literal letter folded to lower
// case, since ARM resource ids are case-insensitive (Azure Resource
// Manager's resource naming rules: "Resource and resource group names are
// case-insensitive"; azapi itself compares types with strings.EqualFold,
// internal/services/parse/resource.go lines 159-166 and 283). A reference
// to a parent's live id is kept as the reference it is: two different
// configuration addresses are two different parents, and folding them would
// merge addresses OpenTofu keeps apart.
func armCollisionKey(res Resolution) string {
	if res.Class == ClassConcrete {
		return strings.ToLower(res.ImportID)
	}
	if res.Formula == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range res.Formula.Parts {
		if p.Parent == nil {
			b.WriteString(strings.ToLower(p.Literal))
			continue
		}
		b.WriteString("${")
		b.WriteString(p.Parent.String())
		b.WriteString("}")
	}
	return b.String()
}

// armTypeSegmentParts is [resolver.resolveInstance]'s [Component.ARMTypeSegment]
// step: given the parts the `type` argument resolved to and the parts the
// parent_id component before it contributed, the parts that replace both.
// It refuses, naming the instance, when the type is not a literal this run
// can read (the segment depends on the value's shape, so a promise to read
// it later cannot be rendered) or is one azapi gives no nameable id.
func (r *resolver) armTypeSegmentParts(typeParts, parentParts []Part, attr *hcl.Attribute, addr addrs.AbsResourceInstance) ([]Part, bool) {
	typeParts = coalesce(typeParts)
	if len(typeParts) != 1 || typeParts[0].Parent != nil {
		r.errorf(attr.Range, "Identity not resolvable from configuration",
			"%s reads %s for its identity, and the ARM id azapi builds depends on how many segments that resource type has, so its value has to be known from this configuration alone; it is not.",
			addr.String(), attr.Name)
		return nil, false
	}
	seg, use, problem := armTypeSegment(typeParts[0].Literal)
	if problem != "" {
		r.errorf(attr.Range, "Identity not resolvable from configuration",
			"%s cannot be identified: %s.", addr.String(), problem)
		return nil, false
	}
	var out []Part
	switch use {
	case armParentKept:
		out = append(out, parentParts...)
	case armParentRootDropped:
		if p := coalesce(parentParts); !(len(p) == 1 && p[0].Parent == nil && p[0].Literal == "/") {
			out = append(out, parentParts...)
		}
	case armParentDropped:
	}
	return append(out, Part{Literal: seg}), true
}
