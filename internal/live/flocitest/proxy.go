// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"bytes"
	"context"
	"encoding/json"
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
//
// It also separates two things a bare call count conflates (issue #565:
// "we made few calls but streamed a lot" and "we were throttled" have to
// be distinguishable findings, not one undifferentiated slow number):
//
//   - [CountingProxy.PaginationCounts] - of an action's calls, how many
//     carried a continuation cursor from a previous page (Marker,
//     NextToken/nextToken, or Route53's StartRecordName family) rather
//     than being that call's first page. This is list PAGINATION VOLUME:
//     a type whose population outgrows one page starts paying it, and it
//     grows with instance count even though the action name doing the
//     work never changes.
//   - [CountingProxy.ThrottleCounts] - of an action's calls, how many
//     responses came back shaped like a throttle (HTTP 429, or a 4xx body
//     naming Throttling/TooManyRequests/SlowDown/RequestLimitExceeded).
//     Retried calls inflate the same total a busy-but-unthrottled action
//     would, and only this counter tells them apart.
type CountingProxy struct {
	mu        sync.Mutex
	counts    map[string]int
	total     int
	pages     map[string]int
	throttles map[string]int
	srv       *httptest.Server
	endpoint  string
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

	p := &CountingProxy{counts: map[string]int{}, pages: map[string]int{}, throttles: map[string]int{}}
	rp := httputil.NewSingleHostReverseProxy(u)
	rp.ModifyResponse = p.observeResponse
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		action := p.record(r)
		// httputil.ReverseProxy.ServeHTTP clones r via r.Context() before
		// handing the clone to Director/RoundTrip, and http.Transport sets
		// resp.Request to (a clone of) that same outbound request - so a
		// value stashed in r's context here is still readable from
		// resp.Request.Context() in observeResponse below, long after the
		// outbound request's own body has been drained onto the wire and
		// can no longer be re-parsed for the action name the way [actionOf]
		// reads an inbound request.
		r = r.WithContext(context.WithValue(r.Context(), proxyActionCtxKey{}, action))
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

// proxyActionCtxKey is the context key [NewCountingProxy]'s handler stashes
// each request's action name under, so [CountingProxy.observeResponse] can
// read it back from resp.Request.Context() once the outbound request's own
// body is no longer available to re-parse.
type proxyActionCtxKey struct{}

// record classifies r by [actionOf] and counts it, then separately counts
// it as a pagination call ([isContinuationRequest]) if it carries a
// continuation cursor. Called from the proxy's own handler, before
// ServeHTTP forwards the (still-intact) request on. Returns the action name
// so the caller can stash it for [CountingProxy.observeResponse].
func (p *CountingProxy) record(r *http.Request) string {
	action := actionOf(r)
	continuation := isContinuationRequest(r)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts[action]++
	p.total++
	if continuation {
		p.pages[action]++
	}
	return action
}

// observeResponse inspects a forwarded response for a throttle shape
// ([isThrottleResponse]) and counts it against the action its own request
// carried (read from context - see proxyActionCtxKey). Installed as the
// reverse proxy's ModifyResponse hook, so it runs once per response
// actually received, retries included.
func (p *CountingProxy) observeResponse(resp *http.Response) error {
	throttled, err := isThrottleResponse(resp)
	if err != nil {
		return err
	}
	if !throttled {
		return nil
	}
	action, _ := resp.Request.Context().Value(proxyActionCtxKey{}).(string)
	if action == "" {
		action = "(unknown)"
	}
	p.mu.Lock()
	p.throttles[action]++
	p.mu.Unlock()
	return nil
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

// continuationParams names every request parameter this checkout's floci
// services use to carry a "give me the page after this one" cursor, across
// every wire shape [actionOf] already distinguishes: IAM/EC2/STS's
// query-protocol Marker/NextToken form fields, ECS/Cloud Control's JSON RPC
// nextToken/NextToken body field, Route53's REST-XML
// StartRecordName/StartRecordType/StartRecordIdentifier query parameters
// (ListResourceRecordSets) plus its ListHostedZones marker, and the
// Resource Groups Tagging API's PaginationToken. A name appears in
// whichever casing that service actually sends; matching is
// case-insensitive so one list covers both.
//
// PaginationToken was missing until issue #584 and its absence mattered
// more than any other entry would have, because the one call the estate-wide
// sweep's tagging leg makes is a GetResources against exactly that API. With
// it missing, every measurement this harness has produced reported
// pagination_total = 0 while the tagging leg was in fact fetching a page per
// 100 tagged resources - floci's ResourceGroupsTaggingService defaults
// resourcesPerPage to 100 and returns a nextPaginationToken whenever more
// remain. The measured GetResources counts (1, 2 and 4 calls at 38, 137 and
// 335 tagged resources) are ceil(n/100) exactly. So "floci returns a single
// page unconditionally" (lex00/floci#185) does not hold for this service,
// and a document repeating it about GetResources specifically is wrong.
var continuationParams = []string{
	"marker", "nexttoken", "next-token", "continuationtoken",
	"paginationtoken",
	"startrecordname", "startrecordtype", "startrecordidentifier",
}

// isContinuationRequest reports whether r carries a non-empty continuation
// cursor - a page fetched because an earlier response for the same action
// said there was more, as opposed to that action's first call. It checks,
// in turn, the query string, a query-protocol form body, and a JSON RPC
// body, restoring r.Body exactly as [actionOf] does so the proxy still
// forwards the original bytes.
func isContinuationRequest(r *http.Request) bool {
	if hasContinuationParam(r.URL.Query()) {
		return true
	}
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil || len(body) == 0 {
		return false
	}
	if vals, qErr := url.ParseQuery(string(body)); qErr == nil && hasContinuationParam(vals) {
		return true
	}
	var asJSON map[string]any
	if jErr := json.Unmarshal(body, &asJSON); jErr == nil {
		for k, v := range asJSON {
			s, ok := v.(string)
			if !ok || s == "" {
				continue
			}
			if isContinuationParamName(k) {
				return true
			}
		}
	}
	return false
}

func hasContinuationParam(vals url.Values) bool {
	for k, v := range vals {
		if len(v) == 0 || v[0] == "" {
			continue
		}
		if isContinuationParamName(k) {
			return true
		}
	}
	return false
}

func isContinuationParamName(name string) bool {
	lower := strings.ToLower(name)
	for _, want := range continuationParams {
		if lower == want {
			return true
		}
	}
	return false
}

// throttleBodyMarkers are the substrings a floci/AWS error body carries
// when a request was throttled, checked case-sensitively against the exact
// exception-name shapes these services actually send (ThrottlingException,
// TooManyRequestsException, SlowDown, RequestLimitExceeded) rather than a
// looser case-insensitive match that would also catch an unrelated message
// that merely mentions rate limits in prose.
var throttleBodyMarkers = []string{
	"Throttling", "TooManyRequests", "SlowDown", "RequestLimitExceeded",
}

// isThrottleResponse reports whether resp is shaped like a throttle: HTTP
// 429, or a 4xx/5xx body naming one of [throttleBodyMarkers]. It restores
// resp.Body after reading it, the response-side counterpart of
// [isContinuationRequest]'s request-body handling, so the real client
// behind the proxy still receives the original bytes.
func isThrottleResponse(resp *http.Response) (bool, error) {
	if resp.StatusCode == http.StatusTooManyRequests {
		return true, nil
	}
	if resp.StatusCode < 400 || resp.Body == nil {
		return false, nil
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	text := string(body)
	for _, marker := range throttleBodyMarkers {
		if strings.Contains(text, marker) {
			return true, nil
		}
	}
	return false, nil
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

// PaginationCounts is a snapshot of, per action, how many of its calls
// carried a continuation cursor (see [isContinuationRequest]) rather than
// being that action's first page - list pagination volume, reported
// separately from [CountingProxy.Counts] so it can be told apart from a
// call count inflated by retries instead (see
// [CountingProxy.ThrottleCounts]).
func (p *CountingProxy) PaginationCounts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.pages))
	for k, v := range p.pages {
		out[k] = v
	}
	return out
}

// PaginationTotal sums [CountingProxy.PaginationCounts] across every
// action.
func (p *CountingProxy) PaginationTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, v := range p.pages {
		total += v
	}
	return total
}

// ThrottleCounts is a snapshot of, per action, how many responses came back
// shaped like a throttle (see [isThrottleResponse]).
func (p *CountingProxy) ThrottleCounts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]int, len(p.throttles))
	for k, v := range p.throttles {
		out[k] = v
	}
	return out
}

// ThrottleTotal sums [CountingProxy.ThrottleCounts] across every action.
func (p *CountingProxy) ThrottleTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	total := 0
	for _, v := range p.throttles {
		total += v
	}
	return total
}

// Reset zeroes every counter, so a caller can measure one phase of a run
// (a plan's reads) without last phase's calls (the apply that manufactured
// the estate) polluting the total.
func (p *CountingProxy) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.counts = map[string]int{}
	p.total = 0
	p.pages = map[string]int{}
	p.throttles = map[string]int{}
}
