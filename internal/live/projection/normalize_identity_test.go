// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// ---------------------------------------------------------------------------
// GitHub issue #281: a rendered identity component has to survive the
// provider's own normalisation, or the object materialises under a
// spelling the ordinary plan then compares against configuration and
// proposes a forced replace over, forever.
//
// dotProvider is a fake whose shape reproduces the mechanism found against
// floci, not just the symptom: ImportResourceState hands back a stub that
// carries the identity object's OWN "name" value verbatim (the real AWS
// provider does this for an identity-object import), ReadResource echoes
// that value back unchanged (the real provider does not re-derive "name"
// from the API answer on this path), and PlanResourceChange - a synthetic
// CREATE, prior=null - strips a trailing dot from "name", the one fact
// [builder.normalizeIdentityAttrs] is allowed to lean on. Confirmed against
// a live floci + terraform-provider-aws 6.58.0: a create-shaped
// PlanResourceChange for aws_route53_record turns "foo.example.com." into
// "foo.example.com" before the object exists.
// ---------------------------------------------------------------------------

func dotProviderSchema() providers.Schema {
	str := &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":      {Type: cty.String, Computed: true},
				"zone_id": str,
				"name":    str,
				"type":    str,
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"zone_id": {Type: cty.String, Required: true},
				"name":    {Type: cty.String, Required: true},
				"type":    {Type: cty.String, Required: true},
			},
		},
	}
}

func dotProvider(t *testing.T) providers.Interface {
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
		var resp providers.ImportResourceStateResponse
		if !r.Target.IsIdentityBased() {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("dotProvider only serves identity-based imports in this test"))
			return resp
		}
		ident := r.Target.Identity
		stub := cty.ObjectVal(map[string]cty.Value{
			"id":      cty.StringVal("Z1/" + ident.GetAttr("name").AsString() + "/CNAME"),
			"zone_id": ident.GetAttr("zone_id"),
			// The stub carries the identity's OWN "name" spelling verbatim -
			// this is the exact real-provider shape #281 found, reproduced
			// rather than asserted.
			"name": ident.GetAttr("name"),
			"type": ident.GetAttr("type"),
		})
		resp.ImportedResources = []providers.ImportedResource{{TypeName: r.TypeName, State: stub}}
		return resp
	}

	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		// Echoes the prior back unchanged: the real provider's Read does not
		// re-derive "name" from the API answer on the identity-object import
		// path, which is the whole reason the wrong spelling survives.
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}

	p.PlanResourceChangeFn = func(r providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		vals := map[string]cty.Value{}
		for name := range schema.Block.Attributes {
			v := r.ProposedNewState.GetAttr(name)
			if name == "name" && v.IsKnown() && !v.IsNull() {
				v = cty.StringVal(strings.TrimSuffix(v.AsString(), "."))
			}
			vals[name] = v
		}
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(vals)}
	}

	return p
}

// TestNormalizeIdentityAttrsConvergesBothSpellings is acceptance criterion
// (b): a configuration spelled with Route 53's own trailing dot and one
// spelled without it must resolve to the SAME stored identity-bearing
// value, and it must be the value the provider's own create-time answer
// says the live object goes by - never the raw string either configuration
// happened to write.
func TestNormalizeIdentityAttrsConvergesBothSpellings(t *testing.T) {
	cfg := loadConfig(t, "testdata/dotnormalize")

	dotted := mustAddr(t, `aws_route53_record.dotted`)
	plain := mustAddr(t, `aws_route53_record.plain`)

	provs := SingleProvider(awsProvider, dotProvider(t))

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{
			Addr:           dotted,
			Class:          identity.ClassConcrete,
			ImportID:       "Z1_foo.example.com._CNAME",
			IdentityValues: map[string]string{"zone_id": "Z1", "name": "foo.example.com.", "type": "CNAME"},
		},
		{
			Addr:           plain,
			Class:          identity.ClassConcrete,
			ImportID:       "Z1_foo.example.com_CNAME",
			IdentityValues: map[string]string{"zone_id": "Z1", "name": "foo.example.com", "type": "CNAME"},
		},
	}, provs)
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{dotted.String(), plain.String()})

	const want = `"name":"foo.example.com"` // no trailing dot: what the fake's create-time plan says the object answers to
	for _, addr := range []addrs.AbsResourceInstance{dotted, plain} {
		is := res.State.ResourceInstance(addr)
		if is == nil || is.Current == nil {
			t.Fatalf("%s is not in the projection", addr)
		}
		got := string(is.Current.AttrsJSON)
		if !strings.Contains(got, want) {
			t.Errorf("%s stored %s, want it to contain %s - both spellings must converge on the provider's own canonical form", addr, got, want)
		}
		if strings.Contains(got, `"name":"foo.example.com."`) {
			t.Errorf("%s kept the raw configuration's own spelling (%s) instead of the provider's normalised one", addr, got)
		}
	}
}

// TestNormalizeIdentityAttrsLeavesAnAlreadyCanonicalValueAlone is the
// negative case for the same mechanism: when ReadResource already answers
// with the provider's own canonical spelling, normalizeIdentityAttrs must
// make no PlanResourceChange call and no change at all - the common case,
// and the one this design is built not to tax.
func TestNormalizeIdentityAttrsLeavesAnAlreadyCanonicalValueAlone(t *testing.T) {
	cfg := loadConfig(t, "testdata/dotnormalize")
	plain := mustAddr(t, `aws_route53_record.plain`)

	provs := SingleProvider(awsProvider, dotProvider(t))
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{
			Addr:           plain,
			Class:          identity.ClassConcrete,
			ImportID:       "Z1_foo.example.com_CNAME",
			IdentityValues: map[string]string{"zone_id": "Z1", "name": "foo.example.com", "type": "CNAME"},
		},
	}, provs)
	assertNoErrors(t, diags)
	assertMaterialized(t, res, []string{plain.String()})

	is := res.State.ResourceInstance(plain)
	if is == nil || is.Current == nil {
		t.Fatalf("%s is not in the projection", plain)
	}
	if got, want := string(is.Current.AttrsJSON), `"name":"foo.example.com"`; !strings.Contains(got, want) {
		t.Errorf("%s stored %s, want it to contain %s", plain, got, want)
	}
}
