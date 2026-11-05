// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"testing"

	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// listingFake is a fake provider that also speaks the list protocol, as
// the plugin-backed gRPC providers do.
type listingFake struct {
	fakeProvider
	listed []string
}

func (f *listingFake) ListResourceStream(_ context.Context, req providers.ListResourceRequest, _ func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	f.listed = append(f.listed, req.TypeName)
	return nil
}

// A wrapped provider must keep the list capability its inner provider has:
// greenfield apply lists through listclient.Lister, and losing it failed
// every greenfield apply with "Provider without list protocol support".
func TestWrapKeepsListCapability(t *testing.T) {
	byType := map[string][]Override{"aws_s3_bucket": {{Name: "x", Type: "aws_s3_bucket"}}}

	fake := &listingFake{}
	l, ok := Wrap(fake, byType).(listclient.Lister)
	if !ok {
		t.Fatal("wrapping a provider that can list hid its list capability")
	}
	l.ListResourceStream(context.Background(), providers.ListResourceRequest{TypeName: "aws_s3_bucket"}, func(providers.ListResourceEvent) bool { return true })
	if len(fake.listed) != 1 || fake.listed[0] != "aws_s3_bucket" {
		t.Fatalf("list call did not reach the inner provider: %v", fake.listed)
	}

	if _, ok := Wrap(&fakeProvider{}, byType).(listclient.Lister); ok {
		t.Fatal("wrapping a provider that cannot list made it claim it can")
	}
}
