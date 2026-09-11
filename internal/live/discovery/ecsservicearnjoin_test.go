// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/listclient"
)

// TestECSServiceRoutesThroughTheTaggingLeg is GitHub issue #1039's other
// mechanism: unlike IAM ("aws_iam_" is in taggingAPIUnservedServices), the
// Resource Groups Tagging API genuinely indexes ECS, but [arnJoinTable] had
// no row for a service ARN's "service" segment, so [arnJoinReaches] answered
// false and [partitionSweepTypes] sent aws_ecs_service through the
// whole-account native per-type leg instead of riding the sweep's one
// estate-filtered GetResources call for free, the same way every other
// ARN-joinable type does.
//
// This is TestNativeSweepLegRoutesTheFixtureType's mirror: that file picks
// a type deliberately OUTSIDE arnJoinTable's coverage to prove it lands in
// the native leg; this asserts aws_ecs_service now lands in the TAGGING leg,
// by value rather than by predicate, so a regression that dropped the
// "service" row again would fail here rather than three tests later as an
// unexplained call count.
func TestECSServiceRoutesThroughTheTaggingLeg(t *testing.T) {
	req := Request{
		Sweep:        true,
		TaggingSweep: true,
		Roster:       taggingRoster(t, "aws_ecs_service", "AWS::ECS::Service", true),
		SweepTypes:   []string{"aws_ecs_service"},
	}
	decl, diags := declaredInstances(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("building an empty declared set: %s", renderDiags(diags))
	}
	tagging, native := partitionSweepTypes(req, listclient.Schemas{}, decl)
	for _, typeName := range native {
		if typeName == "aws_ecs_service" {
			t.Fatalf("aws_ecs_service is in the native leg, want the tagging leg: arnJoinTable's ecs/service row is missing or arnJoinReaches regressed")
		}
	}
	for _, typeName := range tagging {
		if typeName == "aws_ecs_service" {
			return
		}
	}
	t.Fatalf("aws_ecs_service is in neither leg (tagging=%v native=%v)", tagging, native)
}
