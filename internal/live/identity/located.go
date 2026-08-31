// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file answers issue #270's question at the layer that classifies:
// which of [MarkerlessTypes] may resolve [ClassRecordLocated], given the
// provider schemas this run actually holds.
//
// The distinction the whole issue turns on, restated here because this is
// the file that acts on it: a marker answers "may I delete this", and an
// identity answers "which object is this". [MarkerlessTypes] is a fact
// about the first - the provider mints the ID and the type has no tags
// argument, so there is nowhere to write a marker. It is not a fact about
// the second. For an object choudoufu itself created, the ID is known at
// the moment of creation, and a per-estate record can hold it. That is only
// true on a platform that has migrated, which is why every predicate here
// is gated on a record_store being declared: the store is the migration
// step, and nothing below admits a type without one.

// locatedImportIDAttr is the schema attribute a located identity is read
// out of after an apply, and therefore the attribute a type must have
// before this package will admit it as located.
//
// It is "id" because that is the attribute OpenTofu's own import path
// round-trips: ImportResourceState is given a string and returns an object
// whose "id" is that string, so a type carrying a top-level string "id" is
// exactly a type that can be re-found from one. A type without one may
// still be importable by an identity OBJECT; that is what
// [LocatedIdentityPlanFor] answers, and a located record now carries the
// object when the provider's own identity schema says the string is not the
// whole identity. Measured against hashicorp/aws 6.59.0, thirteen of the 145
// markerless types carry no top-level string id, of which eleven reach this
// condition (the other two are credential material and are refused before
// it). TestLocatedTypePopulation records the counts so they cannot drift
// silently.
//
// It is a schema fact, not a type list: no resource type is named here or
// anywhere else this predicate reads.
const locatedImportIDAttr = "id"

// recordStoreConfiguredIn reports whether cfg's root module declares a live
// block with a record_store.
//
// The same fact internal/live/lint reads under the same name, from the same
// field, for the same decision. It is duplicated rather than shared because
// the dependency would run the wrong way - lint already imports this
// package - and it is three lines over a field that cannot be read two ways.
// What must not drift is the ANSWER, and
// TestLocatedAdmissionAgreesWithLint is what holds the two together.
func recordStoreConfiguredIn(cfg *configs.Config) bool {
	if cfg == nil || cfg.Module == nil || cfg.Module.Live == nil {
		return false
	}
	return cfg.Module.Live.RecordStore != nil
}

// LocatedType reports whether resourceType may be admitted as
// record-located: the object exists in the cloud, carries no marker, and
// its identity can be recorded in and recovered from the estate's record
// store.
//
// Four conditions. Three are read from the provider's own schema; the
// fourth is the one question a schema cannot answer:
//
//  0. The type is not vetoed by [NotImportable]. A located record holds an
//     identity so that a LATER run can import the object back - so the
//     located mechanism is an importing mechanism, and a type the provider
//     will not import is one it cannot serve. Left out, this route admitted
//     the type, the first apply created the object and wrote its record,
//     and every plan after that failed on internal/live/projection's
//     importAndRead with "resource ... doesn't support import" - a plan
//     refusal traded for an apply refusal, with the object already live.
//     Issue #331; see [NotImportable] for why the check is one function
//     rather than one lookup per route.
//
//  1. The type is in [MarkerlessTypes] and has no row of its own. A type
//     with a ratified row is already admitted by an ordinary path and must
//     not be re-routed through the store; the two sets are disjoint today
//     (live/admission_coverage_test.go holds the overlap at zero) and this
//     check is what keeps the ordering safe if that ever changes.
//
//  2. The identity this route would RECORD carries no secret. Not "the type
//     has no secret anywhere" - [sensitiveIdentityAttr] asks only about the
//     attributes [LocatedIdentityPlanFor] actually reads, because those are
//     the only ones this route's promise touches. See the check itself,
//     below, for the measurement that replaced a whole-schema veto with
//     this narrower one.
//
//  3. The type has an identity this mechanism can record IN FULL - which is
//     the top-level string [locatedImportIDAttr] for a type whose whole
//     identity is server-minted, and every component of the provider's own
//     identity schema for a type whose identity is composite. Where the
//     provider serves no identity schema and its own documentation describes
//     a composite import string it does not corroborate `id` as, the type is
//     refused instead ([IDNotProvenWholeTypes]). See
//     [LocatedIdentityPlanFor]. Admitting a type whose identity could
//     never be recorded would trade a plan refusal for an apply-time
//     failure, which is the trade this whole mechanism is forbidden to make,
//     and recording only PART of a composite identity is the same trade
//     wearing a disguise: the record would be written, the plan would be
//     clean, and the next run's import would be handed a fragment.
//
// # Failing closed
//
// schemas is what the run holds. A run with no schemas, or with no schema
// for this type, gets false - not "probably fine". That is deliberate and
// it is the only safe direction: conditions 2 and 3 are both readable ONLY
// from a schema, so an absent schema means the credential exclusion cannot
// run, and a predicate that cannot run must not admit. The visible cost is
// that tools/refusal-probe's schema-less mode reports these types refused
// where a real run admits them; the alternative cost would be admitting
// credential material whenever a schema failed to load, which is not a
// trade worth making for a cheaper measurement.
func LocatedType(resourceType string, schemas map[string]providers.Schema) bool {
	if NotImportable(resourceType) {
		// Condition 0, ahead of everything else because it is the one that
		// does not depend on a schema being present: a run with no schemas
		// already fails closed below, and a run WITH them must not be
		// talked into this route by a schema that is entirely correct
		// about an identity the provider will never accept back.
		return false
	}
	if _, ok := MarkerlessTypes[resourceType]; !ok {
		return false
	}
	if _, ok := LookupType(resourceType); ok {
		// A ratified row wins, exactly as lint.markerlessVetoed already
		// arranges for the veto itself. Such a type is admitted by its own
		// path and never becomes located.
		return false
	}
	schema, ok := schemas[resourceType]
	if !ok {
		return false
	}
	return recordableIdentitySchema(resourceType, schema)
}

