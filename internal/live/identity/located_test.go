// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// locatedSchema builds a schema for a type that would be admitted as
// located: a string "id" and nothing sensitive.
func locatedSchema(extra map[string]*configschema.Attribute) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
}

// aMarkerlessType returns one type from [MarkerlessTypes] that no ratified
// row covers, chosen deterministically so a failure names the same type
// twice running. Fails the test if the population is empty, since every
// assertion below would then be vacuous.
//
// It also skips [IDNotProvenWholeTypes]. Every caller below asserts that
// SOME condition other than the doc-derived one decides the verdict - the
// credential rule, the string id, a composite the block cannot carry - and a
// subject the doc rule already refuses would make all of them pass while
// measuring the wrong refusal. That is the "test measuring itself" shape,
// and skipping here is what keeps those assertions about their own
// conditions. #309's widening made this live rather than theoretical: 52 of
// the 158 markerless types are now in that set.
func aMarkerlessType(t *testing.T) string {
	t.Helper()
	names := make([]string, 0, len(MarkerlessTypes))
	for name := range MarkerlessTypes {
		if _, ratified := LookupType(name); ratified {
			continue
		}
		if _, unproven := IDNotProvenWholeTypes[name]; unproven {
			continue
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("MarkerlessTypes is empty of unratified types, so every located assertion below would pass vacuously")
	}
	sort.Strings(names)
	return names[0]
}

// TestLocatedTypeRequiresAllThreeConditions walks [LocatedType]'s three
// conditions one at a time, each failure in isolation, so that a rule which
// silently stopped checking one of them is caught rather than masked by the
// other two.
func TestLocatedTypeRequiresAllThreeConditions(t *testing.T) {
	markerless := aMarkerlessType(t)

	t.Run("admitted when all three hold", func(t *testing.T) {
		if !LocatedType(markerless, map[string]providers.Schema{markerless: locatedSchema(nil)}) {
			t.Fatalf("LocatedType(%q) = false with a markerless type, a clean schema and a string id; the located population is empty and every other case here proves nothing", markerless)
		}
	})

	t.Run("a type outside MarkerlessTypes is never located", func(t *testing.T) {
		// A type with nothing to do with the veto. Its object may well be
		// findable by other means; the located path is not for it.
		const other = "not_a_real_provider_type"
		if _, vetoed := MarkerlessTypes[other]; vetoed {
			t.Fatalf("%q is unexpectedly in MarkerlessTypes; pick another name for this case", other)
		}
		if LocatedType(other, map[string]providers.Schema{other: locatedSchema(nil)}) {
			t.Errorf("LocatedType(%q) = true for a type outside MarkerlessTypes. The located path exists for types with nowhere to carry a marker, and widening it to every type would route resources with a working marker through a record instead.", other)
		}
	})

	t.Run("a ratified row wins", func(t *testing.T) {
		// A ratified type is admitted by its own path. Even if it were
		// somehow also in MarkerlessTypes, the row ships (row-gen's
		// emit.go copies every field verbatim) and re-routing it through
		// the store would silently change what a ratification signed off
		// on.
		var ratified string
		for name := range MarkerlessTypes {
			if _, ok := LookupType(name); ok {
				ratified = name
				break
			}
		}
		if ratified == "" {
			// The two sets are disjoint today, which is the state
			// live/admission_coverage_test.go holds. Assert the ordering
			// on a plain ratified type instead: it must not be located
			// even with a clean schema.
			for name := range DefaultTable {
				ratified = name
				break
			}
			if ratified == "" {
				t.Skip("DefaultTable is empty")
			}
		}
		if LocatedType(ratified, map[string]providers.Schema{ratified: locatedSchema(nil)}) {
			t.Errorf("LocatedType(%q) = true for a type with a ratified row", ratified)
		}
	})

	t.Run("a sensitive id is refused", func(t *testing.T) {
		// The identity this route would RECORD is sensitive - id itself.
		// This is the one shape condition 2 exists to catch: the record
		// would hold the secret.
		schema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true, Sensitive: true},
		}}}
		if LocatedType(markerless, map[string]providers.Schema{markerless: schema}) {
			t.Errorf("LocatedType(%q) = true for a schema whose id itself is sensitive", markerless)
		}
	})

	t.Run("a sensitive attribute outside the recorded identity does not refuse", func(t *testing.T) {
		// #365 population 2, measured 2026-08-22: condition 2 asks whether
		// the RECORD would carry a secret, not whether the type has one
		// anywhere. A "secret" attribute the plan never reads (id is the
		// whole identity here, and "secret" isn't it) must not refuse -
		// refusing bought nothing, since the record this route would write
		// never touches "secret" either way. This is the case the old
		// whole-schema veto got wrong.
		schema := locatedSchema(map[string]*configschema.Attribute{
			"secret": {Type: cty.String, Computed: true, Sensitive: true},
		})
		if !LocatedType(markerless, map[string]providers.Schema{markerless: schema}) {
			t.Errorf("LocatedType(%q) = false for a schema carrying a sensitive attribute OUTSIDE its recorded identity (id); the record's promise is about id, not about every attribute in the block", markerless)
		}
	})

	t.Run("a type with no string id is refused", func(t *testing.T) {
		noID := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"arn": {Type: cty.String, Computed: true},
		}}}
		if LocatedType(markerless, map[string]providers.Schema{markerless: noID}) {
			t.Errorf("LocatedType(%q) = true for a schema with no string id. Admitting such a type would let it plan and apply and then have no identity to record, which trades a plan refusal for a silent duplicate on the next run.", markerless)
		}
		wrongType := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.Number, Computed: true},
		}}}
		if LocatedType(markerless, map[string]providers.Schema{markerless: wrongType}) {
			t.Errorf("LocatedType(%q) = true for an id that is not a string", markerless)
		}
	})
}

