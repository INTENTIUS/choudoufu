// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestDiscoverDeposedDisambiguation is GitHub issue #361's crash-window
// recovery, at the collision-breaking seam: two live vpc-shaped objects
// both carry aws_vpc.main's marker, exactly the shape a create-before-
// destroy replace interrupted between the create's commit and the old
// object's destroy leaves behind. With no recorded deposed candidate this
// is TestDiscoverCollision's own fixture, byte for byte, and it still
// raises ProblemCollision (see the sibling tests below). Naming vpc-old as
// a recorded deposed object for the address breaks the tie: vpc-old is
// pulled out into Result.DeposedBindings and vpc-new binds through the
// ordinary case-1 path, with no collision problem raised at all.
func TestDiscoverDeposedDisambiguation(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-old", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-new", `aws_vpc.main`)

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		DeposedRecords: map[string]map[string]projection.DeposedRecord{
			addr.String(): {
				"deadbeef": {ImportID: "vpc-old", Provider: `provider["registry.opentofu.org/hashicorp/aws"]`},
			},
		},
	})
	assertNoErrors(t, diags)

	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 0 {
		t.Fatalf("a disambiguated deposed pair still raised a collision:\n%s", res)
	}

	if len(res.DeposedBindings) != 1 {
		t.Fatalf("want exactly one deposed binding, got %d:\n%s", len(res.DeposedBindings), res)
	}
	db := res.DeposedBindings[0]
	if db.Addr.String() != addr.String() {
		t.Errorf("deposed binding addr = %s, want %s", db.Addr, addr)
	}
	if string(db.DeposedKey) != "deadbeef" {
		t.Errorf("deposed binding key = %q, want %q", db.DeposedKey, "deadbeef")
	}
	if db.ImportID != "vpc-old" {
		t.Errorf("deposed binding import id = %q, want %q", db.ImportID, "vpc-old")
	}

	b, bound := res.BindingFor(addr)
	if !bound {
		t.Fatalf("the remaining claimant was not bound at all:\n%s", res)
	}
	if b.ImportID != "vpc-new" {
		t.Errorf("the address bound to %q, want the NEW object vpc-new", b.ImportID)
	}
}

// TestDiscoverDeposedDisambiguationNoMatchStillCollides: the record names a
// deposed candidate for the address, but neither live claimant matches it -
// stale, or simply wrong. Zero matches must still raise ProblemCollision
// exactly as before this mechanism existed; a record is a hint, never
// authority, and this is the case where trusting it would silently destroy
// or misclassify an object neither the record nor a live read agrees on.
func TestDiscoverDeposedDisambiguationNoMatchStillCollides(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		DeposedRecords: map[string]map[string]projection.DeposedRecord{
			addr.String(): {
				"deadbeef": {ImportID: "vpc-neither-claimant"},
			},
		},
	})
	if !diags.HasErrors() {
		t.Fatalf("a non-matching deposed record silently resolved a collision:\n%s", res)
	}
	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want one collision problem when the record matches nothing live, got:\n%s", res)
	}
	if len(res.DeposedBindings) != 0 {
		t.Errorf("a deposed binding was produced with no matching claimant:\n%s", res)
	}
}

// TestDiscoverDeposedDisambiguationBothMatchStillCollides: two DIFFERENT
// recorded deposed keys each match a different one of the two claimants -
// an operator or a corrupted record naming both live objects as deposed at
// once. More than one match is exactly as unresolvable as zero: this must
// still raise ProblemCollision, never guess which of the two is "the" new
// object.
func TestDiscoverDeposedDisambiguationBothMatchStillCollides(t *testing.T) {
	cloud := newFakeCloud()
	cloud.own("aws_vpc", "vpc-1", `aws_vpc.main`)
	cloud.own("aws_vpc", "vpc-2", `aws_vpc.main`)

	addr := mustAddr(t, `aws_vpc.main`)
	res, diags := discoverFixture(t, cloud, Request{
		DeposedRecords: map[string]map[string]projection.DeposedRecord{
			addr.String(): {
				"deadbeef": {ImportID: "vpc-1"},
				"beefdead": {ImportID: "vpc-2"},
			},
		},
	})
	if !diags.HasErrors() {
		t.Fatalf("a doubly-matching deposed record silently resolved a collision:\n%s", res)
	}
	if problems := res.ProblemsOfKind(ProblemCollision); len(problems) != 1 {
		t.Fatalf("want one collision problem when the record matches both claimants, got:\n%s", res)
	}
	if len(res.DeposedBindings) != 0 {
		t.Errorf("a deposed binding was produced when both claimants matched:\n%s", res)
	}
	if !hasDiag(diags, "Two live resources claiming one address", "vpc-1") {
		t.Errorf("the diagnostic does not name the colliding resources:\n%s", renderDiags(diags))
	}
}
