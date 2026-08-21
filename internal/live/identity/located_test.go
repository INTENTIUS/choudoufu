// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
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

	t.Run("credential material is refused", func(t *testing.T) {
		schema := locatedSchema(map[string]*configschema.Attribute{
			"secret": {Type: cty.String, Computed: true, Sensitive: true},
		})
		if LocatedType(markerless, map[string]providers.Schema{markerless: schema}) {
			t.Errorf("LocatedType(%q) = true for a schema carrying a sensitive attribute", markerless)
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

// credentialFixtures reproduces, for the two type names CLAUDE.md's
// sanctioned credential exclusion names AND that are actually in
// [MarkerlessTypes], the sensitive attributes hashicorp/aws 6.59.0 declares
// for them.
//
// Measured, not guessed. Running the recursive Sensitive walk over the
// provider's own GetProviderSchema response for the whole markerless
// population returns ten types, these two among them:
//
//	aws_iam_access_key   secret, ses_smtp_password_v4
//	aws_iot_certificate  ca_pem, certificate_pem, private_key, public_key
//
// TestLocatedTypePopulation re-derives that from the
// real provider when a checkout has one; this table is what keeps the two
// names pinned in an offline run.
//
// The other two names in CLAUDE.md's roster are deliberately absent:
// aws_ivs_playback_key_pair and aws_appstream_directory_config are not in
// MarkerlessTypes at all (the first is taggable), so they never reach this
// predicate and pinning them here would assert nothing.
var credentialFixtures = map[string][]string{
	"aws_iam_access_key":  {"secret", "ses_smtp_password_v4"},
	"aws_iot_certificate": {"ca_pem", "certificate_pem", "private_key", "public_key"},
}

// TestCredentialMaterialExcludesTheSanctionedTypes pins the two types by
// NAME, which is the one place a name may appear: the predicate itself
// names none, and this test is what says the derivation reaches the types
// the ruling requires it to reach.
//
// It asserts in both directions on purpose. That the type is refused WITH
// its measured sensitive attributes is the requirement; that the SAME type
// with those attributes removed would be admitted is what proves the
// refusal came from the credential rule rather than from the type happening
// to fail one of the other two conditions - which is exactly the way a
// guard comes to pass while measuring nothing.
func TestCredentialMaterialExcludesTheSanctionedTypes(t *testing.T) {
	for typeName, sensitive := range credentialFixtures {
		if _, ok := MarkerlessTypes[typeName]; !ok {
			t.Errorf("%s is no longer in MarkerlessTypes, so the credential exclusion is no longer what keeps it out of the located population. Find out what does.", typeName)
			continue
		}

		attrs := map[string]*configschema.Attribute{}
		for _, name := range sensitive {
			attrs[name] = &configschema.Attribute{Type: cty.String, Computed: true, Sensitive: true}
		}
		if LocatedType(typeName, map[string]providers.Schema{typeName: locatedSchema(attrs)}) {
			t.Errorf("LocatedType(%q) = true. This type is credential material and the run-time predicate must exclude it: %v are sensitive in its schema.", typeName, sensitive)
		}

		// The counterfactual. Same type, same everything, sensitivity
		// removed.
		clean := map[string]*configschema.Attribute{}
		for _, name := range sensitive {
			clean[name] = &configschema.Attribute{Type: cty.String, Computed: true}
		}
		if !LocatedType(typeName, map[string]providers.Schema{typeName: locatedSchema(clean)}) {
			t.Errorf("LocatedType(%q) is false even with no sensitive attribute, so the refusal above proves nothing about the credential rule - the type is failing one of the other two conditions and this test is measuring itself.", typeName)
		}
	}
}

// TestCredentialMaterialSeesNestedAttributes pins the reach of the walk.
// lint.ClassSecretRefused's evidence rule (tools/row-gen's
// reduceResourceSchema) descends into nested attribute object types and
// nested blocks, and a walk that stopped at the top level would admit a
// type whose secret sits one level down.
func TestCredentialMaterialSeesNestedAttributes(t *testing.T) {
	markerless := aMarkerlessType(t)

	nestedBlock := providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{"id": {Type: cty.String, Computed: true}},
		BlockTypes: map[string]*configschema.NestedBlock{
			"parameters": {
				Nesting: configschema.NestingList,
				Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
					"payload": {Type: cty.String, Optional: true, Sensitive: true},
				}},
			},
		},
	}}
	if LocatedType(markerless, map[string]providers.Schema{markerless: nestedBlock}) {
		t.Errorf("LocatedType(%q) = true for a secret inside a nested BLOCK", markerless)
	}

	nestedAttr := providers.Schema{Block: &configschema.Block{
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
	}}
	if LocatedType(markerless, map[string]providers.Schema{markerless: nestedAttr}) {
		t.Errorf("LocatedType(%q) = true for a secret inside a nested ATTRIBUTE TYPE", markerless)
	}
}

// TestCredentialMaterialSubtractsDeprecated pins the one clause of the
// evidence rule that is a subtraction rather than a match. See
// [credentialMaterial] and tools/row-gen's liveSensitiveAttrs for why a
// deprecated sensitive attribute does not classify a type.
func TestCredentialMaterialSubtractsDeprecated(t *testing.T) {
	markerless := aMarkerlessType(t)
	schema := locatedSchema(map[string]*configschema.Attribute{
		"sensitive_content": {Type: cty.String, Optional: true, Sensitive: true, Deprecated: true},
	})
	if !LocatedType(markerless, map[string]providers.Schema{markerless: schema}) {
		t.Errorf("LocatedType(%q) = false for a type whose only sensitive attribute is deprecated. The deprecation subtraction is part of lint.ClassSecretRefused's rule and this predicate is supposed to apply the same one.", markerless)
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

	var located, composite, composed, credential, noID, unprovenID []string
	for name := range MarkerlessTypes {
		schema, ok := schemas[name]
		if !ok || schema.Block == nil {
			continue
		}
		_, unproven := IDNotProvenWholeTypes[name]
		plan, recordable := LocatedIdentityPlanFor(name, schema)
		switch {
		case credentialMaterial(schema.Block):
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
		// The predicate re-derived from its own three conditions, in the
		// order LocatedType applies them. This is the guard against the
		// predicate and its stated conditions drifting apart, and it has
		// to name every condition or it stops being one: it missed the
		// composite branch between #329 and #309 and passed anyway,
		// because no markerless type happened to be composite AND without
		// a top-level string id at the same time.
		want := !credentialMaterial(schema.Block) && recordable
		if LocatedType(name, schemas) != want {
			t.Errorf("LocatedType(%q) disagrees with its own three conditions (credential=%v recordable=%v)",
				name, credentialMaterial(schema.Block), recordable)
		}
	}
	sort.Strings(credential)
	sort.Strings(composed)
	t.Logf("markerless=%d located(string id)=%d located(composite object)=%d located(composed string)=%d credential=%d unprovenID=%d noID=%d",
		len(MarkerlessTypes), len(located), len(composite), len(composed), len(credential), len(unprovenID), len(noID))
	t.Logf("credential material: %v", credential)
	t.Logf("composed from the documented grammar (#337): %v", composed)

	// The requirement, against the real schema rather than a fixture.
	for typeName := range credentialFixtures {
		if LocatedType(typeName, schemas) {
			t.Errorf("LocatedType(%q) = true against the real hashicorp/aws schema", typeName)
		}
	}
	if len(located) == 0 {
		t.Error("no markerless type is admitted as located against the real schemas, so the mechanism reaches nothing")
	}
}
