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
	return directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read")
}

// directReadFixtureRequestDir is [directReadFixtureRequest] against an
// arbitrary fixture directory, for issue #1049's count and for_each
// variants: same settled-but-silent tag index, same shape, a different
// configuration.
func directReadFixtureRequestDir(t *testing.T, cloud *fakeCloud, dir string) Request {
	t.Helper()
	srv := &taggingServer{}
	// The index has caught up on some OTHER resource, never this one - what
	// makes this a lag rather than an outage: [markerIndex.available]
	// reports true and [markerIndex.marksAddress] still finds nothing here.
	markedARN(srv, "arn:aws:ec2:us-east-1:000000000000:vpc/vpc-caught-up", `aws_vpc.unrelated`)
	req := taggingRequest(t, srv)
	req.Provider = cloud

	cfg := loadConfig(t, dir)
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

// TestDirectReadBindsATagIndexLaggedCountInstance is issue #1049's primary
// fix: a terralith is mostly count-expanded IAM policies, and #1046's
// scalar-only fallback refused every one of them outright. Instance [1]'s
// own name argument reads count.index, so composing its candidate ARN needs
// [instanceRepetitionData] to bind count.index = 1 for this instance
// specifically - not 0, not 2 - before [composeIAMPolicyARN] can evaluate
// "team-${count.index}-policy".
func TestDirectReadBindsATagIndexLaggedCountInstance(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:policy/team-1-policy"

	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", arn, `aws_iam_policy.team_policy[1]`)
	// iam:ListPolicies returns no tags at all; stripTags reproduces that on
	// the list path while leaving the object itself listed and listable.
	stripTags(t, cloud, "aws_iam_policy", arn)
	cloud.withDirectReadTags(t, "aws_iam_policy", arn, map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_iam_policy.team_policy[1]`,
	})
	// Filler stands in for team_policy[0] and team_policy[2] plus the rest
	// of a large account's other policies, also unreadable - the ordinary
	// shape this run has to see past, not this test's point. Neither [0]
	// nor [2] has a live object in this fake cloud at all, so their own
	// direct reads come back "absent" and they keep the ordinary warning -
	// asserted below by name, so a regression that also binds them wrongly
	// would be caught.
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-count")

	t.Run("RED: without the fallback wired into bind, the plan proposes a duplicate create with no warning at all", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_policy[1]")); ok {
			t.Fatal("bound with the fallback's own bind()-site removed - this RED proof no longer isolates anything")
		}
		// A count block's own zero-claimant case never produces
		// ProblemUnreadableMarker at all - see bindtags.go's own doc
		// comment ("A count instance going unbound over an unreadable
		// object of its type is still silent") - so today's behaviour here
		// is not a warning to quote, it is the complete absence of one: the
		// plan simply proposes a create over an address whose live object
		// already exists.
		if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
			t.Fatalf("want no UNREADABLE_MARKER problems (count blocks never produce one), got %d:\n%s", len(found), res)
		}
		if found := problemsOfKind(res, ProblemDirectReadUnresolved); len(found) != 0 {
			t.Fatalf("want no DIRECT_READ_UNRESOLVED refusals with the fallback's own wiring removed, got %d:\n%s", len(found), res)
		}
		unbound := 0
		for _, u := range res.Unbound {
			if u.String() == "aws_iam_policy.team_policy[1]" {
				unbound++
			}
		}
		if unbound != 1 {
			t.Fatalf("want aws_iam_policy.team_policy[1] reported Unbound exactly once with no explanation, got %d\n%s", unbound, res)
		}
		t.Logf("RED, quoted verbatim: aws_iam_policy.team_policy[1] is Unbound with zero diagnostics - a silent create over a live object that already carries this estate's marker")
	})

	t.Run("GREEN: the direct read finds the real marker and binds instance [1] only", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)

		binding, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_policy[1]"))
		if !ok || binding.ImportID != arn {
			t.Fatalf("aws_iam_policy.team_policy[1] did not bind via the direct-read fallback: ok=%v binding=%+v\n%s", ok, binding, res)
		}
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_policy[0]")); ok {
			t.Error("team_policy[0] bound too - it has no live object at its own composed ARN")
		}
		if _, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_policy[2]")); ok {
			t.Error("team_policy[2] bound too - it has no live object at its own composed ARN")
		}
		scan, ok := res.ScanFor("aws_iam_policy")
		if !ok || scan.DirectRead != 3 {
			t.Errorf("aws_iam_policy scan = %+v, want DirectRead=3 (one attempt per count instance)", scan)
		}
	})
}

// TestDirectReadBindsATagIndexLaggedForEachInstance is #1049's for_each
// sibling: the collection (toset(["ops", "dev"])) is fully static, so
// [staticeval.ForEachElements] can enumerate it and bind each.key/each.value
// for one specific key ("ops") without touching the other ("dev").
func TestDirectReadBindsATagIndexLaggedForEachInstance(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:policy/team-ops-policy"

	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", arn, `aws_iam_policy.team_policy["ops"]`)
	stripTags(t, cloud, "aws_iam_policy", arn)
	cloud.withDirectReadTags(t, "aws_iam_policy", arn, map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_iam_policy.team_policy["ops"]`,
	})
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-foreach")

	t.Run("RED: without the fallback wired into bind, the plan proposes a duplicate create", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		if _, ok := res.BindingFor(mustAddr(t, `aws_iam_policy.team_policy["ops"]`)); ok {
			t.Fatal("bound with the fallback's own bind()-site removed - this RED proof no longer isolates anything")
		}
		found := problemsOfKind(res, ProblemUnreadableMarker)
		if len(found) != 2 {
			t.Fatalf("want one UNREADABLE_MARKER warning per for_each instance (today's behaviour), got %d:\n%s", len(found), res)
		}
		t.Logf("RED, quoted verbatim: %s", found[0].Detail)
	})

	t.Run("GREEN: the direct read finds the real marker and binds the \"ops\" key only", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)

		binding, ok := res.BindingFor(mustAddr(t, `aws_iam_policy.team_policy["ops"]`))
		if !ok || binding.ImportID != arn {
			t.Fatalf(`aws_iam_policy.team_policy["ops"] did not bind via the direct-read fallback: ok=%v binding=%+v`+"\n%s", ok, binding, res)
		}
		if _, ok := res.BindingFor(mustAddr(t, `aws_iam_policy.team_policy["dev"]`)); ok {
			t.Error(`team_policy["dev"] bound too - it has no live object at its own composed ARN`)
		}
		scan, ok := res.ScanFor("aws_iam_policy")
		if !ok || scan.DirectRead != 2 {
			t.Errorf("aws_iam_policy scan = %+v, want DirectRead=2 (one attempt per for_each instance)", scan)
		}
	})
}

