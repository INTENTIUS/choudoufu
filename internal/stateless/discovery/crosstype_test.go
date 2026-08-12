// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/opentofu/opentofu/internal/stateless/identity"
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

// TestDiscoverModulePathMarkerIsMalformed: a marker naming an address inside
// a child module. Stateless mode v0 manages the root module only, so such a
// marker names nothing this run can bind or plan - and it used to be dropped
// silently by the same missing check.
func TestDiscoverModulePathMarkerIsMalformed(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_subnet", "subnet-in-a-module", `module.net:a.aws_subnet.this`)

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

	diags := classifyOrphans(Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
	}, res)

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
