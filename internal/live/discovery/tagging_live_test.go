// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/registry"
)

// TestFlociServesTaggingAPI is the "curl the endpoint shape" probe issue #51
// asks for before trusting the gated e2e test below to say anything about
// floci's real behavior: one real GetResources call against a bare
// emulator, independent of terraform or the AWS provider, so a failure here
// is unambiguously about floci's tagging support and not about anything
// else this fixture does. If this fails, TestTaggingSweepAgainstFloci's
// finding is moot and should be read as "floci does not serve the tagging
// API at all" rather than the narrower "does not yet reflect resources"
// gap that test documents.
//
// Evidence recorded from this probe (floci 1.5.33, ghcr.io/lex00/floci,
// checked 2026-08-12): /_localstack/health lists "tagging": "running", and a
// GetResources call succeeds - but only once the request's Content-Type
// says "application/x-amz-json-1.1", the Resource Groups Tagging API's real
// protocol version (confirmed against botocore's own
// resourcegroupstaggingapi service model: jsonVersion "1.1", vs. Cloud
// Control's "1.0"). The identical call with "1.0" - what this package's
// Client sent before this issue fixed it in cloudcontrol/client.go and
// tagging.go - comes back {"__type":"UnknownOperationException",...} even
// though X-Amz-Target already names GetResources correctly. See
// TestGetResourcesHitsTaggingTarget in cloudcontrol/tagging_test.go for the
// unit-level pin of the fix.
func TestFlociServesTaggingAPI(t *testing.T) {
	flocitest.Gate(t, "discovery/tagging")
	flocitest.RequireBinary(t, "docker")

	flociPort := flocitest.StartFloci(t, "cdf-tagging51-probe")
	endpoint := flocitest.Endpoint(flociPort)

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: awsRegion})
	res, err := tagging.GetResources(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("floci does not serve the Resource Groups Tagging API's GetResources: %v", err)
	}
	t.Logf("floci served GetResources: %d resources on a fresh emulator", len(res))
}

