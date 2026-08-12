// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugin6

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/msgpack"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	mockproto "github.com/intentius/choudoufu/internal/plugin6/mock_proto"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	proto "github.com/intentius/choudoufu/internal/tfplugin6"
)

// fakeListStream is a canned ListResource stream: it hands back events in
// order and then finishes with finalErr, or io.EOF when finalErr is nil.
type fakeListStream struct {
	grpc.ClientStream
	events   []*proto.ListResource_Event
	finalErr error
	next     int
}

func (s *fakeListStream) Recv() (*proto.ListResource_Event, error) {
	if s.next < len(s.events) {
		ev := s.events[s.next]
		s.next++
		return ev, nil
	}
	if s.finalErr != nil {
		return nil, s.finalErr
	}
	return nil, io.EOF
}

// listProtoSchema is a provider that can list one type, "test_thing", and
// serves an identity schema for it.
func listProtoSchema() *proto.GetProviderSchema_Response {
	return &proto.GetProviderSchema_Response{
		Provider: &proto.Schema{
			Block: &proto.Schema_Block{},
		},
		ResourceSchemas: map[string]*proto.Schema{
			"test_thing": {
				Version: 1,
				Block: &proto.Schema_Block{
					Attributes: []*proto.Schema_Attribute{
						{Name: "id", Type: []byte(`"string"`), Computed: true},
						{Name: "name", Type: []byte(`"string"`), Optional: true},
					},
				},
			},
		},
		ListResourceSchemas: map[string]*proto.Schema{
			"test_thing": {
				Block: &proto.Schema_Block{
					Attributes: []*proto.Schema_Attribute{
						{Name: "region", Type: []byte(`"string"`), Optional: true},
					},
				},
			},
		},
	}
}

func listIdentitySchemas() *proto.GetResourceIdentitySchemas_Response {
	return &proto.GetResourceIdentitySchemas_Response{
		IdentitySchemas: map[string]*proto.ResourceIdentitySchema{
			"test_thing": {
				Version: 2,
				IdentityAttributes: []*proto.ResourceIdentitySchema_IdentityAttribute{
					{Name: "id", Type: []byte(`"string"`), RequiredForImport: true},
					{Name: "region", Type: []byte(`"string"`), OptionalForImport: true},
				},
			},
		},
	}
}

// newListProvider wires a GRPCProvider onto a mock client serving the given
// schema and identity schemas.
func newListProvider(t *testing.T, schema *proto.GetProviderSchema_Response, ids *proto.GetResourceIdentitySchemas_Response) (*GRPCProvider, *mockproto.MockProviderClient) {
	t.Helper()
	ctrl := gomock.NewController(t)
	client := mockproto.NewMockProviderClient(ctrl)
	client.EXPECT().GetProviderSchema(gomock.Any(), gomock.Any(), gomock.Any()).Return(schema, nil).AnyTimes()
	client.EXPECT().GetResourceIdentitySchemas(gomock.Any(), gomock.Any(), gomock.Any()).Return(ids, nil).AnyTimes()
	return newGRPCProvider(client), client
}

// identityValue msgpack-encodes an identity object the way a provider would.
func identityValue(t *testing.T, id, region string) *proto.ResourceIdentityData {
	t.Helper()
	ty := cty.Object(map[string]cty.Type{"id": cty.String, "region": cty.String})
	mp, err := msgpack.Marshal(cty.ObjectVal(map[string]cty.Value{
		"id":     cty.StringVal(id),
		"region": cty.StringVal(region),
	}), ty)
	if err != nil {
		t.Fatal(err)
	}
	return &proto.ResourceIdentityData{IdentityData: &proto.DynamicValue{Msgpack: mp}}
}

func TestGRPCProviderV6_getProviderSchema_listResourceTypes(t *testing.T) {
	p, _ := newListProvider(t, listProtoSchema(), listIdentitySchemas())

	resp := p.GetProviderSchema(context.Background())
	checkDiags(t, resp.Diagnostics)

	listSchema, ok := resp.ListResourceTypes["test_thing"]
	if !ok {
		t.Fatalf("test_thing missing from ListResourceTypes; got %v", resp.ListResourceTypes)
	}
	if _, ok := listSchema.Block.Attributes["region"]; !ok {
		t.Fatalf("list config schema lost its region attribute: %#v", listSchema.Block)
	}
	// The list schema must not be confused with the managed resource
	// schema, which has different attributes.
	if _, ok := listSchema.Block.Attributes["name"]; ok {
		t.Fatal("list config schema picked up the managed resource schema's attributes")
	}
	// A provider that lists nothing must yield an empty map, not a nil one,
	// and no error.
	noList := listProtoSchema()
	noList.ListResourceSchemas = nil
	p2, _ := newListProvider(t, noList, listIdentitySchemas())
	resp2 := p2.GetProviderSchema(context.Background())
	checkDiags(t, resp2.Diagnostics)
	if resp2.ListResourceTypes == nil {
		t.Fatal("ListResourceTypes should be an empty map, not nil")
	}
	if len(resp2.ListResourceTypes) != 0 {
		t.Fatalf("expected no listable types, got %v", resp2.ListResourceTypes)
	}
}