// recordableIdentitySchema is [LocatedType] and [RecordFallbackType]'s
// shared tail: the three schema-read conditions that decide whether a
// record can hold resourceType's identity in full, once each has settled
// the one condition that differs between them (type absent from the table
// versus type present in it).
//
// Condition 2 asks whether the RECORD would carry a secret, not whether
// the type has one anywhere in its schema - those are different
// questions. [credentialMaterial]'s whole-schema sweep answers the
// second, which is [CredentialMaterial]'s job for internal/live/projection's
// residue (a value-preservation promise this route makes no claim
// about). This route's promise is narrower: it records
// locatedImportIDAttr or plan's own components, nothing else, so the
// only sensitive material it can leak is a secret that IS one of those
// attributes. Measured 2026-08-22 (issue #365 population 2, commit
// 361e0da9ab): of the types [credentialMaterial] excludes, nine of eleven
// carry their secret on an attribute the plan never reads, and refusing
// them here bought nothing - the record it would have written never
// touched the secret either way. [sensitiveIdentityAttr] is that
// narrower question, already written for the operator-selected "markers
// record" route.
//
// It does NOT also refuse on [strictSecretsLocatedExclusion]. That was
// this function's job before the maintainer's 2026-08-23 ruling
// (rulings/20260823-foundation-order-ruling.md, ruling 5): an unconditional
// veto, baked into the schema question itself, so a caller with no way to
// express the operator's `strict { secrets }` setting still refused the
// two named types. Schema admission and the operator's policy are
// different questions - this function answers only the first now - and
// [LocatedStrictSecretsRefusal] is where the second is asked, by the
// three callers that need to ask it.
func recordableIdentitySchema(resourceType string, schema providers.Schema) bool {
	if schema.Block == nil {
		return false
	}
	plan, recordable := LocatedIdentityPlanFor(resourceType, schema)
	if !recordable {
		return false
	}
	if sensitiveIdentityAttr(plan, schema) != "" {
		return false
	}
	return true
}

// RecordableIdentitySchema is [recordableIdentitySchema] exported for GitHub
// issue #364 unit A2's writers: [internal/live/projection.LocatedRecordFrom]
// folds this in so every one of its callers - not only [LocatedType] and
// [RecordFallbackType], which gated it before a stamped, ordinary taggable
// instance ever reached that function - refuses to derive an identity a
// record must never carry, because it is either not fully recordable
// ([LocatedIdentityPlanFor]'s own refusal) or would carry a secret
// ([sensitiveIdentityAttr]). Named identically to the unexported function it
// wraps so the two cannot answer differently by drifting apart; a single
// call site keeps that true by construction.
func RecordableIdentitySchema(resourceType string, schema providers.Schema) bool {
	return recordableIdentitySchema(resourceType, schema)
}

// RecordFallbackType reports whether resourceType may use the record store
// as an INSTANCE-level identity fallback when the type's own admission row
// could not resolve one of its identity components from configuration.
//
// It is [LocatedType] answered through the opposite door. LocatedType
// serves a type [MarkerlessTypes] refused a table row to in the first
// place, because the whole type is server-minted AND untaggable
// ([MarkerlessReason]). This serves a type WITH a ratified row - an
// ordinary component-based identity path exists and is used for every
// instance whose configuration states it - whose schema is untaggable in
// exactly the same sense [MarkerlessReason] measures (no settable
// top-level tags map), so the one instance in front of it that could not
// fold its identity from configuration this run (a name_prefix sibling, an
// omitted server-assigned name, a cloud property this run was not given)
// has no marker to fall back on either. aws_autoscaling_group is the type
// that exposed the gap: its row resolves every instance that states `name`
// literally, and [MarkerlessTypes]'s generator never considered it,
// because the rule it applies is about types that are server-assigned at
// the WHOLE-TYPE level, which aws_autoscaling_group is not - most of its
// instances resolve from configuration alone.
//
// live/survey-full.json's taggable signal is a looser measurement than
// [markers.Taggable] (the schema check this function actually applies), so
// a candidate list built from the survey alone overstates the reach: of
// the four other ratified rows whose identity argument follows the
// name/name_prefix or server-assigned-if-absent convention and whose
// survey entry reads taggable=false (aws_iam_role_policy, aws_kms_alias,
// aws_launch_configuration, aws_scheduler_schedule), measuring against a
// real hashicorp/aws 6.59.0 schema shows only aws_launch_configuration
// sharing aws_autoscaling_group's shape; the other three carry a real
// settable top-level tags map and take the ordinary marker-fallback path
// instead. This function's own schema check is the source of truth here,
// not the count in this comment - re-measure with
// [pluginschema.ResourceTypes] before quoting a number.
//
// Every other condition is [LocatedType]'s own - importable, and the
// identity fully recordable with nothing sensitive in it - because the
// promise this route makes ("the record can be read back as a whole,
// correct import identity") does not change depending on which door let
// the type in. [resolver.recordFallback] is the sole caller: it never
// runs ahead of an instance's own component resolution, only after that
// resolution has already concluded it needs discovery, so a configuration
// that states `name` literally never reaches this predicate at all.
func RecordFallbackType(resourceType string, schemas map[string]providers.Schema) bool {
	if NotImportable(resourceType) {
		return false
	}
	if _, ok := LookupType(resourceType); !ok {
		// No table row: this is [LocatedType]'s population, not this
		// route's. The two stay disjoint the same way [MarkerlessTypes] and
		// the table itself do (live/admission_coverage_test.go), and this
		// condition is what holds that disjointness from the other
		// direction.
		return false
	}
	schema, ok := schemas[resourceType]
	if !ok || schema.Block == nil {
		return false
	}
	if markers.Taggable(schema.Block) {
		// Has somewhere to carry a marker. The instance in front of
		// [resolver.recordFallback] reached it because ITS OWN
		// configuration could not fold an identity component - not because
		// the type has no marker - so the honest answer is still
		// discovery, the same answer every taggable type in this position
		// has always gotten.
		return false
	}
	return recordableIdentitySchema(resourceType, schema)
}

