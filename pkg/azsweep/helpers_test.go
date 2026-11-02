// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package azsweep

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/intentius/choudoufu/pkg/markerkeys"
)

// testKeys is choudoufu's vocabulary, which every fixture here is tagged
// with.
var testKeys = markerkeys.Keys{Owner: "tofu-estate", Address: "tofu-address"}

// testSensitive marks a value the way a caller's sensitive mark would;
// the package reads only whether a value is marked, never which mark.
const testSensitive = "sensitive"

func newWith(ep Endpoints, cred azcore.TokenCredential, hc *http.Client) *Client {
	c, err := New(Config{Markers: testKeys, Endpoints: ep, Credential: cred, HTTPClient: hc})
	if err != nil {
		panic(err)
	}
	return c
}

func newTaggerWith(ep Endpoints, cred azcore.TokenCredential, hc *http.Client) *Tagger {
	return NewTagger(Config{Endpoints: ep, Credential: cred, HTTPClient: hc})
}

func newKeyVaultWith(cred azcore.TokenCredential, hc *http.Client) *KeyVaultClient {
	return NewKeyVault(Config{Credential: cred, HTTPClient: hc})
}
