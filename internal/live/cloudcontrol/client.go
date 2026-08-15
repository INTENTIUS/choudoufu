// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// defaultRegion is what a Client signs for and hosts requests against when
// neither Config.Region nor a call site names one.
const defaultRegion = "us-east-1"

// targetPrefix is the X-Amz-Target namespace Cloud Control's two operations
// (ListResources, GetResource) live under. [New] is the only constructor
// that uses it; [NewTagging] (tagging.go) configures a different one for the
// Resource Groups Tagging API's GetResources.
const targetPrefix = "CloudApiService"

// Config configures a [Client]. The zero value is a client for real AWS in
// defaultRegion, unsigned unless the environment's default credential chain
// resolves something.
type Config struct {
	// Endpoint overrides the real AWS host, e.g. floci's
	// "http://localhost:4566". Empty means real AWS.
	Endpoint string

	// Region selects the real-AWS host and the SigV4 credential scope.
	// Empty means defaultRegion.
	Region string

	// Credentials is what to sign with. Nil defers to aws-sdk-go-v2's
	// default credential chain (environment, shared config, IMDS, ...),
	// resolved lazily on first use. When nothing resolves, requests go out
	// unsigned.
	Credentials aws.CredentialsProvider

	// SignEndpointOverride signs even when Endpoint is set, for an override
	// that is itself real AWS — a VPC endpoint, a signing proxy. Floci does
	// not verify signatures, so the default (false) is what every local run
	// wants: unsigned requests against the emulator, no credentials
	// required to read back what was just written.
	SignEndpointOverride bool

	// RoundTripper is what requests are sent over. Defaults to
	// http.DefaultTransport.
	RoundTripper http.RoundTripper

	// HTTPTimeout bounds one HTTP attempt end to end - dial, send, response
	// body. Zero means defaultHTTPTimeout (30s). Without a bound, one
	// unresponsive host parks a discovery scan indefinitely: the ctx most
	// callers pass has no deadline of its own.
	HTTPTimeout time.Duration

	// Now is the signing clock. Defaults to time.Now; tests inject it for a
	// reproducible signature and a reproducible region-scope placeholder.
	Now func() time.Time

	// MaxAttempts bounds how many times one call may attempt the request
	// before giving up, counting the first try. Only a ThrottlingException
	// response triggers a retry at all (see doc.go's "Retries" section);
	// every other failure - including a different *APIError code, a
	// transport error, a malformed response - returns to the caller after
	// the first attempt. Zero means defaultMaxAttempts (5).
	MaxAttempts int

	// RetryBaseDelay is the backoff curve's starting point: the first
	// retry's delay is uniformly random between 0 and this value, doubling
	// (still full-jitter) on each attempt after that, up to RetryMaxDelay.
	// Zero means defaultRetryBaseDelay (200ms).
	RetryBaseDelay time.Duration

	// RetryMaxDelay caps any single retry's sleep. Zero means
	// defaultRetryMaxDelay (5s).
	RetryMaxDelay time.Duration

	// RetrySleep overrides the function a retry waits with between
	// attempts - a test's hook for a deterministic, instant backoff curve
	// instead of a real sleep. Nil means retrySleep, which respects ctx
	// cancellation.
	RetrySleep func(ctx context.Context, d time.Duration) error
}

// Client is a Cloud Control API client: two operations (ListResources,
// GetResource), each a POST of AWS JSON 1.0 against Cloud Control's
// endpoint, signed with SigV4 when credentials resolve. See the package doc
// for the signing and endpoint-override rules.
type Client struct {
	endpoint             string
	region               string
	credentials          aws.CredentialsProvider
	signEndpointOverride bool
	httpClient           *http.Client
	now                  func() time.Time

	// The retry policy this Client applies to every call - see doc.go's
	// "Retries" section. retrySleepFn is nil unless Config.RetrySleep set
	// it, in which case call() uses it instead of the package-level
	// retrySleep.
	maxAttempts    int
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
	retrySleepFn   func(ctx context.Context, d time.Duration) error

	// service is the SigV4 service identifier and host subdomain
	// (<service>.<region>.amazonaws.com), and opTargetPrefix is the
	// X-Amz-Target namespace requests carry. [New] sets Cloud Control's own
	// values; [NewTagging] overrides both for the Resource Groups Tagging
	// API, which speaks the same AWS JSON RPC shape against a different
	// service and a different protocol version - see contentType.
	service        string
	opTargetPrefix string

	// contentType is the Content-Type header every request carries:
	// "application/x-amz-json-1.0" for Cloud Control, "1.1" for the
	// Resource Groups Tagging API. The two AWS JSON RPC protocol versions
	// differ only in this header, but an emulator that inspects it to route
	// the request - floci does - refuses the call outright when it does
	// not match the service's real one, X-Amz-Target notwithstanding
	// (verified against floci 1.5.33: a GetResources call with "1.0" comes
	// back UnknownOperationException; "1.1", the value botocore's own
	// resourcegroupstaggingapi model sends, succeeds). [New] sets Cloud
	// Control's "1.0"; [NewTagging] overrides it.
	contentType string

	fallbackOnce  sync.Once
	fallbackCreds aws.CredentialsProvider
}

