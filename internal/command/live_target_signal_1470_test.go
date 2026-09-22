// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
)

// GitHub issue #1470, end to end. TestATargetedRunIsNotRefusedByAnExcludedForEach
// pins the shape on a fixture where [statelessResolve]'s second pass
// happens to clear the excluded block's refusal, because
// [projection.PlanInstances] plans the certificate the record reads. This
// file is the fixture where it cannot: the certificate is a for_each block,
// which PlanInstances never plans, so the second pass is handed nothing
// about domain_validation_options and the first pass's verdict is the
// run's.
//
// The scope is the plan graph's own, computed inside live-plan by
// [statelessTargetScope], and the assertion is the exit code of the whole
// command: a -target run refused for a block it excludes is #352's shape,
// and the operator sees the exit code, not a resolver's diagnostic count.

// targetSignalFixture is the configuration every reading in this file is
// taken on.
const targetSignalFixture = "live-plan-target-signal-1470"

// targetSignalSchemas is [statelessTestSchemas]'s caricature widened by the
// two types the fixture's excluded pair declares. domain_validation_options
// is computed and never set by the fake, so a run that DID plan the
// certificate would still learn nothing from it; the structural reason the
// second pass cannot settle the record is the certificate's own for_each,
// and the schema is only here so the provider admits the blocks at all.
func targetSignalSchemas() map[string]providers.Schema {
	out := statelessTestSchemas()
	optionType := cty.Object(map[string]cty.Type{
		"domain_name":           cty.String,
		"resource_record_name":  cty.String,
		"resource_record_type":  cty.String,
		"resource_record_value": cty.String,
	})
	out["aws_acm_certificate"] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":                        {Type: cty.String, Computed: true},
		"arn":                       {Type: cty.String, Computed: true},
		"domain_name":               {Type: cty.String, Optional: true},
		"validation_method":         {Type: cty.String, Optional: true},
		"domain_validation_options": {Type: cty.Set(optionType), Computed: true},
		"tags":                      {Type: cty.Map(cty.String), Optional: true},
	}}}
	out["aws_route53_record"] = providers.Schema{Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"id":      {Type: cty.String, Computed: true},
		"zone_id": {Type: cty.String, Optional: true},
		"name":    {Type: cty.String, Optional: true},
		"type":    {Type: cty.String, Optional: true},
		"records": {Type: cty.List(cty.String), Optional: true},
		"ttl":     {Type: cty.Number, Optional: true},
	}}}
	return out
}

// TestLivePlan_targetIsNotRefusedByAnExcludedForEachTheSecondPassCannotSettle
// is GitHub issue #1470's end-to-end proof, in the two arms
// TestLivePlan_targetScopesTheStatelessPipeline uses.
//
// Untargeted, the run refuses on the record's for_each. That arm is the
// behaviour that must not change, and it is the mutation check on the
// second: a fixture that had stopped refusing would let the targeted run
// pass for no reason at all.
//
// Targeted at the bucket, the run must succeed. Before the fix it exited 1
// with "Non-static for_each expression" for aws_route53_record.cert_validation,
// a block the plan graph had already dropped: [resolver.collectSignal]
// expanded every block with no scope before the walk, expansionFor memoized
// the failure, and [resolver.walkOutOfScope]'s rollback had nothing of its
// own to remove. On live-target-provider-work's fixture the second pass
// masks that; here it cannot, so the exit code is the defect.
func TestLivePlan_targetIsNotRefusedByAnExcludedForEachTheSecondPassCannotSettle(t *testing.T) {
	run := func(t *testing.T, args ...string) (int, *terminal.TestOutput, *statelessTestCloud) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath(targetSignalFixture), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		cloud.schemas = targetSignalSchemas()
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
		if !strings.Contains(output.Stderr(), "Non-static for_each expression") {
			t.Errorf("an untargeted run no longer refuses the record's for_each, so the targeted case below proves nothing:\n%s", output.Stderr())
		}
	})

	t.Run("targeted proceeds", func(t *testing.T) {
		code, output, cloud := run(t, "-target=aws_s3_bucket.data")
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - a -target run refused for a block it excludes\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if strings.Contains(output.Stderr(), "Non-static for_each expression") {
			t.Errorf("a targeted run still refused on a for_each outside its own target set:\n%s", output.Stderr())
		}
		if !strings.Contains(output.Stdout(), "No changes.") {
			t.Errorf("the bucket's plan is not empty:\n%s", output.Stdout())
		}
		if !cloud.imported("aws_s3_bucket", "tofu-stateless-unit-data") {
			t.Errorf("the bucket was never read from the live system; imports were %v", cloud.imports)
		}
	})
}

// TestTheSecondPassCannotSettleTheTargetSignalFixture is the control on the
// test above: it pins WHY this fixture is the one the second pass cannot
// mask, by value, so that a later change to [projection.PlanInstances] that
// started planning for_each blocks would fail here rather than silently turn
// the end-to-end test into TestATargetedRunIsNotRefusedByAnExcludedForEach's
// twin.
//
// The counting cloud is live-target-provider-work's; the scope is the plan
// graph's, from [statelessTargetScope] over a real [tofu.Context].
func TestTheSecondPassCannotSettleTheTargetSignalFixture(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", targetSignalFixture))
	cloud := newTargetWorkCloud()
	scope := cloud.scopeFor(t, cfg, "aws_s3_bucket.data")

	_, diags := statelessResolve(t.Context(), cfg, cloud, nil, nil, scope)
	if n := cloud.plans["aws_acm_certificate"]; n != 0 {
		t.Errorf("PlanInstances planned the for_each certificate %d time(s); this fixture relies on it never being planned (calls were %s)", n, renderCounts(cloud.plans))
	}
	if n := errorCount(diags); n != 0 {
		t.Errorf("-target=aws_s3_bucket.data refused with %d error(s) for blocks the run excludes, and PlanResourceChange calls were %s: %v",
			n, renderCounts(cloud.plans), renderDiags(diags))
	}
}
