// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// TestSweepDoesNotReListAConfigScannedUnservedType is GitHub issues
// #1037/#1039's mechanism, isolated from the emulator: aws_iam_policy's
// provider list resource carries no filter argument
// (supportsTagFilter false) AND the Resource Groups Tagging API never
// serves the "aws_iam_" service ([taggingAPIUnservedServices]), so
// [sweepTypes] adds a DECLARED aws_iam_policy back into the native sweep
// universe even though the config-driven loop already listed the whole
// account for it a moment earlier ([Discover]'s own scan-then-sweep
// ordering). Before GitHub issue #1037/#1039's fix, that meant the account's
// aws_iam_policy population - including a policy this estate does not own -
// was listed and its provider Read paid for a second time, unconditionally,
// on every plan.
//
// The fixture's own fakeCloud.ListResourceStream records one
// providers.ListResourceRequest per call it receives, which is what lets
// this test assert on the CALL rather than on a downstream predicate (see
// measuring-choudoufu's "assert on rendered identities... never on a
// predicate boolean", the closest analogue here since there is no rendered
// identity to check against - only the network call this fix removes).
func TestSweepDoesNotReListAConfigScannedUnservedType(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")
	// This estate's own instance, matching the declared block's address.
	cloud.own("aws_iam_policy", "owned-policy", `aws_iam_policy.owned`)
	// A policy this estate does not own at all - no tags, no marker. Before
	// the fix this object's full resource (the fake's stand-in for a real
	// provider's per-object Read, GetPolicyVersion on real AWS) is fetched
	// once by the config-driven scan and AGAIN by the redundant sweep call;
	// after the fix the second call, and so the second read, never happens.
	cloud.obj("aws_iam_policy", "unowned-policy", nil)

	srv := (&taggingServer{}).start(t)
	defer srv.Close()

	cfg := loadConfig(t, "testdata/iam-policy-sweep-dedup")
	req := Request{
		Estate:       estateName,
		Config:       cfg,
		Resolutions:  resolveOrFail(t, cfg).All(),
		Provider:     cloud,
		Sweep:        true,
		TaggingSweep: true,
		Tagging:      cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: srv.URL}),
		Roster:       taggingRoster(t, "aws_iam_policy", "AWS::IAM::Policy", true),
		SweepTypes:   []string{"aws_iam_policy"},
	}

	res, diags := Discover(context.Background(), req)
	if diags.HasErrors() {
		t.Fatalf("discovering the iam-policy-sweep-dedup fixture failed:\n%s\n%s", res, renderDiags(diags))
	}

	b, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.owned"))
	if !ok {
		t.Fatalf("aws_iam_policy.owned did not bind at all:\n%s", res)
	}
	if b.ImportID != "owned-policy" {
		t.Errorf("bound to import ID %q, want owned-policy", b.ImportID)
	}

	got := 0
	for _, r := range cloud.requests {
		if r.TypeName == "aws_iam_policy" {
			got++
		}
	}
	if got != 1 {
		t.Errorf("aws_iam_policy was listed (and so its per-object Read paid, including for the unowned policy) %d time(s), want exactly 1: the config-driven scan's own listing must be the only one - a second, redundant sweep listing is GitHub issue #1037/#1039's own defect", got)
	}
}