// strictSecretsLocatedExclusion is the maintainer's 2026-08-23 ruling
// (rulings/20260823-foundation-order-ruling.md, ruling 5), which moved
// aws_iam_access_key and aws_iot_certificate out of the unconditional,
// pre-compatible-by-default veto this file carried before it (see git
// history for the retired sanctionedCredentialExclusion, and
// live/HARNESS.md's now-two-entry "credential-exclusions" ratchet in
// internal/live/harness/assumptions.go, which used to name these two among
// four) and onto the same toggle that already governs a RECORD_BACKED
// type's [TypeIdentity.SecretMaterial]: stored by default, the way stock
// stores it; refused under `strict { secrets = "refuse" }`.
//
// It stays a named list rather than becoming a schema-derived rule keyed on
// [credentialMaterial]'s whole-schema sweep, and that is a measured
// decision, not an oversight: issue #365 population 2 (commit 361e0da9ab,
// 2026-08-22) applied exactly that narrowing as a proposal and refuted it by
// reading the real hashicorp/aws provider directly, with no tofu in the
// loop. Nulling a type's sensitive attributes and refreshing shows some of
// them RESTORED by the provider's own Read - aws_cognito_user_pool_client's
// client_secret, aws_appsync_api_key's key, aws_appconfig_hosted_configuration_version's
// content - which is fine to manage under any secrets setting, since the
// live system is the record; and shows others left null forever -
// aws_iam_access_key's secret and ses_smtp_password_v4 among them - which a
// stock state file is the only place that ever held. No schema fact
// distinguishes the two groups: not Sensitive, not Computed-versus-settable,
// not top-level-versus-nested. A generic credentialMaterial gate here would
// therefore also refuse the first group under strict.Refuse, for no reason a
// schema can state - exactly the narrowing that measurement disproved. This
// list is the record of which types the maintainer has actually checked
// against the API; growing it past what ruling 5 names is a ruling to make,
// not a measurement this predicate can take on its own.
//
// aws_iot_certificate is named here because ruling 5 names it, and it is
// unreachable through this route regardless of the secrets setting: condition
// 0, [NotImportable], already refuses it (tools/survey-gen's own probe found
// no classic Importer), which [LocatedType] checks ahead of everything else.
// Nothing about that changes what this map says - the ruling is honored by
// name here exactly as [sanctionedCredentialExclusion] honored it before,
// whether or not the fact happens to be moot for one of the two entries.
var strictSecretsLocatedExclusion = map[string]bool{
	"aws_iam_access_key":  true,
	"aws_iot_certificate": true,
}

// LocatedStrictSecretsRefusal reports why an operator's `strict { secrets =
// "refuse" }` setting refuses resourceType's automatic record-located
// admission even though [LocatedType] says the schema allows it, or "" when
// nothing is refused - the default setting, or a type
// [strictSecretsLocatedExclusion] does not name.
//
// [TypeIdentity.SecretMaterial]'s own doc comment names three places a
// RECORD_BACKED type's version of this same toggle has to be asked:
// internal/live/lint (the gate a configuration meets first),
// [resolver.resolveInstance] (the layer that acts, asked again so a caller
// that skipped lint still gets a refusal rather than a record it did not
// agree to), and internal/live/liveimport's ratifyOne (which runs no lint
// pass and builds no resolver at all).
//
// The record-LOCATED route this function serves is NOT asked at all three
// of those places, and that asymmetry was an audit finding
// (2026-08-24, issue #365) rather than a design choice worth keeping
// silent: this function is called from internal/live/lint's
// checkManagedResources and from [resolver.resolveInstance], but
// liveimport's ratifyOne calls neither this function nor
// [strictSecretsLocatedExclusion] anywhere on the record-LOCATED path
// ([locatedByProviderSchema] asks only [LocatedType]). So `choudoufu
// live-import` can migrate an aws_iam_access_key's record-located identity
// into the record store under `strict { secrets = "refuse" }` today,
// unlike the RECORD_BACKED route, whose ratifyOne branch does ask the
// equivalent question through secretMaterialType. Closing that gap is
// liveimport's to do, not this comment's; it is not a marker-writing
// change and is out of scope for the identifier fix this comment received.
//
// Only called where [LocatedType] has already said the schema allows the
// route; a type LocatedType refuses needs no second reason, and this
// function does not re-derive that answer.
func LocatedStrictSecretsRefusal(resourceType string, secrets strict.Secrets) string {
	if strict.StoresSecrets(secrets) {
		return ""
	}
	if !strictSecretsLocatedExclusion[resourceType] {
		return ""
	}
	return fmt.Sprintf(
		"%q generates secret material - the access key's own secret half, or the certificate's private key - "+
			"that AWS never returns again once the object is created. Its identity (the attribute this route "+
			"records) never carries that secret, but a stock state file is the only place the secret itself has "+
			"ever existed, and this fork otherwise keeps it there too, as residue in the estate's record store. "+
			"This estate's live block sets strict { secrets = %q }, HANDOFF.md's \"no secrets stored by the tool\" "+
			"principle, so the instance is refused rather than admitted through the record-located route. Remove "+
			"that argument to get the default, %q, which manages it the way stock does.",
		resourceType, secrets, strict.DefaultSecrets,
	)
}

