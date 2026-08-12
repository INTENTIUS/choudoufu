// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package listclient

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/plugin"
	"github.com/opentofu/opentofu/internal/plugin6"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/tfdiags"
)

// Both gRPC provider handles must satisfy Lister. If a signature ever drifts
// on one side, this is where it shows up rather than at a call site far away.
var (
	_ Lister = (*plugin.GRPCProvider)(nil)
	_ Lister = (*plugin6.GRPCProvider)(nil)
)

// fakeLister is a provider handle that answers from canned data, so these
// tests exercise the client's own logic with no gRPC in the way.
type fakeLister struct {
	schema providers.GetProviderSchemaResponse

	// events is what the stream yields, in order.
	events []providers.ListResourceEvent
	// streamDiags is what the stream call reports.
	streamDiags tfdiags.Diagnostics

	// gotReq records the request the client actually built.
	gotReq   providers.ListResourceRequest
	streamed bool
}

func (f *fakeLister) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return f.schema
}

func (f *fakeLister) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	f.gotReq = req
	f.streamed = true
	for _, ev := range f.events {
		if !emit(ev) {
			break
		}
	}
	return f.streamDiags
}

// notAProvider implements enough to look like a provider handle but not the
// list protocol, standing in for the mocks and doubles in the tree.
type notAProvider struct{}

func (notAProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{}
}

// testSchema is a provider that lists aws_vpc-ish things: a region
// attribute, a filter block, an identity schema, and a resource schema.
func testSchema() providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"test_thing": {
				Version: 3,
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"id":   {Type: cty.String, Computed: true},
						"tags": {Type: cty.Map(cty.String), Optional: true},
					},
				},
				IdentitySchemaVersion: 2,
				IdentitySchema: &configschema.Object{
					Nesting: configschema.NestingSingle,
					Attributes: map[string]*configschema.Attribute{
						"id":     {Type: cty.String, Required: true},
						"region": {Type: cty.String, Optional: true},
					},
				},
			},
		},
		ListResourceTypes: map[string]providers.Schema{
			"test_thing": {
				Block: &configschema.Block{
					Attributes: map[string]*configschema.Attribute{
						"region": {Type: cty.String, Optional: true},
					},
					BlockTypes: map[string]*configschema.NestedBlock{
						"filter": {
							Nesting: configschema.NestingList,
							Block: configschema.Block{
								Attributes: map[string]*configschema.Attribute{
									"name":   {Type: cty.String, Required: true},
									"values": {Type: cty.List(cty.String), Required: true},
								},
							},
						},
					},
				},
			},
		},
	}
}

func identityVal(id, region string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal(id),
		"region": cty.StringVal(region),
	})
}

func TestListSchemas(t *testing.T) {
	f := &fakeLister{schema: testSchema()}

	schemas, diags := ListSchemas(context.Background(), f)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}

	if got := schemas.Types(); len(got) != 1 || got[0] != "test_thing" {
		t.Fatalf("Types(): %v", got)
	}
	if !schemas.Supports("test_thing") {
		t.Error("test_thing should be listable")
	}
	if schemas.Supports("test_other") {
		t.Error("test_other is not listable")
	}

	ts, ok := schemas.Get("test_thing")
	if !ok {
		t.Fatal("Get returned nothing for a listable type")
	}
	if ts.TypeName != "test_thing" {
		t.Errorf("TypeName: %q", ts.TypeName)
	}
	// The list config schema, not the resource schema.
	if _, ok := ts.Config.Attributes["region"]; !ok {
		t.Error("list config schema is missing region")
	}
	if _, ok := ts.Config.BlockTypes["filter"]; !ok {
		t.Error("list config schema is missing the filter block")
	}
	// The identity and resource schemas come from the managed resource type
	// of the same name, which is where the wire puts them.
	if !ts.HasIdentity() {
		t.Fatal("expected an identity schema")
	}
	if _, ok := ts.Identity.Attributes["id"]; !ok {
		t.Error("identity schema is missing id")
	}
	if ts.IdentityVersion != 2 {
		t.Errorf("IdentityVersion: %d", ts.IdentityVersion)
	}
	if ts.Resource == nil {
		t.Fatal("expected a resource schema")
	}
	if _, ok := ts.Resource.Attributes["tags"]; !ok {
		t.Error("resource schema is missing tags")
	}
}

