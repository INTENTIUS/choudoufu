// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestCacheVouchPassIsHermetic pins the review findings on #734/#737: the
// cache-vouching pass may produce vouching evidence and NOTHING else. A run
// with a cache file must be byte-identical in output and verdict to the same
// run without one, and every failure of the pass must degrade to "no
// vouches, the instances read" - never to a refusal the cacheless run would
// not have raised.
func TestCacheVouchPassIsHermetic(t *testing.T) {
	t.Run("an unlistable vouch type leaves zero trace", func(t *testing.T) {
		cloud := newFakeCloud()
		cloud.unlistable("aws_ecs_cluster")

		res, diags := Discover(context.Background(), Request{
			Estate: estateName,
			Config: ccConfig("aws_ecs_cluster"),
			Resolutions: []identity.Resolution{
				{Addr: mustAddr(t, "aws_ecs_cluster.x"), Class: identity.ClassConcrete, ImportID: "x"},
			},
			Provider:        cloud,
			CacheVouchTypes: []string{"aws_ecs_cluster"},
		})
		// Before the sandbox, this shape aborted the whole plan with
		// ProblemTypeNotListable at error severity - a refusal that
		// vanished when the cache file was deleted.
		if diags.HasErrors() {
			t.Fatalf("a failed vouch listing aborted the run: %s", diags.Err())
		}
		if len(res.Problems) != 0 {
			t.Errorf("the vouch pass filed %d problem(s) into the report: %v", len(res.Problems), res.Problems)
		}
		if len(res.Unclaimed) != 0 {
			t.Errorf("the vouch pass filed %d unclaimed item(s): %v", len(res.Unclaimed), res.Unclaimed)
		}
		if res.CacheVouchSightings != nil {
			t.Errorf("an unlistable type produced sightings: %v", res.CacheVouchSightings)
		}
	})

	t.Run("an unmarked sighting becomes vouch evidence, never a foreign item", func(t *testing.T) {
		cloud := newFakeCloud()
		cloud.listable("aws_iam_role")
		// noFilter forces the client-side listing an IAM-shaped type has in
		// production (no server-side tag filter argument): the whole point
		// of the vouch pass is types whose unmarked population is visible
		// to a plain list. A server-filterable type never sights unmarked
		// objects at all, and its instances simply read - also hermetic.
		cloud.noFilter("aws_iam_role")
		cloud.obj("aws_iam_role", "role-a", map[string]string{"Name": "role-a"})

		res, diags := Discover(context.Background(), Request{
			Estate: estateName,
			Config: ccConfig("aws_iam_role"),
			Resolutions: []identity.Resolution{
				{Addr: mustAddr(t, "aws_iam_role.x"), Class: identity.ClassConcrete, ImportID: "role-a"},
			},
			Provider:        cloud,
			CacheVouchTypes: []string{"aws_iam_role"},
			VouchProvider:   testProviderAddr(t, ""),
		})
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if !res.CacheVouchSightings.Sighted(testProviderAddr(t, ""), "aws_iam_role", "role-a") {
			t.Errorf("the listed identity is missing from CacheVouchSightings: %v", res.CacheVouchSightings)
		}
		// The hermetic half: the sighting must NOT ride through Unclaimed,
		// which is what the foreign report and the adoption candidates are
		// built from - an unmarked neighbour would otherwise appear as a
		// Foreign line only when a cache file was present.
		if len(res.Unclaimed) != 0 {
			t.Errorf("the vouch sighting leaked into Unclaimed: %v", res.Unclaimed)
		}
		if len(res.Problems) != 0 {
			t.Errorf("the vouch pass filed problems: %v", res.Problems)
		}
	})
}
