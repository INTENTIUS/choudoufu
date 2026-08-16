// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"
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
// admits a ClassRecordAdmitted type outright once the live block declares a
// record_store, and refuses every other class unconditionally. See
// [ClassifyLogicalType].
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
	// This class is the same set as [identity.TypeIdentity.RecordBacked],
	// by construction: one rule over live/logical-schemas.json derives
	// both. They were once derived separately and diverged, which meant
	// resolution held a record for four types lint had already refused.
	ClassRecordAdmitted LogicalClass = "RECORD_ADMITTED"

	// ClassSecretRefused types generate or require secret material - a
	// private key, a generated password, a cryptographically random byte
	// string meant to stay hidden - that the no-secrets rule
	// (live/RECEIPTS.md) forbids a live-markers run from persisting
	// anywhere a record could carry it. Refused permanently, not only
	// until #73 lands: #73's own charter (issue comment, 2026-08) names
	// this exact family as staying refused under the existing no-secrets
	// rule.
	ClassSecretRefused LogicalClass = "SECRET_REFUSED"

	// ClassOtherRefused is every other logical type: local_* and anything
	// else this table has no more specific opinion about, including a
	// prefix-family member this table does not name explicitly. Refused
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

	// Prefix is the shared family prefix this type belongs to (e.g.
	// "null_" for null_resource), for building the "(prefix*)" clause
	// [ClassOtherRefused]'s Detail has always used. Empty for a type with
	// no family of its own - terraform_data is the one entry in this table
	// that shares no prefix with any other logical type.
	Prefix string

	// Evidence is the one-sentence, provider-docs-grounded reason a
	// RECORD_ADMITTED type's outputs carry no secret material, or a
	// SECRET_REFUSED type's do. Required for both classes; empty for
	// ClassOtherRefused, which carries no classification-specific reason
	// beyond the original wording.
	Evidence string
}

// logicalFamilyPrefixes and logicalTypes are generated from
// live/logical-schemas.json into logical_type_generated.go by
// tools/row-gen -emit; see that file for the derivation and its evidence.
// This file holds the vocabulary they are expressed in and the two functions
// that read them.

// ClassifyLogicalType is the exported, per-type classification API this
// file's table drives: whether resourceType is a logical, store-only type
// at all and, if so, which of the three classes it falls in.
//
// A caller uses this to ask, for a given type, whether it is a
// RECORD_ADMITTED candidate without re-deriving admission.go's old prefix
// answer or reading provider schemas itself. checkManagedResources (lint.go)
// is its first caller, and the class is what decides whether a configured
// record_store admits the type or the RuleLogicalResource refusal fires.
//
// An exact match in [logicalTypes] is authoritative, and covers every managed
// resource type of every store-only provider the generator measured. Failing
// that, a type in one of [logicalFamilyPrefixes]' families classifies by
// default, which is the case for a type released after the last
// -logical-schemas run and for every hashicorp/local type: the tls_ family
// defaults to ClassSecretRefused, because all four of its measured types take
// a sensitive private-key argument and a future tls_ addition is
// overwhelmingly likely to as well, so the safe default for an unmeasured
// tls_ type is the same as the measured ones rather than a falsely reassuring
// generic refusal; every other family defaults to ClassOtherRefused, the
// unclassified-but-still-refused verdict, because random_/time_/null_/local_
// do not share the tls_ family's uniform evidence and a future member could
// go either way. A type in no family at all is not logical, and the second
// return is false.
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
// A ban-list of the old wording ("does not exist yet", "not yet", ...) was
// tried first and an audit defeated it in one attempt, by writing "has not
// shipped" instead - there are unbounded ways to spell a false promise, and
// only one thing worth asserting: that the message states the support is
// here NOW. TestLogicalResourceDetailsRenderByClass asserts both that the
// detail contains this string and that this string still says what it says,
// so rewording the claim means editing the test on purpose.
const recordStoreSupportExists = "That support exists"

func logicalResourceDetail(resourceType string, lt LogicalType) string {
	switch lt.Class {
	case ClassRecordAdmitted:
		return fmt.Sprintf(
			"%q is a logical resource, classified RECORD_ADMITTED: its outputs carry "+
				"no secret material (%s), so a persisted micro-state record can hold "+
				"its value where no cloud observation could. "+recordStoreSupportExists+" - "+
				"GitHub issue #73's record-backed identity - and this configuration "+
				"has simply not turned it on, which is the only reason %s is refused "+
				"here. Declare a record_store in the live block and it is admitted, "+
				"running through the stock provider lifecycle against a record "+
				"hydrated from and written back to that store:\n\n"+
				"  terraform {\n"+
				"    live {\n"+
				"      estate = \"my-estate\"\n"+
				"      record_store \"ssm\" {}\n"+
				"    }\n"+
				"  }\n\n"+
				"The label picks the backend: \"ssm\", \"s3\" (which needs a bucket "+
				"argument), or \"local\" (a directory beside the module). If you would "+
				"rather keep no record at all, pass the value in as a variable or a "+
				"local, or read it from a resource that really exists",
			resourceType, lt.Evidence, resourceType,
		)
	case ClassSecretRefused:
		return fmt.Sprintf(
			"%q is a logical resource, classified SECRET_REFUSED: %s. A live-markers "+
				"run has nowhere to keep secret material - no state file today, and, "+
				"per GitHub issue #73's charter, no persisted micro-state record "+
				"either: the no-secrets rule forbids a record from carrying it the "+
				"same way it forbids a snapshot from carrying it. This type stays "+
				"refused permanently, not only until #73 lands. Generate and store "+
				"the secret in a secret manager instead, and have configuration "+
				"reference it by ARN or path, never by value",
			resourceType, lt.Evidence,
		)
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