func TestListSchemas_providerWithoutListSupport(t *testing.T) {
	// A provider from before the list protocol: real schemas, no list
	// schemas. That is not an error, it is an empty answer.
	schema := testSchema()
	schema.ListResourceTypes = nil
	f := &fakeLister{schema: schema}

	schemas, diags := ListSchemas(context.Background(), f)
	if diags.HasErrors() {
		t.Fatalf("a provider that cannot list is not an error: %v", diags.Err())
	}
	if schemas.Len() != 0 {
		t.Fatalf("expected no listable types, got %v", schemas.Types())
	}
	if schemas.Supports("test_thing") {
		t.Error("Supports must be false when nothing is listable")
	}
	if _, ok := schemas.Get("test_thing"); ok {
		t.Error("Get must miss when nothing is listable")
	}
}

func TestListSchemas_notAProviderHandle(t *testing.T) {
	for name, provider := range map[string]any{
		"nil":            nil,
		"wrong type":     notAProvider{},
		"not a provider": "hello",
	} {
		t.Run(name, func(t *testing.T) {
			schemas, diags := ListSchemas(context.Background(), provider)
			if !diags.HasErrors() {
				t.Fatal("expected an error diagnostic, not a panic and not silence")
			}
			if !hasDiag(diags, "Provider without list protocol support") {
				t.Errorf("unexpected diagnostic: %v", diags)
			}
			if schemas.Len() != 0 {
				t.Error("expected no schemas")
			}
		})
	}
}

func TestListSchemas_schemaError(t *testing.T) {
	var bad tfdiags.Diagnostics
	bad = bad.Append(tfdiags.Sourceless(tfdiags.Error, "Provider is broken", "it fell over"))
	f := &fakeLister{schema: providers.GetProviderSchemaResponse{Diagnostics: bad}}

	_, diags := ListSchemas(context.Background(), f)
	if !diags.HasErrors() {
		t.Fatal("a schema error must surface")
	}
	if !hasDiag(diags, "Provider is broken") {
		t.Errorf("the provider's own diagnostic should be passed through: %v", diags)
	}
}

func TestListSchemas_listSchemaWithoutBlock(t *testing.T) {
	// A provider bug: a list schema with no configuration block. The type
	// must be skipped with a warning, not offered up to fail later.
	schema := testSchema()
	schema.ListResourceTypes["test_broken"] = providers.Schema{}
	f := &fakeLister{schema: schema}

	schemas, diags := ListSchemas(context.Background(), f)
	if diags.HasErrors() {
		t.Fatalf("a broken type should not fail the whole call: %v", diags.Err())
	}
	if schemas.Supports("test_broken") {
		t.Error("the unusable type should not be offered")
	}
	if !schemas.Supports("test_thing") {
		t.Error("the usable type should survive its broken neighbour")
	}
	if !hasDiag(diags, "Invalid list resource schema") {
		t.Errorf("expected a warning about the broken schema: %v", diags)
	}
}

