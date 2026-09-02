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

// #178's two buildable-now fixes for the "resource attr through a local's
// values (+ module-var variant)" bucket of the "Dynamic value in static
// context" wall rule: the key-set fix (staticForEachKeys) and the
// local-values fix (namedLeaf / resolveNamed / selectStatic), both in
// localvalue.go. Every fixture here reproduces, generalized past its
// specific resource names, a shape the corpus scoping pass in #178's
// comment thread found: simpleinfra/team-members-access's
// aws_iam_user.users for_each = local.users with name = each.key (the
// key-set fix, TestLocalAttrForEach) and cool-assessment's pattern of a
// resource attribute reached only through a local (the local-values fix,
// TestLocalAttrValue and friends).

// TestLocalAttrForEach is the key-set fix on its own: for_each ranges over
// an object constructor built from a local whose values are another
// resource's attribute, and the ranging resource's identity consumes only
// each.key. The key set has to resolve without ever evaluating the values.
func TestLocalAttrForEach(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-foreach"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_group.admins`:  `CONCRETE admins`,
		`aws_iam_group.readers`: `CONCRETE readers`,

		`aws_iam_user.team["alice"]`: `CONCRETE alice`,
		`aws_iam_user.team["bob"]`:   `CONCRETE bob`,
	})
}

// TestLocalAttrMerge extends the key-set fix through merge(): the for_each
// source is merge() of two object constructors, only one of which carries a
// resource-attribute value, and the key set is their union.
func TestLocalAttrMerge(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-merge"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	assertClassifications(t, result, map[string]string{
		`aws_iam_group.admins`: `CONCRETE admins`,

		`aws_iam_user.team["alice"]`: `CONCRETE alice`,
		`aws_iam_user.team["carol"]`: `CONCRETE carol`,
	})
}

// TestLocalAttrValue is the local-values fix on its own, no for_each
// involved: an identity argument reads local.zone_id, whose own definition
// is aws_route53_zone.public.zone_id. The zone is server-assigned
// (NEEDS_DISCOVERY), so the record has to come back PARENT_DERIVED with a
// Formula naming the zone - not a flat string, and not a refusal.
func TestLocalAttrValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-value"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	zone := resolutionAt(t, result, `aws_route53_zone.public`)
	if zone.Class != ClassNeedsDiscovery {
		t.Fatalf("the zone resolved %s; aws_route53_zone is server-assigned and should be NEEDS_DISCOVERY", zone.Class)
	}

	record := resolutionAt(t, result, `aws_route53_record.www`)
	if record.Class != ClassParentDerived {
		t.Fatalf("the record resolved %s, want PARENT_DERIVED - it depends on the zone's live ID", record.Class)
	}
	if want := "${aws_route53_zone.public.zone_id}_www_A"; record.Formula.String() != want {
		t.Errorf("the record's formula is %q, want %q", record.Formula.String(), want)
	}
}

// TestLocalAttrSelected pins that selecting one field of a mixed local -
// one field a resource reference, one a plain literal - resolves both
// correctly, and in particular that the literal sibling is not collateral
// damage: before the fix, evaluating local.zones as a whole to answer
// EITHER field raised the same diagnostic, because GetLocalValue evaluates
// the whole object regardless of which field was asked for.
func TestLocalAttrSelected(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-selected"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	dynamic := resolutionAt(t, result, `aws_route53_record.dynamic`)
	if dynamic.Class != ClassParentDerived {
		t.Fatalf("the dynamic record resolved %s, want PARENT_DERIVED", dynamic.Class)
	}
	if want := "${aws_route53_zone.public.zone_id}_dyn_A"; dynamic.Formula.String() != want {
		t.Errorf("the dynamic record's formula is %q, want %q", dynamic.Formula.String(), want)
	}

	static := resolutionAt(t, result, `aws_route53_record.static`)
	if static.Class != ClassConcrete {
		t.Fatalf("the static record resolved %s, want CONCRETE - its zone_id is a plain literal", static.Class)
	}
	if want := "Z000STATIC_stat_A"; static.ImportID != want {
		t.Errorf("the static record resolved to %q, want %q", static.ImportID, want)
	}
}

// TestLocalAttrModuleVar is the local-values fix's module-var variant
// (RESOURCE_MODULEVAR): the resource attribute crosses a module boundary as
// a call argument instead of through a local, and has to resolve the same
// way once it is read back out as var.zone_id inside the module.
func TestLocalAttrModuleVar(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "local-attr-module-var"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	record := resolutionAt(t, result, `module.dns.aws_route53_record.www`)
	if record.Class != ClassParentDerived {
		t.Fatalf("the record resolved %s, want PARENT_DERIVED", record.Class)
	}
	if want := "${aws_route53_zone.public.zone_id}_www_A"; record.Formula.String() != want {
		t.Errorf("the record's formula is %q, want %q", record.Formula.String(), want)
	}
	if len(record.Formula.Parents) != 1 || record.Formula.Parents[0].String() != "aws_route53_zone.public" {
		t.Errorf("the record's formula parents are %v, want [aws_route53_zone.public] - the reference crosses back into the ROOT module, not module.dns", record.Formula.Parents)
	}
}

// TestLocalAttrNonIdentity is the boundary the other fixtures are not
// allowed to move: reading a non-identity attribute of a parent through a
// local still refuses, exactly the way a direct reference already does
// (parentPart, resolve.go), rather than resolving to a value nothing about
// the parent's identity backs. aws_route53_zone's identity is
// [id, zone_id]; "name" is not in that list.
func TestLocalAttrNonIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "local-attr-non-identity"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("expected a refusal: local.zone_domain reaches a non-identity attribute (name) of aws_route53_zone")
	}
	if !hasDiag(diags, "Not an identity attribute", `"name" is not an identity attribute`) {
		t.Errorf("expected \"Not an identity attribute\" naming \"name\"; got:\n%s", renderDiags(diags))
	}
	if hasDiag(diags, "Dynamic value in static context", "") {
		t.Errorf("the generic static-context diagnostic should have been superseded by the specific one; got:\n%s", renderDiags(diags))
	}
}
