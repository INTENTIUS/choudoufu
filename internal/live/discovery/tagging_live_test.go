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
// It asserts the bind. From the pin's move to
// ghcr.io/lex00/floci@sha256:a1c729f4... this test discovers
// aws_iam_role.demo through the estate-wide sweep and checks its ImportID,
// rather than recording a gap and skipping.
//
// That was not always true, and the history is worth keeping because the
// skip branch below is what a regression would land back on. Through
// sha256:1362e856... floci served GetResources correctly on the wire - once
// the Content-Type fix TestFlociServesTaggingAPI documents was in place -
// but the index it answered from was fed by only 2 of its 64 services, so
// `aws iam create-role ... --tags tofu-estate=...` followed immediately by
// `aws resourcegroupstaggingapi get-resources` returned an empty
// ResourceTagMappingList even though `iam list-role-tags` on the same role
// returned the tags that were written. Issue #229 (2026-08-16) established
// the gap was estate-wide rather than aws_iam_role- or S3-specific:
// tools/floci-capability-gen -mode=tagging drives seven curated recipes
// across seven distinct services (EC2, S3, SQS, SNS, DynamoDB, IAM, Secrets
// Manager), each confirming its own tags natively first, and all seven came
// back empty.
//
// floci's fix (lex00/floci#229) unions that private map with a live read of
// every service's stores through StorageFactory, recognising tags and ARNs
// structurally rather than per service. Re-probed against
// sha256:a1c729f445a96fce8858ac45318d5188b5c2afc76a06e819f234326d52e6bd5f
// on 2026-08-16: the same seven recipes are 7/7 implemented - see
// live/floci-capabilities.json's "tagging-sweep" rows under that digest for
// each one's ARN and native tag confirmation, and
// tools/floci-capability-gen/tagging.go's package-level doc comment for the
// probe's own mechanics.
//
// A separate direct probe on the same digest answered the one thing the
// oracle above cannot, because it sweeps unfiltered while
// sweepViaTagging sends TagFilter{Key: "tofu-estate", Values: [estate]}:
// the union index honours TagFilters. Two SQS queues and one SNS topic
// tagged across two estates plus one untagged topic gave 3 hits unfiltered,
// 2 for tofu-estate=alpha, 1 for tofu-estate=beta, 0 for an absent value, 3
// for a key-only filter and 0 for an absent key, with ResourceTypeFilters
// narrowing correctly too. So the loop at tagging.go's sweepViaTagging does
// not see foreign-estate ARNs and cannot raise spurious
// ProblemUnsweepableOwnedType/ProblemUnresolvedTaggedARN warnings.
//
// The skip branch below is deliberately self-retiring:
// flocitest.TaggingSweepCapabilityGate skips only on unimplemented/broken,
// so an implemented row makes it a no-op and the t.Fatal after it fires.
// Nothing needs editing here if floci regresses or if a future pin loses the
// union index - the manifest row is what decides. See
// testdata/tagging-e2e/main.tf for the type choice (aws_iam_role) and
// TestFlociServesTaggingAPI for the Content-Type finding that makes this
// test reach floci's tagging service at all.
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
		// exact gap. As of the pinned digest it does not: aws_iam_role's
		// tagging-sweep row there is "implemented", so the gate is a no-op
		// and the t.Fatal below is what a miss produces. If the manifest has
		// no matching entry, this also falls through to a real failure
		// instead of a silent skip: an unexplained miss here means either
		// floci's tagging-sweep coverage regressed, or a new gap needs
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
