// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2140 item 7. The untaggable-gap text (discovery.go,
// servicelist.go, contentmatch.go, views/live_plan.go's TYPE_NOT_TAGGABLE)
// says that on AWS, with the estate's record present, removing an
// untaggable type's block plans the destroy. That holds only if the
// write-back after an apply records the instance; the record-orphan leg
// tests above seed their records by hand
// (projection.SeedLocatedForInstance), so none of them shows it. This one
// writes the record through the real write-back path from an applied
// aws_route53_record (no tags argument at provider 6.59.0, so no marker),
// removes the block, and asks discovery's record-removal leg.

// route53RecordAppliedSchema is aws_route53_record's shape at provider
// 6.59.0 as far as the write-back reads it: no tags argument, and the
// resource identity schema the provider serves (zone_id, name, type, with
// set_identifier optional).
func route53RecordAppliedSchema() providers.Schema {
	return providers.Schema{
		Version: 2,
		Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":              {Type: cty.String, Computed: true},
			"zone_id":         {Type: cty.String, Required: true},
			"name":            {Type: cty.String, Required: true},
			"type":            {Type: cty.String, Required: true},
			"ttl":             {Type: cty.Number, Optional: true},
			"records":         {Type: cty.Set(cty.String), Optional: true},
			"allow_overwrite": {Type: cty.Bool, Optional: true},
			"set_identifier":  {Type: cty.String, Optional: true},
		}},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"zone_id":        {Type: cty.String, Required: true},
				"name":           {Type: cty.String, Required: true},
				"type":           {Type: cty.String, Required: true},
				"set_identifier": {Type: cty.String, Optional: true},
			},
		},
	}
}

// writeBackRoute53Record runs projection.WriteBack over an apply that left
// aws_route53_record.eu-ns in the final state, into the estate's record
// store, as an apply under a live block does.
func writeBackRoute53Record(t *testing.T, store *projection.RecordStore, addr addrs.AbsResourceInstance) {
	t.Helper()
	schema := route53RecordAppliedSchema()
	obj := &states.ResourceInstanceObject{
		Status: states.ObjectReady,
		Value: cty.ObjectVal(map[string]cty.Value{
			"id":              cty.StringVal("ZJB88OBW3J7TXGA_datacite.eu_NS"),
			"zone_id":         cty.StringVal("ZJB88OBW3J7TXGA"),
			"name":            cty.StringVal("datacite.eu"),
			"type":            cty.StringVal("NS"),
			"ttl":             cty.NumberIntVal(172800),
			"records":         cty.SetVal([]cty.Value{cty.StringVal("ns-1.example.com")}),
			"allow_overwrite": cty.True,
			"set_identifier":  cty.NullVal(cty.String),
		}),
	}
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), 0)
	if err != nil {
		t.Fatalf("encoding the applied object: %s", err)
	}
	final := states.NewState()
	final.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, recordOrphanProviderAddr, addrs.NoKey)
	diags := projection.WriteBack(t.Context(), projection.WriteBackRequest{
		Store:      store,
		FinalState: final,
		Config:     loadConfig(t, route53RecordOrphanFixture(t, true)),
		Schemas: &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
			recordOrphanProviderAddr.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"aws_route53_record": schema},
			},
		}},
	})
	if diags.HasErrors() {
		t.Fatalf("write-back: %s", diags.Err())
	}
}

// TestUntaggableAWSRecordFromWriteBackPlansTheRemoval: the write-back
// records the untaggable instance's identity, and with its block removed
// the record-removal leg proposes it, undeclared and record-rooted, at the
// id the provider imports it by, which the plan reads and destroys.
func TestUntaggableAWSRecordFromWriteBackPlansTheRemoval(t *testing.T) {
	recordAddr := mustAddr(t, "aws_route53_record.eu-ns")
	if _, ok := route53RecordAppliedSchema().Block.Attributes["tags"]; ok {
		t.Fatal("the fixture's aws_route53_record schema has a tags argument; this test is about an untaggable type")
	}
	rawStore, store := recordOrphanHintStore(t)
	writeBackRoute53Record(t, store, recordAddr)

	// What the write-back left: the identity, read back the way the leg
	// reads it.
	rec, _, keyExists, identityFound, err := store.GetIdentityWithProvider(t.Context(), recordAddr)
	if err != nil || !keyExists || !identityFound {
		t.Fatalf("the write-back left no identity record for %s (exists %v, identity %v, err %v)", recordAddr, keyExists, identityFound, err)
	}
	if rec.Components["zone_id"] != "ZJB88OBW3J7TXGA" || rec.Components["name"] != "datacite.eu" || rec.Components["type"] != "NS" {
		t.Fatalf("the record holds %+v, not the applied object's identity", rec)
	}

	cfg := loadConfig(t, route53RecordOrphanFixture(t, false))
	cloud := newFakeCloud()
	cloud.own("aws_route53_zone", "ZJB88OBW3J7TXGA", "aws_route53_zone.eu")
	res, diags := Discover(t.Context(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		Sweep:       true,
		HintStore:   rawStore,
	})
	assertNoErrors(t, diags)

	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != recordAddr.String() {
			continue
		}
		found = true
		if !r.Undeclared || !r.RecordRooted || r.Class != identity.ClassConcrete {
			t.Errorf("%s: want an undeclared, record-rooted, concrete resolution, got %+v", recordAddr, r)
		}
		if want := "ZJB88OBW3J7TXGA_datacite.eu_NS"; r.ImportID != want {
			t.Errorf("%s: ImportID %q, want %q", recordAddr, r.ImportID, want)
		}
	}
	if !found {
		t.Fatalf("the removed %s was not proposed from the record the write-back left:\n%s", recordAddr, res)
	}
}
