// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/pluginschema"
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

// TestSanctionedCredentialExclusionRefusesRegardlessOfSchema pins the two
// types [sanctionedCredentialExclusion] names by NAME, which is the one
// place a name may appear here: the ruling is what cannot be derived, and
// this test is what says the ruling reaches the types it names.
//
// Before 2026-08-22 (issue #365 population 2) this refusal came from
// [credentialMaterial]'s whole-schema sweep, and the test proved that by a
// counterfactual: strip the sensitive attribute and the type would be
// admitted. That measurement showed the sweep was answering the wrong
// question for nine of the eleven types it excluded - whether the RECORD
// would carry a secret, not whether the type has one anywhere - so
// condition 2 was narrowed to [sensitiveIdentityAttr]. These two names
// survive as a standing exclusion regardless: the counterfactual would now
// prove nothing (the refusal is unconditional, not schema-derived), so what
// this test asserts instead is that property itself - refused even with a
// schema [sensitiveIdentityAttr] would admit.
func TestSanctionedCredentialExclusionRefusesRegardlessOfSchema(t *testing.T) {
	for typeName := range sanctionedCredentialExclusion {
		if _, ok := MarkerlessTypes[typeName]; !ok {
			t.Errorf("%s is no longer in MarkerlessTypes, so sanctionedCredentialExclusion no longer has a route to keep it out of. Find out what does.", typeName)
			continue
		}
		if LocatedType(typeName, map[string]providers.Schema{typeName: locatedSchema(nil)}) {
			t.Errorf("LocatedType(%q) = true with a clean schema (a string id, nothing sensitive). This type is one of the maintainer's sanctioned credential exclusions and must be refused regardless of what its schema says.", typeName)
		}
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
		wall := sensitiveID || sanctionedCredentialExclusion[name]
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
			t.Errorf("LocatedType(%q) disagrees with its own conditions (notImportable=%v recordable=%v sensitiveID=%v sanctioned=%v)",
				name, NotImportable(name), recordable, sensitiveID, sanctionedCredentialExclusion[name])
		}
	}
	sort.Strings(credential)
	sort.Strings(credentialWide)
	sort.Strings(composed)
	sort.Strings(notImportable)
	t.Logf("markerless=%d located(string id)=%d located(composite object)=%d located(composed string)=%d credential=%d unprovenID=%d noID=%d notImportable=%d",
		len(MarkerlessTypes), len(located), len(composite), len(composed), len(credential), len(unprovenID), len(noID), len(notImportable))
	t.Logf("credential wall (identity itself sensitive, or sanctioned by name): %v", credential)
	t.Logf("credential material anywhere in schema (informational only - not what LocatedType checks): %v", credentialWide)
	t.Logf("composed from the documented grammar (#337): %v", composed)
	t.Logf("refused by the not-importable veto (#331): %v", notImportable)
	for _, line := range credentialWallDetail(schemas, credentialWide) {
		t.Logf("credential wall detail: %s", line)
	}

	// The requirement, against the real schema rather than a fixture.
	for typeName := range sanctionedCredentialExclusion {
		if LocatedType(typeName, schemas) {
			t.Errorf("LocatedType(%q) = true against the real hashicorp/aws schema", typeName)
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

// TestTheSecretsSettingDoesNotReachTheLocatedCredentialVeto is GitHub issue
// #365 slice 3's deliberate BOUND, written as a test because a bound nobody
// can see is a bound the next change removes by accident.
//
// The slice turned this fork's three secret refusals into one setting. Two
// of them moved: a secret-generating logical type is admitted under the
// default and its record holds the value the way a stock state file does,
// and a sensitive settable argument is recorded as residue under the same
// default. This one, [LocatedType]'s condition 2, did NOT, and it must not
// be widened without answering the two things below.
//
// # Hazard one: a located record has no slot for the value
//
// A record-backed type's record holds its whole object; a record-LOCATED
// type's record holds its IDENTITY and nothing else, so that a later run can
// import the object back. That difference is the whole reason the two are
// different classes. So "stored the way stock stores them", which is what
// `secrets = "store"` promises, is a promise this route cannot keep for an
// attribute the provider's own read does not return - aws_iam_access_key's
// secret is the measured case (issue #365's credential-material census,
// commit 29a47794f5). Admitting the type would trade a loud refusal for a
// silent loss that stock does not have, which is worse than either.
//
// Residue is the mechanism that COULD carry such a value, and it is not a
// substitute for this analysis: residue records an attribute only after
// [classifyResidue]'s two-read discriminator proves the provider neither
// sources it from the remote nor re-derives it, which is an empirical,
// per-type, post-apply fact. Lint has to answer at the configuration, before
// any of that has happened, so admitting here would be promising something
// no static check can know.
//
// # Hazard two: for at least one type the identity IS the secret
//
// The same census found it: a markerless type whose recorded identity is
// itself a sensitive attribute. This test builds that shape and shows what
// admitting it would produce - not a secret in the record, but a run that
// stops at apply, because [locatedAttrString] refuses a marked value rather
// than unmarking it. A lint refusal traded for an apply-time failure with
// the object already live is the one trade this mechanism is forbidden to
// make ([LocatedType], condition 0's own reasoning).
//
// The selected route already answers hazard two on its own - see
// [sensitiveIdentityAttr], which is the blanket narrowed to exactly the
// attributes a record would hold - and it is safe there for a reason that
// does not transfer: a SELECTED type is already admitted by its ratified row
// and already applied with every one of its attributes, so nothing about the
// selection changes which values exist. An automatically-located type has no
// other route at all.
//
// # What this bound costs, measured
//
// As of 2026-08-22 this veto is the SOLE remaining wall for the
// corpus-alb-complete gauntlet estate's test_plan stage, on one
// aws_cognito_user_pool_client. Its other wall, condition 3, cleared when
// [DocumentedImportIDs] learned to read the type's possessive-of import
// sentence, so nothing else stands between that estate and the stage.
//
// The rule that would clear it is [sensitiveIdentityAttr], unchanged, asked
// here under [strict.Store] instead of the blanket. What is missing is not
// the rule but one measurement: whether every sensitive attribute of each
// type it would newly admit survives an import-and-read, or is picked up by
// internal/live/projection's residue classifier instead - which under
// secrets = "store" now records sensitive attributes, and is the mechanism
// that would carry exactly the values hazard one is about. That is a
// per-type, post-apply fact about a provider, and admitting at lint time on
// an unmeasured version of it is the thing this test exists to stop.
func TestTheSecretsSettingDoesNotReachTheLocatedCredentialVeto(t *testing.T) {
	typeName := aMarkerlessType(t)

	// Hazard one, as a shape: a clean identity beside a sensitive attribute
	// no record on this route would hold.
	withSecret := locatedSchema(map[string]*configschema.Attribute{
		"secret": {Type: cty.String, Computed: true, Sensitive: true},
	})
	if LocatedType(typeName, map[string]providers.Schema{typeName: withSecret}) {
		t.Error("LocatedType admitted a type carrying credential material. The secrets setting does not reach " +
			"this veto: a located record holds the identity and nothing else, so admitting the type would " +
			"lose the sensitive attribute silently, which stock does not do - see this test's doc comment.")
	}
	// And the same schema without the sensitive attribute IS admitted, so
	// the assertion above is about the veto rather than about some other
	// condition failing first.
	clean := locatedSchema(nil)
	if !LocatedType(typeName, map[string]providers.Schema{typeName: clean}) {
		t.Fatal("the control schema is not admitted either, so the assertion above proves nothing about the " +
			"credential veto")
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
