// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/check"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// resolutionOf builds a minimal [identity.Resolution] naming one resource
// type, for tests that only need [check.Report.Identities] to carry a
// Class and a type name - the same construction discoverycause_test.go uses.
func resolutionOf(typeName string, class identity.Class) identity.Resolution {
	return identity.Resolution{
		Addr: addrs.AbsResourceInstance{
			Module: addrs.RootModuleInstance,
			Resource: addrs.ResourceInstance{
				Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: typeName, Name: "x"},
				Key:      addrs.NoKey,
			},
		},
		Class: class,
	}
}

func awsProvider(t *testing.T) addrs.Provider {
	t.Helper()
	p, diags := addrs.ParseProviderSourceString("hashicorp/aws")
	if diags.HasErrors() {
		t.Fatalf("parsing hashicorp/aws: %v", diags.Err())
	}
	return p
}

// TestVersionSkewForFindsARegression pins issue #269's actual shape: an
// entry declares a type ([identity.ClassNeedsDiscovery]) that the run's
// PINNED provider version can list, but the entry's OWN required_providers
// constraint resolves to a release that cannot - team-members-access's
// "~> 5.64" resolving to 5.100.0, a release with no aws_iam_policy list
// resource, against this run's 6.59.0 pin which has one.
//
// Both acquisitions are pre-seeded into the acquirer's cache under the exact
// keys [acquirer.acquire] and [acquirer.acquireOwn] compute, so this test
// launches no subprocess and needs no network.
func TestVersionSkewForFindsARegression(t *testing.T) {
	aws := awsProvider(t)
	a := newAcquirer("terraform", aws, "6.59.0", nil, nil)

	need := providerNeed{Provider: aws, Constraint: "~> 5.64"}

	pinnedKey := (providerNeed{Provider: aws}).key() // acquire's pinned branch strips the constraint
	a.cache[pinnedKey] = acquired{
		Available: true, Version: "6.59.0",
		listTypes: map[string]bool{"aws_iam_policy": true, "aws_iam_role": true},
	}
	ownKey := "own:" + need.key()
	a.cache[ownKey] = acquired{
		Available: true, Version: "5.100.0",
		listTypes: map[string]bool{}, // 5.100.0 has no list resources at all
	}

	rep := check.Report{Identities: []identity.Resolution{
		resolutionOf("aws_iam_policy", identity.ClassNeedsDiscovery),
		// A concrete resolution must never contribute to NeedsDiscovery: it
		// needs no listing, so its absence from an old release is not a
		// version-skew regression, whatever its type.
		resolutionOf("aws_iam_role", identity.ClassConcrete),
	}}

	skew := a.versionSkewFor([]providerNeed{need}, rep)
	if skew == nil {
		t.Fatal("versionSkewFor returned nil, want a result - the fixture declares a discovery type and a pinned-provider requirement")
	}
	if !skew.Diverges {
		t.Errorf("Diverges = false, want true: pinned version lists aws_iam_policy, own version does not")
	}
	if skew.PinnedVersion != "6.59.0" || skew.OwnVersion != "5.100.0" {
		t.Errorf("PinnedVersion/OwnVersion = %q/%q, want 6.59.0/5.100.0", skew.PinnedVersion, skew.OwnVersion)
	}
	if len(skew.NeedsDiscovery) != 1 || skew.NeedsDiscovery[0] != "aws_iam_policy" {
		t.Errorf("NeedsDiscovery = %v, want [aws_iam_policy] - aws_iam_role is ClassConcrete and must not appear", skew.NeedsDiscovery)
	}
	if len(skew.MissingUnderOwn) != 1 || skew.MissingUnderOwn[0] != "aws_iam_policy" {
		t.Errorf("MissingUnderOwn = %v, want [aws_iam_policy]", skew.MissingUnderOwn)
	}
}

// TestVersionSkewForAgreesWhenBothVersionsListTheType is the false-positive
// guard #269's verification step calls for explicitly: an entry whose own
// constraint resolves compatibly must read Diverges false, not merely
// "not checked".
func TestVersionSkewForAgreesWhenBothVersionsListTheType(t *testing.T) {
	aws := awsProvider(t)
	a := newAcquirer("terraform", aws, "6.59.0", nil, nil)

	need := providerNeed{Provider: aws, Constraint: "= 6.58.0"}

	pinnedKey := (providerNeed{Provider: aws}).key()
	a.cache[pinnedKey] = acquired{Available: true, Version: "6.59.0", listTypes: map[string]bool{"aws_iam_policy": true}}
	ownKey := "own:" + need.key()
	a.cache[ownKey] = acquired{Available: true, Version: "6.58.0", listTypes: map[string]bool{"aws_iam_policy": true}}

	rep := check.Report{Identities: []identity.Resolution{
		resolutionOf("aws_iam_policy", identity.ClassNeedsDiscovery),
	}}

	skew := a.versionSkewFor([]providerNeed{need}, rep)
	if skew == nil {
		t.Fatal("versionSkewFor returned nil, want a checked-and-clean result")
	}
	if skew.Diverges {
		t.Errorf("Diverges = true, want false: both versions list aws_iam_policy")
	}
	if len(skew.MissingUnderOwn) != 0 {
		t.Errorf("MissingUnderOwn = %v, want empty", skew.MissingUnderOwn)
	}
}

// TestVersionSkewForNilWhenNothingNeedsDiscovery is the other false-positive
// guard: an entry that requires the pinned provider but declares no
// marker-discovered type has nothing this signal can say, and must read nil
// rather than a checked-clean result a summary could mistake for "verified
// fine" when it was in fact never examined.
func TestVersionSkewForNilWhenNothingNeedsDiscovery(t *testing.T) {
	aws := awsProvider(t)
	a := newAcquirer("terraform", aws, "6.59.0", nil, nil)
	need := providerNeed{Provider: aws, Constraint: "~> 5.64"}

	rep := check.Report{Identities: []identity.Resolution{
		resolutionOf("aws_iam_role", identity.ClassConcrete),
	}}

	if skew := a.versionSkewFor([]providerNeed{need}, rep); skew != nil {
		t.Errorf("versionSkewFor = %+v, want nil: nothing in the fixture needs discovery", skew)
	}
}

// TestVersionSkewForNilWithNoPinnedRequirement covers an entry that needs
// discovery for some OTHER provider's type entirely - a google_* or tfe_*
// estate, say - and never asked for [acquirer.pinned] at all. There is no
// "own version" to compare against a pin that was never forced on this
// entry in the first place.
func TestVersionSkewForNilWithNoPinnedRequirement(t *testing.T) {
	aws := awsProvider(t)
	a := newAcquirer("terraform", aws, "6.59.0", nil, nil)

	other, diags := addrs.ParseProviderSourceString("hashicorp/google")
	if diags.HasErrors() {
		t.Fatalf("parsing hashicorp/google: %v", diags.Err())
	}
	need := providerNeed{Provider: other, Constraint: ">= 5.0"}

	rep := check.Report{Identities: []identity.Resolution{
		resolutionOf("google_compute_instance", identity.ClassNeedsDiscovery),
	}}

	if skew := a.versionSkewFor([]providerNeed{need}, rep); skew != nil {
		t.Errorf("versionSkewFor = %+v, want nil: this entry never required the pinned provider", skew)
	}
}
