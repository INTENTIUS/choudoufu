// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// aws_sqs_queue is this file's stand-in for "an admitted type in the native
// sweep leg". SQS is not one of the thirteen services arnJoinTable covers
// (see [partitionSweepTypes]'s doc comment), so with a roster that maps it,
// [arnJoinReaches] is false and the type routes to the per-type leg, which
// is the leg this file's change narrows. Nothing here depends on SQS
// specifically; any type outside arnJoinTable would do, and the assertion
// below that it really did route native fails loudly if that ever changes.
const nativeLegType = "aws_sqs_queue"

// nativeSweepStore builds a real local record store holding one located
// record per address given, and returns the raw handle Request.HintStore
// takes.
func nativeSweepStore(t *testing.T, addresses ...string) staterecord.Store {
	t.Helper()

	raw, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := projection.NewRecordEnvelopeStore(raw, projection.RecordKeyPrefix(estateName))
	for _, addr := range addresses {
		if _, err := projection.SeedLocatedForInstance(context.Background(), store, mustAddr(t, addr), addrs.AbsProviderConfig{}, projection.LocatedRecord{
			Components: map[string]string{"name": "whatever"},
		}); err != nil {
			t.Fatalf("seeding a record for %s: %s", addr, err)
		}
	}
	return raw
}

// nativeSweepRequest is the production Request shape for these tests: the
// tagging sweep on, with a roster that maps nativeLegType to a CFN type
// arnJoinTable does not cover, so the type lands in the native leg.
func nativeSweepRequest(t *testing.T, taggingURL string) Request {
	t.Helper()
	return Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: taggingURL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, nativeLegType, "AWS::SQS::Queue", true),
	}
}

// TestNativeSweepLegRoutesTheFixtureType is the premise every other test in
// this file rests on, asserted rather than assumed: nativeLegType really is
// routed to the per-type leg by [partitionSweepTypes] under the roster
// above. If arnJoinTable grows an SQS row this fails here, pointing at the
// premise, instead of failing three tests later as an unexplained call
// count.
func TestNativeSweepLegRoutesTheFixtureType(t *testing.T) {
	req := nativeSweepRequest(t, "http://127.0.0.1:1")
	decl, diags := declaredInstances(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("building an empty declared set: %s", renderDiags(diags))
	}
	_, native := partitionSweepTypes(req, decl)
	for _, typeName := range native {
		if typeName == nativeLegType {
			return
		}
	}
	t.Fatalf("%s is not in the native sweep leg under this file's roster; arnJoinTable now covers it, so pick another type outside arnJoinTable's thirteen services", nativeLegType)
}

// TestNativeSweepNarrowsToEstateEvidence is the measurement this change is
// for, made on the one observable that matters: which types the provider
// was actually asked to list.
//
// It runs the same estate four ways and asserts by value, never on a
// predicate, that a type this estate has no evidence of costs a list call in
// exactly the cases rfc/20260830-stale-state-charter.md says it should.
func TestNativeSweepNarrowsToEstateEvidence(t *testing.T) {
	srv := (&taggingServer{}).start(t)
	defer srv.Close()

	// listedNativeType runs one Discover pass and reports whether the
	// provider was asked to list nativeLegType. The fake cloud records
	// every ListResourceRequest it receives, so this is the call itself
	// and not a claim about it.
	listedNativeType := func(t *testing.T, mutate func(*Request)) bool {
		t.Helper()
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable(nativeLegType)
		req := nativeSweepRequest(t, srv.URL)
		mutate(&req)
		_, diags := discoverFixture(t, cloud, req)
		assertNoErrors(t, diags)
		_, listed := cloud.requestFor(nativeLegType)
		return listed
	}

	t.Run("no record store sweeps in full", func(t *testing.T) {
		if !listedNativeType(t, func(*Request) {}) {
			t.Errorf("%s was not listed with no record store to narrow by. An estate with no record of itself has only its markers to find its own resources with, so this pass must sweep in full - see nativesweep.go.", nativeLegType)
		}
	})

	t.Run("empty record store sweeps in full", func(t *testing.T) {
		if !listedNativeType(t, func(req *Request) { req.HintStore = nativeSweepStore(t) }) {
			t.Errorf("%s was not listed with an EMPTY record store. Empty is not evidence of absence: it is the rebuild-from-markers case, which must sweep in full.", nativeLegType)
		}
	})

	t.Run("CollectUnclaimed sweeps in full", func(t *testing.T) {
		if !listedNativeType(t, func(req *Request) {
			req.CollectUnclaimed = true
			req.HintStore = nativeSweepStore(t, `aws_vpc.main`)
		}) {
			t.Errorf("%s was not listed even though CollectUnclaimed was set. That flag IS the account-inventory question, and narrowing it away would leave no way to ask it at all.", nativeLegType)
		}
	})

	t.Run("a record store with no evidence of the type skips it", func(t *testing.T) {
		if listedNativeType(t, func(req *Request) { req.HintStore = nativeSweepStore(t, `aws_vpc.main`) }) {
			t.Errorf("%s was listed even though this estate's configuration and record store contain no instance of it and CollectUnclaimed was unset. That list call is the cost rfc/20260830-stale-state-charter.md's ruling removes.", nativeLegType)
		}
	})

	t.Run("a record naming the type keeps it swept", func(t *testing.T) {
		if !listedNativeType(t, func(req *Request) {
			req.HintStore = nativeSweepStore(t, nativeLegType+".gone")
		}) {
			t.Errorf("%s was not listed even though the estate's own record store holds an instance of it. A record for a type is exactly the evidence that this estate may own an object of it whose block has since been deleted, which is the removal the native leg exists to find.", nativeLegType)
		}
	})
}

