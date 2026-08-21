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

	// s3 used to have no entry at all ("never investigated" - the old
	// -mode=services watchlist only ever checked services some past run
	// had already recorded). Issue #276 made the watchlist self-expanding
	// from floci's own live health response, so every service the pinned
	// image names - 82 of them, s3 included - now gets a real round trip
	// instead of staying an unexamined gap.
	s3cap, ok := FlociServiceCapability(pinnedDigest, "s3")
	if !ok {
		t.Fatal("expected a manifest entry for s3 at the pinned digest (issue #276 widened -mode=services past its old fixed watchlist)")
	}
	if s3cap.Status != FlociImplemented {
		t.Errorf("s3 status = %q, want %q", s3cap.Status, FlociImplemented)
	}
	if s3cap.Evidence == "" || s3cap.Source == "" {
		t.Errorf("s3 entry is missing evidence or a source citation: %+v", s3cap)
	}

	if _, ok := FlociServiceCapability(pinnedDigest, "totally-fictional-service-xyz"); ok {
		t.Error("expected no manifest entry for a service that does not exist, got one")
	}
	if _, ok := FlociServiceCapability("sha256:doesnotexist", "networkmanager"); ok {
		t.Error("expected no manifest entry for an unknown digest, got one")
	}
}

func TestFlociTypeCapability(t *testing.T) {
	// aws_redshift_cluster's CreateCluster misrouted to the SQS handler on
	// every digest through sha256:488f4d6d. Re-probed 2026-08-18 against
	// the pinned digest: create-cluster now succeeds and returns
	// ClusterStatus "available" - the honest status is implemented.
	cap, ok := FlociTypeCapability(pinnedDigest, "aws_redshift_cluster", "")
	if !ok {
		t.Fatal("expected a manifest entry for aws_redshift_cluster at the pinned digest")
	}
	if cap.Status != FlociImplemented {
		t.Errorf("aws_redshift_cluster status = %q, want %q", cap.Status, FlociImplemented)
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

// TestCloudControlListRowsRecordAnAnswerNotACall pins the defect that made
// this mechanism's verdict worth rewriting.
//
// Every cloudcontrol-list row for the pinned digest used to read
// "implemented", on the strength of "ListResources(X) succeeded". Succeeding
// is not answering: floci's ListResources returns an empty
// ResourceDescriptions, cleanly, for a type whose objects demonstrably
// exist. Measured against sha256:a1c729f4 on 2026-08-17: create a cache
// policy through CloudFront's own API and `cloudfront list-cache-policies`
// returns it (Quantity 1), while `cloudcontrol list-resources --type-name
// AWS::CloudFront::CachePolicy` returns []. A discovery leg reading the old
// manifest would have planned a crossing against a list that cannot see
// what it just made, and AWS::ECS::TaskDefinition is where that actually
// happened - two corpus estates hard-errored on their first cold live-plan
// with "Listed resource with no identity" after applying cleanly.
//
// The three types are named HERE rather than anywhere in
// tools/floci-capability-gen, which must stay free of any AWS::* or aws_*
// literal in its control flow: the generator derives its whole type list
// from internal/live/identity.AdmittedTypes joined through the registry,
// and a test is the right place to hold it to a known answer.
func TestCloudControlListRowsRecordAnAnswerNotACall(t *testing.T) {
	// AWS::ECS::TaskDefinition has moved to the other side of this guard.
	// lex00/floci bda9bc3d backed ListResources' default branch with the
	// service's own store, and the pin moved to sha256:ff1bc407 with it, so
	// the create/list round trip now closes for the type that hard-errored two
	// corpus estates. It stays in this test, asserting the opposite, because a
	// row that goes back to claiming a bare call is the defect either way.
	answers := map[string]bool{
		"aws_cloudfront_cache_policy": false, // AWS::CloudFront::CachePolicy
		"aws_route53_cidr_collection": false, // AWS::Route53::CidrCollection
		"aws_ecs_task_definition":     true,  // AWS::ECS::TaskDefinition
	}
	for tfType, enumerates := range answers {
		entry, ok := FlociTypeCapability(pinnedDigest, tfType, "cloudcontrol-list")
		if !ok {
			t.Errorf("%s has no cloudcontrol-list row at the pinned digest; it is meant to carry the probe's finding", tfType)
			continue
		}
		switch {
		case enumerates && entry.Status != FlociImplemented:
			t.Errorf("%s cloudcontrol-list = %q, but the emulator's list does find an object it just created: %s",
				tfType, entry.Status, entry.Evidence)
		case !enumerates && entry.Status == FlociImplemented:
			t.Errorf("%s cloudcontrol-list = implemented, but the emulator's list cannot find an object it just created: %s",
				tfType, entry.Evidence)
		}
		if !strings.Contains(entry.Evidence, "ListResources") || !strings.Contains(entry.Evidence, "CreateResource") {
			t.Errorf("%s evidence does not say which calls were made, so a reader cannot tell a round trip from a bare call: %q",
				tfType, entry.Evidence)
		}
	}
}

// TestNoCloudControlListRowClaimsImplementedOnABareCall is the same guard
// widened past the three named types, so the defect cannot come back for
// any of the several hundred others. An "implemented" verdict under this
// mechanism means one thing and may only be reached one way: something was
// created and the following list enumerated it. A row that claims it
// without citing a create is a call being reported as an answer.
func TestNoCloudControlListRowClaimsImplementedOnABareCall(t *testing.T) {
	state := loadFlociCapabilities()
	rows, ok := state.types[pinnedDigest]
	if !ok {
		t.Fatalf("no manifest entry for the pinned digest %s", pinnedDigest)
	}

	var seen, implemented int
	for _, row := range rows {
		if row.Mechanism != "cloudcontrol-list" {
			continue
		}
		seen++
		if row.Status != string(FlociImplemented) {
			continue
		}
		implemented++
		if !strings.Contains(row.Evidence, "CreateResource") {
			t.Errorf("%s claims implemented without citing the create it round-tripped through: %q", row.Type, row.Evidence)
		}
		if strings.Contains(row.Evidence, "succeeded") {
			t.Errorf("%s evidence is the bare-call wording this mechanism was rewritten to stop producing: %q", row.Type, row.Evidence)
		}
	}
	if seen == 0 {
		t.Fatal("no cloudcontrol-list rows at the pinned digest at all - this guard is checking nothing")
	}
	if implemented == seen {
		t.Errorf("all %d cloudcontrol-list rows read implemented, which is the shape the old bare-call probe produced", seen)
	}
}
