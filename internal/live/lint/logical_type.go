// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"

	"github.com/intentius/choudoufu/internal/live/strict"
)

// LogicalClass is the record-backed classification of a logical, store-only
// resource type: what its whole existence turns on, and therefore whether
// GitHub issue #73's persisted micro-state record could ever stand in for
// the authoritative state a stateless run has none of.
//
// Replaces logicalTypePrefixes' plain "which prefix matched" answer with a
// policy-grade one, per type name rather than per family, so that the #73
// projection work can ask this package which types are candidates without
// re-deriving the answer from provider docs itself.
//
// The class is load-bearing, not descriptive: checkManagedResources (lint.go)
// admits a ClassRecordAdmitted or ClassExternalAdmitted type outright once the
// live block declares a record_store, and refuses every other class
// unconditionally. See [ClassifyLogicalType].
type LogicalClass string

const (
	// ClassRecordAdmitted types generate or hold no secret material
	// anywhere in their schema, measured from the provider's own
	// GetProviderSchema response (see each [logicalTypes] entry's
	// Evidence). #73's persisted micro-state record carries their whole
	// identity - an identity backed by the record itself, with no cloud
	// observation behind it at all - and lint admits one the moment a live
	// block declares a record_store. Without a store they are still
	// refused, and the Detail names the store as the remedy.
	//
	// This class is a SUBSET of [identity.TypeIdentity.RecordBacked], by
	// construction: one rule over live/logical-schemas.json derives both,
	// and the other part of that set is [ClassExternalAdmitted], whose
	// prior state comes out of the same record by the same mechanism. They
	// were once derived separately and diverged, which meant resolution
	// held a record for four types lint had already refused. What this
	// class adds over ClassExternalAdmitted is the provider-level
	// measurement that the record is the WHOLE of the resource -
	// live/logical-schemas.json's store_only - which is what licenses
	// countIndexScopeForType's skip.
	ClassRecordAdmitted LogicalClass = "RECORD_ADMITTED"

	// ClassExternalAdmitted types are record-backed in exactly the way
	// [ClassRecordAdmitted] types are - #73's persisted micro-state record
	// carries their whole prior state, they generate or hold no secret
	// material, and lint admits one the moment a live block declares a
	// record_store - but the record does not bound what they AFFECT. One of
	// their own arguments names an object outside the record, and that
	// object outlives the record: hashicorp/local's local_file writes a
	// real file onto the machine that ran apply, and deleting the record
	// does not delete the file.
	//
	// The distinction is not cosmetic and is not about wording. It is the
	// one thing this class does not license that [ClassRecordAdmitted]
	// does: countIndexScopeForType (count_index.go) skips the count.index
	// safety walk outright for a RECORD_ADMITTED type, on the ground that
	// such a type's identity is the record addressed by its own instance
	// address and no argument can reach outside it. That ground is false
	// here. Two instances at distinct addresses hold two distinct records
	// and still collide on one filename, which stock OpenTofu does not
	// converge either (measured against hashicorp/local 2.9.0: count = 4
	// with a count.index-derived filename applies "5 added", leaves two
	// files, and the very next plan reports "2 to add", forever). So the
	// walk still runs, and TestLocalFileKeepsItsCountIndexCheck is the pin.
	//
	// Two things this class deliberately does NOT claim, both of them
	// measured rather than assumed, and both of them the reason it is not
	// spelled "argument-derived IDENTITY" the way logical_type.go's own
	// prior comment and GitHub issue #314 both proposed:
	//
	//   - The argument is not an import identity. hashicorp/local 2.9.0
	//     implements no ImportState for local_file at all: `tofu import
	//     local_file.f <path>` answers "Resource Import Not Implemented /
	//     This resource does not support import." So a resolution carrying
	//     the filename as [identity.Resolution.ImportID] would hand
	//     internal/live/projection a string its provider refuses, turning
	//     this lint refusal into a "Cannot import for projection" hard
	//     error - a plan refusal traded for a worse plan refusal.
	//   - The argument is not reliably static either. The estate that
	//     found this (terraform-aws-modules/terraform-aws-lambda v8.8.1,
	//     package.tf:44) writes `filename = data.external.archive_prepare[0]
	//     .result.build_plan_filename`, so even a perfect static evaluator
	//     has nothing to fold.
	//
	// Which leaves the record as the only carrier that can bring this
	// type's prior state back, exactly as for [ClassRecordAdmitted] - the
	// argument's job here is to say why the safety walk stays, not to say
	// where the identity lives.
	ClassExternalAdmitted LogicalClass = "EXTERNAL_ADMITTED"

	// ClassSecretRefused types generate or require secret material - a
	// private key, a generated password, a cryptographically random byte
	// string meant to stay hidden - so the persisted micro-state record
	// holding their whole prior state would hold that material too.
	//
	// # What this class is, after GitHub issue #365 slice 3
	//
	// It is a fact about the type's schema and a DEFAULT, not a permanent
	// verdict. It used to be the second: "refused permanently, not only
	// until #73 lands", on the reading that a live-markers run has nowhere
	// to keep secret material at all. That reading was wrong in a way
	// HANDOFF.md's first difference row names - it refused a configuration
	// stock OpenTofu runs. Stock keeps random_password.result in its state
	// file, in clear, and this fork's record store is precisely where it
	// keeps what a state file would: namespaced per estate, under IAM,
	// written with compare-and-swap, with the sensitivity marks travelling
	// beside the value (internal/live/projection's sensitivepaths.go).
	//
	// So the refusal became a toggle with a default, `strict { secrets }`.
	// Under [strict.Store], the default, such a type is admitted with a
	// record_store declared, as its [LogicalType.StoredClass]. Under
	// [strict.Refuse] it is refused exactly as it always was, and that is
	// what HANDOFF.md's first principle - "no secrets stored by the tool
	// (secret-generating types refused ...)" - now means in code.
	//
	// The class name did not change with the meaning, and that is
	// deliberate: it still names the measurement, which is the only thing
	// live/logical-schemas.json can tell anybody. What varies is what a run
	// does about it. See [admitsUnder].
	ClassSecretRefused LogicalClass = "SECRET_REFUSED"

	// ClassOtherRefused is every other logical type: anything this table
	// has no more specific opinion about, including a prefix-family member
	// this table does not name explicitly. It used to be where the whole
	// local_ family sat; both of hashicorp/local's types now carry a
	// measured row of their own ([ClassExternalAdmitted] for local_file,
	// [ClassSecretRefused] for local_sensitive_file), so what is left here
	// is a member of a surveyed family released after the last
	// -logical-schemas run. Refused
	// for the original reason (live/LIMITATIONS.md's logical-resource
	// entries), with the original wording - no #73 forwarding address of
	// its own yet, because nobody has done the per-type verification work
	// for it that the other two classes required.
	ClassOtherRefused LogicalClass = "OTHER_REFUSED"
)

