// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// This file makes the project's most-repeated standing rule mechanical.
//
// The rule is "this is a generator; all work must be derived, and hand-wiring
// a named resource type is cheating". It has been violated and caught twice by
// adversarial reading and never once by CI, because until now it lived only in
// prose - in .claude/agents/live-markers.md and in whatever an orchestrator
// remembered to paste into a brief. Prose is re-read by whoever happens to
// read it, and this repository keeps discovering its prose was stale.
//
// What the rule cannot be, and why
// --------------------------------
//
// "Could the generic pass have derived this?" is not decidable by a test; it
// is the open problem the whole project is working on. So this guard does not
// try to judge whether a literal is justified. It makes the SURFACE explicit
// and bounded: every place a concrete aws_* type name appears in hand-written
// Go is registered here with a reason and an exact count, so adding one is a
// visible line in a diff next to a question, and removing one is a line that
// must also be claimed.
//
// The buckets were measured before the rule was written, not after. Over
// internal/live, tools, live, cmd and internal/command at 6da372bfd2 there are
// 7004 string literals that are exactly a provider type name:
//
//	3680  in _test.go files          - fixtures; a test naming a type is the test
//	2778  in *_generated.go files    - the generated tables; the whole product
//	 425  package-level table data   - hand tables, each already carrying prose
//	 125  inside a function body     - the decision positions, and the debt
//
// The prior this was written against guessed that the shape to catch was a
// literal in a `switch` or an `if`. Measured, there are ZERO of either. Every
// one of the 125 in-function literals is an index, and 119 of them are one
// idiom: `g.byType["aws_..."]` in tools/estate-gen, meaning "wire this
// argument to the cohort's own instance of this exact type". That idiom is
// what commit 4251066763 deleted from overrides_cohort_ecs_eks.go when
// siblingRef learned the plural <base>_arns shape generically - the canonical
// example of the rule this file exists to hold. The Code count below is
// therefore the number that carries the rule, and estate-gen holds 123 of it.
//
// Comments are deliberately not scanned. The walk is over the AST, so
// `// e.g. "aws_route"` in a doc comment is invisible here, which is correct:
// a comment is not control flow, and eight files carry exactly that.
//
// Known bound: a type name assembled at runtime evades this guard.
// TestNoTypeNameIsAssembledFromLiterals closes the literal half of that route;
// `"aws_" + someVariable` remains invisible and is exactly right, because
// every one of the seven sites doing that today is the generic derivation
// itself building a candidate name.

// typeLiteralSurface is one registered place a concrete provider type name is
// spelled out in hand-written Go, with the reason and the exact census.
type typeLiteralSurface struct {
	// Reason says why a derived value cannot stand here. It is the
	// ciExcludedPackages contract: a standing decision, not a parking space.
	Reason string

	// Data is the count of literals in package-level composite literals -
	// hand tables. A table of types with prose per entry is the sanctioned
	// form; tools/row-gen's identityAttrEvidence is its best specimen.
	Data int

	// Code is the count of literals inside a function body. These are the
	// decision positions - a lookup, a branch, a comparison keyed on one
	// named type - and this is the number the standing rule is about. It
	// should fall over time and each fall should be claimed.
	Code int
}

// estateGenCohortReason is shared by the 33 per-cohort override files. They
// are one mechanism, not 33 decisions, so they carry one reason.
//
// estate-gen renders the e2e cohort fixtures. Its typeOverrides tables are
// keyed by type by construction, and every entry already carries a Reasons
// string quoting the `terraform validate` error the override silences - the
// sanctioned form. The Code half is the debt: `g.byType["aws_..."]` reaches
// past the generic pass to name a sibling directly, and each one is a
// candidate for the treatment 4251066763 gave capacity_provider_arns.
const estateGenCohortReason = "estate-gen per-cohort typeOverrides: Data entries are the keyed override table, each carrying its own " +
	"Reasons string quoting the provider validation error it silences. Code entries are `g.byType[\"aws_...\"]` sibling " +
	"lookups that reach past the generic pass; each is derivation debt of the exact shape commit 4251066763 retired for " +
	"aws_ecs_daemon.capacity_provider_arns."