func TestBuildConfig(t *testing.T) {
	f := &fakeLister{schema: testSchema()}
	schemas, diags := ListSchemas(context.Background(), f)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	ts, _ := schemas.Get("test_thing")

	t.Run("empty", func(t *testing.T) {
		val, diags := ts.BuildConfig(nil)
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		// Must conform to the schema's implied type, or msgpack encoding
		// against that type fails on the way out.
		if !val.Type().Equals(ts.Config.ImpliedType()) {
			t.Fatalf("type mismatch: %#v", val.Type())
		}
		if !val.GetAttr("region").IsNull() {
			t.Error("an unset attribute should be null")
		}
		// A block goes to its empty collection, not to null: providers
		// decoding a block list do not universally tolerate a null.
		filter := val.GetAttr("filter")
		if filter.IsNull() {
			t.Fatal("an unset list block should be an empty list, not null")
		}
		if filter.LengthInt() != 0 {
			t.Errorf("expected an empty filter list, got %d elements", filter.LengthInt())
		}
	})

	t.Run("attribute set", func(t *testing.T) {
		val, diags := ts.BuildConfig(map[string]cty.Value{
			"region": cty.StringVal("us-east-1"),
		})
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		if got := val.GetAttr("region").AsString(); got != "us-east-1" {
			t.Errorf("region: %q", got)
		}
		if !val.Type().Equals(ts.Config.ImpliedType()) {
			t.Errorf("type mismatch: %#v", val.Type())
		}
	})

	t.Run("filter block set", func(t *testing.T) {
		// This is the shape P2.3 needs: a tag filter expressed in the
		// provider's own filter vocabulary.
		val, diags := ts.BuildConfig(map[string]cty.Value{
			"filter": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"name":   cty.StringVal("tag:tofu-estate"),
				"values": cty.ListVal([]cty.Value{cty.StringVal("stateless-e2e")}),
			})}),
		})
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		if !val.Type().Equals(ts.Config.ImpliedType()) {
			t.Fatalf("type mismatch: %#v", val.Type())
		}
		filter := val.GetAttr("filter")
		if filter.LengthInt() != 1 {
			t.Fatalf("expected one filter, got %d", filter.LengthInt())
		}
		got := filter.Index(cty.NumberIntVal(0)).GetAttr("name").AsString()
		if got != "tag:tofu-estate" {
			t.Errorf("filter name: %q", got)
		}
		if !val.GetAttr("region").IsNull() {
			t.Error("region should still be null")
		}
	})

	t.Run("unknown argument", func(t *testing.T) {
		_, diags := ts.BuildConfig(map[string]cty.Value{
			"tags": cty.MapValEmpty(cty.String),
		})
		if !diags.HasErrors() {
			t.Fatal("an argument the list schema does not have must be rejected")
		}
		if !hasDiag(diags, "no argument named \"tags\"") {
			t.Errorf("the diagnostic should name the argument: %v", diags)
		}
		// It should also say what is accepted, so the caller can fix it.
		if !hasDiag(diags, "filter, region") {
			t.Errorf("the diagnostic should list the accepted arguments: %v", diags)
		}
	})

	t.Run("unconvertible value", func(t *testing.T) {
		_, diags := ts.BuildConfig(map[string]cty.Value{
			"region": cty.ListValEmpty(cty.String),
		})
		if !diags.HasErrors() {
			t.Fatal("a value of the wrong type must be rejected")
		}
		if !hasDiag(diags, "Invalid list configuration argument") {
			t.Errorf("unexpected diagnostic: %v", diags)
		}
	})

	t.Run("converted value", func(t *testing.T) {
		// A number where a string is wanted converts, the same way HCL
		// would convert it.
		val, diags := ts.BuildConfig(map[string]cty.Value{
			"region": cty.NumberIntVal(1),
		})
		if diags.HasErrors() {
			t.Fatal(diags.Err())
		}
		if got := val.GetAttr("region").AsString(); got != "1" {
			t.Errorf("region: %q", got)
		}
	})

	t.Run("no schema", func(t *testing.T) {
		var empty TypeSchema
		if _, diags := empty.BuildConfig(nil); !diags.HasErrors() {
			t.Fatal("building against no schema must be an error, not a nil deref")
		}
		if got := empty.EmptyConfig(); got != cty.NilVal {
			t.Errorf("EmptyConfig with no schema: %#v", got)
		}
	})
}

func TestList(t *testing.T) {
	f := &fakeLister{
		schema: testSchema(),
		events: []providers.ListResourceEvent{
			{Identity: identityVal("vpc-1", "us-east-1"), DisplayName: "first"},
			{Identity: identityVal("vpc-2", "us-east-1"), DisplayName: "second"},
		},
	}

	config := cty.ObjectVal(map[string]cty.Value{
		"region": cty.StringVal("us-east-1"),
		"filter": cty.ListValEmpty(cty.Object(map[string]cty.Type{
			"name": cty.String, "values": cty.List(cty.String),
		})),
	})

	results, diags := List(context.Background(), f, "test_thing", config, false)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].TypeName != "test_thing" {
		t.Errorf("TypeName: %q", results[0].TypeName)
	}
	if results[0].DisplayName != "first" {
		t.Errorf("DisplayName: %q", results[0].DisplayName)
	}
	if !results[0].HasIdentity() {
		t.Fatal("expected an identity")
	}
	id, ok := results[0].IdentityAttr("id")
	if !ok || id != "vpc-1" {
		t.Errorf("IdentityAttr(id): %q %v", id, ok)
	}
	if region, ok := results[1].IdentityAttr("region"); !ok || region != "us-east-1" {
		t.Errorf("IdentityAttr(region): %q %v", region, ok)
	}
	if _, ok := results[0].IdentityAttr("nope"); ok {
		t.Error("IdentityAttr must miss on an attribute the identity does not have")
	}
	if results[0].Resource != cty.NilVal {
		t.Error("no resource object was requested")
	}

	// The caller's configuration must reach the provider untouched.
	if !f.gotReq.Config.RawEquals(config) {
		t.Errorf("config was altered on the way through: %#v", f.gotReq.Config)
	}
	if f.gotReq.IncludeResourceObject {
		t.Error("IncludeResource was false")
	}
	if f.gotReq.TypeName != "test_thing" {
		t.Errorf("TypeName on the request: %q", f.gotReq.TypeName)
	}
}