// New builds a Client from cfg, configured for Cloud Control's own two
// operations (ListResources, GetResource).
func New(cfg Config) *Client {
	c := newClient(cfg)
	c.service = serviceName
	c.opTargetPrefix = targetPrefix
	c.contentType = "application/x-amz-json-1.0"
	return c
}

// newClient builds the common parts every constructor in this package
// shares; the caller sets service, opTargetPrefix and contentType.
func newClient(cfg Config) *Client {
	transport := cfg.RoundTripper
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &Client{
		endpoint:             strings.TrimSuffix(cfg.Endpoint, "/"),
		region:               cfg.Region,
		credentials:          cfg.Credentials,
		signEndpointOverride: cfg.SignEndpointOverride,
		httpClient:           &http.Client{Transport: transport, Timeout: positiveDurationOr(cfg.HTTPTimeout, defaultHTTPTimeout)},
		now:                  cfg.Now,
		maxAttempts:          positiveIntOr(cfg.MaxAttempts, defaultMaxAttempts),
		retryBaseDelay:       positiveDurationOr(cfg.RetryBaseDelay, defaultRetryBaseDelay),
		retryMaxDelay:        positiveDurationOr(cfg.RetryMaxDelay, defaultRetryMaxDelay),
		retrySleepFn:         cfg.RetrySleep,
	}
}

// positiveIntOr returns v when it is positive, and fallback otherwise - the
// "zero means the default" rule every retry-policy Config field follows.
func positiveIntOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}