// typeLiteralSurfaces is the registry. Both directions are enforced: a file
// with literals and no entry fails, and an entry whose file is gone, whose
// literals are gone, or whose counts have moved in either direction fails too.
//
// Counts were produced by this file's own walk at 6da372bfd2, not copied from
// anywhere. To change one, say in the commit message which literal moved and
// why. A rising Code count is the thing this guard exists to make somebody
// argue for.
var typeLiteralSurfaces = map[string]typeLiteralSurface{
	// ---- product path -------------------------------------------------
	"internal/live/discovery/discovery.go": {
		Reason: "iamServiceLinkedRoleSibling names the one pair, aws_iam_role and aws_iam_service_linked_role (issue #302). " +
			"IAM has no ListServiceLinkedRoles operation, so aws_iam_role's own native list call returns every " +
			"service-linked role too, right alongside the ordinary ones. This pair's ratified identity does not carry the " +
			"same ImportSyntax or IdentityAttrs the way defaultAdopterSiblings' three prefix-derived pairs do (aws_iam_role " +
			"imports by bare role name; aws_iam_service_linked_role's documented import ID is the role's ARN), so no " +
			"prefix-plus-table derivation covers it generically the way defaultAdopterSiblings covers #305's family. " +
			"tagging.go's iamRoleEntry already hand-curates the identical fact one join stage earlier, for the ARN-shape " +
			"leg rather than this native-list leg. Issue #394 added a second reader of the same one fact " +
			"(typeNeedsResourceObjectToRecompose, which asks the sweep-partition question 'is typeName either side of " +
			"this pair' from outside iamServiceLinkedRoleSibling) and moved both literals from a local const inside " +
			"the function to the two package-level consts iamRoleTypeName/iamServiceLinkedRoleTypeName so the second " +
			"reader could name the pair by identifier rather than retyping it - Code fell to 0 and Data rose to 2 " +
			"with no new literal surface. gauntlet:record-located-destroy (corpus-rds-complete-postgres's day2_remove " +
			"unit) added a second such pair the identical way: rdsClusterInstanceSibling names aws_db_instance and " +
			"aws_rds_cluster_instance, via the two package-level consts awsDBInstanceTypeName/" +
			"awsRDSClusterInstanceTypeName, for the same reason - live/mapping.json joins both to one CFN type " +
			"(AWS::RDS::DBInstance, an Aurora cluster member IS an RDS DB instance in AWS's own model, not a distinct " +
			"resource kind, so RDS has one DescribeDBInstances call covering both), and the pair fails " +
			"sameRatifiedIdentity for real (aws_db_instance reads ['identifier']; aws_rds_cluster_instance reads " +
			"['id','identifier']), so it needs the same hand-verified pair, not a generic derivation, exactly as " +
			"iamServiceLinkedRoleSibling's own pair does. Measured against live/mapping.json (2026-08-25): 21 CFN " +
			"types map more than one admitted TF type (46 types total, most of them aws_alb_*/aws_lb_* naming " +
			"aliases already excluded from this shape elsewhere) - each remaining pair needs its own " +
			"sameRatifiedIdentity verification before it could fold in here too, which is why this stays a " +
			"hand-verified pair-by-pair registry rather than a blanket same-CFN-type rule. Data rose to 4, Code " +
			"stayed 0.",
		Data: 4, Code: 0,
	},
	"internal/live/foreign/classify.go": {
		Reason: "foreign-resource matchTable: which argument makes a live resource the one a declared block means. Each entry's " +
			"justification is an AWS uniqueness guarantee (ELBv2 names unique per account/region, an SNS ARN built out of its " +
			"name), which no provider schema states, so it cannot be read off one.",
		Data: 10, Code: 0,
	},
	"internal/live/harness/assumptions.go": {
		Reason: "SanctionedCredentialExclusions: the types with no admission route at all, because their identity IS " +
			"credential material and a marker goes in a tag rather than in the secret. Four at the start, shrunk to " +
			"two by the 2026-08-23 ruling (#365 ruling 5), which moved aws_iam_access_key and aws_iot_certificate to " +
			"internal/live/identity/located.go's toggle-gated exclusion below - they are admitted by default now, " +
			"through a route this ratchet's own check does not read, so its \"none of them is admitted\" claim would " +
			"be false for them. Grown back to three by issue #431's provider-wide credentialMaterial sweep " +
			"(tools/credential-sweep), which added aws_wafv2_api_key: not a fresh ruling on the maintainer's part but " +
			"a measurement that the code (LocatedType's sensitiveIdentityAttr check) already refused it unconditionally " +
			"with no ledger entry naming it. A ruling - or a measurement standing in the maintainer's place for a fact " +
			"the code already enforces - is the one thing that cannot be derived from a schema, and the assumption " +
			"exists precisely to fail when the veto set drifts away from what it names. Naming them here is the " +
			"check, not the hand-wiring. Exported (issue #418) so tools/readiness-gen can read it directly.",
		Data: 3, Code: 0,
	},
	"internal/live/identity/docimportid.go": {
		Reason: "VariadicTrailingImportIDTypes: aws_security_group_rule alone, the one type today whose documented " +
			"import grammar (\"SOURCE[_SOURCE]*\") ends in a variadic tail AND whose provider-side importer has " +
			"been verified, from hashicorp/terraform-provider-aws's own resourceSecurityGroupRuleImport source " +
			"(GitHub issue #384), to classify each trailing token by its own content rather than by position - a " +
			"fact about the provider's IMPORT FUNCTION that no schema and no scraped documentation states, so " +
			"neither resolveDocumentedImportID's schema-only view nor tools/row-gen's doc-only view can derive it. " +
			"variadicTrailingGroup, the only reader, is keyed generically off Component.SoleElement (reach: the " +
			"SoleElement population, one row today, `grep -c \"SoleElement: true\"` in table_generated.go) and " +
			"refuses any type not in this map regardless of shape; growing the map past this one entry is a " +
			"per-type provider-source verification, not a schema-derivable count.",
		Data: 1, Code: 0,
	},
	"internal/live/identity/located.go": {
		Reason: "strictSecretsLocatedExclusion: aws_iam_access_key and aws_iot_certificate, the maintainer's 2026-08-23 " +
			"ruling (rfc/20260823-foundation-order-ruling.md, ruling 5) moving them off the four-type unconditional " +
			"veto this file carried before (live/HARNESS.md's credential-exclusions-are-sanctioned ratchet, a " +
			"three-entry set as of issue #431) onto the " +
			"same strict { secrets } toggle a RECORD_BACKED type's SecretMaterial already uses: stored by default, " +
			"refused under strict.Refuse. Kept a named list rather than a schema-derived one on purpose - the " +
			"2026-08-22 census this ruling cites (issue #365 population 2) measured against the real provider that " +
			"no schema fact tells a type whose secret a Read restores from one whose secret a Read loses forever, so " +
			"a generic identity.CredentialMaterial gate here would refuse types that need no refusing. A ruling is " +
			"the one thing that cannot be derived.",
		Data: 2, Code: 0,
	},
	"internal/live/identity/parent.go": {
		Reason: "parentReadRemovable and foldParentTypes: two one-entry maps whose growth rule is written into their own doc " +
			"comments as \"one provider-behavior verification at a time\". The fact recorded is what a live " +
			"GetBucketPolicy-style read means when it finds nothing, which is provider behaviour and not schema.",
		Data: 3, Code: 0,
	},
	"internal/live/lint/receipt_leaf.go": {
		Reason: "receiptType: live/RECEIPTS.md defines a receipt AS an aws_ssm_parameter under /tofu-receipts/. This is a " +
			"protocol constant this fork authored, not a claim about a provider type, and changing it changes the protocol.",
		Data: 1, Code: 0,
	},
	"live/residue.go": {
		Reason: "EmulatorBlocked (#26): which floci gap blocks which type, read off live/e2e/run.sh and the harness rather than " +
			"any artifact. Its own doc comment already says it is hand data. Shrinks as the emulator improves.",
		Data: 4, Code: 0,
	},
	"tools/row-gen/identityattr.go": {
		Reason: "identityAttrEvidence: the hand half of row-gen's criterion 3, every entry carrying the evidence actually " +
			"inspected. Already held exact by row-gen's own ratchet, which fails on both stale entries and undocumented " +
			"exceptions - this file is the same contract one level up.",
		Data: 8, Code: 0,
	},
	"tools/survey-gen/classify.go": {
		Reason: "opsExcluded: aws_iam_access_key, one of the four sanctioned credential-material exclusions (a ruling, not " +
			"hand-wiring). The secret half is unreadable after create, which is a fact about the resource's own contents " +
			"and is in no schema. It was two until 2026-08-17, when the maintainer withdrew aws_acm_certificate_validation " +
			"(\"waiter: records only that DNS validation finished\") - a judgment about what the resource means, not about " +
			"what can name it, and the classifier settles the naming question from the schemas.",
		Data: 1, Code: 0,
	},
	"tools/survey-gen/render.go": {
		Reason: "summaryOverrides: one row, aws_iam_role_policy_attachment, where the survey's strongest-path classing and " +
			"the identity table's structural grouping disagree, so the rendered tally reproduces the survey's counts instead " +
			"of restyling them. A row leaving live/LIMITATIONS.md's wrinkle list should leave here in the same change. It was " +
			"two until 2026-08-17: aws_eip's override said 'count it as marker, the table shows the fork's list-plus-content " +
			"wiring', and there is no such wiring - bindCountBySlot binds an eip by its tofu-slot tag, which is a marker. The " +
			"table now says marker too, so there is nothing left to override and the rendered counts did not move.",
		Data: 1, Code: 0,
	},
	"tools/survey-gen/untaggable_render.go": {
		Reason: "nonAWSAdmittedUntaggable (issue #326): a named ledger, not hand-wiring in the sense this file exists to " +
			"catch - it is the exact escape hatch contributing/LIVE-TABLES.md and tools/row-gen/annotations.json's own doc " +
			"comment describe, for a ruling that genuinely cannot be derived from live/survey-full.json. That artifact is " +
			"CloudFormation-registry-backed and by construction carries only the AWS provider's roster, so it has no row, " +
			"and never will, for a type belonging to a different provider entirely. kubernetes_cluster_role_binding, " +
			"kubernetes_config_map, kubernetes_namespace and kubernetes_storage_class are the first four admitted types " +
			"to cross that line; each entry's taggability was verified directly against the real, current " +
			"hashicorp/kubernetes provider schema (markers.Taggable/TagSurface reading - see tools/row-gen/annotations.json's " +
			"own rulings for these four types, which record the same finding) rather than derived from the AWS survey. " +
			"Retires the same way those four rulings' own Exit fields say: once there is a non-AWS analogue of " +
			"live/survey-full.json this derivation can read instead.",
		Data: 4, Code: 0,
	},
	"tools/floci-capability-gen/tagging.go": {
		Reason: "taggingRecipes: seven hand-verified probes of floci's tagging index. Each is a literal sequence of `aws` CLI " +
			"calls for one service - a create, a native tag read-back, a cleanup - which is an experiment against an emulator " +
			"and not something any provider artifact describes. Data and Code counts pair one to one: each recipe names its " +
			"type in the struct and again in the closure's own result.",
		Data: 7, Code: 7,
	},
	"tools/wo-sweep/main.go": {
		Reason: "the two proven write-only instances (aws_s3_object.content, aws_iam_access_key.secret) probed raw so the " +
			"sweep's report can state whether its own derived buckets catch them. Naming them is the control in the " +
			"experiment; deriving them would make the check circular.",
		Data: 0, Code: 2,
	},
	"tools/row-gen/notimportable.go": {
		Reason: "notImportableExempt: aws_acm_certificate_validation, the one type issue #331's importable=false veto must not " +
			"act on. classify.go's 2026-08-17 ruling already admits it on the nameability axis (identity.Derivable resolves " +
			"certificate_arn from configuration, no Importer ever in that path); the importability gap is new evidence on a " +
			"different axis and reversing the ruling over it is the maintainer's call, not a mechanical veto tied to this fix.",
		Data: 1, Code: 0,
	},
	"tools/importer-probe/main.go": {
		Reason: "controlTypes: four types with a documented Import section (issue #331), probed alongside the " +
			"identity_schema_wire_only bucket - itself read at runtime from live/identity-sources.json, not hand-listed " +
			"here - to prove the discriminator reports HAS_IMPORTER for something known to have one. Same shape as " +
			"tools/wo-sweep's own positive control, immediately above: naming the control is the experiment, deriving it " +
			"would make the check circular.",
		Data: 4, Code: 0,
	},
	"tools/readiness-gen/build.go": {
		Reason: "noLocatedIdentityAttrTypes: three MarkerlessTypes members - aws_apigatewayv2_routing_rule, " +
			"aws_network_interface_permission, aws_notifications_event_rule - measured 2026-08-29 (issue #430) " +
			"against a live hashicorp/aws 6.59.0 pull to export no top-level string \"id\" at all, which " +
			"identity.LocatedType's bare-id fallback needs and this generator's other two static proxies " +
			"(identity.NotImportable, identity.IDNotProvenWholeTypes) cannot see. Same shape as this file's own " +
			"harness.SanctionedCredentialExclusions entry: a live-schema fact readiness-gen's committed inputs " +
			"cannot derive, named rather than guessed at.",
		Data: 3, Code: 0,
	},

	// ---- estate-gen shared machinery ----------------------------------
	"tools/estate-gen/gen.go": {
		Reason: "iamRoleRefExpr and planCohort's `supporting[\"aws_iam_role\"]`: every cohort gets one shared IAM role donor, " +
			"because a required *_role_arn argument names another resource's ARN rather than its identity-table argument. " +
			"This is the one cross-cohort wire in the generic pass and it is Code, so it is visible here.",
		Data: 0, Code: 2,
	},
	"tools/estate-gen/overrides.go": {
		Reason: "the cross-cohort typeOverrides table plus six shared sibling-ref helpers (eksClusterNameRef, " +
			"cognitoUserPoolIDRef and friends). The helpers exist so the same `g.byType` lookup is spelled once rather than " +
			"per cohort; they are still Code, and each is a candidate for the generic treatment 4251066763 applied.",
		Data: 4, Code: 8,
	},

	// ---- estate-gen per-cohort overrides ------------------------------
	"tools/estate-gen/overrides_cohort_ai_location.go":          {Reason: estateGenCohortReason, Data: 21, Code: 1},
	"tools/estate-gen/overrides_cohort_apigateway.go":           {Reason: estateGenCohortReason, Data: 24, Code: 22},
	"tools/estate-gen/overrides_cohort_compute_platforms.go":    {Reason: estateGenCohortReason, Data: 13, Code: 0},
	"tools/estate-gen/overrides_cohort_connect_euc.go":          {Reason: estateGenCohortReason, Data: 11, Code: 1},
	"tools/estate-gen/overrides_cohort_data.go":                 {Reason: estateGenCohortReason, Data: 9, Code: 3},
	"tools/estate-gen/overrides_cohort_data_movement.go":        {Reason: estateGenCohortReason, Data: 22, Code: 8},
	"tools/estate-gen/overrides_cohort_databases.go":            {Reason: estateGenCohortReason, Data: 18, Code: 6},
	"tools/estate-gen/overrides_cohort_devtools.go":             {Reason: estateGenCohortReason, Data: 12, Code: 2},
	"tools/estate-gen/overrides_cohort_dynamodb_elasticache.go": {Reason: estateGenCohortReason, Data: 4, Code: 0},
	"tools/estate-gen/overrides_cohort_ec2_core.go":             {Reason: estateGenCohortReason, Data: 7, Code: 1},
	"tools/estate-gen/overrides_cohort_ec2_networking.go":       {Reason: estateGenCohortReason, Data: 12, Code: 0},
	"tools/estate-gen/overrides_cohort_ecs_eks.go":              {Reason: estateGenCohortReason, Data: 11, Code: 1},
	"tools/estate-gen/overrides_cohort_governance.go":           {Reason: estateGenCohortReason, Data: 13, Code: 1},
	"tools/estate-gen/overrides_cohort_iam_ecr.go":              {Reason: estateGenCohortReason, Data: 4, Code: 1},
	"tools/estate-gen/overrides_cohort_identity.go":             {Reason: estateGenCohortReason, Data: 19, Code: 2},
	"tools/estate-gen/overrides_cohort_iot.go":                  {Reason: estateGenCohortReason, Data: 4, Code: 0},
	"tools/estate-gen/overrides_cohort_lambda.go":               {Reason: estateGenCohortReason, Data: 4, Code: 0},
	"tools/estate-gen/overrides_cohort_media.go":                {Reason: estateGenCohortReason, Data: 2, Code: 0},
	"tools/estate-gen/overrides_cohort_messaging.go":            {Reason: estateGenCohortReason, Data: 8, Code: 4},
	"tools/estate-gen/overrides_cohort_networking_advanced.go":  {Reason: estateGenCohortReason, Data: 30, Code: 23},
	"tools/estate-gen/overrides_cohort_observability.go":        {Reason: estateGenCohortReason, Data: 21, Code: 5},
	"tools/estate-gen/overrides_cohort_rds.go":                  {Reason: estateGenCohortReason, Data: 10, Code: 5},
	"tools/estate-gen/overrides_cohort_remainder.go":            {Reason: estateGenCohortReason, Data: 9, Code: 1},
	"tools/estate-gen/overrides_cohort_route53_cloudfront.go":   {Reason: estateGenCohortReason, Data: 18, Code: 0},
	"tools/estate-gen/overrides_cohort_sagemaker.go":            {Reason: estateGenCohortReason, Data: 14, Code: 4},
	"tools/estate-gen/overrides_cohort_security.go":             {Reason: estateGenCohortReason, Data: 32, Code: 12},
	"tools/estate-gen/overrides_cohort_storage.go":              {Reason: estateGenCohortReason, Data: 13, Code: 1},
	"tools/estate-gen/overrides_cohort_stragglers.go":           {Reason: estateGenCohortReason, Data: 9, Code: 0},
	"tools/estate-gen/overrides_cohort_streaming.go":            {Reason: estateGenCohortReason, Data: 12, Code: 4},
}

