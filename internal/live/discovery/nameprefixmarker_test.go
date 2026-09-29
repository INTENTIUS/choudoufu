// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"
)

// GitHub issue #1212, pinned here because the only other thing that ever
// caught it is corpus-iam-policy's greenfield stage, which needs Docker,
// two emulator containers and about three minutes.
//
// # The shape
//
// An aws_iam_policy declared with `name_prefix` rather than `name`. IAM
// mints the name, so configuration states no name at all and
// [composeIAMPolicyARN] cannot build a candidate ARN - directread.go's
// whole leg is unavailable for this instance BY CONSTRUCTION, not because
// this run is missing something it could go and fetch. Its statically
// named sibling in the same estate recovers through that leg and this one
// does not, which is exactly what #1212 measured.
//
// # Why it is recoverable anyway, which is the part #1212 got wrong
//
// The marker is on the object. `name_prefix` moves the NAME off
// configuration; it moves nothing off the policy's tag set, and
// internal/live/stamp writes tofu-estate and tofu-address onto a
// name_prefix policy exactly as onto any other taggable resource. So the
// question was never "can this be recovered" but "does any route this run
// takes actually read that tag":
//
//   - iam:ListPolicies drops tags by design, so the native list call
//     returns the object with none (bindtags.go's own package comment).
//   - The Resource Groups Tagging API does not index iam:policy on the
//     pinned emulator (#1152, lex00/floci#205), so #266's join has nothing
//     to join against. Probed against the pin on 2026-09-17: a policy
//     created with tofu-estate reads that tag back through
//     iam:ListPolicyTags and does not appear in GetResources at all, while
//     an S3 bucket tagged in the same container does.
//   - iam:ListPolicyTags returns it. That is #1131's per-service tag-read
//     leg, which #1125 wired into [scanType] - the native-list arm - where
//     before it existed only on [scanTypeCloudControl].
//
// So this fixture's red arm is #1212's verbatim refusal and its green arm
// is the marker being read off the object by the one API that carries it.
// A type with no route, or a route that fails, keeps the refusal: see
// TestNativeServiceTagReadKeepsTheGapWhenTheReadFails for that direction on
// the sweep side.

// namePrefixPolicyARN is a minted name, suffix and all - what IAM actually
// returns for name_prefix = "example-", and not something configuration
// could have predicted. Taken from corpus-iam-policy's own recorded run
// (live/gauntlet.json, day2_replace).
const namePrefixPolicyARN = "arn:aws:iam::000000000000:policy/example-438145c3b8f365868243c7f283"

const namePrefixPolicyAddr = `aws_iam_policy.prefixed`

// namePrefixFixture is the shared cloud: one live, marked, DECLARED
// name_prefix policy listed natively with its tags stripped (iam:ListPolicies
// drops them), plus one untagged filler standing in for the rest of the
// account's policies.
func namePrefixFixture(t *testing.T) *fakeCloud {
	t.Helper()
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")
	cloud.own("aws_iam_policy", namePrefixPolicyARN, namePrefixPolicyAddr)
	stripTags(t, cloud, "aws_iam_policy", namePrefixPolicyARN)
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)
	return cloud
}

// TestNamePrefixPolicyRefusesWithNoServiceTagRoute is the red arm, and it is
// #1212 as filed: with no per-service tag reader on the request, every route
// that could carry the marker is exhausted and the run refuses rather than
// propose a create IAM would reject.
//
// It is a real assertion about product behaviour, not only a control. A run
// with no reader wired (req.ServiceTags nil is the default for every caller
// that does not opt in) must still refuse here rather than silently create.
func TestNamePrefixPolicyRefusesWithNoServiceTagRoute(t *testing.T) {
	cloud := namePrefixFixture(t)
	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-name-prefix")
	if req.ServiceTags != nil {
		t.Fatalf("the shared fixture now wires a service tag reader by default, so this arm no longer isolates the leg under test")
	}

	res, diags := Discover(context.Background(), req)
	if !diags.HasErrors() {
		t.Fatalf("a name_prefix policy with no readable marker route produced no error:\n%s", res)
	}
	if _, ok := res.BindingFor(mustAddr(t, namePrefixPolicyAddr)); ok {
		t.Errorf("%s bound with no route able to read its marker", namePrefixPolicyAddr)
	}
	found := problemsOfKind(res, ProblemDirectReadUnresolved)
	if len(found) != 1 {
		t.Fatalf("want exactly one DIRECT_READ_UNRESOLVED refusal, got %d:\n%s", len(found), res)
	}
	// Pinned verbatim against the issue's own quoted output, so a reword
	// that changes what an operator is told shows up here.
	const want = "its name (or path) argument could not be evaluated from configuration alone, even with its own per-instance scope in hand"
	if !strings.Contains(found[0].Detail, want) {
		t.Errorf("the refusal no longer names the unevaluable name argument as the reason.\n got: %s\nwant substring: %s", found[0].Detail, want)
	}
	t.Logf("RED, quoted verbatim: %s", found[0].Detail)
}

// TestNamePrefixPolicyBindsThroughTheServiceTagRead is the verdict line:
// with #1125's leg reachable, the marker on the object binds the instance,
// the refusal is gone, and nothing is proposed over a live policy the estate
// already owns.
func TestNamePrefixPolicyBindsThroughTheServiceTagRead(t *testing.T) {
	cloud := namePrefixFixture(t)
	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-name-prefix")
	reader := &fakeServiceTags{
		routes: map[string]bool{"aws_iam_policy": true},
		tags: map[string]map[string]string{
			namePrefixPolicyARN: {TagEstate: estateName, TagAddress: namePrefixPolicyAddr},
		},
	}
	req.ServiceTags = reader

	res, diags := Discover(context.Background(), req)
	assertNoErrors(t, diags)

	binding, ok := res.BindingFor(mustAddr(t, namePrefixPolicyAddr))
	if !ok || binding.ImportID != namePrefixPolicyARN {
		t.Fatalf("%s did not bind through the per-service tag read: ok=%v binding=%+v\n%s", namePrefixPolicyAddr, ok, binding, res)
	}
	if found := problemsOfKind(res, ProblemDirectReadUnresolved); len(found) != 0 {
		t.Errorf("a bound instance still carries a DIRECT_READ_UNRESOLVED refusal: %v", found)
	}
	if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
		t.Errorf("a bound instance still carries an UNREADABLE_MARKER warning: %v", found)
	}

	// Premise checks, so this cannot pass for the wrong reason. The
	// direct-read leg must NOT be what bound it - a name_prefix policy has
	// no composable ARN, and if DirectRead ever climbs above zero here the
	// fixture has stopped being the #1212 shape.
	scan, ok := res.ScanFor("aws_iam_policy")
	if !ok || scan.Source != SourceProvider {
		t.Fatalf("the aws_iam_policy scan is %+v (found=%v), want Source=%s - this fixture is not the native-enumeration shape", scan, ok, SourceProvider)
	}
	if scan.DirectRead != 0 {
		t.Errorf("the direct-read leg ran %d time(s) on a name_prefix policy whose ARN cannot be composed from configuration", scan.DirectRead)
	}
	if reader.calls != 2 {
		t.Errorf("the service tag reader was called %d time(s) for %v, want 2 - one per listed object with no readable marker", reader.calls, reader.askedFor)
	}
	if len(reader.askedFor) == 0 || reader.askedFor[0] != "aws_iam_policy "+namePrefixPolicyARN {
		t.Errorf("the reader was asked %v, want the policy's own ARN first - iam:ListPolicyTags takes a PolicyArn", reader.askedFor)
	}
}
