// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// GitHub issue #365, slice 2. The `markers "record"` selection is the first
// thing that ever tells this pass NOT to write a marker onto a resource that
// can carry one, so what it does has to be asserted by the tags this pass
// leaves behind rather than by a Skip reason - a skip is a report, and the
// tags are the product.
//
// The three claims below are the three ways this can go wrong, and the
// second and third are the ones that would be silent:
//
//  1. A selected resource gains no marker.
//  2. Every OTHER resource in the same configuration gains its markers
//     unchanged. A selection that widened by a module path, by a type
//     prefix, or by an address comparison that normalized too far would show
//     up here and nowhere else.
//  3. A selection this pass cannot verify - no schema for the type, or a
//     type whose identity a record cannot hold - is NOT honoured, and the
//     marker is written exactly as it always was. Honouring it would leave
//     a live object with neither a marker nor a usable record, which is the
//     "created unfindable" failure the safety rule exists to prevent.

// markersRecordSource renders a two-resource configuration whose live block
// selects whatever the caller names. Both resources are taggable types with
// a top-level string "id" in testSchemas(), and neither is in
// identity.IDNotProvenWholeTypes, so both are types a record CAN identify -
// which is what makes claim 2 above a real test rather than an accident of
// one of them being ineligible.
func markersRecordSource(selection string) string {
	return fmt.Sprintf(`
terraform {
  live {
    estate = "stamp-unit"

    record_store "local" {}

    strict {
      marker_repair = "never"

      markers "record" {
        %s
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    Owner = "platform"
  }
}

resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.42.1.0/24"
}
`, selection)
}

// TestStamp_markersRecordWithholdsOnlyTheSelectedMarker is claims 1 and 2,
// over both ways of naming a resource.
func TestStamp_markersRecordWithholdsOnlyTheSelectedMarker(t *testing.T) {
	for _, tc := range []struct {
		name      string
		selection string
	}{
		{"by type", `types = ["aws_vpc"]`},
		{"by address", `addresses = ["aws_vpc.main"]`},
		{"by both, naming the same resource twice", `types = ["aws_vpc"]
        addresses = ["aws_vpc.main"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadSource(t, markersRecordSource(tc.selection))

			res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
			assertNoErrors(t, diags)

			// Claim 1, by value: the selected resource keeps the operator's
			// own tag and gains neither marker. "No tofu-address" is the
			// whole point, so it is asserted as an exact tag set rather than
			// as an absence.
			assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
				"Owner": "platform",
			})

			// Claim 2, by value: the unselected sibling is stamped exactly as
			// it would be with no strict block at all.
			assertTags(t, evalTags(t, cfg, "aws_subnet.private", nil), map[string]string{
				"tofu-estate":  "stamp-unit",
				"tofu-address": "aws_subnet.private",
			})

			// And the report says what happened, so an operator reading a
			// plan is not left to infer a withheld marker from its absence.
			assertSkipReason(t, res, "aws_vpc.main", SkipMarkersRecord)
			if len(res.Stamped) != 1 || res.Stamped[0].Addr.String() != "aws_subnet.private" {
				t.Errorf("stamped %+v, want exactly aws_subnet.private", res.Stamped)
			}
		})
	}
}

// TestStamp_markersRecordIsNotHonouredWithoutASchema is claim 3's first half.
//
// With no schema for the selected type this pass cannot ask whether a record
// could hold its identity, and neither can internal/live/identity's resolver.
// Both decline, so the marker is written and the resolver classifies the
// instance exactly as it always did. The two agreeing is what matters: a
// pass that withheld the marker here while the resolver still called the
// instance needs-discovery would create it unfindable.
func TestStamp_markersRecordIsNotHonouredWithoutASchema(t *testing.T) {
	cfg := loadSource(t, markersRecordSource(`types = ["aws_vpc"]`))

	// Every schema except the selected type's.
	schemas := testSchemaSource{}
	for name, schema := range testSchemas().(testSchemaSource) {
		if name == "aws_vpc" {
			continue
		}
		schemas[name] = schema
	}

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: schemas})
	assertNoErrors(t, diags)

	// Unchanged: the author's tag only, because the pass could not read the
	// schema at all - which is SkipNoSchema's existing behavior and must not
	// have become "withheld deliberately".
	tags := evalTags(t, cfg, "aws_vpc.main", nil)
	if _, marked := tags["tofu-address"]; marked {
		t.Errorf("aws_vpc.main was stamped with no schema for its type: %v", tags)
	}
	assertTags(t, evalTags(t, cfg, "aws_subnet.private", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_subnet.private",
	})
}

// TestStamp_markersRecordIsNotHonouredForAnUnrecordableType is claim 3's
// second half, and the one that matters most.
//
// aws_glue_partition is in identity.IDNotProvenWholeTypes and has no
// identity.DocumentedImportIDs grammar: its documented import string is
// composite, nothing proves its exported `id` is the whole of it, and the
// page names one segment as the prose phrase "partition values" rather than
// as a token, which tools/row-gen/docimportid.go refuses to read by rule. An
// operator may select it - the grammar has no opinion - and this pass must
// refuse to act on the selection, because a resource with no marker whose
// record would hold a FRAGMENT of an identity is worse than one with a
// marker. internal/live/lint refuses the configuration outright; this is the
// independent leg, since this package never sees lint's verdict.
//
// The subject was aws_cognito_user_pool_client until 2026-08-22, when
// tools/importdocs-gen learned to read its page's possessive-of import
// sentence and gave it a grammar; a type with one is recordable, so it
// stopped being an example of this refusal. Both halves of the premise are
// asserted below rather than assumed.
//
// The schema it is given is a fully taggable one with a top-level string
// "id", so nothing about the schema is what refuses it. If a provider
// release or a documentation scrape ever proves this type's `id` whole, this
// test starts failing and the fix is to pick another member of the set, not
// to relax the assertion.
func TestStamp_markersRecordIsNotHonouredForAnUnrecordableType(t *testing.T) {
	const typeName = "aws_glue_partition"
	if _, unproven := identity.IDNotProvenWholeTypes[typeName]; !unproven {
		t.Skipf("%s left identity.IDNotProvenWholeTypes; pick another member for this test", typeName)
	}
	if _, described := identity.DocumentedImportIDs[typeName]; described {
		t.Skipf("%s gained a documented import grammar, so a record can hold its whole identity; "+
			"pick another member of identity.IDNotProvenWholeTypes that has none", typeName)
	}

	source := fmt.Sprintf(`
terraform {
  live {
    estate = "stamp-unit"

    record_store "local" {}

    strict {
      markers "record" {
        types = [%q]
      }
    }
  }
}

resource %q "app" {
  database_name = "db"
  table_name    = "tbl"
}
`, typeName, typeName)

	cfg := loadSource(t, source)

	schemas := testSchemaSource{typeName: taggedSchema("id", "database_name", "table_name")}

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: schemas})
	assertNoErrors(t, diags)

	// The marker is written, exactly as it is for any other taggable
	// resource: the selection was not honoured, and the resource is
	// findable.
	assertTags(t, evalTags(t, cfg, typeName+".app", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": typeName + ".app",
	})
}
