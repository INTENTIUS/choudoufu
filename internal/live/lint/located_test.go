// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// aLocatableType returns one type from identity.MarkerlessTypes that a
// clean schema would admit as record-located, chosen deterministically.
// Naming a type here would be a test naming a type, which is allowed; not
// naming one is better, because this test then keeps holding whatever the
// veto's membership becomes.
//
// It picks the first name the located predicate actually admits rather than
// simply the first name, because membership in identity.MarkerlessTypes is
// no longer sufficient: issue #331's veto refuses seven of them outright and
// the credential exclusion refuses more, and a caller handed one of those
// would be measuring the wrong refusal.
func aLocatableType(t *testing.T) string {
	t.Helper()
	names := make([]string, 0, len(identity.MarkerlessTypes))
	for name := range identity.MarkerlessTypes {
		names = append(names, name)
	}
	if len(names) == 0 {
		t.Fatal("identity.MarkerlessTypes is empty; every assertion below would be vacuous")
	}
	sort.Strings(names)
	for _, name := range names {
		if identity.LocatedType(name, locatableSchemas(name)) {
			return name
		}
	}
	t.Fatal("no markerless type is admitted as record-located even with a clean schema, so the located mechanism reaches nothing and every assertion below would be vacuous")
	return ""
}

// locatableSchemas is the one-entry schema map that admits typeName as
// located: a string id and nothing sensitive.
func locatableSchemas(typeName string) map[string]providers.Schema {
	return map[string]providers.Schema{typeName: {Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		},
	}}}
}

// locatedStoreShape is what a [locatedFixture] says about its record store.
// Issue #364 turned this from a bool into three cases: a live block with no
// record_store block no longer means "no store", it means the implied local
// one, and the only configuration left with no store at all is one with no
// live block.
type locatedStoreShape int

const (
	// locatedNoLiveBlock: no live block, so no estate, so nothing to imply
	// a store for. The one remaining nil-RecordStore shape, and what
	// `live-check` reads on a configuration nobody has adopted yet.
	locatedNoLiveBlock locatedStoreShape = iota
	// locatedImpliedStore: a live block with nothing but an estate name.
	locatedImpliedStore
	// locatedDeclaredStore: the same store written out longhand.
	locatedDeclaredStore
)

