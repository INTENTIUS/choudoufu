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

// measuredWritesOutsideItsRecord is the second measured axis, per family:
// does an instance of this family leave anything behind that OpenTofu's own
// record of it does not contain?
//
// It is what tools/row-gen's logicalProviderSources spells StoreOnly, stated
// here independently and from behaviour rather than copied from the
// generator, for the same reason [measuredSensitivity] is: a cross-check that
// reads its subject's own input is not a cross-check. Two facts settle the
// one family whose answer is "yes", both re-runnable in a scratch directory
// against hashicorp/local 2.9.0:
//
//   - `tofu import local_file.f /path/to/an/existing/file` answers "Resource
//     Import Not Implemented / This resource does not support import." The
//     provider offers no way to recover an instance from the file, which is
//     why the record is still the carrier for its prior state and why
//     [identity.TypeIdentity.RecordBacked] is true for it.
//   - Apply a local_file, delete the record, and the file is still on disk;
//     apply four instances of `filename = "c-${count.index % 2}.txt"` under
//     stock OpenTofu and it reports "5 added", leaves two files, then plans
//     "2 to add" forever. The file is a real object two records can name.
//
// The other four families leave nothing: hashicorp/random generates a value
// and keeps it in the record, hashicorp/time stores a timestamp ("keeps a ...
// UTC timestamp stored in the Terraform state", its own description),
// null_resource "implements the standard resource lifecycle but takes no
// further action", and terraform_data echoes its input back.
//
// Keyed by family prefix rather than by type because the property is a
// provider's, not a type's - which is also why the generator records it once
// per provider.
var measuredWritesOutsideItsRecord = map[string]bool{
	"local_":  true,
	"null_":   false,
	"random_": false,
	"time_":   false,
	"tls_":    false,
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
// attribute anywhere in the schema => SECRET_REFUSED, none => admitted.
//
// Which of the two ADMITTED classes then comes from the second measured
// axis, [measuredWritesOutsideItsRecord]: a family that leaves an object
// behind its record does not contain is EXTERNAL_ADMITTED, and one that
// leaves nothing is RECORD_ADMITTED. Applied to the measured data the pair
// reproduces all seventeen provider-backed rows, and this test is what keeps
// that true.
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

		external, known := measuredWritesOutsideItsRecord[lt.Prefix]
		if !known {
			t.Errorf("logicalTypes[%q] carries prefix %q, which measuredWritesOutsideItsRecord does "+
				"not cover; measure the family rather than dropping the cross-check", typ, lt.Prefix)
			continue
		}

		want := ClassRecordAdmitted
		switch {
		case len(sensitive) > 0:
			want = ClassSecretRefused
		case external:
			want = ClassExternalAdmitted
		}
		if lt.Class != want {
			t.Errorf("logicalTypes[%q].Class = %q, but the provider's schema marks %d attribute(s) "+
				"sensitive (%v) and its family writes-outside-its-record is %v, which derives %q - "+
				"one of the two is wrong",
				typ, lt.Class, len(sensitive), sensitive, external, want)
		}

		// The half that matters most is not the class name but what the
		// class licenses. Recomputed here rather than left to
		// TestLocalFileKeepsItsCountIndexCheck's two hardcoded type names,
		// so a future family measured as writing outside its record cannot
		// pick up the skip by inheriting a class name.
		scope := countIndexScopeForType(typ, lt, true)
		if scope.skip != (!external && len(sensitive) == 0) {
			t.Errorf("countIndexScopeForType(%q).skip = %v, but the family's measured "+
				"writes-outside-its-record is %v - the count.index walk must run for exactly the "+
				"types whose arguments can name something the record does not bound",
				typ, scope.skip, external)
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
// [logicalFamilyPrefixes] families that [logicalTypes] has no row for AND
// [ClassifyLogicalType] gives no exact-match verdict of its own, so they
// classify [ClassOtherRefused] by the generic family-prefix default.
//
// This is not a permitted-exceptions list. It is a measured gap, pinned so
// that it cannot grow or shrink silently.
//
// It used to hold six: random_string, random_uuid, random_uuid4 and
// random_uuid7 were the hand-written table's blind spot, and their entry
// here recorded that a record_store did not in fact admit them. Once row-gen
// derived [logicalTypes] from live/logical-schemas.json
// (tools/row-gen/logicalschemas.go), the same rule that admitted them to
// [identity.DefaultTable] admitted them here, and all four left this list.
// local_sensitive_file left it next, on a hand-coded exact-match SECRET_REFUSED
// verdict [ClassifyLogicalType] carried for a while and no longer needs: the
// derivation reaches it now, from its own schema (content and content_base64,
// String and Sensitive and not deprecated) rather than from its docs' prose.
//
// local_file was the last, and issue #314 emptied the list. It was never an
// unreviewed remainder - the derivation excluded it because hashicorp/local is
// the one input provider whose StoreOnly is false - but "excluded" was the
// wrong answer to the right observation. Its ONLY sensitive attribute is
// sensitive_content, deprecated with the message "Use the
// `local_sensitive_file` resource instead", so the sensitivity rule alone
// derives an admitted class for it; what StoreOnly=false says is that
// RECORD_ADMITTED is the wrong admitted class, not that no class fits.
// OTHER_REFUSED never fitted either, and its Detail said something false to
// the author's face: "there is no live system holding it", when the local
// filesystem is holding it. [ClassExternalAdmitted] is the class that fits,
// and [TestLocalFileKeepsItsCountIndexCheck] still pins the reason it is not
// RECORD_ADMITTED.
//
// The list stays, empty, rather than being deleted with the test below: the
// gap it measures is "a family member the derivation reaches no verdict for",
// which a future provider release can re-open, and an empty pinned list is
// what makes that visible on the release rather than on the estate.
var unclassifiedFamilyMembers = []string{}

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

// TestLocalFileKeepsItsCountIndexCheck guards the thing that would break if
// local_file were promoted to RECORD_ADMITTED on the strength of the
// derivation above, which is not a lint message but a silenced check.
//
// It survives issue #314's admission unchanged, and that is the point of
// citing it here. #314 admitted local_file - through
// [ClassExternalAdmitted], a class that exists precisely so this assertion
// keeps holding - and the temptation the whole way through was to reach
// RECORD_ADMITTED and delete this. The class boundary is drawn where it is so
// that admitting the type and keeping this check are not in tension.
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
// It also bounds the other direction: every RecordBacked identity row must be
// a type lint classifies RECORD_ADMITTED, or resolution would carry a record
// for something lint never let through.
//
// That second direction used to be asserted only by this comment. The loop
// ran over [logicalTypes] alone, so it could see a lint row whose identity row
// was missing and never the reverse - and the reverse is exactly what
// happened. When row-gen's recordBackedRows derivation landed it marked
// random_string, random_uuid, random_uuid4 and random_uuid7 RecordBacked;
// none of the four had a [logicalTypes] row, so none was reachable from this
// loop, and the check stayed green while lint refused all four outright under
// a configured record_store. The audit shape is "a completeness test that
// could see almost nothing": a cross-layer check keyed on one layer's key set
// cannot report what only the other layer knows about. The second loop below
// is keyed on identity's.
func TestNoLogicalTypeIsAdmittedWithoutAnIdentityRow(t *testing.T) {
	for typ, lt := range logicalTypes {
		entry, ok := identity.LookupType(typ)
		if !recordStoreAdmits(lt.Class) {
			if ok && entry.RecordBacked {
				t.Errorf("logicalTypes[%q] is %s but identity.DefaultTable marks it RecordBacked; "+
					"resolution would hold a record for a type lint refuses", typ, lt.Class)
			}
			continue
		}
		if !ok || !entry.RecordBacked {
			t.Errorf("logicalTypes[%q] is %s, so lint admits it under a record_store, "+
				"but identity.DefaultTable has no RecordBacked row for it - resolution would refuse "+
				"what lint just promised", typ, lt.Class)
		}
	}

	for typ, entry := range identity.DefaultTable {
		if !entry.RecordBacked {
			continue
		}
		lt, ok := ClassifyLogicalType(typ)
		if !ok {
			t.Errorf("identity.DefaultTable marks %q RecordBacked, but ClassifyLogicalType does not "+
				"recognise it as a logical type at all - lint would send it to the admission table, "+
				"which by construction does not list a RecordBacked type", typ)
			continue
		}
		if !recordStoreAdmits(lt.Class) {
			t.Errorf("identity.DefaultTable marks %q RecordBacked, so resolution holds its prior state "+
				"in a record, but lint classifies it %s and refuses it before resolution ever runs - "+
				"a record_store cannot admit what lint has already rejected", typ, lt.Class)
		}
	}
}

// TestRecordStoreAdmitsMatchesTheRecordBackedSet is [recordStoreAdmits]'s own
// external check, and it is deliberately not a restatement of the two-constant
// disjunction that function contains.
//
// It computes the set of classes that ACTUALLY carry a RecordBacked identity
// row, from identity.DefaultTable, and requires recordStoreAdmits to answer
// true for exactly those and false for every other class this package
// declares. A class added to the disjunction with no RecordBacked row behind
// it fails here, and so does a class left out of it that has one - the drift
// the test above records happening once already, when the two sets were
// maintained by hand.
func TestRecordStoreAdmitsMatchesTheRecordBackedSet(t *testing.T) {
	backing := map[LogicalClass]bool{}
	for typ, entry := range identity.DefaultTable {
		if !entry.RecordBacked {
			continue
		}
		if lt, ok := ClassifyLogicalType(typ); ok {
			backing[lt.Class] = true
		}
	}
	if len(backing) == 0 {
		t.Fatal("no RecordBacked row in identity.DefaultTable resolves to a logical class; " +
			"this check is not exercising anything")
	}

	all := []LogicalClass{ClassRecordAdmitted, ClassExternalAdmitted, ClassSecretRefused, ClassOtherRefused}
	for _, c := range all {
		if got, want := recordStoreAdmits(c), backing[c]; got != want {
			t.Errorf("recordStoreAdmits(%s) = %v, but %d RecordBacked identity row(s) carry that "+
				"class (want %v) - lint's admission set and identity's record-backed set have diverged",
				c, got, len(backing), want)
		}
	}
	if len(backing) != 2 {
		t.Errorf("RecordBacked rows resolve to %d logical class(es) (%v), want 2 "+
			"(RECORD_ADMITTED and EXTERNAL_ADMITTED) - a new one needs recordStoreAdmits, "+
			"countIndexScopeForType and logicalResourceDetail all considered on purpose",
			len(backing), backing)
	}
}
