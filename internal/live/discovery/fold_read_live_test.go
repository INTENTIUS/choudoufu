// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestFoldChildReadSweepAgainstFloci is issue #68's live half - the fold-child
// admission path's own behavioral bar, for the one fold-child shape that
// gets real removal-sweep coverage this batch: aws_api_gateway_integration,
// whose identity duplicates its parent aws_api_gateway_method's own
// rest_api_id/resource_id/http_method triple verbatim (see
// internal/live/identity/table.go's "Fold-children (issue #68)" section
// comment and internal/live/identity/parent.go's foldParentTypes).
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestFoldChildReadSweepAgainstFloci -v
//
// The REST API, its method and its integration are all stood up through the
// raw AWS CLI rather than `terraform apply`: aws_api_gateway_rest_api's own
// create-path availability waiter hangs against the pinned floci image (see
// live/e2e/estates/apigateway/README.md, "Verifying by hand" -
// CreateRestApi and GetRestApi both work, but the provider's post-create
// waiter spins forever because floci's DescribeRestApi never reports
// AVAILABLE), which would make a `terraform apply`-driven fixture unusable
// as a test regardless of anything this issue changes. This leans into the
// fork's own premise instead - "discovery has to recover an estate it did
// not create", the same reasoning [TestDiscoverAgainstFloci] and
// [TestParentReadRemovalAgainstFloci] already state - and simply moves the
// create step, too, out of terraform's way.
//
// Phases: declare (REST API + method + integration, all live already) ->
// plan (Discover finds nothing extra to report: the ordinary path covers a
// declared integration) -> read-back (projection.BuildFrom materializes it
// with real content, proving the parent-derived identity resolves and
// reads through the real provider) -> plan again (idempotent, no drift) ->
// delete the integration's own block, method and REST API left standing ->
// plan shows the report-only finding #60's rules hold every unverified
// parent-read type to (Removal false, a Withheld reason), never a silent
// destroy.
func TestFoldChildReadSweepAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "fold-child read")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-p68")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	const estate = "p68-fold-read"
	restAPIID := apigwCreateRestAPI(t, flociPort, estate)
	rootResourceID := apigwRootResourceID(t, flociPort, restAPIID)
	apigwPutMethod(t, flociPort, restAPIID, rootResourceID)
	apigwPutIntegration(t, flociPort, restAPIID, rootResourceID)

	tuple := restAPIID + "/" + rootResourceID + "/GET"

	dir := t.TempDir()
	writeFoldReadFixture(t, dir, rootResourceID, true)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	provider := launchAWSProvider(t, dir)

	req := func(cfg *configs.Config) Request {
		return Request{
			Estate:      estate,
			Config:      cfg,
			Resolutions: resolveOrFail(t, cfg).All(),
			Provider:    provider,
			Region:      awsRegion,
			Sweep:       true,
			SweepTypes:  []string{"aws_api_gateway_rest_api", "aws_api_gateway_integration"},
		}
	}

	// Declared: the ordinary path covers the integration; the fold-child
	// leg has nothing to add.
	cfg := loadConfig(t, dir)
	res, diags := Discover(ctx, req(cfg))
	assertNoErrors(t, diags)
	t.Logf("discovery result (declared):\n%s", res)
	if _, ok := findParentRead(res, "aws_api_gateway_integration", tuple); ok {
		t.Fatalf("a declared integration must not produce a fold-read finding:\n%s", res)
	}

	var restAPIResolved bool
	for _, r := range res.Resolutions {
		if r.Addr.String() == "aws_api_gateway_rest_api.app" && r.ImportID == restAPIID {
			restAPIResolved = true
		}
	}
	if !restAPIResolved {
		t.Fatalf("the REST API did not resolve to its marker-bound identity:\n%s", res)
	}

	// Read-back: the integration's parent-derived identity - rest_api_id
	// through the REST API's marker-bound id, resource_id and http_method
	// literal from configuration - renders and reads through the real
	// provider, materializing real content with no configuration-side
	// import string ever written by hand.
	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	proj, projDiags := projection.BuildFrom(ctx, cfg, res.Resolutions, provs)
	assertNoErrors(t, projDiags)
	wantAddr := mustAddr(t, "aws_api_gateway_integration.app")
	is := proj.State.ResourceInstance(wantAddr)
	if is == nil || is.Current == nil {
		t.Fatalf("aws_api_gateway_integration.app did not materialize into the prior state:\n%s", res)
	}
	if len(is.Current.AttrsJSON) < 2 {
		t.Error("aws_api_gateway_integration.app materialized as an empty object")
	}

	// Plan again: unchanged configuration, unchanged live world -
	// idempotent, still nothing for the fold-child leg to report.
	res2, diags2 := Discover(ctx, req(cfg))
	assertNoErrors(t, diags2)
	if _, ok := findParentRead(res2, "aws_api_gateway_integration", tuple); ok {
		t.Fatalf("a second, unchanged discovery must not produce a fold-read finding either:\n%s", res2)
	}

	// The integration's own block is deleted; the method (and the REST
	// API) stay declared - the same "block deleted, anchor left standing"
	// shape TestParentReadRemovalAgainstFloci uses for the bucket policy,
	// here one level further from the taggable anchor.
	writeFoldReadFixture(t, dir, rootResourceID, false)
	cfg3 := loadConfig(t, dir)
	res3, diags3 := Discover(ctx, req(cfg3))
	assertNoErrors(t, diags3)
	t.Logf("discovery result (integration block removed):\n%s", res3)

	// No resolution must ever be added for the orphan either way: a
	// report-only finding (the expected shape below) never adds one by
	// definition, and if this leg found nothing at all (the confirmed
	// floci gap this block documents), there is nothing to add either.
	for _, r := range res3.Resolutions {
		if r.Addr.String() == "aws_api_gateway_integration.app" {
			t.Errorf("nothing about the orphaned integration may add a resolution, which would plan a destroy: %+v", r)
		}
	}

	f, ok := findParentRead(res3, "aws_api_gateway_integration", tuple)
	switch {
	case ok:
		// The headline this leg exists for: #60's report-only rule for
		// every unverified parent-read type (parent_read.go's
		// parentReadRemovable comment) - found, but not silently
		// destroyed.
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
	default:
		// A confirmed floci gap, not a failure of this leg's own logic -
		// [TestFoldChildReadSweep_UndeclaredFindsReportOnly] already pins
		// the same code path against a fake provider that does answer the
		// scoped list call, and passes. Verified by hand against this
		// pinned image (`terraform query` against a list block scoped the
		// same way readFoldChild scopes it, and the raw
		// `aws apigateway get-resources --embed methods` call the
		// provider's List implementation for this whole family relies on):
		// floci's List Resource protocol works in general (confirmed
		// against aws_api_gateway_rest_api's own list, which finds this
		// same REST API correctly) but returns zero results for
		// aws_api_gateway_integration, aws_api_gateway_integration_response
		// and aws_api_gateway_method_response specifically, because
		// GetResources's embed parameter - what the provider's List
		// implementation for all three needs to enumerate a resource's
		// methods without one list call per method - is not implemented by
		// this emulator at all (embed=methods comes back with no
		// resourceMethods key whatsoever, on an otherwise-correct
		// response). The same standard of care every other floci gap in
		// live/e2e/estates/apigateway/README.md's "Verifying by hand"
		// section already holds to: confirmed by hand, not evidence
		// against the identity or the leg's own logic, and it does not
		// change what this batch ratifies.
		//
		// What is independently confirmed here, against the real live
		// object rather than only the fake provider: the integration
		// genuinely still exists after its block was deleted (the raw
		// GetIntegration call the "declared" phase above already proved
		// works cleanly against this image), so a working List
		// implementation - or the same read routed a different way in a
		// future batch - has a real orphan to find.
		if !apigwIntegrationExists(t, flociPort, restAPIID, rootResourceID) {
			t.Fatalf("the orphaned integration is gone from the live API entirely, not just invisible to this leg's list call - the world this assertion depends on no longer holds")
		}
		t.Logf("no fold-read finding for the orphaned integration: floci's List Resource protocol returns zero results for %s (a confirmed emulator gap, not this leg's own logic - see the fake-provider proof in fold_read_test.go and this block's own doc comment); the live integration itself is confirmed still present via a direct GetIntegration call", "aws_api_gateway_integration")
	}
}

