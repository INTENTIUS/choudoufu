// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is the external cross-check on [logicalTypes]. Every other test
// of that table checks it against itself: TestClassifyLogicalType restates
// each row's Class as the expectation, and TestLogicalTypesTableWellFormed
// checks internal consistency. Neither can catch a row whose Class the
// provider contradicts, because neither consults the provider.
//
// [measuredSensitivity] is that consultation, frozen. It also records, as a
// checked fact rather than a comment, which family members the table has no
// row for at all - the gap a reader would otherwise have to re-measure the
// providers to find.

// measuredSensitivity is every managed resource type in the five
// [logicalFamilyPrefixes] families, with the attributes the provider's own
// GetProviderSchema response marks Sensitive.
//
// Measured 2026-08-16 against hashicorp/random 3.9.0, hashicorp/tls 4.3.0,
// hashicorp/time 0.14.1, hashicorp/null 3.3.1 and hashicorp/local 2.9.0, by
// launching each provider through internal/live/pluginschema and walking
// every Attribute of every Block in the response, nested attribute types and
// nested blocks included. No attribute in any of the twenty-one types is
// WriteOnly, so that flag adds nothing here and is not recorded.
//
// This is measured data, not a restatement of [logicalTypes]. Editing a row
// here to make a failing test pass is falsifying a measurement; re-measure
// the providers instead, and say which versions.
var measuredSensitivity = map[string][]string{
	// hashicorp/local 2.9.0
	"local_file":           {"sensitive_content"},
	"local_sensitive_file": {"content", "content_base64"},
	// hashicorp/null 3.3.1
	"null_resource": nil,
	// hashicorp/random 3.9.0
	"random_bytes":    {"base64", "hex"},
	"random_id":       nil,
	"random_integer":  nil,
	"random_password": {"bcrypt_hash", "result"},
	"random_pet":      nil,
	"random_shuffle":  nil,
	"random_string":   nil,
	"random_uuid":     nil,
	"random_uuid4":    nil,
	"random_uuid7":    nil,
	// hashicorp/time 0.14.1
	"time_offset":   nil,
	"time_rotating": nil,
	"time_sleep":    nil,
	"time_static":   nil,
	// hashicorp/tls 4.3.0
	"tls_cert_request":        {"private_key_pem"},
	"tls_locally_signed_cert": {"ca_private_key_pem"},
	"tls_private_key":         {"private_key_openssh", "private_key_pem", "private_key_pem_pkcs8"},
	"tls_self_signed_cert":    {"private_key_pem"},
}

// TestLogicalClassAgreesWithProviderSensitivity checks every [logicalTypes]
// row against the provider's own sensitivity markings.
//
// The classification each row's Evidence argues from is exactly this one:
// RECORD_ADMITTED means "no secret material", SECRET_REFUSED means "secret
// material", and the tls_self_signed_cert row makes the marked-anywhere
// reading explicit by refusing a type whose own cert_pem output is not
// secret, on the strength of a sensitive private_key_pem *argument*. So the
// mechanical form of that argument is: sensitive attribute anywhere in the
// schema => SECRET_REFUSED, none => RECORD_ADMITTED. Applied to the
// measured data it reproduces all fifteen provider-backed rows, and this
// test is what keeps that true.
func TestLogicalClassAgreesWithProviderSensitivity(t *testing.T) {
	covered := 0
	for typ, lt := range logicalTypes {
		sensitive, measured := measuredSensitivity[typ]
		if !measured {
			// terraform_data is a language built-in; no provider publishes a
			// schema for it, so it is the one row this check cannot cover.
			if typ != "terraform_data" {
				t.Errorf("logicalTypes has a row for %q that measuredSensitivity does not cover; "+
					"re-measure the provider rather than dropping the cross-check", typ)
			}
			continue
		}
		covered++

		want := ClassRecordAdmitted
		if len(sensitive) > 0 {
			want = ClassSecretRefused
		}
		if lt.Class != want {
			t.Errorf("logicalTypes[%q].Class = %q, but the provider's schema marks %d attribute(s) "+
				"sensitive (%v), which derives %q - one of the two is wrong",
				typ, lt.Class, len(sensitive), sensitive, want)
		}
	}
	if want := len(logicalTypes) - 1; covered != want {
		t.Errorf("cross-checked %d provider-backed rows, want %d", covered, want)
	}
}

