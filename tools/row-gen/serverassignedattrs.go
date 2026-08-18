// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file supplies [identity.TypeIdentity.IdentityAttrs] for the rows that
// have no components: the server-assigned ones. It is the second field
// renderIdentityFile recomputes rather than copies, for the same reason
// mergeServerAssigned recomputes [identity.Component.ServerAssignedIfAbsent]
// - nothing ever ratifies it by hand, because the provider states it.
//
// # What the field is for, and what was missing
//
// IdentityAttrs is the set of attribute names whose value equals the type's
// import identity, so that another resource reading one of them
// (aws_s3tables_table_bucket_policy's table_bucket_arn reading
// aws_s3tables_table_bucket.this.arn) resolves to that parent's live ID
// instead of refusing with "Not an identity attribute"
// (internal/live/identity/resolve.go's parentPart). It is also what
// internal/live/discovery's importIdentity and internal/live/liveimport's
// liveIDFrom read the live ID out of a list result by; an empty list makes
// both fall back to "id", which for a type whose identity schema is {arn}
// is not an attribute the provider's identity object carries at all.
//
// Measured over the table as committed before this rule existed: 497 rows
// are ServerAssigned, 291 carry IdentityAttrs and 206 do not. Of those 206,
// 75 have a provider identity schema requiring exactly one attribute - so
// the provider states what identifies them and the row does not say it.
//
// # The rule, and why it stops where it does
//
// A ServerAssigned row gets every attribute its provider identity schema
// requires for import, appended in the schema's own order. The source is
// [surveyEntry.requiredForImport] - live/survey-full.json, the provider's
// own resource identity schema, the same source classify.go already prefers
// over the carve seed and the snake-cased guess. This does not claim that
// one required attribute alone equals the row's import identity: for
// aws_ecs_task_definition (required: family, revision) neither does, since
// ECS keeps many revisions of one family. What the rule does claim is
// narrower and is all [identity.TypeIdentity.IdentityAttrs]'s two consumers
// need - internal/live/discovery's importIdentity and
// internal/live/liveimport's liveIDFrom both try the list in order and stop
// at the first attribute the list result actually carries, so a type is
// discoverable the moment ANY one of its schema's required attributes shows
// up in the identity object the provider's ListResource RPC returns. Before
// this rule such a row had no IdentityAttrs at all, so both fell back to
// "id" - an attribute a composite-identity type's identity object does not
// carry either, which is exactly the failure live/e2e/corpus-ecs-taskdef
// pins.
//
// Measured against the table as committed before this change: three
// ServerAssigned rows have no Components, no ratified IdentityAttrs, and a
// survey identity schema requiring more than one attribute -
// aws_ecs_task_definition ([family, revision]),
// aws_eks_pod_identity_association ([association_id, cluster_name]) and
// aws_prometheus_anomaly_detector ([id, workspace_id]). The single-attribute
// case below already accounted for every other row this rule could reach;
// widening it from "requires exactly one" to "requires any number" changes
// nothing for rows already covered (a length-1 schema still contributes
// exactly the one attribute it always did) and touches no row whose ratified
// IdentityAttrs already disagrees - checked over all 342 ServerAssigned rows
// that already carry IdentityAttrs, zero have a multi-attribute required
// schema missing one of its own attributes from the ratified list.
//
// Two things bound it:
//
//   - Only ServerAssigned rows. A row with Components asserts its own import
//     ID by construction, and that assertion can legitimately disagree with
//     the identity schema's single required attribute: aws_codebuild_project
//     and aws_codebuild_fleet import by "name" while their identity schemas
//     require "arn", and the two codeartifact permissions-policy rows have
//     the same split. [identityAttrEvidence] records those rulings for the
//     per-component field; this rule simply never reaches a row that has
//     components, so the table's own claim is never overwritten by the
//     schema's. A ServerAssigned row makes no competing claim - it says the
//     cloud assigned the identity and no argument reconstructs it - which
//     leaves the identity schema uncontested.
//
//   - Union, never replacement. A ratified list keeps its order and its
//     members; the schema's attribute is only appended when absent. So a row
//     that already names "id" first still tries "id" first, and this rule can
//     only ever widen what resolves, never redirect it.
//
// # Evidence the rule is right where the table already answers
//
// Of the 291 ServerAssigned rows that do carry IdentityAttrs, 103 have a
// single-required-attribute identity schema. In all 103 the schema's
// attribute is already among the row's ratified IdentityAttrs; there are no
// disagreements. The rule reproduces every ratified answer in its own domain
// before it is allowed to supply a missing one, and the source it is checked
// against - the provider's identity schema - is external to the table.
//
// All 75 rows the rule newly fills were checked against hashicorp/aws
// 6.59.0's real schemas: the named attribute exists in every one of their
// resource schema blocks, so no row hands out an identity source the
// provider does not serve (the standing check for that is
// [identity.VerifyTable]'s FindingAttributeNotInSchema, which stays the
// backstop for a future release that drops one). Their ImportSyntax strings
// corroborate independently: all 75 document an import ID naming the same
// attribute, and the scraped documentation's own "### Identity Schema"
// section names it for 73 of them, with 2 coverage gaps and no
// contradictions - see TestServerAssignedIdentityAttrsAgreeWithTheDocs.
//
// # This reverses eleven ratified rulings, and why
//
// row-gen's fresh classifier already derived this attribute for eleven of
// these rows, and annotations.json ruled against it eleven times, in one
// shared sentence: "the ratified entry deliberately claims none. table.go
// documents the empty list as the honest answer when no attribute is a
// verified identity source ... references to this type's attributes refuse
// rather than guess. Row-gen's fault here is claiming too much, not knowing
// too little." The eleven were aws_bedrockagentcore_evaluator,
// aws_bedrockagentcore_harness, aws_bedrockagentcore_online_evaluation_config,
// aws_bedrockagentcore_policy_engine, aws_ce_anomaly_monitor,
// aws_ce_anomaly_subscription, aws_ce_cost_category, aws_dx_gateway,
// aws_s3files_file_system, aws_securityhub_configuration_policy and
// aws_ssmcontacts_contact.
//
// The ruling reads the empty list as a conservative refusal. It is not one,
// because IdentityAttrs has a second consumer the ruling never mentions.
// internal/live/discovery's importIdentity reads the live ID out of a list
// result's identity OBJECT by these names, and an empty list makes it fall
// back to "id". Measured over the 75 rows: 59 have an identity schema with
// no "id" attribute at all. For those, the fallback looks for an attribute
// the provider's identity object does not carry, importIdentity returns
// nothing, and discovery raises ProblemNoIdentity - "came back from the list
// call with no usable identity, so there is nothing to read it with and no
// destroy can be planned for it", a diagnostic that then names "id" as the
// attribute it wanted. The empty list does not make such a type refuse
// carefully; it makes it undiscoverable, and blames the provider for it.
//
// Eight of the eleven are in that 59 (the four bedrockagentcore rows, the
// three ce rows and aws_ssmcontacts_contact - their identity schemas require
// evaluator_id, harness_id, online_evaluation_config_id, policy_engine_id or
// arn, and carry no id). The other three (aws_dx_gateway,
// aws_s3files_file_system, aws_securityhub_configuration_policy) require
// "id" itself, so naming it changes nothing at discovery time and only lifts
// the sibling-reference refusal.
//
// table.go's own words are narrower than the ruling's paraphrase of them:
// an empty list is the honest answer "for types whose \"id\" attribute is a
// provider-synthesized value distinct from the import ID". That describes a
// type whose id disagrees with its identity - not a type whose provider
// states, in its own identity schema, which single attribute the import
// identity is.
func mergeIdentityAttrs(entry identity.TypeIdentity, survey surveyEntry) identity.TypeIdentity {
	if !entry.ServerAssigned {
		return entry
	}
	have := make(map[string]bool, len(entry.IdentityAttrs))
	for _, attr := range entry.IdentityAttrs {
		have[attr] = true
	}
	out := entry.IdentityAttrs
	for _, attr := range survey.requiredForImport() {
		if have[attr] {
			continue
		}
		have[attr] = true
		out = append(append([]string(nil), out...), attr)
	}
	entry.IdentityAttrs = out
	return entry
}
