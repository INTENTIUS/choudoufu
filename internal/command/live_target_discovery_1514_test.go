// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
)

// targetDiscoveryFixture is GitHub issue #1514's configuration: a bucket
// the -target run is about, and an excluded certificate whose identity is
// server-assigned (so it needs marker discovery) and whose type the fake
// cloud serves no list resource for.
const targetDiscoveryFixture = "live-plan-target-discovery-1514"

// targetDiscoverySchemas is [statelessTestSchemas]'s caricature widened by
// the excluded certificate. It carries tags, so the certificate is a
// marker-discovered type; [statelessTestListSchemas] has no entry for it,
// which is what makes it unlistable here.
func targetDiscoverySchemas() map[string]providers.Schema {
	out := statelessTestSchemas()
	out["aws_acm_certificate"] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                {Type: cty.String, Computed: true},
		"arn":               {Type: cty.String, Computed: true},
		"domain_name":       {Type: cty.String, Optional: true},
		"validation_method": {Type: cty.String, Optional: true},
		"tags":              {Type: cty.Map(cty.String), Optional: true},
	}}}
	return out
}

// TestLivePlan_targetIsNotRefusedByAnExcludedBlocksDiscoveryNeed is GitHub
// issue #1514, end to end, in the two arms
// TestLivePlan_targetIsNotRefusedByAnExcludedForEachTheSecondPassCannotSettle
// uses.
//
// Untargeted, the certificate's discovery need is the run's own, and a
// type the cloud cannot list refuses it. That arm is the mutation check on
// the second: a fixture that had stopped refusing would let the targeted
// run pass for no reason.
//
// Targeted at the bucket, the run must succeed. Before the fix it exited 1
// with "Unlistable marker-discovered type" for aws_acm_certificate.cert, a
// block the plan graph had already dropped, because [statelessDiscover]
// took its needs-discovery set from the whole configuration.
func TestLivePlan_targetIsNotRefusedByAnExcludedBlocksDiscoveryNeed(t *testing.T) {
	run := func(t *testing.T, args ...string) (int, *terminal.TestOutput, *statelessTestCloud) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath(targetDiscoveryFixture), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		cloud.schemas = targetDiscoverySchemas()
		cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
			"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
		})
		c, done := newLivePlanCommand(t, cloud)
		code := c.Run(append([]string{"-no-color", "-estate=stateless-unit"}, args...))
		return code, done(t), cloud
	}

	t.Run("untargeted still refuses", func(t *testing.T) {
		code, output, _ := run(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if !strings.Contains(output.Stderr(), "Unlistable marker-discovered type") {
			t.Errorf("an untargeted run no longer refuses the certificate as unlistable, so the targeted case below proves nothing:\n%s", output.Stderr())
		}
	})

	t.Run("targeted proceeds", func(t *testing.T) {
		code, output, cloud := run(t, "-target=aws_s3_bucket.data")
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - a -target run refused for a block it excludes\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if strings.Contains(output.Stderr(), "Unlistable marker-discovered type") {
			t.Errorf("a targeted run still refused on a discovery need outside its own target set:\n%s", output.Stderr())
		}
		if !strings.Contains(output.Stdout(), "No changes.") {
			t.Errorf("the bucket's plan is not empty:\n%s", output.Stdout())
		}
		if !cloud.imported("aws_s3_bucket", "tofu-stateless-unit-data") {
			t.Errorf("the bucket was never read from the live system; imports were %v", cloud.imports)
		}
	})
}

// TestLivePlan_targetIsNotRefusedByAnExcludedBlocksDiscoveryProvider is
// the other half of GitHub issue #1514: the provider set whose failure is
// fatal. [statelessDiscoverProviderUnavailable] downgrades a provider it
// cannot configure only when no needs-discovery instance uses it, and that
// set came from the whole configuration too. Here the excluded
// certificate's provider reads a managed attribute, so it cannot be
// configured before apply; untargeted that is the certificate's own
// identity and stays fatal, and under -target it is a block the run
// excludes and must not refuse the run.
func TestLivePlan_targetIsNotRefusedByAnExcludedBlocksDiscoveryProvider(t *testing.T) {
	const fixture = "live-plan-target-discovery-provider-1514"
	const notEvaluable = summaryProviderConfigNotEvaluableForSweep
	run := func(t *testing.T, args ...string) (int, *terminal.TestOutput) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath(fixture), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		cloud.schemas = targetDiscoverySchemas()
		cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
			"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
		})
		c, done := newLivePlanCommand(t, cloud)
		code := c.Run(append([]string{"-no-color", "-estate=stateless-unit"}, args...))
		return code, done(t)
	}

	t.Run("untargeted still refuses", func(t *testing.T) {
		code, output := run(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if !strings.Contains(output.Stderr(), notEvaluable) {
			t.Errorf("an untargeted run no longer refuses the certificate's unconfigurable provider, so the targeted case below proves nothing:\n%s", output.Stderr())
		}
	})

	t.Run("targeted proceeds", func(t *testing.T) {
		code, output := run(t, "-target=aws_s3_bucket.data")
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - a -target run refused over a provider only an excluded block needs\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if strings.Contains(output.Stderr(), notEvaluable) {
			t.Errorf("a targeted run still refused on a provider outside its own target set:\n%s", output.Stderr())
		}
		if !strings.Contains(output.Stdout(), "Provider unavailable for the estate-wide sweep") {
			t.Errorf("the skipped pass was not reported; a sweep that silently lists nothing is the failure the warning exists to prevent:\n%s", output.Stdout())
		}
		if !strings.Contains(output.Stdout(), "No changes.") {
			t.Errorf("the bucket's plan is not empty:\n%s", output.Stdout())
		}
	})
}
