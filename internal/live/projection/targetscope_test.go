// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// TestBuildSkipsWhatTargetingExcludes is GitHub issue #1176's second pass.
// #352 gave the resolution pass an [identity.Scope]; the projection never
// got one, so a -target run still looked up every concrete resolution in
// the configuration - and a lookup is a real provider call that can error,
// which here is the hard "Cannot import for projection" refusal.
//
// reference-k8s-cert-manager is the worked case and the reason the field
// exists: 47 blocks install cert-manager's CRDs and go up first with
// -target, the three custom resources of those CRDs are excluded, and
// importing them anyway made the kubernetes provider answer "no matches
// for kind \"Certificate\" in version \"cert-manager.io/v1\"" - a refusal
// over resources the run had been told not to act on, on the only route
// that can install those CRDs at all.
//
// The control is the same build with no scope, which must still fail:
// otherwise this test would pass against a change that merely stopped
// reporting import errors.
func TestBuildSkipsWhatTargetingExcludes(t *testing.T) {
	cfg := loadConfig(t, "testdata/named")
	const excluded = `aws_s3_bucket_policy.data`
	const kept = `aws_cloudwatch_log_group.app`

	build := func(t *testing.T, scope identity.Scope) (*Result, string) {
		t.Helper()
		cloud := newFakeCloud()
		cloud.put("aws_cloudwatch_log_group", "/ours/logs", map[string]string{
			"id": "/ours/logs", "name": "/ours/logs",
		})
		// The excluded resource cannot be looked up at all - the same
		// position the cert-manager custom resources are in before their
		// CRDs exist.
		cloud.importErrors["aws_s3_bucket_policy/ownership-unit-data"] = "no matches for kind"

		res, diags := BuildWith(context.Background(), cfg, []identity.Resolution{
			{Addr: mustAddr(t, kept), Class: identity.ClassConcrete, ImportID: "/ours/logs"},
			{Addr: mustAddr(t, excluded), Class: identity.ClassConcrete, ImportID: "ownership-unit-data"},
		}, cloud.providers(t), Options{Scope: scope})
		if diags.HasErrors() {
			return res, renderDiags(diags)
		}
		return res, ""
	}

	t.Run("control: no scope still refuses", func(t *testing.T) {
		_, errs := build(t, nil)
		if errs == "" {
			t.Fatal("an untargeted run over an unlookuppable resource did not refuse; this test's control is not load-bearing")
		}
		if !strings.Contains(errs, "Cannot import for projection") {
			t.Errorf("the control failed for some other reason:\n%s", errs)
		}
	})

	t.Run("excluded is never looked up", func(t *testing.T) {
		res, errs := build(t, func(addr addrs.ConfigResource) bool {
			return addr.String() != "aws_s3_bucket_policy.data"
		})
		if errs != "" {
			t.Fatalf("a -target run was refused over a resource it excludes:\n%s", errs)
		}
		if !res.Has(mustAddr(t, kept)) {
			t.Errorf("the in-scope resource was not projected; the scope is narrowing more than it should:\n%s", res)
		}
		om := omissionFor(t, res, excluded)
		if om.Reason != ReasonOutOfScope {
			t.Errorf("the excluded resource is omitted as %s, want %s: %s", om.Reason, ReasonOutOfScope, om.Detail)
		}
		if !strings.Contains(om.Detail, "-target") {
			t.Errorf("the omission does not say targeting caused it: %s", om.Detail)
		}
	})
}