// hasLocatedImportID reports whether b carries a top-level string
// [locatedImportIDAttr].
func hasLocatedImportID(b *configschema.Block) bool {
	a, ok := b.Attributes[locatedImportIDAttr]
	return ok && a != nil && a.Type == cty.String
}

// LocatedIdentityPlan is what a located record must carry for one type: which
// of the three shapes its identity takes, and the attributes to read it out
// of the applied object with.
//
// Exactly one shape applies, and the zero value is the third: a plan with no
// Components and no ImportIDParts means the type's whole identity is the
// string [locatedImportIDAttr], which is what every record written before
// composite identities existed looks like.
//
// It is a struct rather than three return values for [LocatedRecord]'s own
// reason: two independently written branches extending the same call by
// position is a merge git resolves silently and wrongly, and a wrongly
// resolved merge here is a wrong identity.
type LocatedIdentityPlan struct {
	// Components is the provider's own identity OBJECT, one attribute per
	// component, UNORDERED - which is what an identity object is. Set only
	// where the provider serves a wire identity schema requiring two or
	// more attributes: [locatedImportIDAttr] plus at least one other
	// (issue #329), or two or more attributes that do not include
	// [locatedImportIDAttr] at all (the 2026-08-24 widening -
	// [compositeIdentity]'s own doc comment has the measurement). Every
	// name here is REQUIRED for import; [LocatedIdentity]'s reading of them
	// is all-or-nothing. Nothing joins these into a string; see
	// [LocatedIdentityPlanFor].
	Components []string

	// OptionalComponents names every attribute the same wire identity
	// schema marks optional for import, alongside Components' required
	// ones - set only when Components is. [LocatedIdentityOptional] reads
	// whichever of them the applied object genuinely carries a value for
	// and silently leaves the rest out: the same "include when present,
	// omit when genuinely absent" rule [Component.OmitIfAbsent] already
	// gives the documented-import-string route, extended to the identity
	// OBJECT route so a type whose optional attribute can disambiguate two
	// otherwise-identical required components - aws_lb_target_group_attachment's
	// port: the same target_group_arn/target_id pair registered at two
	// different ports is two distinct live objects - still records that
	// disambiguator whenever this run can see it, and drops only what the
	// object genuinely does not have, such as a lambda target's port, which
	// AWS never assigns one. Never consulted by [LocatedIdentity]'s
	// all-or-nothing rule: an optional component with no value is not a
	// reason to refuse the record.
	OptionalComponents []string

	// ImportIDParts are the attributes whose values compose the documented
	// import STRING, in the documented order, joined by ImportIDSeparator.
	// Set only where [DocumentedImportIDs] carries a grammar for the type
	// and every segment of it resolves against the schema (issue #337).
	ImportIDParts     []string
	ImportIDSeparator string

	// ImportIDVariadicGroup is set only where the documented grammar's LAST
	// segment resolved to a variadic tail (GitHub issue #384): the ordered
	// sibling attributes [variadicTrailingGroup] read off the type's own
	// identity-table row, each contributing one token per element it
	// carries on the applied object rather than one attribute contributing
	// exactly one token. See [VariadicTrailingImportIDTypes] for which
	// types may reach this and why, and [LocatedComposedImportID] for how
	// it renders.
	ImportIDVariadicGroup []string

	// ImportIDAlternatives, present only alongside ImportIDParts, names -
	// for each index into ImportIDParts - the several candidate attributes
	// that documented segment may resolve to when [namedAlternativeGroup]
	// found the type's own ratified row modelling that SAME position as an
	// ANY-OF over more than one argument (GitHub issue #364's aws_route:
	// its "destination" segment is whichever of destination_cidr_block,
	// destination_ipv6_cidr_block or destination_prefix_list_id the route
	// carries, never a single named argument). A nil entry at index i means
	// ImportIDParts[i] is an ordinary, single-attribute segment;
	// ImportIDParts[i] is itself empty at every OTHER index, a placeholder
	// [LocatedComposedImportID] recognises rather than reads. See
	// [resolveDocumentedImportID]'s "any-of segment" doc section for why
	// this exists instead of the bare-`id` inference every other
	// unresolved segment falls back to, and [resolveAlternativeSegment] for
	// how the single populated candidate is chosen at write time.
	ImportIDAlternatives [][]string

	// Attr is a single attribute, OTHER than [locatedImportIDAttr], whose
	// value is the type's whole identity. Set only where the provider's own
	// wire identity schema says nothing usable (the [!compositeIdentity]
	// branch below applies) and a ratified [TypeIdentity.IdentityAttrs] row
	// names exactly one attribute that is not "id" - GitHub issue #332's
	// aws_default_route_table (IdentityAttrs ["vpc_id"]) is the type that
	// exposed the gap this closes: its Import section documents import by
	// the parent VPC's id, not by the route table's own, and
	// internal/live/discovery already reads the same row's IdentityAttrs[0]
	// for the identical reason (see discovery.go and locatedfallback.go).
	// Before this field existed, every type reaching the default branch was
	// recorded by its bare "id" regardless of what the table already knew,
	// which is a wrong identity rather than a missing one for a type this
	// shaped - see [namedIdentityAttr].
	Attr string
}

