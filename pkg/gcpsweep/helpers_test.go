// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package gcpsweep

import (
	"context"
	"net/http"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// testKeys is choudoufu's vocabulary, which every fixture here is labelled
// with.
var testKeys = markerkeys.Keys{Owner: "tofu-estate", Address: "tofu-address"}

// testSensitive marks a value the way a caller's sensitive mark would;
// the package reads only whether a value is marked, never which mark.
const testSensitive = "sensitive"

// newProviderClient is a [Client] built the way choudoufu builds one: the
// provider's credential rule ([HTTPClient]) and choudoufu's vocabulary.
func newProviderClient(ctx context.Context, a Attrs) (*Client, error) {
	return newClientWith(ctx, a, func(ctx context.Context) (*http.Client, Credentials, error) {
		return HTTPClient(ctx, a)
	})
}

// newClientWith is a [Client] over the HTTP client build makes.
func newClientWith(ctx context.Context, a Attrs, build func(context.Context) (*http.Client, Credentials, error)) (*Client, error) {
	hc, creds, err := build(ctx)
	if err != nil {
		return nil, err
	}
	return New(ctx, a, Config{Markers: testKeys, HTTPClient: hc, Credentials: creds})
}
