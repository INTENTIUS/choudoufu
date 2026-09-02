// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestScanTypeContentMatch is issue #272's end-to-end wiring check: the
// full [Discover] entry point, a real parsed configuration (no synthetic
// hcl.Body), a real [identity.ContentMatchTypes] entry (no test-only
// binding), and a fake Cloud Control server - exercising the actual
// dispatch in scanType (identity.ContentMatchTypes lookup ahead of
// cloudControlSource) rather than calling scanTypeContentMatch directly.
//
// aws_cloudfront_realtime_log_config is the real, generated binding this
// uses: Argument "name", CFN type AWS::CloudFront::RealtimeLogConfig,
// PropertyPath Name (tools/row-gen's contentMatchRoster, verified
// separately in tools/row-gen/contentmatch_test.go). If a future
// regeneration ever drops this type from the table, this test's own
// t.Fatal below - not a silently-skipped assertion - is what says so.
//
// This fixture used aws_cloudfront_cache_policy until the merge that added
// scanType's unique-name precedence check (see discovery.go's own doc
// comment): that type also carries a UniqueName row from the separate,
// independently-evolved unique-name mechanism, reading the same two-source
// evidence, so scanType now defers to that stronger leg for it and never
// reaches scanTypeContentMatch. aws_cloudfront_realtime_log_config has no
// such row - untaggable, no native list resource, and content-match is the
// only leg that can bind it - so it stays a genuine exercise of this
// dispatch branch. See the fixture's own comment for why
// aws_route53_key_signing_key, tried in between, was not it either.
func TestScanTypeContentMatch(t *testing.T) {
	binding, ok := identity.ContentMatchTypes["aws_cloudfront_realtime_log_config"]
	if !ok {
		t.Fatal("aws_cloudfront_realtime_log_config is not in identity.ContentMatchTypes; this test's whole premise depends on it")
	}

	cfg := loadConfig(t, filepath.Join("testdata", "contentmatch-e2e"))
	addr := mustAddr(t, "aws_cloudfront_realtime_log_config.x")

	newReq := func(cc *cloudcontrol.Client) Request {
		return Request{
			Estate:       ccEstate,
			Config:       cfg,
			Resolutions:  []identity.Resolution{{Addr: addr, Class: identity.ClassNeedsDiscovery}},
			Provider:     newFakeCloud(),
			CloudControl: cc,
		}
	}

	t.Run("zero live candidates - unbound, a create is proposed", func(t *testing.T) {
		srv := newCCServer(t)
		srv.listResources[binding.CFNType] = nil // nothing listed at all
		server := srv.start()
		defer server.Close()

		res, diags := Discover(context.Background(), newReq(cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})))
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		if _, ok := res.BindingFor(addr); ok {
			t.Fatalf("zero live candidates still produced a binding:\n%s", res)
		}
		if len(res.ProblemsOfKind(ProblemAmbiguousContentMatch)) != 0 {
			t.Errorf("zero candidates must never raise AMBIGUOUS_CONTENT_MATCH:\n%s", res)
		}
		found := false
		for _, u := range res.Unbound {
			if u.String() == addr.String() {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %s in Result.Unbound (so a plan proposes creating it):\n%s", addr, res)
		}
	})

	t.Run("one live candidate carrying the declared name - binds", func(t *testing.T) {
		srv := newCCServer(t)
		srv.listResources[binding.CFNType] = []ccResource{
			{
				identifier: "658327ea-f89d-4fab-a63d-7e88639e58f6",
				properties: map[string]any{
					"Id":   "658327ea-f89d-4fab-a63d-7e88639e58f6",
					"Name": "my-policy",
				},
			},
			{
				// A second live key-signing key with a DIFFERENT name must
				// never interfere with the match.
				identifier: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
				properties: map[string]any{
					"Id":   "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
					"Name": "unrelated-policy",
				},
			},
		}
		server := srv.start()
		defer server.Close()

		res, diags := Discover(context.Background(), newReq(cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})))
		if diags.HasErrors() {
			t.Fatalf("unexpected errors: %s", diags.Err())
		}
		b, ok := res.BindingFor(addr)
		if !ok {
			t.Fatalf("expected a binding for %s, got none:\n%s", addr, res)
		}
		if b.ImportID != "658327ea-f89d-4fab-a63d-7e88639e58f6" {
			t.Errorf("Binding.ImportID = %q, want the one candidate whose CachePolicyConfig.Name matched", b.ImportID)
		}
	})

	t.Run("two live candidates sharing the declared name - refuses, never guesses", func(t *testing.T) {
		srv := newCCServer(t)
		srv.listResources[binding.CFNType] = []ccResource{
			{
				identifier: "658327ea-f89d-4fab-a63d-7e88639e58f6",
				properties: map[string]any{
					"Id":   "658327ea-f89d-4fab-a63d-7e88639e58f6",
					"Name": "my-policy",
				},
			},
			{
				identifier: "11111111-2222-3333-4444-555555555555",
				properties: map[string]any{
					"Id":   "11111111-2222-3333-4444-555555555555",
					"Name": "my-policy",
				},
			},
		}
		server := srv.start()
		defer server.Close()

		res, diags := Discover(context.Background(), newReq(cloudcontrol.New(cloudcontrol.Config{Endpoint: server.URL, MaxAttempts: 1})))
		if diags.HasErrors() {
			t.Logf("diagnostics (expected - the refusal itself): %s", diags.Err())
		}
		if _, ok := res.BindingFor(addr); ok {
			t.Fatalf("two candidates sharing the declared name still produced a binding:\n%s", res)
		}
		problems := res.ProblemsOfKind(ProblemAmbiguousContentMatch)
		if len(problems) != 1 {
			t.Fatalf("want exactly one AMBIGUOUS_CONTENT_MATCH problem, got %d:\n%s", len(problems), res)
		}
		if len(problems[0].LiveIDs) != 2 {
			t.Errorf("Problem.LiveIDs = %v, want both candidates named", problems[0].LiveIDs)
		}
	})
}
