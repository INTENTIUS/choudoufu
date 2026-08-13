// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import "testing"

// pinnedDigest is the digest internal/live/flocitest.defaultImage and
// live/e2e/run.sh's own FLOCI_IMAGE default currently pin, duplicated here
// (rather than imported - internal/live/flocitest cannot be imported from
// this package without a cycle, since flocitest itself will come to depend
// on this package) so this test fails loudly, not silently, the day the pin
// moves without live/floci-capabilities.json gaining a matching entry.
const pinnedDigest = "sha256:4753246c0260a22af1056c65993f4d73b0a907729a9580b9baba5d628b6dad34"

func TestFlociServiceCapability(t *testing.T) {
	cap, ok := FlociServiceCapability(pinnedDigest, "networkmanager")
	if !ok {
		t.Fatal("expected a manifest entry for networkmanager at the pinned digest")
	}
	if cap.Status != FlociUnimplemented {
		t.Errorf("networkmanager status = %q, want %q", cap.Status, FlociUnimplemented)
	}
	if cap.Evidence == "" || cap.Source == "" {
		t.Errorf("networkmanager entry is missing evidence or a source citation: %+v", cap)
	}

	if _, ok := FlociServiceCapability(pinnedDigest, "s3"); ok {
		t.Error("expected no manifest entry for s3 (never investigated), got one")
	}
	if _, ok := FlociServiceCapability("sha256:doesnotexist", "networkmanager"); ok {
		t.Error("expected no manifest entry for an unknown digest, got one")
	}
}

func TestFlociTypeCapability(t *testing.T) {
	cap, ok := FlociTypeCapability(pinnedDigest, "aws_redshift_cluster", "")
	if !ok {
		t.Fatal("expected a manifest entry for aws_redshift_cluster at the pinned digest")
	}
	if cap.Status != FlociUnimplemented {
		t.Errorf("aws_redshift_cluster status = %q, want %q", cap.Status, FlociUnimplemented)
	}

	broken, ok := FlociTypeCapability(pinnedDigest, "aws_qldb_ledger", "")
	if !ok || broken.Status != FlociBroken {
		t.Errorf("aws_qldb_ledger = %+v, ok=%v, want status %q", broken, ok, FlociBroken)
	}

	partial, ok := FlociTypeCapability(pinnedDigest, "aws_opensearch_domain", "")
	if !ok || partial.Status != FlociPartial {
		t.Errorf("aws_opensearch_domain = %+v, ok=%v, want status %q", partial, ok, FlociPartial)
	}

	// Mechanism scoping: aws_iam_role is well-supported on the ordinary
	// path (no entry there at all - it is only the tagging-sweep mechanism
	// that misses it), so the empty-mechanism lookup must miss while the
	// scoped one hits.
	if _, ok := FlociTypeCapability(pinnedDigest, "aws_iam_role", ""); ok {
		t.Error("expected no ordinary-path manifest entry for aws_iam_role (it works fine there); got one")
	}
	sweep, ok := FlociTypeCapability(pinnedDigest, "aws_iam_role", "tagging-sweep")
	if !ok || sweep.Status != FlociUnimplemented {
		t.Errorf("aws_iam_role tagging-sweep = %+v, ok=%v, want status %q", sweep, ok, FlociUnimplemented)
	}

	if _, ok := FlociTypeCapability(pinnedDigest, "aws_no_such_type", ""); ok {
		t.Error("expected no manifest entry for a made-up type, got one")
	}
}