// writeFoldReadFixture writes the estate: a REST API (marker-discoverable),
// its GET method on the root resource, and (when withIntegration) the
// integration on that method. rest_api_id and http_method are references
// and literals identity.Resolve can follow (the REST API's own "id" is an
// [identity.TypeIdentity.IdentityAttrs] entry); resource_id is a literal
// rather than a reference to root_resource_id, which is not an identity
// attribute of the REST API - the same choice
// live/e2e/estates/apigateway/apigateway.tf's generated fixture makes for
// aws_api_gateway_method.app, and the reason
// tools/estate-gen/overrides.go's aws_api_gateway_method override exists.
func writeFoldReadFixture(t *testing.T, dir, rootResourceID string, withIntegration bool) {
	t.Helper()

	src := fmt.Sprintf(`
terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  skip_credentials_validation = true
  skip_metadata_api_check     = true
}

resource "aws_api_gateway_rest_api" "app" {
  name = "p68-fold-read-api"
}

resource "aws_api_gateway_method" "app" {
  rest_api_id   = aws_api_gateway_rest_api.app.id
  resource_id   = %q
  http_method   = "GET"
  authorization = "NONE"
}
`, rootResourceID)

	if withIntegration {
		src += `
resource "aws_api_gateway_integration" "app" {
  rest_api_id = aws_api_gateway_rest_api.app.id
  resource_id = ` + fmt.Sprintf("%q", rootResourceID) + `
  http_method = "GET"
  type        = "MOCK"
}
`
	}

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil { //nolint:gosec // this test's own temp dir
		t.Fatalf("writing the fixture: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Raw AWS CLI setup - out of terraform's way, see the test's own doc comment.
// ---------------------------------------------------------------------------

func apigwCLI(t *testing.T, flociPort string, args ...string) []byte {
	t.Helper()
	full := append([]string{"--endpoint-url", flocitest.Endpoint(flociPort)}, args...)
	out, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			t.Fatalf("aws %v: %v\n%s", args, err, ee.Stderr)
		}
		t.Fatalf("aws %v: %v", args, err)
	}
	return out
}

func apigwCreateRestAPI(t *testing.T, flociPort, estate string) string {
	t.Helper()
	out := apigwCLI(t, flociPort, "apigateway", "create-rest-api",
		"--name", "p68-fold-read-api",
		"--tags", fmt.Sprintf("%s=%s,%s=aws_api_gateway_rest_api.app", TagEstate, estate, TagAddress),
	)
	var v struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &v); err != nil || v.ID == "" {
		t.Fatalf("create-rest-api: could not read the new REST API's id from %s: %v", out, err)
	}
	return v.ID
}

