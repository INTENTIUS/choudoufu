// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugin

import (
	"context"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/intentius/choudoufu/internal/providers"
	proto "github.com/intentius/choudoufu/internal/tfplugin5"
	"github.com/intentius/choudoufu/internal/tracing"
	"github.com/intentius/choudoufu/internal/tracing/traceattrs"
)

// GitHub issue #1898: a provider call is a span under the caller's span, and
// the provider receives that span as its gRPC "traceparent".
func TestGRPCProvider_ReadResourceSpan(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	tracing.SetDetailForTest(t, tracing.DetailFull, 0)

	client := mockProviderClient(t)
	p := newGRPCProvider(client)
	p.Addr = "registry.opentofu.org/hashicorp/test"

	var gotTraceparent string
	client.EXPECT().ReadResource(
		gomock.Any(),
		gomock.Any(),
	).DoAndReturn(func(ctx context.Context, _ *proto.ReadResource_Request, _ ...grpc.CallOption) (*proto.ReadResource_Response, error) {
		md, _ := metadata.FromOutgoingContext(ctx)
		if v := md.Get("traceparent"); len(v) == 1 {
			gotTraceparent = v[0]
		}
		return &proto.ReadResource_Response{
			NewState: &proto.DynamicValue{Msgpack: []byte("\x81\xa4attr\xa3bar")},
		}, nil
	})

	ctx, parent := tracing.Tracer().Start(t.Context(), "Plan resource instance changes")
	resp := p.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:   "resource",
		PriorState: cty.ObjectVal(map[string]cty.Value{"attr": cty.StringVal("secret-value")}),
	})
	parent.End()
	checkDiags(t, resp.Diagnostics)

	spans := recorder.Ended()
	calls := tracing.SpansNamed(spans, "tfplugin5.Provider/ReadResource")
	if len(calls) != 1 {
		t.Fatalf("got %d ReadResource spans, want 1", len(calls))
	}
	call := calls[0]
	if p := tracing.ParentOf(spans, call); p == nil || p.Name() != "Plan resource instance changes" {
		t.Errorf("ReadResource span parent = %v, want the caller's span", p)
	}
	if !strings.Contains(gotTraceparent, call.SpanContext().SpanID().String()) {
		t.Errorf("provider got traceparent %q, want the call span %s", gotTraceparent, call.SpanContext().SpanID())
	}
	for key, want := range map[string]string{
		traceattrs.AttrRPCMethod:    "ReadResource",
		traceattrs.AttrRPCService:   "tfplugin5.Provider",
		traceattrs.AttrResourceType: "resource",
		"opentofu.provider.address": "registry.opentofu.org/hashicorp/test",
	} {
		if v, ok := tracing.SpanAttr(call, key); !ok || v.AsString() != want {
			t.Errorf("%s = %q, want %q", key, v.AsString(), want)
		}
	}
	for _, kv := range call.Attributes() {
		if strings.Contains(kv.Value.Emit(), "secret-value") {
			t.Errorf("attribute %s carries a resource attribute value", kv.Key)
		}
	}
}

// In aggregate mode inside a walk the call is counted, not emitted, and the
// provider gets the enclosing span as its parent instead.
func TestGRPCProvider_ReadResourceAggregate(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	tracing.SetDetailForTest(t, tracing.DetailAggregate, 0)

	client := mockProviderClient(t)
	p := newGRPCProvider(client)
	client.EXPECT().ReadResource(gomock.Any(), gomock.Any()).Return(&proto.ReadResource_Response{
		NewState: &proto.DynamicValue{Msgpack: []byte("\x81\xa4attr\xa3bar")},
	}, nil)

	ctx, walk := tracing.Tracer().Start(t.Context(), "Graph walk")
	ctx, rec := tracing.WithDetailRecorder(ctx)
	p.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:   "resource",
		PriorState: cty.ObjectVal(map[string]cty.Value{"attr": cty.StringVal("foo")}),
	})
	rec.Emit(ctx)
	walk.End()

	spans := recorder.Ended()
	if n := len(tracing.SpansNamed(spans, "tfplugin5.Provider/ReadResource")); n != 0 {
		t.Errorf("aggregate mode emitted %d ReadResource spans", n)
	}
	sums := tracing.SpansNamed(spans, tracing.AggregateSpanPrefix+"tfplugin5.Provider/ReadResource")
	if len(sums) != 1 {
		t.Fatalf("got %d summary spans, want 1", len(sums))
	}
	if v, _ := tracing.SpanAttr(sums[0], traceattrs.AttrAggregateCount); v.AsInt64() != 1 {
		t.Errorf("count = %d, want 1", v.AsInt64())
	}
}
