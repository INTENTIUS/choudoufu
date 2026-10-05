// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tracing"
)

// GitHub issue #1898: starting a provider is a "Start provider" span, and a
// provider with a launcher is started with that span in its context, which
// is where the provider process's TRACEPARENT comes from.
func TestStartProviderSpan(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	addr := addrs.NewDefaultProvider("test")

	var launchEnv []string
	stop := errors.New("not starting a real provider in this test")
	lib := NewLibraryWithLaunchers(
		ProviderFactories{addr: func() (providers.Interface, error) {
			t.Error("the factory was used although the provider has a launcher")
			return nil, stop
		}},
		ProviderLaunchers{addr: func(ctx context.Context) (providers.Interface, error) {
			launchEnv = tracing.ChildProcessEnv(ctx, []string{"TRACEPARENT=00-11111111111111111111111111111111-2222222222222222-01"})
			return nil, stop
		}},
		nil,
	)

	ctx, root := tracing.Tracer().Start(context.Background(), "Graph walk")
	_, diags := lib.NewProviderManager().NewProvider(ctx, addr)
	root.End()
	if !diags.HasErrors() {
		t.Fatal("want the launcher's error")
	}

	spans := recorder.Ended()
	starts := tracing.SpansNamed(spans, TraceNameStartProvider)
	if len(starts) != 1 {
		t.Fatalf("got %d %q spans, want 1", len(starts), TraceNameStartProvider)
	}
	start := starts[0]
	if p := tracing.ParentOf(spans, start); p == nil || p.Name() != "Graph walk" {
		t.Errorf("parent = %v, want the caller's span", p)
	}
	if v, _ := tracing.SpanAttr(start, "opentofu.provider.address"); v.AsString() != addr.String() {
		t.Errorf("provider address = %q", v.AsString())
	}
	if start.Status().Code.String() != "Error" {
		t.Errorf("status = %v, want Error for a failed start", start.Status().Code)
	}
	want := "TRACEPARENT=00-" + start.SpanContext().TraceID().String() + "-" + start.SpanContext().SpanID().String() + "-01"
	if len(launchEnv) != 1 || !strings.EqualFold(launchEnv[0], want) {
		t.Errorf("provider process env = %v, want [%s]", launchEnv, want)
	}
}

// A provider with only a factory still gets the span.
func TestStartProviderSpan_factoryOnly(t *testing.T) {
	recorder := tracing.NewTestTracer(t)
	addr := addrs.NewDefaultProvider("test")
	called := false
	lib := NewLibrary(ProviderFactories{addr: func() (providers.Interface, error) {
		called = true
		return nil, errors.New("no provider")
	}}, nil)

	lib.NewProviderManager().NewProvider(context.Background(), addr)
	if !called {
		t.Error("the factory was not called")
	}
	if n := len(tracing.SpansNamed(recorder.Ended(), TraceNameStartProvider)); n != 1 {
		t.Errorf("got %d %q spans, want 1", n, TraceNameStartProvider)
	}
}