// TestLocatedTypeFailsClosedWithoutSchemas is the safety property the
// maintainer ruling attaches to deriving the credential exclusion at run
// time instead of listing it.
//
// Two of [LocatedType]'s three conditions are readable ONLY from a schema.
// A predicate that cannot run must not admit, or the day a provider's
// schema fails to load is the day credential material is admitted through
// the back door. The visible cost - tools/refusal-probe's default,
// schema-less mode reports these types refused where a real run admits them
// - is stated on [LocatedType] and is the cheaper of the two.
func TestLocatedTypeFailsClosedWithoutSchemas(t *testing.T) {
	markerless := aMarkerlessType(t)

	for name, schemas := range map[string]map[string]providers.Schema{
		"nil schemas":               nil,
		"empty schemas":             {},
		"schemas for another type":  {"aws_vpc": locatedSchema(nil)},
		"an entry with no block":    {markerless: {}},
		"an entry with a nil block": {markerless: {Block: nil}},
		"an empty block, no id":     {markerless: {Block: &configschema.Block{}}},
	} {
		if LocatedType(markerless, schemas) {
			t.Errorf("LocatedType(%q) with %s = true. The credential exclusion cannot run without a schema, and a predicate that cannot run must refuse.", markerless, name)
		}
	}
}

// TestLocatedTypeAdmitsFormerSanctionedNamesOnSchemaAlone is the inverse of
// what this test asserted before the maintainer's 2026-08-23 ruling
// (rfc/20260823-foundation-order-ruling.md, ruling 5): [LocatedType] on its
// own - no secrets context, schema only - now admits both of
// [strictSecretsLocatedExclusion]'s names whenever a clean schema would
// admit any other markerless type, because the veto that used to live
// inside [recordableIdentitySchema] moved to [LocatedStrictSecretsRefusal],
// which this predicate does not call. What still refuses aws_iam_access_key
// and aws_iot_certificate is the operator's `strict { secrets }` setting,
// asked separately by the three callers named in
// [LocatedStrictSecretsRefusal]'s own doc comment - see
// TestLocatedStrictSecretsRefusalNamesExactlyTheRuledTypes for that half.
func TestLocatedTypeAdmitsFormerSanctionedNamesOnSchemaAlone(t *testing.T) {
	for typeName := range strictSecretsLocatedExclusion {
		if _, ok := MarkerlessTypes[typeName]; !ok {
			t.Errorf("%s is no longer in MarkerlessTypes, so this predicate has nothing to say about it. Find out what does.", typeName)
			continue
		}
		if NotImportable(typeName) {
			// aws_iot_certificate: unreachable regardless, by a wholly
			// different condition (0) - see strictSecretsLocatedExclusion's
			// own doc comment for why it is named here anyway.
			continue
		}
		if !LocatedType(typeName, map[string]providers.Schema{typeName: locatedSchema(nil)}) {
			t.Errorf("LocatedType(%q) = false with a clean schema (a string id, nothing sensitive) and NotImportable false. "+
				"Ruling 5 retired the unconditional veto; schema-only admission must now behave like any other markerless type.", typeName)
		}
	}
}

