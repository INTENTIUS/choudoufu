// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"testing"

	flocicap "github.com/intentius/choudoufu/live"
)

func TestImageDigest(t *testing.T) {
	t.Run("pinned by digest (the default)", func(t *testing.T) {
		digest, ok := imageDigest()
		if !ok {
			t.Fatal("defaultImage has no @sha256: digest to extract - has it been repinned to a floating tag?")
		}
		if digest == "" {
			t.Error("imageDigest returned ok=true with an empty digest")
		}
	})

	t.Run("FLOCI_IMAGE override with no digest is not pinned", func(t *testing.T) {
		t.Setenv("FLOCI_IMAGE", "floci/floci:latest")
		if _, ok := imageDigest(); ok {
			t.Error("expected ok=false for a floating-tag override, got true")
		}
	})

	t.Run("FLOCI_IMAGE override pinned by digest resolves", func(t *testing.T) {
		t.Setenv("FLOCI_IMAGE", "example.com/other@sha256:deadbeef")
		digest, ok := imageDigest()
		if !ok || digest != "sha256:deadbeef" {
			t.Errorf("imageDigest = %q, ok=%v, want (\"sha256:deadbeef\", true)", digest, ok)
		}
	})
}

// TestCapabilityGateSkipsForKnownGap exercises CapabilityGate against the
// real, committed live/floci-capabilities.json entry for the default
// pinned image (no FLOCI_IMAGE override) - aws_qldb_ledger is one of the
// databases cohort's documented "Floci coverage" findings: QLDBSession/QLDB
// CreateLedger both return a clean UnknownOperationException, re-probed
// against the pinned digest 2026-08-18, no QLDB handler at all.
//
// This used aws_redshift_cluster until 2026-08-18: re-probing the pinned
// digest found redshift create-cluster now succeeds (the SQS-misroute that
// made it a "known gap" is fixed on this pin), so it stopped being a gap
// example and moved to aws_qldb_ledger, which is still genuinely broken.
func TestCapabilityGateSkipsForKnownGap(t *testing.T) {
	var sub *testing.T
	t.Run("gap", func(st *testing.T) {
		sub = st
		CapabilityGate(st, "aws_qldb_ledger")
		t.Fatal("unreachable: CapabilityGate should have skipped before this line")
	})
	if !sub.Skipped() {
		t.Fatal("CapabilityGate did not skip for aws_qldb_ledger, a documented manifest gap")
	}
	if sub.Failed() {
		t.Error("the subtest failed rather than skipped cleanly")
	}
}

// TestCapabilityGateNoOpForUnrecordedType checks the other half of the
// contract: a type the manifest has no entry for must not be skipped -
// silence means "not investigated", not "known broken", and the caller's
// test must run normally.
func TestCapabilityGateNoOpForUnrecordedType(t *testing.T) {
	var sub *testing.T
	ran := false
	t.Run("noop", func(st *testing.T) {
		sub = st
		CapabilityGate(st, "aws_s3_bucket")
		ran = true
	})
	if sub.Skipped() {
		t.Error("CapabilityGate skipped for aws_s3_bucket, which has no manifest entry; expected a no-op")
	}
	if !ran {
		t.Error("code after CapabilityGate never ran; expected a no-op for an unrecorded type")
	}
}

// TestCapabilityGateMechanismScoping checks that a gap recorded under one
// mechanism never leaks into a lookup scoped to another, driven by real
// committed rows at the pinned digest rather than a fixture.
//
// aws_redshift_cluster carries a mechanism="" row recording unimplemented
// (create-cluster misroutes to the SQS handler) and no tagging-sweep row at
// all, so the ordinary-path gate must skip on it
// (TestCapabilityGateSkipsForKnownGap covers that half) while the
// tagging-sweep wrapper must be a no-op, there being no row under that
// mechanism to read.
//
// aws_vpc runs the property the other way round: no mechanism="" row at
// all, and a cloudcontrol-list row recording implemented, so both gates are
// no-ops for opposite reasons. Its cloudcontrol-list row is one of the
// seven that survived the sweep's rewrite into a create/list round trip -
// CreateResource made a VPC and the following ListResources came back
// carrying it.
//
// This test used to run the first property through aws_redshift_cluster's
// own cloudcontrol-list row, on the strength of that row recording
// implemented. It no longer does, and the reason is the point of the
// rewrite: that verdict came from ListResources returning without erroring,
// which floci does for every type whether or not its list handler answers.
// Re-probed as a round trip, the row is unimplemented, and the gate now
// skips on it - correctly.
//
// The tagging-sweep direction is covered as a positive: the gate must be a
// no-op on aws_iam_role, which is exactly what makes
// internal/live/discovery's TestTaggingSweepAgainstFloci assert its bind
// rather than skip.
func TestCapabilityGateMechanismScoping(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
		why  string
	}{
		{
			name: "cloudcontrol-list is a no-op once its own row records implemented",
			run:  func(st *testing.T) { CloudControlListCapabilityGate(st, "aws_vpc") },
			why:  "aws_vpc's cloudcontrol-list row records implemented, on a create/list round trip",
		},
		{
			name: "ordinary path does not inherit a scoped row",
			run:  func(st *testing.T) { CapabilityGate(st, "aws_vpc") },
			why:  "aws_vpc has no mechanism=\"\" row; only its cloudcontrol-list row is recorded",
		},
		{
			name: "tagging-sweep does not inherit the ordinary-path gap",
			run:  func(st *testing.T) { TaggingSweepCapabilityGate(st, "aws_redshift_cluster") },
			why:  "aws_redshift_cluster has no tagging-sweep row at all, and silence means \"not investigated\"",
		},
		{
			name: "ordinary path is a no-op for a type with only scoped rows",
			run:  func(st *testing.T) { CapabilityGate(st, "aws_iam_role") },
			why:  "aws_iam_role has no mechanism=\"\" row; it works fine on the ordinary path",
		},
		{
			name: "tagging-sweep is a no-op once the row records implemented",
			run:  func(st *testing.T) { TaggingSweepCapabilityGate(st, "aws_iam_role") },
			why:  "the pinned digest's union index populates the tagging sweep, so the row is implemented",
		},
	}

	for _, tc := range cases {
		var sub *testing.T
		ran := false
		t.Run(tc.name, func(st *testing.T) {
			sub = st
			tc.run(st)
			ran = true
		})
		if sub.Skipped() {
			t.Errorf("%s: gate skipped, expected a no-op (%s)", tc.name, tc.why)
		}
		if !ran {
			t.Errorf("%s: code after the gate never ran (%s)", tc.name, tc.why)
		}
	}
}