// LogicalType is one resource type's row in [logicalTypes]: its
// classification and the operator-facing evidence behind it.
type LogicalType struct {
	// Type is the resource type name, e.g. "null_resource".
	Type string

	// Class is this type's classification. See the three [LogicalClass]
	// constants.
	Class LogicalClass

	// StoredClass is the classification that applies under GitHub issue
	// #365's `strict { secrets = "store" }`, which is the default.
	//
	// Set only on a [ClassSecretRefused] row, where it is
	// [ClassRecordAdmitted] or [ClassExternalAdmitted] according to the
	// provider's own store_only measurement - the class the type would carry
	// if nothing in its schema were sensitive. Empty everywhere else, where
	// [Class] is already the answer under either setting.
	//
	// It is a second column rather than a second table for [Valid]'s reason
	// in internal/live/strict: one rule, read at two settings, cannot drift
	// from itself. The generator derives both from one predicate over
	// live/logical-schemas.json (tools/row-gen's logicalClassRows), the same
	// predicate that sets [identity.TypeIdentity.SecretMaterial] on the
	// matching identity row.
	//
	// See [admitsUnder], which is the only thing that reads it.
	StoredClass LogicalClass

	// Prefix is the shared family prefix this type belongs to (e.g.
	// "null_" for null_resource), for building the "(prefix*)" clause
	// [ClassOtherRefused]'s Detail has always used. Empty for a type with
	// no family of its own - terraform_data is the one entry in this table
	// that shares no prefix with any other logical type.
	Prefix string

	// Evidence is the one-sentence, provider-docs-grounded reason a
	// RECORD_ADMITTED or EXTERNAL_ADMITTED type's outputs carry no secret
	// material, or a SECRET_REFUSED type's do. Required for those three
	// classes; empty for ClassOtherRefused, which carries no
	// classification-specific reason beyond the original wording.
	Evidence string

	// External is the provider-level reason an [ClassExternalAdmitted]
	// type's effect reaches outside its own record - live/logical-schemas.
	// json's store_only_evidence for a provider whose store_only is false,
	// which is the same measurement that keeps the type out of
	// ClassRecordAdmitted. Populated only for that class, and empty for
	// every other, including a SECRET_REFUSED type of the same provider:
	// nothing is admitted there, so nothing needs the reason.
	External string
}