// TestLocatedStrictSecretsRefusalNamesExactlyTheRuledTypes is #365 ruling
// 5's own pattern at the pure-function layer: the toggle refuses exactly
// the two names it is given, under exactly the setting that asks it to, and
// nothing else - proved by flipping both axes.
func TestLocatedStrictSecretsRefusalNamesExactlyTheRuledTypes(t *testing.T) {
	for typeName := range strictSecretsLocatedExclusion {
		if got := LocatedStrictSecretsRefusal(typeName, strict.DefaultSecrets); got != "" {
			t.Errorf("LocatedStrictSecretsRefusal(%q, %q) = %q, want \"\" - the default setting must admit, the way stock stores the value",
				typeName, strict.DefaultSecrets, got)
		}
		if got := LocatedStrictSecretsRefusal(typeName, strict.Refuse); got == "" {
			t.Errorf("LocatedStrictSecretsRefusal(%q, %q) = \"\", want a refusal naming the setting", typeName, strict.Refuse)
		}
	}

	// A type NOT in the ruling is untouched by the toggle at either
	// setting - the whole reason this stays a named list rather than a
	// schema-derived rule (see strictSecretsLocatedExclusion's doc comment,
	// and the census it cites): a generic gate here would also catch types
	// whose secret the provider's own Read restores.
	const other = "aws_cognito_user_pool_client"
	if strictSecretsLocatedExclusion[other] {
		t.Fatalf("%s is one of the ruled types; pick a different control", other)
	}
	if got := LocatedStrictSecretsRefusal(other, strict.Refuse); got != "" {
		t.Errorf("LocatedStrictSecretsRefusal(%q, %q) = %q, want \"\" - the toggle must refuse exactly the two ruled types and nothing else",
			other, strict.Refuse, got)
	}
}

// TestCredentialMaterialSeesNestedAttributes pins the reach of the walk.
// lint.ClassSecretRefused's evidence rule (tools/row-gen's
// reduceResourceSchema) descends into nested attribute object types and
// nested blocks, and a walk that stopped at the top level would admit a
// type whose secret sits one level down.
//
// This tests [credentialMaterial] directly rather than through [LocatedType]
// as it did before 2026-08-22 (issue #365 population 2): LocatedType's own
// condition 2 no longer runs the whole-schema sweep this test is about (see
// [sensitiveIdentityAttr]), so routing through it would prove nothing about
// nested reach - it would pass because "id" stays clean, regardless of
// whether the nested walk works at all. [CredentialMaterial] is still this
// exact rule, still used by internal/live/projection's residue.
func TestCredentialMaterialSeesNestedAttributes(t *testing.T) {
	nestedBlock := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"id": {Type: cty.String, Computed: true}},
		BlockTypes: map[string]*configschema.NestedBlock{
			"parameters": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"payload": {Type: cty.String, Optional: true, Sensitive: true},
				}},
			},
		},
	}
	if !CredentialMaterial(nestedBlock) {
		t.Errorf("CredentialMaterial(...) = false for a secret inside a nested BLOCK")
	}

	nestedAttr := &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
			"credentials": {
				NestedType: &configschema.Object{
					Nesting: configschema.NestingSingle,
					Attributes: map[string]*configschema.Attribute{
						"token": {Type: cty.String, Optional: true, Sensitive: true},
					},
				},
			},
		},
	}
	if !CredentialMaterial(nestedAttr) {
		t.Errorf("CredentialMaterial(...) = false for a secret inside a nested ATTRIBUTE TYPE")
	}
}

// TestCredentialMaterialSubtractsDeprecated pins the one clause of the
// evidence rule that is a subtraction rather than a match. See
// [credentialMaterial] and tools/row-gen's liveSensitiveAttrs for why a
// deprecated sensitive attribute does not classify a type.
//
// Tests [CredentialMaterial] directly; see
// TestCredentialMaterialSeesNestedAttributes for why routing through
// [LocatedType] no longer proves this.
func TestCredentialMaterialSubtractsDeprecated(t *testing.T) {
	schema := locatedSchema(map[string]*configschema.Attribute{
		"sensitive_content": {Type: cty.String, Optional: true, Sensitive: true, Deprecated: true},
	}).Block
	if CredentialMaterial(schema) {
		t.Errorf("CredentialMaterial(...) = true for a block whose only sensitive attribute is deprecated. The deprecation subtraction is part of lint.ClassSecretRefused's rule and this predicate is supposed to apply the same one.")
	}
}