// Composite reports whether p carries the provider's identity object.
func (p LocatedIdentityPlan) Composite() bool { return len(p.Components) > 0 }

// Composed reports whether p carries a documented import-string grammar,
// fixed or with a variadic tail.
func (p LocatedIdentityPlan) Composed() bool {
	return len(p.ImportIDParts) > 0 || len(p.ImportIDVariadicGroup) > 0
}

// Named reports whether p carries a single ratified attribute name other
// than [locatedImportIDAttr]. See [LocatedIdentityPlan.Attr].
func (p LocatedIdentityPlan) Named() bool { return p.Attr != "" }

// LocatedIdentityPlanFor answers the question [locatedImportIDAttr]'s own
// doc comment used to answer by assumption: what IS this type's identity,
// and can a located record hold the whole of it?
//
// The plan is the attribute-per-component identity a located record must
// carry, the ordered attributes its documented import string composes from,
// or neither - in which case the type's whole identity is the string
// [locatedImportIDAttr] and a record of that string alone is complete. The
// second return is false when the identity is composite and this mechanism
// cannot record it, which is a refusal rather than a partial record.
//
// # The defect this exists to close
//
// The premise the located mechanism shipped on is that a type carrying a
// top-level string "id" can be re-found from that string. That holds for a
// type whose whole identity is server-minted, and fails for a type whose
// identity is a server-minted LEAF under a config-known parent: the provider
// sets "id" to the bare leaf and the import path expects <parent>/<leaf>.
// Recording the leaf and importing by it later is a wrong identity, not a
// missing one, and a wrong identity is invisible to every verdict-level
// check - the record is written, the apply succeeds, and the failure arrives
// on the NEXT run as an import of a fragment.
//
// # Why the provider's identity schema is the source
//
// It is the only account of a type's identity that is a schema fact rather
// than a scrape, and it names the components without ordering them or
// putting a separator between them - which is exactly what an identity
// OBJECT import needs and an import-ID string does not. So this needs no
// grammar, no separator and no component order, and issue #105's rule that a
// composite must never be flattened into a plausible string is kept by
// construction rather than by care: nothing here builds a string.
// internal/live/projection's identityFromValues turns these components into
// the provider's own identity object, and importTarget already ranks that
// above the string.
//
// # Which types the composite branch reaches
//
// Two populations, both routed here by [compositeIdentity]: a type whose
// identity schema requires "id" alongside a parent (issue #329 -
// aws_default_route_table's own shape, "id" present AND insufficient), and
// a type whose identity schema requires two or more attributes that do NOT
// include "id" at all (aws_lb_target_group_attachment - target_group_arn
// and target_id, neither of which the provider's d.SetId ever wrote to
// "id"). A single REQUIRED, non-"id" attribute is neither: the provider's
// own d.SetId already put that one value into "id", so today's bare-string
// rule already serves it correctly, and reclassifying it here would change
// what a working population records for no defect - see
// [compositeIdentity]'s own doc comment for the len(required)<2 case this
// leaves alone.
//
// Optional attributes ride along as [LocatedIdentityPlan.OptionalComponents],
// read separately by [LocatedIdentityOptional] with its own,
// never-refuses-the-whole-record rule - see that type's doc comment.
//
// # The second source, for the types the wire schema cannot settle
//
// A provider that serves no identity schema for a type leaves the wire with
// nothing to say, and the fallback below - record the string `id` - is then
// a bet rather than a reading. Issue #337 measured that bet: of the
// markerless types whose Import section documents a COMPOSITE string, most
// carry no wire identity schema at all, so nothing at run time could tell a
// leaf `id` from a whole one and this function recorded either.
//
// [IDNotProvenWholeTypes] is what settles it, from the provider's own
// documentation rather than from the wire: the Import section's scraped
// separator and the Attribute Reference's own `id` bullet naming the same
// join character. A type in that set has a composite documented import and
// no corroboration that `id` is the whole of it, and this function refuses
// it - the same refusal, for the same reason, the composite branch below
// makes when a component cannot be read off the applied object. Refusing an
// identity that MIGHT be a fragment and recording one that IS are not
// symmetric errors: the first is visible immediately and the second arrives
// on the next run as an import of half a string.
//
// # The third source, which turns that refusal into an admission
//
// A refusal is honest and it is not a fix. Where the same page that documents
// the composite import goes on to NAME its segments one token at a time,
// [DocumentedImportIDs] carries that grammar and
// [resolveDocumentedImportID] resolves it against this very schema. The
// record then holds the whole documented string, composed from the applied
// object's own attributes in the documented order - issue #337.
//
// That route is consulted ONLY inside the refusal it replaces, which is what
// makes it safe to add: it cannot reach a type the bare-`id` rule admits, so
// nothing that resolves today can be made to stop or to resolve differently.
// Its own refusals leave [IDNotProvenWholeTypes]' refusal exactly where it
// was.
//
// # Which source wins when two of them speak
//
// The wire identity schema, always, and it is checked first. The generator
// excludes a type with a wire identity schema from [DocumentedImportIDs]'
// population, so the two normally cannot both apply; where a provider release
// adds an identity schema the pinned scrape has not seen, the schema the
// RUNNING provider serves is the authority and the scrape is the stale
// account. Ordering the branches this way means version skew resolves towards
// the wire rather than towards a documentation snapshot.
//
// It names no resource type, here or anywhere it reads.
func LocatedIdentityPlanFor(resourceType string, schema providers.Schema) (plan LocatedIdentityPlan, recordable bool) {
	if schema.Block == nil {
		return LocatedIdentityPlan{}, false
	}
	required, optional := identityAttrs(schema.IdentitySchema)
	if !compositeIdentity(required) {
		// Either the provider serves no identity schema at all, or the one
		// it serves is answered by the string this mechanism already
		// records - so the string is all there is to go on, and the
		// question becomes whether the documentation says it is enough.
		if _, unproven := IDNotProvenWholeTypes[resourceType]; unproven {
			if parts, variadicGroup, alternatives, sep, ok := resolveDocumentedImportID(resourceType, schema.Block); ok {
				return LocatedIdentityPlan{ImportIDParts: parts, ImportIDVariadicGroup: variadicGroup, ImportIDAlternatives: alternatives, ImportIDSeparator: sep}, true
			}
			return LocatedIdentityPlan{}, false
		}
		if attr, ok := namedIdentityAttr(resourceType, schema.Block); ok {
			return LocatedIdentityPlan{Attr: attr}, true
		}
		return LocatedIdentityPlan{}, hasLocatedImportID(schema.Block)
	}
	for _, name := range required {
		a := schema.Block.Attributes[name]
		if a == nil || a.Type != cty.String {
			// A component the applied object does not carry as a top-level
			// string cannot be read back out of it, so the record would be
			// incomplete. Refusing is the whole point of this function.
			return LocatedIdentityPlan{}, false
		}
	}
	// optional rides along verbatim, with no per-attribute check here: an
	// optional component that turns out not to be a plain string or number
	// on the resource's own block, or not present on obj at all, is exactly
	// what [locatedAttrSegment] - [LocatedIdentityOptional]'s own reader -
	// already answers false for, and answering false for one optional
	// component is never a reason to refuse the whole identity the way it
	// is for a required one. Filtering here would only duplicate that
	// check, at the cost of a second place it could drift from it.
	return LocatedIdentityPlan{Components: required, OptionalComponents: optional}, true
}