// logicalFamilyPrefixes and logicalTypes are generated from
// live/logical-schemas.json into logical_type_generated.go by
// tools/row-gen -emit; see that file for the derivation and its evidence.
// This file holds the vocabulary they are expressed in and the two functions
// that read them.

// ClassifyLogicalType is the exported, per-type classification API this
// file's table drives: whether resourceType is a logical type at all and, if
// so, which of the four classes it falls in.
//
// A caller uses this to ask, for a given type, whether a record_store admits
// it without re-deriving admission.go's old prefix answer or reading provider
// schemas itself. checkManagedResources (lint.go) is its first caller, and the
// class is what decides whether a configured record_store admits the type or
// the RuleLogicalResource refusal fires.
//
// An exact match in [logicalTypes] is authoritative, and covers every managed
// resource type of every provider the generator measured - store-only or not.
// It used to cover only the store-only ones, which is what left both of
// hashicorp/local's types on the generic default and cost this repository
// three issues (#237, #238, #314); the generator now classifies a
// non-store-only provider's types too, as [ClassExternalAdmitted] or
// [ClassSecretRefused], and store_only decides between the two admitted
// classes rather than deciding whether a row exists at all. That also
// retired a hand-written local_sensitive_file exception this function used
// to carry, since the derivation now reaches it.
//
// Failing an exact match, a type in one of [logicalFamilyPrefixes]' families
// classifies by default, which is the case for a type released after the last
// -logical-schemas run: the tls_ family defaults to ClassSecretRefused,
// because all four of its measured types take a sensitive private-key
// argument and a future tls_ addition is overwhelmingly likely to as well, so
// the safe default for an unmeasured tls_ type is the same as the measured
// ones rather than a falsely reassuring generic refusal; every other family
// defaults to ClassOtherRefused, the unclassified-but-still-refused verdict,
// because random_/time_/null_/local_ do not share the tls_ family's uniform
// evidence and a future member could go either way. A type in no family at
// all is not logical, and the second return is false.
//
// The default is the safe answer, not the right one, and re-running
// -logical-schemas is what turns a defaulted type into a measured row. Four
// types sat on that default for longer than they should have: random_string
// and random_uuid from the start, random_uuid4 and random_uuid7 from the
// hashicorp/random 3.9.0 release onward.
func ClassifyLogicalType(resourceType string) (LogicalType, bool) {
	if lt, ok := logicalTypes[resourceType]; ok {
		return lt, true
	}
	for _, prefix := range logicalFamilyPrefixes {
		if !strings.HasPrefix(resourceType, prefix) {
			continue
		}
		if prefix == "tls_" {
			return LogicalType{
				Type: resourceType, Class: ClassSecretRefused, Prefix: prefix,
				Evidence: "every hashicorp/tls resource type measured into " +
					"live/logical-schemas.json takes or produces private key material " +
					"through a required, sensitive *_pem argument; this type is not in " +
					"that measurement and is presumed to as well until it is, per " +
					"\"anything uncertain goes REFUSED\"",
			}, true
		}
		return LogicalType{Type: resourceType, Class: ClassOtherRefused, Prefix: prefix}, true
	}
	return LogicalType{}, false
}