// TestLocatedImportID pins what write-back reads out of an applied object,
// including the three shapes that must NOT produce an identity: a value
// that is null, one that is not yet known (a planned object rather than an
// applied one), and an empty string. Recording any of those would put a
// record in the store that binds the instance to nothing.
func TestLocatedImportID(t *testing.T) {
	obj := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("eipassoc-01234")})
	if got, ok := LocatedImportID(obj); !ok || got != "eipassoc-01234" {
		t.Errorf("LocatedImportID = (%q, %v), want (\"eipassoc-01234\", true)", got, ok)
	}

	for name, val := range map[string]cty.Value{
		"nil":           cty.NilVal,
		"null object":   cty.NullVal(cty.Object(map[string]cty.Type{"id": cty.String})),
		"no id":         cty.ObjectVal(map[string]cty.Value{"arn": cty.StringVal("x")}),
		"null id":       cty.ObjectVal(map[string]cty.Value{"id": cty.NullVal(cty.String)}),
		"unknown id":    cty.ObjectVal(map[string]cty.Value{"id": cty.UnknownVal(cty.String)}),
		"empty id":      cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("")}),
		"not an object": cty.StringVal("x"),
	} {
		if got, ok := LocatedImportID(val); ok {
			t.Errorf("LocatedImportID(%s) = (%q, true); recording it would bind the instance to nothing", name, got)
		}
	}
}

