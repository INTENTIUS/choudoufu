// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/configs"
)

// The tofu-slot half of stamping. Everything here is asserted by evaluating
// the stamped configuration the way the plan engine will, rather than by
// inspecting the AST: the claim is about the tag a resource ends up carrying.

// TestStamp_slotsComeFromTheTable is the seam working: a slot assignment that
// discovery worked out from the live set reaches each instance's tags.
func TestStamp_slotsComeFromTheTable(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots: map[string]string{
			"aws_eip.pool:0": "0",
			"aws_eip.pool:1": "3",
			"aws_eip.pool:2": "7",
		},
	})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %+v, want one entry", res.Stamped)
	}
	if got := strings.Join(res.Stamped[0].Keys, ","); got != "tofu-estate,tofu-address,tofu-slot" {
		t.Errorf("stamped keys are %q, want all three in spec order", got)
	}

	want := []struct{ address, slot string }{
		{"aws_eip.pool:0", "0"},
		{"aws_eip.pool:1", "3"},
		{"aws_eip.pool:2", "7"},
	}
	for i, w := range want {
		assertTags(t, evalTags(t, cfg, "aws_eip.pool", countData(i)), map[string]string{
			"tofu-estate":  "stamp-unit",
			"tofu-address": w.address,
			"tofu-slot":    w.slot,
		})
	}
}

// TestStamp_slotIsNotAFunctionOfTheIndex is the property MARKERS.md and the
// lint boundary both insist on, checked by making it impossible to satisfy by
// accident: the assignment is 5, 2, 9, which no template over count.index can
// produce and no ordering of the indices explains.
func TestStamp_slotIsNotAFunctionOfTheIndex(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 3
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots: map[string]string{
			"aws_eip.pool:0": "5",
			"aws_eip.pool:1": "2",
			"aws_eip.pool:2": "9",
		},
	})
	assertNoErrors(t, diags)

	for i, want := range []string{"5", "2", "9"} {
		tags := evalTags(t, cfg, "aws_eip.pool", countData(i))
		if tags["tofu-slot"] != want {
			t.Errorf("instance %d carries slot %q, want %q", i, tags["tofu-slot"], want)
		}
	}
}

// TestStamp_slotTableIsWrittenInInstanceOrder: the table is source this run
// synthesizes, and two runs over one configuration have to produce the same
// source or nothing downstream can compare them.
func TestStamp_slotTableIsWrittenInInstanceOrder(t *testing.T) {
	table := map[string]string{
		"aws_eip.pool:0":  "0",
		"aws_eip.pool:1":  "1",
		"aws_eip.pool:2":  "2",
		"aws_eip.pool:10": "10",
		"aws_eip.pool:9":  "9",
	}
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 11
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate: "stamp-unit", Config: cfg, Schemas: testSchemas(), Slots: table,
	})
	assertNoErrors(t, diags)

	keys := slotTableKeys(t, cfg, "aws_eip.pool")
	want := []string{"aws_eip.pool:0", "aws_eip.pool:1", "aws_eip.pool:2", "aws_eip.pool:9", "aws_eip.pool:10"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Errorf("the table reads %v, want instance order %v - 9 before 10, not after it", keys, want)
	}
}

// TestStamp_slotTableOnlyCoversItsOwnBlock: one run's table holds every count
// block in the configuration, and each block takes only its own rows.
func TestStamp_slotTableOnlyCoversItsOwnBlock(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 2
}

resource "aws_eip" "spare" {
  count = 1
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots: map[string]string{
			"aws_eip.pool:0":  "0",
			"aws_eip.pool:1":  "1",
			"aws_eip.spare:0": "4",
		},
	})
	assertNoErrors(t, diags)

	if got := evalTags(t, cfg, "aws_eip.spare", countData(0))["tofu-slot"]; got != "4" {
		t.Errorf("aws_eip.spare[0] carries slot %q, want 4", got)
	}
	if got := evalTags(t, cfg, "aws_eip.pool", countData(1))["tofu-slot"]; got != "1" {
		t.Errorf("aws_eip.pool[1] carries slot %q, want 1", got)
	}
	if keys := slotTableKeys(t, cfg, "aws_eip.pool"); len(keys) != 2 {
		t.Errorf("aws_eip.pool's table is %v, want only its own two rows", keys)
	}
}