// recordStoreAdmits reports whether a configured record_store admits a logical
// type of this class, which is the single question checkManagedResources
// (lint.go) asks the classification and the single question
// internal/live/identity's RecordBacked half must agree with.
//
// It is a function rather than two equality comparisons at the call site
// because the set has grown once already and each growth is a place a caller
// can be missed. TestRecordStoreAdmitsMatchesTheRecordBackedSet recomputes it
// against identity.DefaultTable rather than restating it.
func recordStoreAdmits(c LogicalClass) bool {
	return c == ClassRecordAdmitted || c == ClassExternalAdmitted
}

// admitsUnder is [recordStoreAdmits] plus the operator's secrets setting:
// whether a configured record_store admits lt given what this run was told
// to do about secret material (GitHub issue #365).
//
// Two branches and they are asymmetric on purpose. A type whose own class is
// admitted is admitted whatever the setting says - nothing about "no secrets
// stored by the tool" is a reason to refuse a type that generates none, and
// reading the toggle as a general strictness dial would refuse null_resource
// under it. A [ClassSecretRefused] type is admitted only under
// [strict.Store], and then as its [LogicalType.StoredClass], which is the
// class it would have carried if nothing in its schema were sensitive.
//
// # Why the setting cannot rescue a type with no StoredClass
//
// A row reaching here with ClassSecretRefused and an empty StoredClass is
// not a measured type. It is the tls_ family default (see
// [ClassifyLogicalType]), the answer for a type released since the last
// -logical-schemas run, and "anything uncertain goes REFUSED" is a rule
// about measurement rather than about secrets. Admitting one would hand
// internal/live/identity a type with no row at all, which produces the
// "Resource type outside the live-markers subset" error - a clear refusal
// traded for a confusing one, on a type nobody has measured.
//
// The same is true of [ClassOtherRefused], which never carries a
// StoredClass either.
func admitsUnder(lt LogicalType, secrets strict.Secrets) bool {
	if recordStoreAdmits(lt.Class) {
		return true
	}
	return strict.StoresSecrets(secrets) && recordStoreAdmits(lt.StoredClass)
}

// logicalResourceDetail builds the Detail of a RuleLogicalResource issue for
// resourceType, worded according to lt.Class.
//
// ClassOtherRefused reuses the original, single, class-agnostic wording
// byte for byte - the "(prefix*)" wording every logical type carried before
// this file's table existed - because nothing has been individually
// verified about that type to say more.
//
// ClassRecordAdmitted names a remedy rather than a wait. Reaching this
// function with that class means checkManagedResources did NOT take its
// recordStoreConfigured branch (lint.go), so the store is absent - the
// support itself landed with #73 phase (d). The wording said "that support
// does not exist yet" for long enough that operators read a one-block fix as
// an unsupported type; that was the defect #101 exists for. Do not
// reintroduce a "not yet" here without checking lint.go's guard first.
// recordStoreSupportExists is the load-bearing claim of the RECORD_ADMITTED
// detail, factored out so a test can pin it by value.
//
// ClassSecretRefused's wording is the one that moved with GitHub issue #365
// slice 3, and it moved because the claim it used to make stopped being
// true. It said the type "stays refused permanently, not only until #73
// lands", on the reading that a live-markers run has nowhere to keep secret
// material. It has somewhere: the estate's record store, namespaced under
// IAM and written with compare-and-swap, which is where this fork keeps
// everything a stock state file would - and a stock state file holds
// random_password.result in clear. So the refusal became a setting with a
// default, and the message now says which of the two reasons an operator is
// looking at. See [secretRefusedDetail].
//
// ClassExternalAdmitted names the same remedy for the same reason, and adds
// the one sentence a RECORD_ADMITTED reader does not need: that declaring the
// store does NOT make count.index safe inside this resource, because an
// argument names the object it writes. Saying so here rather than only in the
// count-index refusal matters, since an operator who reaches this message is
// about to declare a store and re-plan, and the count-index refusal is what
// they will hit next if their configuration has that shape.
//
// A ban-list of the old wording ("does not exist yet", "not yet", ...) was
// tried first and an audit defeated it in one attempt, by writing "has not
// shipped" instead - there are unbounded ways to spell a false promise, and
// only one thing worth asserting: that the message states the support is
// here NOW. TestLogicalResourceDetailsRenderByClass asserts both that the
// detail contains this string and that this string still says what it says,
// so rewording the claim means editing the test on purpose.
const recordStoreSupportExists = "That support exists"