// TestLocatedTypePopulation records the size of the located population
// against the schemas a real provider serves, and the two residues it
// leaves. It runs only where a provider can be installed, because that is
// the only place the number means anything - see this file's other tests
// for the offline half.
//
// Set CHOUDOUFU_LIVE_SCHEMAS=1 to run it. Measured at hashicorp/aws 6.59.0
// on 2026-08-17:
//
//	markerless population                     145
//	refused, credential material               10
//	refused, no top-level string id            13 (2 of them also credential)
//	admitted as record-located                124
//
// Re-measured after issue #309 widened the markerless veto and wired
// [IDNotProvenWholeTypes] into the admission - see this run's own log line
// for the current split, which now has five buckets rather than three.
//
// Ruling 5 (2026-08-23) moved aws_iam_access_key and aws_iot_certificate off
// [LocatedType]'s own credential wall (the "wall" bucket below no longer
// consults [strictSecretsLocatedExclusion] - it is a secrets-setting
// question now, asked by [LocatedStrictSecretsRefusal], not a schema
// question this predicate can see). aws_iam_access_key moves into the
// "located" bucket; aws_iot_certificate does not move at all, because
// [NotImportable] already refuses it first, unconditionally. Re-measure
// after this change before quoting the split above; it is stale by two.
//
// Re-measured 2026-08-29 (issue #430's full re-evaluation of the veto
// against #429's composite-capable located path, which had widened
// MarkerlessTypes from 140 to 158+ without a matching population sweep):
//
//	markerless population                              159
//	located, plain top-level string id                  95
//	located, composite identity object (#329)            18
//	located, composed documented import string (#337)    15
//	  (record-locatable total: 128 of 159)
//	refused, identity itself sensitive (credential wall)  1  (aws_wafv2_api_key)
//	refused, no top-level string id at all                3  (aws_apigatewayv2_routing_rule,
//	                                                          aws_network_interface_permission,
//	                                                          aws_notifications_event_rule)
//	refused, composite import documented but unproven     20 (IDNotProvenWholeTypes member with
//	                                                          no DocumentedImportIDs grammar - see
//	                                                          below)
//	refused, provider offers no classic Importer at all    7  ([NotImportable], issue #331)
//	  (genuinely tier-C-pending total: 31 of 159)
//
// The 20-type "unproven" bucket is uniform, not 20 separate gaps: every one
// of them is a member of [IDNotProvenWholeTypes] (the provider's Import
// section documents a composite string, and nothing corroborates the
// exported "id" as the whole of it) AND absent from [DocumentedImportIDs]
// (issue #337's segment-by-segment grammar, scraped from the same Import
// section by tools/importdocs-gen) - confirmed by grepping
// internal/live/identity/idnotwhole_generated.go and
// internal/live/identity/docimportid_generated.go for all 20 names, 2026-08-29.
// So [LocatedIdentityPlanFor]'s Composed branch never gets a grammar to try
// resolving against the schema; the missing capability is the scraper's, not
// the schema's. aws_identitystore_group is the type tools/row-gen/rejected.json's
// own entry for it names as the worked example: the page states the grammar in
// prose ("identity_store_id/group_id") well enough for a human to read, but
// group_id's own Attribute Reference bullet names it a server-assigned value
// rather than a configuration argument, which is exactly the shape
// tools/importdocs-gen's scraper does not turn into a [DocumentedImportIDPart].
//
// None of the 159 are rescueable through issue #272's unique-name mechanism
// (internal/live/uniquename, tools/row-gen/uniquename.go): that mechanism
// computes its own admitted rows (uniqueNameRows) and hands markerlessRoster
// their key set to SUBTRACT before this roster is ever built
// (tools/row-gen/markerless.go's own boundByName parameter), so the two sets
// are disjoint by construction - a type the unique-name rule would rescue
// never reaches [MarkerlessTypes] in the first place. Confirmed empirically,
// not just by reading the generator: `go run ./tools/row-gen -emit` against
// this commit's live/registry.json and live/import-grammar.json reproduces
// internal/live/identity/markerless_generated.go and table_generated.go
// byte-for-byte (zero diff), which is only possible if every current
// MarkerlessTypes member already failed uniqueNameRows' own admission check
// on this same run.
//
// This measurement moved three types from wrongly-approximated in-contract
// to correctly pending-mechanism in live/readiness.json: see
// tools/readiness-gen/build.go's noLocatedIdentityAttrTypes.
func TestLocatedTypePopulation(t *testing.T) {
	if os.Getenv("CHOUDOUFU_LIVE_SCHEMAS") == "" {
		t.Skip("set CHOUDOUFU_LIVE_SCHEMAS=1 to install hashicorp/aws and measure the located population against it")
	}
	dir := t.TempDir()
	schemas, err := pluginschema.ResourceTypes(context.Background(), pluginschema.Request{
		InitBin:  "terraform",
		WorkDir:  dir,
		Source:   "hashicorp/aws",
		Version:  "6.59.0",
		Provider: addrs.NewDefaultProvider("aws"),
	})
	if err != nil {
		t.Fatalf("acquiring hashicorp/aws schemas: %s", err)
	}

	var located, composite, composed, credential, credentialWide, noID, unprovenID, notImportable []string
	for name := range MarkerlessTypes {
		schema, ok := schemas[name]
		if !ok || schema.Block == nil {
			continue
		}
		if credentialMaterial(schema.Block) {
			credentialWide = append(credentialWide, name)
		}
		_, unproven := IDNotProvenWholeTypes[name]
		plan, recordable := LocatedIdentityPlanFor(name, schema)
		sensitiveID := recordable && sensitiveIdentityAttr(plan, schema) != ""
		// sanctionedCredentialExclusion is retired (ruling 5): the two
		// names it held are now a secrets-setting question
		// (LocatedStrictSecretsRefusal), not a schema-only wall this
		// predicate can see, so wall is sensitiveID alone.
		wall := sensitiveID
		switch {
		case NotImportable(name):
			// Condition 0 (issue #331). These are the types the other
			// conditions would have admitted - a clean schema, a recordable
			// id - and the provider will not import back, so the record
			// would be written and never usable.
			notImportable = append(notImportable, name)
		case wall:
			credential = append(credential, name)
		case recordable && plan.Composite():
			composite = append(composite, name)
		case recordable && plan.Composed():
			// Issue #337's second route: no wire identity schema, but the
			// page names every segment of its own composite import and
			// every one of them resolved against this schema.
			composed = append(composed, name)
		case unproven:
			unprovenID = append(unprovenID, name)
		case !hasLocatedImportID(schema.Block):
			noID = append(noID, name)
		default:
			located = append(located, name)
		}
		// The predicate re-derived from its own conditions, in the order
		// LocatedType applies them. This is the guard against the predicate
		// and its stated conditions drifting apart, and it has to name
		// every condition or it stops being one: it missed the composite
		// branch between #329 and #309 and passed anyway, because no
		// markerless type happened to be composite AND without a top-level
		// string id at the same time.
		want := !NotImportable(name) && recordable && !wall
		if LocatedType(name, schemas) != want {
			t.Errorf("LocatedType(%q) disagrees with its own conditions (notImportable=%v recordable=%v sensitiveID=%v)",
				name, NotImportable(name), recordable, sensitiveID)
		}
	}
	sort.Strings(credential)
	sort.Strings(credentialWide)
	sort.Strings(composed)
	sort.Strings(notImportable)
	t.Logf("markerless=%d located(string id)=%d located(composite object)=%d located(composed string)=%d credential=%d unprovenID=%d noID=%d notImportable=%d",
		len(MarkerlessTypes), len(located), len(composite), len(composed), len(credential), len(unprovenID), len(noID), len(notImportable))
	t.Logf("credential wall (identity itself sensitive): %v", credential)
	t.Logf("credential material anywhere in schema (informational only - not what LocatedType checks): %v", credentialWide)
	t.Logf("composed from the documented grammar (#337): %v", composed)
	t.Logf("refused by the not-importable veto (#331): %v", notImportable)
	for _, line := range credentialWallDetail(schemas, credentialWide) {
		t.Logf("credential wall detail: %s", line)
	}

	// Ruling 5, against the real schema rather than a fixture: LocatedType
	// alone (no secrets context) now ADMITS both former sanctioned
	// exclusions whenever their own conditions otherwise clear - the
	// inverse of what this loop asserted before the ruling.
	for typeName := range strictSecretsLocatedExclusion {
		if NotImportable(typeName) {
			continue
		}
		if !LocatedType(typeName, schemas) {
			t.Errorf("LocatedType(%q) = false against the real hashicorp/aws schema; ruling 5 retired the unconditional veto, so schema-only admission should behave like any other markerless type", typeName)
		}
	}
	if len(located) == 0 {
		t.Error("no markerless type is admitted as located against the real schemas, so the mechanism reaches nothing")
	}
}

