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

// measuredDeprecated is every attribute the same five providers mark
// Deprecated, across all twenty-one types - the complete list, not a
// selection. Measured the same way and at the same time as
// [measuredSensitivity], from each Attribute's Deprecated flag, with the
// provider's own DeprecationMessage quoted.
//
// It exists because the derivation in
// [TestLogicalClassAgreesWithProviderSensitivity] reads "a sensitive
// attribute means this type handles secret material", and a deprecated
// attribute does not support that reading. local_file.sensitive_content is
// the case that forces the point: hashicorp/local deprecated it *in order
// to* move the sensitive case out into its own resource type, so the one
// attribute that would classify local_file as a secret-handling type is
// the one the provider tells you not to use, and its replacement is a
// different type that this table classifies separately. Counting it would
// have local_file classified by an attribute whose whole purpose is to no
// longer be local_file's.
//
// Only local_file.sensitive_content is both sensitive and deprecated, so
// subtracting this map moves exactly one of the twenty-one types and no
// [logicalTypes] row at all.
var measuredDeprecated = map[string][]string{
	// "Use the `local_sensitive_file` resource instead"
	"local_file": {"sensitive_content"},
	// "**NOTE**: This is deprecated, use `numeric` instead." - not sensitive
	// in either type, recorded so this map is the complete measurement
	// rather than only the part that changes an answer.
	"random_password": {"number"},
	"random_string":   {"number"},
}

// liveSensitiveAttrs is typ's sensitive attributes with its deprecated ones
// removed: the input to the classification rule below.
func liveSensitiveAttrs(typ string) []string {
	deprecated := make(map[string]bool, len(measuredDeprecated[typ]))
	for _, name := range measuredDeprecated[typ] {
		deprecated[name] = true
	}
	var live []string
	for _, name := range measuredSensitivity[typ] {
		if !deprecated[name] {
			live = append(live, name)
		}
	}
	return live
}

