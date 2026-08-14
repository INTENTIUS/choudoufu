// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package acceptance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestSynthesizedCompositeEndToEnd is GitHub issue #105's third acceptance
// criterion, the half its closing comment deferred to this tier: a
// schema-synthesized composite admitted, applied against a cloud, and
// replanned empty from markers alone.
//
// The fixture's aws_s3_object is in neither generated table; its admission
// is SynthesizeTypeIdentity reading the provider's own identity schema
// (required: bucket, key), and #105 made that entry IdentityObjectOnly - no
// import-ID string exists for it at all. So an empty replan here is not
// merely "the object was found": the only route to the live object is the
// identity-object import, and the meaningless concatenated fallback
// ("tofu-synth-composite-e2edoc.txt") that criterion 1 guarded against
// would read nothing and surface as a create in this plan.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/acceptance -run TestSynthesizedCompositeEndToEnd -v -timeout 30m
func TestSynthesizedCompositeEndToEnd(t *testing.T) {
	flocitest.Gate(t, "synthesized composite")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort := flocitest.StartFloci(t, "cdf-synth")
	t.Setenv("AWS_ENDPOINT_URL", flocitest.Endpoint(flociPort))
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_REGION", "us-east-1")
	flocitest.PluginCacheDir(t)
	tofuBin := flocitest.BuildTofu(t)

	src := filepath.Join(flocitest.RepoRoot(t), "internal", "live", "acceptance", "testdata", "synth-composite")
	dir := flocitest.CopyFixtureDir(t, src)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	// The object really exists, checked with no choudoufu code in the path.
	body := flocitest.AWSCLI(t, flociPort, "s3api", "head-object",
		"--bucket", "tofu-synth-composite-e2e", "--key", "doc.txt",
		"--query", "ContentLength", "--output", "text")
	t.Logf("head-object ContentLength: %s", body)

	for _, f := range []string{"terraform.tfstate", "terraform.tfstate.backup"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("removing %s: %v", f, err)
		}
	}

	out, err, timedOut := runTimed(t, dir, planTimeout, tofuBin, "live-plan", "-estate=synth-composite-e2e", "-input=false", "-no-color")
	t.Logf("live-plan:\n%s", out)
	if err != nil {
		t.Fatalf("live-plan failed (timed out: %t): %v", timedOut, err)
	}

	if add, change, destroy, ok := flocitest.PlanSummary(out); ok && add+change+destroy > 0 {
		t.Fatalf("the replan is not empty: %s\nchanged: %v", planSummaryOf(out), flocitest.ChangedResources(out))
	}
	if !strings.Contains(out, "No changes.") {
		t.Fatalf("live-plan reported neither changes nor \"No changes.\"")
	}
	for _, addr := range []string{"aws_s3_bucket.holder", "aws_s3_object.doc"} {
		if strings.Contains(out, "# "+addr+" will be") {
			t.Errorf("%s is proposed for a change; it exists and carries this estate's markers", addr)
		}
	}
}