// credentialWallDetail reports, for each type the credential veto refuses,
// the two facts a decision about narrowing that veto turns on: whether the
// veto is the SOLE wall (the type's identity is otherwise recordable), and
// which sensitive attributes the schema carries against which of them a
// located record would actually write.
//
// It exists because "narrow the veto for the located path, since a located
// record holds only the identity" is an argument about a population, and
// until 2026-08-21 nobody had measured that population. The line this emits
// is the measurement. It asserts nothing on its own; the numbers are for
// whoever takes the narrowing, which is a maintainer call (see
// live/e2e/corpus-alb-complete/run.sh's header and [credentialMaterial]).
//
// The three facts it separates, all of which turned out to be occupied at
// hashicorp/aws 6.59.0:
//
//   - identity not recordable. The veto is NOT the sole wall; narrowing it
//     changes nothing for this type. aws_cognito_user_pool_client, the type
//     the argument was raised for, is in this bucket.
//   - identity itself sensitive. A narrowed veto still has to refuse, or the
//     record store would hold a secret in clear - so "narrow it to the
//     recorded attributes" is not the same rule as "delete it".
//   - identity clean. These are the types a narrowing would newly admit, and
//     each one is a separate question about whether its sensitive attributes
//     survive an import-and-read, because a located record holds none of
//     them. A provider-minted secret AWS never returns (an IAM access key's
//     secret) does not survive, and admitting such a type trades a refusal
//     for a silent loss stock does not have.
//
// It names no resource type; every name in its output is derived.
func credentialWallDetail(schemas map[string]providers.Schema, credential []string) []string {
	out := make([]string, 0, len(credential))
	for _, name := range credential {
		schema := schemas[name]
		if schema.Block == nil {
			continue
		}
		var sensitive []string
		walkSchemaAttrs(schema.Block, func(a *configschema.Attribute) {
			if a.Sensitive && !a.Deprecated {
				sensitive = append(sensitive, "?")
			}
		})
		var topSensitive []string
		for attrName, a := range schema.Block.Attributes {
			if a != nil && a.Sensitive && !a.Deprecated {
				topSensitive = append(topSensitive, attrName)
			}
		}
		sort.Strings(topSensitive)

		plan, recordable := LocatedIdentityPlanFor(name, schema)
		if !recordable {
			out = append(out, name+": identity not recordable, so the credential veto is not the sole wall; top-level sensitive "+fmt.Sprint(topSensitive))
			continue
		}
		var recorded []string
		switch {
		case plan.Composite():
			recorded = plan.Components
		case plan.Composed():
			recorded = plan.ImportIDParts
		default:
			recorded = []string{locatedImportIDAttr}
		}
		identitySensitive := false
		for _, attrName := range recorded {
			a := schema.Block.Attributes[attrName]
			if a != nil && a.Sensitive && !a.Deprecated {
				identitySensitive = true
			}
		}
		bucket := "identity clean"
		if identitySensitive {
			bucket = "identity itself sensitive"
		}
		out = append(out, fmt.Sprintf("%s: sole wall, %s; would record %v; top-level sensitive %v (%d sensitive attributes in all)",
			name, bucket, recorded, topSensitive, len(sensitive)))
	}
	sort.Strings(out)
	return out
}

