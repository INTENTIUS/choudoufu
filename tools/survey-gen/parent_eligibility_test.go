// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// This file is issue #130's guard.
//
// parent_render.go's doc comment claims its eligible-parent set and
// internal/live/discovery's markerCapable are "two independent readings" of
// the same fact. They were not: discovery's is a positive test over the
// provider's live schema, and this one was subtractive - every admitted type
// minus the known-untaggable ones - so an admitted type the survey has no row
// for was silently eligible here and refused there. null_resource was
// published as the swept parent of seven types on the strength of it.
//
// A doc comment cannot hold that. These tests can: the doc generator's
// eligible set must never claim a parent the runtime would refuse.

// TestEligibleParentsAreOnlyPositivelyTaggableTypes pins the direction of the
// test. Membership requires live/survey-full.json to record taggable: true;
// absence from the artifact is never enough.
// testServiceOf is the ServiceOf the schema-driven tests run under: the real
// roster, so parentRef's suffix-match affinity (#167) is exercised against
// live/mapping.json rather than against a stub that would let anything match.
var testServiceOf = func() identity.ServiceOf {
	r, err := registry.Embedded()
	if err != nil {
		panic("registry.Embedded: " + err.Error())
	}
	return r.ServiceOf
}()

// testEnumeration is testServiceOf's counterpart for the Cloud Control
// listing question, and it is the real roster for the same reason: a stub
// that answered "nothing is listable" would let the staleness tests below
// pass against an artifact main.go's own run would never produce.
var testEnumeration = func() cfnEnumeration {
	r, err := registry.Embedded()
	if err != nil {
		panic("registry.Embedded: " + err.Error())
	}
	return rosterEnumeration(r)
}()

func TestEligibleParentsAreOnlyPositivelyTaggableTypes(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	untaggable, taggable, err := untaggableAdmittedTypes(root)
	if err != nil {
		t.Fatalf("untaggableAdmittedTypes: %v", err)
	}

	taggableSet := make(map[string]bool, len(taggable))
	for _, x := range taggable {
		taggableSet[x] = true
	}
	for _, x := range untaggable {
		if taggableSet[x] {
			t.Errorf("%s is in both partitions", x)
		}
	}

	// The two partitions must not add up to the whole admitted table: the
	// record-backed types belong to neither, and a change that quietly folded
	// them into one would put null_resource back.
	for _, typeName := range identity.AdmittedTypes() {
		entry, ok := identity.LookupType(typeName)
		if !ok || !entry.RecordBacked {
			continue
		}
		if taggableSet[typeName] {
			t.Errorf("record-backed type %s is eligible as a parent; it has no AWS existence to read", typeName)
		}
	}
}

// TestParentReadRosterNamesNoIneligibleParent is the end-to-end form: every
// parent the rendered roster names must itself be in the eligible set the
// roster was built from. It is the assertion that would have failed while
// null_resource was in the published table.
func TestParentReadRosterNamesNoIneligibleParent(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	_, taggable, err := untaggableAdmittedTypes(root)
	if err != nil {
		t.Fatalf("untaggableAdmittedTypes: %v", err)
	}
	eligible := make(map[string]bool, len(taggable))
	for _, x := range taggable {
		eligible[x] = true
	}

	readable, _, err := parentReadableRoster(root)
	if err != nil {
		t.Fatalf("parentReadableRoster: %v", err)
	}
	if len(readable) == 0 {
		t.Fatal("the parent-read roster is empty; the walk is broken, not the tree")
	}

	for _, row := range readable {
		if !eligible[row.Parent] {
			t.Errorf("%s is published as swept via a parent read of %s, which is not an eligible (taggable) parent - "+
				"internal/live/discovery would refuse it, so this row describes a sweep that never happens",
				row.Type, row.Parent)
		}
		if entry, ok := identity.LookupType(row.Parent); ok && entry.RecordBacked {
			t.Errorf("%s names record-backed %s as its parent", row.Type, row.Parent)
		}
	}
}
