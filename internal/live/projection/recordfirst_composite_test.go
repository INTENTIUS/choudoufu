// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #410 (day2_rename, corpus-simpleinfra-dns): a bare `moved`
// block on a parent (a Route 53 zone) leaves a composite-identity CHILD
// (aws_route53_record, identified by zone_id/name/type with no single
// import-ID string - table_generated.go's own row) needing no `moved`
// statement of its own, because its declared identity is re-derived from
// the parent's live value on every plan. The live object relocates; the
// record store's OLD, pre-rename key for the SAME child does not, because
// nothing ever prunes or rekeys it. GitHub issue #404 built exactly the
// safety net this shape needs - builder.materializedIdentity, checked
// before an Undeclared+ClassConcrete resolution is allowed to materialize
// as a destroy candidate - but it silently never fired for a composite
// type routed through builder.materializeFromRecord (the applyRecordFirst
// intercept every non-record-backed/-located resolution tries FIRST,
// GitHub issue #364 unit A2): that function hands materialize()
// rec.ImportID verbatim, which [LocatedRecordFrom] leaves EMPTY for a
// composite identity - the identity lives entirely in rec.Components - so
// the old build.go line `if importID != "" { materializedIdentity[...] =
// true }` never registered the declared instance at all, and
// recordOrphanReadSweep's own (correctly composed, non-empty) undeclared
// ImportID had nothing to match against.
//
// The fix: the dedup key is built from [traceImportID], which already
// falls back to composing the identical canonical string from the
// materialized object's own value - via the SAME [identity.LookupType]
// Components table [composeImportIDFromComponents]
// (internal/live/discovery/recordorphan_read.go) reads to build the
// undeclared side's ImportID - whenever the raw importID parameter is
// empty. Reverting to the bare `importID` check reproduces GitHub issue
// #410 exactly: the old address stops being reported ReasonSuperseded and
// materializes instead, which is precisely the shape that plans a spurious
// destroy of a live object a currently-declared instance still manages.
//
// Reaches every admitted type whose ratified entry carries Components with
// no single IdentityAttrs string and is resolved through the record-first
// path, not aws_route53_record specifically; this type is named because it
// is the confirmed, real-world instance (day2_rename's own e2e script).
func TestRecordFirstCompositeIdentitySupersedesOldAddress(t *testing.T) {
	cfg := loadConfig(t, "testdata/dotnormalize")

	declaredAddr := mustAddr(t, `aws_route53_record.plain`)
	oldAddr := mustAddr(t, `aws_route53_record.old`)

	const zoneID, name, recType = "Z1", "foo.example.com", "CNAME"
	// The exact string [composeImportIDFromComponents] would build from a
	// PERSISTED record's Components at the OLD address - table_generated.go's
	// zone_id "_" name "_" type, set_identifier omitted (absent) - which is
	// what GitHub issue #410's recordOrphanReadSweep leg hands the
	// Undeclared resolution below.
	const oldComposedImportID = zoneID + "_" + name + "_" + recType

	store := localHintStore(t)
	located := newTestLocatedStore(store, "test-estate")
	// The DECLARED address's own persisted record: Components only, no flat
	// ImportID - exactly [LocatedRecordFrom]'s plan.Composite() branch for a
	// type whose provider identity schema names zone_id/name/type (real
	// hashicorp/aws 6.59.0's shape for aws_route53_record; dotProviderSchema
	// reproduces it), written the moment this estate's own D1 rename applied.
	if _, err := located.Put(context.Background(), declaredAddr, LocatedRecord{
		Components: map[string]string{"zone_id": zoneID, "name": name, "type": recType},
	}, ""); err != nil {
		t.Fatalf("seeding the declared address's record: %s", err)
	}

	provs := SingleProvider(awsProvider, recordFirstDualModeProvider(t))

	res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
		// Class is irrelevant here on purpose: applyRecordFirst tries
		// materializeFromRecord for every resolution not
		// ClassRecordBacked/ClassRecordLocated, by ADDRESS, before ever
		// consulting Class. A real run reaches this address as
		// ClassParentDerived (the zone's live id substituted into the
		// record's own formula); ClassConcrete exercises the identical
		// applyRecordFirst intercept with less setup.
		{Addr: declaredAddr, Class: identity.ClassConcrete},
		// The OLD, pre-rename key: recordOrphanReadSweep's own shape -
		// Undeclared, ClassConcrete, a composed (non-empty) ImportID, no
		// IdentityValues map (composeImportIDFromComponents produces only
		// the joined string, never a map).
		{Addr: oldAddr, Class: identity.ClassConcrete, ImportID: oldComposedImportID, Undeclared: true},
	}, provs, Options{RecordStore: located.rs})
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{declaredAddr.String()})
	assertOmitted(t, res, map[string]Reason{oldAddr.String(): ReasonSuperseded})

	if res.Has(oldAddr) {
		t.Fatalf(
			"the old, pre-rename address is in prior state - it would plan a destroy of the live object %s still manages:\n%s",
			declaredAddr, res,
		)
	}
	if !res.Has(declaredAddr) {
		t.Fatalf("the currently-declared address is missing from prior state:\n%s", res)
	}
}

// recordFirstDualModeProvider answers both import shapes a resolution in
// this file's test can produce: identity-object (the declared, record-first
// path, whose w.values comes from the persisted record's Components) and
// plain-ID (the undeclared/old-address path, whose composed ImportID has no
// accompanying values map). dotProvider (normalize_identity_test.go) only
// ever serves the identity-object form, which is why this file does not
// reuse it: reproducing GitHub issue #410 with the fix reverted means the
// old address actually reaches materialize() and imports by its bare
// composed ID string.
func recordFirstDualModeProvider(t *testing.T) providers.Interface {
	t.Helper()
	schema := dotProviderSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"aws_route53_record": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var zoneID, name, recType string
		if r.Target.IsIdentityBased() {
			ident := r.Target.Identity
			zoneID = ident.GetAttr("zone_id").AsString()
			name = ident.GetAttr("name").AsString()
			recType = ident.GetAttr("type").AsString()
		} else if parts := strings.SplitN(r.Target.ID, "_", 3); len(parts) == 3 {
			zoneID, name, recType = parts[0], parts[1], parts[2]
		}
		state := cty.ObjectVal(map[string]cty.Value{
			"id":             cty.StringVal(zoneID + "/" + name + "/" + recType),
			"zone_id":        cty.StringVal(zoneID),
			"name":           cty.StringVal(name),
			"type":           cty.StringVal(recType),
			"set_identifier": cty.NullVal(cty.String),
		})
		return providers.ImportResourceStateResponse{
			ImportedResources: []providers.ImportedResource{{TypeName: r.TypeName, State: state}},
		}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	return p
}