const (
	// typeLiteralDataTotal and typeLiteralCodeTotal are the registry's sums,
	// pinned so that moving a count from one file to another - which every
	// per-file check passes individually - still has to be claimed.
	// 425 -> 424 on 2026-08-17: survey-gen's summaryOverrides lost its
	// aws_eip row when the "list + content match" token was renamed and the
	// eip's Path cell was corrected to marker, which is what actually binds
	// it. One literal deleted, none moved.
	// 424 -> 423, same day: survey-gen's opsExcluded lost
	// aws_acm_certificate_validation when the maintainer withdrew the
	// waiter exclusion. One literal deleted, none moved.
	// 423 -> 421 data, 125 -> 124 code, on 2026-08-18 (issue #136): deleted
	// the orphaned aws_lambda_layer_version override
	// (tools/estate-gen/overrides_cohort_lambda.go) - that type was
	// retracted from admission entirely by issue #249 (identity.MarkerlessTypes),
	// not merely unfixtured, so the override had nothing left to apply to.
	// Two Data literals (the map key and its NeedsSupporting: []string{"aws_s3_bucket"}
	// entry) and one Code-classed g.byType["aws_s3_bucket"] lookup inside
	// the deleted Apply closure, all deleted, none moved.
	// 421 -> 426 data, 124 -> 126 code, same day: apigateway's aws_lb was
	// missing both subnets and subnet_mapping (neither schema-Required),
	// so terraform validate refused it with "one of `subnet_mapping,subnets`
	// must be specified" - the same ExactlyOneOf-shaped gap this file's
	// header measured (live/import-grammar.json already extracts it as
	// aws_lb's own exclusive_groups entry, which estate-gen does not yet
	// consume generically). Two new typeOverridesApigateway entries
	// (aws_lb, aws_api_gateway_vpc_link) added five Data literals (two map
	// keys, three inside aws_api_gateway_vpc_link's
	// NeedsSupporting: []string{"aws_lb","aws_subnet","aws_vpc"}) and two
	// Code-classed g.byType[...] sibling lookups (aws_lb's Apply reads
	// "aws_subnet", aws_api_gateway_vpc_link's Apply reads "aws_lb").
	// 126 -> 128 code, 2026-08-19 (issue #302): internal/live/discovery/
	// discovery.go's new iamServiceLinkedRoleSibling names the one
	// aws_iam_role/aws_iam_service_linked_role pair as a function-body
	// const, because that pair's ratified identity does not match the way
	// defaultAdopterSiblings' three prefix-derived pairs do and so cannot
	// be derived the same way. Two Code literals, no Data, nothing moved.
	// 426 -> 430 data, 2026-08-19 (issue #331 follow-up): new
	// tools/importer-probe/main.go's controlTypes, four fixed positive
	// controls (aws_iam_role, aws_s3_bucket, aws_iam_role_policy_attachment,
	// aws_security_group) proving the tool's Importer discriminator reports
	// HAS_IMPORTER for something known to have one. The tool's actual
	// subject - the identity_schema_wire_only bucket - is read from
	// live/identity-sources.json at runtime rather than hand-listed, so
	// this file gains only the control set, same shape as tools/wo-sweep's
	// own two-literal control immediately above it in the registry. Four
	// Data literals added, no Code, nothing moved.
	// 430 -> 431 data, 2026-08-20 (issue #331's derived fix): new
	// tools/row-gen/notimportable.go's notImportableExempt, one entry -
	// aws_acm_certificate_validation, the type the new importable=false veto
	// must not act on because classify.go already rules it admitted on a
	// different axis (nameability). One Data literal added, no Code, nothing
	// moved.
	// 431 -> 435 data, 2026-08-20 (issue #326, merged after #331): new
	// tools/survey-gen/untaggable_render.go's nonAWSAdmittedUntaggable, a
	// named ledger ruling taggability for the first four non-AWS admitted
	// types (the Kubernetes provider's kubernetes_cluster_role_binding,
	// kubernetes_config_map, kubernetes_namespace, kubernetes_storage_class)
	// live/survey-full.json structurally has no row for and never will.
	// Four Data literals added, no Code, nothing moved.
	// 435 -> 437 data, 2026-08-22 (issue #365 population 2): new
	// internal/live/identity/located.go's sanctionedCredentialExclusion, two
	// entries - aws_iam_access_key, aws_iot_certificate, the markerless
	// subset of the maintainer's four sanctioned credential exclusions,
	// honored on the located route now that its general credential veto was
	// narrowed to the identity attributes it actually records. Two Data
	// literals added, no Code, nothing moved.
	// 437 -> 435 data, 2026-08-23 (issue #365 ruling 5): the same two
	// literals stay in internal/live/identity/located.go (renamed
	// sanctionedCredentialExclusion -> strictSecretsLocatedExclusion, same
	// two names, so that file's own count is unchanged), but
	// internal/live/harness/assumptions.go's SanctionedCredentialExclusions
	// drops the two literal type names it carried for them - they moved off
	// that unconditional, admission-table-wide ratchet onto a
	// strict{secrets}-gated one, so the harness ratchet's own four-entry
	// list shrinks to two. Two Data literals removed, no Code, nothing
	// else moved.
	// 435 -> 436 data, 2026-08-23 (issue #384): new
	// internal/live/identity/docimportid.go's VariadicTrailingImportIDTypes,
	// one entry - aws_security_group_rule, the ratified allowlist recording
	// that this type's provider-side importer is verified, from the
	// provider's own source, to classify its documented import grammar's
	// variadic trailing tokens by content rather than by position. One
	// Data literal added, no Code, nothing moved.
	// 436 -> 438 data, 128 -> 126 code, 2026-08-23 (issue #394): internal/live/discovery/
	// discovery.go's iamServiceLinkedRoleSibling loses its two-literal
	// function-body const; iamRoleTypeName and iamServiceLinkedRoleTypeName
	// carry the same pair as package-level consts instead, so
	// typeNeedsResourceObjectToRecompose (issue #394's sweep-partition
	// helper, which needs to ask "is typeName either side of this pair"
	// from outside iamServiceLinkedRoleSibling) can name it by identifier
	// rather than retyping the two literals a second time. Two literals
	// moved from Code to Data, none added or deleted.
	// 438 -> 440 data, 2026-08-25 (gauntlet:record-located-destroy,
	// corpus-rds-complete-postgres's day2_remove unit): internal/live/discovery/
	// discovery.go gains a second overlapping-list-call companion pair,
	// rdsClusterInstanceSibling (aws_db_instance / aws_rds_cluster_instance,
	// the same shape iamServiceLinkedRoleSibling's own pair is), named via
	// two new package-level consts, awsDBInstanceTypeName and
	// awsRDSClusterInstanceTypeName, following the identical Data-not-Code
	// precedent the entry above already set. Two Data literals added, no
	// Code, nothing moved.
	// 440 -> 443 data, 126 -> 127 code, 2026-08-29 (issue #432): two
	// fixture bugs found triaging the acceptance cohorts, each fixed with a
	// new typeOverrides entry rather than a hand-edit, following this
	// file's own "regenerate, never hand-edit" rule.
	// tools/estate-gen/overrides_cohort_identity.go gained one entry -
	// aws_iam_saml_provider, whose saml_metadata_document the generic
	// pass's 11-character placeholder failed the provider's own
	// 1000-character minimum against. One Data literal (the map key), no
	// Code, no NeedsSupporting.
	// tools/estate-gen/overrides_cohort_storage.go gained one entry -
	// aws_fsx_lustre_file_system, whose import_path was wired to a bare
	// sibling reference with no "s3://" prefix (seedFromExample drops the
	// literal template text around a doc example's interpolated
	// reference). Fixed with a NeedsSupporting: []string{"aws_s3_bucket"}
	// entry plus a g.byType["aws_s3_bucket"] sibling lookup in its Apply
	// closure - the same shape this log's apigateway entry above already
	// used for aws_api_gateway_vpc_link's aws_lb lookup, and the same
	// class of debt commit 4251066763 generically retired for the plural
	// <base>_arns shape; a generic fix here would mean teaching
	// seedFromExample to keep a doc example's literal template text around
	// an interpolated reference, which is broader than one type and out of
	// #432's scope. Two Data literals (the map key and the NeedsSupporting
	// entry), one Code literal, nothing moved.
	// 443 -> 444 data, 2026-08-28 (issue #431): internal/live/harness/
	// assumptions.go's sanctionedCredentialExclusions gains aws_wafv2_api_key,
	// measured by issue #431's provider-wide credentialMaterial sweep
	// (tools/credential-sweep) as already unconditionally refused by
	// LocatedType's sensitiveIdentityAttr check, with no ledger entry naming
	// it - a third type with no admission route at all, not a fourth kind of
	// veto. One Data literal added, no Code, nothing moved.
	// 444 -> 447 data, 2026-08-29 (issue #430): new tools/readiness-gen/build.go's
	// noLocatedIdentityAttrTypes, three entries - aws_apigatewayv2_routing_rule,
	// aws_network_interface_permission, aws_notifications_event_rule - found by
	// issue #430's full re-evaluation of the 159-type MarkerlessTypes population
	// against a live hashicorp/aws 6.59.0 pull (CHOUDOUFU_LIVE_SCHEMAS=1's
	// TestLocatedTypePopulation): three types classify's static locatedApprox
	// proxy called in-contract that identity.LocatedType itself already refuses,
	// because none of the three exports a top-level string "id" and
	// identity.LocatedIdentityPlanFor's bare-id fallback has nothing to read.
	// The same live-schema-fact-classify-cannot-derive shape as this file's own
	// harness.SanctionedCredentialExclusions entry. Three Data literals added,
	// no Code, nothing moved.
	typeLiteralDataTotal = 447
	typeLiteralCodeTotal = 127

	// typeLiteralSweepFloor is the anti-tamper leg, in the spirit of
	// identity_golden_pin_test.go's identityGoldenSweepFloor and
	// admission_coverage_test.go's universeFloor.
	//
	// Every count above can be satisfied by sweeping less: drop a root,
	// tighten the file filter, widen what counts as "generated", and the
	// registry empties one reasonable-looking edit at a time. This is the
	// number of Go files the walk must visit, and it is not allowed to fall.
	// It is far enough below the current count that deleting files is not an
	// event, and far enough above zero that a broken walk is.
	typeLiteralSweepFloor = 400
)