// TestLogicalClassAgreesWithProviderSensitivity checks every [logicalTypes]
// row against the provider's own sensitivity markings.
//
// The classification each row's Evidence argues from is exactly this one:
// RECORD_ADMITTED means "no secret material", SECRET_REFUSED means "secret
// material", and the tls_self_signed_cert row makes the marked-anywhere
// reading explicit by refusing a type whose own cert_pem output is not
// secret, on the strength of a sensitive private_key_pem *argument*. So the
// mechanical form of that argument is: a live (non-deprecated) sensitive
// attribute anywhere in the schema => SECRET_REFUSED, none =>
// RECORD_ADMITTED. Applied to the measured data it reproduces all fifteen
// provider-backed rows, and this test is what keeps that true.
//
// The deprecation clause is [measuredDeprecated]'s doc comment; it changes
// no row here, and is stated as part of the rule rather than as a special
// case so that the rule can be applied to a type this table has no row for
// without reaching a conclusion the provider disowns.
func TestLogicalClassAgreesWithProviderSensitivity(t *testing.T) {
	covered := 0
	for typ, lt := range logicalTypes {
		_, measured := measuredSensitivity[typ]
		sensitive := liveSensitiveAttrs(typ)
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

// TestDeprecationClauseMovesOnlyLocalFile bounds the clause the rule above
// applies, so that widening it later is a deliberate act with a visible
// diff rather than a quiet reclassification.
//
// It recomputes, rather than restates, both halves: which types
// [measuredDeprecated] names attributes for that [measuredSensitivity] does
// not mark sensitive (so the clause cannot reach them), and which types the
// clause actually changes the derived class of. The audit shape this exists
// for is "a mask wider than its label" - a subtraction that looks like it
// touches one type and in fact suppresses evidence on several.
func TestDeprecationClauseMovesOnlyLocalFile(t *testing.T) {
	var moved []string
	for typ := range measuredSensitivity {
		before := len(measuredSensitivity[typ]) > 0
		after := len(liveSensitiveAttrs(typ)) > 0
		if before != after {
			moved = append(moved, typ)
		}
	}
	sort.Strings(moved)

	want := []string{"local_file"}
	if len(moved) != len(want) || (len(moved) == 1 && moved[0] != want[0]) {
		t.Errorf("the deprecation clause changes the derived class of %v, want %v - "+
			"re-read measuredDeprecated's doc comment before widening this", moved, want)
	}

	// Every type measuredDeprecated names must be one measuredSensitivity
	// covers, or the two measurements are of different provider versions.
	for typ := range measuredDeprecated {
		if _, ok := measuredSensitivity[typ]; !ok {
			t.Errorf("measuredDeprecated names %q, which measuredSensitivity does not cover", typ)
		}
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
//
//     The four agree with the provider's own prose, read out of the same
//     schema response as the sensitivity flags (every Attribute and Block
//     carries a Description the provider ships in its binary, so this needs
//     no doc cache and no network). random_uuid, random_uuid4 and
//     random_uuid7 are each described as generating a value "intended to be
//     used as a unique identifier for other resources" - word for word what
//     random_pet's description says, and random_pet is RECORD_ADMITTED.
//     random_string's is the explicit one: "For unique ids please use
//     random_id, for sensitive random values please use random_password",
//     and random_password's own description reads "Identical to
//     random_string with the exception that the result is treated as
//     sensitive". The provider documents the pair as the non-sensitive and
//     sensitive halves of one resource, and puts random_string on the
//     non-sensitive half.
//
//   - local_file: its ONLY sensitive attribute is sensitive_content, which
//     is deprecated with the message "Use the `local_sensitive_file`
//     resource instead" - so once the deprecation clause above applies, it
//     derives RECORD_ADMITTED, not SECRET_REFUSED.
//
//   - local_sensitive_file: content and content_base64 are sensitive and
//     settable, and projection's recordPayload stores the whole object
//     value (internal/live/projection/record.go), so a record for one would
//     carry that content. SECRET_REFUSED on the rule and on the mechanism.
//
//     Neither wording fits either type, for a reason the rule cannot see.
//     Measured against hashicorp/local 2.9.0: delete the file and the
//     provider drops the resource from state, so the next plan proposes a
//     create; change its content and the same thing happens, because id is
//     documented as (and measured to be) the SHA1 of the content. Both
//     types read their file back. OTHER_REFUSED's Detail tells the author
//     "there is no live system holding it", which for these two is false:
//     the local filesystem is holding it, and every attribute of both types
//     is a function of configuration plus that file. What they lack is not
//     a value to recover but an estate to belong to.
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

// TestLocalFileKeepsItsCountIndexCheck guards the second thing that would
// break if local_file were promoted to RECORD_ADMITTED on the strength of
// the derivation above, which is not a lint message but a silenced check.
//
// countIndexScopeForType (count_index.go) returns skip:true for a
// RECORD_ADMITTED type, on the stated ground that such a type's identity is
// "the persisted record addressed by the resource's own instance address,
// never by any argument value". That holds for all ten current members -
// null_resource, terraform_data, random_*, time_* have no argument naming
// anything outside the record. It does not hold for local_file, whose
// filename argument names a real file, and two instances at distinct
// addresses with distinct records still collide on one path.
//
// Measured against hashicorp/local 2.9.0 under stock Terraform: four
// instances of count = 4 with filename = "c-${count.index % 2}.txt" apply
// cleanly ("5 added"), leave two files holding instances 2 and 3, and the
// very next plan reports "2 to add" - it never converges, under stock
// OpenTofu semantics, with a state file present. So filename is genuinely
// identity-bearing and the count-index walk over it is doing real work.
//
// This is the "did this change turn any warning into silence?" shape: the
// promotion looks like a wording improvement and would also delete a check
// on a configuration stock OpenTofu itself cannot settle.
func TestLocalFileKeepsItsCountIndexCheck(t *testing.T) {
	for _, typ := range []string{"local_file", "local_sensitive_file"} {
		lt, isLogical := ClassifyLogicalType(typ)
		if scope := countIndexScopeForType(typ, lt, isLogical); scope.skip {
			t.Errorf("countIndexScopeForType(%q) skips the count.index walk, but %q's "+
				"filename argument names the file itself - two instances can collide on "+
				"one path, which stock OpenTofu does not converge either", typ, typ)
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
