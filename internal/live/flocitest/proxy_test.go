// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package flocitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestCountingProxyPaginationAndThrottle proves issue #565's two new
// counters actually measure what they claim, against a fake backend this
// test controls completely - no docker, no floci. Without a control like
// this, "pagination volume" and "throttle count" would be numbers nobody
// ever watched fail.
func TestCountingProxyPaginationAndThrottle(t *testing.T) {
	var responses []func(w http.ResponseWriter, r *http.Request)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(responses) == 0 {
			t.Fatalf("backend got an unexpected request: %s %s", r.Method, r.URL)
		}
		next := responses[0]
		responses = responses[1:]
		next(w, r)
	}))
	defer backend.Close()

	ok := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<ok/>"))
	}
	throttled := func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<ErrorResponse><Error><Code>ThrottlingException</Code></Error></ErrorResponse>`))
	}

	// Call 1: ListRoles, first page (query protocol, no Marker).
	responses = append(responses, ok)
	// Call 2: ListRoles, page 2 (carries Marker - a continuation).
	responses = append(responses, ok)
	// Call 3: ListRoles, page 3, throttled once then (implicitly) would
	// retry - this test only sends one throttled response and checks it is
	// counted; it does not need a real retry loop to prove the counter.
	responses = append(responses, throttled)
	// Call 4: CreateRole (query protocol, no continuation, not a list).
	responses = append(responses, ok)

	p := NewCountingProxy(t, backend.URL)
	client := &http.Client{}

	post := func(form url.Values) {
		req, err := http.NewRequest(http.MethodPost, p.Endpoint()+"/", strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatalf("building request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}

	post(url.Values{"Action": {"ListRoles"}})
	post(url.Values{"Action": {"ListRoles"}, "Marker": {"page-2-cursor"}})
	post(url.Values{"Action": {"ListRoles"}, "Marker": {"page-3-cursor"}})
	post(url.Values{"Action": {"CreateRole"}, "RoleName": {"r1"}})

	if got, want := p.Total(), 4; got != want {
		t.Fatalf("Total() = %d, want %d", got, want)
	}
	if got, want := p.Counts()["ListRoles"], 3; got != want {
		t.Fatalf("Counts()[ListRoles] = %d, want %d", got, want)
	}

	// Has-teeth check: the first ListRoles call carried no Marker and must
	// NOT be counted as a pagination call, only the two that did.
	if got, want := p.PaginationCounts()["ListRoles"], 2; got != want {
		t.Fatalf("PaginationCounts()[ListRoles] = %d, want %d (the first page must not count)", got, want)
	}
	if got, want := p.PaginationCounts()["CreateRole"], 0; got != want {
		t.Fatalf("PaginationCounts()[CreateRole] = %d, want %d (not a continuation call)", got, want)
	}
	if got, want := p.PaginationTotal(), 2; got != want {
		t.Fatalf("PaginationTotal() = %d, want %d", got, want)
	}

	// Has-teeth check: only the deliberately-throttled third call may be
	// counted; the other three (two clean list pages, one create) must not.
	if got, want := p.ThrottleCounts()["ListRoles"], 1; got != want {
		t.Fatalf("ThrottleCounts()[ListRoles] = %d, want %d", got, want)
	}
	if got, want := p.ThrottleTotal(), 1; got != want {
		t.Fatalf("ThrottleTotal() = %d, want %d", got, want)
	}

	p.Reset()
	if got := p.Total(); got != 0 {
		t.Fatalf("after Reset, Total() = %d, want 0", got)
	}
	if got := p.PaginationTotal(); got != 0 {
		t.Fatalf("after Reset, PaginationTotal() = %d, want 0", got)
	}
	if got := p.ThrottleTotal(); got != 0 {
		t.Fatalf("after Reset, ThrottleTotal() = %d, want 0", got)
	}
}

// TestIsContinuationRequestJSONBody proves the JSON-RPC body path (ECS,
// Cloud Control) is also recognized, not just the query-protocol form
// field the main test above exercises.
func TestIsContinuationRequestJSONBody(t *testing.T) {
	first, err := http.NewRequest(http.MethodPost, "http://example/", strings.NewReader(`{"cluster":"c1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if isContinuationRequest(first) {
		t.Fatalf("a JSON body with no nextToken must not read as a continuation")
	}

	cont, err := http.NewRequest(http.MethodPost, "http://example/", strings.NewReader(`{"cluster":"c1","nextToken":"abc"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !isContinuationRequest(cont) {
		t.Fatalf("a JSON body carrying a non-empty nextToken must read as a continuation")
	}

	// Route53's REST query-parameter style (ListResourceRecordSets paging).
	restCont, err := http.NewRequest(http.MethodGet, "http://example/2013-04-01/hostedzone/Z1/rrset?name=foo.&type=A&StartRecordName=bar.", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if !isContinuationRequest(restCont) {
		t.Fatalf("a StartRecordName query parameter must read as a continuation")
	}
}