// generatedTypeLiteralFiles pins the escape hatch itself.
//
// A file carrying "Code generated ... DO NOT EDIT" in its first 20 lines is
// exempt from the registry, which is correct - the generated tables ARE the
// derived product - but it is also the cheapest way to smuggle hand-wiring
// past this guard. So the exempt set is exact rather than a rule: a fourth
// generated file, or a hand-written one that grows the header, fails here.
var generatedTypeLiteralFiles = []string{
	"internal/live/identity/contentmatch_generated.go",
	"internal/live/identity/discoverablefallback_generated.go",
	"internal/live/identity/docimportid_generated.go",
	"internal/live/identity/idnotwhole_generated.go",
	"internal/live/identity/markerless_generated.go",
	"internal/live/identity/notimportable_generated.go",
	"internal/live/identity/table_generated.go",
	"internal/live/lint/admission_generated.go",
}

// providerTypeLiteral matches a string literal that is exactly a provider
// resource type name. The whole-literal anchor is deliberate: "aws_" on its
// own is a prefix test and "aws_%s" is a template, and both are the generic
// derivation rather than a named type.
//
// The prefix list is wider than the tree needs today, and that is the point.
// Every literal the registry above accounts for is an aws_ one, because the
// live path is AWS-only so far; a guard hard-coded to aws_ would then be
// silent through the whole of a second provider's onboarding, which is the
// "check narrower than the thing it guards" shape this repository has now
// found four times.
//
// The list is measured, not assumed, and the measurement corrected a first
// draft that also carried "oci". At 6da372bfd2 the seven prefixes here add
// exactly zero literals to the census - the only non-aws type names in the
// tree are two "google_compute_instance" mentions in upstream's jsonplan and
// jsonstate doc COMMENTS, which the AST walk does not see. "oci" was
// different: internal/command/cliconfig spells "oci_default_credentials" and
// "oci_mirror", which are upstream CLI-config BLOCK names and not resource
// types at all. A prefix goes on this list once it has been checked against
// the tree, not because a provider exists.
var providerTypeLiteral = regexp.MustCompile(`^(aws|google|azurerm|azuread|alicloud|kubernetes|helm)_[a-z0-9]+(_[a-z0-9]+)*$`)