// unclassifiedFamilyMembers are the resource types in the five
// [logicalFamilyPrefixes] families that [logicalTypes] has no hand-written
// row for, so they classify [ClassOtherRefused] by default (except the tls_
// family, which has none left uncovered).
//
// This is not a permitted-exceptions list. It is a measured gap, pinned so
// that it cannot grow or shrink silently, with what
// [measuredSensitivity] says each one would classify as if the derivation
// above were applied to it:
//
//   - random_string, random_uuid, random_uuid4, random_uuid7: no sensitive
//     attribute, so RECORD_ADMITTED - but they get OTHER_REFUSED's wording,
//     which offers no remedy, and neither
//     [identity.DefaultTable] nor lint's record_store branch covers them, so
//     a record_store does not in fact admit them. random_uuid4 and
//     random_uuid7 shipped in random 3.9.0, after this table was written:
//     the table cannot grow a row for a type released after it, which is why
//     the classification wants deriving rather than listing.
//   - local_file, local_sensitive_file: sensitive attributes, so
//     SECRET_REFUSED - though neither wording fits a resource whose side
//     effect is a file on local disk, which no record and no cloud marker
//     describes.
//
// See the tracker issue this list cites for the work that would empty it.
var unclassifiedFamilyMembers = []string{
	"local_file",
	"local_sensitive_file",
	"random_string",
	"random_uuid",
	"random_uuid4",
	"random_uuid7",
}

// TestUnclassifiedFamilyMembersAreExactlyTheKnownGap pins that list against
// [measuredSensitivity], so adding a [logicalTypes] row for one of them - or
// measuring a new family member - forces this list to be edited on purpose
// and the reasoning above to be re-read.
func TestUnclassifiedFamilyMembersAreExactlyTheKnownGap(t *testing.T) {
	var got []string
	for typ := range measuredSensitivity {
		if lt, ok := ClassifyLogicalType(typ); ok && lt.Class == ClassOtherRefused {
			got = append(got, typ)
		}
	}
	sort.Strings(got)

	want := append([]string(nil), unclassifiedFamilyMembers...)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("family members with no logicalTypes row = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("family members with no logicalTypes row = %v, want %v", got, want)
		}
	}
}

// TestNoLogicalTypeIsAdmittedWithoutAnIdentityRow is the layer-agreement
// check: lint's record_store branch (checkManagedResources) admits a
// RECORD_ADMITTED type outright, and identity.Resolve then needs a
// RecordBacked row for it or refuses with "Resource type outside the
// live-markers subset". A RECORD_ADMITTED class with no identity row is a
// lint that promises an admission resolution does not honour.
//
// It also bounds the other direction: every RecordBacked identity row must
// be a type lint classifies RECORD_ADMITTED, or resolution would carry a
// record for something lint never let through.
func TestNoLogicalTypeIsAdmittedWithoutAnIdentityRow(t *testing.T) {
	for typ, lt := range logicalTypes {
		entry, ok := identity.LookupType(typ)
		if lt.Class != ClassRecordAdmitted {
			if ok && entry.RecordBacked {
				t.Errorf("logicalTypes[%q] is %s but identity.DefaultTable marks it RecordBacked; "+
					"resolution would hold a record for a type lint refuses", typ, lt.Class)
			}
			continue
		}
		if !ok || !entry.RecordBacked {
			t.Errorf("logicalTypes[%q] is RECORD_ADMITTED, so lint admits it under a record_store, "+
				"but identity.DefaultTable has no RecordBacked row for it - resolution would refuse "+
				"what lint just promised", typ)
		}
	}
}