// TestTheSecretsSettingNowReachesTheLocatedCredentialException is what
// TestTheSecretsSettingDoesNotReachTheLocatedCredentialVeto asserted before
// the maintainer's 2026-08-23 ruling (rfc/20260823-foundation-order-ruling.md,
// ruling 5), inverted where the ruling moved the ground: the setting still
// does not reach [LocatedType]'s condition 2 (hazard one, below, is
// unchanged), but it now DOES reach [strictSecretsLocatedExclusion]'s two
// named types, through [LocatedStrictSecretsRefusal] rather than through
// [LocatedType] itself.
//
// # Hazard one, unchanged: a located record has no slot for the value it never holds
//
// A record-LOCATED type's record holds its IDENTITY and NOTHING ELSE -
// never a secret attribute outside it, whether the provider's read returns
// that attribute or not. Whether such an attribute round-trips through a
// read is [projection]'s residue question, unconditional on this route and
// on the `secrets` setting, because residue is what would carry it if
// anything does. Measured 2026-08-22 (issue #365 population 2, commit
// 361e0da9ab): nine of the eleven types [credentialMaterial] excludes have
// a clean identity - "id" is never the sensitive attribute - so the record
// this route writes never touches such an attribute either way, under any
// secrets setting. That is still condition 2's ground ([sensitiveIdentityAttr]),
// and this test's control (withSecret, below) still proves it.
//
// # What moved: the two named types are now a secrets question, not a schema wall
//
// Before ruling 5, [sanctionedCredentialExclusion] held aws_iam_access_key
// and aws_iot_certificate out of [LocatedType] unconditionally - a ruling
// baked into the schema question, so no caller could express "admit it, the
// operator asked for stock's behaviour". Ruling 5 retired that: LocatedType
// alone now admits both (see TestLocatedTypeAdmitsFormerSanctionedNamesOnSchemaAlone),
// and [LocatedStrictSecretsRefusal] is where the operator's `strict {
// secrets }` setting is asked, separately, by the three callers its own doc
// comment names.
//
// # Hazard two: for at least one type the identity IS the secret
//
// The 2026-08-22 census found it: a markerless type whose recorded identity
// is itself a sensitive attribute. This test builds that shape and shows
// what admitting it would produce - not a secret in the record, but a run
// that stops at apply, because [locatedAttrString] refuses a marked value
// rather than unmarking it. A lint refusal traded for an apply-time failure
// with the object already live is the one trade this mechanism is
// forbidden to make ([LocatedType], condition 0's own reasoning). Nothing
// about ruling 5 touches this hazard; it is still condition 2's job.
func TestTheSecretsSettingNowReachesTheLocatedCredentialException(t *testing.T) {
	typeName := aMarkerlessType(t)

	// A sensitive attribute outside the recorded identity is admitted -
	// hazard one, unchanged (see the doc comment above). The control proves
	// this is the shape being asserted, not some other condition passing by
	// coincidence: both schemas admit.
	withSecret := locatedSchema(map[string]*configschema.Attribute{
		"secret": {Type: cty.String, Computed: true, Sensitive: true},
	})
	if !LocatedType(typeName, map[string]providers.Schema{typeName: withSecret}) {
		t.Error("LocatedType refused a type whose only sensitive attribute is outside its recorded identity. " +
			"The record this route writes never touches \"secret\" regardless of the secrets setting - see " +
			"this test's doc comment, hazard one.")
	}
	clean := locatedSchema(nil)
	if !LocatedType(typeName, map[string]providers.Schema{typeName: clean}) {
		t.Fatal("the control schema is not admitted either, so the assertion above proves nothing about the " +
			"credential veto")
	}
	// The generic control type must not be one of the two ruled names, or
	// the assertions below would prove nothing about the boundary.
	if strictSecretsLocatedExclusion[typeName] {
		t.Skipf("aMarkerlessType picked %s, one of the ruled names; re-run - this control needs a different type", typeName)
	}
	if got := LocatedStrictSecretsRefusal(typeName, strict.Refuse); got != "" {
		t.Errorf("LocatedStrictSecretsRefusal(%q, refuse) = %q, want \"\" - the toggle must reach only the two ruled names, "+
			"not every markerless type with a sensitive attribute outside its identity", typeName, got)
	}

	// The maintainer's named exception is now a SECRETS-SETTING question,
	// asked at [LocatedStrictSecretsRefusal] rather than baked into
	// [LocatedType]: schema-only admission succeeds for both names (ruling
	// 5 retired the unconditional block), and the toggle is what still
	// refuses them under strict.Refuse.
	for sanctioned := range strictSecretsLocatedExclusion {
		if _, ok := MarkerlessTypes[sanctioned]; !ok {
			continue
		}
		if !NotImportable(sanctioned) && !LocatedType(sanctioned, map[string]providers.Schema{sanctioned: clean}) {
			t.Errorf("LocatedType(%q) = false with a clean schema; ruling 5 retired the unconditional veto from LocatedType itself", sanctioned)
		}
		if got := LocatedStrictSecretsRefusal(sanctioned, strict.DefaultSecrets); got != "" {
			t.Errorf("LocatedStrictSecretsRefusal(%q, default) = %q, want \"\" - stored by default, the way stock stores it", sanctioned, got)
		}
		if got := LocatedStrictSecretsRefusal(sanctioned, strict.Refuse); got == "" {
			t.Errorf("LocatedStrictSecretsRefusal(%q, refuse) = \"\", want a refusal naming the setting", sanctioned)
		}
	}

	// Hazard two, by value: the identity attribute itself is sensitive, so
	// the applied object carries it marked, and reading it back for the
	// record fails rather than writing a secret.
	marked := cty.ObjectVal(map[string]cty.Value{
		"id": cty.StringVal("wafv2-api-key-material").Mark(marks.Sensitive),
	})
	if got, ok := LocatedImportID(marked); ok {
		t.Errorf("LocatedImportID returned %q for a marked identity. Admitting such a type under any setting "+
			"would either write the secret into the record store in clear or stop the run at apply with the "+
			"object already live; refusing at the configuration is the only answer that is neither.", got)
	}
}

