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
// Every class stays refused at lint today - this file is groundwork, not a
// new admission path. See [ClassifyLogicalType].
type LogicalClass string

const (
	// ClassRecordAdmitted types generate or hold no secret material in any
	// of their outputs, verified against the provider's own documentation
	// (see each [logicalTypes] entry's Evidence). #73's persisted
	// micro-state record could carry their whole identity once the
	// projection and store work lands - an identity backed by the record
	// itself, with no cloud observation behind it at all. Today they are
	// refused exactly like every other logical type; the Detail lint
	// produces for one names this class and the coming path rather than
	// leaving the type looking like a permanent dead end.
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

// logicalFamilyPrefixes are the provider-local type prefixes whose resources
// exist only inside the state file. Their value is generated once and then
// remembered; the record of them IS the store that stateless mode removes, so
// there is nothing to recover them from and no version of them that works
// without authoritative state. See live/LIMITATIONS.md.
//
// Membership here is what makes an unlisted type - a prefix-family member
// [logicalTypes] has no explicit row for - still classify (as
// [ClassOtherRefused], except the tls_ family, see [ClassifyLogicalType])
// instead of falling through to the generic unadmitted-type refusal. It is
// checked before the admission table for the same reason it always was: a
// random_id gets the logical-resource explanation for why its whole family
// is out, not the generic "not in the v0 table" message.
var logicalFamilyPrefixes = []string{
	"random_",
	"tls_",
	"time_",
	"null_",
	"local_",
}

// logicalTypes is the per-type classification table: hand-verified rows for
// every type this file's audit could confidently place in
// [ClassRecordAdmitted] or [ClassSecretRefused]. A type in a
// [logicalFamilyPrefixes] family with no row here classifies
// [ClassOtherRefused] by default (see [ClassifyLogicalType]) - the safe
// answer for a type nobody has individually verified yet, per "anything
// uncertain goes REFUSED" - rather than being silently promoted to
// RECORD_ADMITTED.
//
// terraform_data is the one entry with no family prefix of its own: GitHub
// issue #73's audit found it missing from the old prefix list entirely, so
// a terraform_data resource fell through to the generic RuleUnadmittedType
// refusal instead of RuleLogicalResource. It is admitted to this table by
// exact type name, the same way every other row is looked up, so that gap
// closes without needing a "terraform_" prefix that would falsely claim a
// whole family this fork has never surveyed.
var logicalTypes = map[string]LogicalType{
	// ---- RECORD_ADMITTED ----------------------------------------------
	// Each entry's Evidence is drawn from the provider's own docs
	// (hashicorp/{null,time,random} on the Terraform Registry, and
	// developer.hashicorp.com/terraform/language/resources/terraform-data
	// for the one built-in type), fetched 2026-08.
	"null_resource": {
		Type: "null_resource", Class: ClassRecordAdmitted, Prefix: "null_",
		Evidence: "hashicorp/null's docs give null_resource exactly two attributes: " +
			`"triggers", an arbitrary map the author supplies, and "id", documented ` +
			`as "set to a random value at create time" - neither is secret material`,
	},
	"terraform_data": {
		Type: "terraform_data", Class: ClassRecordAdmitted,
		Evidence: "the language docs describe terraform_data's input/output/" +
			"triggers_replace/id with no sensitivity marking and no mention of " +
			"secret material: output is simply input echoed back after apply",
	},
	"time_static": {
		Type: "time_static", Class: ClassRecordAdmitted, Prefix: "time_",
		Evidence: "hashicorp/time's docs give time_static only timestamp outputs " +
			"(rfc3339, unix, and the individual date/time components) - a captured " +
			"point in time, not secret material",
	},
	"time_offset": {
		Type: "time_offset", Class: ClassRecordAdmitted, Prefix: "time_",
		Evidence: "same shape as time_static: rfc3339, unix, base_rfc3339, and the " +
			"date/time components of a computed offset - not secret material",
	},
	"time_rotating": {
		Type: "time_rotating", Class: ClassRecordAdmitted, Prefix: "time_",
		Evidence: "same shape as time_static: rfc3339, unix, and the date/time " +
			"components of a rotating timestamp - not secret material",
	},
	"time_sleep": {
		Type: "time_sleep", Class: ClassRecordAdmitted, Prefix: "time_",
		Evidence: "its only output is id, an RFC3339 timestamp; create_duration/" +
			"destroy_duration/triggers are author-supplied delay configuration, " +
			"not secret material",
	},
	"random_id": {
		Type: "random_id", Class: ClassRecordAdmitted, Prefix: "random_",
		Evidence: `hashicorp/random's docs say so directly: "if the output is ` +
			`considered sensitive, and should not be displayed in the CLI, use ` +
			`random_bytes instead" - random_id's own output is documented as the ` +
			"non-sensitive case",
	},
	"random_pet": {
		Type: "random_pet", Class: ClassRecordAdmitted, Prefix: "random_",
		Evidence: `its only output is id, a generated pet name (e.g. "happy-cat") ` +
			"meant to be a readable unique identifier, not secret material",
	},
	"random_shuffle": {
		Type: "random_shuffle", Class: ClassRecordAdmitted, Prefix: "random_",
		Evidence: "its result is a permutation of the input list already present " +
			"in configuration; the resource creates no new material, secret or " +
			"otherwise, only reorders what configuration already supplied",
	},
	"random_integer": {
		Type: "random_integer", Class: ClassRecordAdmitted, Prefix: "random_",
		Evidence: "its result/id outputs are a number drawn from an author-" +
			"supplied min/max range, documented with no sensitivity marking - an " +
			"identifier, not secret material",
	},

	// ---- SECRET_REFUSED -------------------------------------------------
	"random_password": {
		Type: "random_password", Class: ClassSecretRefused, Prefix: "random_",
		Evidence: `its result attribute is documented "(String, Sensitive)": the ` +
			"generated password is itself the secret",
	},
	"random_bytes": {
		Type: "random_bytes", Class: ClassSecretRefused, Prefix: "random_",
		Evidence: "hashicorp/random's docs position random_bytes as the " +
			`"use this in preference to random_id when the output is considered ` +
			`sensitive" resource; both its base64 and hex outputs are documented ` +
			"sensitive",
	},
	"tls_private_key": {
		Type: "tls_private_key", Class: ClassSecretRefused, Prefix: "tls_",
		Evidence: "hashicorp/tls's docs mark private_key_pem/private_key_openssh/" +
			"private_key_pem_pkcs8 sensitive and warn the key \"will be stored " +
			`unencrypted in your Terraform state file"`,
	},
	"tls_self_signed_cert": {
		Type: "tls_self_signed_cert", Class: ClassSecretRefused, Prefix: "tls_",
		Evidence: "requires a sensitive private_key_pem (or write-only " +
			"private_key_pem_wo) argument naming the key the certificate belongs " +
			"to: the tls_ family handles private key material even on a type whose " +
			"own cert_pem output is not itself secret",
	},
	"tls_locally_signed_cert": {
		Type: "tls_locally_signed_cert", Class: ClassSecretRefused, Prefix: "tls_",
		Evidence: "requires a sensitive ca_private_key_pem (or write-only " +
			"ca_private_key_pem_wo) argument to sign with - same family, same " +
			"handling of private key material",
	},
	"tls_cert_request": {
		Type: "tls_cert_request", Class: ClassSecretRefused, Prefix: "tls_",
		Evidence: "requires a sensitive private_key_pem (or write-only " +
			"private_key_pem_wo) argument the request is generated from - same " +
			"family, same handling of private key material",
	},
}

// ClassifyLogicalType is the exported, per-type classification API this
// file's table drives: whether resourceType is a logical, store-only type
// at all and, if so, which of the three classes it falls in.
//
// A caller working ahead of #73's projection work uses this to ask, for a
// given type, whether it is a RECORD_ADMITTED candidate without re-deriving
// admission.go's old prefix answer or re-auditing provider docs itself.
// checkManagedResources (lint.go) is its first caller, unchanged in outcome:
// every currently-refused logical type is still refused, only the Detail
// text differs by class.
//
// An exact match in [logicalTypes] is authoritative. Failing that, a type in
// one of [logicalFamilyPrefixes]' families classifies by default: the tls_
// family defaults to ClassSecretRefused, because every resource type in it
// verified above takes a sensitive private-key argument and a future tls_
// addition is overwhelmingly likely to as well, so the safe default for an
// unreviewed tls_ type is the same as the reviewed ones rather than a
// falsely reassuring generic refusal; every other family defaults to
// ClassOtherRefused, the original, unclassified-but-still-refused verdict,
// because random_/time_/null_/local_ do not share the tls_ family's uniform
// evidence and a future member could go either way. A type in no family at
// all is not logical, and the second return is false.
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
				Evidence: "every hashicorp/tls resource type verified above takes or " +
					"produces private key material through a required, sensitive " +
					"*_pem argument; this type is presumed to as well until reviewed " +
					"individually, per \"anything uncertain goes REFUSED\"",
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
func logicalResourceDetail(resourceType string, lt LogicalType) string {
	switch lt.Class {
	case ClassRecordAdmitted:
		return fmt.Sprintf(
			"%q is a logical resource, classified RECORD_ADMITTED: its outputs carry "+
				"no secret material (%s), so a persisted micro-state record can hold "+
				"its value where no cloud observation could. That support exists - "+
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
