// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// twoAccountSweepServer stands in for the tagging API (and, for whatever
// the roster routes there, Cloud Control) behind AWS_ENDPOINT_URL, and
// records who signed each GetResources: the access key id out of the SigV4
// credential scope, or "unsigned". It answers every listing empty - the
// buckets are client-named and read through the mock provider, so the
// sweep finds nothing here and the assertion is about the requests
// themselves, not their answers.
type twoAccountSweepServer struct {
	mu        sync.Mutex
	signedAs  []string
	getResReq int
}

func (s *twoAccountSweepServer) handler(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	if strings.HasSuffix(target, ".GetResources") {
		s.mu.Lock()
		s.getResReq++
		s.signedAs = append(s.signedAs, principalOf(r.Header.Get("Authorization")))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_ = json.NewEncoder(w).Encode(map[string]any{"ResourceTagMappingList": []any{}})
		return
	}
	// Cloud Control, or anything else the sweep might route here.
	w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	_ = json.NewEncoder(w).Encode(map[string]any{"ResourceDescriptions": []any{}})
}

// principalOf is the test's own read of a SigV4 Authorization header, kept
// independent of the client's signedAs so the two can disagree.
func principalOf(auth string) string {
	const prefix = "AWS4-HMAC-SHA256 Credential="
	if !strings.HasPrefix(auth, prefix) {
		return "unsigned"
	}
	keyID, _, _ := strings.Cut(strings.TrimPrefix(auth, prefix), "/")
	return keyID
}

// TestLivePlan_sweepSignsAsEachProviderConfiguration is GitHub issue #957.
// Two provider configurations in one region and two accounts, each named
// by its own static key pair; the estate-wide tag index has to be fetched
// once as each principal. Before this, both clients were built with
// Credentials nil and never signed against an endpoint override, so the
// two fetches went out identical - and on the emulator, which resolves the
// account from the access key id on the wire, both landed in the default
// account. The process environment carries a THIRD key pair ("test") so
// that a sweep that fell back to the default chain would be visible as
// such rather than accidentally passing as one of the two.
func TestLivePlan_sweepSignsAsEachProviderConfiguration(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-two-accounts"), td)
	t.Chdir(td)

	srv := &twoAccountSweepServer{}
	server := httptest.NewServer(http.HandlerFunc(srv.handler))
	t.Cleanup(server.Close)
	t.Setenv("TOFU_LIVE_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-two-accounts-home", "two-accounts-unit", "aws_s3_bucket.home", map[string]string{
		"id": "tofu-two-accounts-home", "bucket": "tofu-two-accounts-home",
	})
	cloud.putMarked("aws_s3_bucket", "tofu-two-accounts-other", "two-accounts-unit", "aws_s3_bucket.other_account", map[string]string{
		"id": "tofu-two-accounts-other", "bucket": "tofu-two-accounts-other",
	})

	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=two-accounts-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stdout(), "No changes.") {
		t.Errorf("both buckets are owned and unchanged; want a clean plan:\n%s", output.Stdout())
	}

	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.getResReq < 2 {
		t.Fatalf("GetResources was called %d time(s), want at least one per provider configuration (2): the tag index was not fetched per configuration", srv.getResReq)
	}
	seen := map[string]bool{}
	for _, p := range srv.signedAs {
		seen[p] = true
	}
	got := make([]string, 0, len(seen))
	for p := range seen {
		got = append(got, p)
	}
	sort.Strings(got)
	want := []string{"111111111111", "222222222222"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the tag index was fetched signed as %v, want exactly one principal per provider configuration %v (\"unsigned\" means the client ignored the block's keys and sent the request as nobody; \"test\" means it fell back to the process environment)\nall %d GetResources calls: %v",
			got, want, srv.getResReq, srv.signedAs)
	}
}