// TestResolverRefusesAccessKeyOnlyUnderStrictSecrets is #365 ruling 5 proved
// at the layer that acts, [resolver.resolveInstance]: aws_iam_access_key resolves
// [ClassRecordLocated] under the default secrets setting (its record holds
// only "id", never the secret - the whole reason the old exclusion was
// retired, see located.go's own commit for the finding), and is refused at
// resolution under strict { secrets = "refuse" }, mutation-checked here by
// building the identical fixture under both settings.
func TestResolverRefusesAccessKeyOnlyUnderStrictSecrets(t *testing.T) {
	schemas := map[string]providers.Schema{"aws_iam_access_key": locatedSchema(nil)}

	writeFixture := func(t *testing.T, secrets string) *configs.Config {
		t.Helper()
		dir := t.TempDir()
		strictBlock := ""
		if secrets != "" {
			strictBlock = `
    strict {
      secrets = "` + secrets + `"
    }`
		}
		src := `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }` + strictBlock + `
  }
}

resource "aws_iam_access_key" "this" {
  user = "example"
}
`
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
			t.Fatalf("writing fixture: %s", err)
		}
		return loadConfig(t, dir, nil)
	}

	t.Run("default admits", func(t *testing.T) {
		cfg := writeFixture(t, "")
		result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		assertNoErrors(t, diags)
		res := resolutionAt(t, result, "aws_iam_access_key.this")
		if res.Class != ClassRecordLocated {
			t.Fatalf("aws_iam_access_key.this resolved %s under the default secrets setting, want %s", res.Class, ClassRecordLocated)
		}
	})

	t.Run("refuse refuses", func(t *testing.T) {
		cfg := writeFixture(t, "refuse")
		_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		if !diags.HasErrors() {
			t.Fatal("aws_iam_access_key.this was admitted under strict { secrets = \"refuse\" }")
		}
		if !hasDiag(diags, "Secret-generating resource refused", "aws_iam_access_key") {
			t.Errorf("the refusal is not the secrets one:\n%s", renderDiags(diags))
		}
	})
}
