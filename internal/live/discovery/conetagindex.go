// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"sort"

	"github.com/intentius/choudoufu/internal/live/registry"
)

// TaggingTypeFilters is the Resource Groups Tagging API resource-type
// filters for typeNames, for a tag index read for those types alone
// ([Request.TaggingTypeFilters], GitHub issue #2260). Each filter is the
// ARN service, or "service:resourcetype" when the type's ARN has a
// resource-type segment, read backwards out of [arnJoinTable]: the same
// table that joins a tagged ARN to its type joins a type to the ARNs the
// index should return. A segment two CFN types share (iam's "role" covers
// roles and service-linked roles) is one filter for both, and the join
// stays type-safe because a marker names its own type ([markerTypeOf]).
//
// unfiltered is every type of typeNames with no filter: no CFN type in the
// roster, or one the table does not join. The index cannot be narrowed to
// such a type, so a caller reading the index for the cone leaves it out
// rather than widen the call to the whole estate.
func TaggingTypeFilters(roster *registry.Roster, typeNames []string) (filters, unfiltered []string) {
	seen := map[string]bool{}
	for _, typeName := range typeNames {
		filter, ok := taggingTypeFilter(roster, typeName)
		if !ok {
			unfiltered = append(unfiltered, typeName)
			continue
		}
		if !seen[filter] {
			seen[filter] = true
			filters = append(filters, filter)
		}
	}
	sort.Strings(filters)
	sort.Strings(unfiltered)
	return filters, unfiltered
}

func taggingTypeFilter(roster *registry.Roster, typeName string) (string, bool) {
	cfnType, ok := arnJoinCFNType(roster, typeName)
	if !ok || !arnJoinCovers(cfnType) {
		return "", false
	}
	var found []string
	for service, byResourceType := range arnJoinTable {
		for segment, entry := range byResourceType {
			for _, covered := range entry.coverage {
				if covered != cfnType {
					continue
				}
				if segment == "" {
					found = append(found, service)
				} else {
					found = append(found, service+":"+segment)
				}
			}
		}
	}
	if len(found) != 1 {
		// No row, or two rows naming the same CFN type: either way there is
		// no one filter to send.
		return "", false
	}
	return found[0], true
}
