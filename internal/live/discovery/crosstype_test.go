// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
)

// Audit finding C4's regression. A tofu-address marker names the resource it
// is written on, so its leading segment is that resource's own type. A marker
// naming another type's address is therefore malformed, and the one thing it
// must never be is invisible.
//
// It was invisible. The set of declared addresses discovery checked a marker
// against carried no type key, so a subnet tagged with an EIP's address
// matched "some declared address" and was skipped - not bound, not an orphan,
// not unclaimed, not a problem. A resource this estate owns disappeared from
// every section of the output, which is a hole in the owned/malformed/foreign
// trichotomy the whole marker spec rests on.

// TestDiscoverCrossTypeMarkerIsMalformed is the audit's own case: a subnet
// tagged tofu-address=aws_eip.pool:0, where aws_eip.pool[0] is a real
// declared address of another type.
func TestDiscoverCrossTypeMarkerIsMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-confused", `aws_eip.pool:0`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a cross-type marker produced no error:\n%s", res)
	}

	problems := res.ProblemsOfKind(ProblemMalformedMarker)
	if len(problems) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
	p := problems[0]
	if p.TypeName != "aws_subnet" {
		t.Errorf("the problem names type %q, want the live resource's own type", p.TypeName)
	}
	if strings.Join(p.LiveIDs, ",") != "subnet-confused" {
		t.Errorf("the problem does not name the live resource: %v", p.LiveIDs)
	}
	for _, want := range []string{"aws_eip", "aws_subnet", "aws_eip.pool:0"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("the problem does not name %q, so nobody can find the resource:\n%s", want, p.Detail)
		}
	}

	// And it is nothing else. The point of the finding is that a resource must
	// land in exactly one of the three buckets.
	if len(res.Orphans) != 0 || len(res.Unclaimed) != 0 || len(res.Bindings) != 0 {
		t.Errorf("a cross-type marker was also classified as something actionable:\n%s", res)
	}
	if !hasDiag(diags, "Malformed ownership marker", "subnet-confused") {
		t.Errorf("the diagnostic does not name the live resource:\n%s", renderDiags(diags))
	}

	// The EIP whose address it borrowed is untouched by any of this: its own
	// instances are still waiting to be found.
	if _, bound := res.BindingFor(mustAddr(t, `aws_eip.pool[0]`)); bound {
		t.Error("a subnet's marker bound an EIP instance")
	}
}

// TestDiscoverCrossTypeMarkerOnUndeclaredAddress: the same shape where the
// borrowed address belongs to no declared instance either. It is still a
// malformed marker rather than an orphan, because an orphan is a resource an
// address describes and this address describes a resource of another type.
func TestDiscoverCrossTypeMarkerOnUndeclaredAddress(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-confused", `aws_security_group.deleted`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a cross-type marker produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
	for _, o := range res.Orphans {
		if o.Removal {
			t.Errorf("a type-confused marker was turned into a destroy: %s", o.Marker)
		}
	}
}

// TestDiscoverModulePathMarkerIsMalformed is
// TestDiscoverCrossTypeMarkerIsMalformed's module-qualified analogue: a
// marker whose module prefix decodes fine (59c, issue #59 phase 3 - a keyed
// module step is no longer refused outright the way it was through 59b) but
// whose type segment names a different type than the live resource it is
// written on. A marker names the resource it is written on, module prefix or
// not, so this is still malformed rather than bound to the address it
// borrowed.
func TestDiscoverModulePathMarkerIsMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-in-a-module", `module.net:a.aws_vpc.main`)

	res, diags := discoverFixture(t, cloud, Request{})
	if !diags.HasErrors() {
		t.Fatalf("a module-path marker produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
}

// TestClassifyOrphansRefusesTypeConfusedDestroy is the second half of the fix,
// at the layer that proposes destroying things. The scan refuses cross-type
// markers at the source, so this drives classifyOrphans directly: whatever
// route an orphan arrives by, a destroy is never planned at an address of
// another type than the resource it would destroy.
func TestClassifyOrphansRefusesTypeConfusedDestroy(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))

	res := &Result{Estate: estateName}
	res.Orphans = []OwnedResource{{
		TypeName:   "aws_subnet",
		ImportID:   "subnet-confused",
		Marker:     `aws_eip.pool:0`,
		Normalized: `aws_eip.pool:0`,
	}}

	diags := classifyOrphans(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
	}, listclient.Schemas{}, res)

	if !diags.HasErrors() {
		t.Fatalf("a type-confused orphan was accepted:\n%s", res)
	}
	if res.Orphans[0].Removal {
		t.Error("a destroy was planned at an address of another type")
	}
	if res.Orphans[0].Withheld == "" {
		t.Error("the orphan was withheld with no reason recorded")
	}
	for _, r := range res.Resolutions {
		if r.Class == identity.ClassConcrete && r.Addr.String() == `aws_eip.pool[0]` {
			t.Error("a subnet's import ID was fed into the projection at an EIP's address")
		}
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
}