// namedIdentityAttr reports the single, non-"id" attribute a ratified
// identity-table row names as resourceType's whole identity, for
// [LocatedIdentityPlanFor]'s default branch: the wire identity schema said
// nothing usable (that branch's own precondition), so the only remaining
// source that is not a guess is the same [TypeIdentity.IdentityAttrs] row
// internal/live/discovery's own identical questions already read
// (discovery.go's importIdentityAttr-shaped helpers, locatedfallback.go's
// identityAttr) - asked here again rather than duplicated, because a type
// admitted through either the discovery-sweep route or this
// record-writing route must resolve to the SAME string on both, or the
// record and a rediscovered marker would name the object two different
// ways.
//
// false, meaning "no better answer than the bare id default": there is no
// ratified row, the row is silent, the row names more than one attribute
// (a genuine composite - the branch above this one's caller owns that
// shape, not this one), the row names "id" itself (restating the default
// is not overriding it), or the schema this specific provider release
// serves does not carry that attribute as a plain top-level string (nothing
// to read it out of, so the bare "id" guess is the safer of the two
// available).
func namedIdentityAttr(resourceType string, b *configschema.Block) (string, bool) {
	ti, ok := LookupType(resourceType)
	if !ok || len(ti.IdentityAttrs) != 1 {
		return "", false
	}
	name := ti.IdentityAttrs[0]
	if name == locatedImportIDAttr {
		return "", false
	}
	a := b.Attributes[name]
	if a == nil || a.Type != cty.String {
		return "", false
	}
	return name, true
}

// compositeIdentity reports whether required - a type's required identity
// attributes - describes an identity that [locatedImportIDAttr] alone
// cannot be trusted to carry: two or more attributes the provider's own
// wire identity schema requires to name the object.
//
// It no longer also requires "id" to be one of them. That was this
// function's whole rule until the 2026-08-24 measurement of
// aws_lb_target_group_attachment (corpus-alb-complete's remaining
// test_plan wall, a lambda-target attachment whose port argument is
// genuinely null): the type's real hashicorp/aws 6.59.0 wire identity
// schema requires target_group_arn and target_id, neither of which is
// "id", and the old rule routed it past this branch entirely into the
// weaker documented-import-string fallback below, which has no grammar
// for this type and refused it outright - not because the identity could
// not be built, but because this predicate never let the branch that
// could build it run.
//
// The single-required-attribute case is unchanged: len(required) < 2 still
// answers false unconditionally, because there the provider's own d.SetId
// already put that one value into "id" and nothing about a second,
// unrelated attribute changes that (see [LocatedIdentityPlanFor]'s "identity
// schema requiring something other than id" case). What widens is only the
// len>=2 case, where no single string - "id" included - can already hold
// two independently-required values, so reading the identity OBJECT
// component by component is the only account of it that is not a bet.
//
// Measured against live/survey-full.json (hashicorp/aws 6.59.0): 80 types
// carry a wire identity schema whose required set has two or more members
// and does not include "id" - aws_lb_target_group_attachment among them -
// none of which this branch could ever admit before this change. Every one
// of them still goes through the identical per-attribute string check in
// [LocatedIdentityPlanFor] that already refuses a component the resource's
// own block cannot supply as a plain top-level string, so nothing here
// admits a type whose identity cannot actually be read off the applied
// object.
func compositeIdentity(required []string) bool {
	return len(required) >= 2
}

