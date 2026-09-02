// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file guards the one hazard that kept [PlanInstances] unwired: it takes
// a value FROM a provider, and which provider answers changes the value.
//
// An AWS provider configured for us-east-1 derives a different ARN for the
// same resource block than the same provider configured for eu-west-2. So a
// pass that walks every module and plans everything through one instance -
// which is what PlanInstances did before the [Providers] seam - mints a
// wrong-region value for every aliased resource in the configuration. This
// repository ranks a wrong value below a missing one, and a wrong ARN here
// would become a wrong ImportID, which is the worst outcome the live path
// has: it names a real object that is not the one the block owns.
//
// The assertion is the rendered value per resource, never "a plan came back".
// Routing both resources through the default provider produces a full result
// with the right number of keys and the right shapes, and only the value
// tells the two apart.

// regionStub plans a value stamped with the region its own provider
// configuration was given, the way a real provider stamps its region into
// every ARN it derives.
func regionStub(region string) *planStub {
	return &planStub{plan: func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		obj := req.ProposedNewState.AsValueMap()
		var out []cty.Value
		if names := obj["names"]; names.IsKnown() && !names.IsNull() {
			for _, n := range names.AsValueSlice() {
				out = append(out, cty.StringVal(region+"/"+n.AsString()))
			}
		}
		if len(out) == 0 {
			obj["derived"] = cty.ListValEmpty(cty.String)
		} else {
			obj["derived"] = cty.ListVal(out)
		}
		obj["id"] = cty.UnknownVal(cty.String)
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
	}}
}

// TestPlanInstancesPlansThroughEachBlocksOwnProvider is acceptance (a) of
// GitHub issue #284.
//
// The seam is asked for each block's OWN provider configuration, and the
// value that comes back is the one that configuration produced. The mutation
// that must turn this red - and was run against it - is routing every request
// through the default configuration, which is exactly what wiring
// PlanInstances without this seam would have done.
func TestPlanInstancesPlansThroughEachBlocksOwnProvider(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-aliased")

	stubProvider := addrs.NewDefaultProvider("stub")
	defaultAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: stubProvider}
	westAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: stubProvider, Alias: "west"}

	var asked []string
	provs := ProviderFunc(func(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
		asked = append(asked, addr.String())
		switch addr.String() {
		case defaultAddr.String():
			return regionStub("us-east-1"), nil
		case westAddr.String():
			return regionStub("eu-west-2"), nil
		}
		return nil, fmt.Errorf("no provider configured for %s", addr)
	})

	got, diags := PlanInstances(context.Background(), cfg, provs)
	if diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}

	// The rendered value per address, which is the only thing that separates
	// "planned through its own provider" from "planned through the wrong one".
	want := map[string]string{
		"stub_cert.home": "us-east-1/example.com",
		"stub_cert.away": "eu-west-2/example.com",
	}
	for addr, wantVal := range want {
		val, ok := got[addr]
		if !ok {
			t.Errorf("%s absent from the result; got keys %v", addr, keysOf(got))
			continue
		}
		derived := val.GetAttr("derived")
		if derived.LengthInt() != 1 {
			t.Errorf("%s planned derived with %d elements, want 1", addr, derived.LengthInt())
			continue
		}
		if gotVal := derived.AsValueSlice()[0].AsString(); gotVal != wantVal {
			t.Errorf("%s planned %q, want %q - it was planned through the wrong provider configuration, "+
				"which is the wrong-region hazard this seam exists for", addr, gotVal, wantVal)
		}
	}

	// And that the seam was consulted for both configurations at all. A
	// version that asked only for the default would fail the value assertion
	// above too, but this says WHY in one line rather than leaving a reader
	// to work out that eu-west-2 never happened.
	sort.Strings(asked)
	wantAsked := []string{defaultAddr.String(), westAddr.String()}
	if len(asked) != 2 || asked[0] != wantAsked[0] || asked[1] != wantAsked[1] {
		t.Errorf("the provider seam was asked for %v, want exactly %v", asked, wantAsked)
	}
}

// TestPlanInstancesOmitsABlockWhoseProviderWillNotConfigure pins the other
// half of the same rule. A configuration this run cannot configure - no
// credentials for that alias, a provider block reading a value nothing set -
// contributes NOTHING, and specifically does not fall back to a provider that
// did configure. The fallback is where the wrong-region value would come
// from, and it must not exist.
func TestPlanInstancesOmitsABlockWhoseProviderWillNotConfigure(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-aliased")

	stubProvider := addrs.NewDefaultProvider("stub")
	defaultAddr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: stubProvider}

	provs := ProviderFunc(func(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
		if addr.String() == defaultAddr.String() {
			return regionStub("us-east-1"), nil
		}
		return nil, fmt.Errorf("the %q configuration has no credentials in this run", addr.Alias)
	})

	got, diags := PlanInstances(context.Background(), cfg, provs)
	if diags.HasErrors() {
		t.Fatalf("a provider that will not configure must not fail the whole pass: %s", diags.Err())
	}
	if _, ok := got["stub_cert.away"]; ok {
		t.Errorf("stub_cert.away was planned anyway, as %#v; its own provider configuration "+
			"was unavailable, so the only honest answer is no value at all", got["stub_cert.away"])
	}
	if _, ok := got["stub_cert.home"]; !ok {
		t.Errorf("stub_cert.home is missing; one unavailable configuration must not take down the "+
			"blocks whose own configuration was available. Got keys %v", keysOf(got))
	}
}
