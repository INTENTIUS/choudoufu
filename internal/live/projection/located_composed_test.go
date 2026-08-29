// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #429, from the transport end: [TestCompositeLocatedRoundTripRecordsTheWholeIdentity]
// (located_composite_test.go) already proves the WIRE-IDENTITY-OBJECT
// composite shape ([identity.LocatedIdentityPlan.Components]) round-trips
// through [materializeLocated]. This file proves the OTHER composite shape
// [LocatedRecordFrom] can produce: [identity.LocatedIdentityPlan.Composed],
// a documented import-ID STRING assembled from more than one attribute
// (issue #337), which carries through as [LocatedRecord.ImportID] rather
// than [LocatedRecord.Components] and is handed to the provider as a flat
// [providers.ImportTarget.ID] rather than an identity object - a different
// wire shape from the Components case, and one nothing in this package
// exercised end to end before this.
//
// The type named here is real: aws_cognito_user_pool_client is the type
// tools/row-gen/rejected.json's own entry for it discusses at length, and
// is confirmed today (2026-08-28, against a live hashicorp/aws 6.59.0
// schema via `CHOUDOUFU_LIVE_SCHEMAS=1 go test ./internal/live/identity/
// -run TestLocatedTypePopulation -v`) to land in that test's "composed"
// bucket - identity.MarkerlessTypes membership, no ratified table row, no
// wire identity schema, and identity.LocatedType admits it because
// identity.IDNotProvenWholeTypes plus identity.DocumentedImportIDs resolve
// its Import section's documented grammar ("userpoolid/id", separator "/")
// against this exact schema shape. Naming a real type here is allowed for
// the same reason located_build_test.go and located_composite_test.go name
// one; no production control flow in this package names it.
const composedLocatedType = "aws_cognito_user_pool_client"

// composedLocatedSchema mirrors the real hashicorp/aws 6.59.0 shape at the
// four attributes this mechanism actually reads (confirmed against a live
// `terraform providers schema -json` pull of that exact provider version):
// a config-known parent (user_pool_id), a client-set name, a
// provider-minted leaf (id) that is documented as part of a composite
// import rather than the whole of it, and a Sensitive, non-Deprecated
// client_secret this route must never record - the same reduced shape
// internal/live/liveimport's located_admission_test.go and
// internal/live/identity's docimportid_possessive_test.go already use for
// the same type. No wire IdentitySchema, which is what routes
// [identity.LocatedIdentityPlanFor] to the Composed branch instead of the
// Components one [TestCompositeLocatedRoundTripRecordsTheWholeIdentity]
// already covers.
func composedLocatedSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"user_pool_id":  {Type: cty.String, Required: true},
				"name":          {Type: cty.String, Required: true},
				"id":            {Type: cty.String, Computed: true},
				"client_secret": {Type: cty.String, Computed: true, Sensitive: true},
			},
		},
	}
}

// composedLocatedProvider records every import target it is asked for -
// [providers.ImportTarget], the exact struct [importTarget] chooses between
// an identity object and a bare ID - so a test can assert what the run
// actually sent the provider, not merely that something materialized.
//
// Its ImportResourceStateFn deliberately reads ONLY req.Target.ID: a real
// provider release for this type serves no identity schema (confirmed
// above), so a wire request carrying Target.Identity instead of Target.ID
// would be a shape this route must never produce, and a fake provider that
// accepted an identity object here would hide that defect rather than
// catch it.
func composedLocatedProvider(targets *[]providers.ImportTarget) providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{composedLocatedType: composedLocatedSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if targets != nil {
			*targets = append(*targets, req.Target)
		}
		if req.Target.IsIdentityBased() {
			// The mutation this whole file exists to catch: importTarget
			// must fall back to the ID string when the schema carries no
			// IdentitySchema, per its own doc comment. Answering from an
			// identity object here would let that regression pass unnoticed
			// - see this function's own doc comment.
			return providers.ImportResourceStateResponse{}
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{
				TypeName: req.TypeName,
				State: cty.ObjectVal(map[string]cty.Value{
					"id":            cty.StringVal(cognitoClientID),
					"user_pool_id":  cty.StringVal(cognitoPoolID),
					"name":          cty.StringVal("app"),
					"client_secret": cty.NullVal(cty.String),
				}),
			}},
		}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	return p
}

func writeComposedLocatedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "` + composedLocatedType + `" "app" {
  user_pool_id = "` + cognitoPoolID + `"
  name         = "app"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// cognitoPoolID and cognitoClientID are the exact values
// internal/live/liveimport/located_admission_test.go and
// internal/live/identity/docimportid_possessive_test.go already pin for
// this type's documented Import section example
// ("us-west-2_abc123/3ho4ek12345678909nh3fmhpko"), restated here rather
// than exported across packages so a reader can check the three by eye
// without following an import.
const (
	cognitoPoolID   = "us-west-2_abc123"
	cognitoClientID = "3ho4ek12345678909nh3fmhpko"
)

// TestComposedLocatedRoundTripImportsTheDocumentedString is issue #429's
// whole claim for the Composed shape: record the object, "delete state" (a
// second projection built from nothing but the record store), import back
// through the located mechanism, and confirm both that the import used the
// EXACT documented string - never an identity object, never the bare
// leaf - and that the replan materializes the same object clean, which is
// what an empty plan means at this package's own layer (every other
// round-trip test in this package - TestWriteBackLocatedRoundTrip,
// TestCompositeLocatedRoundTripRecordsTheWholeIdentity - stops at the same
// point and leaves the full-plan proof to live/e2e's floci crossing).
//
// Two things are asserted separately on purpose, mirroring
// TestCompositeLocatedRoundTripRecordsTheWholeIdentity's own reasoning:
// that the RECORD holds the composed string and nothing else (Components
// empty - this is the OTHER composite shape, not the one that file
// covers), and that the PROVIDER was asked to import that exact string
// rather than a fragment or an identity object. The mutation this guards
// against is real: before the Composed branch existed, writeBackLocated
// (LocatedRecordFrom's predecessor) recorded identity.LocatedImportID(obj)
// unconditionally, which for this type is the bare leaf
// "3ho4ek12345678909nh3fmhpko" - a real client ID with no pool to find it
// in, and the next run's import of it would fail against a real API
// (never against this fixture's own accommodating mock).
func TestComposedLocatedRoundTripImportsTheDocumentedString(t *testing.T) {
	addr := mustAddr(t, composedLocatedType+`.app`)
	const estate = "test-estate"
	const wantComposed = cognitoPoolID + "/" + cognitoClientID

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)

	// The state a migrate or an apply finished with: the real object,
	// client_secret included, which the record must never carry.
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal(cognitoClientID),
		"user_pool_id":  cty.StringVal(cognitoPoolID),
		"name":          cty.StringVal("app"),
		"client_secret": cty.StringVal("s3cr3t"),
	})
	final := states.NewState()
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(composedLocatedSchema().Block.ImpliedType(), 0, 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{composedLocatedType: composedLocatedSchema()}},
	}}

	assertNoErrors(t, WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas:    schemas,
	}))

	rec, _, exists, err := located.Get(context.Background(), addr)
	if err != nil {
		t.Fatalf("reading back the located record: %s", err)
	}
	if !exists {
		t.Fatal("write-back recorded nothing for a Composed identity")
	}
	if rec.ImportID != wantComposed {
		t.Errorf("recorded import ID = %q, want %q - the documented \"userpoolid/id\" grammar, composed from the "+
			"applied object's own attributes in the documented order, not the bare leaf \"id\" alone", rec.ImportID, wantComposed)
	}
	if len(rec.Components) != 0 {
		t.Errorf("the record carries a Components object (%v) as well as an ImportID string.\n"+
			"This type has no wire identity schema, so LocatedRecordFrom must take the Composed branch (a "+
			"documented import STRING), never the Components branch (a provider identity OBJECT) "+
			"TestCompositeLocatedRoundTripRecordsTheWholeIdentity already covers.", rec.Components)
	}

	// The read half, sharing nothing with the write half but the store -
	// the "delete state, import back" leg.
	var targets []providers.ImportTarget
	provs := SingleProvider(locatedTestProvider, composedLocatedProvider(&targets))
	res, buildDiags := BuildWith(context.Background(), loadConfig(t, writeComposedLocatedFixture(t)),
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{RecordStore: located.rs})
	assertNoErrors(t, buildDiags)

	if len(targets) != 1 {
		t.Fatalf("the replan made %d imports, want 1", len(targets))
	}
	target := targets[0]
	if target.IsIdentityBased() {
		t.Fatalf("the replan imported by identity object rather than by ID string.\n" +
			"aws_cognito_user_pool_client serves no wire identity schema at hashicorp/aws 6.59.0, so importTarget " +
			"must fall back to the ID string; sending an identity object here is a shape the real provider refuses.")
	}
	if target.ID != wantComposed {
		t.Errorf("the replan imported %q, want %q - the exact documented import string the record held, not the "+
			"bare leaf and not a fragment", target.ID, wantComposed)
	}

	// Materialized clean: the decoded prior-state object carries exactly
	// the identity components the fixture's own configuration and the
	// applied object agree on, which is what makes the next plan empty -
	// a wrong identity would still materialize (an import of the bare leaf
	// succeeds against this permissive mock too), so this checks the VALUE,
	// not merely that something was placed in prior state.
	assertMaterialized(t, res, []string{composedLocatedType + `.app`})
	inst := res.State.ResourceInstance(addr)
	if inst == nil || inst.Current == nil {
		t.Fatal("no current object for the located instance")
	}
	obj, err := inst.Current.Decode(composedLocatedSchema().Block.ImpliedType())
	if err != nil {
		t.Fatalf("decoding the materialized object: %s", err)
	}
	if got := obj.Value.GetAttr("user_pool_id").AsString(); got != cognitoPoolID {
		t.Errorf("materialized user_pool_id = %q, want %q - the fixture's own configuration would show a diff on "+
			"this argument if it did not match, which is what an empty plan means for a Required, non-computed "+
			"attribute", got, cognitoPoolID)
	}
	if got := obj.Value.GetAttr("id").AsString(); got != cognitoClientID {
		t.Errorf("materialized id = %q, want %q", got, cognitoClientID)
	}
}