// credentialMaterial reports whether b describes a resource that holds
// secret material: any attribute the provider marks Sensitive and does not
// also mark Deprecated, anywhere in the block - nested attribute object
// types and nested blocks included.
//
// This is the run-time form of the exclusion CLAUDE.md states as a roster
// of four type names. Deriving it instead of listing it is a maintainer
// ruling (2026-08-17) and it is the better rule for the ordinary reason: a
// roster covers the four types someone thought of, and this covers every
// type whose schema says the same thing, including the ones a future
// provider release adds.
//
// The rule is copied in SUBSTANCE, not in code, from
// tools/row-gen's liveSensitiveAttrs - the evidence behind
// lint.ClassSecretRefused - and the deprecation subtraction comes with it
// for that rule's own reason: a deprecated attribute is one the provider
// tells you not to use, and hashicorp/local deprecated
// local_file.sensitive_content precisely in order to move the sensitive
// case out into a different type, so counting it would classify a type by
// an attribute whose whole purpose is to no longer be its.
// CredentialMaterial is [credentialMaterial] for callers outside this
// package: internal/live/projection's residue classifier (issue #275) asks
// the identical question of the identical schema before it will consider
// recording any of a type's argument values.
//
// It is a wrapper and not a second implementation on purpose. The exclusion
// is the ONE sanctioned refusal in this fork, and the way it stops being
// one rule is by being written down twice with a small difference between
// the copies.
func CredentialMaterial(b *configschema.Block) bool {
	return credentialMaterial(b)
}

func credentialMaterial(b *configschema.Block) bool {
	found := false
	walkSchemaAttrs(b, func(a *configschema.Attribute) {
		if a.Sensitive && !a.Deprecated {
			found = true
		}
	})
	return found
}

// walkSchemaAttrs visits every attribute reachable from b, including those
// inside nested attribute object types and nested blocks.
func walkSchemaAttrs(b *configschema.Block, visit func(*configschema.Attribute)) {
	if b == nil {
		return
	}
	for _, a := range b.Attributes {
		if a == nil {
			continue
		}
		visit(a)
		walkSchemaObjectAttrs(a.NestedType, visit)
	}
	for _, nested := range b.BlockTypes {
		if nested == nil {
			continue
		}
		walkSchemaAttrs(&nested.Block, visit)
	}
}

// walkSchemaObjectAttrs is [walkSchemaAttrs] for a nested attribute type.
func walkSchemaObjectAttrs(o *configschema.Object, visit func(*configschema.Attribute)) {
	if o == nil {
		return
	}
	for _, a := range o.Attributes {
		if a == nil {
			continue
		}
		visit(a)
		walkSchemaObjectAttrs(a.NestedType, visit)
	}
}

// LocatedImportID reads the identity a located instance's applied object
// carries, for writing back to the store.
//
// It is the inverse of the import that [LocatedType]'s third condition
// describes, and it reads the same attribute, so a type this returns false
// for is a type [LocatedType] already refused - there is no shape where a
// located instance applies and then has no identity to record.
//
// The second return is false when the object has no such attribute, when it
// is null, or when it is not yet known, which is what a value read from a
// plan rather than from a finished apply looks like.
func LocatedImportID(obj cty.Value) (string, bool) {
	return locatedAttrString(obj, locatedImportIDAttr)
}

// LocatedNamedAttr reads [LocatedIdentityPlan.Attr]'s value off an applied
// located object, the way [LocatedImportID] reads [locatedImportIDAttr] -
// same nullability and unknown-value rules, different attribute name.
func LocatedNamedAttr(obj cty.Value, name string) (string, bool) {
	return locatedAttrString(obj, name)
}

// LocatedIdentity reads the whole identity of an applied record-located
// object, component by component, for writing back to the store.
//
// components is [LocatedIdentityPlan.Components]. A nil or empty
// components yields a nil map and ok == true: the type's identity is the
// string [LocatedImportID] reads and there is no object to record, which is
// the answer for every type the located mechanism admitted before composite
// identities existed.
//
// It is all-or-nothing on purpose. A component that is absent, null, unknown
// or marked makes the whole identity unrecordable, because a record carrying
// SOME of a composite identity is worse than no record at all: no record
// proposes a create, which internal/live/foreign then surfaces as an
// unclaimed live object, whereas a partial one is handed to a later import
// as though it were complete.
func LocatedIdentity(obj cty.Value, components []string) (map[string]string, bool) {
	if len(components) == 0 {
		return nil, true
	}
	out := make(map[string]string, len(components))
	for _, name := range components {
		v, ok := locatedAttrString(obj, name)
		if !ok {
			return nil, false
		}
		out[name] = v
	}
	return out, true
}

