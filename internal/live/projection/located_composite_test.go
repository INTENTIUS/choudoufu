// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #329, from the transport end: a located record held one
// string, and for a type whose identity is a server-minted LEAF under a
// config-known PARENT that string is the leaf alone.
//
// The type named here is real and is a real member of that population,
// measured against hashicorp/aws 6.59.0: aws_apigatewayv2_route is in
// identity.MarkerlessTypes, has no ratified row, documents its import as
// <api_id>/<route_id>, and documents "id" as "Route identifier" - the leaf.
// Naming it in a test is allowed for the same reason located_build_test.go
// names aws_eip_association; no production control flow names either.
const compositeLocatedType = "aws_apigatewayv2_route"

// compositeLocatedSchema is that type's shape: a required config-known
// parent, a computed server-minted leaf in "id", and the provider's own
// identity schema naming both.
func compositeLocatedSchema() providers.Schema {
	return providers.Schema{
		Version: 0,
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":        {Type: cty.String, Computed: true},
				"api_id":    {Type: cty.String, Required: true},
				"route_key": {Type: cty.String, Required: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_id":     {Type: cty.String, Required: true},
				"id":         {Type: cty.String, Required: true},
				"account_id": {Type: cty.String, Optional: true},
				"region":     {Type: cty.String, Optional: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

// compositeLocatedProvider records every import target it is asked for, so a
// test can assert what the run actually sent rather than that something
// materialized.
func compositeLocatedProvider(targets *[]providers.ImportTarget) providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{compositeLocatedType: compositeLocatedSchema()},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		if targets != nil {
			*targets = append(*targets, req.Target)
		}
		// The provider answers from whichever form it was given, exactly as
		// a real one does: an identity object names the route directly, and
		// a bare string is taken as the whole import ID.
		apiID, leaf := "", ""
		if req.Target.IsIdentityBased() {
			apiID = req.Target.Identity.GetAttr("api_id").AsString()
			leaf = req.Target.Identity.GetAttr("id").AsString()
		} else {
			leaf = req.Target.ID
		}
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{
				TypeName: req.TypeName,
				State: cty.ObjectVal(map[string]cty.Value{
					"id":        cty.StringVal(leaf),
					"api_id":    cty.StringVal(apiID),
					"route_key": cty.StringVal("GET /pets"),
				}),
			}},
		}
	}
	p.ReadResourceFn = func(req providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: req.PriorState}
	}
	return p
}

func writeCompositeLocatedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `
resource "` + compositeLocatedType + `" "pets" {
  api_id    = "aabbccddee"
  route_key = "GET /pets"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	return dir
}

// TestCompositeLocatedRoundTripRecordsTheWholeIdentity is issue #329's whole
// claim, both halves, asserted by value.
//
// The mutation it is written against is the state of the code before the
// fix, and reverting to that state is a one-line change:
// writeBackLocated recording identity.LocatedImportID(obj) unconditionally.
// Do that and the record holds "1122334" - the bare leaf - the assertions
// below on Components fail, and the replan imports a fragment.
//
// Two things are asserted separately on purpose. That the RECORD holds both
// components, because a record is what survives the run; and that the
// PROVIDER was asked by identity object naming both, because a record
// nothing reads correctly is inert. Asserting only that the instance
// materialized would pass under the defect: an import of "1122334" against
// this stub materializes just as happily, which is the shape this repository
// has shipped six times.
func TestCompositeLocatedRoundTripRecordsTheWholeIdentity(t *testing.T) {
	addr := mustAddr(t, compositeLocatedType+`.pets`)
	const estate = "test-estate"
	const wantAPIID = "aabbccddee"
	const wantLeaf = "1122334"

	store := localHintStore(t)
	located := newTestLocatedStore(store, estate)

	// The state an apply finished with. "id" is the LEAF, which is the whole
	// of the defect: it is not the import identity and never was.
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal(wantLeaf),
		"api_id":    cty.StringVal(wantAPIID),
		"route_key": cty.StringVal("GET /pets"),
	})
	final := states.NewState()
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(compositeLocatedSchema().Block.ImpliedType(), 0, 1)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
		locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{compositeLocatedType: compositeLocatedSchema()}},
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
		t.Fatal("write-back recorded nothing for a composite identity")
	}
	if got := rec.Components["id"]; got != wantLeaf {
		t.Errorf("the record's leaf component = %q, want %q", got, wantLeaf)
	}
	if got := rec.Components["api_id"]; got != wantAPIID {
		t.Errorf("the record's parent component = %q, want %q.\n"+
			"This is the defect in one line: without it the record holds the leaf alone, the plan stays clean, and the NEXT run imports a fragment.", got, wantAPIID)
	}
	if rec.ImportID != "" {
		t.Errorf("the record carries the import-ID string %q as well as an identity object.\n"+
			"A composite identity has no string form this fork is willing to invent (issue #105); recording one would be a plausible identity built by joining, which is exactly what that rule forbids.", rec.ImportID)
	}

	// The read half, sharing nothing with the write half but the store.
	var targets []providers.ImportTarget
	provs := SingleProvider(locatedTestProvider, compositeLocatedProvider(&targets))
	res, buildDiags := BuildWith(context.Background(), loadConfig(t, writeCompositeLocatedFixture(t)),
		[]identity.Resolution{{Addr: addr, Class: identity.ClassRecordLocated}},
		provs, Options{RecordStore: located.rs})
	assertNoErrors(t, buildDiags)

	if len(targets) != 1 {
		t.Fatalf("the replan made %d imports, want 1", len(targets))
	}
	target := targets[0]
	if !target.IsIdentityBased() {
		t.Fatalf("the replan imported by ID %q rather than by identity object.\n"+
			"For this type the ID is the bare leaf, so a string import asks the provider for a route with no API to find it in.", target.ID)
	}
	if got := target.Identity.GetAttr("api_id").AsString(); got != wantAPIID {
		t.Errorf("imported with api_id %q, want %q", got, wantAPIID)
	}
	if got := target.Identity.GetAttr("id").AsString(); got != wantLeaf {
		t.Errorf("imported with id %q, want %q", got, wantLeaf)
	}
	assertMaterialized(t, res, []string{compositeLocatedType + `.pets`})
}

// TestWriteBackRefusesAPartialCompositeIdentity is the negative control on
// the write side: an applied object missing one component records NOTHING
// and says so, rather than recording what it has.
//
// The failure this forbids is the quiet one. A record holding only the leaf
// is indistinguishable, at read time, from a complete record - it decodes,
// it is not empty, and it binds an instance to an identity that names no
// object.
func TestWriteBackRefusesAPartialCompositeIdentity(t *testing.T) {
	addr := mustAddr(t, compositeLocatedType+`.pets`)
	store := localHintStore(t)
	located := newTestLocatedStore(store, "test-estate")

	// The parent component came back null, which is every way a component
	// can be unusable collapsed into the one an encoded object can carry.
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":        cty.StringVal("1122334"),
		"api_id":    cty.NullVal(cty.String),
		"route_key": cty.StringVal("GET /pets"),
	})
	final := states.NewState()
	src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: applied}).
		Encode(compositeLocatedSchema().Block.ImpliedType(), 0, 1)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final.EnsureModule(addrs.RootModuleInstance).
		SetResourceInstanceCurrent(addr.Resource, src, locatedTestProvider, addrs.NoKey)

	diags := WriteBack(context.Background(), WriteBackRequest{
		Store:      located.rs,
		FinalState: final,
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			locatedTestProvider.Provider: {ResourceTypes: map[string]providers.Schema{compositeLocatedType: compositeLocatedSchema()}},
		}},
	})
	if !diags.HasErrors() {
		t.Error("write-back accepted an applied object missing a component of its identity")
	}
	if _, _, exists, err := located.Get(context.Background(), addr); err != nil || exists {
		t.Errorf("a record was written anyway (exists=%v err=%v).\n"+
			"A partial record is worse than none: no record proposes a create and internal/live/foreign surfaces the object as unclaimed, whereas a partial one is imported as though it were complete.", exists, err)
	}
}

// TestLocatedStoreRefusesAnEmptyComponent closes the store-level hole the
// two tests above leave: a record whose map is present but whose value is
// empty would build an identity object the provider accepts and no object
// answers.
func TestLocatedStoreRefusesAnEmptyComponent(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, compositeLocatedType+`.pets`)
	located := newTestLocatedStore(localHintStore(t), "test-estate")

	if _, err := located.Put(ctx, addr, LocatedRecord{Components: map[string]string{"api_id": "", "id": "1122334"}}, ""); err == nil {
		t.Error("Put accepted an identity with an empty component")
	}
	if _, err := located.Put(ctx, addr, LocatedRecord{}, ""); err == nil {
		t.Error("Put accepted a record saying nothing about which object an instance owns")
	}

	// A whole one is accepted, so the refusals above are about the defect
	// and not about the store rejecting everything.
	if _, err := located.Put(ctx, addr, LocatedRecord{Components: map[string]string{"api_id": "aabbccddee", "id": "1122334"}}, ""); err != nil {
		t.Fatalf("Put refused a whole composite identity: %s", err)
	}
	rec, _, exists, err := located.Get(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("Get after Put: exists=%v err=%v", exists, err)
	}
	if rec.Components["api_id"] != "aabbccddee" || rec.Components["id"] != "1122334" {
		t.Errorf("Get returned %v, want both components round-tripped", rec.Components)
	}
}

// TestSingleStringLocatedRecordsOmitAttrs is the other half of the
// compatibility claim, and the one a reader should be most suspicious of: a
// type whose identity is one string must record and read back exactly that
// string, with the envelope's "attrs" member absent rather than
// present-and-empty - the same discriminator [identityPayload] documents
// for telling a single-string identity from a composite one.
func TestSingleStringLocatedRecordsOmitAttrs(t *testing.T) {
	ctx := context.Background()
	addr := mustAddr(t, locatedTestType+`.bastion`)
	prefix := RecordKeyPrefix("test-estate")
	rawStore := localHintStore(t)
	located := newTestLocatedStore(rawStore, "test-estate")

	const wantID = "eipassoc-00112233445566778"
	if _, err := located.Put(ctx, addr, LocatedRecord{ImportID: wantID}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	raw, _, exists, err := rawStore.Get(ctx, RecordKey(prefix, addr))
	if err != nil || !exists {
		t.Fatalf("reading the raw payload: exists=%v err=%v", exists, err)
	}
	// The wire form, not merely the decoded one: "attrs" must be absent
	// rather than present-and-empty.
	var env recordEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decoding the raw payload: %s", err)
	}
	if env.Identity == nil || env.Identity.ImportID != wantID {
		t.Fatalf("the stored envelope's identity = %+v, want ImportID %q", env.Identity, wantID)
	}
	if len(env.Identity.Attrs) != 0 {
		t.Errorf("the stored envelope's identity carries %v attrs for a single-string identity; want none", env.Identity.Attrs)
	}
	if !strings.Contains(string(raw), `"import_id":"`+wantID+`"`) || strings.Contains(string(raw), `"attrs"`) {
		t.Errorf("the stored payload is %s.\nA single-string identity must carry import_id and omit attrs entirely.", raw)
	}
}