func TestGRPCProviderV6_ListResource(t *testing.T) {
	p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())

	var gotReq *proto.ListResource_Request
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *proto.ListResource_Request, _ ...grpc.CallOption) (proto.Provider_ListResourceClient, error) {
			gotReq = req
			return &fakeListStream{events: []*proto.ListResource_Event{
				{Identity: identityValue(t, "thing-1", "us-east-1"), DisplayName: "first"},
				{Identity: identityValue(t, "thing-2", "us-east-1"), DisplayName: "second"},
			}}, nil
		})

	configTy := cty.Object(map[string]cty.Type{"region": cty.String})
	resp := p.ListResource(context.Background(), providers.ListResourceRequest{
		TypeName: "test_thing",
		Config:   cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal("us-east-1")}),
		Limit:    7,
	})
	checkDiags(t, resp.Diagnostics)

	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if got := resp.Events[0].DisplayName; got != "first" {
		t.Errorf("display name: got %q", got)
	}
	// The identity comes back as an object of the schema's type, decoded
	// from the wire: comparing the whole value covers its type too, since
	// valueComparer is cty.Value.RawEquals.
	want := cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("thing-1"), "region": cty.StringVal("us-east-1")})
	if diff := cmp.Diff(want, resp.Events[0].Identity, typeComparer, valueComparer, equateEmpty); diff != "" {
		t.Errorf("wrong identity\n%s", diff)
	}
	if resp.Events[0].ResourceObject != cty.NilVal {
		t.Errorf("resource object should be absent when not requested, got %#v", resp.Events[0].ResourceObject)
	}

	// The request must carry the caller's config and limit through to the
	// wire unchanged.
	if gotReq.TypeName != "test_thing" {
		t.Errorf("type name on the wire: %q", gotReq.TypeName)
	}
	if gotReq.Limit != 7 {
		t.Errorf("limit on the wire: %d", gotReq.Limit)
	}
	if gotReq.IncludeResourceObject {
		t.Error("include_resource_object should be false")
	}
	sentConfig, err := msgpack.Unmarshal(gotReq.Config.Msgpack, configTy)
	if err != nil {
		t.Fatal(err)
	}
	if got := sentConfig.GetAttr("region").AsString(); got != "us-east-1" {
		t.Errorf("config on the wire: region = %q", got)
	}
}

func TestGRPCProviderV6_ListResource_includeResourceObject(t *testing.T) {
	p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())

	resTy := cty.Object(map[string]cty.Type{"id": cty.String, "name": cty.String})
	objMP, err := msgpack.Marshal(cty.ObjectVal(map[string]cty.Value{
		"id":   cty.StringVal("thing-1"),
		"name": cty.StringVal("a thing"),
	}), resTy)
	if err != nil {
		t.Fatal(err)
	}

	var gotReq *proto.ListResource_Request
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req *proto.ListResource_Request, _ ...grpc.CallOption) (proto.Provider_ListResourceClient, error) {
			gotReq = req
			return &fakeListStream{events: []*proto.ListResource_Event{
				{
					Identity:       identityValue(t, "thing-1", "us-east-1"),
					DisplayName:    "first",
					ResourceObject: &proto.DynamicValue{Msgpack: objMP},
				},
			}}, nil
		})

	resp := p.ListResource(context.Background(), providers.ListResourceRequest{
		TypeName:              "test_thing",
		Config:                cty.NullVal(cty.Object(map[string]cty.Type{"region": cty.String})),
		IncludeResourceObject: true,
	})
	checkDiags(t, resp.Diagnostics)

	if !gotReq.IncludeResourceObject {
		t.Error("include_resource_object should be true on the wire")
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	obj := resp.Events[0].ResourceObject
	if obj == cty.NilVal {
		t.Fatal("resource object was not decoded")
	}
	if got := obj.GetAttr("name").AsString(); got != "a thing" {
		t.Errorf("resource object name: %q", got)
	}
}

func TestGRPCProviderV6_ListResource_unimplemented(t *testing.T) {
	unimplemented := status.Error(codes.Unimplemented, "unknown method ListResource")

	t.Run("at stream open", func(t *testing.T) {
		p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())
		client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, unimplemented)

		resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
		assertListNotSupported(t, resp, "does not implement the list protocol")
	})

	t.Run("on first recv", func(t *testing.T) {
		// A gRPC server without the handler registered accepts the stream
		// and reports Unimplemented only when the client reads.
		p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())
		client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(
			&fakeListStream{finalErr: unimplemented}, nil)

		resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
		assertListNotSupported(t, resp, "does not implement the list protocol")
	})
}

func TestGRPCProviderV6_ListResource_typeNotListable(t *testing.T) {
	t.Run("provider lists other types", func(t *testing.T) {
		p, _ := newListProvider(t, listProtoSchema(), listIdentitySchemas())
		// No ListResource expectation: reaching the RPC at all is a failure.
		resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_other"})
		assertListNotSupported(t, resp, "cannot list this resource type")
	})

	t.Run("provider lists nothing", func(t *testing.T) {
		schema := listProtoSchema()
		schema.ListResourceSchemas = nil
		p, _ := newListProvider(t, schema, listIdentitySchemas())
		resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
		assertListNotSupported(t, resp, "offers no list resource types")
	})
}