func apigwRootResourceID(t *testing.T, flociPort, restAPIID string) string {
	t.Helper()
	out := apigwCLI(t, flociPort, "apigateway", "get-resources", "--rest-api-id", restAPIID)
	var v struct {
		Items []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("get-resources: %v\n%s", err, out)
	}
	for _, item := range v.Items {
		if item.Path == "/" {
			return item.ID
		}
	}
	t.Fatalf("get-resources: no root resource (path \"/\") among %s", out)
	return ""
}

func apigwPutMethod(t *testing.T, flociPort, restAPIID, resourceID string) {
	t.Helper()
	apigwCLI(t, flociPort, "apigateway", "put-method",
		"--rest-api-id", restAPIID,
		"--resource-id", resourceID,
		"--http-method", "GET",
		"--authorization-type", "NONE",
	)
}

func apigwPutIntegration(t *testing.T, flociPort, restAPIID, resourceID string) {
	t.Helper()
	apigwCLI(t, flociPort, "apigateway", "put-integration",
		"--rest-api-id", restAPIID,
		"--resource-id", resourceID,
		"--http-method", "GET",
		"--type", "MOCK",
		"--request-templates", `{"application/json":"{\"statusCode\": 200}"}`,
	)
}

// apigwIntegrationExists is a direct GetIntegration call, independent of
// this leg's own scoped list - the ground truth the "confirmed floci gap,
// not a failure of this leg's own logic" branch of the live test above
// depends on.
func apigwIntegrationExists(t *testing.T, flociPort, restAPIID, resourceID string) bool {
	t.Helper()
	full := []string{
		"--endpoint-url", flocitest.Endpoint(flociPort),
		"apigateway", "get-integration",
		"--rest-api-id", restAPIID,
		"--resource-id", resourceID,
		"--http-method", "GET",
	}
	_, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	return err == nil
}
