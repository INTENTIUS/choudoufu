// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// overrideAdmissionRoute names the identity-layer route that admits a type
// estate-gen holds an override for, or "" when no route does.
//
// #809's ruling made internal/live/identity's table the master for
// admission. Lint now derives its list from that table rather than holding a
// generated copy, and estate-gen picks a cohort's types from it
// (defaultCohortTypes, through identity.AdmittedTypes). What stayed
// hand-written here is typeOverrides: per-type fixture knowledge (the
// `terraform validate` error each entry fixes), which no generator can
// derive and which is not an admission fact. The one way it can still
// disagree with the master is by keying an override on a type the identity
// layer does not admit by any route, so this function reads both routes off
// the identity package instead of keeping a list:
//
//   - "table": a ratified row in [identity.DefaultTable].
//   - "record-located": [identity.MarkerlessTypes], which is the record rung's
//     population (identity.LocatedType's condition 1). Eight overrides sit
//     here today, all types row-gen's markerless veto retracted from the
//     table after their cohort's override was written. They still render
//     under an explicit -types call, onto the record rung.
func overrideAdmissionRoute(typeName string) string {
	if _, ok := identity.LookupType(typeName); ok {
		return "table"
	}
	if _, ok := identity.MarkerlessTypes[typeName]; ok {
		return "record-located"
	}
	return ""
}

// TestEveryOverrideNamesATypeTheIdentityLayerAdmits is #809's guard on the
// third corner of the triad. An override keyed on a type the identity table
// has dropped, or never held, is fixture knowledge for a type no cohort can
// be generated with, and for as long as it exists it suggests that type is
// supported. Fixing it means deleting the override or ratifying the type;
// both are edits to the master's inputs, not to this file.
func TestEveryOverrideNamesATypeTheIdentityLayerAdmits(t *testing.T) {
	if len(typeOverrides) == 0 {
		t.Fatal("typeOverrides is empty; the cohort init()s did not register, so this guard read nothing")
	}
	var orphans []string
	routes := map[string]int{}
	for typeName, o := range typeOverrides {
		route := overrideAdmissionRoute(typeName)
		if route == "" {
			orphans = append(orphans, typeName)
			continue
		}
		routes[route]++
		for _, sup := range o.NeedsSupporting {
			if _, ok := identity.LookupType(sup); !ok {
				t.Errorf("the %s override names supporting type %s, which identity.DefaultTable does not admit; "+
					"planCohort renders a supporting row as an ordinary admitted resource", typeName, sup)
			}
		}
	}
	sort.Strings(orphans)
	for _, typeName := range orphans {
		t.Errorf("tools/estate-gen holds an override for %s, which internal/live/identity admits by no route "+
			"(no DefaultTable row, not in MarkerlessTypes). The identity table is the master for admission (#809): "+
			"delete the override, or ratify the type in tools/row-gen/ratified.json and re-run -emit.", typeName)
	}
	// Both routes are populated today. One reading zero means the predicate
	// above stopped matching, not that the tree got cleaner.
	if routes["table"] == 0 || routes["record-located"] == 0 {
		t.Errorf("override routes = %v; expected both table and record-located to be populated", routes)
	}
}
