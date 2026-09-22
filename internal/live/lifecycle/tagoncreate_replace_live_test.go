// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestTagOnCreateReplace is GitHub issue #1512's live pin: #1084's ruling
// ("the node writer withholds tags from the create call and the live path
// issues the tag write immediately after") on the create half of a
// REPLACE, which #1489 left planned with the markers in the value because
// the prior object exists at plan time.
//
// Four phases, each its own one-resource estate on one emulator:
//
//  1. delete-then-create: a zone replaced by a `name` change (ForceNew).
//     No request the provider makes may carry tofu-estate, this fork's
//     TagResources runs exactly once, the new zone carries both markers
//     through both tag views by the end of the apply, and the next plan
//     binds it by its marker.
//
//  2. create_before_destroy: the same, with the lifecycle flag set.
//
//  3. refused: the replace's tag writes are refused. The apply error names
//     the new zone by ARN and prints the command that marks it (#1489's
//     error); running it and planning again binds the zone.
//
//  4. an ordinary type (aws_s3_bucket, tag_on_create true) replaced by a
//     `bucket` change: the markers still go out through the provider's
//     own calls, and this fork makes no TagResources call.
//
//     TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestTagOnCreateReplace -v
func TestTagOnCreateReplace(t *testing.T) {
	flocitest.Gate(t, "tag on create, replace (#1512)")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	port := flocitest.StartFloci(t, "cdf-1512-replace")
	floci := flocitest.Endpoint(port)
	proxy := newTagWriteProxy(t, floci)

	t.Setenv("AWS_ENDPOINT_URL", proxy.endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)

	zoneReplace := func(t *testing.T, estate, lifecycle string) {
		zone1 := estate + "-1.example."
		zone2 := estate + "-2.example."
		dir := t.TempDir()
		trWriteZone(t, dir, estate, zone1, lifecycle)
		tofu(t, tofuBin, dir, "init")
		tofu(t, tofuBin, dir, "apply", "-auto-approve")

		trWriteZone(t, dir, estate, zone2, lifecycle)
		proxy.reset()
		applied := tofu(t, tofuBin, dir, "apply", "-auto-approve")
		added, changed, destroyed, ok := applySummary(applied)
		if !ok || added != 1 || changed != 0 || destroyed != 1 {
			t.Fatalf("replace: want 1 added / 0 changed / 1 destroyed, got %d/%d/%d (ok=%v)", added, changed, destroyed, ok)
		}
		zoneID := tocZoneID(t, floci, zone2)
		arn := "arn:aws:route53:::hostedzone/" + zoneID

		if w := proxy.providerMarkerWrites(); len(w) != 0 {
			t.Errorf("replace: the provider's own requests carried the ownership markers (the create half was planned with them): %v", w)
		}
		for i, body := range proxy.hostedZoneTagWrites() {
			if strings.Contains(body, "tofu-estate") || strings.Contains(body, "tofu-address") {
				t.Errorf("replace: ChangeTagsForResource request %d carried an ownership marker:\n%s", i, body)
			}
		}
		if n := proxy.count("ResourceGroupsTaggingAPI_20170126.TagResources"); n != 1 {
			t.Errorf("replace: want exactly 1 TagResources call from this run, saw %d (actions: %v)", n, proxy.actions())
		}
		want := map[string]string{"tofu-estate": estate, "tofu-address": "aws_route53_zone.this"}
		assertTags(t, tocSweptTags(t, floci, arn), "aws_route53_zone.this (new zone, get-resources)", want)
		assertTags(t, tocRoute53Tags(t, floci, zoneID), "aws_route53_zone.this (new zone, route53 list-tags-for-resource)", want)

		tocAssertBound(t, "the plan after the replace", tofu(t, tofuBin, dir, "plan"))
	}

	t.Run("delete-then-create", func(t *testing.T) {
		zoneReplace(t, "cdf-1512-dtc", "")
	})
	t.Run("create-before-destroy", func(t *testing.T) {
		zoneReplace(t, "cdf-1512-cbd", "create_before_destroy = true")
	})

	t.Run("refused", func(t *testing.T) {
		const estate = "cdf-1512-refused"
		zone1 := estate + "-1.example."
		zone2 := estate + "-2.example."
		dir := t.TempDir()
		trWriteZone(t, dir, estate, zone1, "")
		tofu(t, tofuBin, dir, "init")
		tofu(t, tofuBin, dir, "apply", "-auto-approve")

		trWriteZone(t, dir, estate, zone2, "")
		proxy.reset()
		proxy.setRefuse(true)
		refused, err := tocRun(t, tofuBin, dir, "apply", "-auto-approve")
		proxy.setRefuse(false)
		if err == nil {
			t.Fatalf("the replace succeeded with every tag write refused:\n%s", refused)
		}
		zoneID := tocZoneID(t, floci, zone2)
		arn := "arn:aws:route53:::hostedzone/" + zoneID

		if !strings.Contains(refused, "Created object is not marked") {
			t.Errorf("the replace's refused write is not #1489's error (%q)", "Created object is not marked")
		}
		if !strings.Contains(refused, arn) {
			t.Errorf("the apply error does not name the new zone by its ARN %s", arn)
		}
		command := tocMarkCommand(refused)
		if command == "" {
			t.Fatalf("the apply error prints no `aws resourcegroupstaggingapi tag-resources` command:\n%s", refused)
		}
		if !strings.Contains(command, arn) || !strings.Contains(command, "tofu-estate="+estate) || !strings.Contains(command, "tofu-address=aws_route53_zone.this") {
			t.Errorf("the printed command does not carry the ARN and both markers: %s", command)
		}
		tocRunAWSCommand(t, floci, command)
		tocAssertBound(t, "the plan after the hand-run command", tofu(t, tofuBin, dir, "plan"))
	})

	t.Run("ordinary-type", func(t *testing.T) {
		const estate = "cdf-1512-bucket"
		dir := t.TempDir()
		trWriteBucket(t, dir, estate, estate+"-1")
		tofu(t, tofuBin, dir, "init")
		tofu(t, tofuBin, dir, "apply", "-auto-approve")

		trWriteBucket(t, dir, estate, estate+"-2")
		proxy.reset()
		applied := tofu(t, tofuBin, dir, "apply", "-auto-approve")
		added, changed, destroyed, ok := applySummary(applied)
		if !ok || added != 1 || changed != 0 || destroyed != 1 {
			t.Fatalf("replace: want 1 added / 0 changed / 1 destroyed, got %d/%d/%d (ok=%v)", added, changed, destroyed, ok)
		}
		if w := proxy.providerMarkerWrites(); len(w) == 0 {
			t.Errorf("the bucket's replace sent no ownership marker through the provider: tag_on_create true types must keep the markers in the create")
		} else {
			t.Logf("provider requests carrying the markers: %v", w)
		}
		if n := proxy.count("ResourceGroupsTaggingAPI_20170126.TagResources"); n != 0 {
			t.Errorf("want no TagResources call for a tag_on_create true type, saw %d", n)
		}
		tocAssertBound(t, "the plan after the bucket replace", tofu(t, tofuBin, dir, "plan"))
	})
}

func trProvider(estate string) string {
	return fmt.Sprintf(`terraform {
  required_version = ">= 1.5.0"

  live {
    estate = %q
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}
`, estate)
}

func trWriteZone(t *testing.T, dir, estate, zone, lifecycle string) {
	t.Helper()
	body := ""
	if lifecycle != "" {
		body = "\n  lifecycle {\n    " + lifecycle + "\n  }\n"
	}
	content := trProvider(estate) + fmt.Sprintf(`
resource "aws_route53_zone" "this" {
  name = %q
%s}
`, zone, body)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}

func trWriteBucket(t *testing.T, dir, estate, bucket string) {
	t.Helper()
	content := trProvider(estate) + fmt.Sprintf(`
resource "aws_s3_bucket" "this" {
  bucket = %q
}
`, bucket)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
		t.Fatalf("writing the fixture: %v", err)
	}
}