func TestGRPCProviderV6_ListResource_transportError(t *testing.T) {
	p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&fakeListStream{
			events:   []*proto.ListResource_Event{{Identity: identityValue(t, "thing-1", "us-east-1")}},
			finalErr: status.Error(codes.Unavailable, "provider went away"),
		}, nil)

	resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
	checkDiagsHasError(t, resp.Diagnostics)
	// Results delivered before the failure are still returned; a partial
	// list plus an error is more useful than nothing plus an error.
	if len(resp.Events) != 1 {
		t.Fatalf("expected the one event that did arrive, got %d", len(resp.Events))
	}
}

func TestGRPCProviderV6_ListResource_perEventDiagnostics(t *testing.T) {
	p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&fakeListStream{events: []*proto.ListResource_Event{
			{
				Identity:    identityValue(t, "thing-1", "us-east-1"),
				DisplayName: "first",
				Diagnostic: []*proto.Diagnostic{{
					Severity: proto.Diagnostic_WARNING,
					Summary:  "partial read",
					Detail:   "some attributes were unavailable",
				}},
			},
			{Identity: identityValue(t, "thing-2", "us-east-1"), DisplayName: "second"},
		}}, nil)

	resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
	// A per-result diagnostic must not become a call-level diagnostic: the
	// rest of the stream is still valid.
	checkDiags(t, resp.Diagnostics)
	if len(resp.Diagnostics) != 0 {
		t.Fatalf("per-event diagnostics leaked to the call level: %v", resp.Diagnostics)
	}
	if len(resp.Events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(resp.Events))
	}
	if len(resp.Events[0].Diagnostics) != 1 {
		t.Fatalf("expected the warning on the first event, got %v", resp.Events[0].Diagnostics)
	}
	if len(resp.Events[1].Diagnostics) != 0 {
		t.Fatalf("second event should carry no diagnostics, got %v", resp.Events[1].Diagnostics)
	}
}

func TestGRPCProviderV6_ListResource_identityWithoutSchema(t *testing.T) {
	// A provider that lists a type but serves no identity schema for it
	// must not crash the client; the identity is dropped with a warning.
	p, client := newListProvider(t, listProtoSchema(), &proto.GetResourceIdentitySchemas_Response{
		IdentitySchemas: map[string]*proto.ResourceIdentitySchema{},
	})
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&fakeListStream{events: []*proto.ListResource_Event{
			{Identity: identityValue(t, "thing-1", "us-east-1"), DisplayName: "first"},
		}}, nil)

	resp := p.ListResource(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"})
	if resp.Diagnostics.HasErrors() {
		t.Fatalf("a missing identity schema is not an error: %v", resp.Diagnostics.Err())
	}
	if len(resp.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(resp.Events))
	}
	if resp.Events[0].Identity != cty.NilVal {
		t.Errorf("identity should have been dropped, got %#v", resp.Events[0].Identity)
	}
	if !hasDiagContaining(resp.Diagnostics, "could not be decoded") {
		t.Errorf("expected a warning about the undecodable identity, got %v", resp.Diagnostics)
	}
}

func TestGRPCProviderV6_ListResourceStream_earlyStop(t *testing.T) {
	p, client := newListProvider(t, listProtoSchema(), listIdentitySchemas())
	client.EXPECT().ListResource(gomock.Any(), gomock.Any(), gomock.Any()).Return(
		&fakeListStream{events: []*proto.ListResource_Event{
			{Identity: identityValue(t, "thing-1", "us-east-1")},
			{Identity: identityValue(t, "thing-2", "us-east-1")},
			{Identity: identityValue(t, "thing-3", "us-east-1")},
		}}, nil)

	seen := 0
	diags := p.ListResourceStream(context.Background(), providers.ListResourceRequest{TypeName: "test_thing"},
		func(providers.ListResourceEvent) bool {
			seen++
			return seen < 2
		})
	checkDiags(t, diags)
	if seen != 2 {
		t.Fatalf("stream should have stopped after 2 results, saw %d", seen)
	}
}

func assertListNotSupported(t *testing.T, resp providers.ListResourceResponse, want string) {
	t.Helper()
	checkDiagsHasError(t, resp.Diagnostics)
	if len(resp.Events) != 0 {
		t.Fatalf("expected no results, got %d", len(resp.Events))
	}
	if !hasDiagContaining(resp.Diagnostics, want) {
		t.Fatalf("expected a diagnostic containing %q, got: %v", want, resp.Diagnostics)
	}
}

// hasDiagContaining looks for want in any diagnostic's summary or detail,
// including warnings, which never reach Diagnostics.Err().
func hasDiagContaining(diags tfdiags.Diagnostics, want string) bool {
	for _, d := range diags {
		desc := d.Description()
		if strings.Contains(desc.Summary, want) || strings.Contains(desc.Detail, want) {
			return true
		}
	}
	return false
}