var generatedHeader = regexp.MustCompile(`Code generated .* DO NOT EDIT`)

// typeLiteralCensus is one file's counts.
type typeLiteralCensus struct {
	Data, Code int
	// Sample is a literal from the file, so a failure names something the
	// reader can grep for rather than only a number.
	Sample string
	Line   int
}

// TestEveryTypeLiteralSurfaceIsRegistered is the forward direction: a
// concrete provider type spelled out in hand-written Go must be registered.
func TestEveryTypeLiteralSurfaceIsRegistered(t *testing.T) {
	census, files := walkTypeLiterals(t)

	if files < typeLiteralSweepFloor {
		t.Errorf("the walk visited %d Go files, below the floor of %d.\n"+
			"Every count in typeLiteralSurfaces can be satisfied by sweeping less, so this is the leg that is not allowed to move.\n"+
			"If the tree genuinely shrank, lower the floor in its own commit that says so - not in the commit that shrank it.",
			files, typeLiteralSweepFloor)
	}
	if len(census) == 0 {
		t.Fatal("found no provider type literals anywhere; the walk is broken, not the tree")
	}

	for _, file := range sortedCensusKeys(census) {
		got := census[file]
		want, registered := typeLiteralSurfaces[file]
		if !registered {
			t.Errorf("%s:%d spells out the provider type %q (%d in tables, %d inside function bodies), and typeLiteralSurfaces does not mention the file.\n"+
				"A named resource type in hand-written Go is hand-wiring until somebody says otherwise: it buys the cohort in front of you and nothing in the ~1700-type tail.\n"+
				"If the derivation can be fixed a layer up, fix it there. If it genuinely cannot, add the file here with the reason and the counts - the reason is the deliverable, not the entry.",
				file, got.Line, got.Sample, got.Data, got.Code)
			continue
		}
		if got.Data != want.Data || got.Code != want.Code {
			t.Errorf("%s: registered as %d table / %d in-function literals, walk finds %d / %d.\n%s\n"+
				"Registered reason: %s",
				file, want.Data, want.Code, got.Data, got.Code,
				typeLiteralAdvice(want.Data, want.Code, got.Data, got.Code), want.Reason)
		}
	}

	var data, code int
	for _, s := range typeLiteralSurfaces {
		data += s.Data
		code += s.Code
	}
	if data != typeLiteralDataTotal || code != typeLiteralCodeTotal {
		t.Errorf("typeLiteralSurfaces sums to %d table / %d in-function literals, pinned at %d / %d.\n"+
			"The totals are pinned separately from the per-file counts so that moving hand-wiring between files, which every per-file check passes, still has to be claimed.",
			data, code, typeLiteralDataTotal, typeLiteralCodeTotal)
	}
}