// TestStamp_noSlotForABlockWithNoRows: a table that says nothing about this
// block leaves it exactly as a pre-slot run would.
func TestStamp_noSlotForABlockWithNoRows(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 2
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots:   map[string]string{"aws_eip.other:0": "0"},
	})
	assertNoErrors(t, diags)

	if got := evalTags(t, cfg, "aws_eip.pool", countData(0))["tofu-slot"]; got != "" {
		t.Errorf("a block with no rows in the table was stamped with slot %q", got)
	}
}

// TestStamp_forEachNeverGetsASlot: a slot names a member of a fungible set,
// and a for_each instance is named by its key. Even a table that happens to
// hold a matching row does not put one on it.
func TestStamp_forEachNeverGetsASlot(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_subnet" "this" {
  for_each   = { a = "10.42.1.0/24" }
  cidr_block = each.value
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots:   map[string]string{"aws_subnet.this:a": "0"},
	})
	assertNoErrors(t, diags)

	if got := evalTags(t, cfg, "aws_subnet.this", eachData("a"))["tofu-slot"]; got != "" {
		t.Errorf("a for_each instance was stamped with slot %q", got)
	}
}

// TestStamp_slotIsIdempotent: stamping a configuration this pass already
// stamped changes nothing and adds nothing, which is what makes the lookup
// table safe to re-synthesize on every run.
func TestStamp_slotIsIdempotent(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 2
}
`)
	req := Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots:   map[string]string{"aws_eip.pool:0": "0", "aws_eip.pool:1": "6"},
	}

	if _, diags := Stamp(t.Context(), req); diags.HasErrors() {
		t.Fatalf("first pass: %s", diags.Err())
	}
	before := tagsSource(t, cfg, "aws_eip.pool")

	res, diags := Stamp(t.Context(), req)
	assertNoErrors(t, diags)
	if len(diags) != 0 {
		t.Errorf("the second pass produced diagnostics: %s", diags.ErrWithWarnings())
	}
	if len(res.Stamped) != 0 {
		t.Errorf("the second pass stamped over its own work: %+v", res.Stamped)
	}
	if !hasSkip(res, "aws_eip.pool", SkipAlreadyStamped) {
		t.Errorf("the second pass does not report the resource as already stamped: %+v", res.Skipped)
	}
	if after := tagsSource(t, cfg, "aws_eip.pool"); after != before {
		t.Errorf("the tags object grew from %d entries to %d", before, after)
	}
}

// TestStamp_handWrittenConstantSlotIsAnError: one slot shared by every
// instance is three resources claiming to be the same member of the set. Named
// rather than replaced, the same as a constant address.
func TestStamp_handWrittenConstantSlotIsAnError(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_eip" "pool" {
  count = 2

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${count.index}"
    tofu-slot    = "0"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		Slots:   map[string]string{"aws_eip.pool:0": "0", "aws_eip.pool:1": "1"},
	})
	if !diags.HasErrors() {
		t.Fatal("a constant slot on a count resource was accepted")
	}
	assertDiagContains(t, diags, "Ownership marker conflict", "fungible set", "tofu-slot")
}

// slotTableKeys reads the keys of a stamped resource's slot lookup table out
// of the synthesized source, in the order they were written. Reading the AST
// is the point here rather than a shortcut: source order is the claim, and
// evaluating the expression would throw it away.
func slotTableKeys(t *testing.T, cfg *configs.Config, addr string) []string {
	t.Helper()

	rc, ok := cfg.Module.ManagedResources[addr]
	if !ok {
		t.Fatalf("no resource %s in the configuration", addr)
	}
	body, ok := rc.Config.(*hclsyntax.Body)
	if !ok {
		t.Fatalf("%s is not in HCL native syntax", addr)
	}
	attr, ok := body.Attributes["tags"]
	if !ok {
		t.Fatalf("%s has no tags argument", addr)
	}
	obj, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		t.Fatalf("%s's tags is not an object literal", addr)
	}

	for _, item := range obj.Items {
		key, ok := objectKeyLiteral(item.KeyExpr)
		if !ok || key != TagSlot {
			continue
		}
		call, ok := item.ValueExpr.(*hclsyntax.FunctionCallExpr)
		if !ok || call.Name != "lookup" || len(call.Args) != 3 {
			t.Fatalf("%s's %s is not a three-argument lookup: %#v", addr, TagSlot, item.ValueExpr)
		}
		table, ok := call.Args[0].(*hclsyntax.ObjectConsExpr)
		if !ok {
			t.Fatalf("%s's slot lookup is not over an object literal", addr)
		}
		out := make([]string, 0, len(table.Items))
		for _, row := range table.Items {
			k, ok := objectKeyLiteral(row.KeyExpr)
			if !ok {
				t.Fatalf("%s's slot table has a non-literal key", addr)
			}
			out = append(out, k)
		}
		return out
	}
	t.Fatalf("%s carries no %s tag", addr, TagSlot)
	return nil
}

// perInstanceMarkerCases is GitHub issue #115's own table: the five
// expressions it lists, plus the two shapes that motivated comparing values
// rather than structure.
//
// Before #115 exactly one of these was accepted and the other six produced
// "Ownership marker could not be checked" - a warning, with the tag left as
// written, so four wrong markers reached the cloud quietly and two correct
// ones were reported as unverifiable.
var perInstanceMarkerCases = []struct {
	name string
	expr string
	// wantErr is whether the marker is wrong and must be refused.
	wantErr bool
	// wantIn is a fragment the refusal has to carry, so a refusal for the
	// wrong reason fails too.
	wantIn string
}{
	{
		name: "the template this pass writes",
		expr: `"aws_eip.pool:${count.index}"`,
	},
	{
		name: "format producing the same value",
		expr: `format("aws_eip.pool:%d", count.index)`,
	},
	{
		name: "a template over a local holding the prefix",
		expr: `"${local.prefix}${count.index}"`,
	},
	{
		name:    "square brackets instead of the escaped colon",
		expr:    `"aws_eip.pool[${count.index}]"`,
		wantErr: true,
		wantIn:  `produces "aws_eip.pool[0]"`,
	},
	{
		name:    "the index alone",
		expr:    `"${count.index}"`,
		wantErr: true,
		wantIn:  `produces "0"`,
	},
	{
		name:    "a different prefix",
		expr:    `"pool-${count.index}"`,
		wantErr: true,
		wantIn:  `produces "pool-0"`,
	},
	{
		name:    "right prefix, wrong separator",
		expr:    `"aws_eip.pool.${count.index}"`,
		wantErr: true,
		wantIn:  `produces "aws_eip.pool.0"`,
	},
}

// TestStamp_perInstanceMarkerIsCheckedByValue is #115's acceptance: an
// expression that interpolates the instance key but produces the wrong
// marker is refused, not warned.
func TestStamp_perInstanceMarkerIsCheckedByValue(t *testing.T) {
	for _, tc := range perInstanceMarkerCases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadSource(t, `
