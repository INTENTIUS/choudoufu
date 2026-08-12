// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// ---------------------------------------------------------------------------
// A provider that serves identity schemas and reports which form each import
// arrived in.
//
// The existing fakeCloud deliberately serves no identity schema for anything,
// which is what pins the fallback: every test in projection_test.go goes on
// importing by ID and must keep doing so. This one is its opposite number.
// ---------------------------------------------------------------------------

// identityCloud is a fake cloud whose provider serves resource identity
// schemas, and records the form of every import request it is handed.
type identityCloud struct {
	// identityAttrs names each type's required-for-import identity
	// attributes. A type absent from the map gets no identity schema at all,
	// which is the aws_ecs_cluster shape.
	identityAttrs map[string][]string

	// objects are the live resources, keyed by "type/id" exactly as
	// fakeCloud keys them.
	objects map[string]map[string]string

	// asked records one line per import: the type, the form it arrived in,
	// and what it carried.
	asked []string
}

func newIdentityCloud() *identityCloud {
	return &identityCloud{
		identityAttrs: map[string][]string{
			"aws_cloudwatch_log_group": {"name"},
			// Two attributes, which is the composite shape.
			"aws_s3_bucket_policy": {"bucket"},
		},
		objects: make(map[string]map[string]string),
	}
}

func (c *identityCloud) put(typeName, id string, attrs map[string]string) {
	c.objects[typeName+"/"+id] = attrs
}

// identitySchemaFor is the AWS shape: the attributes that name the resource,
// plus the account and region the provider fills in itself.
func (c *identityCloud) identitySchemaFor(typeName string) *configschema.Object {
	names, ok := c.identityAttrs[typeName]
	if !ok {
		return nil
	}
	attrs := map[string]*configschema.Attribute{
		"account_id": {Type: cty.String, Optional: true},
		"region":     {Type: cty.String, Optional: true},
	}
	for _, n := range names {
		attrs[n] = &configschema.Attribute{Type: cty.String, Required: true}
	}
	return &configschema.Object{Attributes: attrs, Nesting: configschema.NestingSingle}
}

func (c *identityCloud) schemas() map[string]providers.Schema {
	out := fakeSchemas()
	for typeName, schema := range out {
		if body := c.identitySchemaFor(typeName); body != nil {
			schema.IdentitySchema = body
			schema.IdentitySchemaVersion = 1
			out[typeName] = schema
		}
	}
	return out
}

// identityValue builds one identity object of a type's schema, with the named
// attributes set and account/region left to the provider.
func (c *identityCloud) identityValue(typeName string, attrs map[string]string) cty.Value {
	body := c.identitySchemaFor(typeName)
	if body == nil {
		panic("no identity schema for " + typeName)
	}
	vals := make(map[string]cty.Value, len(body.Attributes))
	for name, at := range body.Attributes {
		if v, ok := attrs[name]; ok {
			vals[name] = cty.StringVal(v)
			continue
		}
		vals[name] = cty.NullVal(at.Type)
	}
	return cty.ObjectVal(vals)
}

func (c *identityCloud) providers(t *testing.T) Providers {
	return SingleProvider(awsProvider, c.provider(t))
}

func (c *identityCloud) provider(t *testing.T) providers.Interface {
	t.Helper()

	schemas := c.schemas()
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: schemas,
		},
	}
	p.ConfigureProviderCalled = true

	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse

		var id string
		switch {
		case r.Target.IsIdentityBased():
			// The plugin layer refuses an identity for a type with no
			// identity schema, so the fake does too rather than being kinder
			// than the wire.
			if schemas[r.TypeName].IdentitySchema == nil {
				resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("no identity schema for %s", r.TypeName))
				return resp
			}
			c.asked = append(c.asked, r.TypeName+" identity "+identityLine(r.Target.Identity))
			// A real provider looks the object up by every attribute; the
			// caricature joins the required ones the way the import string
			// does, which is what the objects are keyed by.
			id = strings.Join(identityStrings(r.Target.Identity, c.identityAttrs[r.TypeName]), "/")
		default:
			c.asked = append(c.asked, r.TypeName+" id "+r.Target.ID)
			id = r.Target.ID
		}

		schema := schemas[r.TypeName]
		stub := objectFor(schema, map[string]string{"id": id})
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    stub,
		}}
		return resp
	}

	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		var resp providers.ReadResourceResponse
		schema := schemas[r.TypeName]
		key := r.TypeName + "/" + r.PriorState.GetAttr("id").AsString()
		attrs, ok := c.objects[key]
		if !ok {
			resp.NewState = cty.NullVal(schema.Block.ImpliedType())
			return resp
		}
		resp.NewState = objectFor(schema, attrs)
		return resp
	}

	return p
}

