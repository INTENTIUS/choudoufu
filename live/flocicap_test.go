// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"strings"
	"testing"
)

// pinnedDigest reads the digest out of live/floci-image, the pin's single
// source of truth (#98), so this test fails loudly, not silently, the day
// the pin moves without live/floci-capabilities.json gaining a matching
// entry. It used to be a fourth hand-duplicated copy of the digest.
var pinnedDigest = func() string {
	data, err := os.ReadFile("floci-image")
	if err != nil {
		panic("reading live/floci-image: " + err.Error())
	}
	ref := strings.TrimSpace(string(data))
	i := strings.Index(ref, "@")
	if i < 0 {
		panic("live/floci-image holds no @sha256 digest: " + ref)
	}
	return ref[i+1:]
}()

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

	// aws_qldb_ledger was FlociBroken (HTML-error-page crash) until the
	// media-services image's routing fix converted the shape into a clean
	// UnknownOperationException - re-probed 2026-08-14 against the pinned
	// digest, still no QLDB handler, so the honest status is unimplemented.
	qldb, ok := FlociTypeCapability(pinnedDigest, "aws_qldb_ledger", "")
	if !ok || qldb.Status != FlociUnimplemented {
		t.Errorf("aws_qldb_ledger = %+v, ok=%v, want status %q", qldb, ok, FlociUnimplemented)
	}

	partial, ok := FlociTypeCapability(pinnedDigest, "aws_opensearch_domain", "")
	if !ok || partial.Status != FlociPartial {
		t.Errorf("aws_opensearch_domain = %+v, ok=%v, want status %q", partial, ok, FlociPartial)
	}

	// Mechanism scoping: aws_iam_role is well-supported on the ordinary
	// path and has no entry there at all, so the empty-mechanism lookup
	// must miss while the tagging-sweep-scoped one hits. What the scoped
	// row says changed with the pin: sha256:a1c729f4 unioned the
	// resourcegroupstaggingapi index with a live read of every service's
	// stores, so the same seven recipes that all came back empty against
	// sha256:1362e856 now all turn up in the sweep. The row is still here,
	// still mechanism-scoped, and now records implemented - which is what
	// makes flocitest.TaggingSweepCapabilityGate a no-op rather than a skip.
	if _, ok := FlociTypeCapability(pinnedDigest, "aws_iam_role", ""); ok {
		t.Error("expected no ordinary-path manifest entry for aws_iam_role (it works fine there); got one")
	}
	sweep, ok := FlociTypeCapability(pinnedDigest, "aws_iam_role", "tagging-sweep")
	if !ok || sweep.Status != FlociImplemented {
		t.Errorf("aws_iam_role tagging-sweep = %+v, ok=%v, want status %q", sweep, ok, FlociImplemented)
	}

	if _, ok := FlociTypeCapability(pinnedDigest, "aws_no_such_type", ""); ok {
		t.Error("expected no manifest entry for a made-up type, got one")
	}
}