// locatedFixture writes a module declaring one block of typeName, with the
// live block shape asked for.
func locatedFixture(t *testing.T, typeName string, shape locatedStoreShape) *configs.Config {
	t.Helper()
	var live string
	switch shape {
	case locatedNoLiveBlock:
		live = ""
	case locatedImpliedStore:
		live = `
terraform {
  live {
    estate = "test-estate"
  }
}
`
	case locatedDeclaredStore:
		live = `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }
  }
}
`
	}
	src := live + `
resource "` + typeName + `" "thing" {
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return loadConfigDir(t, dir)
}

// TestMarkerlessTypeAdmittedUnderARecordStore is acceptance criterion (a)
// at the lint layer: with a record_store declared and the provider's schema
// clearing the type, a markerless resource is no longer refused.
//
// The point being made is the one issue #270 turns on. A marker answers
// "may I delete this"; an identity answers "which object is this". The veto
// is a fact about the first and was being read as a fact about the second.
func TestMarkerlessTypeAdmittedUnderARecordStore(t *testing.T) {
	typeName := aLocatableType(t)
	schemas := locatableSchemas(typeName)

	if !identity.LocatedType(typeName, schemas) {
		t.Fatalf("identity.LocatedType(%q) = false with a clean schema, so this test is not exercising the branch it is written for", typeName)
	}

	issues := CheckWith(t.Context(), locatedFixture(t, typeName, locatedDeclaredStore), Context{Schemas: schemas})
	for _, issue := range issues {
		if issue.Rule == RuleMarkerlessType || issue.Rule == RuleUnadmittedType {
			t.Errorf("%s still refused a markerless type under a record_store: %s\n%s", issue.Rule, issue.Construct, issue.Detail)
		}
	}
}

// TestMarkerlessLocatableTypeWithoutARecordStoreIsRefusedByName is
// acceptance criterion (d) at the lint layer, and the reason the demotion
// this issue unblocks is safe.
//
// Once tools/estate-plan can demote markerless-type to a pre-onboarding
// finding, an operator whose configuration has no store must still be
// stopped, by name, with the missing thing named. The refusal must also NOT
// carry the permanent wording, which says no configuration edit changes the
// verdict - false here, where one block does.
//
// Issue #364 moved which configuration that is. It used to be "the live
// block is there and the record_store is not"; it is now "there is no live
// block", because every live block implies a local store. The remedy the
// refusal names got SHORTER as a result, and the sentence about it still
// has to name a store.
func TestMarkerlessLocatableTypeWithoutARecordStoreIsRefusedByName(t *testing.T) {
	typeName := aLocatableType(t)
	schemas := locatableSchemas(typeName)

	issues := CheckWith(t.Context(), locatedFixture(t, typeName, locatedNoLiveBlock), Context{Schemas: schemas})

	var detail string
	for _, issue := range issues {
		if issue.Rule == RuleMarkerlessType {
			detail = issue.Detail
		}
	}
	if detail == "" {
		t.Fatalf("no %s refusal for %q with no live block at all. Demoting this refusal in tools/estate-plan would then be trading a refusal for a silent failure.", RuleMarkerlessType, typeName)
	}
	if !strings.Contains(detail, "record_store") {
		t.Errorf("the refusal does not name record_store, which is the whole fix:\n%s", detail)
	}
	if !strings.Contains(detail, markerlessLocatedSupportExists) {
		t.Errorf("the refusal does not state that the support exists now, which is the #101 defect over again:\n%s", detail)
	}
	if strings.Contains(detail, "No configuration edit changes that") {
		t.Errorf("the refusal carries the PERMANENT markerless wording for a type one block admits:\n%s", detail)
	}
}

// TestMarkerlessLocatedSupportExistsSaysSo pins the claim by value, the
// same device TestLogicalResourceDetailsRenderByClass uses on
// [recordStoreSupportExists]. Rewording it means editing this line on
// purpose.
func TestMarkerlessLocatedSupportExistsSaysSo(t *testing.T) {
	if markerlessLocatedSupportExists != "That support exists" {
		t.Errorf("markerlessLocatedSupportExists = %q. It must state, in the present tense, that the support is here NOW - a ban-list of ways to spell 'not yet' was tried once and defeated in one attempt.", markerlessLocatedSupportExists)
	}
}

// TestMarkerlessTypeStaysRefusedWithoutSchemas is the fail-closed property
// seen from lint: with no schemas the credential exclusion cannot run, so
// nothing is admitted and the permanent refusal stands unchanged.
//
// This is also the shape tools/refusal-probe's default mode measures, which
// is why that mode over-reports these types as refused. Stated on
// identity.LocatedType; asserted here.
func TestMarkerlessTypeStaysRefusedWithoutSchemas(t *testing.T) {
	typeName := aLocatableType(t)

	issues := CheckWith(t.Context(), locatedFixture(t, typeName, locatedDeclaredStore), Context{})

	var found bool
	for _, issue := range issues {
		if issue.Rule == RuleMarkerlessType {
			found = true
			if !strings.Contains(issue.Detail, "No configuration edit changes that") {
				t.Errorf("a schema-less run produced the located wording, which claims a remedy it has not verified:\n%s", issue.Detail)
			}
		}
	}
	if !found {
		t.Errorf("a markerless type was admitted with no schemas to check it against. The credential exclusion is readable only from a schema, and a predicate that cannot run must refuse.")
	}
}

// TestCredentialMaterialStaysRefusedUnderARecordStore is acceptance
// criterion (c) seen from lint: a record_store does not admit a type whose
// recorded identity would itself be a secret, whatever else it admits.
//
// Before 2026-08-22 (issue #365 population 2) the fixture put the sensitive
// attribute OUTSIDE the identity ("id" stayed clean) and this still refused,
// because identity.LocatedType's condition 2 was a whole-schema sweep. That
// was measured and found over-broad - the record this route writes never
// touches an attribute the identity doesn't include - and narrowed to
// identity.sensitiveIdentityAttr. The fixture now makes the identity itself
// sensitive, which is the shape that must still refuse.
func TestCredentialMaterialStaysRefusedUnderARecordStore(t *testing.T) {
	typeName := aLocatableType(t)
	schemas := map[string]providers.Schema{typeName: {Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true, Sensitive: true},
		},
	}}}

	issues := CheckWith(t.Context(), locatedFixture(t, typeName, locatedDeclaredStore), Context{Schemas: schemas})

	var found bool
	for _, issue := range issues {
		if issue.Rule == RuleMarkerlessType {
			found = true
		}
	}
	if !found {
		t.Errorf("a type whose recorded identity is itself sensitive was admitted under a record_store. A record_store must not be a way around that.")
	}
}

// TestLocatedTypeIsNeverAskedToStamp is the forbidden trade, asserted
// directly.
//
// Admitting a markerless type in lint without a projection path that can
// find its object would send it to marker discovery, and from there to
// internal/live/stamp's must-stamp escalation - the run would lint clean
// and then fail at APPLY with "applying this unmarked creates a resource
// you can never find again". A plan refusal traded for an apply refusal.
//
// The mechanism that prevents it is that stamp's must-stamp set is
// Result.DiscoveryCausesByBlock, which is built from NeedsDiscovery() and
// nothing else (identity's discoverycause.go). A located instance is
// ClassRecordLocated, so it is not in that map, so stamper.mustStamp is
// false and the untaggable branch stays silent. That chain is three
// packages long and this asserts its input end.
func TestLocatedTypeIsNeverAskedToStamp(t *testing.T) {
	typeName := aLocatableType(t)
	schemas := locatableSchemas(typeName)

	result, diags := identity.ResolveWith(t.Context(), locatedFixture(t, typeName, locatedDeclaredStore), identity.Context{Schemas: schemas})
	if diags.HasErrors() {
		t.Fatalf("resolution refused an admitted located type: %s", diags.Err())
	}
	if got := len(result.RecordLocated()); got != 1 {
		t.Fatalf("RecordLocated() = %d instances, want 1; this test is not exercising the class", got)
	}
	if got := result.NeedsDiscovery(); len(got) != 0 {
		t.Fatalf("NeedsDiscovery() = %d instances, want 0. A located instance routed to marker discovery is a tag sweep that can never find it, followed by stamp's unmarked-apply refusal.", len(got))
	}
	if got := result.DiscoveryCausesByBlock(); len(got) != 0 {
		t.Errorf("DiscoveryCausesByBlock() = %v, want empty. That map IS internal/live/stamp's must-stamp set, and a located block in it turns a silent untaggable skip into an apply-stopping error.", got)
	}
}

// TestLocatedAdmissionAgreesWithLint holds the two readings of "does this
// configuration declare a record_store" together.
//
// internal/live/identity reads it from cfg.Module.Live.RecordStore in its
// own recordStoreConfiguredIn, and so does this package. They must never
// disagree: lint deciding a type is admitted while resolution refuses it
// (or the reverse) is the drift GitHub issue #73 already produced once,
// when a hand table in lint and a derivation in row-gen answered the same
// question differently and resolution held records for four types lint had
// refused.
//
// The check is empirical rather than textual: the same configuration is put
// through lint's admission and through identity's resolution, and the two
// verdicts are compared.
func TestLocatedAdmissionAgreesWithLint(t *testing.T) {
	typeName := aLocatableType(t)
	schemas := locatableSchemas(typeName)

	for _, tc := range []struct {
		name       string
		shape      locatedStoreShape
		wantLocate int
	}{
		{"a declared record_store", locatedDeclaredStore, 1},
		// Issue #364. This row is the whole change seen from the
		// admission side: the same live block, with nothing but an
		// estate name in it, now produces the same located instance a
		// declared store does. If the implied default were dropped from
		// internal/configs this row goes to 0 and this test fails.
		{"the implied local record store", locatedImpliedStore, 1},
		{"no live block at all", locatedNoLiveBlock, 0},
	} {
		cfg := locatedFixture(t, typeName, tc.shape)

		var lintRefused bool
		for _, issue := range CheckWith(t.Context(), cfg, Context{Schemas: schemas}) {
			if issue.Rule == RuleMarkerlessType || issue.Rule == RuleUnadmittedType {
				lintRefused = true
			}
		}

		result, diags := identity.ResolveWith(t.Context(), cfg, identity.Context{Schemas: schemas})
		resolutionRefused := diags.HasErrors()
		var located int
		if result != nil {
			located = len(result.RecordLocated())
		}

		if lintRefused != resolutionRefused {
			t.Errorf("with %s: lint refused=%v but resolution refused=%v.\n"+
				"The two read the same field for the same decision and must agree. A type lint admits and resolution refuses stops the run after lint said it was fine; the reverse holds a record for a type lint already turned away.",
				tc.name, lintRefused, resolutionRefused)
		}
		if located != tc.wantLocate {
			t.Errorf("with %s, resolution produced %d RECORD_LOCATED instances, want %d", tc.name, located, tc.wantLocate)
		}
	}
}

// TestLocatedStrictSecretsRefusalNamesExactlyAccessKey is #365 ruling 5's
// own pattern at the lint layer: with a record_store declared, the toggle
// refuses exactly aws_iam_access_key under strict { secrets = "refuse" },
// admits it under the default, and does not reach a different markerless
// type under either setting.
func TestLocatedStrictSecretsRefusalNamesExactlyAccessKey(t *testing.T) {
	schemas := locatableSchemas("aws_iam_access_key")

	fixture := func(t *testing.T, secretsBlock string) *configs.Config {
		t.Helper()
		dir := t.TempDir()
		src := `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }` + secretsBlock + `
  }
}