func TestList_nilConfigUsesSchemaEmpty(t *testing.T) {
	f := &fakeLister{schema: testSchema()}

	_, diags := List(context.Background(), f, "test_thing", cty.NilVal, true)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !f.streamed {
		t.Fatal("the stream should have been opened")
	}
	// cty.NilVal must be resolved into a schema-conforming object before it
	// reaches the wire, not passed along as an untyped nil.
	sent := f.gotReq.Config
	if sent == cty.NilVal {
		t.Fatal("a nil config must be filled in from the schema")
	}
	ts, _ := mustSchemas(t, f).Get("test_thing")
	if !sent.Type().Equals(ts.Config.ImpliedType()) {
		t.Errorf("the filled-in config does not conform to the schema: %#v", sent.Type())
	}
	if !sent.GetAttr("region").IsNull() {
		t.Error("region should be null in an empty config")
	}
	if !f.gotReq.IncludeResourceObject {
		t.Error("IncludeResource should have carried through")
	}
}

func TestList_resourceObject(t *testing.T) {
	resource := cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("vpc-1"),
		"tags": cty.MapVal(map[string]cty.Value{"tofu-estate": cty.StringVal("stateless-e2e")}),
	})
	f := &fakeLister{
		schema: testSchema(),
		events: []providers.ListResourceEvent{
			{Identity: identityVal("vpc-1", "us-east-1"), DisplayName: "first", ResourceObject: resource},
		},
	}

	results, diags := List(context.Background(), f, "test_thing", cty.NilVal, true)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !results[0].Resource.RawEquals(resource) {
		t.Errorf("resource object: %#v", results[0].Resource)
	}
	// Reading a marker off the resource object is what P2.3 will do.
	tags := results[0].Resource.GetAttr("tags")
	if got := tags.Index(cty.StringVal("tofu-estate")).AsString(); got != "stateless-e2e" {
		t.Errorf("tofu-estate tag: %q", got)
	}
}

func TestList_unsupportedType(t *testing.T) {
	t.Run("provider lists other types", func(t *testing.T) {
		f := &fakeLister{schema: testSchema()}
		results, diags := List(context.Background(), f, "test_other", cty.NilVal, false)
		if !diags.HasErrors() {
			t.Fatal("expected an error diagnostic")
		}
		if !hasDiag(diags, "Unlistable resource type") {
			t.Errorf("unexpected diagnostic: %v", diags)
		}
		if len(results) != 0 {
			t.Error("expected no results")
		}
		if f.streamed {
			t.Error("no RPC should have been made for a type the provider cannot list")
		}
	})

	t.Run("provider lists nothing", func(t *testing.T) {
		schema := testSchema()
		schema.ListResourceTypes = nil
		f := &fakeLister{schema: schema}
		_, diags := List(context.Background(), f, "test_thing", cty.NilVal, false)
		if !diags.HasErrors() {
			t.Fatal("expected an error diagnostic")
		}
		if !hasDiag(diags, "offers no list resource types") {
			t.Errorf("unexpected diagnostic: %v", diags)
		}
		if f.streamed {
			t.Error("no RPC should have been made")
		}
	})
}

func TestList_notAProviderHandle(t *testing.T) {
	results, diags := List(context.Background(), notAProvider{}, "test_thing", cty.NilVal, false)
	if !diags.HasErrors() {
		t.Fatal("expected an error diagnostic, not a panic")
	}
	if !hasDiag(diags, "Provider without list protocol support") {
		t.Errorf("unexpected diagnostic: %v", diags)
	}
	if results != nil {
		t.Error("expected no results")
	}
}

func TestList_missingTypeName(t *testing.T) {
	f := &fakeLister{schema: testSchema()}
	_, diags := List(context.Background(), f, "", cty.NilVal, false)
	if !diags.HasErrors() {
		t.Fatal("an empty type name must be rejected")
	}
	if !hasDiag(diags, "Missing resource type to list") {
		t.Errorf("unexpected diagnostic: %v", diags)
	}
}