// TestCloudControlListGateSkipsForAListThatCannotFindWhatExists is the
// scoping property in the skip direction, and the reason the manifest's
// cloudcontrol-list verdict was rewritten to mean something.
//
// aws_cloudfront_cache_policy has no mechanism="" row, so the ordinary gate
// is a no-op on it - floci's CloudFront handler creates cache policies
// fine, and its own list-cache-policies returns them. What it cannot do is
// enumerate them through Cloud Control, which is the one thing a discovery
// leg needs, and the scoped gate is what has to catch that. Under the old
// bare-call sweep this row read implemented and the gate waved the test
// through.
func TestCloudControlListGateSkipsForAListThatCannotFindWhatExists(t *testing.T) {
	var scoped *testing.T
	t.Run("scoped", func(st *testing.T) {
		scoped = st
		CloudControlListCapabilityGate(st, "aws_cloudfront_cache_policy")
		st.Fatal("unreachable: the cloudcontrol-list gate should have skipped before this line")
	})
	if !scoped.Skipped() {
		t.Error("CloudControlListCapabilityGate did not skip for aws_cloudfront_cache_policy, whose list cannot find an object it just created")
	}

	var ordinary *testing.T
	ran := false
	t.Run("ordinary", func(st *testing.T) {
		ordinary = st
		CapabilityGate(st, "aws_cloudfront_cache_policy")
		ran = true
	})
	if ordinary.Skipped() || !ran {
		t.Error("CapabilityGate skipped for aws_cloudfront_cache_policy; a cloudcontrol-list gap must not leak into the ordinary path")
	}
}

// TestCapabilityGateIsANoOpForUnverified holds the contract for the
// manifest's fifth status. "unverified" is what the round-trip sweep writes
// when it reached a real handler and settled nothing - for
// aws_subnet, ListResources came back carrying floci's three default
// subnets, and CreateResource with an empty desired state was refused, so
// there was no object of this run's own making to look for. That is
// evidence of nothing, and it must be read exactly the way an absent row is
// read: let the test run and find out.
//
// Skipping on it would be the failure mode this whole rewrite is against,
// inverted - hiding a type nobody has established anything about, instead
// of waving one through.
func TestCapabilityGateIsANoOpForUnverified(t *testing.T) {
	entry, ok := flocicap.FlociTypeCapability(digestOrSkip(t), "aws_subnet", "cloudcontrol-list")
	if !ok {
		t.Skip("aws_subnet has no cloudcontrol-list row at the pinned digest")
	}
	if entry.Status != flocicap.FlociUnverified {
		t.Skipf("aws_subnet cloudcontrol-list is %q, not unverified; this test needs an unverified row to mean anything", entry.Status)
	}

	var sub *testing.T
	ran := false
	t.Run("noop", func(st *testing.T) {
		sub = st
		CloudControlListCapabilityGate(st, "aws_subnet")
		ran = true
	})
	if sub.Skipped() {
		t.Error("CloudControlListCapabilityGate skipped on an unverified row; unverified means not established, not known-broken")
	}
	if !ran {
		t.Error("code after the gate never ran for an unverified row")
	}
}

// digestOrSkip is imageDigest with a skip for a run whose image is not
// pinned by digest, since every manifest lookup is a no-op in that case.
func digestOrSkip(t *testing.T) string {
	t.Helper()
	digest, ok := imageDigest()
	if !ok {
		t.Skip("the image in play is not pinned by digest; manifest lookups are no-ops")
	}
	return digest
}

// TestServiceCapabilityGateSkipsForKnownGap exercises the service-level
// twin against a real committed entry (networkmanager is absent from
// floci's own /_localstack/health, per the stragglers cohort's "Floci
// coverage" section).
func TestServiceCapabilityGateSkipsForKnownGap(t *testing.T) {
	var sub *testing.T
	t.Run("gap", func(st *testing.T) {
		sub = st
		ServiceCapabilityGate(st, "networkmanager")
		t.Fatal("unreachable: ServiceCapabilityGate should have skipped before this line")
	})
	if !sub.Skipped() {
		t.Fatal("ServiceCapabilityGate did not skip for networkmanager, a documented manifest gap")
	}
}