// TestDirectReadRefusesADynamicForEachCollection is #1049's negative proof:
// a for_each over another managed resource (aws_subnet.this) produces real,
// addressable instances - identity.Resolve derives their keys from the
// parent block's own expansion, never from a read - but the collection
// itself is not evaluable through [staticeval.ForEachElements] (its root is
// a managed resource, not var/local/path/terraform), so
// [instanceRepetitionData] cannot bind each.key/each.value from
// configuration alone. This population must keep refusing exactly like
// #1046's original scalar-only restriction did, and the refusal must say
// WHY: the collection is not statically known, not merely "no scope".
func TestDirectReadRefusesADynamicForEachCollection(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")
	// noFilter, same as stripTags uses: an untagged filler object is exactly
	// what a server-side estate filter would otherwise drop before this run
	// ever saw it, and decl.unreadable (this fallback's own precondition)
	// needs at least one unreadable LISTED object of the type, not merely
	// one that exists.
	cloud.noFilter("aws_iam_policy")
	// No live object at all for either key: this test is about the refusal
	// firing before any read is even attempted, not about what a read would
	// have found.
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-foreach-dynamic")

	t.Run("RED: without the fallback wired into bind, this still just warns and proposes a create", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		found := problemsOfKind(res, ProblemUnreadableMarker)
		if len(found) != 2 {
			t.Fatalf("want one UNREADABLE_MARKER warning per for_each instance (today's behaviour), got %d:\n%s", len(found), res)
		}
		t.Logf("RED, quoted verbatim: %s", found[0].Detail)
	})

	t.Run("GREEN: the fallback refuses both instances, naming the collection as the reason", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		if !diags.HasErrors() {
			t.Fatalf("a dynamic for_each collection produced no error:\n%s", res)
		}
		for _, key := range []string{`"a"`, `"b"`} {
			addr := mustAddr(t, `aws_iam_policy.dynamic_policy[`+key+`]`)
			if _, ok := res.BindingFor(addr); ok {
				t.Errorf("%s bound despite its collection not being statically known", addr)
			}
		}
		found := problemsOfKind(res, ProblemDirectReadUnresolved)
		if len(found) != 2 {
			t.Fatalf("want one DIRECT_READ_UNRESOLVED refusal per instance, got %d:\n%s", len(found), res)
		}
		for _, p := range found {
			if p.Kind.Severity() != SeverityError {
				t.Error("the refusal is a warning; it must be an error")
			}
			if !strings.Contains(p.Detail, "not itself statically known") {
				t.Errorf("refusal does not name the collection as the reason: %s", p.Detail)
			}
		}
		t.Logf("GREEN, quoted verbatim: %s", found[0].Detail)
	})
}

