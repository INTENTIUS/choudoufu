// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lifecycle

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestMarkerSurvivesIncrementalTagUpdate is GitHub issue #306's regression
// pin: a stamped resource that ALSO declares its own explicit "tags"
// argument must keep its tofu-estate/tofu-address markers through an
// ordinary apply that changes one of its own declared tags. #306 reported
// the opposite: a clean "Apply complete!" exit whose plan showed the
// markers unchanged, but whose live object afterward carried only the
// tag that actually changed - every other tag, including both markers,
// silently gone.
//
// The investigation this test pins down: it is not choudoufu's own
// config-synthesis stamping (internal/live/stamp), not
// internal/live/projection/build.go's configuredTagsSeed, and not a stale
// config reaching NodeApplyableResourceInstance's apply-time re-plan - all
// three were the seams issue #306 named as suspects, and all three turned
// out innocent: the same drop reproduces under vanilla, unmodified
// Terraform with a real, persisted state file and no choudoufu anywhere in
// the loop, given the identical sequence (a bucket already carrying tags,
// then one more tag added to its config). See this test's own log output
// for the same assertion driven through choudoufu.
//
// The actual defect is one layer further out: terraform-provider-aws
// (v6.58+) applies an incremental tag change to an S3 bucket through S3
// Control's TagResource action, sending only the tags that changed and
// relying on TagResource's real, documented merge/upsert semantics to
// leave every other tag alone - the same way UntagResource is a
// read-modify-write rather than a wholesale replace. floci's emulation of
// TagResource used to call straight into a replace-the-whole-set helper,
// so any tag not part of that one delta - including both of choudoufu's
// own ownership markers - was deleted by the very next incremental update.
// Fixed in the lex00/floci fork (S3ControlController.tagResource: read the
// current tags, merge the request's tags into them, write the merged set
// back - mirroring untagResource's own read-modify-write immediately below
// it), with its own regression test
// (S3ControlTagResourceMergeTest#tagResourceMergesRatherThanReplaces).
//
// This test exists on the choudoufu side too because the failure is only
// observable at the full crossing: the plan choudoufu shows is correct
// either way, so nothing here can be a unit test of choudoufu's own code -
// the drop happens beneath choudoufu, between the provider and the
// emulator, on the specific wire shape a stamped resource's own declared
// tags argument produces. Once live/floci-image is repinned to an image
// carrying the floci-side fix, this test is what proves the crossing sees
// it.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/lifecycle/ -run TestMarkerSurvivesIncrementalTagUpdate -v
func TestMarkerSurvivesIncrementalTagUpdate(t *testing.T) {
	flocitest.Gate(t, "marker survives an incremental tag update (#306)")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "aws")
	flocitest.RequireBinary(t, "go")

	port := flocitest.StartFloci(t, "cdf-306-tagmerge")

	// A bare host:port endpoint is not enough here on purpose: the AWS
	// provider's S3 Control calls (which it issues for a plain
	// aws_s3_bucket's tag read/write, in v6.58+) address themselves at
	// "<account-id>.<host>" - a real DNS label, not a path - so pointing
	// AWS_ENDPOINT_URL at a bare 127.0.0.1 or "localhost" host makes every
	// such call fail DNS resolution before it ever reaches floci, which
	// would make this test pass for a reason that has nothing to do with
	// #306 (see live/e2e/corpus-s3-bucket-complete/run.sh's own comment on
	// the same endpoint choice, and this fork's earlier, wrongly-negative
	// attempt to reproduce #306 without it).
	// localhost.localstack.cloud is a public wildcard-DNS name resolving
	// every subdomain to 127.0.0.1, which floci's port is bound on.
	endpoint := "http://localhost.localstack.cloud:" + port

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "eu-west-1")
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := t.TempDir()

	const (
		estateName = "tagmerge-306"
		bucketN    = "cdf-306-tagmerge-bucket"
	)

	write := func(tagsBody string) {
		t.Helper()
		content := fmt.Sprintf(`terraform {
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
  region                       = "eu-west-1"
  skip_credentials_validation  = true
  skip_metadata_api_check      = true
  skip_region_validation       = true
  s3_use_path_style            = true
}

resource "aws_s3_bucket" "b" {
  bucket = %q
  tags = {
%s
  }
}
`, estateName, bucketN, tagsBody)
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o600); err != nil {
			t.Fatalf("writing the fixture: %v", err)
		}
	}

	// A local aws-CLI helper rather than the package's shared awsJSON/
	// awsOutput: those hardcode the package-level flociPort var, which
	// belongs to TestStatelessLifecycleAgainstFloci's own container
	// ("no other test in this package touches them", by that var's own
	// doc comment) and is wired to a bare "http://localhost:PORT" endpoint
	// besides - exactly the shape this test exists to avoid, since it
	// would never route through S3 Control at all.
	readTags := func() map[string]string {
		t.Helper()
		out, err := exec.Command("aws", "--endpoint-url", endpoint,
			"s3api", "get-bucket-tagging", "--bucket", bucketN,
			"--query", "TagSet[].{Key:Key,Value:Value}", "--output", "json").Output()
		if err != nil {
			t.Fatalf("aws s3api get-bucket-tagging: %v\n%s", err, out)
		}
		return decodeTagList(t, strings.TrimSpace(string(out)))
	}

	// --- 1. First apply: create the bucket, own declared tag, stamped ----

	write(`    Owner = "Anton"`)

	tofu(t, tofuBin, dir, "init", "-input=false", "-no-color")

	first := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(first, "Apply complete!") {
		t.Fatalf("the first apply did not complete:\n%s", first)
	}
	added, changed, destroyed, ok := applySummary(first)
	if !ok || added != 1 || changed != 0 || destroyed != 0 {
		t.Fatalf("want 1 added / 0 changed / 0 destroyed, got %d/%d/%d (ok=%v):\n%s",
			added, changed, destroyed, ok, first)
	}

	afterCreate := readTags()
	assertTags(t, afterCreate, "aws_s3_bucket.b", map[string]string{
		"Owner":        "Anton",
		"tofu-estate":  estateName,
		"tofu-address": "aws_s3_bucket.b",
	})

	// --- 2. Add a genuinely new declared tag, forcing an incremental ------
	//        update - the shape that sends only the delta over the wire.

	write(`    Owner = "Anton"
    Team  = "Ops"`)

	plan := tofu(t, tofuBin, dir, "plan")
	block := flocitest.ResourceBlock(t, plan, "aws_s3_bucket.b")
	for _, want := range []string{`"Team"`, `"Owner"`, `"tofu-address"`, `"tofu-estate"`} {
		if !strings.Contains(block, want) {
			t.Errorf("the shown plan's tags diff does not mention %s:\n%s", want, block)
		}
	}

	second := tofu(t, tofuBin, dir, "apply", "-auto-approve")
	if !strings.Contains(second, "Apply complete!") {
		t.Fatalf("the second apply did not complete:\n%s", second)
	}
	added, changed, destroyed, ok = applySummary(second)
	if !ok || added != 0 || changed != 1 || destroyed != 0 {
		t.Fatalf("want 0 added / 1 changed / 0 destroyed, got %d/%d/%d (ok=%v):\n%s",
			added, changed, destroyed, ok, second)
	}

	// --- 3. The load-bearing assertion: read the LIVE object directly, ---
	//        never through choudoufu's own report of what it did.

	afterUpdate := readTags()
	assertTags(t, afterUpdate, "aws_s3_bucket.b", map[string]string{
		"Owner":        "Anton",
		"Team":         "Ops",
		"tofu-estate":  estateName,
		"tofu-address": "aws_s3_bucket.b",
	})
	if len(afterUpdate) != 4 {
		t.Errorf("want exactly 4 live tags after the incremental update, got %d: %v", len(afterUpdate), afterUpdate)
	}

	// A third, no-op plan converges: the markers this test cares about were
	// never lost from choudoufu's own point of view either.
	converged := tofu(t, tofuBin, dir, "plan")
	if !strings.Contains(converged, "No changes.") {
		t.Errorf("the estate did not converge after the incremental update:\n%s", converged)
	}
}