// TestTaggingSweepAgainstFloci is issue #51's e2e case, mirroring
// cloudcontrol_live_test.go's TestDiscoverCloudControlFallbackAgainstFloci:
// real terraform, real AWS provider, real floci - but through
// Request.TaggingSweep and cloudcontrol.Client.GetResources rather than
// Cloud Control's ListResources.
//
// It inherits that test's skip condition rather than avoiding it. Manual
// probing (floci 1.5.33, 2026-08-12, same session as the Content-Type
// finding TestFlociServesTaggingAPI documents) found the Resource Groups
// Tagging API answers GetResources correctly on the wire - once the
// Content-Type fix is in place - but does not yet reflect resources tagged
// through the ordinary service APIs, the same gap class
// cloudcontrol_live_test.go documents for Cloud Control's ListResources
// rather than a different one: `aws iam create-role ... --tags
// tofu-estate=...` followed immediately by `aws resourcegroupstaggingapi
// get-resources` returns an empty ResourceTagMappingList, and the same is
// true of an S3 bucket tagged via put-bucket-tagging - even though
// `iam list-role-tags` on the same role correctly returns the tags that
// were written. So this test runs the real pipeline (terraform apply,
// GetResources against real floci, the ARN join, Discover) and asserts the
// scan happened through SourceTagging, then skips with this evidence
// recorded rather than asserting a bind floci cannot currently deliver. See
// testdata/tagging-e2e/main.tf for the type choice (aws_iam_role) and
// TestFlociServesTaggingAPI for the Content-Type finding that makes this
// test reach floci's tagging service at all.
//
// Issue #229 (2026-08-16) established that the gap is not aws_iam_role- or
// S3-specific, or even the two-type coincidence this comment used to read
// as: tools/floci-capability-gen -mode=tagging drives seven curated
// recipes across seven distinct services (EC2, S3, SQS, SNS, DynamoDB, IAM,
// Secrets Manager), each confirming its own tags natively before checking
// an unfiltered GetResources sweep, and all seven came back empty against
// ghcr.io/lex00/floci@sha256:1362e856... - the estate-wide tagging index
// itself is what floci does not populate, not any one service's tagging
// call. See live/floci-capabilities.json's "tagging-sweep" rows for the
// full evidence and tools/floci-capability-gen/tagging.go's package-level
// doc comment for the probe's own mechanics.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestTaggingSweepAgainstFloci -v
func TestTaggingSweepAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/tagging")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, terraformBin)

	ctx := context.Background()
	flociPort := flocitest.StartFloci(t, "cdf-tagging51")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", awsRegion)

	dir := copyFixture(t, filepath.Join(flocitest.RepoRoot(t), "internal", "live", "discovery", "testdata", "tagging-e2e"))
	flocitest.PluginCacheDir(t)
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	provider := launchAWSProvider(t, dir)
	cfg := loadConfig(t, dir)

	roster, err := registry.Load(
		filepath.Join(flocitest.RepoRoot(t), "live", "mapping.json"),
		filepath.Join(flocitest.RepoRoot(t), "live", "registry.json"),
	)
	if err != nil {
		t.Fatalf("loading the real live/mapping.json and live/registry.json: %v", err)
	}

	tagging := cloudcontrol.NewTagging(cloudcontrol.Config{Endpoint: endpoint, Region: awsRegion})

	// The role is not in this pass's declared config at all - the whole
	// point is that the estate-wide sweep finds it with nothing waiting on
	// discovery, the same shape TestSweepFindsDeletedBlock exercises against
	// a fake cloud. Resolutions is empty, and Sweep is what does the work.
	res, diags := Discover(ctx, Request{
		Estate:       "tagging-e2e",
		Config:       cfg,
		Resolutions:  nil,
		Provider:     provider,
		Region:       awsRegion,
		Sweep:        true,
		SweepTypes:   []string{"aws_iam_role"},
		Tagging:      tagging,
		TaggingSweep: true,
		Roster:       roster,
	})
	t.Logf("discovery result:\n%s", res)
	assertNoErrors(t, diags)

	scan, ok := res.ScanFor("aws_iam_role")
	if !ok {
		t.Fatal("no scan was recorded for aws_iam_role at all")
	}
	if scan.Source != SourceTagging {
		t.Fatalf("aws_iam_role scan source = %q, want %q", scan.Source, SourceTagging)
	}

	rm := removalsByAddr(res)
	o, ok := rm[`aws_iam_role.demo`]
	if !ok {
		// flocitest.TaggingSweepCapabilityGate skips with a loud, digest-cited
		// reason when live/floci-capabilities.json already explains this
		// exact gap (it does, as of the pinned digest - see this file's own
		// doc comment for the full probe notes that entry is built from). If
		// the manifest has no matching entry, this falls through to a real
		// failure instead of a silent skip: an unexplained miss here means
		// either floci's tagging-sweep coverage regressed, or a new gap needs
		// investigating and recording, not waving through by hand again.
		flocitest.TaggingSweepCapabilityGate(t, "aws_iam_role")
		t.Fatal("aws_iam_role.demo was not discovered through the estate-wide tagging sweep against real floci, " +
			"and live/floci-capabilities.json has no entry explaining why for this floci image - investigate and " +
			"record the finding there (tools/floci-capability-gen's doc comment) rather than skip unexplained")
	}
	if !strings.Contains(o.ImportID, "tagging-e2e-demo") {
		t.Errorf("ImportID = %q, want it to name the role tagging-e2e-demo", o.ImportID)
	}
	if strings.Contains(o.ImportID, "arn:") {
		t.Errorf("ImportID = %q carries a raw ARN; aws_iam_role's identity is its name, not the ARN itself", o.ImportID)
	}
	if !o.Swept {
		t.Error("the removal is not marked as found by the sweep")
	}

	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after discovery (err = %v)", err)
	}
}
