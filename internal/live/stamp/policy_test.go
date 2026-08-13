// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"
)

// TestStamp_PolicyUntagReleasesEstateMarker: GitHub issue #67's
// declared_tagged = "untag" verb, tag_key defaulting to the estate marker -
// the maintainer's ruling on the "one semantic question": this is the
// reading where releasing the tag has a consequence to state, because it is
// the marker discovery reads to find the resource again. The resource's
// other marker (tofu-address) is unaffected, and the release is recorded
// with EstateMarker true.
func TestStamp_PolicyUntagReleasesEstateMarker(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:      "stamp-unit",
		Config:      cfg,
		Schemas:     testSchemas(),
		PolicyUntag: map[string]string{"aws_vpc.main": TagEstate},
	})
	assertNoErrors(t, diags)

	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %d resources, want 1 (tofu-address still needs adding): %+v", len(res.Stamped), res.Stamped)
	}
	for _, k := range res.Stamped[0].Keys {
		if k == TagEstate {
			t.Errorf("tofu-estate was added even though policy asked to release it: %v", res.Stamped[0].Keys)
		}
	}

	if len(res.Untagged) != 1 {
		t.Fatalf("want 1 Untagged entry, got %d: %+v", len(res.Untagged), res.Untagged)
	}
	u := res.Untagged[0]
	if u.Key != TagEstate {
		t.Errorf("released key %q, want %q", u.Key, TagEstate)
	}
	if !u.EstateMarker {
		t.Error("releasing the estate marker itself must set EstateMarker true - this is the case issue #67 says the plan must state the management consequence for")
	}

	tags := evalTags(t, cfg, "aws_vpc.main", nil)
	if _, present := tags[TagEstate]; present {
		t.Errorf("the rewritten configuration still carries tofu-estate=%q, want it released", tags[TagEstate])
	}
	if tags[TagAddress] != "aws_vpc.main" {
		t.Errorf("tofu-address was not stamped alongside the release: %v", tags)
	}
}

// TestStamp_PolicyUntagReleasesCustomPreservationTag: the other reading of
// issue #67's "one semantic question" - tag_key names a preservation tag
// distinct from the estate marker. The maintainer's ruling: no management
// consequence here, because the estate marker is untouched and a later run
// finds the resource exactly as it always did. This test's whole claim is
// that the released key carries EstateMarker false, and that the estate
// marker survives the release right alongside it.
func TestStamp_PolicyUntagReleasesCustomPreservationTag(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    "team-owns" = "platform"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:      "stamp-unit",
		Config:      cfg,
		Schemas:     testSchemas(),
		PolicyUntag: map[string]string{"aws_vpc.main": "team-owns"},
	})
	assertNoErrors(t, diags)

	// "team-owns" is not one of the three markers this package ever writes,
	// so there is nothing to omit from the stamped set: the estate marker
	// and tofu-address are added exactly as they would be without any
	// policy at all.
	if len(res.Stamped) != 1 {
		t.Fatalf("stamped %d resources, want 1: %+v", len(res.Stamped), res.Stamped)
	}
	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
		// The preservation tag itself is written in the configuration this
		// pass never rewrites (it is not one of the three markers), so
		// stamping neither adds it nor removes it: it survives, present in
		// the config the author wrote, exactly as declared_tagged = "untag"
		// releasing a non-marker key is supposed to leave it alone from
		// this package's own side. See [Request.PolicyUntag]: nothing in
		// this package's writable set names "team-owns", so there is no
		// key to omit.
		"team-owns": "platform",
	})

	// This package's PolicyUntag hook only ever suppresses one of the three
	// markers it manages; a key outside that set produces no Untagged entry
	// at all, because there is nothing for this pass to release - the
	// ordinary provider apply already treats the tags argument as
	// authoritative, and a custom key untouched by stamping was never going
	// to be added or removed here regardless of policy.
	for _, u := range res.Untagged {
		if u.EstateMarker {
			t.Errorf("a custom preservation tag release must never carry EstateMarker true: %+v", u)
		}
	}
}

// TestStamp_PolicyUntagHandWrittenValueIsNotOverwritten: this package never
// overwrites a hand-written marker value anywhere else, and untag is not an
// exception. A configuration that already hardcodes the key policy asked to
// release keeps it, and the skip is recorded rather than silent.
func TestStamp_PolicyUntagHandWrittenValueIsNotOverwritten(t *testing.T) {
	cfg := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    "tofu-estate" = "stamp-unit"
  }
}
`)

	res, diags := Stamp(t.Context(), Request{
		Estate:      "stamp-unit",
		Config:      cfg,
		Schemas:     testSchemas(),
		PolicyUntag: map[string]string{"aws_vpc.main": TagEstate},
	})
	assertNoErrors(t, diags)

	if len(res.Untagged) != 0 {
		t.Errorf("a hand-written marker value must not be released: %+v", res.Untagged)
	}
	found := false
	for _, s := range res.Skipped {
		if s.Reason == SkipUntagHandWritten {
			found = true
		}
	}
	if !found {
		t.Errorf("want a SkipUntagHandWritten entry recording the hand-written value, got: %+v", res.Skipped)
	}

	assertTags(t, evalTags(t, cfg, "aws_vpc.main", nil), map[string]string{
		"tofu-estate":  "stamp-unit",
		"tofu-address": "aws_vpc.main",
	})
}

func TestUntagged_String(t *testing.T) {
	rc := loadSource(t, `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
`).Module.ManagedResources["aws_vpc.main"]

	u := Untagged{Addr: rc.Addr(), Key: TagEstate, EstateMarker: true}
	if s := u.String(); !strings.Contains(s, "leaves management") {
		t.Errorf("String() = %q, want it to mention leaving management", s)
	}

	u2 := Untagged{Addr: rc.Addr(), Key: "team-owns", EstateMarker: false}
	if s := u2.String(); strings.Contains(s, "leaves management") {
		t.Errorf("String() = %q, a non-estate-marker release must not mention leaving management", s)
	}
}
