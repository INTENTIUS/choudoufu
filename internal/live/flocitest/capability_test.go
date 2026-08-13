// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import "testing"

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
// pinned image (no FLOCI_IMAGE override) - aws_redshift_cluster is one of
// the databases cohort's documented "Floci coverage" findings.
func TestCapabilityGateSkipsForKnownGap(t *testing.T) {
	var sub *testing.T
	t.Run("gap", func(st *testing.T) {
		sub = st
		CapabilityGate(st, "aws_redshift_cluster")
		t.Fatal("unreachable: CapabilityGate should have skipped before this line")
	})
	if !sub.Skipped() {
		t.Fatal("CapabilityGate did not skip for aws_redshift_cluster, a documented manifest gap")
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

// TestCapabilityGateMechanismScoping checks that a mechanism-scoped gap
// (aws_iam_role only fails on the tagging-sweep discovery path, per
// live/floci-capabilities.json - it works fine on the ordinary path) does
// not leak into the ordinary mechanism="" lookup, and does fire under its
// own named wrapper.
func TestCapabilityGateMechanismScoping(t *testing.T) {
	var ordinary *testing.T
	t.Run("ordinary path", func(st *testing.T) {
		ordinary = st
		CapabilityGate(st, "aws_iam_role")
	})
	if ordinary.Skipped() {
		t.Error("CapabilityGate (ordinary path) skipped for aws_iam_role; that gap is scoped to tagging-sweep only")
	}

	var sweep *testing.T
	t.Run("tagging-sweep", func(st *testing.T) {
		sweep = st
		TaggingSweepCapabilityGate(st, "aws_iam_role")
		t.Fatal("unreachable: TaggingSweepCapabilityGate should have skipped before this line")
	})
	if !sweep.Skipped() {
		t.Fatal("TaggingSweepCapabilityGate did not skip for aws_iam_role, a documented mechanism-scoped gap")
	}
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