// positiveDurationOr is positiveIntOr for time.Duration fields.
func positiveDurationOr(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

// clock is the signing time: cfg.Now when the caller set one, time.Now
// otherwise.
func (c *Client) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// fallbackCredentials loads the default credential chain on first use and
// caches the result (which may be nil, when loading it failed) for the
// life of the Client. It is only ever reached when Config.Credentials was
// nil, so a caller — including every test in this package — that supplies
// its own provider never pays for it and never touches the network this
// can involve (shared config files, IMDS).
func (c *Client) fallbackCredentials(ctx context.Context) aws.CredentialsProvider {
	c.fallbackOnce.Do(func() {
		cfg, err := config.LoadDefaultConfig(ctx)
		if err != nil {
			return
		}
		c.fallbackCreds = cfg.Credentials
	})
	return c.fallbackCreds
}

// JoinIdentifier joins a multi-part resource identifier with Cloud
// Control's own separator, "|" — for example the two-part key an
// aws_route_table_association needs: JoinIdentifier(routeTableID, subnetID).
func JoinIdentifier(parts ...string) string {
	return strings.Join(parts, "|")
}

// baseURL is the endpoint every request in this package POSTs to.
func (c *Client) baseURL() string {
	if c.endpoint != "" {
		return c.endpoint + "/"
	}
	region := c.region
	if region == "" {
		region = defaultRegion
	}
	return fmt.Sprintf("https://%s.%s.amazonaws.com/", c.service, region)
}

// ResourceDescription is one live resource as Cloud Control describes it.
type ResourceDescription struct {
	// Identifier is the resource's Cloud Control identifier. A multi-part
	// identifier arrives already joined with "|"; see [JoinIdentifier] for
	// building one to send.
	Identifier string
	// Properties is the resource's model, decoded from the JSON string
	// Cloud Control wraps it in. Nil when the response carried no
	// Properties at all, which is not an error — see
	// wireResourceDescription.decode for the cases this client does treat
	// as one.
	Properties map[string]any
}

// wireResourceDescription is one ResourceDescriptions entry (or
// ResourceDescription) exactly as Cloud Control's JSON shapes it:
// Properties travels as a JSON string, not a nested object.
type wireResourceDescription struct {
	Identifier string `json:"Identifier"`
	Properties string `json:"Properties"`
}

// decode turns the wire shape into the public one. A missing or empty
// Properties string decodes to a nil map — Cloud Control sends that for
// resource types with nothing to report, and it is not this client's place
// to call that malformed. A non-empty Properties string that fails to
// parse as a JSON object is different: that is a response this client
// cannot make sense of, and it is reported as an error rather than silently
// discarded, so a caller finds out rather than working from a resource
// that is quietly missing every property.
func (w wireResourceDescription) decode(op string) (ResourceDescription, error) {
	desc := ResourceDescription{Identifier: w.Identifier}
	if w.Properties == "" {
		return desc, nil
	}
	if err := json.Unmarshal([]byte(w.Properties), &desc.Properties); err != nil {
		return ResourceDescription{}, &APIError{
			Op:      op,
			Message: fmt.Sprintf("Properties for %q did not parse as a JSON object: %v", w.Identifier, err),
		}
	}
	return desc, nil
}

// errorEnvelope is what Cloud Control's AWS JSON 1.0 error responses carry,
// and — the field that matters — what a successful response never sets:
// __type present is itself the signal that the call failed, independent of
// the HTTP status.
type errorEnvelope struct {
	Type    string `json:"__type"`
	Message string `json:"message"`
}

// errorCode strips the shape-ID prefix Cloud Control puts in front of its
// error type, e.g. "com.amazonaws.cloudformation#ResourceNotFoundException"
// becomes "ResourceNotFoundException".
func errorCode(wireType string) string {
	if i := strings.LastIndex(wireType, "#"); i >= 0 {
		return wireType[i+1:]
	}
	return wireType
}

// call makes one Cloud Control request, retrying it under this Client's
// retry policy when the failure is a ThrottlingException (doc.go's
// "Retries" section): every other failure - a transport error, a
// different *APIError code, a malformed response - returns to the caller
// straight from the first attempt. out may be nil for an operation whose
// response carries nothing the caller needs.
func (c *Client) call(ctx context.Context, operation string, payload any, out any) error {
	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		err := c.callOnce(ctx, operation, payload, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == c.maxAttempts || !HasCode(err, CodeThrottlingError) {
			return err
		}
		sleep := c.retrySleepFn
		if sleep == nil {
			sleep = retrySleep
		}
		if sleepErr := sleep(ctx, backoffDelay(c.retryBaseDelay, c.retryMaxDelay, attempt)); sleepErr != nil {
			return sleepErr
		}
	}
	return lastErr
}

// callOnce is call's single attempt: marshals payload, sends it as
// operation, and either decodes the response into out or returns an
// *APIError.
func (c *Client) callOnce(ctx context.Context, operation string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cloudcontrol: encoding the %s request: %w", operation, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cloudcontrol: building the %s request: %w", operation, err)
	}
	req.Header.Set("Content-Type", c.contentType)
	req.Header.Set("X-Amz-Target", c.opTargetPrefix+"."+operation)

	if err := c.authenticate(ctx, req, body); err != nil {
		return fmt.Errorf("cloudcontrol: signing the %s request: %w", operation, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cloudcontrol: %s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("cloudcontrol: reading the %s response: %w", operation, err)
	}

	var env errorEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return &APIError{
			Op:         operation,
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("unparseable response: %v", err),
		}
	}
	if env.Type != "" || resp.StatusCode >= http.StatusBadRequest {
		message := env.Message
		if message == "" {
			message = fmt.Sprintf("%s failed with HTTP %d", operation, resp.StatusCode)
		}
		return &APIError{
			Op:         operation,
			StatusCode: resp.StatusCode,
			Code:       errorCode(env.Type),
			Message:    message,
		}
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("cloudcontrol: decoding the %s response: %w", operation, err)
	}
	return nil
}

// GetResource fetches the full live model for one identifier of typeName.
func (c *Client) GetResource(ctx context.Context, typeName, identifier string) (*ResourceDescription, error) {
	var resp struct {
		ResourceDescription wireResourceDescription `json:"ResourceDescription"`
	}
	payload := struct {
		TypeName   string `json:"TypeName"`
		Identifier string `json:"Identifier"`
	}{TypeName: typeName, Identifier: identifier}

	if err := c.call(ctx, "GetResource", payload, &resp); err != nil {
		return nil, err
	}
	desc, err := resp.ResourceDescription.decode("GetResource")
	if err != nil {
		return nil, err
	}
	return &desc, nil
}

// ListResources enumerates every live resource of typeName, paginating
// NextToken to exhaustion.
func (c *Client) ListResources(ctx context.Context, typeName string) ([]ResourceDescription, error) {
	var out []ResourceDescription
	var nextToken string
	for {
		var resp struct {
			ResourceDescriptions []wireResourceDescription `json:"ResourceDescriptions"`
			NextToken            string                    `json:"NextToken"`
		}
		payload := struct {
			TypeName  string `json:"TypeName"`
			NextToken string `json:"NextToken,omitempty"`
		}{TypeName: typeName, NextToken: nextToken}

		if err := c.call(ctx, "ListResources", payload, &resp); err != nil {
			return nil, err
		}
		for _, w := range resp.ResourceDescriptions {
			desc, err := w.decode("ListResources")
			if err != nil {
				return nil, err
			}
			out = append(out, desc)
		}
		if resp.NextToken == "" {
			return out, nil
		}
		nextToken = resp.NextToken
	}
}
