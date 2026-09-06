// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"regexp"

	"github.com/zclconf/go-cty/cty"
)

// This file holds the one question two otherwise unrelated halves of this
// fork both have to answer the same way, and used to answer differently:
// "what single string names this live object?"
//
// A type can be identified two ways at once, and several are. The provider's
// wire identity SCHEMA answers with an object - aws_ecs_task_definition's is
// family + revision - and that is what internal/live/projection records
// ([LocatedIdentityPlanFor]'s Composite branch). The provider's own
// documented "## Import" section answers with a string - for the same type,
// the whole task-definition ARN - and that is what internal/live/discovery
// composes for every live object it finds by its marker and nothing else
// (importIDFromARN, reached from the tag sweep and from the no-list-route
// marker fallback). Neither answer is wrong and neither is derivable from
// the other: an ARN carries an account and a partition the identity object
// never held, and family+revision does not say which account's ARN it is.
//
// GitHub issue #879 is what happens when the two meet. A replace records
// the destroyed object's identity as a tombstone in the OBJECT form, the
// next plan finds the same destroyed object's lingering tag and identifies
// it in the STRING form, and the comparison between them
// (internal/live/discovery's recordIdentityMatches) can only answer "no" -
// so the estate refuses "Indistinguishable instances without per-instance
// markers" for as long as AWS keeps listing the dead object, on the plainest
// possible day-2 replace.
//
// The fix is to record BOTH names for the same object, which is only
// possible on the write side: an apply holds the whole applied object, and
// so can read the string form off it with the same rule discovery uses to
// compose it. [SecondaryImportID] is that reader.

// arnImportSyntaxRe matches a [TypeIdentity.ImportSyntax] placeholder that
// is a single token - letters and underscores only - ending in ARN:
// TASKDEFINITIONARN, GRAPHARN, POLICYARN, the bare ARN itself.
// tools/row-gen's tryOpaqueOverride (importprecedence.go) only ever writes
// this shape when the provider's own documented "## Import" section shows an
// arn:-prefixed example verbatim, with no other segment - never guessed from
// the CFN registry's property naming alone. A composite syntax carrying any
// separator (",", ":", "/", "#", "|") fails this pattern by construction, so
// a multi-ARN join (DataSync's "DataSync-ARN#FSx-ARN") is never mistaken for
// this shape.
var arnImportSyntaxRe = regexp.MustCompile(`^[A-Z_]*ARN$`)

// ImportsWholeARN reports whether ti's type imports by its own bare ARN
// whenever only an ID string - not the provider's own identity object - is
// available for the legacy Terraform import path every admitted type still
// answers to. Two independent signals both say so, and either is enough:
//
//   - ti.IdentityAttrs names "arn" first: the newer Terraform 1.12+
//     resource-identity convention, the IVS family.
//   - ti.ImportSyntax is [arnImportSyntaxRe]-shaped, which row-gen only
//     produces when the provider's OWN documented Import section shows an
//     arn:-prefixed example.
//
// This is internal/live/discovery's own importsWholeARNString, moved here so
// that the record written by an apply and the identity composed by a later
// discovery pass are decided by one function rather than by two that agree
// until they do not. That package's own doc comment carries the measured
// history behind both signals (issue #298's aws_ecs_task_definition, issue
// #124's aws_prometheus_workspace).
func ImportsWholeARN(ti TypeIdentity) bool {
	if len(ti.IdentityAttrs) > 0 && ti.IdentityAttrs[0] == "arn" {
		return true
	}
	return arnImportSyntaxRe.MatchString(ti.ImportSyntax)
}

// SecondaryImportID reads off an applied object the single import-identity
// string a marker-driven discovery pass would compose for that same object -
// the string form described at the top of this file - for a type whose
// RECORDED identity is the composite object form instead.
//
// It is the same two-way answer internal/live/discovery's importIDFromARN
// gives, asked of the object rather than of an ARN:
//
//   - a type that imports by its whole ARN ([ImportsWholeARN]) is named by
//     its own "arn" attribute, which is the attribute importIDFromARN
//     itself names when it hands the ARN back whole;
//   - every other type is named by the ARN's resource-id segment, which is
//     that object's own "id" - the convention importIdentity uses on the
//     native list path and resolveCloudControlImportID uses on the Cloud
//     Control one.
//
// The second arm is why this is deliberately NOT recorded as an import ID.
// For a composite-identity type "id" may be a fragment that reads back as a
// whole identity - the exact defect [LocatedIdentityPlanFor]'s Composite
// branch refuses to write - so what this returns is stored in a field
// nothing imports from and only ever compared against a live object's own
// composed identity string. A value that is a fragment simply fails to
// equal any claimant's import ID, which leaves the estate exactly where it
// was: refusing, loudly. A value that DOES equal one names the object
// discovery itself named that way, which is the whole question being asked.
//
// ok is false when the type has no ratified table row, or when the object
// carries no usable value under the attribute the row implies. Both mean
// "no second name for this object", never an error: the record is written
// with its composite identity alone, exactly as before.
func SecondaryImportID(resourceType string, obj cty.Value) (string, bool) {
	ti, ok := LookupType(resourceType)
	if !ok {
		return "", false
	}
	if ImportsWholeARN(ti) {
		return locatedAttrString(obj, "arn")
	}
	return locatedAttrString(obj, locatedImportIDAttr)
}
