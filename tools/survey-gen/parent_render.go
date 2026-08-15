// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The parent-read split (issue #60): live/LIMITATIONS.md's "Untaggable
// types cannot be removed by the sweep" entry used to name one roster with
// one forwarding address for all of it. It splits in two here: the
// untaggable admitted types whose identity is composed from an admitted,
// taggable parent's own identity - swept by reading that parent instead of
// by a marker of their own - and the true residue that is neither. Both
// spans are derived the same way untaggable_render.go's own span is: from
// the committed live/survey-full.json (for which admitted types are
// taggable) and internal/live/identity's DefaultTable (for
// identity.ParentOf's Components-based derivation), never from a
// hand-maintained roster.
package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// The two rendered spans, siblings of spanUntaggableAdmitted
// (untaggable_render.go) in live/LIMITATIONS.md.
const (
	spanUntaggableParentRead = "untaggable-parent-read"
	spanUntaggableResidue    = "untaggable-residue"
)

// parentReadableType is one row of the "swept via parent read" table: an
// untaggable admitted type, the admitted taggable parent
// [identity.ParentOf] resolves it against, and whether this pass also
// trusts the read to remove the child rather than only report it
// ([identity.ParentReadRemovable]).
type parentReadableType struct {
	Type      string
	Parent    string
	Removable bool
}

// parentReadableRoster splits root's untaggable-admitted roster
// (untaggable_render.go's untaggableAdmittedTypes) into the types
// identity.ParentOf resolves against an eligible parent, and the residue it
// does not.
//
// The eligible-parent set is every admitted type live/survey-full.json
// positively records as taggable. That is the same fact
// internal/live/discovery computes live from the provider's own schema
// (markerCapable, in taggableAdmittedTypes) at run time, and the two are
// independent readings of "can this type carry an ownership marker", which
// is what the parent-read leg is a substitute for.
//
// Issue #130: this used to be derived subtractively, as every admitted type
// minus the untaggable ones, which is not the same set. A type the survey
// has no row for fell through the filter and became eligible, so
// null_resource - record-backed, no tags argument, no AWS existence at all -
// was published as the swept parent of seven types that discovery's own
// default-deny check would never have allowed. Both sides are positive
// tests now, which is the only way the "independent readings" claim above
// can hold.
func parentReadableRoster(root string) (readable []parentReadableType, residue []string, err error) {
	untaggable, taggable, err := untaggableAdmittedTypes(root)
	if err != nil {
		return nil, nil, err
	}

	eligibleParents := make(map[string]bool, len(taggable))
	for _, t := range taggable {
		eligibleParents[t] = true
	}

	// The CFN service each type belongs to, for rule 2's affinity test
	// (issue #129). The embedded roster carries live/mapping.json, which is
	// the only thing that knows aws_volume_attachment and aws_ebs_volume are
	// both AWS::EC2 despite sharing no Terraform prefix.
	roster, err := registry.Embedded()
	if err != nil {
		return nil, nil, fmt.Errorf("loading the embedded registry roster: %w", err)
	}
	serviceOf := identity.ServiceOf(roster.ServiceOf)

	for _, t := range untaggable {
		links := identity.ParentOf(t, eligibleParents, serviceOf)
		if len(links) == 0 {
			residue = append(residue, t)
			continue
		}
		// Several links (aws_route_table_association's subnet/gateway
		// alternatives) still name one type once - the first, sorted by
		// parent, is enough to say which parent the doc names, and does
		// not change whether the type is removable, which is keyed on the
		// type alone.
		readable = append(readable, parentReadableType{
			Type:      t,
			Parent:    links[0].Parent,
			Removable: identity.ParentReadRemovable(t),
		})
	}
	sort.Slice(readable, func(i, j int) bool { return readable[i].Type < readable[j].Type })
	sort.Strings(residue)
	return readable, residue, nil
}

// renderUntaggableParentRead renders the "swept via parent read" table: one
// row per type, its parent, and whether this pass also trusts it to remove
// what the read finds.
func renderUntaggableParentRead(readable []parentReadableType) string {
	var b strings.Builder
	b.WriteString("| Type | Parent | Removed by this leg |\n")
	b.WriteString("|---|---|---|\n")
	for _, r := range readable {
		removed := "no (report-only)"
		if r.Removable {
			removed = "yes"
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.Type, r.Parent, removed)
	}
	fmt.Fprintf(&b, "\n**Total.** %d types swept via a parent read.\n", len(readable))
	return b.String()
}

// renderUntaggableResidue renders the true residue: the untaggable admitted
// types neither taggable nor parent-readable, in the same backtick-quoted
// prose enumeration untaggable_render.go's renderUntaggableAdmitted uses.
func renderUntaggableResidue(types []string) string {
	return wrapIndented(joinWithAnd(backtickTypes(types), false), 76, "")
}