func TestList_perResultDiagnostics(t *testing.T) {
	var warn tfdiags.Diagnostics
	warn = warn.Append(tfdiags.Sourceless(tfdiags.Warning, "partial read", "some attributes were unavailable"))
	f := &fakeLister{
		schema: testSchema(),
		events: []providers.ListResourceEvent{
			{Identity: identityVal("vpc-1", "us-east-1"), Diagnostics: warn},
			{Identity: identityVal("vpc-2", "us-east-1")},
		},
	}

	results, diags := List(context.Background(), f, "test_thing", cty.NilVal, false)
	if diags.HasErrors() {
		t.Fatalf("a per-result diagnostic is not a call failure: %v", diags.Err())
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if len(results[0].Diagnostics) != 1 {
		t.Errorf("expected the warning to ride on the first result: %v", results[0].Diagnostics)
	}
	if len(results[1].Diagnostics) != 0 {
		t.Errorf("the second result should carry nothing: %v", results[1].Diagnostics)
	}
}

func TestList_streamError(t *testing.T) {
	var boom tfdiags.Diagnostics
	boom = boom.Append(tfdiags.Sourceless(tfdiags.Error, "Provider went away", "connection reset"))
	f := &fakeLister{
		schema:      testSchema(),
		events:      []providers.ListResourceEvent{{Identity: identityVal("vpc-1", "us-east-1")}},
		streamDiags: boom,
	}

	results, diags := List(context.Background(), f, "test_thing", cty.NilVal, false)
	if !diags.HasErrors() {
		t.Fatal("a stream failure must surface")
	}
	// Whatever arrived before the failure is still handed back.
	if len(results) != 1 {
		t.Fatalf("expected the one result that arrived, got %d", len(results))
	}
}

func TestStream_earlyStop(t *testing.T) {
	f := &fakeLister{
		schema: testSchema(),
		events: []providers.ListResourceEvent{
			{Identity: identityVal("vpc-1", "us-east-1")},
			{Identity: identityVal("vpc-2", "us-east-1")},
			{Identity: identityVal("vpc-3", "us-east-1")},
		},
	}

	var seen []string
	diags := Stream(context.Background(), f, Request{TypeName: "test_thing", Limit: 10}, func(r Result) bool {
		id, _ := r.IdentityAttr("id")
		seen = append(seen, id)
		return len(seen) < 2
	})
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if len(seen) != 2 {
		t.Fatalf("stopping early should have ended the stream, saw %v", seen)
	}
	if f.gotReq.Limit != 10 {
		t.Errorf("Limit should reach the provider: %d", f.gotReq.Limit)
	}
}

func TestResult_identityEdgeCases(t *testing.T) {
	t.Run("no identity", func(t *testing.T) {
		r := Result{TypeName: "test_thing"}
		if r.HasIdentity() {
			t.Error("a zero Result has no identity")
		}
		if _, ok := r.IdentityAttr("id"); ok {
			t.Error("IdentityAttr must miss when there is no identity")
		}
	})

	t.Run("null identity", func(t *testing.T) {
		r := Result{Identity: cty.NullVal(cty.Object(map[string]cty.Type{"id": cty.String}))}
		if r.HasIdentity() {
			t.Error("a null identity is not an identity")
		}
		if _, ok := r.IdentityAttr("id"); ok {
			t.Error("IdentityAttr must miss on a null identity")
		}
	})

	t.Run("null attribute", func(t *testing.T) {
		r := Result{Identity: cty.ObjectVal(map[string]cty.Value{"id": cty.NullVal(cty.String)})}
		if !r.HasIdentity() {
			t.Error("the identity itself is present")
		}
		if _, ok := r.IdentityAttr("id"); ok {
			t.Error("a null attribute is not a value")
		}
	})

	t.Run("non-string attribute", func(t *testing.T) {
		r := Result{Identity: cty.ObjectVal(map[string]cty.Value{"port": cty.NumberIntVal(443)})}
		if _, ok := r.IdentityAttr("port"); ok {
			t.Error("IdentityAttr only reads strings")
		}
	})
}

func mustSchemas(t *testing.T, l Lister) Schemas {
	t.Helper()
	s, diags := ListSchemas(context.Background(), l)
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	return s
}

func hasDiag(diags tfdiags.Diagnostics, want string) bool {
	for _, d := range diags {
		desc := d.Description()
		if strings.Contains(desc.Summary, want) || strings.Contains(desc.Detail, want) {
			return true
		}
	}
	return false
}
