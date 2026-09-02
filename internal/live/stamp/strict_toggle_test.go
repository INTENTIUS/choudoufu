// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"testing"
)

// The strict block's marker_repair toggle (GitHub issue #365) is the one
// setting an operator could read as "this tool will stop touching my tags",
// so what it does to STAMPING has to be asserted by value rather than
// reasoned about. HANDOFF.md's safety rule is the reason: a refusal is loud
// and reversible, a wrong marker is silent, and "convergence is never
// evidence an identity is right".
//
// Two claims, and the second is the one that has to survive the next slice.
//
// First: a strict block changes nothing this package does today. It is
// decoded (internal/configs) and validated (internal/live/lint) and read by
// nothing else, which is deliberate - see internal/live/strict.Implemented
// for why the mechanism is not here. These tests say so by value instead of
// by grep.
//
// Second, and the point of pinning it here rather than in the lint package:
// none of the three settings is about CREATING a marker. The safety rule has
// no converse permitting an unmarked create, so a resource this pass stamps
// for the first time must gain both markers whatever the strict block says.
// A future slice that wires marker_repair into a plan-time path has to leave
// this test green; if it cannot, it has changed the wrong thing.

// strictSource renders a one-resource configuration whose live block carries
// a strict block with the given body, or no strict block at all.
func strictSource(hasBlock bool, strictBody string) string {
	block := ""
	if hasBlock {
		block = fmt.Sprintf("\n    strict {\n      %s\n    }", strictBody)
	}
	return fmt.Sprintf(`
terraform {
  live {
    estate = "stamp-unit"%s
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    Owner = "platform"
  }
}
`, block)
}

// TestStamp_strictMarkerRepairDoesNotChangeAFreshStamp: whatever the strict
// block says - absent, empty, or naming any of the three marker_repair
// settings - a resource with no markers gains both, with the same values.
//
// The settings that lint refuses today are included on purpose. This package
// never sees lint's verdict, so "the refusal protects us" is not an argument
// available to it, and a build that later lifts the refusal must not
// discover that stamping quietly changed underneath.
func TestStamp_strictMarkerRepairDoesNotChangeAFreshStamp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hasBlock bool
		strict   string
	}{
		{"no strict block", false, ""},
		{"empty strict block", true, ""},
		{"repair", true, `marker_repair = "repair"`},
		{"report", true, `marker_repair = "report"`},
		{"never", true, `marker_repair = "never"`},
		{"outside the vocabulary", true, `marker_repair = "sometimes"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadSource(t, strictSource(tc.hasBlock, tc.strict))

			res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
			assertNoErrors(t, diags)

			if len(res.Stamped) != 1 {
				t.Fatalf("stamped %d resources, want 1: %+v", len(res.Stamped), res.Stamped)
			}
			if got := res.Stamped[0].Addr.String(); got != "aws_vpc.main" {
				t.Errorf("stamped %s, want aws_vpc.main", got)
			}

			// The claim, by value. A create is stamped whatever the toggle
			// says, and the operator's own tag survives beside the markers.
			assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
				"Owner":        "platform",
				"tofu-estate":  "stamp-unit",
				"tofu-address": "aws_vpc.main",
			})
		})
	}
}

// TestStamp_strictMarkerRepairDoesNotChangeADisagreeingMarker: the case the
// toggle is NAMED for, asserted to be untouched by it.
//
// A configuration declaring a tofu-address that is not this resource's
// address is a conflict, and this package's answer is a hard error that
// rewrites nothing - `verify` is documented as never returning "overwrite".
// That answer is identical with and without a strict block, which is the
// concrete form of the finding behind #365's first slice: there is no repair
// path inside this package for marker_repair to gate. The repair of a
// DRIFTED LIVE tag is the provider's ordinary tags diff, downstream of here.
//
// If a later slice makes marker_repair suppress that diff, this test still
// has to hold: suppressing a repair must never turn into writing a different
// marker, and it must never turn this loud conflict into silence.
func TestStamp_strictMarkerRepairDoesNotChangeADisagreeingMarker(t *testing.T) {
	const body = `
terraform {
  live {
    estate = "stamp-unit"%s
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    "tofu-estate"  = "stamp-unit"
    "tofu-address" = "aws_vpc.somewhere_else"
  }
}
`

	for _, tc := range []struct {
		name   string
		strict string
	}{
		{"no strict block", ""},
		{"repair", "\n    strict {\n      marker_repair = \"repair\"\n    }"},
		{"report", "\n    strict {\n      marker_repair = \"report\"\n    }"},
		{"never", "\n    strict {\n      marker_repair = \"never\"\n    }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadSource(t, fmt.Sprintf(body, tc.strict))

			_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
			if !diags.HasErrors() {
				t.Fatal("a tofu-address naming another address was accepted")
			}
			var conflicts int
			for _, d := range diags {
				if d.Description().Summary == SummaryMarkerConflict {
					conflicts++
				}
			}
			if conflicts != 1 {
				t.Fatalf("got %d %q diagnostics, want 1:\n%s", conflicts, SummaryMarkerConflict, diags.Err())
			}

			// Nothing was rewritten: the author's value is still there. A
			// pass that "repaired" it would have written aws_vpc.main.
			assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
				"tofu-estate":  "stamp-unit",
				"tofu-address": "aws_vpc.somewhere_else",
			})
		})
	}
}
