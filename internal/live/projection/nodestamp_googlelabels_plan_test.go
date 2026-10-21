// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/encryption"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states/statefile"
	"github.com/intentius/choudoufu/internal/tofu"
)

// GitHub issue #2134 item 7: TestMirrorGoogleMarkerLabelsMatchesStockState
// holds the mirror's output equal to a stock state's object at the value
// level. This file holds the claim the mirror exists for, through the plan
// path: the projection's own read (BuildFrom, so importAndRead,
// readImported and the attrs seed that carries default_labels), the node
// stamp, objchange.ProposedNew and the provider's PlanResourceChange. A
// converged labelled bucket plans as a no-op from a stock state file's
// prior and from the prior the projection re-derives with no state at all.
//
// The fake provider is hashicorp/google 8.6.0's label handling, from
// provider source (unmeasured against a real plugin):
//
//   - Read: tpgresource.SetLabels for labels and terraform_labels keeps
//     only the keys the prior's map names, valued from the object;
//     effective_labels is the object's whole label set.
//   - Plan: tpgresource.SetLabelsDiff. terraform_labels is default_labels,
//     the attribution label when it is on and the object already carries it
//     (or this is a create), then labels. effective_labels is the prior's,
//     with every terraform_labels entry set and every key the prior's
//     terraform_labels had and the new one lacks removed.

const gPlanEstate = "prod-net"

// gPlanDefaults is the provider block's default_labels in the
// configuration below, which the fake's plan applies the way the plugin
// would after ConfigureProvider.
var gPlanDefaults = map[string]string{"env": "prod"}

// gPlanLive is the bucket's whole label set in GCP after the apply that
// created it, plus one label another client added since.
func gPlanLive() map[string]string {
	return map[string]string{
		"team":               "net",
		"env":                "prod",
		markers.TagEstate:    gPlanEstate,
		markers.TagAddress:   markers.GoogleAddressValue(googleBucketAddr(addrs.NoKey)),
		googleAttributionKey: "true",
		"goog-other":         "x",
	}
}

func gMap(m map[string]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.String)
	}
	out := map[string]cty.Value{}
	for k, v := range m {
		out[k] = cty.StringVal(v)
	}
	return cty.MapVal(out)
}

func gKeys(v cty.Value) map[string]string {
	out := map[string]string{}
	if v.IsNull() || !v.IsKnown() {
		return out
	}
	for k, e := range v.AsValueMap() {
		out[k] = e.AsString()
	}
	return out
}

// gReadLabels is tpgresource.SetLabels: the prior's keys, valued from the
// object.
func gReadLabels(prior cty.Value, live map[string]string) cty.Value {
	out := map[string]string{}
	for k := range gKeys(prior) {
		out[k] = live[k]
	}
	return gMap(out)
}

// googleLabelsProvider is a hashicorp/google-shaped fake for one
// google_storage_bucket whose labels in GCP are live.
func googleLabelsProvider(live map[string]string) *tofu.MockProvider {
	schema := googleBucketSchema()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{"google_storage_bucket": schema},
		},
	}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		m := cty.Map(cty.String)
		return providers.ImportResourceStateResponse{ImportedResources: []providers.ImportedResource{{
			TypeName: r.TypeName,
			State: cty.ObjectVal(map[string]cty.Value{
				"name":             cty.StringVal(r.Target.ID),
				"labels":           cty.NullVal(m),
				"terraform_labels": cty.NullVal(m),
				"effective_labels": cty.NullVal(m),
			}),
		}}}
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		prior := r.PriorState
		return providers.ReadResourceResponse{NewState: cty.ObjectVal(map[string]cty.Value{
			"name":             prior.GetAttr("name"),
			"labels":           gReadLabels(prior.GetAttr("labels"), live),
			"terraform_labels": gReadLabels(prior.GetAttr("terraform_labels"), live),
			"effective_labels": gMap(live),
		})}
	}
	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		proposed := r.ProposedNewState
		prior := r.PriorState
		creating := prior.IsNull()
		priorEff, priorTL := map[string]string{}, map[string]string{}
		if !creating {
			priorEff = gKeys(prior.GetAttr("effective_labels"))
			priorTL = gKeys(prior.GetAttr("terraform_labels"))
		}
		tl := map[string]string{}
		for k, v := range gPlanDefaults {
			tl[k] = v
		}
		if _, carried := priorEff[googleAttributionKey]; carried || creating {
			tl[googleAttributionKey] = "true"
		}
		for k, v := range gKeys(proposed.GetAttr("labels")) {
			tl[k] = v
		}
		eff := map[string]string{}
		for k, v := range priorEff {
			eff[k] = v
		}
		for k, v := range tl {
			eff[k] = v
		}
		for k := range priorTL {
			if _, ok := tl[k]; !ok {
				delete(eff, k)
			}
		}
		attrs := proposed.AsValueMap()
		attrs["terraform_labels"] = gMap(tl)
		attrs["effective_labels"] = gMap(eff)
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(attrs)}
	}
	return p
}

const gPlanConfig = `
provider "google" {
  default_labels = { env = "prod" }
}
resource "google_storage_bucket" "logs" {
  name   = "logs"
  labels = { team = "net" }
}
`

