// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// The fold-child leg (issue #68): a small, self-contained fake provider
// exercises foldChildReadSweep's own orchestration - the declared/undeclared
// split and the report-only recording - with no gRPC or emulator in the
// way, the same role fakeLister plays for internal/live/listclient's own
// tests. The gated live proof against a real provider and floci is
// TestFoldChildReadSweepAgainstFloci, in fold_read_live_test.go.

// foldFakeProvider answers GetProviderSchema/ListResourceStream from canned
// data, keyed by type name only: every list call for a type returns
// whatever events were registered for it, regardless of the request's own
// scoping config. That is coarser than the real provider (which actually
// filters on rest_api_id/resource_id/http_method), but it is enough to
// exercise foldChildReadSweepType's own logic - the declared-instance skip
// happens before any list call is made at all, so a test proves "no call
// happened" by registering no events and asserting no finding.
type foldFakeProvider struct {
	schemas providers.GetProviderSchemaResponse
	events  map[string][]providers.ListResourceEvent
}

func newFoldFakeProvider() *foldFakeProvider {
	restAPIAttrs := map[string]*configschema.Attribute{
		"id":   {Type: cty.String, Computed: true},
		"tags": {Type: cty.Map(cty.String), Optional: true},
	}
	integrationAttrs := map[string]*configschema.Attribute{
		"rest_api_id": {Type: cty.String, Required: true},
		"resource_id": {Type: cty.String, Required: true},
		"http_method": {Type: cty.String, Required: true},
		"type":        {Type: cty.String, Required: true},
	}
	return &foldFakeProvider{
		events: make(map[string][]providers.ListResourceEvent),
		schemas: providers.GetProviderSchemaResponse{
			ResourceTypes: map[string]providers.Schema{
				"aws_api_gateway_rest_api": {
					Block: &configschema.Block{Attributes: restAPIAttrs},
					IdentitySchema: &configschema.Object{
						Nesting:    configschema.NestingSingle,
						Attributes: map[string]*configschema.Attribute{"id": {Type: cty.String, Required: true}},
					},
				},
				"aws_api_gateway_integration": {
					Block: &configschema.Block{Attributes: integrationAttrs},
				},
			},
			ListResourceTypes: map[string]providers.Schema{
				"aws_api_gateway_rest_api": {
					Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
						"region": {Type: cty.String, Optional: true},
					}},
				},
				"aws_api_gateway_integration": {
					Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
						"rest_api_id": {Type: cty.String, Optional: true},
						"resource_id": {Type: cty.String, Optional: true},
						"http_method": {Type: cty.String, Optional: true},
						"region":      {Type: cty.String, Optional: true},
					}},
				},
			},
		},
	}
}

func (p *foldFakeProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return p.schemas
}

func (p *foldFakeProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	for _, ev := range p.events[req.TypeName] {
		if !emit(ev) {
			break
		}
	}
	return nil
}

// restAPIObject registers the one live REST API this test's fixture
// declares, carrying this estate's own marker tags so the ordinary marker
// sweep binds aws_api_gateway_rest_api.app to it.
func (p *foldFakeProvider) restAPIObject(id string) {
	p.events["aws_api_gateway_rest_api"] = []providers.ListResourceEvent{{
		DisplayName: id,
		Identity:    cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal(id)}),
		ResourceObject: cty.ObjectVal(map[string]cty.Value{
			"id": cty.StringVal(id),
			"tags": cty.MapVal(map[string]cty.Value{
				TagEstate:  cty.StringVal(estateName),
				TagAddress: cty.StringVal("aws_api_gateway_rest_api.app"),
			}),
		}),
	}}
}

// liveIntegration registers a live integration foldChildReadSweep's own
// scoped read should find.
func (p *foldFakeProvider) liveIntegration() {
	p.events["aws_api_gateway_integration"] = []providers.ListResourceEvent{{
		DisplayName: "integration",
		ResourceObject: cty.ObjectVal(map[string]cty.Value{
			"rest_api_id": cty.StringVal("api-123"),
			"resource_id": cty.StringVal("root-resource"),
			"http_method": cty.StringVal("GET"),
			"type":        cty.StringVal("MOCK"),
		}),
	}}
}