// TestDiscoverSweepStillSkipsClientNamedAddresses guards the behaviour the
// broken check was there to provide in the first place. The sweep lists
// client-named types nothing is waiting on, and a live bucket carrying its own
// declared address must not be read as an orphan - orphans are destroyed. Now
// that the check is keyed by type, it has to still hold for the type it is
// about.
func TestDiscoverSweepStillSkipsClientNamedAddresses(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.own("aws_s3_bucket", "tofu-stateless-e2e-data", `aws_s3_bucket.data`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	assertNoErrors(t, diags)

	if len(res.Orphans) != 0 {
		t.Errorf("a declared client-named resource was swept up as an orphan:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Errorf("a well-formed marker was reported as malformed:\n%s", res)
	}
}

// TestSweepCrossTypeMarkerOnUndeclaredTypeIsAWarning is
// reference-ec2-vpc/greenfield's own regression, at the layer that produced
// it. A greenfield apply marks aws_instance.main; the account then holds a
// SECOND live object - the primary network interface RunInstances created for
// that instance - carrying a copy of the instance's tags, marker included.
// aws_network_interface is a type this configuration never declares, so the
// only reason anybody looks at that object is the estate-wide sweep.
//
// Before the fix the sweep filed it as ProblemMalformedMarker, an ERROR, and
// every plan the estate ran failed with "Malformed ownership marker" - a
// refusal where stock OpenTofu, which sweeps nothing, plans an empty diff.
// The object is still refused in the sense that matters (nothing binds it,
// destroys it or retags it) and still named in the output, at warning
// severity.
//
// The marker assertions are by VALUE, on both objects: the instance binds to
// its own live id under the exact marker it was stamped with, and the copy on
// the network interface changes neither. A cross-type marker that started
// binding, or that moved the instance's own binding, is the wrong-marker
// failure this whole path exists to prevent, and reading the two identities
// off the result is the only way to see it.
func TestSweepCrossTypeMarkerOnUndeclaredTypeIsAWarning(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_instance")
	cloud.listable("aws_network_interface")
	cloud.own("aws_instance", "i-9e8b5b0575d98fa4e", `aws_instance.main`)
	// The propagated copy: a different live object, a different type, the
	// instance's marker verbatim.
	cloud.own("aws_network_interface", "eni-e5112ab22f3d2a82a", `aws_instance.main`)

	cfg := loadConfig(t, "testdata/propagated-child-marker")
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		Sweep:       true,
		SweepTypes:  []string{"aws_network_interface"},
	})
	if diags.HasErrors() {
		t.Fatalf("the propagated marker failed the run:\n%s", renderDiags(diags))
	}

	// The declared instance, by value: its own live object, under its own
	// marker, untouched by the copy.
	b, ok := res.BindingFor(mustAddr(t, `aws_instance.main`))
	if !ok {
		t.Fatalf("aws_instance.main is not bound:\n%s", res)
	}
	if b.ImportID != "i-9e8b5b0575d98fa4e" {
		t.Errorf("aws_instance.main bound to %q, want the instance's own live id", b.ImportID)
	}
	if b.Marker != `aws_instance.main` {
		t.Errorf("aws_instance.main bound under marker %q, want aws_instance.main exactly", b.Marker)
	}
	if b.TypeName != "aws_instance" {
		t.Errorf("aws_instance.main bound as type %q", b.TypeName)
	}
	if len(res.Bindings) != 1 {
		t.Errorf("want exactly one binding, got:\n%s", res)
	}

	// The copy, by value: reported, and nothing else.
	problems := res.ProblemsOfKind(ProblemUndeclaredCrossTypeMarker)
	if len(problems) != 1 {
		t.Fatalf("want one undeclared-cross-type-marker problem, got:\n%s", res)
	}
	p := problems[0]
	if p.Kind.Severity() != SeverityWarning {
		t.Errorf("the problem is %s, want a warning", p.Kind.Severity())
	}
	if p.TypeName != "aws_network_interface" {
		t.Errorf("the problem names type %q, want the live object's own type", p.TypeName)
	}
	if p.Marker != `aws_instance.main` {
		t.Errorf("the problem carries marker %q, want the value read off the object", p.Marker)
	}
	if strings.Join(p.LiveIDs, ",") != "eni-e5112ab22f3d2a82a" {
		t.Errorf("the problem does not name the live resource: %v", p.LiveIDs)
	}
	for _, want := range []string{"aws_network_interface", "aws_instance"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("the detail does not name %q, so nobody can find the object:\n%s", want, p.Detail)
		}
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 0 {
		t.Errorf("the propagated copy was also filed as malformed:\n%s", res)
	}
	if len(res.Orphans) != 0 || len(res.Unclaimed) != 0 {
		t.Errorf("the propagated copy was classified as something actionable:\n%s", res)
	}
	if !hasDiag(diags, "Cross-type marker on an undeclared type", "eni-e5112ab22f3d2a82a") {
		t.Errorf("the warning does not name the live resource:\n%s", renderDiags(diags))
	}
}

// TestSweepCrossTypeMarkerOnDeclaredTypeStillFails is the other half of the
// split, and the mutation control for the test above: the identical shape
// where the configuration DOES declare the type the object belongs to. An
// instance of that type is waiting to be found, so a marker naming another
// type's address is a conflict a human settles, and the run still stops.
// Flipping [undeclaredCrossTypeMarker] to ignore what the configuration
// declares - the obvious way to "fix" the greenfield failure - fails here.
func TestSweepCrossTypeMarkerOnDeclaredTypeStillFails(t *testing.T) {
	cloud := newFakeCloud()
	// aws_subnet is declared by the estate fixture, so this object arrives
	// through its own config-driven scan rather than the sweep.
	cloud.own("aws_subnet", "subnet-confused", `aws_eip.pool:0`)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true})
	if !diags.HasErrors() {
		t.Fatalf("a cross-type marker on a declared type produced no error:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemMalformedMarker)) != 1 {
		t.Fatalf("want one malformed-marker problem, got:\n%s", res)
	}
	if len(res.ProblemsOfKind(ProblemUndeclaredCrossTypeMarker)) != 0 {
		t.Errorf("a declared type's cross-type marker was downgraded to a warning:\n%s", res)
	}
}
