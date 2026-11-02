// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestUnsignedRegionScopeAgainstOverride(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	fixedNow := time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	c := New(Config{
		Endpoint: server.URL,
		Region:   "eu-west-2",
		// Credentials left nil on purpose: an endpoint override without
		// SignEndpointOverride stays unsigned regardless of what a
		// credential provider would resolve, so this never touches the
		// default chain (see authenticate's override check).
		Now: func() time.Time { return fixedNow },
	})

	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if gotAuth == "" {
		t.Fatal("no Authorization header was sent")
	}
	if !strings.Contains(gotAuth, "AWS4-HMAC-SHA256") {
		t.Errorf("Authorization = %q, want it to start with AWS4-HMAC-SHA256", gotAuth)
	}
	if !strings.Contains(gotAuth, "/20260304/eu-west-2/cloudcontrolapi/aws4_request") {
		t.Errorf("Authorization = %q, want the credential scope to carry 20260304/eu-west-2/cloudcontrolapi/aws4_request", gotAuth)
	}
	if !strings.Contains(gotAuth, "Signature=unsigned") {
		t.Errorf("Authorization = %q, want a placeholder Signature=unsigned, not a real signature", gotAuth)
	}
}

func TestNoRegionScopeHeaderWithoutRegion(t *testing.T) {
	var gotAuth string
	sawAuthHeader := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, sawAuthHeader = r.Header.Get("Authorization"), r.Header.Get("Authorization") != ""
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	c := New(Config{Endpoint: server.URL})
	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if sawAuthHeader {
		t.Errorf("Authorization = %q, want none when no region is configured", gotAuth)
	}
}

func TestSignedRequestSmoke(t *testing.T) {
	var gotAuth, gotDate string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotDate = r.Header.Get("X-Amz-Date")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	fixedNow := time.Date(2026, 3, 4, 12, 30, 0, 0, time.UTC)
	c := New(Config{
		Endpoint:             server.URL,
		Region:               "us-west-2",
		SignEndpointOverride: true, // this endpoint override should be signed
		Credentials:          credentials.NewStaticCredentialsProvider("AKIAEXAMPLE", "secretexample", ""),
		Now:                  func() time.Time { return fixedNow },
	})

	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}

	if gotDate != "20260304T123000Z" {
		t.Errorf("X-Amz-Date = %q, want 20260304T123000Z", gotDate)
	}

	// Assert the Authorization shape per AWS's SigV4 spec, not the exact
	// signature: AWS4-HMAC-SHA256 Credential=<key>/<scope>, SignedHeaders=...,
	// Signature=<64 hex chars>.
	want := regexp.MustCompile(
		`^AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20260304/us-west-2/cloudcontrolapi/aws4_request, ` +
			`SignedHeaders=[a-z0-9;-]+, Signature=[0-9a-f]{64}$`,
	)
	if !want.MatchString(gotAuth) {
		t.Errorf("Authorization = %q, does not match the expected SigV4 shape", gotAuth)
	}
	if !strings.Contains(gotAuth, "host") {
		t.Errorf("Authorization = %q, want SignedHeaders to include host", gotAuth)
	}
}

func TestNoSigningAgainstOverrideByDefault(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	c := New(Config{
		Endpoint:    server.URL,
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKIAEXAMPLE", "secretexample", ""),
		// SignEndpointOverride left false: even with credentials configured,
		// an override should stay unsigned unless the caller opts in.
	})

	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if !strings.Contains(gotAuth, "Signature=unsigned") {
		t.Errorf("Authorization = %q, want the unsigned placeholder since SignEndpointOverride is false", gotAuth)
	}
}

// errCredentials always fails to resolve, simulating an environment with no
// usable credentials anywhere in the chain.
type errCredentials struct{}

func (errCredentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{}, errors.New("no credentials available")
}

func TestUnsignedWhenCredentialsProviderErrors(t *testing.T) {
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
	}))
	defer server.Close()

	c := New(Config{
		Endpoint: server.URL,
		Region:   "us-east-1",
		// SignEndpointOverride is set so authenticate does not short-circuit
		// on the override alone — this test wants to reach
		// resolveCredentials and exercise its error path specifically.
		SignEndpointOverride: true,
		Credentials:          errCredentials{},
	})

	if _, err := c.ListResources(context.Background(), "AWS::EC2::Instance"); err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if !strings.Contains(gotAuth, "Signature=unsigned") {
		t.Errorf("Authorization = %q, want the unsigned placeholder when the credentials provider errors", gotAuth)
	}
}
