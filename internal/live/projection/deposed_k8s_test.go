// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tofu"
)

// TestKubernetesDeposedRecordCarriesTheObjectsIdentity is the premise of
// GitHub issue #1683's second part: that the record an interrupted
// create_before_destroy rename writes on Kubernetes carries the deposed
// object's identity, so discovery's Kubernetes collision can match a live
// claimant against it (settleByDeposedRecord, internal/live/discovery).
//
// It does for a type whose record renders an identity at all, and only
// for those: kubernetes_config_map records the old object as "ns/cfg-a",
// the same NAMESPACE/NAME the sweep reads off a listed ConfigMap as its
// import ID. kubernetes_config_map_v1, the same provider resource under its
// current name, renders none (#1188,
// TestKubernetesIdentityRecordFollowsTheRatifiedRowNotTheVersionSuffix), so
// its deposed entry, if one is written at all, names no object, matches no
// claimant, and the collision stands for it. That bound is pinned here so
// that the day a _v1 type starts recording an identity this test says so.
//
// Nothing here is new behaviour: diffDeposedForWrite has recorded this
// since #361. The test pins what #1683's consumer relies on.
func TestKubernetesDeposedRecordCarriesTheObjectsIdentity(t *testing.T) {
	k8s := addrs.AbsProviderConfig{Provider: addrs.NewDefaultProvider("kubernetes"), Module: addrs.RootModule}
	for _, tc := range []struct {
		typeName   string
		wantImport string // "" when the deposed entry names no object
	}{
		{"kubernetes_config_map", "ns/cfg-a"},
		{"kubernetes_config_map_v1", ""},
	} {
		t.Run(tc.typeName, func(t *testing.T) {
			ctx := context.Background()
			store := NewRecordEnvelopeStore(localHintStore(t), RecordKeyPrefix("k8s-deposed"))
			addr := mustAddr(t, tc.typeName+".cfg")
			schema := k8sObjectMetaSchema(true)
			encode := func(name string) *states.ResourceInstanceObjectSrc {
				src, err := (&states.ResourceInstanceObject{Status: states.ObjectReady, Value: k8sObjectMetaValue("ns", name)}).
					Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
				if err != nil {
					t.Fatalf("encoding %s: %s", name, err)
				}
				return src
			}

			// The interrupted rename's final state: cfg-b created, cfg-a
			// deposed and never destroyed.
			final := states.NewState()
			final.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, encode("cfg-b"), k8s, addrs.NoKey)
			final.EnsureModule(addr.Module).SetResourceInstanceDeposed(addr.Resource, states.DeposedKey("00000001"), encode("cfg-a"), k8s, addrs.NoKey)
			schemas := &tofu.Schemas{Providers: map[addrs.Provider]providers.ProviderSchema{
				k8s.Provider: {
					Provider:      providers.Schema{Block: &configschema.Block{}},
					ResourceTypes: map[string]providers.Schema{tc.typeName: schema},
				},
			}}
			assertNoErrors(t, WriteBack(ctx, WriteBackRequest{Store: store, FinalState: final, Schemas: schemas}))

			deposed, _, _, err := store.GetDeposed(ctx, addr)
			if err != nil {
				t.Fatalf("GetDeposed: %s", err)
			}
			rec := deposed["00000001"]
			if rec.ImportID != tc.wantImport {
				t.Errorf("the deposed record for %s names %q, want %q (record %+v)", addr, rec.ImportID, tc.wantImport, deposed)
			}
			if tc.wantImport == "" && len(rec.Components) != 0 {
				t.Errorf("the deposed record for %s carries components %v; #1188 measured none for this type", addr, rec.Components)
			}
		})
	}
}
