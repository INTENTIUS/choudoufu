// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"testing"
)

// TestCompositionCountsAreExact pins the deterministic formulas
// buildEstate's doc comment states, so a change to the per-team or
// per-service resource count is a deliberate edit to this test rather than
// a silent drift. teams = 6*scale, services = 1*scale, dnsRecords =
// 10*scale; identity = 6*teams + 2*services; container = 1 (cluster) +
// 2*services; dns = 1 (zone) + dnsRecords; supporting = 3 (vpc, subnet,
// security group), fixed regardless of scale.
func TestCompositionCountsAreExact(t *testing.T) {
	for _, tc := range []struct {
		scale                                          int
		wantIdentity, wantContainer, wantDNS, wantSupp int
	}{
		{1, 38, 3, 11, 3},
		{4, 152, 9, 41, 3},
		{10, 380, 21, 101, 3},
	} {
		t.Run(fmt.Sprintf("scale=%d", tc.scale), func(t *testing.T) {
			c := buildEstate(tc.scale, "tl").composition
			if c.identityResources != tc.wantIdentity {
				t.Errorf("identityResources = %d, want %d", c.identityResources, tc.wantIdentity)
			}
			if c.containerResources != tc.wantContainer {
				t.Errorf("containerResources = %d, want %d", c.containerResources, tc.wantContainer)
			}
			if c.dnsResources != tc.wantDNS {
				t.Errorf("dnsResources = %d, want %d", c.dnsResources, tc.wantDNS)
			}
			if c.supportingResources != tc.wantSupp {
				t.Errorf("supportingResources = %d, want %d", c.supportingResources, tc.wantSupp)
			}
		})
	}
}

// TestIdentityShareApproximatesTarget is #564's "scale is a parameter; the
// composition proportions hold as it grows" acceptance bullet, checked
// against the actual computed share rather than assumed. The epic (#546)
// describes "~70%" as the target; this asserts a band around it (60-80%)
// at both a small and a larger scale, which is what "holds as it grows"
// means operationally - not that the two numbers are identical, since the
// fixed 3-resource network layer means the small-scale share starts a
// little under the large-scale asymptote (measured: 69.1% at scale=1,
// converging to ~75% by scale=10 - see gen.go's doc comment for the
// derivation).
func TestIdentityShareApproximatesTarget(t *testing.T) {
	const lowBand, highBand = 60.0, 80.0
	for _, scale := range []int{1, 4, 10, 40} {
		c := buildEstate(scale, "tl").composition
		pct := c.identityPercent()
		if pct < lowBand || pct > highBand {
			t.Errorf("scale=%d: identity share = %.1f%%, want within [%.0f%%, %.0f%%] of the ~70%% target", scale, pct, lowBand, highBand)
		}
	}
}

// TestDuplicationIsSubstantialAndMeasured is the other half of #546's
// composition description ("~45% duplication ... near-identical roles and
// policies"). It does not assert an exact percentage - the method
// (composition's doc comment) is a stated, auditable definition, not a
// tuned constant, and different scales land at different points before
// the small-N pool effects saturate (measured: 30.8% at scale=1, 53.8%
// from scale=4 on). What must hold at every scale: duplication is
// substantial (not near-zero, which would mean every role and policy is
// already unique) and not total (not near-100%, which would mean nothing
// about this identity layer is genuinely per-team) - see gen.go's
// isBoilerplateTeam split for the mechanism that keeps both true.
func TestDuplicationIsSubstantialAndMeasured(t *testing.T) {
	for _, scale := range []int{1, 4, 10} {
		c := buildEstate(scale, "tl").composition
		if c.totalRolePolicyBlocks == 0 {
			t.Fatalf("scale=%d: no role/policy blocks were measured at all", scale)
		}
		pct := c.duplicationPercent()
		if pct < 20 || pct > 80 {
			t.Errorf("scale=%d: role+policy duplication = %.1f%% (%d/%d), want a substantial-but-not-total band [20%%, 80%%]",
				scale, pct, c.duplicateRolePolicyBlocks, c.totalRolePolicyBlocks)
		}
	}
}

// TestDeterministic mirrors estate-gen's own TestDeterminism: two runs at
// the same scale and prefix must produce byte-identical files, since a
// generator whose output depends on map iteration order or a timestamp
// cannot be diffed meaningfully across regenerations.
func TestDeterministic(t *testing.T) {
	a := buildEstate(3, "tl")
	b := buildEstate(3, "tl")
	if len(a.files) != len(b.files) {
		t.Fatalf("file count differs: %d vs %d", len(a.files), len(b.files))
	}
	for name, contentA := range a.files {
		contentB, ok := b.files[name]
		if !ok {
			t.Fatalf("%s present in run 1, absent in run 2", name)
		}
		if contentA != contentB {
			t.Errorf("%s differs between two generations at the same scale/prefix", name)
		}
	}
}
