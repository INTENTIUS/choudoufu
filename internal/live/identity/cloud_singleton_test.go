// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The cloud-singleton batch admitted two types whose whole documented import
// ID is the Region name: aws_vpc_block_public_access_options ("import VPC
// Block Public Access Options using the `aws_region`", id = "us-east-1") and
// aws_cloudwatch_otel_enrichment ("import CloudWatch OTel Enrichment using
// the region", id = "us-west-2"). Both rows are one Component{Cloud:
// "region"}, which tools/row-gen derives from the scrape's own
// sole_id_cloud_value field.
//
// These tests assert the RENDERED identity rather than a predicate. The two
// rows are the first admitted entries whose identity contains nothing of the
// resource's own, so "the type is admitted" and "the marker names the right
// object" are further apart here than anywhere else in the table: an
// admission boolean would be green over a component that resolved to the
// empty string, and so would row-gen's own mismatch ledger.
//
// The two blocks in the first fixture deliberately reach their region by the
// two different routes resolve.go distinguishes, because only one of the two
// rows can exercise each. aws_vpc_block_public_access_options carries
// Attrs: ["region"] alongside its Cloud value - emit.go's mergeCloudDefault
// puts it there, from the doc's own "Defaults to the Region set in the
// provider configuration" bullet - so a stated `region` argument is read by
// cloudComponentAttr. aws_cloudwatch_otel_enrichment's page documents its
// `region` argument without that sentence ("AWS region where this resource
// is managed"), so cloud_default is empty, mergeCloudDefault adds no Attrs,
// and its stated `region` has to arrive through resolver.effectiveRegion
// instead. Both routes must produce the region, and the fixture crosses them
// over so neither test passes by the other's mechanism.

// TestCloudSingletonResolvesToTheRegion pins both rows to the region each
// block actually targets: one inherited from the provider block, one stated
// on the resource itself.
func TestCloudSingletonResolvesToTheRegion(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-singleton"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for addr, want := range map[string]string{
		"aws_vpc_block_public_access_options.inherited": "eu-west-1",
		"aws_cloudwatch_otel_enrichment.override":       "us-west-2",
	} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s; the region its identity needs is in configuration", addr, res.Class)
			continue
		}
		if res.ImportID != want {
			t.Errorf("%s resolved to import ID %q, want %q", addr, res.ImportID, want)
		}
	}
}

// TestCloudSingletonCollisionStillCaught is the injectivity half, and it
// matters more for this shape than for any other. A region-only identity
// means two blocks of the same type pointed at the same region ARE the same
// cloud object - that is the whole premise of a per-account, per-region
// singleton - so a configuration declaring both must be refused rather than
// silently applied twice. One block inherits the provider block's region and
// the other names the identical region through a variable, so the two reach
// it by different routes and only the rendered value makes them equal.
func TestCloudSingletonCollisionStillCaught(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "cloud-singleton-collision"), nil)

	_, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatal("two aws_vpc_block_public_access_options blocks in the same region were both accepted")
	}
	if !hasDiag(diags, "Two resources with the same identity", `"eu-west-1"`) {
		t.Errorf("the refusal is not the duplicate-identity one:\n%s", renderDiags(diags))
	}
}

// TestCloudSingletonNeedsDiscoveryWithNoRegionAnywhere is the mutation check
// on the two above: strip the region from both the resource and its provider
// block and the identity must go unresolved, not fall back to an empty
// string. An identity component that renders "" would still be a marker, and
// it would name every region's singleton at once.
func TestCloudSingletonNeedsDiscoveryWithNoRegionAnywhere(t *testing.T) {
	dir := t.TempDir()
	const src = `
resource "aws_vpc_block_public_access_options" "nowhere" {
  internet_gateway_block_mode = "block-bidirectional"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	cfg := loadConfig(t, dir, nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, "aws_vpc_block_public_access_options.nowhere")
	if res.Class == ClassConcrete {
		t.Fatalf("resolved CONCRETE with no region stated anywhere, import ID %q", res.ImportID)
	}
	if res.ImportID != "" {
		t.Errorf("unresolved identity still carries an import ID %q", res.ImportID)
	}
}
