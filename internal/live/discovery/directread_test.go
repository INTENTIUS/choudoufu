// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"
)

// GitHub issue #1046. See directread.go's own package doc comment for the
// mechanism: the Resource Groups Tagging API's search index lags a
// migration's own IAM tag writes by minutes at volume, aws_iam_policy's
// list call (iam:ListPolicies) carries no tags of its own either, and
// #266's tag join has nothing to join against while the index is still
// catching up - so a declared instance whose live object already carries
// the estate's real markers goes unbound and the plan proposes a create the
// provider will reject with EntityAlreadyExists.
//
// Both tests below drive [directReadFallback] with its call site in [bind]
// temporarily removed (see the "wiring removed" comment inline) to produce
// the RED proof against this fix's own mechanism, not against main's
// incompatible pre-#1046 source - main carries neither directread.go nor
// the fake cloud's ImportResourceState/ReadResource methods these tests
// need to compile at all.

const directReadPolicyARN = "arn:aws:iam::000000000000:policy/team-0002-policy"

// withDirectReadWiringRemoved flips [directReadFallbackEnabled] off for the
// duration of one test, restoring it on cleanup, so a RED sub-test can prove
// this fix's own bind()-site integration is what makes the difference -
// without it, exercising main's incompatible pre-#1046 source (which lacks
// this whole file and the fake cloud methods these tests need to compile).
func withDirectReadWiringRemoved(t *testing.T) {
	t.Helper()
	directReadFallbackEnabled = false
	t.Cleanup(func() { directReadFallbackEnabled = true })
}

// directReadFixtureRequest builds one aws_iam_policy discovery request
// against testdata/iam-policy-direct-read (a single instance with a static
// `name`, so its ARN is composable from configuration alone) with a
// Tagging client whose index is SETTLED but holds nothing for this
// address - #1046's own measured shape: a real GetResources call answered,
// 21 minutes after migrate, without yet knowing about this address.
func directReadFixtureRequest(t *testing.T, cloud *fakeCloud) Request {
	t.Helper()
	srv := &taggingServer{}
	// The index has caught up on some OTHER resource, never this one - what
	// makes this a lag rather than an outage: [markerIndex.available]
	// reports true and [markerIndex.marksAddress] still finds nothing here.
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-caught-up", `aws_vpc.unrelated`)
	req := taggingRequest(t, srv)
	req.Provider = cloud

	cfg := loadConfig(t, "testdata/iam-policy-direct-read")
	req.Estate = estateName
	req.Config = cfg
	req.Resolutions = resolveOrFail(t, cfg).All()
	return req
}

// TestDirectReadBindsATagIndexLaggedPolicy is #1046's primary fix: the
// live policy already carries this estate's real markers - a single-object
// Read would find them - and the plan must bind it rather than propose a
// duplicate.
func TestDirectReadBindsATagIndexLaggedPolicy(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", directReadPolicyARN, `aws_iam_policy.team_0002_policy`)
	// iam:ListPolicies returns no tags at all (bindtags.go's own package
	// doc comment); stripTags reproduces exactly that on the list path
	// while leaving the object itself listed and listable.
	stripTags(t, cloud, "aws_iam_policy", directReadPolicyARN)
	// A single-object Read (ListPolicyTags) still returns the real marker -
	// the fact #266's tag join has no way to reach while the estate-wide
	// index is still catching up.
	cloud.withDirectReadTags(t, "aws_iam_policy", directReadPolicyARN, map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_iam_policy.team_0002_policy`,
	})
	// Filler stands in for the rest of a large account's other policies,
	// also coming back with no tags - the ordinary shape this run has to
	// see past, not this test's point.
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequest(t, cloud)

	t.Run("RED: without the fallback wired into bind, the plan proposes a duplicate create", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_0002_policy")); ok {
			t.Fatal("bound with the fallback's own bind()-site removed - this RED proof no longer isolates anything")
		}
		found := problemsOfKind(res, ProblemUnreadableMarker)
		if len(found) != 1 {
			t.Fatalf("want exactly one UNREADABLE_MARKER warning (today's behaviour), got %d:\n%s", len(found), res)
		}
		t.Logf("RED, quoted verbatim: %s", found[0].Detail)
	})

	t.Run("GREEN: the direct read finds the real marker and binds it", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)

		binding, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_0002_policy"))
		if !ok || binding.ImportID != directReadPolicyARN {
			t.Fatalf("aws_iam_policy.team_0002_policy did not bind via the direct-read fallback: ok=%v binding=%+v\n%s", ok, binding, res)
		}
		if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
			t.Errorf("a bound instance still carries an UNREADABLE_MARKER warning: %v", found)
		}
		scan, ok := res.ScanFor("aws_iam_policy")
		if !ok || scan.DirectRead != 1 {
			t.Errorf("aws_iam_policy scan = %+v, want DirectRead=1", scan)
		}
	})
}

// TestDirectReadRefusesAForeignMarker is part 2: the composed candidate ARN
// really does have a live object, and it carries another estate's marker -
// so the create the ordinary warning would let through is not merely
// unverified, it is provably wrong to propose, and this run has to say so
// with an error rather than a footnote.
func TestDirectReadRefusesAForeignMarker(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", directReadPolicyARN, `aws_iam_policy.team_0002_policy`)
	stripTags(t, cloud, "aws_iam_policy", directReadPolicyARN)
	// The direct read finds a real object at the composed identity, and it
	// is somebody else's.
	cloud.withDirectReadTags(t, "aws_iam_policy", directReadPolicyARN, map[string]string{
		TagEstate:  "someone-elses-estate",
		TagAddress: `aws_iam_policy.team_0002_policy`,
	})
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequest(t, cloud)

	t.Run("RED: without the fallback wired into bind, this still just warns and proposes a create", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		if diags.HasErrors() {
			t.Fatalf("RED proof assumption broken: today's path already errors:\n%s", renderDiags(diags))
		}
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_0002_policy")); ok {
			t.Fatal("bound with the fallback's own bind()-site removed - this RED proof no longer isolates anything")
		}
		found := problemsOfKind(res, ProblemUnreadableMarker)
		if len(found) != 1 {
			t.Fatalf("want exactly one UNREADABLE_MARKER warning (today's behaviour), got %d:\n%s", len(found), res)
		}
		t.Logf("RED, quoted verbatim: severity=warning detail=%s", found[0].Detail)
	})

	t.Run("GREEN: the direct read finds a foreign marker and refuses", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		if !diags.HasErrors() {
			t.Fatalf("a foreign object at the composed identity produced no error:\n%s", res)
		}
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_0002_policy")); ok {
			t.Fatal("bound to an object carrying another estate's marker")
		}
		found := problemsOfKind(res, ProblemDirectReadUnresolved)
		if len(found) != 1 {
			t.Fatalf("want exactly one DIRECT_READ_UNRESOLVED refusal, got %d:\n%s", len(found), res)
		}
		if found[0].Kind.Severity() != SeverityError {
			t.Error("the refusal is a warning; it must be an error - the object it found proves the create would be wrong")
		}
		if got := found[0].Addr.String(); got != `aws_iam_policy.team_0002_policy` {
			t.Errorf("the refusal names %s, want aws_iam_policy.team_0002_policy", got)
		}
		t.Logf("GREEN, quoted verbatim: %s", found[0].Detail)
	})
}