resource "aws_iam_access_key" "this" {
  user = "example"
}
`
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
			t.Fatalf("writing fixture: %s", err)
		}
		return loadConfigDir(t, dir)
	}

	refuseCfg := fixture(t, `
    strict {
      secrets = "refuse"
    }`)
	var detail string
	for _, issue := range CheckWith(t.Context(), refuseCfg, Context{Schemas: schemas}) {
		if issue.Rule == RuleMarkerlessType {
			detail = issue.Detail
		}
	}
	if detail == "" {
		t.Fatal("aws_iam_access_key was not refused under strict { secrets = \"refuse\" } with a record_store declared")
	}
	if !strings.Contains(detail, `strict { secrets`) {
		t.Errorf("the refusal does not name the setting:\n%s", detail)
	}

	storeCfg := fixture(t, "")
	for _, issue := range CheckWith(t.Context(), storeCfg, Context{Schemas: schemas}) {
		if issue.Rule == RuleMarkerlessType || issue.Rule == RuleUnadmittedType {
			t.Errorf("aws_iam_access_key was refused under the default secrets setting: %s\n%s", issue.Rule, issue.Detail)
		}
	}

	// Refuses exactly this type: a different markerless type is unaffected
	// by strict { secrets } at all, under the identical record_store shape.
	other := aLocatableType(t)
	if other == "aws_iam_access_key" || other == "aws_iot_certificate" {
		t.Fatal("aLocatableType picked one of ruling 5's own names; this control needs a different type")
	}
	otherSchemas := locatableSchemas(other)
	otherCfg := locatedFixtureWithStrictSecretsRefuse(t, other)
	for _, issue := range CheckWith(t.Context(), otherCfg, Context{Schemas: otherSchemas}) {
		if issue.Rule == RuleMarkerlessType || issue.Rule == RuleUnadmittedType {
			t.Errorf("%s was refused under strict { secrets = \"refuse\" }, but the toggle must reach only aws_iam_access_key and aws_iot_certificate: %s\n%s", other, issue.Rule, issue.Detail)
		}
	}
}

// locatedFixtureWithStrictSecretsRefuse is [locatedFixture] with a
// declared record store plus strict { secrets = "refuse" }, for asserting
// the toggle's boundary against a type outside ruling 5's own two names.
func locatedFixtureWithStrictSecretsRefuse(t *testing.T, typeName string) *configs.Config {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  live {
    estate = "test-estate"
    record_store "local" {
      path = ".tofu-records"
    }
    strict {
      secrets = "refuse"
    }
  }
}

resource "` + typeName + `" "thing" {
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return loadConfigDir(t, dir)
}