// impliedRecordStoreRemedy is what an operator does about any of the
// refusals that read "there is nowhere to keep this instance's record".
//
// Since GitHub issue #364 that is one step, not two. Every live block
// implies a local record store (internal/configs.impliedRecordStore), so
// the only configuration that still reaches these refusals is one with no
// live block at all - `live-check` reading a stock configuration nobody has
// adopted. Telling that reader to "declare a record_store", which is what
// these details said before, names a block they do not need and hides the
// one they do.
//
// The cloud backends are still named, because the implied store is a local
// directory and a team sharing an estate wants to know the other two exist
// before their first apply rather than after it.
const impliedRecordStoreRemedy = "Add a live block and it is admitted:\n\n" +
	"  terraform {\n" +
	"    live {\n" +
	"      estate = \"my-estate\"\n" +
	"    }\n" +
	"  }\n\n" +
	"That is the whole setup step: a live block with no record_store block of its own gets an " +
	"implied local record store - a \".tofu-records\" directory beside the module, the way stock " +
	"implies a local state file. To keep the records somewhere a team shares instead, name the " +
	"backend: record_store \"ssm\" {}, record_store \"s3\" { bucket = \"...\" }, or " +
	"record_store \"local\" { path = \"...\" }."

func logicalResourceDetail(resourceType string, lt LogicalType, secrets strict.Secrets, recordStoreConfigured bool) string {
	switch lt.Class {
	case ClassRecordAdmitted:
		return fmt.Sprintf(
			"%q is a logical resource, classified RECORD_ADMITTED: its outputs carry "+
				"no secret material (%s), so a persisted micro-state record can hold "+
				"its value where no cloud observation could. "+recordStoreSupportExists+" - "+
				"GitHub issue #73's record-backed identity - and this configuration "+
				"has no live block, which is the only reason %s is refused here. Under "+
				"one it runs through the stock provider lifecycle against a record "+
				"hydrated from and written back to the estate's record store. "+
				impliedRecordStoreRemedy+" If you would "+
				"rather keep no record at all, pass the value in as a variable or a "+
				"local, or read it from a resource that really exists",
			resourceType, lt.Evidence, resourceType,
		)
	case ClassExternalAdmitted:
		return fmt.Sprintf(
			"%q is a logical resource, classified EXTERNAL_ADMITTED: its outputs carry "+
				"no secret material (%s), so a persisted micro-state record can hold its "+
				"prior state where no cloud observation could - and unlike a "+
				"RECORD_ADMITTED type, the record is not the whole of what this "+
				"resource is (%s). "+recordStoreSupportExists+" - GitHub issue #73's "+
				"record-backed identity - and this configuration has no live block, "+
				"which is the only reason %s is refused here. Under one it runs through "+
				"the stock provider lifecycle against a record hydrated from and "+
				"written back to the estate's record store. "+
				impliedRecordStoreRemedy+" One thing the "+
				"record does not do for this class is make two instances of %s "+
				"interchangeable: because an argument names the object it writes, two "+
				"instances at distinct addresses hold distinct records and can still "+
				"collide on one object, so count.index stays refused inside this "+
				"resource's arguments",
			resourceType, lt.Evidence, lt.External, resourceType, resourceType,
		)
	case ClassSecretRefused:
		return secretRefusedDetail(resourceType, lt, secrets, recordStoreConfigured)
	default: // ClassOtherRefused
		return fmt.Sprintf(
			"%q is a logical resource (%s*): it has no existence outside the record "+
				"that OpenTofu keeps of it, so that record is the store live resource "+
				"markers remove. Nothing can recover its value from the live system, because "+
				"there is no live system holding it. Pass the value in as a variable or "+
				"a local, or read it from a resource that really exists",
			resourceType, lt.Prefix,
		)
	}
}

