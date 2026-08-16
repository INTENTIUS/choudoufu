// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// A CLOUD_UNKNOWN refusal used to carry only the cloud property it was
// missing, which left every consumer able to say "the AWS account ID is not
// known here" and none of them able to say "or set catalog_id" - even though
// catalog_id is the entire fix, is in the provider's own Argument Reference,
// and is already sitting in the component the refusal came from. GitHub issue
// #250.
//
// The subjects are asserted here and the sentence built from them is asserted
// in internal/live/stamp, deliberately in both places: a slice with the right
// contents and a message that never reads it is exactly the shape that has
// shipped green in this repository before.

// TestCloudUnknownCauseArgsNameTheArgument holds both halves of the contract
// at once - the property is still first, and the arguments that answer it
// follow - over a fixture carrying a component that has such an argument and
// one that does not.
func TestCloudUnknownCauseArgsNameTheArgument(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-default-attr"), nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, tc := range []struct {
		addr string
		want []string
		why  string
	}{
		{
			addr: `aws_glue_catalog_database.own`,
			want: []string{"account-id", "catalog_id"},
			why:  "the provider documents catalog_id as defaulting to the caller's account, so naming it resolves the identity",
		},
		{
			addr: `aws_glue_catalog_database.maybe`,
			want: []string{"account-id", "catalog_id"},
			why:  "a conditionally-null catalog_id is an absence, and the way out of an absence is still to state it",
		},
		{
			addr: `aws_cloudfront_realtime_log_config.logs`,
			want: []string{"account-id"},
			why:  "the account is a bare ARN segment with no argument beside it, so there is no step to offer and none must be invented",
		},
	} {
		res, ok := result.Get(mustAddr(t, tc.addr))
		if !ok {
			t.Errorf("%s missing from the result", tc.addr)
			continue
		}
		if res.Cause != DiscoveryCloudUnknown {
			t.Errorf("%s cause = %q, want %q", tc.addr, res.Cause, DiscoveryCloudUnknown)
			continue
		}
		if len(res.CauseArgs) != len(tc.want) {
			t.Errorf("%s CauseArgs = %v, want %v (%s)", tc.addr, res.CauseArgs, tc.want, tc.why)
			continue
		}
		for i := range tc.want {
			if res.CauseArgs[i] != tc.want[i] {
				t.Errorf("%s CauseArgs = %v, want %v (%s)", tc.addr, res.CauseArgs, tc.want, tc.why)
				break
			}
		}
	}
}

// TestCloudUnknownBothShapesHaveAPopulation guards the thing the fixture
// above cannot: that the two branches the widening creates in
// stamp.UnmarkedDiscoveryDetail are both reachable from the shipped table.
//
// A cloud component with an argument beside it gets a next step; one without
// gets the sentence that has always been there. If the table ever held only
// one of those shapes, one branch would be dead code and the fixture would be
// asserting over a population of one - which is how a message defect stays
// green. The counts are not asserted, only that neither is zero, because the
// exact split is a fact about the provider release and moves with it.
func TestCloudUnknownBothShapesHaveAPopulation(t *testing.T) {
	withArgument := map[string][]string{}
	var bare []string
	for _, typeName := range AdmittedTypes() {
		entry, _ := LookupType(typeName)
		comp, ok := (&resolver{}).missingCloudComponent(entry, nil, instScope{}, mustAddr(t, "aws_vpc.x"), cloudScopeKey{})
		if !ok {
			continue
		}
		if len(comp.Attrs) > 0 {
			withArgument[typeName] = comp.Attrs
		} else {
			bare = append(bare, typeName)
		}
	}
	if len(withArgument) == 0 {
		t.Error("no admitted type produces a CLOUD_UNKNOWN whose component names an argument, so the sentence that offers a next step is unreachable")
	}
	if len(bare) == 0 {
		t.Error("every admitted type's first missing cloud component names an argument, so the sentence that offers no step is unreachable")
	}
}