locals {
  prefix = "aws_eip.pool:"
}

resource "aws_eip" "pool" {
  count = 2

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = `+tc.expr+`
  }
}
`)

			_, diags := Stamp(t.Context(), Request{
				Estate:  "stamp-unit",
				Config:  cfg,
				Schemas: testSchemas(),
			})

			switch {
			case tc.wantErr && !diags.HasErrors():
				t.Fatalf("%s was accepted; it produces a marker discovery will never match.\ngot: %s", tc.expr, diags.Err())
			case !tc.wantErr && diags.HasErrors():
				t.Fatalf("%s produces exactly the marker this pass would write and was refused: %s", tc.expr, diags.Err())
			}
			if tc.wantErr {
				assertDiagContains(t, diags, "Ownership marker conflict", tc.wantIn, "aws_eip.pool[0]")
			}
			// A correct-but-differently-written expression must not be
			// reported as unverifiable either: that was the other half of
			// the old behaviour, and it is what sent operators looking for
			// a problem that was not there.
			for _, d := range diags {
				if desc := d.Description(); desc.Summary == SummaryMarkerUncheckable {
					t.Errorf("%s was reported as unverifiable: %s", tc.expr, desc.Detail)
				}
			}
		})
	}
}

// TestStamp_chunkedPerInstanceAddressHasAValidAnswer is GitHub issue #115's
// second half, and the case it says no current test covers.
//
// When an escaped address exceeds markers.MaxTagValue, the address is split
// across tofu-address and its continuations, and this pass's own expression
// for each continuation is a substr() window over the instance template. No
// expression that merely interpolates ${count.index} can match that
// structurally, so before #115 every continuation tag's conflict message
// demanded a form that could not be written - while a sibling message in the
// same file told the operator continuation tags are never edited by hand.
//
// Comparing values gives it an answer both ways: an unstamped block is
// stamped, and a wrong hand-written continuation is refused with a remedy
// that exists.
func TestStamp_chunkedPerInstanceAddressHasAValidAnswer(t *testing.T) {
	// A label long enough that the escaped address needs a continuation.
	longName := strings.Repeat("n", 300)

	t.Run("unstamped block is stamped across continuations", func(t *testing.T) {
		cfg := loadSource(t, `