// typeLiteralAdvice says which direction of movement is the alarming one.
// They are not symmetric: Code rising is the standing rule being broken, and
// Code falling is the campaign working and should be banked, not absorbed.
func typeLiteralAdvice(wantData, wantCode, gotData, gotCode int) string {
	switch {
	case gotCode > wantCode:
		return "IN-FUNCTION LITERALS ROSE. That is a named type reached for inside control flow, which is the shape this guard exists to catch:\n" +
			"it buys the estate in front of you and nothing in the long tail an unfamiliar user's configuration lands in.\n" +
			"Before re-pinning, check whether the fix belongs a layer up - commit 4251066763 deleted exactly one of these by teaching\n" +
			"siblingRef the plural <base>_arns shape, and that one fix wired 12 further arguments across 7 cohorts for free."
	case gotCode < wantCode:
		return "In-function literals fell, which is the campaign working. Re-pin and say in the commit message which derivation now covers it."
	case gotData > wantData:
		return "The hand table grew. That is the sanctioned form, but it is still a type this fork describes by hand: make sure the new entry\n" +
			"carries its own prose reason next to it, the way row-gen's identityAttrEvidence does."
	default:
		return "The hand table shrank. If a derivation replaced the entry, say which; if the file was merely reorganised, re-pin."
	}
}

