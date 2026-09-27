// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestEveryAdmittedTypeHasATierOrIsAwaitingRuling is issue #1600's own
// accept criterion: an admitted type of either substrate never leaves this
// generator with no tier and no explanation for why not.
//
// AWS: every row Build() produces from live/survey-full.json carries one of
// the four ruling tiers (TestPartitionGuard already pins this half).
//
// Kubernetes: every admitted NonAWSProvider type either carries a real tier
// (once a ruling resolves one) or is named in kubernetesAwaitingRuling with
// [StatusAwaitingRuling] - never silently absent from
// [Artifact.Kubernetes.Types] and never present with an empty tier and no
// ledger entry.
func TestEveryAdmittedTypeHasATierOrIsAwaitingRuling(t *testing.T) {
	root := testRepoRoot(t)
	art, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	awsTier := make(map[string]string, len(art.Types))
	for _, r := range art.Types {
		awsTier[r.Type] = r.Tier
	}
	k8sRow := make(map[string]Row, len(art.Kubernetes.Types))
	for _, r := range art.Kubernetes.Types {
		k8sRow[r.Type] = r
	}

	for _, typeName := range identity.AdmittedTypes() {
		entry, ok := identity.LookupType(typeName)
		if !ok {
			t.Fatalf("%s is in identity.AdmittedTypes() but LookupType found nothing", typeName)
		}
		if entry.RecordBacked {
			// Issue #1600 is scoped to "AWS and Kubernetes on one approach"
			// (epic #1579): a RECORD_ADMITTED logical type (null_resource,
			// terraform_data, the random_*, time_*, tls_* and local_* rows)
			// belongs to neither substrate - its whole existence is a
			// persisted micro-state record, not a live cloud object, and
			// "which tier does its identity recover through" is not even a
			// meaningful question for it (the same reasoning
			// tools/survey-gen's untaggableAdmittedTypes already applies).
			// Out of scope here; a real gap in covering these would be its
			// own issue.
			continue
		}
		if !entry.NonAWSProvider {
			tier, present := awsTier[typeName]
			if !present {
				t.Errorf("%s is admitted and AWS-provider but has no row in live/readiness.json's Types at all", typeName)
				continue
			}
			if tier == "" {
				t.Errorf("%s is admitted with no tier and is not a Kubernetes (or other non-AWS) type, so no allowlist can excuse it", typeName)
			}
			continue
		}

		row, present := k8sRow[typeName]
		if !present {
			t.Errorf("%s is an admitted non-AWS-provider type absent from Artifact.Kubernetes.Types entirely", typeName)
			continue
		}
		if row.Tier != "" {
			// A ruling has resolved this type into a real tier; nothing left
			// to check here.
			continue
		}
		if !kubernetesAwaitingRuling[typeName] {
			t.Errorf("%s has no tier and is not in kubernetesAwaitingRuling; classify it or add it to the ledger with a reason (issue #1600)", typeName)
		}
		if row.Status != StatusAwaitingRuling {
			t.Errorf("%s has an empty tier but status %q, want %q", typeName, row.Status, StatusAwaitingRuling)
		}
	}
}

// TestClassifyNonAWSRefusesAnUnlistedType is the guard's unit-level proof,
// independent of what is currently admitted: a synthetic non-AWS type with
// no tier rule and no ledger entry must error rather than be silently
// classified. Made to fail on purpose while writing this test by clearing
// kubernetesAwaitingRuling's real entries first - it passed on an empty
// ledger for a real member too, as expected, then failed the moment a real
// member's own tier was asserted non-empty by mistake.
func TestClassifyNonAWSRefusesAnUnlistedType(t *testing.T) {
	_, err := classifyNonAWS("kubernetes_totally_made_up_type", map[string]bool{})
	if err == nil {
		t.Fatal("classifyNonAWS accepted a type with no tier rule and no ledger entry; it should have errored")
	}
}

// TestKubernetesLabelSurfaceTypesAreTierA is the maintainer's 2026-09-27
// ruling on #1600 (tier A reads the substrate's own marker: tags on AWS,
// labels on Kubernetes), applied once #1630 confirmed LabelSurface against
// the real hashicorp/kubernetes 3.2.1 schema for all four types (verified
// 2026-09-26 via internal/live/pluginschema against the warm plugin cache:
// LabelSurface=true, TagSurface=false for every one of them, 81 resource
// types total). They must classify as marker-carried and in-contract, not
// sit in kubernetesAwaitingRuling any longer.
func TestKubernetesLabelSurfaceTypesAreTierA(t *testing.T) {
	for _, typeName := range []string{
		"kubernetes_cluster_role_binding",
		"kubernetes_config_map",
		"kubernetes_namespace",
		"kubernetes_storage_class",
	} {
		row, err := classifyNonAWS(typeName, map[string]bool{})
		if err != nil {
			t.Fatalf("classifyNonAWS(%s): %v", typeName, err)
		}
		if row.Tier != TierMarkerCarried {
			t.Errorf("%s: tier = %q, want %q", typeName, row.Tier, TierMarkerCarried)
		}
		if row.Status != StatusInContract {
			t.Errorf("%s: status = %q, want %q", typeName, row.Status, StatusInContract)
		}
		if kubernetesAwaitingRuling[typeName] {
			t.Errorf("%s is still in kubernetesAwaitingRuling; the ruling resolved it into tier A", typeName)
		}
		if !row.Facts.LabelSurface {
			t.Errorf("%s: Facts.LabelSurface = false, want true (the confirmed evidence for this ruling)", typeName)
		}
	}
}

// TestKubernetesAwaitingRulingLedgerMatchesAdmission is the ratchet half:
// every ledger entry must actually be an admitted, NonAWSProvider type today
// - the same hygiene live/harness's credential-exclusion ledgers and
// tools/readiness-gen/build_test.go's readinessRatchetAllowlist both hold
// their own exemption lists to. A stale entry here would hide a type that
// left admission, or one that turned out to be AWS-provider after all.
func TestKubernetesAwaitingRulingLedgerMatchesAdmission(t *testing.T) {
	for typeName := range kubernetesAwaitingRuling {
		entry, ok := identity.LookupType(typeName)
		if !ok {
			t.Errorf("kubernetesAwaitingRuling names %s, which is not in identity.DefaultTable at all; delete the entry", typeName)
			continue
		}
		if !entry.NonAWSProvider {
			t.Errorf("kubernetesAwaitingRuling names %s, which is admitted but not NonAWSProvider; delete the entry", typeName)
		}
	}
}

// TestKubernetesLabelSurfaceLedgerMatchesAdmission is
// TestKubernetesAwaitingRulingLedgerMatchesAdmission's twin for
// [kubernetesLabelSurfaceTypes]: every entry must actually be an admitted,
// NonAWSProvider type today.
func TestKubernetesLabelSurfaceLedgerMatchesAdmission(t *testing.T) {
	for typeName := range kubernetesLabelSurfaceTypes {
		entry, ok := identity.LookupType(typeName)
		if !ok {
			t.Errorf("kubernetesLabelSurfaceTypes names %s, which is not in identity.DefaultTable at all; delete the entry", typeName)
			continue
		}
		if !entry.NonAWSProvider {
			t.Errorf("kubernetesLabelSurfaceTypes names %s, which is admitted but not NonAWSProvider; delete the entry", typeName)
		}
	}
}