// TestDirectReadBindsAForEachModuleVariable is issue #1063's primary fix:
// aws_iam_policy.pod_policy[0] is declared INSIDE module.team_pod["pod-a"],
// and its own `name` argument reads var.prefix - a variable whose value
// comes from team_pod's own for_each'd call (`prefix =
// "${local.name_prefix}-${each.key}"`), not from anything on the resource's
// own body. Before #1063, [moduleScope] did not exist and this module's
// var.* closure was frozen with no repetition data at all, so var.prefix
// could never be evaluated for ANY instance of this call - exactly the
// shape the issue was opened against
// (module.team_pod["pod-a"].aws_iam_policy.pod_policy[0]).
func TestDirectReadBindsAForEachModuleVariable(t *testing.T) {
	const arn = "arn:aws:iam::000000000000:policy/acme-pod-a-team-0000-policy"
	const addr = `module.team_pod["pod-a"].aws_iam_policy.pod_policy[0]`

	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", arn, addr)
	// iam:ListPolicies returns no tags at all; stripTags reproduces that on
	// the list path while leaving the object itself listed and listable.
	stripTags(t, cloud, "aws_iam_policy", arn)
	cloud.withDirectReadTags(t, "aws_iam_policy", arn, map[string]string{
		TagEstate:  estateName,
		TagAddress: addr,
	})
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-module-foreach")

	t.Run("RED: without the fallback wired into bind, the plan proposes a duplicate create with no warning at all", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		if _, ok := res.BindingFor(mustAddr(t, addr)); ok {
			t.Fatal("bound with the fallback's own bind()-site removed - this RED proof no longer isolates anything")
		}
		// This instance is COUNT-keyed inside the module (count =
		// var.pod_size), and a count block's own zero-claimant case never
		// produces ProblemUnreadableMarker at all - see bindtags.go's own
		// doc comment, and TestDirectReadBindsATagIndexLaggedCountInstance's
		// identical RED shape - so today's behaviour here is the complete
		// absence of a warning, not one to quote.
		if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
			t.Fatalf("want no UNREADABLE_MARKER problems (count blocks never produce one), got %d:\n%s", len(found), res)
		}
		unbound := 0
		for _, u := range res.Unbound {
			if u.String() == addr {
				unbound++
			}
		}
		if unbound != 1 {
			t.Fatalf("want %s reported Unbound exactly once with no explanation, got %d\n%s", addr, unbound, res)
		}
		t.Logf("RED, quoted verbatim: %s is Unbound with zero diagnostics - a silent create over a live object that already carries this estate's marker", addr)
	})

	t.Run("GREEN: the direct read composes the ARN through the module's own for_each'd variable and binds it", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)

		binding, ok := res.BindingFor(mustAddr(t, addr))
		if !ok || binding.ImportID != arn {
			t.Fatalf("%s did not bind via the direct-read fallback: ok=%v binding=%+v\n%s", addr, ok, binding, res)
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

// TestDirectReadRefusesAModuleArgumentNotStaticallyKnown is #1063's negative
// proof: team_pod's own for_each collection is fully static (same as the
// positive fixture above), so module.team_pod["pod-a"].aws_iam_policy.
// pod_policy[0] is a real, addressable instance - but the CALL's own
// `prefix` argument, which var.prefix's value comes from, reaches a data
// source this fork's static-only subset cannot answer. The refusal must
// name the MODULE CALL as the problem, not the resource's own body -
// distinguishable from both [TestDirectReadRefusesADynamicForEachCollection]
// (a resource's own collection unknown) and the generic "its name argument
// could not be evaluated" text a resource-level failure renders.
func TestDirectReadRefusesAModuleArgumentNotStaticallyKnown(t *testing.T) {
	const addr = `module.team_pod["pod-a"].aws_iam_policy.pod_policy[0]`

	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")
	// noFilter, same as the dynamic for_each collection test uses: an
	// untagged filler object is what a server-side estate filter would
	// otherwise drop before this run ever saw it, and decl.unreadable
	// (this fallback's own precondition) needs at least one unreadable
	// LISTED object of the type.
	cloud.noFilter("aws_iam_policy")
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)

	req := directReadFixtureRequestDir(t, cloud, "testdata/iam-policy-direct-read-module-foreach-dynamic")

	t.Run("RED: without the fallback wired into bind, the plan proposes a duplicate create with no warning at all", func(t *testing.T) {
		withDirectReadWiringRemoved(t)
		res, diags := Discover(context.Background(), req)
		assertNoErrors(t, diags)
		// COUNT-keyed inside the module, same as the positive fixture: no
		// ProblemUnreadableMarker at all, only a silent Unbound entry.
		if found := problemsOfKind(res, ProblemUnreadableMarker); len(found) != 0 {
			t.Fatalf("want no UNREADABLE_MARKER problems (count blocks never produce one), got %d:\n%s", len(found), res)
		}
		unbound := 0
		for _, u := range res.Unbound {
			if u.String() == addr {
				unbound++
			}
		}
		if unbound != 1 {
			t.Fatalf("want %s reported Unbound exactly once with no explanation, got %d\n%s", addr, unbound, res)
		}
		t.Logf("RED, quoted verbatim: %s is Unbound with zero diagnostics - a silent create over a live object that has no marker index entry yet", addr)
	})

	t.Run("GREEN: the fallback refuses, naming the module call as the reason", func(t *testing.T) {
		res, diags := Discover(context.Background(), req)
		if !diags.HasErrors() {
			t.Fatalf("a module call argument that cannot be evaluated produced no error:\n%s", res)
		}
		if _, ok := res.BindingFor(mustAddr(t, addr)); ok {
			t.Errorf("%s bound despite its module call's own argument not being statically known", addr)
		}
		found := problemsOfKind(res, ProblemDirectReadUnresolved)
		if len(found) != 1 {
			t.Fatalf("want exactly one DIRECT_READ_UNRESOLVED refusal, got %d:\n%s", len(found), res)
		}
		if found[0].Kind.Severity() != SeverityError {
			t.Error("the refusal is a warning; it must be an error")
		}
		if !strings.Contains(found[0].Detail, "module call") {
			t.Errorf("refusal does not name the module call as the reason: %s", found[0].Detail)
		}
		if strings.Contains(found[0].Detail, "its name (or path) argument could not be evaluated from configuration alone, even with its own per-instance scope in hand") {
			t.Errorf("refusal fell back to the generic resource-level text instead of naming the module call: %s", found[0].Detail)
		}
		t.Logf("GREEN, quoted verbatim: %s", found[0].Detail)
	})
}

