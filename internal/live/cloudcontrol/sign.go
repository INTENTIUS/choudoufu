// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// serviceName is Cloud Control's SigV4 service identifier, the segment that
// appears in both the credential scope and the request's host.
const serviceName = "cloudcontrolapi"

// authenticate decides whether req can be signed and either signs it or
// attaches the unsigned region-scope placeholder, matching read-client.ts's
// requestHeaders: signing requires credentials to resolve, and — against an
// endpoint override — requires the caller to have opted in with
// Config.SignEndpointOverride.
//
// The override check comes first and, when it rules signing out, skips
// credential resolution entirely: resolving the default chain can mean
// reading shared config files or asking IMDS, and a floci run has no
// business paying for that on every call just to throw the answer away.
func (c *Client) authenticate(ctx context.Context, req *http.Request, body []byte) error {
	if c.endpoint != "" && !c.signEndpointOverride {
		c.applyRegionScope(req)
		return nil
	}
	creds, resolved := c.resolveCredentials(ctx)
	if !resolved {
		c.applyRegionScope(req)
		return nil
	}
	return c.sign(ctx, req, body, creds)
}

// resolveCredentials retrieves credentials from the configured provider, or
// the default credential chain when none was configured. Any failure to
// resolve — no provider, a provider that errors, empty keys — is reported
// as "nothing resolved" rather than as an error: the caller's request still
// goes out, unsigned, exactly as it would have before signing existed.
func (c *Client) resolveCredentials(ctx context.Context) (aws.Credentials, bool) {
	provider := c.credentials
	if provider == nil {
		provider = c.fallbackCredentials(ctx)
	}
	if provider == nil {
		return aws.Credentials{}, false
	}
	creds, err := provider.Retrieve(ctx)
	if err != nil || creds.AccessKeyID == "" {
		return aws.Credentials{}, false
	}
	return creds, true
}

// sign signs req with SigV4 using creds, hashing body as the payload.
func (c *Client) sign(ctx context.Context, req *http.Request, body []byte, creds aws.Credentials) error {
	hash := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(hash[:])
	signer := v4.NewSigner()
	return signer.SignHTTP(ctx, creds, req, payloadHash, serviceName, c.regionOrDefault(), c.clock())
}

// applyRegionScope sets the unsigned placeholder Authorization header that
// carries the region in its credential scope, the same trick
// read-client.ts's regionScope documents at length: an emulator has one
// host for every region, so an unsigned request still has to say which
// region it is for somewhere, and the credential scope is where a real
// SDK's signature would carry it. This is not a signature — real AWS
// rejects it outright — it only teaches an emulator the region.
//
// It is applied whenever a request goes out unsigned and a region is set,
// matching chant: the override case is the one that matters in practice
// (real AWS unsigned requests are rejected regardless), but the header
// costs nothing to add either way and keeps the two code paths from
// silently drifting apart.
func (c *Client) applyRegionScope(req *http.Request) {
	region := c.region
	if region == "" {
		return
	}
	day := c.clock().UTC().Format("20060102")
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=unsigned/%s/%s/%s/aws4_request, SignedHeaders=host, Signature=unsigned",
		day, region, serviceName,
	))
}

// regionOrDefault is the region to sign for and to host real AWS requests
// against, falling back to defaultRegion when the caller named none — a
// signature needs some region to put in its scope, and this borrows the
// same default baseURL uses so the two agree.
func (c *Client) regionOrDefault() string {
	if c.region != "" {
		return c.region
	}
	return defaultRegion
}
