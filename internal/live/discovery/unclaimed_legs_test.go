// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// GitHub issue #1729, part 1. Since #1707 a provider no family claims (helm,
// random) gets no sweep leg, but its discovery pass keeps Request.Sweep on so
// the record-store removal legs run on it. #1715 scoped the record-orphan
// leg to the pass's own provider. The parent-read and fold-child legs run on
// the same pass, and these tests pin what they do there: nothing.
//
// Both legs pick their types from the identity table (AWS-heavy; it also
// admits ten random_* types) and then gate each one on the PASS's own list
// schema, schemas.Get. Measured on 2026-09-30 by dispensing the cached
// provider binaries and reading GetProviderSchema:
//
//	helm 3.2.0    resources=[helm_release]                       list=[] tags-attr=0
//	random 3.9.1  resources=[random_bytes ... random_uuid7] (10) list=[] tags-attr=0
//
// No list resource, so schemas.Get is false for every type and neither leg
// gets past its first gate. Each test also gives the pass a list schema for
// every one of its own resource types, which neither provider serves today,
// so a later release that adds list resources is covered too. A parent
// needs a tags attribute in the pass's schema (taggableAdmittedTypes) and
// the fold-child table names one AWS type, so the legs still have nothing
// to act on.
//
// The control in each test is the same Result run through the AWS schema.
// The leg acts there, so the parents this test hands the unclaimed pass are
// ones the leg would read. Proved red by handing the helm pass list
// schemas for the AWS child types (and, for the parent-read leg, an empty
// ScopeProvider): both tests then report the list call and the finding.
//
// The parent-read leg has a second guard the fold-child leg lacks: it skips
// a parent another provider configuration declares (inScope). The fold-child
// leg's only protection on another provider's pass is the schema gate.

// unclaimedSurface is the measured surface of the two unclaimed providers,
// plus a hypothetical list schema per type (see the file comment).
func unclaimedSurface() map[string][]string {
	return map[string][]string{
		"helm": {"helm_release"},
		"random": {
			"random_bytes", "random_id", "random_integer", "random_password", "random_pet",
			"random_shuffle", "random_string", "random_uuid", "random_uuid4", "random_uuid7",
		},
	}
}

// countingProvider serves a fixed schema and counts every list call, and
// answers each one with a live object, so a leg that reached the list
// would find something and the test would see it.
type countingProvider struct {
	resp  providers.GetProviderSchemaResponse
	calls []string
}

func newUnclaimedProvider(types []string, withLists bool) *countingProvider {
	p := &countingProvider{resp: providers.GetProviderSchemaResponse{
		ResourceTypes:     map[string]providers.Schema{},
		ListResourceTypes: map[string]providers.Schema{},
	}}
	for _, ty := range types {
		p.resp.ResourceTypes[ty] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id": {Type: cty.String, Computed: true},
		}}}
		if withLists {
			p.resp.ListResourceTypes[ty] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			}}}
		}
	}
	return p
}

func (p *countingProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return p.resp
}

func (p *countingProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	p.calls = append(p.calls, req.TypeName)
	emit(providers.ListResourceEvent{
		DisplayName:    "live",
		Identity:       cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("live")}),
		ResourceObject: cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("live")}),
	})
	return nil
}

// declaredOnly is res with every leg-added removal and finding stripped:
// what the record legs see when they start, the bind and classifyOrphans
// output, which in a real multi-provider run is the whole estate's
// resolutions, not only the pass's own.
func declaredOnly(res *Result) *Result {
	out := &Result{Estate: res.Estate}
	for _, r := range res.Resolutions {
		if !r.Undeclared {
			out.Resolutions = append(out.Resolutions, r)
		}
	}
	return out
}

// runUnclaimedLegs runs the two legs this file pins, in Discover's order,
// on an unclaimed provider's pass over start, and fails on anything they
// do.
func runUnclaimedLegs(t *testing.T, start *Result, req Request) {
	t.Helper()
	for name, types := range unclaimedSurface() {
		for _, withLists := range []bool{false, true} {
			p := newUnclaimedProvider(types, withLists)
			schemas, diags := listclient.ListSchemas(t.Context(), p)
			assertNoErrors(t, diags)

			res := declaredOnly(start)
			before := len(res.Resolutions)
			pass := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider(name)}
			req.Provider = p
			req.ScopeProvider = pass
			req.VouchProvider = pass
			req.Sweep = true

			d := parentReadSweep(t.Context(), req, schemas, res)
			d = d.Append(foldChildReadSweep(t.Context(), req, schemas, res))
			if len(d) != 0 {
				t.Errorf("%s pass (list schemas %v): the legs raised %d diagnostics: %s", name, withLists, len(d), d.ErrWithWarnings())
			}
			if len(p.calls) != 0 {
				t.Errorf("%s pass (list schemas %v): the legs listed %v through the %s provider", name, withLists, p.calls, name)
			}
			if len(res.ParentReads) != 0 {
				t.Errorf("%s pass (list schemas %v): the legs reported %+v", name, withLists, res.ParentReads)
			}
			if len(res.Resolutions) != before {
				t.Errorf("%s pass (list schemas %v): the legs added %d resolutions: %v", name, withLists, len(res.Resolutions)-before, res.Resolutions[before:])
			}
		}
	}
}

// TestParentReadLegDoesNothingOnAnUnclaimedPass: the bucket a parent read
// anchors on is in the Result, and the AWS pass finds its undeclared
// policy. The helm and random passes over the same Result list nothing and
// propose nothing.
func TestParentReadLegDoesNothingOnAnUnclaimedPass(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_policy")
	cloud.obj("aws_s3_bucket_policy", "my-bucket", nil)
	cloud.obj("aws_s3_bucket_policy", "other-bucket", nil)
	awsRes := discoverParentReadFixture(t, cloud, Request{})
	if _, ok := findParentRead(awsRes, "aws_s3_bucket_policy", "my-bucket"); !ok {
		t.Fatalf("control: the AWS pass found no undeclared policy, so this test measures nothing:\n%s", awsRes)
	}

	cfg := loadConfig(t, parentReadFixture(t))
	runUnclaimedLegs(t, awsRes, Request{Estate: estateName, Config: cfg})
}

// TestFoldChildLegDoesNothingOnAnUnclaimedPass: the method a fold read
// renders is in the Result, and the AWS pass finds the integration under
// it. The helm and random passes over the same Result list nothing and
// propose nothing.
func TestFoldChildLegDoesNothingOnAnUnclaimedPass(t *testing.T) {
	provider := newFoldFakeProvider()
	provider.restAPIObject("api-123")
	provider.liveIntegration()
	awsRes := discoverFoldFixture(t, false, provider)
	if _, ok := findParentRead(awsRes, "aws_api_gateway_integration", "api-123/root-resource/GET"); !ok {
		t.Fatalf("control: the AWS pass found no integration, so this test measures nothing:\n%s", awsRes)
	}
	var method bool
	for _, r := range awsRes.Resolutions {
		if r.Type() == "aws_api_gateway_method" && !r.Undeclared {
			method = true
		}
	}
	if !method {
		t.Fatal("control: the Result carries no aws_api_gateway_method for the fold leg to anchor on")
	}
	if _, ok := identity.FoldParentOf("aws_api_gateway_integration"); !ok {
		t.Fatal("control: aws_api_gateway_integration is no longer a fold child; re-point this test at one that is")
	}

	cfg := loadConfig(t, foldReadFixture(t, false))
	runUnclaimedLegs(t, awsRes, Request{Estate: estateName, Config: cfg})
}
