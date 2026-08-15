// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/registry"
)

// This file is issue #129's guard, and it lives here rather than in
// internal/live/identity because the fact it checks against - which AWS
// service a Terraform type belongs to - comes from live/mapping.json, which
// the identity package deliberately does not read.

// TestNoDerivedParentCrossesAServiceBoundary is the rule.
//
// parentByConvention used to search every admitted type for the shortest name
// ending in the argument's base, which made "name" resolve to
// aws_api_gateway_domain_name for 35 unrelated types and "resource" resolve to
// null_resource for seven more. The affinity requirement is what stops that,
// and it has to be the CloudFormation service rather than the Terraform
// prefix: aws_volume_attachment and aws_ebs_volume are both AWS::EC2 while
// sharing no Terraform prefix at all, so a prefix rule would drop a correct
// link and need an exception entry to get it back.
//
// Rule 1's type-name prefix chain is exempt: a name that literally extends
// another admitted type's name (aws_s3_bucket_policy over aws_s3_bucket) is a
// stronger fact than any service test, and it is allowed to cross.
func TestNoDerivedParentCrossesAServiceBoundary(t *testing.T) {
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("registry.Embedded: %v", err)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	readable, _, err := parentReadableRoster(root)
	if err != nil {
		t.Fatalf("parentReadableRoster: %v", err)
	}
	if len(readable) == 0 {
		t.Fatal("no parent-readable rows; the roster is broken, not the rule")
	}

	crossed := 0
	for _, row := range readable {
		if strings.HasPrefix(row.Type, row.Parent+"_") {
			continue // rule 1: the name chain, which may cross
		}
		childSvc, childOK := roster.ServiceOf(row.Type)
		parentSvc, parentOK := roster.ServiceOf(row.Parent)
		if !childOK || !parentOK {
			continue // unmapped: the Terraform-prefix fallback governs, tested below
		}
		if childSvc != parentSvc {
			crossed++
			t.Errorf("%s is derived as a child of %s, across a service boundary (%s vs %s) "+
				"that no type-name chain established", row.Type, row.Parent, childSvc, parentSvc)
		}
	}
	t.Logf("checked %d parent-read rows, %d crossed a service boundary", len(readable), crossed)
}

// TestDerivedParentsAgreeWithRelationshipRef is the ratchet #151 exists for:
// where AWS itself declares that a property references another type, a
// derivation must not contradict it.
//
// Scope note, so a future reader knows what this is worth today. 26 registry
// types carry a relationshipRef and 20 of them map to an admitted Terraform
// type, but none of those 20 is an untaggable parent-read child, so the
// overlap with the rendered roster is currently empty and this test guards
// nothing yet. It is written anyway because relationshipRef coverage grows on
// every registry refresh, and the day it reaches one of these rows is not a
// day anyone will remember to add the check. The logged counts are what say
// whether it has started guarding.
func TestDerivedParentsAgreeWithRelationshipRef(t *testing.T) {
	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("registry.Embedded: %v", err)
	}
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	readable, _, err := parentReadableRoster(root)
	if err != nil {
		t.Fatalf("parentReadableRoster: %v", err)
	}

	checked := 0
	for _, row := range readable {
		declared := roster.RelatedTypes(row.Type)
		if len(declared) == 0 {
			continue
		}
		parentCFN, ok := roster.CloudControlTypeOrService(row.Parent)
		if !ok {
			continue
		}
		checked++
		if !declared[parentCFN] {
			t.Errorf("%s is derived as a child of %s (%s), but its schema declares relationshipRefs to %v "+
				"and not to that - AWS's own declaration wins, so the derivation is wrong",
				row.Type, row.Parent, parentCFN, sortedKeysOf(declared))
		}
	}
	t.Logf("%d of %d parent-read rows have a relationshipRef to check against", checked, len(readable))
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
