// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/intentius/choudoufu/pkg/staterecord"
)

// TestDataSourcesCarryNoMarkerOrRecord: data sources carry no ownership
// marker and no record, on either runtime. The datasources fork port
// (#2414) makes the new runtime keep data sources in its final state, as the
// default runtime always has, so the write-back sees them; it must write a
// record only for the managed instance, even when a data source shares the
// type name of a record-backed managed resource type.
func TestDataSourcesCarryNoMarkerOrRecord(t *testing.T) {
	ctx := context.Background()
	store, err := staterecord.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("building the local store: %s", err)
	}
	const prefix = "tofu-records/test-estate"

	managed := mustAddr(t, `null_resource.kept`)
	sameType := addrs.Resource{Mode: addrs.DataResourceMode, Type: "null_resource", Name: "d"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)
	plainData := addrs.Resource{Mode: addrs.DataResourceMode, Type: "null_data_source", Name: "e"}.Instance(addrs.NoKey).Absolute(addrs.RootModuleInstance)

	schema := nullResourceSchema()
	dataSchema := providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}}}
	finalState := states.NewState()
	put := func(addr addrs.AbsResourceInstance, sch providers.Schema, val cty.Value) {
		obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: val}
		src, err := obj.Encode(sch.Block.ImpliedType(), uint64(sch.Version), 0)
		if err != nil {
			t.Fatalf("encoding %s: %s", addr, err)
		}
		finalState.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, nullProvider, addrs.NoKey)
	}
	put(managed, schema, cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("kept-id"),
		"triggers": cty.NullVal(cty.Map(cty.String)),
	}))
	put(sameType, dataSchema, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("d-id")}))
	put(plainData, dataSchema, cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("e-id")}))

	schemas := &tofu.Schemas{
		Providers: map[addrs.Provider]providers.ProviderSchema{
			nullProvider.Provider: {
				Provider:      providers.Schema{Block: &configschema.Block{}},
				ResourceTypes: map[string]providers.Schema{"null_resource": schema},
				DataSources:   map[string]providers.Schema{"null_resource": dataSchema, "null_data_source": dataSchema},
			},
		},
	}

	diags := WriteBack(ctx, WriteBackRequest{
		Store:      NewRecordEnvelopeStore(store, prefix),
		FinalState: finalState,
		Schemas:    schemas,
	})
	assertNoErrors(t, diags)

	all, err := store.GetAll(ctx, prefix)
	if err != nil {
		t.Fatalf("listing the store: %s", err)
	}
	want := RecordKey(prefix, managed)
	for key := range all {
		if key != want {
			addr, _ := RecordAddr(prefix, key)
			t.Errorf("the write-back wrote a record for %s (key %s); data sources carry none", addr, key)
		}
	}
	if _, ok := all[want]; !ok {
		t.Errorf("no record for the managed instance %s; the test proves nothing", managed)
	}
}