func identityStrings(v cty.Value, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, v.GetAttr(n).AsString())
	}
	return out
}

// identityLine renders an identity object as "attr=value" pairs, sorted, with
// null attributes left out. It is what the assertions compare against.
func identityLine(v cty.Value) string {
	var parts []string
	for name, av := range v.AsValueMap() {
		if av.IsNull() {
			continue
		}
		parts = append(parts, name+"="+av.AsString())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// ---------------------------------------------------------------------------

// TestImportsByIdentityWhenDiscoveryServedOne is piece 2's claim: a
// resolution that carries the provider's own identity object is imported with
// it, not with the string read out of one of its attributes.
func TestImportsByIdentityWhenDiscoveryServedOne(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newIdentityCloud()
	cloud.put("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	})

	logs := mustAddr(t, `aws_cloudwatch_log_group.app`)
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{{
		Addr:     logs,
		Class:    identity.ClassConcrete,
		ImportID: "/ours/logs",
		Identity: cloud.identityValue("aws_cloudwatch_log_group", map[string]string{
			"name": "/ours/logs", "account_id": "000000000000", "region": "us-east-1",
		}),
	}}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{logs.String()})
	want := []string{"aws_cloudwatch_log_group identity account_id=000000000000,name=/ours/logs,region=us-east-1"}
	if !equalLines(cloud.asked, want) {
		t.Errorf("imports were asked as\n %v\nwant\n %v", cloud.asked, want)
	}
}

// TestImportsByIDWithoutAnIdentity is the other half: a resolution with no
// identity object - which is every resolution static analysis produces on its
// own - still imports by string.
func TestImportsByIDWithoutAnIdentity(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newIdentityCloud()
	cloud.put("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	})

	logs := mustAddr(t, `aws_cloudwatch_log_group.app`)
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{{
		Addr:     logs,
		Class:    identity.ClassConcrete,
		ImportID: "/ours/logs",
	}}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{logs.String()})
	if !equalLines(cloud.asked, []string{"aws_cloudwatch_log_group id /ours/logs"}) {
		t.Errorf("imports were asked as %v, want the ID form", cloud.asked)
	}
}

// TestImportsByIDWhenTheProviderServesNoIdentitySchema: the identity object is
// in hand and the provider has nowhere to put it. Both plugin protocols error
// rather than falling back, so the choice has to be made here.
func TestImportsByIDWhenTheProviderServesNoIdentitySchema(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")

	cloud := newIdentityCloud()
	// The identity object is built against a schema this provider is about to
	// stop serving, which is the shape of a resolution carried over from a
	// list call for a type whose import path has no identity schema.
	served := cloud.identityValue("aws_cloudwatch_log_group", map[string]string{"name": "/ours/logs"})
	delete(cloud.identityAttrs, "aws_cloudwatch_log_group")
	cloud.put("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
		"id": "/ours/logs", "name": "/ours/logs",
	})

	logs := mustAddr(t, `aws_cloudwatch_log_group.app`)
	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{{
		Addr:     logs,
		Class:    identity.ClassConcrete,
		ImportID: "/ours/logs",
		Identity: served,
	}}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{logs.String()})
	if !equalLines(cloud.asked, []string{"aws_cloudwatch_log_group id /ours/logs"}) {
		t.Errorf("imports were asked as %v, want the ID form for a type with no identity schema", cloud.asked)
	}
}

func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
