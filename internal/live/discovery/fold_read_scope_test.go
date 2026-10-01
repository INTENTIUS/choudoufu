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

	"github.com/intentius/choudoufu/internal/addrs"
)

// GitHub issue #1721's sibling, found by #1729's worker: the fold-child leg
// read a declared parent's child in every pass of a multi-provider root,
// where the parent-read leg reads only in the pass whose provider
// configuration the parent's block names. With two aws configurations,
// both passes read the same method's integration, the second read against
// an account or region the method is not declared in.
//
// The method's rest_api_id is a literal, so its identity is concrete from
// configuration in every pass and nothing but a scope check stops a pass
// from reading through it.
func TestFoldChildReadSweepReadsOnlyInTheParentsProviderPass(t *testing.T) {
	dir := t.TempDir()
	src := `
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {}

provider "aws" {
  alias = "west"
}

resource "aws_api_gateway_method" "app" {
  provider      = aws.west
  rest_api_id   = "api-123"
  resource_id   = "root-resource"
  http_method   = "GET"
  authorization = "NONE"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	var passes []Pass
	perPass := map[string]int{}
	for _, prov := range []addrs.AbsProviderConfig{awsDefaultProv, awsWestProv} {
		provider := newFoldFakeProvider()
		provider.liveIntegration()
		res, diags := Discover(context.Background(), Request{
			Estate:        estateName,
			Config:        cfg,
			Resolutions:   resolutions,
			Provider:      provider,
			Sweep:         true,
			SweepTypes:    []string{"aws_api_gateway_integration"},
			ScopeProvider: prov,
			VouchProvider: prov,
		})
		assertNoErrors(t, diags)
		for _, f := range res.ParentReads {
			if f.TypeName == "aws_api_gateway_integration" {
				perPass[prov.String()]++
			}
		}
		passes = append(passes, Pass{Provider: prov, Result: res})
	}
	merged, _, mdiags := Merge(estateName, passes, false)
	assertNoErrors(t, mdiags)

	var findings int
	for _, f := range merged.ParentReads {
		if f.TypeName == "aws_api_gateway_integration" {
			findings++
		}
	}
	t.Logf("integration findings per pass: %v", perPass)
	if findings != 1 {
		t.Errorf("the merge carries %d fold-read findings for the one integration, want exactly 1", findings)
	}
	if perPass[awsDefaultProv.String()] != 0 || perPass[awsWestProv.String()] != 1 {
		t.Errorf("findings per pass = %v, want the integration read only through %s, the method's own configuration", perPass, awsWestProv)
	}
}
