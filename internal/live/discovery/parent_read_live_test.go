// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/projection"
)

// TestParentReadRemovalAgainstFloci is issue #60's live half: one
// parent-read removal, against a real emulated cloud and a real AWS
// provider plugin rather than the fake one [TestParentReadSweepFindsUndeclaredChild]
// uses. aws_s3_bucket_policy is the type live/LIMITATIONS.md's parent-read
// table wires for removal - bucket admitted and marked, policy readable via
// the bucket, floci serves both.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestParentReadRemovalAgainstFloci -v
//
// The estate goes up with a bucket and a declared policy, stood up by stock
// terraform - discovery has to recover an estate it did not create, the
// same reason [TestDiscoverAgainstFloci] does - and its state is deleted.
// The policy block is then removed from configuration with the bucket left
// standing, and [Discover] runs with the sweep on but narrowed to just the
// two types this test is about ([Request.SweepTypes]): the estate-wide
// sweep otherwise lists every admitted type, including several this floci
// image serves with gaps of their own that have nothing to do with issue
// #60 and would make this test about them instead.
//
// A plan against markers alone would have nothing to say about the
// now-undeclared policy: it carries no tags, so it can carry no marker,
// and the ordinary estate-wide sweep has nothing to search on for it. This
// is exactly the gap the parent-read leg closes - the bucket is marked,
// admitted and still declared, and reading its own identity, against the
// real provider and the real emulator, is enough to find the policy and
// materialize it into the prior state as a destroy.
func TestParentReadRemovalAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "parent-read removal")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-p60")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)
	flocitest.PluginCacheDir(t)

	dir := t.TempDir()
	writeParentReadFixture(t, dir, true)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	if !policyExists(flociPort) {
		t.Fatal("the stock apply did not create a live bucket policy")
	}

	// The non-event: delete the state and carry on as though it had never
	// existed, the same as every other live test in this package.
	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)

	// The policy block is deleted, the bucket is not - the same edit
	// live/e2e/estate's own removal-exactness step makes to a whole other
	// type, narrowed here to the one shape issue #60 is about.
	writeParentReadFixture(t, dir, false)

	cfg := loadConfig(t, dir)
	resolutions := resolveOrFail(t, cfg).All()

	res, diags := Discover(ctx, Request{
		Estate:      "p60-parent-read",
		Config:      cfg,
		Resolutions: resolutions,
		Provider:    provider,
		Region:      awsRegion,
		Sweep:       true,
		// Narrowed sweep universe: see the doc comment above for why the
		// whole admission table is not asked for here.
		SweepTypes: []string{"aws_s3_bucket", "aws_s3_bucket_policy"},
	})
	t.Logf("discovery result:\n%s", res)
	assertNoErrors(t, diags)

	// The bucket resolves concrete straight from its own configuration
	// (client-named: bucket = "..." composes its identity with no
	// discovery needed), the same shape [TestSweepLeavesDeclaredClientNamedResourcesAlone]
	// pins for the fake-provider suite - so it never reaches
	// [Result.Bindings], and this checks the resolution directly instead.
	var bucketResolved bool
	for _, r := range res.Resolutions {
		if r.Addr.String() == "aws_s3_bucket.data" && r.ImportID == parentReadBucket {
			bucketResolved = true
		}
	}
	if !bucketResolved {
		t.Fatalf("the bucket did not resolve to its own configured identity:\n%s", res)
	}

	f, ok := findParentRead(res, "aws_s3_bucket_policy", parentReadBucket)
	if !ok {
		t.Fatalf("no parent-read finding for the bucket's policy:\n%s", res)
	}
	if f.Parent != "aws_s3_bucket" {
		t.Errorf("finding names parent %q, want aws_s3_bucket", f.Parent)
	}
	if f.ParentAddr.String() != "aws_s3_bucket.data" {
		t.Errorf("finding names parent address %q, want aws_s3_bucket.data", f.ParentAddr.String())
	}
	if !f.Removal {
		t.Errorf("aws_s3_bucket_policy finding is not a removal: %+v", f)
	}
	if f.Withheld != "" {
		t.Errorf("a removal finding carries a withheld reason: %q", f.Withheld)
	}

	wantAddr := mustAddr(t, "aws_s3_bucket_policy.data")
	var foundResolution bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != wantAddr.String() {
			continue
		}
		foundResolution = true
		if r.ImportID != parentReadBucket {
			t.Errorf("resolution carries import ID %q, want %q", r.ImportID, parentReadBucket)
		}
		if !r.Undeclared {
			t.Error("the parent-read removal's resolution is not marked Undeclared")
		}
	}
	if !foundResolution {
		t.Fatalf("no resolution was produced at aws_s3_bucket_policy.data:\n%s", res)
	}

	// End to end: the projection materializes the policy from the live
	// read, with no configuration behind it, exactly the shape a deleted
	// block's prior state has for every other removal in this fork.
	provs := projection.SingleProvider(addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.NewDefaultProvider("aws"),
	}, provider)
	proj, projDiags := projection.BuildFrom(ctx, cfg, res.Resolutions, provs)
	assertNoErrors(t, projDiags)

	is := proj.State.ResourceInstance(wantAddr)
	if is == nil || is.Current == nil {
		t.Fatalf("aws_s3_bucket_policy.data did not materialize into the prior state:\n%s", res)
	}
	if len(is.Current.AttrsJSON) < 2 {
		t.Error("aws_s3_bucket_policy.data materialized as an empty object")
	}
}

const parentReadBucket = "p60-parent-read-data"

// writeParentReadFixture writes the estate: one bucket, and (when
// withPolicy) one policy on it. The bucket carries an explicit
// tofu-estate/tofu-address tag pair, the ordinary marker path; the policy
// carries none at all - deliberately, since the whole point is that
// discovery recovers the bucket the ordinary way and the policy rides along
// on a read of it, with nothing of its own to find it by.
func writeParentReadFixture(t *testing.T, dir string, withPolicy bool) {
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

  s3_use_path_style = true
}

resource "aws_s3_bucket" "data" {
  bucket = %q

  tags = {
    tofu-estate  = "p60-parent-read"
    tofu-address = "aws_s3_bucket.data"
  }
}
`, parentReadBucket)

	if withPolicy {
		src += `
resource "aws_s3_bucket_policy" "data" {
  bucket = aws_s3_bucket.data.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "AllowRead"
      Effect    = "Allow"
      Principal = "*"
      Action    = ["s3:GetObject"]
      Resource  = "${aws_s3_bucket.data.arn}/*"
    }]
  })
}
`
	}

	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil { //nolint:gosec // this test's own temp dir
		t.Fatalf("writing the fixture: %v", err)
	}
}

func policyExists(flociPort string) bool {
	full := []string{"--endpoint-url", flocitest.Endpoint(flociPort), "s3api", "get-bucket-policy", "--bucket", parentReadBucket}
	_, err := exec.Command("aws", full...).Output() //nolint:gosec // fixed binary, test-only
	return err == nil
}
