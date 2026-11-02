// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package sweepconfig

import (
	"context"

	"github.com/intentius/choudoufu/pkg/gcpsweep"
)

// GCPSweep builds the GCP estate sweep's client from a provider block's
// arguments: the provider's credential through [gcpsweep.HTTPClient],
// which keeps an emulator run's tokens local, and choudoufu's markers.
// Nothing is called until a search, except that a credentials file is read
// and Application Default Credentials are looked up now when the block's
// endpoints are not local, so a missing credential fails here rather than
// per project.
func GCPSweep(ctx context.Context, a gcpsweep.Attrs) (*gcpsweep.Client, error) {
	hc, creds, err := gcpsweep.HTTPClient(ctx, a)
	if err != nil {
		return nil, err
	}
	return gcpsweep.New(ctx, a, gcpsweep.Config{Markers: Markers, HTTPClient: hc, Credentials: creds})
}