// secretMaterialRemedy is the load-bearing claim of the SECRET_REFUSED
// detail under strict.Refuse, factored out so a test can pin it by value the
// way [recordStoreSupportExists] is pinned.
//
// It replaces the old detail's "This type stays refused permanently, not
// only until #73 lands", which stopped being true when the refusal became a
// setting. A ban-list of the old wording was not attempted here for the
// reason [logicalResourceDetail]'s own comment gives about that technique:
// what is worth asserting is that the message names the setting that produced
// the refusal, which is what
// TestLogicalResourceDetailsRenderByClass's SECRET_REFUSED subtests check -
// against this constant AND against its own value, so rewording it means
// editing that test on purpose.
const secretMaterialRemedy = `strict { secrets = "store" }`

// secretRefusedDetail is the RuleLogicalResource detail for a SECRET_REFUSED
// type, which since GitHub issue #365 slice 3 has three readers rather than
// one. Each gets the one sentence that is actually their next step.
//
//   - Under strict.Refuse: the operator turned this off on purpose. The
//     remedy is the setting, and the message says so rather than describing
//     a permanent property of the type - which is what it used to do, and
//     what would now be a lie.
//   - Under strict.Store with no record_store: the same one-block fix every
//     other admitted logical class gets, for the same reason. This is the
//     #101 shape and the message is deliberately close to
//     [logicalResourceDetail]'s RECORD_ADMITTED wording.
//   - A type with no StoredClass reaching here at all: the tls_ family
//     default, a type released since the last -logical-schemas run. Nothing
//     has measured it, so nothing can say what a record would hold, and
//     "anything uncertain goes REFUSED" applies whatever the setting says.
//     See [admitsUnder].
func secretRefusedDetail(resourceType string, lt LogicalType, secrets strict.Secrets, recordStoreConfigured bool) string {
	switch {
	case lt.StoredClass == "":
		return fmt.Sprintf(
			"%q is a logical resource this fork classifies SECRET_REFUSED by FAMILY rather than by measurement: %s. "+
				"Nothing has read this type's own schema, so nothing can say what a record holding its prior state "+
				"would contain, and %s does not reach it - a setting cannot admit a type nobody has measured. "+
				"Generate and store the secret in a secret manager instead, and have configuration reference it by "+
				"ARN or path, never by value.",
			resourceType, lt.Evidence, secretMaterialRemedy,
		)
	case !strict.StoresSecrets(secrets):
		return fmt.Sprintf(
			"%q is a logical resource, classified SECRET_REFUSED: %s. This estate's live block sets "+
				"strict { secrets = %q }, which is HANDOFF.md's \"no secrets stored by the tool\" principle: a "+
				"secret-generating type is refused rather than admitted, so nothing this run writes can carry the "+
				"material. That is a setting and not a property of the type - the default, %s, admits it and keeps "+
				"its value in the estate's record store the way a stock OpenTofu state file keeps it. Remove the "+
				"argument to get that, or generate and store the secret in a secret manager instead and have "+
				"configuration reference it by ARN or path, never by value.",
			resourceType, lt.Evidence, strict.Refuse, secretMaterialRemedy,
		)
	case !recordStoreConfigured:
		return fmt.Sprintf(
			"%q is a logical resource, classified SECRET_REFUSED: %s. "+recordStoreSupportExists+" - this estate's "+
				"record store is where a value with no cloud object behind it lives, exactly as it is for every "+
				"other logical type - and this configuration has no live block, which is the only reason "+
				"%s is refused here. "+impliedRecordStoreRemedy+
				" What makes this type different from the others is what the record "+
				"then holds: the secret material named above, in clear, the way a stock OpenTofu state file holds "+
				"it. If that is not a trade you want to make, set strict { secrets = %q } - which keeps this exact "+
				"refusal - and pass the value in as a variable, or read it from a secret manager by ARN or path.",
			resourceType, lt.Evidence, resourceType, strict.Refuse,
		)
	default:
		// Unreachable while checkManagedResources consults admitsUnder
		// before building a detail: a SECRET_REFUSED type with a
		// StoredClass, under secrets=store, with a record_store declared,
		// is admitted and raises nothing. Kept as a real sentence rather
		// than a panic because a lint refusal is the wrong place to crash,
		// and TestSecretRefusedDetailNamesTheSetting pins that no caller
		// reaches it.
		return fmt.Sprintf(
			"%q is a logical resource, classified SECRET_REFUSED: %s. It is admitted under this estate's settings, "+
				"so this message should not have been raised; please report it.",
			resourceType, lt.Evidence,
		)
	}
}