// arnOnlyIdentity replaces one listed object's identity with {arn: id} - the
// shape terraform-provider-aws's real ListResource identity for
// aws_iam_policy carries, confirmed with TF_LOG=debug against a live run
// (live/gauntlet/logs/terralith-scale.log, 2026-09-11): exactly one
// attribute, arn, never account_id, on every floci digest tried. [fakeCloud]'s
// default identity ({id, region, account_id}) hands the account ID over
// ready-made and never exercises this - see
// TestDirectReadResolvesAccountIDFromARNOnlyIdentity's own doc comment for
// why that matters.
func arnOnlyIdentity(t *testing.T, cloud *fakeCloud, typeName, id string) {
	t.Helper()
	for _, o := range cloud.objects[typeName] {
		if o.id == id {
			o.identity = map[string]string{"arn": id}
			return
		}
	}
	t.Fatalf("no %s %q in the fake cloud to set an arn-only identity on", typeName, id)
}

// TestDirectReadResolvesAccountIDFromARNOnlyIdentity is issue #1054.
// [directReadFallback] reads the account ID to compose a candidate ARN from
// [TypeScan.AccountID] (directread.go), itself set only when a listed
// identity carries an "account_id" attribute (discovery.go's account-ID
// loop). Every existing #1046 test builds its fixture through
// [fakeCloud.own], whose default identity is {id, region, account_id} - it
// always hands the account ID over ready-made, so none of them exercise the
// shape the real provider actually serves for aws_iam_policy: an identity
// carrying arn alone (confirmed against a real run with TF_LOG=debug, see
// [arnOnlyIdentity]'s own doc comment). Before the #1045 floci repin
// (lex00/floci#202) this never mattered - the Tagging API incorrectly
// served IAM, so aws_iam_policy was always found through the ordinary tag
// index and directReadFallback's own account-ID read never had to run for
// real. The repin made floci stop serving IAM through the tag index, the
// way real AWS always has, and only then did a genuine tag-index-lagged
// aws_iam_policy reach this fallback and find TypeScan.AccountID empty:
// terralith-scale's own greenfield stage regressed from pass to fail this
// way (F5, "the greenfield plan with no local record store exited 1"; see
// the issue for the run's own verbatim detail). The fix reads the account
// ID out of the arn identity attribute itself
// ([cloudcontrol.ParseARN].Account) whenever no separate account_id
// attribute is present, so this population no longer depends on the
// identity schema happening to carry a second, redundant attribute.
func TestDirectReadResolvesAccountIDFromARNOnlyIdentity(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_policy")

	cloud.own("aws_iam_policy", directReadPolicyARN, `aws_iam_policy.team_0002_policy`)
	stripTags(t, cloud, "aws_iam_policy", directReadPolicyARN)
	cloud.withDirectReadTags(t, "aws_iam_policy", directReadPolicyARN, map[string]string{
		TagEstate:  estateName,
		TagAddress: `aws_iam_policy.team_0002_policy`,
	})
	cloud.obj("aws_iam_policy", "arn:aws:iam::000000000000:policy/filler", nil)
	arnOnlyIdentity(t, cloud, "aws_iam_policy", directReadPolicyARN)
	arnOnlyIdentity(t, cloud, "aws_iam_policy", "arn:aws:iam::000000000000:policy/filler")

	req := directReadFixtureRequest(t, cloud)

	res, diags := Discover(context.Background(), req)
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor("aws_iam_policy")
	if !ok || scan.AccountID != "000000000000" {
		t.Fatalf("aws_iam_policy scan did not resolve the account ID from its own arn-only identity: %+v", scan)
	}
	binding, ok := res.BindingFor(mustAddr(t, "aws_iam_policy.team_0002_policy"))
	if !ok || binding.ImportID != directReadPolicyARN {
		t.Fatalf("aws_iam_policy.team_0002_policy did not bind via the direct-read fallback with an arn-only identity: ok=%v binding=%+v\n%s", ok, binding, res)
	}
	if found := problemsOfKind(res, ProblemDirectReadUnresolved); len(found) != 0 {
		t.Errorf("a bound instance still carries a DIRECT_READ_UNRESOLVED refusal: %v", found)
	}
}
