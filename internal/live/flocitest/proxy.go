// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

// CountingProxy is an HTTP reverse proxy that stands in for floci's own
// endpoint and counts every request it forwards, classified by the AWS API
// action name each request carries. It is issue #64's cheaper alternative
// to an instrumented RoundTripper: the AWS provider plugin that does most
// of a plan's reading runs in its own subprocess with its own http.Client,
// so a RoundTripper this test process installs on its own client would
// never see those requests. Every request still goes out over plain HTTP
// to whatever AWS_ENDPOINT_URL names, provider subprocess and this
// process's own [cloudcontrol.Client] alike, so a proxy standing in for
// that endpoint sees all of them at one seam - the same technique
// discovery's own probe_test.go (requestRecorder) already uses to inspect
// one request's shape; this generalizes it to counting every request
// across an entire run.
type CountingProxy struct {
	mu       sync.Mutex
	counts   map[string]int
	total    int
	srv      *httptest.Server
	endpoint string
}

// NewCountingProxy starts a counting proxy in front of target (floci's own
// endpoint, e.g. [Endpoint](port)) and returns it, cleaned up when t ends.
// The proxy's own URL ([CountingProxy.Endpoint]) is what a caller sets
// AWS_ENDPOINT_URL to in place of target.
func NewCountingProxy(t *testing.T, target string) *CountingProxy {
	t.Helper()

	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parsing the counting proxy's target %q: %v", target, err)
	}

	p := &CountingProxy{counts: map[string]int{}}
	rp := httputil.NewSingleHostReverseProxy(u)
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.record(r)
		rp.ServeHTTP(w, r)
	}))
	t.Cleanup(p.srv.Close)

	// httptest.NewServer's default Listener binds 127.0.0.1 and reports its
	// URL that way, but the S3 Control API - which is what a bucket's tags
	// are read through - builds a virtual-hosted URL by prefixing the
	// account ID onto the endpoint's host ("000000000000." + host) with no
	// check for whether that host is an IP literal a DNS query could never
	// resolve as a subdomain. "localhost" has no such problem: RFC 6761
	// makes every subdomain of it resolve to loopback with no DNS query at
	// all, which is exactly why [Endpoint] (floci's own address) already
	// uses "localhost" rather than "127.0.0.1" - this rewrite just gives
	// the proxy's own address the same property.
	srvURL, err := url.Parse(p.srv.URL)
	if err != nil {
		t.Fatalf("parsing the counting proxy's own URL %q: %v", p.srv.URL, err)
	}
	p.endpoint = "http://localhost:" + srvURL.Port()
	return p
}

// Endpoint is the proxy's own URL: what AWS_ENDPOINT_URL should name so
// every call - the AWS provider subprocess's and this process's own Cloud
// Control client's alike - passes through here on the way to the real
// floci endpoint.
func (p *CountingProxy) Endpoint() string {
	return p.endpoint
}

// record classifies r by [actionOf] and counts it. Called from the proxy's
// own handler, before ServeHTTP forwards the (still-intact) request on.
func (p *CountingProxy) record(r *http.Request) {
	action := actionOf(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[action]++
	p.total++
}

// actionOf names the AWS API action a request carries, trying each
// protocol shape floci's covered services actually use in turn:
//
//  1. AWS JSON RPC (Cloud Control, DynamoDB, SNS's newer surface, ...):
//     the X-Amz-Target header names the action directly.
//  2. The EC2/query protocol (EC2, ELB classic, IAM, STS, ...): an
//     "Action" form field in the body, which this reads without
//     disturbing the body - the proxy still has to forward it unchanged,
//     the same care discovery's own probe_test.go takes.
//  3. Anything else (S3's REST-XML surface, mainly - always path-style in
//     this checkout, tools/estate-gen's versionsTF sets s3_use_path_style):
//     the bucket name and object key are call-specific data living in the
//     path, not the action, so grouping on the raw path would produce one
//     bucket-shaped "action" per *resource* rather than one per
//     *operation*. A subresource query string ("?tagging", "?acl",
//     "?versioning", with no value) names the operation directly and
//     survives grouping-by-key; with no query string, the path's depth
//     past the first segment - none for HeadBucket/CreateBucket, one or
//     more for anything object-level - is the next-best grouping this
//     protocol's wire shape offers with no bucket or object name exposed.
func actionOf(r *http.Request) string {
	if target := r.Header.Get("X-Amz-Target"); target != "" {
		return target
	}
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err == nil {
			if vals, qErr := url.ParseQuery(string(body)); qErr == nil {
				if action := vals.Get("Action"); action != "" {
					return action
				}
			}
		}
	}
	if r.URL.RawQuery != "" {
		vals, err := url.ParseQuery(r.URL.RawQuery)
		if err == nil && len(vals) > 0 {
			keys := make([]string, 0, len(vals))
			for k := range vals {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			return r.Method + " ?" + strings.Join(keys, "&")
		}
	}
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segments) <= 1 {
		return r.Method + " /{bucket}"
	}
	return r.Method + " /{bucket}/..."
}

// Total is how many requests the proxy has forwarded since the last
// [CountingProxy.Reset] (or since it started, if Reset was never called).
func (p *CountingProxy) Total() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

// Counts is a snapshot of the proxy's per-action counters, safe for the
// caller to keep and mutate.
func (p *CountingProxy) Counts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.counts))
	for k, v := range p.counts {
		out[k] = v
	}
	return out
}

// Reset zeroes every counter, so a caller can measure one phase of a run
// (a plan's reads) without last phase's calls (the apply that manufactured
// the estate) polluting the total.
func (p *CountingProxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts = map[string]int{}
	p.total = 0
}