// foldReadFixture writes a minimal configuration: an admitted, taggable
// REST API and its (untaggable, composite) method, with the integration
// block present or absent - the same "block deleted, anchor left standing"
// shape parentReadFixture uses for aws_s3_bucket_policy.
func foldReadFixture(t *testing.T, withIntegration bool) string {
	t.Helper()
	dir := t.TempDir()
	src := `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

resource "aws_api_gateway_rest_api" "app" {
  name = "fold-read-api"
}

resource "aws_api_gateway_method" "app" {
  rest_api_id   = aws_api_gateway_rest_api.app.id
  resource_id   = "root-resource"
  http_method   = "GET"
  authorization = "NONE"
}
`
	if withIntegration {
		src += `
resource "aws_api_gateway_integration" "app" {
  rest_api_id = aws_api_gateway_rest_api.app.id
  resource_id = "root-resource"
  http_method = "GET"
  type        = "MOCK"
}
`
	}
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func discoverFoldFixture(t *testing.T, withIntegration bool, provider *foldFakeProvider) *Result {
	t.Helper()
	cfg := loadConfig(t, foldReadFixture(t, withIntegration))
	res, diags := Discover(context.Background(), Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    provider,
		Sweep:       true,
		SweepTypes:  []string{"aws_api_gateway_rest_api", "aws_api_gateway_integration"},
	})
	assertNoErrors(t, diags)
	return res
}

// TestFoldChildReadSweep_DeclaredIsLeftAlone is the non-event: a declared,
// resolved aws_api_gateway_integration is never reported as an orphan of
// its own parent, and - since the declared check runs before any list call
// - the fake provider need not even carry a live object for it.
func TestFoldChildReadSweep_DeclaredIsLeftAlone(t *testing.T) {
	provider := newFoldFakeProvider()
	provider.restAPIObject("api-123")

	res := discoverFoldFixture(t, true, provider)

	if _, ok := findParentRead(res, "aws_api_gateway_integration", "api-123/root-resource/GET"); ok {
		t.Fatalf("a declared integration must not produce a fold-read finding:\n%s", res)
	}
}

// TestFoldChildReadSweep_UndeclaredFindsReportOnly is the headline: the
// integration's block is gone, its parent method is still declared, and a
// live integration exists under the parent's own rendered identity. The
// leg reports it - Removal is false, per issue #68's report-only standard
// for this shape (internal/live/identity/parent.go's foldParentTypes doc
// comment) - rather than silently proposing a destroy.
func TestFoldChildReadSweep_UndeclaredFindsReportOnly(t *testing.T) {
	provider := newFoldFakeProvider()
	provider.restAPIObject("api-123")
	provider.liveIntegration()

	res := discoverFoldFixture(t, false, provider)

	f, ok := findParentRead(res, "aws_api_gateway_integration", "api-123/root-resource/GET")
	if !ok {
		t.Fatalf("no fold-read finding for the orphaned integration:\n%s", res)
	}
	if f.Parent != "aws_api_gateway_method" {
		t.Errorf("finding names parent %q, want aws_api_gateway_method", f.Parent)
	}
	if f.ParentAddr.String() != "aws_api_gateway_method.app" {
		t.Errorf("finding names parent address %q, want aws_api_gateway_method.app", f.ParentAddr.String())
	}
	if f.Removal {
		t.Errorf("aws_api_gateway_integration must stay report-only, not a removal: %+v", f)
	}
	if f.Withheld == "" {
		t.Error("a report-only finding carries no withheld reason")
	}
	for _, r := range res.Resolutions {
		if r.Addr.String() == "aws_api_gateway_integration.app" {
			t.Errorf("a report-only finding must not add a resolution: %+v", r)
		}
	}
}

// TestFoldChildReadSweep_UndeclaredNothingLiveFindsNothing is the other
// non-event: the block is gone and nothing live matches the parent's own
// tuple either, so there is nothing to report.
func TestFoldChildReadSweep_UndeclaredNothingLiveFindsNothing(t *testing.T) {
	provider := newFoldFakeProvider()
	provider.restAPIObject("api-123")
	// No liveIntegration() call: the fake serves an empty list for the type.

	res := discoverFoldFixture(t, false, provider)

	if _, ok := findParentRead(res, "aws_api_gateway_integration", "api-123/root-resource/GET"); ok {
		t.Fatalf("no live integration exists; must not manufacture a finding:\n%s", res)
	}
}