// TestNativeSweepNarrowingIsReported pins that a narrowed pass says so.
// "We looked and found nothing" and "we did not look" are different
// answers, and [Result.NativeSweepSkipped] is what keeps the run's own
// report able to tell them apart - see internal/command/views/live_plan.go's
// Foreign section, which renders it.
func TestNativeSweepNarrowingIsReported(t *testing.T) {
	srv := (&taggingServer{}).start(t)
	defer srv.Close()

	cloud := newFakeCloud()
	ownWholeEstate(cloud)
	cloud.listable(nativeLegType)

	req := nativeSweepRequest(t, srv.URL)
	req.HintStore = nativeSweepStore(t, `aws_vpc.main`)
	narrowed, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)
	if narrowed.NativeSweepSkipped == 0 {
		t.Fatal("a narrowed pass reported NativeSweepSkipped = 0, so nothing downstream can tell an operator this run did not ask the account-wide question")
	}

	cloud2 := newFakeCloud()
	ownWholeEstate(cloud2)
	cloud2.listable(nativeLegType)
	req2 := nativeSweepRequest(t, srv.URL)
	req2.CollectUnclaimed = true
	req2.HintStore = nativeSweepStore(t, `aws_vpc.main`)
	full, diags := discoverFixture(t, cloud2, req2)
	assertNoErrors(t, diags)
	if full.NativeSweepSkipped != 0 {
		t.Errorf("a pass that asked the account-wide question reported NativeSweepSkipped = %d, want 0: nothing was skipped, so nothing should be reported as skipped", full.NativeSweepSkipped)
	}
}

// TestNarrowedNativeSweepStillProposesRemovals is the correctness half, and
// it is deliberately the same fixture TestTaggingSweepFindsDeletedBlock uses:
// a live resource this estate owns whose resource block the configuration no
// longer declares is still found, and still proposed for destruction, on a
// narrowed pass. The narrowing touches the per-type leg only; the tagging
// leg's one estate-filtered GetResources call is what finds this, and it is
// not narrowed by anything.
func TestNarrowedNativeSweepStillProposesRemovals(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	arn := "arn:aws:logs:us-east-1:123456789012:log-group:/estate/deleted"
	srv := &taggingServer{
		arns: []string{arn},
		tags: map[string]map[string]string{
			arn: {TagEstate: estateName, TagAddress: `aws_cloudwatch_log_group.deleted`},
		},
	}
	server := srv.start(t)
	defer server.Close()

	req := Request{
		Sweep:        true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: server.URL}),
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_cloudwatch_log_group", "AWS::Logs::LogGroup", true),
		// The narrowing condition: no CollectUnclaimed, and a record store
		// with an entry, so estateScopedNativeSweep has evidence to narrow
		// by and does.
		HintStore: nativeSweepStore(t, `aws_vpc.main`),
	}
	res, diags := discoverFixture(t, cloud, req)
	assertNoErrors(t, diags)

	if res.NativeSweepSkipped == 0 {
		t.Fatal("this pass did not narrow, so it proves nothing about a narrowed one; check estateScopedNativeSweep's preconditions")
	}
	rm := removalsByAddr(res)
	o, ok := rm[`aws_cloudwatch_log_group.deleted`]
	if !ok {
		t.Fatalf("the deleted block's resource is not a removal on a narrowed pass:\n%s", res)
	}
	if o.ImportID != "/estate/deleted" {
		t.Errorf("ImportID = %q, want /estate/deleted", o.ImportID)
	}
}
