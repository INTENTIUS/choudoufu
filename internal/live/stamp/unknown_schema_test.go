// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #230's invariant, pinned where it is enforced rather than at
// one caller: a resource type whose own schema this run could not read is
// UNKNOWN, and unknown must not be reported as refused.
//
// The three tests below are one mutation experiment in three parts, over a
// single fixture. aws_route_table_association is server-assigned (nothing in
// its configuration names the rtbassoc- ID) and genuinely untaggable in the
// real provider, so with its schema present it must refuse - that is the
// control, and it is what makes the other two mean something. Take only its
// schema away and the refusal must become a warning, because nothing about
// its taggability was established. Nothing else about the fixture changes.

const unknownSchemaFixture = `
resource "aws_route_table_association" "app" {
  subnet_id      = "subnet-0123456789abcdef0"
  route_table_id = "rtb-0123456789abcdef0"
}
`

// schemasWithout is testSchemas() with one type's entry deleted outright, the
// shape a partial provider acquisition leaves behind (and the shape
// statelessProviders.resourceSchemas leaves behind when two providers serve
// one type name and it drops the ambiguity rather than picking a side).
func schemasWithout(typeName string) Schemas {
	src := testSchemas().(testSchemaSource)
	out := testSchemaSource{}
	for k, v := range src {
		if k == typeName {
			continue
		}
		out[k] = v
	}
	return out
}

// schemasWithNilBlock is the subtler half: the type's KEY is present, but the
// schema behind it carries no block. [stamper.resource] treats that
// identically to an absent key - it tests `schema == nil || schema.Block ==
// nil` - so any guard phrased as "is the key in the map" answers a different
// question from the one stamping asks. The first fix for #230 was phrased
// exactly that way, in internal/live/check, and this case would have walked
// straight past it into the fabricated refusal.
func schemasWithNilBlock(typeName string) Schemas {
	src := testSchemas().(testSchemaSource)
	out := testSchemaSource{}
	for k, v := range src {
		out[k] = v
	}
	out[typeName] = nil
	return out
}

// TestStamp_untaggableWithItsSchemaStillRefuses is the control. If this ever
// stops failing the run, the two tests below prove nothing.
func TestStamp_untaggableWithItsSchemaStillRefuses(t *testing.T) {
	cfg := loadSource(t, unknownSchemaFixture)

	res, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        testSchemas(),
		NeedsDiscovery: needsDiscovery("aws_route_table_association.app"),
	})
	if !diags.HasErrors() {
		t.Fatal("a marker-only resource of an untaggable type was allowed through with its schema present")
	}
	assertDiagContains(t, diags, SummaryUnmarkedApply, "aws_route_table_association.app")
	assertSkipReason(t, res, "aws_route_table_association.app", SkipUntaggable)
}

// TestStamp_absentSchemaIsAWarningNotARefusal is the same fixture with only
// that one type's schema removed.
func TestStamp_absentSchemaIsAWarningNotARefusal(t *testing.T) {
	cfg := loadSource(t, unknownSchemaFixture)

	res, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        schemasWithout("aws_route_table_association"),
		NeedsDiscovery: needsDiscovery("aws_route_table_association.app"),
	})
	assertUnknownSchemaOutcome(t, res, diags)
}

// TestStamp_nilSchemaBlockIsAWarningNotARefusal is the key-present,
// block-absent case. See [schemasWithNilBlock].
func TestStamp_nilSchemaBlockIsAWarningNotARefusal(t *testing.T) {
	cfg := loadSource(t, unknownSchemaFixture)

	res, diags := Stamp(t.Context(), Request{
		Estate:         "stamp-unit",
		Config:         cfg,
		Schemas:        schemasWithNilBlock("aws_route_table_association"),
		NeedsDiscovery: needsDiscovery("aws_route_table_association.app"),
	})
	assertUnknownSchemaOutcome(t, res, diags)
}

// assertUnknownSchemaOutcome is the whole contract for a must-stamp resource
// whose schema this run has not got: no error, a warning that SAYS unknown,
// and the skip recorded so a caller can see what happened. Silence would be
// the other way to get this wrong - the run would report nothing at all about
// a resource it could not check - so the warning is asserted, not merely the
// absence of the error.
func assertUnknownSchemaOutcome(t *testing.T, res *Result, diags tfdiags.Diagnostics) {
	t.Helper()

	if diags.HasErrors() {
		t.Fatalf("an unreadable schema was reported as a refusal: %s", diags.Err())
	}
	var warned bool
	for _, d := range diags {
		desc := d.Description()
		if d.Severity() != tfdiags.Warning || desc.Summary != SummaryNotStamped {
			continue
		}
		warned = true
		if !strings.Contains(desc.Detail, "is unknown") {
			t.Errorf("the warning does not say the answer is unknown: %s", desc.Detail)
		}
		if strings.Contains(desc.Detail, "never see again") {
			t.Errorf("the warning carries the must-stamp escalation's sentence: %s", desc.Detail)
		}
	}
	if !warned {
		t.Errorf("an unreadable schema produced no %q warning at all; unknown became silence: %v", SummaryNotStamped, diags)
	}
	assertSkipReason(t, res, "aws_route_table_association.app", SkipNoSchema)
}

func assertSkipReason(t *testing.T, res *Result, addr string, want SkipReason) {
	t.Helper()

	if res == nil {
		t.Fatal("no result")
	}
	for _, skip := range res.Skipped {
		if skip.Addr.String() == addr {
			if skip.Reason != want {
				t.Errorf("%s was skipped as %s, want %s", addr, skip.Reason, want)
			}
			return
		}
	}
	t.Errorf("%s is not in the skip list at all: %v", addr, res.Skipped)
}

// TestSkipReason_unknownIsOnlyTheUnreadableOne checks the predicate against
// the code that produces each reason rather than against itself: every skip
// reason except SkipNoSchema is recorded only on a path that has already read
// the type's schema block (stamper.resource returns at the schema check
// before any of them can be reached), so every one of them is a fact and none
// of them may read as unknown. A future reason that means "could not tell"
// belongs in Unknown too, and will fail here until it is added deliberately.
func TestSkipReason_unknownIsOnlyTheUnreadableOne(t *testing.T) {
	established := []SkipReason{
		SkipUntaggable,
		SkipAlreadyStamped,
		SkipTagsUnreadable,
		SkipMarkerUnreadable,
		SkipNotHCL,
		SkipUntagHandWritten,
		SkipModuleKeyed,
		SkipModuleKeyedTrusted,
	}
	for _, r := range established {
		if r.Unknown() {
			t.Errorf("%s reads as unknown, but it is only ever recorded after the type's schema block was read", r)
		}
	}
	if !SkipNoSchema.Unknown() {
		t.Errorf("%s does not read as unknown", SkipNoSchema)
	}
}
