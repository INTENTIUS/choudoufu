// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/discovery"
)

// tofu-address continuation tags (issue #71): an address that does not fit
// markers.MaxTagValue (256) Unicode characters in one tag is split across up
// to markers.MaxContinuations ordered tags - tofu-address, tofu-address-2,
// ... - and [discovery.GatherAddress] concatenates them back into the one
// value it started from. This file is the behavioral proof that the two
// sides agree: what this package stamps is what discovery reads back,
// byte for byte, for a constant address and for a per-instance one alike.

// TestStamp_overlongConstantAddressSplitsAcrossContinuationTags: a resource
// with neither count nor for_each, whose label alone pushes the escaped
// address past one tag. The split is exact, computed from the address's own
// known length, and round-trips through discovery.GatherAddress and
// discovery.UnescapeAddress to the instance the marker names.
func TestStamp_overlongConstantAddressSplitsAcrossContinuationTags(t *testing.T) {
	label := strings.Repeat("x", 400) // "aws_s3_bucket." (14) + 400 = 414: two chunks.
	cfg := loadSource(t, `
resource "aws_s3_bucket" "`+label+`" {
  bucket = "overlong"
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %+v, want exactly one resource", res.Stamped)
	}
	if got := res.Stamped[0].Keys; len(got) != 3 {
		t.Fatalf("stamped keys %v, want tofu-estate, tofu-address, tofu-address-2", got)
	}

	full := "aws_s3_bucket." + label
	if len([]rune(full)) != 414 {
		t.Fatalf("fixture arithmetic is wrong: %d", len([]rune(full)))
	}

	tags := evalTags(t, cfg, "aws_s3_bucket."+label, nil)
	assertTags(t, tags, map[string]string{
		"tofu-estate":    "stamp-unit",
		"tofu-address":   full[:256],
		"tofu-address-2": full[256:],
	})

	gathered, corrupt := discovery.GatherAddress(tags)
	if corrupt {
		t.Fatalf("GatherAddress reported a corrupt chain for a set this package itself wrote")
	}
	if gathered != full {
		t.Fatalf("GatherAddress(%v) = %q, want %q", tags, gathered, full)
	}
	if !discovery.ValidMarkerAddress(gathered) {
		t.Fatalf("gathered address %q is not a well-formed marker address", gathered)
	}
	back, ok := discovery.UnescapeAddress(gathered)
	if !ok || back.String() != "aws_s3_bucket."+label {
		t.Fatalf("UnescapeAddress(%q) = %v, %v; want aws_s3_bucket.%s, true", gathered, back, ok, label)
	}
}

// TestStamp_maximallyOverlongConstantAddressUsesAllFourTags: an address at
// the very top of the budget (markers.MaxAddressLen, MaxContinuations x
// MaxTagValue = 4 x 256 = 1024) uses all four tags, the ceiling
// RuleOverlongAddress enforces at lint time.
func TestStamp_maximallyOverlongConstantAddressUsesAllFourTags(t *testing.T) {
	// "aws_vpc." (8) + label = 1024 exactly when label is 1016.
	label := strings.Repeat("z", 1016)
	cfg := loadSource(t, `
resource "aws_vpc" "`+label+`" {
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 1 || len(res.Stamped[0].Keys) != 5 {
		t.Fatalf("stamped %+v, want tofu-estate + four address tags", res.Stamped)
	}

	full := "aws_vpc." + label
	if len([]rune(full)) != 1024 {
		t.Fatalf("fixture arithmetic is wrong: %d", len([]rune(full)))
	}

	tags := evalTags(t, cfg, "aws_vpc."+label, nil)
	assertTags(t, tags, map[string]string{
		"tofu-estate":    "stamp-unit",
		"tofu-address":   full[0:256],
		"tofu-address-2": full[256:512],
		"tofu-address-3": full[512:768],
		"tofu-address-4": full[768:1024],
	})

	gathered, corrupt := discovery.GatherAddress(tags)
	if corrupt || gathered != full {
		t.Fatalf("GatherAddress(%v) = %q, %v; want %q, false", tags, gathered, corrupt, full)
	}
}

// TestStamp_perInstanceAddressSplitsOnlyWhenTheBlockNeedsIt: a for_each
// block whose worst-case key needs two tags. Every instance in the block
// gets the same two tag keys (the split is a property of the block, decided
// once from the static key set - stamper.chunkCount), but a short instance's
// second tag comes back empty rather than erroring: substr() clamps an
// out-of-range offset to "", it does not fail, and
// discovery.GatherAddress's concatenation treats that empty continuation as
// contributing nothing.
func TestStamp_perInstanceAddressSplitsOnlyWhenTheBlockNeedsIt(t *testing.T) {
	longKey := strings.Repeat("k", 245) // prefix (16) + 245 = 261: two chunks.
	cfg := loadSource(t, `
resource "aws_subnet" "this" {
  for_each   = { s = "10.42.1.0/24", `+longKey+` = "10.42.2.0/24" }
  cidr_block = each.value
}
`)

	res, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)
	if len(res.Stamped) != 1 || !res.Stamped[0].PerInstance || len(res.Stamped[0].Keys) != 3 {
		t.Fatalf("stamped %+v, want one per-instance entry with tofu-estate, tofu-address, tofu-address-2", res.Stamped)
	}

	shortFull := `aws_subnet.this:s`
	longFull := "aws_subnet.this:" + longKey
	if len([]rune(shortFull)) >= 256 {
		t.Fatalf("fixture arithmetic is wrong: short instance is %d chars", len([]rune(shortFull)))
	}
	if len([]rune(longFull)) <= 256 {
		t.Fatalf("fixture arithmetic is wrong: long instance is %d chars", len([]rune(longFull)))
	}

	shortTags := evalTags(t, cfg, "aws_subnet.this", eachData("s"))
	assertTags(t, shortTags, map[string]string{
		"tofu-estate":    "stamp-unit",
		"tofu-address":   shortFull,
		"tofu-address-2": "",
	})
	if gathered, corrupt := discovery.GatherAddress(shortTags); corrupt || gathered != shortFull {
		t.Fatalf("short instance: GatherAddress = %q, %v; want %q, false", gathered, corrupt, shortFull)
	}

	longTags := evalTags(t, cfg, "aws_subnet.this", eachData(longKey))
	assertTags(t, longTags, map[string]string{
		"tofu-estate":    "stamp-unit",
		"tofu-address":   longFull[:256],
		"tofu-address-2": longFull[256:],
	})
	gathered, corrupt := discovery.GatherAddress(longTags)
	if corrupt || gathered != longFull {
		t.Fatalf("long instance: GatherAddress = %q, %v; want %q, false", gathered, corrupt, longFull)
	}
	back, ok := discovery.UnescapeAddress(gathered)
	if !ok || back.String() != `aws_subnet.this["`+longKey+`"]` {
		t.Fatalf(`UnescapeAddress(%q) = %v, %v; want aws_subnet.this["%s"], true`, gathered, back, ok, longKey)
	}
}

// TestStamp_continuationTagsAreIdempotent: stamping a configuration whose
// continuation tags are already correct is a no-op, the same "already
// stamped, nothing to do" verdict a short address gets. Running Stamp twice
// over the same in-memory config - once to write the markers, once more to
// find them already there - is what a plan actually does (stamp runs before
// every plan), so this is the regression a structural mismatch in
// [templateChunkMarkers] or [constantAddressMarkers] would show up in first.
func TestStamp_continuationTagsAreIdempotent(t *testing.T) {
	label := strings.Repeat("x", 400)
	cfg := loadSource(t, `
resource "aws_s3_bucket" "`+label+`" {
  bucket = "overlong"
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	res2, diags2 := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags2)
	if len(res2.Stamped) != 0 {
		t.Fatalf("second pass stamped %+v, want nothing: the markers were already there", res2.Stamped)
	}
	if len(res2.Skipped) != 1 || res2.Skipped[0].Reason != SkipAlreadyStamped {
		t.Fatalf("second pass skipped %+v, want exactly one ALREADY_STAMPED", res2.Skipped)
	}
}

// TestStamp_wrongContinuationTagIsAConflict: an author (or a stale write
// from a since-shortened address) who left a tofu-address-2 that does not
// match what this run would write gets a named conflict, the same "never
// overwrite, never guess" rule every other marker key already follows -
// never a silent rewrite and never a value read past the mismatch.
func TestStamp_wrongContinuationTagIsAConflict(t *testing.T) {
	label := strings.Repeat("x", 400)
	full := "aws_s3_bucket." + label
	cfg := loadSource(t, `
resource "aws_s3_bucket" "`+label+`" {
  bucket = "overlong"

  tags = {
    tofu-estate    = "stamp-unit"
    tofu-address   = "`+full[:256]+`"
    tofu-address-2 = "not-what-this-run-would-write"
  }
}
`)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertDiagContains(t, diags, "tofu-address-2", "continuation tag")
}