// LocatedIdentityOptional reads [LocatedIdentityPlan.OptionalComponents]'
// values off an applied located object, for [LocatedRecordFrom]'s
// Composite() branch: the same per-attribute guards [locatedAttrSegment]
// already gives a documented import-string segment - a plain string, or a
// number rendered to the same decimal form [renderIntegralNumber] uses for
// the composed-string route (aws_lb_target_group_attachment's port is a
// number in the resource's own block, same as
// aws_security_group_rule's from_port/to_port); null, unknown, marked or
// empty all excluded - applied here to the wire identity object's own
// optional attributes instead of a documented string's segments.
//
// Unlike [LocatedIdentity], this never refuses the whole identity. An
// optional component this instance genuinely has no value for - a lambda
// target's port, which AWS never assigns one - is left out of the returned
// map rather than treated as a reason to withhold the record, because
// [Component.OmitIfAbsent] already establishes that "the provider's own
// grammar marks this segment optional" and "this instance has nothing
// here" are the same fact, not a different one from "unresolvable." A nil
// or empty optional yields a nil map, never an error.
func LocatedIdentityOptional(obj cty.Value, optional []string) map[string]string {
	if len(optional) == 0 {
		return nil
	}
	var out map[string]string
	for _, name := range optional {
		v, ok := locatedAttrSegment(obj, name)
		if !ok {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(optional))
		}
		out[name] = v
	}
	return out
}

// locatedAttrString reads one top-level string attribute off an applied
// object, under the guards [LocatedImportID] documents: absent, null,
// unknown, wrongly typed, marked and empty all answer false.
//
// The marked case is the one worth reading twice. A marked value is refused,
// never unmarked. cty panics rather than errors on a marked receiver, so
// this guard is what keeps AsString below safe (internal/live/marksafe) -
// but the reason it REFUSES is the stronger one: an identity derived from a
// sensitive value would be written into the estate's record store in clear,
// which is the no-secrets rule. Refusing produces the "no usable identity to
// record" error, which stops the run.
func locatedAttrString(obj cty.Value, name string) (string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return "", false
	}
	if !obj.Type().HasAttribute(name) {
		return "", false
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", false
	}
	if v.IsMarked() {
		// See this function's doc comment: refused, never unmarked.
		return "", false
	}
	s := v.AsString()
	if s == "" {
		return "", false
	}
	return s, true
}

// locatedAttrSegment reads one top-level DOCUMENTED IMPORT-STRING SEGMENT
// off an applied object - [locatedAttrString]'s guards for a string
// attribute, or a number attribute rendered into the plain decimal form the
// provider's own import strings use, for [LocatedComposedImportID]. It is
// not used by the wire-identity Components branch ([LocatedIdentity]),
// which is a different mechanism the provider's own identity schema already
// requires to be a top-level string.
//
// [attrsByDocName] is what let a number attribute reach here in the first
// place - see its doc comment for why aws_security_group_rule's
// from_port/to_port needed it. This is the write-back half: the segment
// resolved by NAME there is rendered by VALUE here, and a number that
// cannot be rendered with confidence is refused rather than guessed at,
// same as every other refusal in this file.
func locatedAttrSegment(obj cty.Value, name string) (string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return "", false
	}
	if !obj.Type().HasAttribute(name) {
		return "", false
	}
	v := obj.GetAttr(name)
	if v.IsNull() || !v.IsKnown() {
		return "", false
	}
	if v.IsMarked() {
		// See locatedAttrString's doc comment: refused, never unmarked.
		return "", false
	}
	switch v.Type() {
	case cty.String:
		s := v.AsString()
		if s == "" {
			return "", false
		}
		return s, true
	case cty.Number:
		return renderIntegralNumber(v)
	default:
		return "", false
	}
}

// renderIntegralNumber renders v - a known, unmarked cty.Number - into the
// plain decimal digits [cty/convert]'s own Number-to-String conversion
// produces (big.Float.Text('f', -1), the same conversion an implicit
// "${...}" string interpolation of a number applies): no exponent and no
// trailing ".0" - "443", never "443.0" or "4.43e2".
//
// That form is confirmed against the provider's own documentation, not
// inferred: hashicorp/aws's security_group_rule.html.markdown Import
// section shows import IDs like
// "sg-6e616f6d69_ingress_tcp_8000_8000_10.0.3.0/24" and
// "..._92_0_65536_10.0.3.0/24_10.0.4.0/24" - plain decimal port and
// protocol numbers, including a bare "0", never a decimal point.
//
// ok is false when v is not integral. A [DocumentedImportIDPart] segment
// names an identifier or a port, and every real schema this route reaches
// today carries those as whole numbers; a fractional value is a shape
// nothing here has verified the provider ever echoes back through an
// import round-trip, so rendering it would be a guess, not a reading -
// exactly the risk HANDOFF's safety rule forbids. There is no separate
// "out of range" refusal to make: the rendering never narrows through a
// fixed-width integer type, so there is no width for a value to overflow.
// [(*big.Float).IsInt] is the sole gate, and it is also what keeps a
// negative zero - which AWS never emits and Text would otherwise render as
// "-0" - from reaching the caller as anything but "0".
func renderIntegralNumber(v cty.Value) (string, bool) {
	if v.IsMarked() {
		// [locatedAttrSegment] already checks this before calling here, but
		// internal/live/marksafe proves each mark-unsafe call site within
		// its OWN function - nothing crosses a function boundary - so the
		// guard is repeated rather than trusted from the caller.
		return "", false
	}
	f := v.AsBigFloat()
	if !f.IsInt() {
		return "", false
	}
	if f.Sign() == 0 {
		return "0", true
	}
	return f.Text('f', -1), true
}