// TestTypeLiteralSurfacesAreReal is the reverse direction. An allowlist that
// outlives its subject passes forever, which is the failure
// live/flociimage_test.go was written to make impossible for floci pins.
func TestTypeLiteralSurfacesAreReal(t *testing.T) {
	census, _ := walkTypeLiterals(t)
	root := repoRoot(t)

	for _, file := range sortedSurfaceKeys(typeLiteralSurfaces) {
		entry := typeLiteralSurfaces[file]
		if strings.TrimSpace(entry.Reason) == "" {
			t.Errorf("typeLiteralSurfaces registers %s with no reason.\n"+
				"A bare list is the thing this file replaces: the entry has to say why a derived value cannot stand there.", file)
		}
		if _, err := os.Stat(filepath.Join(root, file)); err != nil {
			t.Errorf("typeLiteralSurfaces registers %s, which does not exist: %v\n"+
				"Delete the entry. A registered exception naming a file that is gone reads as a live one.", file, err)
			continue
		}
		if _, present := census[file]; !present {
			t.Errorf("typeLiteralSurfaces registers %s (%d table / %d in-function), but the file spells out no provider type at all.\n"+
				"Either the derivation caught up and the entry should go - which is a result worth saying out loud - or the walk stopped seeing the file, which is worse.",
				file, entry.Data, entry.Code)
		}
	}
}

// TestNoTypeNameIsAssembledFromLiterals closes the cheap evasion.
//
// providerTypeLiteral anchors on the whole literal, so `"aws_ecs_" +
// "capacity_provider"` would slip past every check above. Measured at
// 6da372bfd2 there are seven `"aws_" + x` concatenations in the tree and all
// seven concatenate with a VARIABLE - they are the generic pass building a
// candidate name out of a service and a base, which is the derivation itself.
// Concatenating two literals is a different act and there are none, so the
// pin is zero.
//
// The first version of this check asked whether the LEFT operand started with
// "aws_", and an audit defeated it in one byte: `"aws" + "_ecs_capacity_provider"`
// is the same act with the split moved one character earlier, and neither
// operand matches providerTypeLiteral either, so it was invisible to every leg
// of this file. `fmt.Sprintf("aws_%s", "ecs_capacity_provider")` was invisible
// for the same reason - the check looked at the spelling of the pieces rather
// than at the value they make.
//
// So the check now FOLDS the expression and tests the result. Anything whose
// value is knowable from literals alone - a concatenation chain of any depth,
// or a Sprintf whose format and arguments are all literals - is compared
// against providerTypeLiteral exactly as a whole literal would be. A variable
// anywhere in the expression makes it unfoldable, which is still correct and
// still the stated bound: those are the generic pass building a candidate name.
func TestNoTypeNameIsAssembledFromLiterals(t *testing.T) {
	var found []string

	forEachHandWrittenGoFile(t, func(rel string, f *ast.File, fset *token.FileSet) {
		ast.Inspect(f, func(n ast.Node) bool {
			e, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			// A bare literal is the registry's business, not this test's.
			if _, isLit := e.(*ast.BasicLit); isLit {
				return true
			}
			v, folded := foldStringExpr(e)
			if !folded || !providerTypeLiteral.MatchString(v) {
				return true
			}
			found = append(found, fmt.Sprintf("%s:%d: assembles %q", rel, fset.Position(e.Pos()).Line, v))
			// Do not descend: an inner sub-expression of the same fold would
			// be reported twice, and the outermost one names the whole act.
			return false
		})
	})

	if len(found) != 0 {
		sort.Strings(found)
		t.Errorf("a provider type name is assembled from literals at:\n  %s\n"+
			"Spelling `\"aws_ecs_\" + \"capacity_provider\"`, `\"aws\" + \"_ecs_capacity_provider\"` or "+
			"`fmt.Sprintf(\"aws_%%s\", \"ecs_capacity_provider\")` hides the type from typeLiteralSurfaces while doing exactly "+
			"what a whole literal would do. Write it as one literal and register it, or - better - derive it.",
			strings.Join(found, "\n  "))
	}
}

// foldStringExpr evaluates a string expression whose value is knowable from
// literals alone, and reports whether it could.
//
// It handles the two shapes that have actually been used to hide a type name:
// a `+` chain, and fmt.Sprintf with an all-literal format and argument list.
// Anything else - a variable, a function call, a const identifier - is
// unfoldable, which is the deliberate bound: a name built out of a variable is
// the generic derivation this whole project is trying to write more of.
func foldStringExpr(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		return stringLit(x)
	case *ast.ParenExpr:
		return foldStringExpr(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, lok := foldStringExpr(x.X)
		r, rok := foldStringExpr(x.Y)
		if !lok || !rok {
			return "", false
		}
		return l + r, true
	case *ast.CallExpr:
		sel, ok := x.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Sprintf" {
			return "", false
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "fmt" {
			return "", false
		}
		if len(x.Args) == 0 || x.Ellipsis.IsValid() {
			return "", false
		}
		format, ok := foldStringExpr(x.Args[0])
		if !ok {
			return "", false
		}
		args := make([]any, 0, len(x.Args)-1)
		for _, a := range x.Args[1:] {
			v, ok := foldStringExpr(a)
			if !ok {
				return "", false
			}
			args = append(args, v)
		}
		out := fmt.Sprintf(format, args...)
		// A wrong verb count renders %!s(MISSING) or %!(EXTRA ...), which
		// cannot match providerTypeLiteral anyway; returning it is harmless
		// and avoids pretending the fold failed.
		return out, true
	default:
		return "", false
	}
}