// gPlanConfigValue is gPlanConfig's bucket as the plan evaluates it.
func gPlanConfigValue() cty.Value {
	return googleBucketConfig(gMap(map[string]string{"team": "net"}))
}

// gPlanStateFile is the state file a stock apply of the stamped
// configuration leaves: labels as configured after the stamp,
// terraform_labels as the provider's diff computed them, effective_labels
// as GCP held them then (before goog-other was added).
func gPlanStateFile() string {
	addr := markers.GoogleAddressValue(googleBucketAddr(addrs.NoKey))
	return fmt.Sprintf(`{
  "version": 4,
  "terraform_version": "1.10.0",
  "serial": 1,
  "lineage": "2134-item-7",
  "outputs": {},
  "resources": [{
    "mode": "managed",
    "type": "google_storage_bucket",
    "name": "logs",
    "provider": "provider[\"registry.opentofu.org/hashicorp/google\"]",
    "instances": [{
      "schema_version": 0,
      "attributes": {
        "name": "logs",
        "labels": {"team": "net", "tofu-estate": %[1]q, "tofu-address": %[2]q},
        "terraform_labels": {"team": "net", "env": "prod", "tofu-estate": %[1]q, "tofu-address": %[2]q, "goog-terraform-provisioned": "true"},
        "effective_labels": {"team": "net", "env": "prod", "tofu-estate": %[1]q, "tofu-address": %[2]q, "goog-terraform-provisioned": "true"}
      }
    }]
  }]
}`, gPlanEstate, addr)
}

// gPlan is what a plan does with prior once it has it: stamp the
// configuration at the node, propose, and ask the provider. It returns the
// planned state and whether stock would call it a no-op
// (plannedNewVal.Equals(priorVal), node_resource_abstract_instance.go).
func gPlan(t *testing.T, p providers.Interface, prior cty.Value) (cty.Value, bool) {
	t.Helper()
	schema := googleBucketSchema()
	addr := googleBucketAddr(addrs.NoKey)
	resolver := &NodeResolver{Estate: gPlanEstate}
	config, diags := resolver.AdjustConfigValue(context.Background(), addr, gPlanConfigValue(), schema)
	assertNoErrors(t, diags)
	proposed := objchange.ProposedNew(schema.Block, prior, config)
	resp := p.PlanResourceChange(context.Background(), providers.PlanResourceChangeRequest{
		TypeName:         addr.Resource.Resource.Type,
		PriorState:       prior,
		ProposedNewState: proposed,
		Config:           config,
	})
	assertNoErrors(t, resp.Diagnostics)
	eq := resp.PlannedState.Equals(prior)
	return resp.PlannedState, eq.IsKnown() && eq.True()
}

func TestGoogleLabelsCacheLessPlanEqualsPlanFromState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(gPlanConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	schema := googleBucketSchema()
	ty := schema.Block.ImpliedType()
	addr := googleBucketAddr(addrs.NoKey)
	live := gPlanLive()
	p := googleLabelsProvider(live)

	// With a state file: decode the prior, refresh it through the
	// provider's read, plan.
	file, err := statefile.Read(strings.NewReader(gPlanStateFile()), encryption.StateEncryptionDisabled())
	if err != nil {
		t.Fatalf("reading the state file: %v", err)
	}
	is := file.State.ResourceInstance(addr)
	if is == nil || is.Current == nil {
		t.Fatal("the state file holds no google_storage_bucket.logs")
	}
	fromState, err := is.Current.Decode(ty)
	if err != nil {
		t.Fatalf("decoding the state file's object: %v", err)
	}
	refreshed := p.ReadResource(context.Background(), providers.ReadResourceRequest{
		TypeName: "google_storage_bucket", PriorState: fromState.Value,
	})
	assertNoErrors(t, refreshed.Diagnostics)
	stateful := refreshed.NewState
	if planned, noop := gPlan(t, p, stateful); !noop {
		t.Fatalf("the plan from a stock state file is not a no-op, so this fake is not the provider it claims to be:\nprior   %#v\nplanned %#v", stateful, planned)
	}

	// Without one: the projection reads the object from its identity.
	provAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("google")}
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: addr, Class: identity.ClassConcrete, ImportID: "logs"},
	}, SingleProvider(provAddr, p))
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{addr.String()})
	pis := res.State.ResourceInstance(addr)
	if pis == nil || pis.Current == nil {
		t.Fatal("google_storage_bucket.logs is not in the projection")
	}
	projected, err := pis.Current.Decode(ty)
	if err != nil {
		t.Fatalf("decoding the projected object: %v", err)
	}
	cacheLess := projected.Value

	if !cacheLess.RawEquals(stateful) {
		t.Errorf("the cache-less prior differs from the refreshed state file's:\ncache-less %#v\nstate      %#v", cacheLess, stateful)
	}
	if planned, noop := gPlan(t, p, cacheLess); !noop {
		t.Errorf("the plan from the projection's cache-less prior is not a no-op:\nprior   %#v\nplanned %#v", cacheLess, planned)
	}
}