resource "aws_eip" "`+longName+`" {
  count = 2
}
`)
		res, diags := Stamp(t.Context(), Request{
			Estate:  "stamp-unit",
			Config:  cfg,
			Schemas: testSchemas(),
		})
		assertNoErrors(t, diags)
		if len(res.Stamped) != 1 {
			t.Fatalf("want the block stamped once, got %+v", res.Stamped)
		}
		var continuations int
		for _, k := range res.Stamped[0].Keys {
			if strings.HasPrefix(k, TagAddress+"-") {
				continuations++
			}
		}
		if continuations == 0 {
			t.Fatalf("a %d-character address was written with no continuation tags: %v", len(longName), res.Stamped[0].Keys)
		}
	})

	t.Run("a wrong hand-written continuation is refused with a remedy", func(t *testing.T) {
		cfg := loadSource(t, `
resource "aws_eip" "`+longName+`" {
  count = 2

  tags = {
    tofu-estate    = "stamp-unit"
    tofu-address-2 = "definitely-not-the-right-chunk-${count.index}"
  }
}
`)
		_, diags := Stamp(t.Context(), Request{
			Estate:  "stamp-unit",
			Config:  cfg,
			Schemas: testSchemas(),
		})
		if !diags.HasErrors() {
			t.Fatal("a wrong continuation tag was accepted")
		}
		// The remedy has to be one that exists. This pass appends a marker
		// the configuration does not set, so removing the tag works;
		// hand-writing a substr() window does not, which is what the old
		// message asked for.
		assertDiagContains(t, diags, "Ownership marker conflict", "Remove the tag", "continuation")
		for _, d := range diags {
			if desc := d.Description(); desc.Summary == SummaryMarkerUncheckable {
				t.Errorf("a chunked continuation was reported as unverifiable rather than wrong: %s", desc.Detail)
			}
		}
	})
}

// TestStamp_forEachValueIsBoundTruthfully is the regression an adversarial
// audit found in the first version of #115's per-instance comparison.
//
// each.value was bound to the KEY for every instance, on both sides of the
// comparison, so an address built from a map's values matched itself and
// verified - while the plan would write the real value and discovery would
// never find it. That turned a warning into silence, which is the one
// outcome this whole phase exists to remove.
func TestStamp_forEachValueIsBoundTruthfully(t *testing.T) {
	t.Run("a map's values are not its keys", func(t *testing.T) {
		cfg := loadSource(t, `
resource "aws_eip" "pool" {
  for_each = { a = "zzz", b = "yyy" }

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${each.value}"
  }
}
`)
		_, diags := Stamp(t.Context(), Request{
			Estate:  "stamp-unit",
			Config:  cfg,
			Schemas: testSchemas(),
		})
		if !diags.HasErrors() {
			t.Fatal(`an address built from a map's values was accepted; the plan writes "aws_eip.pool:zzz" where discovery expects "aws_eip.pool:a"`)
		}
		assertDiagContains(t, diags, "Ownership marker conflict", `produces "aws_eip.pool:zzz"`)
	})

	t.Run("a set's values are its keys, and still verify", func(t *testing.T) {
		cfg := loadSource(t, `
resource "aws_eip" "pool" {
  for_each = toset(["a", "b"])

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${each.value}"
  }
}
`)
		_, diags := Stamp(t.Context(), Request{
			Estate:  "stamp-unit",
			Config:  cfg,
			Schemas: testSchemas(),
		})
		assertNoErrors(t, diags)
	})

	t.Run("a map keyed correctly still verifies", func(t *testing.T) {
		cfg := loadSource(t, `
resource "aws_eip" "pool" {
  for_each = { a = "zzz", b = "yyy" }

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_eip.pool:${each.key}"
  }
}
`)
		_, diags := Stamp(t.Context(), Request{
			Estate:  "stamp-unit",
			Config:  cfg,
			Schemas: testSchemas(),
		})
		assertNoErrors(t, diags)
	})
}
