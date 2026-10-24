// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package substrate

import (
	"fmt"
	"strings"
)

// The data-plane URL ids (GitHub issue #2206, the maintainer's ruling of
// 2026-10-23 on #2201 item 5). Their host is the name segment of a parent's
// ARM id plus the cloud's DNS suffix for the service, and the suffix is a
// provider-block value like the subscription (internal/live/identity's
// AzureDNSSuffix). That is a component kind the ARM-path guards have no case
// for: neither "one argument whose value is the segment" nor "a whole ARM id
// at the start", but one segment out of an ARM id, then a suffix.
//
// A row that carries one is held to a grammar per service, which says which
// parent, which suffix and which host labels, and nothing else is admitted:
//
//	"https://" {name segment of an ARM id} "." [service "."] {suffix} {path}
//
// where {path} is literals and single arguments (or further segments of an
// ARM id argument, a storage container's name after its account's). The
// generator (tools/azurerm-id-gen dataplane.go) emits only rows it has read
// off azurerm's own id code; this is the independent check that what the
// artifact says has the shape the identity package renders.

// azurermDataPlaneFamily is one service's grammar.
type azurermDataPlaneFamily struct {
	// ParentAfter is the ARM segment that precedes the parent's name in the
	// id its argument holds.
	ParentAfter string
	// Labels are the host labels allowed between the name and the suffix,
	// each with its dots: "." for a service whose host is name.suffix,
	// ".blob." for a storage account's blob endpoint.
	Labels []string
}

// azurermDataPlaneFamilies is keyed by the identity package's CloudValue
// string for the suffix.
var azurermDataPlaneFamilies = map[string]azurermDataPlaneFamily{
	"key-vault-dns-suffix":         {ParentAfter: "vaults", Labels: []string{"."}},
	"managed-hsm-dns-suffix":       {ParentAfter: "managedHSMs", Labels: []string{"."}},
	"app-configuration-dns-suffix": {ParentAfter: "configurationStores", Labels: []string{"."}},
	"storage-dns-suffix":           {ParentAfter: "storageAccounts", Labels: []string{".blob.", ".dfs.", ".file.", ".queue.", ".table."}},
}

func azurermDNSSuffixCloud(cloud string) bool {
	_, ok := azurermDataPlaneFamilies[cloud]
	return ok
}

// azurermDataPlaneProblem is the grammar check, or why the row fails it.
func azurermDataPlaneProblem(t azurermIDType) string {
	comps := t.Components
	if len(comps) < 5 || comps[0].Literal != "https://" || len(comps[0].Attrs) > 0 || comps[0].Cloud != "" {
		return "its data-plane id does not begin with https://"
	}
	name := comps[1]
	if len(name.Attrs) != 1 || name.ARMSegment == nil || name.Cloud != "" || name.ARMID {
		return fmt.Sprintf("its data-plane host {%s} is not one segment of the ARM id one argument holds", name.Segment)
	}
	sep, suffix := comps[2], comps[3]
	fam, ok := azurermDataPlaneFamilies[suffix.Cloud]
	if !ok || len(suffix.Attrs) > 0 || suffix.Literal != "" {
		return "its data-plane host is not followed by a cloud DNS suffix"
	}
	if name.ARMSegment.After != fam.ParentAfter || name.ARMSegment.Index <= 0 {
		return fmt.Sprintf("its data-plane host takes the segment after %q of an ARM id, and the %s service's parent is named after %q", name.ARMSegment.After, suffix.Cloud, fam.ParentAfter)
	}
	allowed := false
	for _, l := range fam.Labels {
		if sep.Literal == l && len(sep.Attrs) == 0 && sep.Cloud == "" {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Sprintf("its data-plane host has %q between the name and the %s, which is not a host label that service uses", sep.Literal, suffix.Cloud)
	}
	arg := name.Attrs[0]
	for _, c := range comps[4:] {
		if c.URLEscape != "" && (len(c.Attrs) != 1 || (c.URLEscape != "path" && c.URLEscape != "query")) {
			return fmt.Sprintf("segment {%s} is escaped as %q, which is not a path or query escape of one argument", c.Segment, c.URLEscape)
		}
		if c.OmitIfAbsent && len(c.Attrs) != 1 {
			return fmt.Sprintf("segment {%s} is omitted when absent but reads no single argument", c.Segment)
		}
		switch {
		case c.Cloud != "" || c.ARMID:
			return fmt.Sprintf("segment {%s} after the host is a cloud value or a whole ARM id, which a data-plane path has no place for", c.Segment)
		case c.ARMSegment != nil:
			if len(c.Attrs) != 1 || c.Attrs[0] != arg || c.ARMSegment.Index <= name.ARMSegment.Index {
				return fmt.Sprintf("segment {%s} is not a later segment of the ARM id %s holds", c.Segment, arg)
			}
		case len(c.Attrs) > 1:
			return fmt.Sprintf("segment {%s} reads whichever of %s is set, so which argument names the object is a choice this table would be guessing", c.Segment, strings.Join(c.Attrs, ", "))
		case len(c.Attrs) == 0 && c.Literal == "":
			return fmt.Sprintf("segment {%s} names no argument and no literal", c.Segment)
		}
	}
	return ""
}