// TestGeneratedTypeLiteralExemptionIsExact pins the escape hatch.
//
// The registry exempts any file whose header says it is generated. That is
// the right exemption and it is also the cheapest way around this guard, so
// the exempt set is enumerated rather than trusted to a rule.
func TestGeneratedTypeLiteralExemptionIsExact(t *testing.T) {
	root := repoRoot(t)
	want := map[string]bool{}
	for _, f := range generatedTypeLiteralFiles {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("generatedTypeLiteralFiles names %s, which does not exist: %v\n"+
				"If a generator stopped emitting it, drop the entry; the pin has to describe the tree.", f, err)
			continue
		}
		want[f] = true
	}

	got := map[string]bool{}
	for _, r := range typeLiteralRoots() {
		walkGo(t, root, r, func(rel, abs string) {
			if !isGeneratedGoFile(abs) || !fileHasProviderTypeLiteral(abs) {
				return
			}
			got[rel] = true
		})
	}

	for f := range got {
		if !want[f] {
			t.Errorf("%s carries a \"Code generated ... DO NOT EDIT\" header and spells out provider type names, and generatedTypeLiteralFiles does not list it.\n"+
				"The generated exemption is how hand-wiring would hide from typeLiteralSurfaces. If this really is a generator's output, add it here and name the generator.", f)
		}
	}
	for f := range want {
		if !got[f] {
			t.Errorf("generatedTypeLiteralFiles names %s, but it no longer reads as generated or no longer holds a provider type literal.\n"+
				"A pin describing nothing passes forever.", f)
		}
	}
}

// walkTypeLiterals produces the per-file census and the number of Go files
// visited. Hand-written files only: generated output and _test.go fixtures
// are not the subject.
func walkTypeLiterals(t *testing.T) (map[string]typeLiteralCensus, int) {
	t.Helper()
	out := map[string]typeLiteralCensus{}
	files := 0

	forEachHandWrittenGoFile(t, func(rel string, f *ast.File, fset *token.FileSet) {
		files++
		c := out[rel]
		parents := parentMap(f)
		ast.Inspect(f, func(n ast.Node) bool {
			bl, ok := n.(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				return true
			}
			v, err := strconv.Unquote(bl.Value)
			if err != nil || !providerTypeLiteral.MatchString(v) {
				return true
			}
			if inFunctionBody(parents, bl) {
				c.Code++
			} else {
				c.Data++
			}
			if c.Sample == "" {
				c.Sample = v
				c.Line = fset.Position(bl.Pos()).Line
			}
			return true
		})
		if c.Data+c.Code > 0 {
			out[rel] = c
		}
	})
	return out, files
}

// typeLiteralRoots is the same "what this fork owns" list ci_coverage_test.go
// walks for test packages. Sharing it is the point: a root added to CI is a
// root this guard covers, with no second list to forget.
func typeLiteralRoots() []string {
	return append(append([]string{}, forkOwnedRoots...), forkOwnedMixedRoots...)
}

// forEachHandWrittenGoFile parses every non-test, non-generated Go file under
// the fork-owned roots. testdata trees are skipped for ci_coverage_test.go's
// reason: their .go files are fixtures the toolchain never builds.
func forEachHandWrittenGoFile(t *testing.T, fn func(rel string, f *ast.File, fset *token.FileSet)) {
	t.Helper()
	root := repoRoot(t)
	for _, r := range typeLiteralRoots() {
		walkGo(t, root, r, func(rel, abs string) {
			if strings.HasSuffix(rel, "_test.go") || isGeneratedGoFile(abs) {
				return
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Errorf("parsing %s: %v", rel, err)
				return
			}
			fn(rel, f, fset)
		})
	}
}

// walkGo calls fn for every .go file under repoRoot/relRoot, with rel spelled
// relative to the repository root.
func walkGo(t *testing.T, repoRoot, relRoot string, fn func(rel, abs string)) {
	t.Helper()
	dir := filepath.Join(repoRoot, relRoot)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("fork-owned root %s does not exist; the list this guard shares with ci_coverage_test.go is stale", relRoot)
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		fn(filepath.ToSlash(rel), path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", relRoot, err)
	}
}

// isGeneratedGoFile reads the conventional header, and only within the first
// 20 lines. The window matters: tools/row-gen/emit.go holds the header text
// as a const at line 87 because it WRITES it, and tools/merge-guard mentions
// it in prose. A whole-file substring match would exempt all three.
func isGeneratedGoFile(abs string) bool {
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	lines := strings.SplitN(string(data), "\n", 21)
	if len(lines) > 20 {
		lines = lines[:20]
	}
	return generatedHeader.MatchString(strings.Join(lines, "\n"))
}

func fileHasProviderTypeLiteral(abs string) bool {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		bl, ok := n.(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		if v, err := strconv.Unquote(bl.Value); err == nil && providerTypeLiteral.MatchString(v) {
			found = true
		}
		return true
	})
	return found
}

// parentMap records each node's parent so a literal's syntactic context can
// be walked upwards.
func parentMap(f *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

// inFunctionBody reports whether a node sits inside a FuncDecl or FuncLit -
// the decision positions. A package-level composite literal is a hand table;
// the same literal inside a closure is a lookup, a branch or a comparison,
// and that is the distinction the standing rule turns on.
func inFunctionBody(parents map[ast.Node]ast.Node, n ast.Node) bool {
	cur := n
	for {
		p, ok := parents[cur]
		if !ok {
			return false
		}
		switch p.(type) {
		case *ast.FuncDecl, *ast.FuncLit:
			return true
		}
		cur = p
	}
}

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

func sortedCensusKeys(m map[string]typeLiteralCensus) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedSurfaceKeys(m map[string]typeLiteralSurface) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
