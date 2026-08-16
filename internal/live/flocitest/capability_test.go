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

// TestCapabilityGateMechanismScoping checks that a gap recorded under one
// mechanism never leaks into a lookup scoped to another, driven by real
// committed rows at the pinned digest rather than a fixture.
//
// aws_redshift_cluster is the vehicle: at the pinned digest it carries a
// mechanism="" row recording unimplemented (create-cluster misroutes to the
// SQS handler), a mechanism="cloudcontrol-list" row recording implemented,
// and no tagging-sweep row at all. So the ordinary-path gate must skip on
// it (TestCapabilityGateSkipsForKnownGap covers that half), while both
// scoped wrappers must be no-ops - one because its own row says
// implemented, the other because there is no row under that mechanism to
// read.
//
// This test used to run the same property through aws_iam_role's
// tagging-sweep row, which recorded unimplemented until the pin moved to
// sha256:a1c729f4 and floci's union index made all seven tagging-sweep
// recipes implemented. That direction is now covered as a positive: the
// tagging-sweep gate must be a no-op on aws_iam_role, which is exactly what
// makes internal/live/discovery's TestTaggingSweepAgainstFloci assert its
// bind rather than skip.
func TestCapabilityGateMechanismScoping(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
		why  string
	}{
		{
			name: "cloudcontrol-list does not inherit the ordinary-path gap",
			run:  func(st *testing.T) { CloudControlListCapabilityGate(st, "aws_redshift_cluster") },
			why:  "aws_redshift_cluster's cloudcontrol-list row records implemented; only its mechanism=\"\" row is a gap",
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
