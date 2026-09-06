// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/stamp"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/provisioners"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
	residue "github.com/intentius/choudoufu/live"
)

// The live-plan tests drive the whole pipeline (lint, identity
// resolution, projection, plan, render) over a mock AWS provider standing in
// for a cloud. No state file exists in any of them, which is the point: none
// of these runs would be possible with a stock plan in the same directory.

func TestLivePlan_noChanges(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// Marked, because an unmarked live resource at a declared name is not
	// this estate's to plan against at all: the projection leaves it alone
	// and the plan proposes creating what the configuration declares. See
	// TestLivePlan_unownedNameIsNotAdopted for that half.
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	// The bucket is the only instance the projection can name in phase 1,
	// so it is the only one that can show "no changes" yet. The VPC is
	// targeted out for the same reason the P1.5 harness step targets the
	// concrete set only.
	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("plan is not empty:\n%s", stdout)
	}
	if !cloud.imported("aws_s3_bucket", "tofu-stateless-unit-data") {
		t.Errorf("the bucket was never read from the live system; imports were %v", cloud.imports)
	}

	// The omissions section is the transparency surface: an instance that
	// is not in the projection has to be named, with a machine-readable
	// reason and a sentence.
	if !strings.Contains(stdout, "Not read from the live system") {
		t.Errorf("no omissions section in the output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_vpc.main") || !strings.Contains(stdout, "NEEDS_DISCOVERY") {
		t.Errorf("the omissions section does not name aws_vpc.main and its reason:\n%s", stdout)
	}
}

// TestLivePlan_sidecarOnlyConfig is issue #72's command-level claim: a
// directory whose .tf files carry no choudoufu-specific syntax at all, with
// the live configuration in the estate.chdf.hcl sidecar file, runs the live
// pipeline exactly as the in-terraform{} form does - the estate name comes
// from the sidecar, no -estate flag needed.
func TestLivePlan_sidecarOnlyConfig(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-sidecar"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if !strings.Contains(output.Stdout(), "No changes.") {
		t.Errorf("plan is not empty:\n%s", output.Stdout())
	}
	if !cloud.imported("aws_s3_bucket", "tofu-stateless-unit-data") {
		t.Errorf("the bucket was never read from the live system; imports were %v", cloud.imports)
	}
}

// TestLivePlan_sidecarConflictsWithEstateFlag: the -estate flag is refused
// when the configuration already names an estate, and the sidecar counts as
// the configuration for that rule - which is also the command-level proof
// that the sidecar reaches statelessSettings' SelectiveLoadBackend load, the
// same load the backend wall depends on.
func TestLivePlan_sidecarConflictsWithEstateFlag(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-sidecar"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=other-estate"})
	output := done(t)
	if code == 0 {
		t.Fatalf("exit code 0, want failure\nstdout:\n%s", output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "Estate named by both the live block and -estate") {
		t.Errorf("no conflict diagnostic; the sidecar did not reach the selective backend load:\n%s", output.Stderr())
	}
}

// TestLivePlan_proposesCreate is the other half of the same run: with no
// -target, the instance that could not be read is planned as a create, which
// is the expected phase-1 answer and the thing the omissions section explains.
func TestLivePlan_proposesCreate(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (changes present, -detailed-exitcode)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if !strings.Contains(stdout, "aws_vpc.main will be created") {
		t.Errorf("the un-materialized VPC is not planned for creation:\n%s", stdout)
	}
	if strings.Contains(stdout, "aws_s3_bucket.data will be") {
		t.Errorf("the bucket read from the live system still has a change planned:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 to add, 0 to change, 0 to destroy") {
		t.Errorf("plan summary is not 1 to add:\n%s", stdout)
	}
}

// writeAWSProviderLock writes a minimal ".terraform.lock.hcl" into dir,
// locking hashicorp/aws to version - just enough for
// [Meta.resolvedAWSProviderVersion] to read a version back, with no hashes
// block since providerFactories never actually opens this package (the
// live-plan tests run through the in-process testingOverrides provider, not
// an installed one, so the lock file's only job here is to name a resolved
// version).
func writeAWSProviderLock(t *testing.T, dir, version string) {
	t.Helper()
	content := fmt.Sprintf(`# This file is maintained automatically by "tofu init".
# Manual edits may be lost in future updates.

provider "registry.opentofu.org/hashicorp/aws" {
  version     = %q
  constraints = %q
}
`, version, version)
	if err := os.WriteFile(filepath.Join(dir, ".terraform.lock.hcl"), []byte(content), 0o644); err != nil {
		t.Fatalf("writing .terraform.lock.hcl: %v", err)
	}
}

// bumpPatchVersion increments the patch component of a clean "x.y.z"
// version string, so TestLivePlan_providerVersionSkewWarns exercises the
// patch-only-differs case issue #63's design comment argues warrants a
// warning, derived from whatever live/survey.json currently pins rather
// than a version string hand-copied here to fall out of sync with it.
func bumpPatchVersion(t *testing.T, v string) string {
	t.Helper()
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		t.Fatalf("evidence version %q is not a clean x.y.z string", v)
	}
	var patch int
	if _, err := fmt.Sscanf(parts[2], "%d", &patch); err != nil {
		t.Fatalf("evidence version %q has a non-numeric patch component: %v", v, err)
	}
	return fmt.Sprintf("%s.%s.%d", parts[0], parts[1], patch+1)
}

// TestLivePlan_providerVersionSkewWarns is issue #63's command-level proof:
// a resolved hashicorp/aws version that does not match live/survey.json's
// admission evidence version produces exactly the one warning
// [providerversion.Check] documents, naming both versions, and the plan
// still succeeds - this is a caution, never a gate.
func TestLivePlan_providerVersionSkewWarns(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)

	evidence := residue.EvidenceVersion()
	if evidence == "" {
		t.Fatal("residue.EvidenceVersion() is empty")
	}
	skewed := bumpPatchVersion(t, evidence)
	writeAWSProviderLock(t, td, skewed)

	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (a version skew warns, it does not fail the run)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	all := output.All()
	if !strings.Contains(all, "Provider version does not match the admission evidence version") {
		t.Errorf("no provider-version-skew warning in output:\n%s", all)
	}
	if !strings.Contains(all, skewed) {
		t.Errorf("warning does not name the resolved version %q:\n%s", skewed, all)
	}
	if !strings.Contains(all, evidence) {
		t.Errorf("warning does not name the evidence version %q:\n%s", evidence, all)
	}
	if strings.Count(all, "Provider version does not match the admission evidence version") != 1 {
		t.Errorf("warning appears %d times, want exactly once per run:\n%s", strings.Count(all, "Provider version does not match the admission evidence version"), all)
	}
}

// TestLivePlan_providerVersionMatchIsSilent is the negative half of
// TestLivePlan_providerVersionSkewWarns: a resolved version identical to
// the admission evidence version produces no warning at all.
func TestLivePlan_providerVersionMatchIsSilent(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)

	evidence := residue.EvidenceVersion()
	if evidence == "" {
		t.Fatal("residue.EvidenceVersion() is empty")
	}
	writeAWSProviderLock(t, td, evidence)

	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	all := output.All()
	if strings.Contains(all, "Provider version does not match the admission evidence version") {
		t.Errorf("a matching resolved version still produced the skew warning:\n%s", all)
	}
}

// TestLivePlan_lintFatal: a configuration outside the subset stops
// before any provider is started, and the rule that rejected it is named.
func TestLivePlan_lintFatal(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-lint"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stderr := output.Stderr()
	if !strings.Contains(stderr, "Logical resource is not admitted") {
		t.Errorf("no lint diagnostic for the logical resource:\n%s", stderr)
	}
	if !strings.Contains(stderr, "logical-resource") {
		t.Errorf("the diagnostic does not name the rule that fired:\n%s", stderr)
	}
	if !strings.Contains(stderr, "random_pet.name") {
		t.Errorf("the diagnostic does not name the offending resource:\n%s", stderr)
	}
	if len(cloud.imports) > 0 {
		t.Errorf("a rejected configuration still read from the live system: %v", cloud.imports)
	}
}

// TestLivePlan_identityFatal: an instance whose identity cannot be
// resolved is fatal, not a create. A partial identity map would plan to
// create resources that already exist.
//
// Two subtests, one per path CHOUDOUFU_NODE_RESOLVE selects (default and
// its "=0" opt-out - see internal/command/live_mode.go's nodeResolveEnabled
// for the grammar), because the two paths now disagree on WHICH diagnostic
// is fatal for the exact same configuration. Default (node-resolve on):
// the static evaluator's own "Identity argument not set" error downgrades
// to a warning (identity.DowngradeForNodeResolution, #364 unit B's landing
// note), so OpenTofu's ordinary Validate pass runs for the first time -
// which needs a real provider schema for aws_iam_group, hence this
// package's own statelessTestSchemas() carries one now - and the node
// resolver's own step (c) refuses instead, with #365's "No source for this
// instance's identity" (aws_iam_group is config-identified: a table row,
// neither ServerAssigned nor RecordBacked, whose derivation failed for this
// instance because "name" is unset). Opt-out: the static evaluator's error
// stays fatal exactly as it always has, and OpenTofu's Plan never runs at
// all, so the schema addition changes nothing there - proof the two paths
// are both still real and both still selectable, not just documented as
// such.
func TestLivePlan_identityFatal(t *testing.T) {
	run := func(t *testing.T, nodeResolve string) {
		t.Helper()
		if nodeResolve != "" {
			t.Setenv("CHOUDOUFU_NODE_RESOLVE", nodeResolve)
		}
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan-no-identity"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		c, done := newLivePlanCommand(t, cloud)

		code := c.Run([]string{"-no-color"})
		output := done(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}

		stderr := output.Stderr()
		want := "No source for this instance's identity"
		if nodeResolve == "0" {
			want = "Identity argument not set"
		}
		if !strings.Contains(stderr, want) {
			t.Errorf("no %q diagnostic:\n%s", want, stderr)
		}
		if strings.Contains(output.Stdout(), "will be created") {
			t.Errorf("an unresolvable identity produced a plan anyway:\n%s", output.Stdout())
		}
		if len(cloud.imports) > 0 {
			t.Errorf("a configuration with an unresolvable identity still read from the live system: %v", cloud.imports)
		}
	}

	t.Run("default (node-resolve on)", func(t *testing.T) { run(t, "") })
	t.Run("CHOUDOUFU_NODE_RESOLVE=0 (static path)", func(t *testing.T) { run(t, "0") })
}

// TestLivePlan_ignoresStateFile: a state file in the working directory
// is not read, not written, and reported.
func TestLivePlan_ignoresStateFile(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	// A state file whose contents are nonsense: if anything read it, the
	// run would fail rather than ignore it.
	statePath := filepath.Join(td, "terraform.tfstate")
	if err := os.WriteFile(statePath, []byte("this is not a state file\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cloud := newStatelessTestCloud()
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-target=aws_s3_bucket.data"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	// A warning, so the standard view sends it to stdout.
	if !strings.Contains(output.Stdout(), "State file present but not consulted") {
		t.Errorf("the run did not report the state file it ignored:\n%s", output.Stdout())
	}

	got, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("the state file is gone after a stateless plan: %s", err)
	}
	if string(got) != "this is not a state file\n" {
		t.Errorf("the state file was rewritten by a stateless plan:\n%s", got)
	}
}

// TestLivePlan_rejectsStateOptions checks that the options stateless
// mode cannot honor fail loudly instead of being ignored.
func TestLivePlan_rejectsStateOptions(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-out=tfplan"}, "Saved plan files are not available under live resource markers"},
		{[]string{"-state=other.tfstate"}, "State file options are not available under live resource markers"},
		{[]string{"-destroy"}, "Only the normal planning mode is available under live resource markers yet"},
		// -json is deliberately absent from this table: GitHub issue #788
		// gave it a document of its own instead of a refusal - see
		// TestLivePlan_jsonDocument below, this table's opposite number.
		// -json-into stays refused, and TestStatelessRejections_surfacesAgree
		// (live_rejections_test.go) already covers that exhaustively at the
		// statelessRejections level, so it is not repeated here.
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			cloud := newStatelessTestCloud()
			c, done := newLivePlanCommand(t, cloud)

			code := c.Run(append([]string{"-no-color"}, tc.args...))
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
			}
			if !strings.Contains(output.Stderr(), tc.want) {
				t.Errorf("wrong diagnostic for %s:\n%s", tc.args[0], output.Stderr())
			}
		})
	}
}

// TestLivePlan_jsonDocument is GitHub issue #788's "Done when": -json
// produces one [views.LivePlanDocument] whose three sections - bound,
// omissions, unowned - agree with the SAME run's own human-mode text, over
// a plan that genuinely exercises all three at once rather than one at a
// time the way the sibling tests above each do.
//
// This drives the existing "live-plan" fixture and mock cloud
// (newLivePlanCommand/newStatelessTestCloud, this file's own harness -
// TestLivePlan_unownedNameIsNotAdopted and
// TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider are this test's
// own two halves, combined into one run) rather than live/e2e/estate
// directly: live/e2e/estate's ~30 real AWS resource types have no entry in
// statelessTestIdentitySchemas/statelessTestSchemas (six types, hand-built
// for the fixtures this package already uses), and building a plannable
// mock schema for all of them - identity fields, nested blocks, every
// attribute every one of that fixture's 20 files references - is a
// disproportionate undertaking for one test, with a real risk of a subtly
// wrong fake schema hiding a real bug rather than proving anything. That
// fixture's own identity-resolution coverage is exercised directly,
// without a mock cloud, by TestLivePlan_estateName's subtests
// (statelessTestLoadConfig(t, "../../live/e2e/estate")). What this test
// proves - the JSON document's three sections agree with the text's own
// counts and content, over a run that exercises all three - is the same
// claim regardless of which admitted fixture produces the data.
func TestLivePlan_jsonDocument(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// The bucket: somebody else's object at the name this configuration
	// declares, no ownership marker at all - TestLivePlan_unownedNameIsNotAdopted's
	// own setup. Client-named, so the projection reads it back directly
	// with no sweep involved; being unowned keeps it out of prior state,
	// so it is both this run's one "unowned" entry and (since nothing
	// materialized it) its one "omission".
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	// The VPC: this estate's own object, found and bound by the marker
	// sweep - putMarked (for the later import/read) plus list (for the
	// sweep itself to find it in the first place), the same pairing
	// twoRegionNeedsDiscoveryCloud uses. This is the run's one "bound"
	// entry, and its source has to be "marker": aws_vpc's identity is
	// server-assigned, so nothing about it is derivable from configuration
	// alone.
	cloud.putMarked("aws_vpc", "vpc-42", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-42", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-42", "the estate's own VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})

	// The human-mode run first, so this test has the text's own counts to
	// cross-check the document against rather than asserting a number
	// against itself.
	human, doneHuman := newLivePlanCommand(t, cloud)
	humanCode := human.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode"})
	humanOut := doneHuman(t).Stdout()
	if humanCode != 2 {
		t.Fatalf("human-mode exit code %d, want 2 (the bucket create is a change)\nstdout:\n%s", humanCode, humanOut)
	}
	if !strings.Contains(humanOut, "Not read from the live system: 1 resource instance") {
		t.Fatalf("the human run's own omissions count is not 1, so this test's cross-check proves nothing:\n%s", humanOut)
	}
	if !strings.Contains(humanOut, "Unowned: 1 live resource holds an identity this configuration declares (1 adoptable)") {
		t.Fatalf("the human run's own Unowned section is not what this test expects:\n%s", humanOut)
	}
	// The human view has no "bound" section of its own to compare against -
	// that is exactly #788's own point - but if the VPC were not actually
	// bound by the marker sweep it would show up here as a create, and this
	// test's "one bound entry" claim below would be proving nothing.
	if strings.Contains(humanOut, "aws_vpc.main will be created") {
		t.Fatalf("the VPC was not bound by the marker sweep, so this test's own setup is broken:\n%s", humanOut)
	}

	// -json against the identical cloud and estate.
	jsonCmd, doneJSON := newLivePlanCommand(t, cloud)
	jsonCode := jsonCmd.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode", "-json"})
	jsonOut := doneJSON(t)
	if jsonCode != 2 {
		t.Fatalf("-json exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", jsonCode, jsonOut.Stdout(), jsonOut.Stderr())
	}
	if strings.TrimSpace(jsonOut.Stderr()) != "" {
		t.Errorf("-json wrote to stderr with nothing to report there:\n%s", jsonOut.Stderr())
	}

	var doc views.LivePlanDocument
	if err := json.Unmarshal([]byte(jsonOut.Stdout()), &doc); err != nil {
		t.Fatalf("-json's stdout does not parse as one JSON document: %s\nstdout:\n%s", err, jsonOut.Stdout())
	}

	if doc.Estate != "stateless-unit" {
		t.Errorf("document estate = %q, want %q", doc.Estate, "stateless-unit")
	}
	if doc.UpstreamVersion == "" {
		t.Error("document's upstream_version is empty")
	}

	// The document's own counts against the text's own counts, read above -
	// this test's whole claim.
	if len(doc.Bound) != 1 {
		t.Fatalf("bound has %d entries, want 1: %#v", len(doc.Bound), doc.Bound)
	}
	if len(doc.Omissions) != 1 {
		t.Fatalf("omissions has %d entries, want 1 (matching the human run's \"1 resource instance\"): %#v", len(doc.Omissions), doc.Omissions)
	}
	if len(doc.Unowned) != 1 {
		t.Fatalf("unowned has %d entries, want 1 (matching the human run's \"1 adoptable\"): %#v", len(doc.Unowned), doc.Unowned)
	}

	bound := doc.Bound[0]
	if bound.Addr != "aws_vpc.main" || bound.TypeName != "aws_vpc" {
		t.Errorf("bound[0] = %+v, want aws_vpc.main/aws_vpc", bound)
	}
	if bound.Identity != "vpc-42" {
		t.Errorf("bound[0].identity = %q, want %q", bound.Identity, "vpc-42")
	}
	if bound.Source != views.LivePlanBoundMarker {
		t.Errorf("bound[0].source = %q, want %q (a server-assigned VPC has no identity a plan could derive from configuration)", bound.Source, views.LivePlanBoundMarker)
	}

	om := doc.Omissions[0]
	if om.Addr != "aws_s3_bucket.data" {
		t.Errorf("omissions[0].addr = %q, want %q", om.Addr, "aws_s3_bucket.data")
	}
	if om.Reason != "UNOWNED" {
		t.Errorf("omissions[0].reason = %q, want %q", om.Reason, "UNOWNED")
	}
	if om.Detail == "" {
		t.Error("omissions[0].detail is empty - the operator sentence the text prints is missing from the document")
	}

	un := doc.Unowned[0]
	if un.Addr != "aws_s3_bucket.data" || un.TypeName != "aws_s3_bucket" {
		t.Errorf("unowned[0] = %+v, want aws_s3_bucket.data/aws_s3_bucket", un)
	}
	if un.LiveID != "tofu-stateless-unit-data" {
		t.Errorf("unowned[0].identity = %q, want %q", un.LiveID, "tofu-stateless-unit-data")
	}
	if un.MarkerEstate != "stateless-unit" || un.MarkerAddress != "aws_s3_bucket.data" {
		t.Errorf("unowned[0]'s exact tag write = (tofu_estate=%q, tofu_address=%q), want (%q, %q)",
			un.MarkerEstate, un.MarkerAddress, "stateless-unit", "aws_s3_bucket.data")
	}
}

// ---------------------------------------------------------------------------
// Removal: owned, undeclared, destroyed
// ---------------------------------------------------------------------------

// TestLivePlan_undeclaredIsDestroyed is the removal claim through the
// whole command: a live resource carrying this estate's marker for an address
// the configuration declares nowhere is destroyed, and nothing else is
// touched.
//
// The type matters. aws_subnet appears nowhere in this fixture, so nothing in
// the configuration would ever cause it to be listed and no config-driven
// scan could see this resource. Only the estate-wide sweep can, which is
// exactly the gap this closes.
func TestLivePlan_undeclaredIsDestroyed(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-owned", "the estate's own VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})

	// The deleted block's resource: owned, and declared nowhere.
	cloud.putMarked("aws_subnet", "subnet-gone", "stateless-unit", "aws_subnet.gone", map[string]string{
		"id": "subnet-gone", "cidr_block": "10.42.1.0/24",
	})
	cloud.list("aws_subnet", "subnet-gone", "the deleted block's subnet",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_subnet.gone"},
		map[string]string{"cidr_block": "10.42.1.0/24"})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "Owned and undeclared: 1 live resource will be destroyed") {
		t.Errorf("no removal section:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_subnet.gone") || !strings.Contains(stdout, "subnet-gone") {
		t.Errorf("the removal section does not name the resource:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_subnet.gone will be destroyed") {
		t.Errorf("the plan does not propose the destroy:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Plan: 0 to add, 0 to change, 1 to destroy") {
		t.Errorf("the plan is not exactly one destroy:\n%s", stdout)
	}
	// The resource has to have been read, or the destroy would be planned
	// against an object nobody looked at.
	if !cloud.imported("aws_subnet", "subnet-gone") {
		t.Errorf("the undeclared resource was never read from the live system")
	}
}

// sweepGapHeadingCount pulls the "Not swept for removal" heading's own type
// count back out of rendered output, so a test can check the summary
// sentence's count agrees with it instead of hard-coding the admission
// table's current size (which grows as later ratification batches land).
var sweepGapHeadingCount = regexp.MustCompile(`Not swept for removal: (\d+) resource`)

// TestLivePlan_sweepGapsAreReported: a type the sweep could not
// enumerate is named, because "nothing undeclared was found" is only
// meaningful beside the list of types that were searched. By default the
// standing-fact groups - a provider version's fixed inability to list or tag
// a type, true of every run against it - collapse to a one-line summary
// naming the count and where the full list lives, rather than printing
// hundreds of type names on a fresh estate's first plan (GitHub issue #78,
// "First plan drowns a small estate in the not-swept type list"). The
// heading's own count is never hidden or softened, and -verbose still
// prints every type (see TestLivePlan_sweepGapsVerboseListsEveryType).
func TestLivePlan_sweepGapsAreReported(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-owned", "the estate's own VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	m := sweepGapHeadingCount.FindStringSubmatch(stdout)
	if m == nil {
		t.Fatalf("no \"Not swept for removal\" heading with a count:\n%s", stdout)
	}
	total := m[1]

	// The mock provider lists one type (aws_vpc) and no more, so every
	// other admitted type is a standing-fact gap: nothing here is a list
	// call that failed during this run, so the whole heading count is what
	// the summary sentence should carry.
	if !strings.Contains(stdout, fmt.Sprintf("%s of them are TYPE_NOT_LISTABLE", total)) {
		t.Errorf("the summary line does not carry the heading's own count (%s):\n%s", total, stdout)
	}
	if !strings.Contains(stdout, "Rerun with -verbose to print every type by name") {
		t.Errorf("the summary line does not point at -verbose for the full list:\n%s", stdout)
	}
	if !strings.Contains(stdout, "live/LIMITATIONS.md") || !strings.Contains(stdout, "Removal coverage is the admission table") {
		t.Errorf("the summary line does not point at LIMITATIONS.md:\n%s", stdout)
	}
	// The full breakdown - one bracketed reason line per type, and the type
	// names themselves - must not render without -verbose. aws_xray_sampling_rule
	// is the alphabetically last admitted type in live/LIMITATIONS.md's
	// contract table, so its absence here (and presence in the -verbose
	// test below) pins the boundary precisely rather than by inference.
	if strings.Contains(stdout, "[TYPE_NOT_LISTABLE]") {
		t.Errorf("the full type-by-type breakdown rendered without -verbose:\n%s", stdout)
	}
	if strings.Contains(stdout, "aws_xray_sampling_rule") {
		t.Errorf("a specific gap type is named without -verbose:\n%s", stdout)
	}
	if strings.Contains(stdout, "Owned and undeclared") {
		t.Errorf("a removal was reported with nothing undeclared:\n%s", stdout)
	}
}

// TestLivePlan_sweepGapsVerboseListsEveryType is -verbose's own claim: the
// full type-by-type breakdown TestLivePlan_sweepGapsAreReported found
// collapsed by default is still there on request, in the same form it
// always rendered in before GitHub issue #78.
func TestLivePlan_sweepGapsVerboseListsEveryType(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-owned", "the estate's own VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-verbose", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "Not swept for removal") {
		t.Errorf("the plan does not report the types the sweep could not cover:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[TYPE_NOT_LISTABLE]") {
		t.Errorf("-verbose does not print the reason each gap type carries:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_xray_sampling_rule") {
		t.Errorf("-verbose does not print the standing-fact gap types by name:\n%s", stdout)
	}
	if strings.Contains(stdout, "Rerun with -verbose") {
		t.Errorf("verbose output should not also point at -verbose for more detail:\n%s", stdout)
	}
}

// ---------------------------------------------------------------------------
// Foreign classification and the protection property
// ---------------------------------------------------------------------------

// TestLivePlan_foreignIsProtected is the safety claim of the whole
// design, run end to end through the command: a live resource of a managed
// type carrying no ownership marker is reported, and the plan proposes zero
// destroys.
//
// The protection is structural rather than filtered. The prior state this
// plan runs against is built from resolutions, resolutions come from declared
// addresses, and the unmarked VPC below has no declared address - so it is
// not in the prior state, and there is nothing for the plan engine to
// propose destroying. The assertions check both halves: the resource is
// visible in the output, and it is nowhere near a destroy line.
func TestLivePlan_foreignIsProtected(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// Both of the estate's own resources already carry their markers, so
	// stamping them is a no-op and this test stays about destroys. The
	// fixture's configuration declares no tags at all; the markers reach it
	// through stamping, which is exactly why a plan over an already-stamped
	// estate is empty.
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.putMarked("aws_vpc", "vpc-owned", "stateless-unit", "aws_vpc.main", map[string]string{
		"id": "vpc-owned", "cidr_block": "10.42.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-owned", "the estate's own VPC",
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_vpc.main"},
		map[string]string{"cidr_block": "10.42.0.0/16"})
	cloud.list("aws_vpc", "vpc-foreign", "somebody else's VPC",
		map[string]string{"Name": "legacy-network", "owner": "platform"},
		map[string]string{"cidr_block": "10.99.0.0/16"})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	// Half one: it is reported, by type, live ID and tags, in its own section.
	if !strings.Contains(stdout, "Foreign resources: 1 live resource not owned by estate stateless-unit") {
		t.Errorf("no foreign section naming the estate:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_vpc vpc-foreign") {
		t.Errorf("the unmarked VPC is not itemized:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Name=legacy-network") {
		t.Errorf("the foreign resource is reported without its tags:\n%s", stdout)
	}

	// Half two: nothing is destroyed, and the foreign resource is not on a
	// removal line. The second check mirrors the harness's own: a delete in
	// plan output is a line beginning with a minus.
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("the estate did not plan clean:\n%s", stdout)
	}
	if strings.Contains(stdout, "will be destroyed") || strings.Contains(stdout, "to destroy") && !strings.Contains(stdout, "0 to destroy") {
		t.Errorf("a destroy was proposed:\n%s", stdout)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "-") && strings.Contains(line, "vpc-foreign") {
			t.Errorf("the foreign VPC appears on a removal line: %q", line)
		}
	}

	// The marked VPC did bind, so the estate itself is fully projected: no
	// omissions section, and no create proposed for it.
	if strings.Contains(stdout, "Not read from the live system") {
		t.Errorf("an instance was omitted even though discovery bound it:\n%s", stdout)
	}
	if strings.Contains(stdout, "aws_vpc.main will be created") {
		t.Errorf("the marker-discovered VPC was planned as a create:\n%s", stdout)
	}

	// The epistemics: the section says which types it can speak for, and
	// which it cannot.
	if !strings.Contains(stdout, "Not swept") || !strings.Contains(stdout, "NOT_SCANNED") {
		t.Errorf("the output does not say which types were never listed:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_s3_bucket") {
		t.Errorf("the client-named type is not named as unswept:\n%s", stdout)
	}
}

// TestLivePlan_bindCandidateIsOfferedNotTaken: an unmarked resource that
// exactly matches a declared instance is surfaced with the markers that would
// adopt it, and is still not adopted - the plan proposes creating the
// declared instance, because nothing bound it.
func TestLivePlan_bindCandidateIsOfferedNotTaken(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "stateless-unit", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	// Nothing carries a marker, and this one's CIDR is exactly the declared
	// VPC's.
	cloud.list("aws_vpc", "vpc-unmarked", "", nil,
		map[string]string{"cidr_block": "10.42.0.0/16"})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (a create is proposed)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "Adoptable: 1 live resource matches a declared resource") {
		t.Errorf("no adoption section:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_vpc.main") || !strings.Contains(stdout, "vpc-unmarked") {
		t.Errorf("the adoption offer does not name both sides:\n%s", stdout)
	}
	if !strings.Contains(stdout, "matched on: cidr_block=10.42.0.0/16") {
		t.Errorf("the adoption offer does not show what it matched on:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws ec2 create-tags --resources 'vpc-unmarked'") ||
		!strings.Contains(stdout, "'Key=tofu-address,Value=aws_vpc.main'") {
		t.Errorf("the adoption hint does not stamp the markers, shell-quoted:\n%s", stdout)
	}
	// The hint is built to be pasted verbatim, so it carries the region the
	// provider block configures rather than leaving the CLI profile to pick
	// one.
	if !strings.Contains(stdout, "--region 'us-east-1'") {
		t.Errorf("the adoption hint does not carry the provider's region:\n%s", stdout)
	}

	// Not taken: the declared instance is still unbound, so it is omitted
	// from the projection and planned as a create - and the live resource is
	// still not destroyed.
	if !strings.Contains(stdout, "aws_vpc.main will be created") {
		t.Errorf("a bind candidate was silently adopted:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 to add, 0 to change, 0 to destroy") {
		t.Errorf("plan summary is not a single create:\n%s", stdout)
	}
	if strings.Contains(stdout, "Foreign resources: 1") {
		t.Errorf("the bind candidate was also reported as plain foreign:\n%s", stdout)
	}
}

// TestLivePlan_estateName covers the three ways this run learns which
// estate it is about.
func TestLivePlan_estateName(t *testing.T) {
	t.Run("rejects an invalid name", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		c, done := newLivePlanCommand(t, cloud)

		code := c.Run([]string{"-no-color", "-estate=Not_An_Estate"})
		output := done(t)
		if code != 1 {
			t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
		}
		if !strings.Contains(output.Stderr(), "Invalid estate name") {
			t.Errorf("no diagnostic naming the marker grammar:\n%s", output.Stderr())
		}
	})

	t.Run("warns rather than failing when there is none", func(t *testing.T) {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
		cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
			"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
		})
		c, done := newLivePlanCommand(t, cloud)

		// The fixture stamps no tofu-estate tag and no flag is given, so
		// discovery cannot run. The pre-discovery behaviour stands: the VPC
		// is omitted and planned as a create, with a warning saying so.
		code := c.Run([]string{"-no-color", "-detailed-exitcode"})
		output := done(t)
		if code != 2 {
			t.Fatalf("exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		stdout := output.Stdout()
		if !strings.Contains(stdout, "No estate name to search by") {
			t.Errorf("no warning about the missing estate name:\n%s", stdout)
		}
		if !strings.Contains(stdout, "-estate") {
			t.Errorf("the warning does not name the flag that fixes it:\n%s", stdout)
		}
		if strings.Contains(stdout, "Foreign resources") {
			t.Errorf("a run that swept nothing printed a foreign section anyway:\n%s", stdout)
		}
		if !strings.Contains(stdout, "aws_vpc.main will be created") {
			t.Errorf("the undiscovered instance was not planned as a create:\n%s", stdout)
		}
	})

	t.Run("derives the name from the configuration's own tags", func(t *testing.T) {
		// The P0.1 estate fixture, which stamps tofu-estate on every taggable
		// resource, is the case the harness runs: no flag, and the name comes
		// out of the configuration.
		cfg := statelessTestLoadConfig(t, "../../live/e2e/estate")
		resolutions, diags := identity.Resolve(t.Context(), cfg)
		if diags.HasErrors() {
			t.Fatalf("resolving the estate fixture: %s", diags.Err())
		}

		got, estateDiags := statelessEstateName(t.Context(), "", cfg, resolutions.NeedsDiscovery())
		if estateDiags.HasErrors() {
			t.Fatalf("deriving the estate name: %s", estateDiags.Err())
		}
		if got != "stateless-e2e" {
			t.Errorf("derived estate name %q, want stateless-e2e", got)
		}
	})

	t.Run("an explicit flag wins over the configuration", func(t *testing.T) {
		cfg := statelessTestLoadConfig(t, "../../live/e2e/estate")
		got, diags := statelessEstateName(t.Context(), "other-estate", cfg, nil)
		if diags.HasErrors() {
			t.Fatalf("unexpected diagnostics: %s", diags.Err())
		}
		if got != "other-estate" {
			t.Errorf("estate name %q, want the flag's value", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Marker stamping
// ---------------------------------------------------------------------------

// TestLivePlan_stampsMissingMarkers is P2.1's whole claim, run through
// the command: a configuration with no tags anywhere, given an estate name,
// produces a plan in which every taggable resource gains both ownership
// markers - visibly, on a "+ tags" line, for a create and for an update
// alike.
func TestLivePlan_stampsMissingMarkers(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// Owned by this estate and missing the address half of its marker, which
	// is the shape a resource stamped by an older run has. An estate's own
	// resource is the only kind whose markers can arrive as an in-place
	// update: one carrying no estate marker at all is not this estate's to
	// update, and the pass that used to adopt it is audit finding C1.
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})
	cloud.tags["aws_s3_bucket/tofu-stateless-unit-data"] = map[string]string{
		"tofu-estate": "stateless-unit",
	}

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the markers are a change)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	// The bucket exists and is bound, so its markers arrive as an in-place
	// tags update: the diff shows the tag being added, which is the
	// difference between a guarantee and a silent rewrite.
	if !strings.Contains(stdout, "aws_s3_bucket.data will be updated in-place") {
		t.Errorf("the bound bucket is not planned for an in-place update:\n%s", stdout)
	}
	for _, want := range []string{
		`+ "tofu-address" = "aws_s3_bucket.data"`,
		`+ "tofu-estate"  = "stateless-unit"`,
		`+ "tofu-address" = "aws_vpc.main"`,
		`~ tags`,
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the plan does not show %s:\n%s", want, stdout)
		}
	}

	// Nothing but the tags moved on the resource that already existed.
	if strings.Contains(stdout, "0 to add, 1 to change, 0 to destroy") {
		t.Errorf("the bucket update is not the only change; the VPC create is missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "1 to add, 1 to change, 0 to destroy") {
		t.Errorf("plan summary is not one create and one tags update:\n%s", stdout)
	}
	if strings.Contains(stdout, "will be destroyed") {
		t.Errorf("stamping proposed a destroy:\n%s", stdout)
	}
}

// TestLivePlan_unownedNameIsNotAdopted is audit finding C1's regression at
// the command's own level: a live resource sitting at a name this
// configuration declares, carrying no ownership marker, is not this estate's
// and no run may quietly take it.
//
// The bug was structural rather than a missing check. A client-named
// resource's identity comes out of the configuration, so it never passed
// through discovery, never reached the foreign classifier, and the projection
// read it straight into prior state on the strength of the name alone. From
// there the plan proposed in-place updates to somebody else's resource, and
// deleting the block proposed destroying it - while the foreign section
// reported, accurately as far as it knew, that nothing unowned had been
// swept.
//
// The claims: the live resource is not in the prior state, the plan proposes
// creating what the configuration declares rather than adopting what is
// there, nothing anywhere is destroyed, and the run says out loud what it
// found and how to adopt it deliberately.
func TestLivePlan_unownedNameIsNotAdopted(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	// Somebody else's bucket, at the name this configuration declares. No
	// markers on it: that is what "not ours" looks like.
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2 (the create is a change)\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if strings.Contains(stdout, "aws_s3_bucket.data will be updated in-place") {
		t.Errorf("an unowned live resource was adopted and planned against:\n%s", stdout)
	}
	if strings.Contains(stdout, "will be destroyed") || !strings.Contains(stdout, "0 to destroy") {
		t.Errorf("a plan touching an unowned resource proposes a destroy:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_s3_bucket.data will be created") {
		t.Errorf("the declared bucket is not planned as a create:\n%s", stdout)
	}

	// Said out loud, three times over: in the omissions section that explains
	// every create, in the Unowned section that says what resolves it, and as
	// a warning carrying the adoption instruction.
	if !strings.Contains(stdout, "aws_s3_bucket.data [UNOWNED]") {
		t.Errorf("the unowned resource is not named in the omissions section:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Unowned: 1 live resource holds an identity this configuration declares (1 adoptable)") {
		t.Errorf("no Unowned section heading:\n%s", stdout)
	}
	if !strings.Contains(stdout, "aws_s3_bucket.data [ADOPTABLE] <- aws_s3_bucket tofu-stateless-unit-data") {
		t.Errorf("the Unowned section does not offer the resource as adoptable:\n%s", stdout)
	}
	if !strings.Contains(stdout, "adopt by writing: tofu-estate=stateless-unit tofu-address=aws_s3_bucket.data") {
		t.Errorf("the Unowned section does not carry the exact tag values to write:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Live resource outside this estate") {
		t.Errorf("no warning about the unowned resource:\n%s", stdout)
	}
	for _, want := range []string{"tofu-estate", "stateless-unit", "tofu-stateless-unit-data"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the report does not name %q, so it cannot be acted on:\n%s", want, stdout)
		}
	}
}

// TestLivePlan_otherEstatesResourceIsNotAdopted is the same refusal against a
// resource that is owned, just not here. Two estates naming one bucket is a
// mistake in one of them, and the run that notices must not be the one that
// takes it.
func TestLivePlan_otherEstatesResourceIsNotAdopted(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-stateless-unit-data", "somebody-elses-estate", "aws_s3_bucket.data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-target=aws_s3_bucket.data", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if strings.Contains(stdout, "aws_s3_bucket.data will be updated in-place") {
		t.Errorf("another estate's resource was adopted:\n%s", stdout)
	}
	if !strings.Contains(stdout, "somebody-elses-estate") {
		t.Errorf("the report does not name the estate that owns it:\n%s", stdout)
	}
	// The Unowned section says at a glance that this one is not adoptable
	// here: it is in the way, and its holder is named.
	if !strings.Contains(stdout, "aws_s3_bucket.data [IN_THE_WAY] <- aws_s3_bucket tofu-stateless-unit-data") {
		t.Errorf("the Unowned section does not report the resource as in the way:\n%s", stdout)
	}
	if !strings.Contains(stdout, `held by estate "somebody-elses-estate"`) {
		t.Errorf("the Unowned section does not name the holding estate:\n%s", stdout)
	}
	if strings.Contains(stdout, "aws_s3_bucket.data [ADOPTABLE]") {
		t.Errorf("another estate's resource is offered as adoptable:\n%s", stdout)
	}
}

// TestLivePlan_stampingNeedsAnEstateName: with no estate name there is
// nothing to stamp with, and that degrades to a warning naming the flag
// rather than to a failure - the same graceful degradation discovery makes.
func TestLivePlan_stampingNeedsAnEstateName(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-detailed-exitcode"})
	output := done(t)
	if code != 2 {
		t.Fatalf("exit code %d, want 2\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if !strings.Contains(stdout, "Ownership markers not stamped") {
		t.Errorf("no warning about the markers that were not stamped:\n%s", stdout)
	}
	if !strings.Contains(stdout, "-estate=<name>") {
		t.Errorf("the warning does not name the flag that fixes it:\n%s", stdout)
	}
	if strings.Contains(stdout, "tofu-estate") && strings.Contains(stdout, `+ tags`) {
		t.Errorf("markers were stamped with no estate name:\n%s", stdout)
	}
}

// TestLivePlan_markerConflictIsFatal: a configuration whose marker names
// another address is refused, by name, instead of being rewritten. Renaming
// is live-mv's job and adoption is adoption's; neither happens as a side
// effect of running a plan.
func TestLivePlan_markerConflictIsFatal(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-marker-conflict"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.put("aws_s3_bucket", "tofu-stateless-unit-data", map[string]string{
		"id": "tofu-stateless-unit-data", "bucket": "tofu-stateless-unit-data",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Ownership marker conflict") {
		t.Errorf("the conflict was not named:\n%s", stderr)
	}
	if !strings.Contains(stderr, "aws_s3_bucket.old_name") || !strings.Contains(stderr, "live-mv") {
		t.Errorf("the diagnostic does not say what conflicts or what fixes it:\n%s", stderr)
	}
	if strings.Contains(output.Stdout(), "will be updated in-place") {
		t.Errorf("a plan was produced over a disputed marker anyway:\n%s", output.Stdout())
	}
}

// ---------------------------------------------------------------------------
// Issue #69: an estate whose managed resources span more than one provider
// configuration.
// ---------------------------------------------------------------------------

// TestLivePlan_multiProviderSweepSucceeds is issue #69's own command-level
// proof, the unit-test twin of internal/live/discovery/alias_live_test.go's
// real floci e2e: two client-named aws_s3_bucket resources, one under the
// default provider and one under an aliased "west" provider, neither
// needing marker discovery. Before this issue's fix, live-plan refused any
// configuration whose managed resources spanned more than one provider
// configuration at all - this asserts that refusal is gone and both
// buckets plan clean, each read through its own provider configuration.
// TestLivePlan_undeclaredProviderAliasIsRefused pins GitHub issue #123's
// case 2: a root resource naming a provider alias the root does not declare.
// Establishing reachability by running it (the issue's own first acceptance
// criterion) showed no upstream validation fires under live-plan - stock
// OpenTofu's "Provider configuration not present" lives in the graph's
// ProviderTransformer, which live mode only reaches at tfCtx.Plan, after
// discovery has already read the live system - and providerConfigValue's
// empty-body fallback configured aws.nope from the environment alone,
// mid-discovery, with types already scanned through other providers. The
// only error in that characterization run came from the mock provider
// insisting on a region; the real AWS provider accepts an empty
// configuration, so against a real account the run proceeded silently.
// Now lint refuses it before anything is read from a cloud.
func TestLivePlan_undeclaredProviderAliasIsRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-undeclared-provider-alias"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-undeclared-alias-east", "undeclared-alias-unit", "aws_s3_bucket.east", map[string]string{
		"id": "tofu-undeclared-alias-east", "bucket": "tofu-undeclared-alias-east",
	})

	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=undeclared-alias-unit"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1 - an undeclared provider alias must be refused, not configured from the environment\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Provider configuration is not declared") {
		t.Errorf("the undeclared-provider-alias refusal did not fire:\n%s", stderr)
	}
	if !strings.Contains(stderr, "aws.nope") {
		t.Errorf("the refusal does not name the missing configuration:\n%s", stderr)
	}
	// The refusal is lint's, which runs before discovery: nothing may have
	// been read from the live system through any provider by the time the
	// run stops. The characterization run this test replaced had already
	// scanned types through the default provider when the stray address was
	// looked up.
	if len(cloud.imports) != 0 {
		t.Errorf("the live system was read before the refusal: %v", cloud.imports)
	}
	if strings.Contains(stderr, "discovering:") {
		t.Errorf("discovery ran before the refusal:\n%s", stderr)
	}
}

// TestLivePlan_residueAttributeWarningIsWired pins that
// lint.CheckResidueAttributes runs in the live-plan entry point. The
// wave-3 audit deleted the call and the whole command suite stayed green;
// this is the consumer-level guard that makes that mutation loud. The
// warning's own behavior is pinned in internal/live/lint; here only the
// wiring is under test.
func TestLivePlan_residueAttributeWarningIsWired(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-residue-attr"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-residue-attr-bucket", "residue-attr-unit", "aws_s3_bucket.app", map[string]string{
		"id": "tofu-residue-attr-bucket", "bucket": "tofu-residue-attr-bucket",
	})

	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=residue-attr-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstderr:\n%s", code, output.Stderr())
	}
	combined := output.Stdout() + output.Stderr()
	if !strings.Contains(combined, "Attribute value cannot round-trip a stateless replan") {
		t.Errorf("the residue-attribute warning did not reach the output; lint.CheckResidueAttributes is not wired into live-plan:\n%s", combined)
	}
	if !strings.Contains(combined, "secret_policy_seed") {
		t.Errorf("the warning does not name the attribute:\n%s", combined)
	}
}

// TestLivePlan_provisionerTaintIsRead pins that the command pipeline
// consults GitHub issue #353's tofu-provisioned namespace.
//
// This is the wiring guard, not the mechanism's own test - that lives in
// internal/live/projection and in live/e2e/provisioner-taint. It exists
// because internal/command builds projection.Options by hand, and an Options
// field left off is invisible at this level: the report comes out clean and
// says the estate matches its configuration while the object it describes is
// live, marked and half-provisioned.
//
// Note which code path it actually covers, because the command name is
// misleading. A configuration with a live block - which this fixture must
// have, since record_store lives in it - is delegated by
// LivePlanCommand.Run to PlanCommand, so the Options under test here are
// internal/command/live_mode.go's, not live_plan.go's own. Mutation-checked
// there: nilling live_mode.go's ProvisionedStore makes this test fail.
// live_plan.go's own Options carries the field too, and its comment says
// plainly that nothing reaches it today.
//
// The taint record is seeded by hand because a plan never applies and so
// never writes one, and it is seeded through the real ProvisionedStore
// rather than by writing a file, so the key and the payload are the ones the
// production path would produce.
func TestLivePlan_provisionerTaintIsRead(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-provisioner-taint"), td)
	t.Chdir(td)

	const estate = "provisioner-taint-unit"
	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-provisioner-taint-bucket", estate, "aws_s3_bucket.app", map[string]string{
		"id": "tofu-provisioner-taint-bucket", "bucket": "tofu-provisioner-taint-bucket",
	})

	// The healthy half first: with nothing recorded, the estate matches its
	// configuration. Without this the assertion below could pass against an
	// implementation that proposes a replacement for some other reason.
	c, done := newLivePlanCommand(t, cloud)
	withLocalExecProvisioner(c)
	// No -estate flag: the live block names it, and live-plan refuses both
	// at once.
	code := c.Run([]string{"-no-color"})
	cleanOut := done(t)
	clean := cleanOut.Stdout() + cleanOut.Stderr()
	if code != 0 {
		t.Fatalf("exit code %d on the clean run, want 0:\n%s", code, clean)
	}
	if strings.Contains(clean, "must be replaced") {
		t.Fatalf("the clean run already proposes a replacement, so the taint assertion below would prove nothing:\n%s", clean)
	}

	store, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store: %s", err)
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_s3_bucket.app")
	if addrDiags.HasErrors() {
		t.Fatalf("parsing the address: %s", addrDiags.Err())
	}
	// GitHub issue #364 folded the provisioner-taint namespace into the one
	// record envelope; this package cannot reach projection's unexported
	// mergeEnvelope to seed it through the real write path, so the fixture
	// writes the v2 wire shape by hand instead - kind=identity with a
	// Provisioned member, exactly what writeBackRecordEnvelopes produces.
	taintPayload := []byte(`{"format_version":2,"address":"` + addr.String() + `","kind":"identity","provisioned":{"tainted":true}}`)
	if _, err := store.PutIfAbsent(t.Context(), projection.RecordKey(projection.RecordKeyPrefix(estate), addr), taintPayload); err != nil {
		t.Fatalf("seeding the taint record: %s", err)
	}

	c2, done2 := newLivePlanCommand(t, cloud)
	withLocalExecProvisioner(c2)
	code2 := c2.Run([]string{"-no-color"})
	out2 := done2(t)
	combined := out2.Stdout() + out2.Stderr()
	if code2 != 0 {
		t.Fatalf("exit code %d, want 0\nstderr:\n%s", code2, out2.Stderr())
	}
	if !strings.Contains(combined, "aws_s3_bucket.app is tainted, so it must be replaced") {
		t.Errorf("live-plan does not report the tainted resource as needing replacement; issue #353's provisioner-taint envelope is not wired into live-plan's own projection.Options.RecordStore, so this report would call a half-provisioned object healthy:\n%s", combined)
	}
}

// TestLivePlan_markersRecordPreservesExistingMarker is GitHub issue #380:
// strict { markers "record" } used to withhold a selected resource's marker
// by writing nothing into its tags at all, which is right for a resource
// that never had one, but silently dropped one that already existed -
// applying an in-place update that stripped tofu-address/tofu-estate off a
// live object internal/live/stamp itself had put there before the selection
// was added. That is HANDOFF.md's "applied unmarked / marker silently
// disappears" failure, produced by this fork's own withholding.
//
// The fix (internal/live/stamp's SkipMarkersRecord branch) synthesizes
// lifecycle { ignore_changes = [tags["tofu-estate"], tags["tofu-address"]] }
// for the selected resource instead of writing nothing and leaving it there,
// so an existing value on those two keys survives untouched. This is that
// mechanism proven at the one level that can see a "live object already has
// a marker" prior state: a real plan, not just the rewritten configuration.
func TestLivePlan_markersRecordPreservesExistingMarker(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-markers-record"), td)
	t.Chdir(td)

	const estate = "markers-record-unit"
	cloud := newStatelessTestCloud()
	// The live object as it looks right after migrating onto this
	// selection: an ordinary run stamped it before strict { markers
	// "record" } existed for it, so it still carries both marker tags.
	cloud.putMarked("aws_vpc", "vpc-existing", estate, "aws_vpc.main", map[string]string{
		"id": "vpc-existing", "cidr_block": "10.42.0.0/16",
	})

	// A record-located instance is bound through the estate's record store
	// rather than through its marker (projection/located.go's
	// materializeLocated), so the scenario needs one seeded too, or the
	// plan would propose a CREATE instead of exercising the withholding
	// path at all. Written by hand for the same reason
	// TestLivePlan_provisionerTaintIsRead's taint record is: this package
	// cannot reach projection's unexported write path, so the fixture
	// writes the v2 wire shape directly - kind=identity with an import_id,
	// exactly what an apply under this same selection would write back.
	store, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store: %s", err)
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_vpc.main")
	if addrDiags.HasErrors() {
		t.Fatalf("parsing the address: %s", addrDiags.Err())
	}
	identityRecord := []byte(`{"format_version":2,"address":"` + addr.String() + `","kind":"identity","identity":{"import_id":"vpc-existing"}}`)
	if _, err := store.PutIfAbsent(t.Context(), projection.RecordKey(projection.RecordKeyPrefix(estate), addr), identityRecord); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	// No -estate flag: the live block names it, and live-plan refuses both
	// at once.
	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color"})
	output := done(t)
	stdout := output.Stdout()
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (no changes: the existing marker must be left alone, not planned away)\nstdout:\n%s\nstderr:\n%s", code, stdout, output.Stderr())
	}
	if !strings.Contains(stdout, "No changes") {
		t.Errorf("plan is not a no-op; the withheld marker was planned as a removal:\n%s", stdout)
	}
	if strings.Contains(stdout, "tofu-address") || strings.Contains(stdout, "tofu-estate") {
		t.Errorf("the plan mentions a marker tag at all, so something still proposes to touch it:\n%s", stdout)
	}
}

// TestLivePlan_markersRecordPreservesExistingMarker_NodeResolve is
// TestLivePlan_markersRecordPreservesExistingMarker with
// CHOUDOUFU_NODE_RESOLVE=1: the identical #380 scenario - a
// markers-record-selected VPC whose live object already carries both
// marker tags from before the selection existed - replanned with GitHub
// issue #388's node seam active in BOTH halves at once: NodeResolver
// resolves the instance's identity from the record store (step (a)) rather
// than the pre-walk static path, and its AdjustConfigValue sets nothing on
// this resource's evaluated configuration value (the record-selection
// branch nodestamp.go documents). The existing marker's survival still
// rests entirely on internal/live/stamp's own #380 fix - the HCL rewrite
// that synthesizes lifecycle { ignore_changes = [...] } runs before the
// graph walk regardless of the flag, and the adjuster's whole obligation is
// to stay out of its way - so this is that composition proven end to end
// rather than assumed.
func TestLivePlan_markersRecordPreservesExistingMarker_NodeResolve(t *testing.T) {
	t.Setenv("CHOUDOUFU_NODE_RESOLVE", "1")

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-markers-record"), td)
	t.Chdir(td)

	const estate = "markers-record-unit"
	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_vpc", "vpc-existing", estate, "aws_vpc.main", map[string]string{
		"id": "vpc-existing", "cidr_block": "10.42.0.0/16",
	})

	store, err := staterecord.NewLocalStore(filepath.Join(td, ".tofu-records"))
	if err != nil {
		t.Fatalf("opening the record store: %s", err)
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_vpc.main")
	if addrDiags.HasErrors() {
		t.Fatalf("parsing the address: %s", addrDiags.Err())
	}
	identityRecord := []byte(`{"format_version":2,"address":"` + addr.String() + `","kind":"identity","identity":{"import_id":"vpc-existing"}}`)
	if _, err := store.PutIfAbsent(t.Context(), projection.RecordKey(projection.RecordKeyPrefix(estate), addr), identityRecord); err != nil {
		t.Fatalf("seeding the located record: %s", err)
	}

	c, done := newLivePlanCommand(t, cloud)
	code := c.Run([]string{"-no-color"})
	output := done(t)
	stdout := output.Stdout()
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (no changes: the existing marker must be left alone, not planned away)\nstdout:\n%s\nstderr:\n%s", code, stdout, output.Stderr())
	}
	if !strings.Contains(stdout, "No changes") {
		t.Errorf("plan is not a no-op with the node seam active; the withheld marker was planned as a removal:\n%s", stdout)
	}
	if strings.Contains(stdout, "tofu-address") || strings.Contains(stdout, "tofu-estate") {
		t.Errorf("the plan mentions a marker tag at all, so something still proposes to touch it:\n%s", stdout)
	}
}

// withLocalExecProvisioner gives a live-plan command a "local-exec"
// provisioner to load a schema for. Schema loading is all it is ever asked
// for here: live-plan never applies, so nothing in this file can run a
// provisioner even by mistake - which is itself the plan-time-is-a-preview
// invariant, held by construction rather than by assertion.
func withLocalExecProvisioner(c *LivePlanCommand) {
	c.Meta.testingOverrides.Provisioners = map[string]provisioners.Factory{
		"local-exec": func() (provisioners.Interface, error) {
			return &tofu.MockProvisioner{
				GetSchemaResponse: provisioners.GetSchemaResponse{
					Provisioner: &configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"command": {Type: cty.String, Optional: true},
						},
					},
				},
			}, nil
		},
	}
}

func TestLivePlan_multiProviderSweepSucceeds(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-multi-provider"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.allowRegion("us-west-2")
	cloud.putMarked("aws_s3_bucket", "tofu-multi-provider-east", "multi-provider-unit", "aws_s3_bucket.east", map[string]string{
		"id": "tofu-multi-provider-east", "bucket": "tofu-multi-provider-east",
	})
	cloud.putMarked("aws_s3_bucket", "tofu-multi-provider-west", "multi-provider-unit", "aws_s3_bucket.west", map[string]string{
		"id": "tofu-multi-provider-west", "bucket": "tofu-multi-provider-west",
	})

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=multi-provider-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 - a multi-provider estate must not be refused before it reaches discovery\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	stdout := output.Stdout()
	if strings.Contains(stdout, "Marker discovery across several provider configurations") {
		t.Errorf("the old blanket multi-provider refusal fired:\n%s", stdout)
	}
	for _, addr := range []string{"aws_s3_bucket.east", "aws_s3_bucket.west"} {
		if strings.Contains(stdout, addr+" will be created") {
			t.Errorf("%s is proposed as a create; it already exists and carries this estate's marker:\n%s", addr, stdout)
		}
	}
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("both buckets are owned and unchanged; want a clean plan:\n%s", stdout)
	}
	if !cloud.imported("aws_s3_bucket", "tofu-multi-provider-east") {
		t.Error("the east bucket was never read from the live system")
	}
	if !cloud.imported("aws_s3_bucket", "tofu-multi-provider-west") {
		t.Error("the west bucket was never read from the live system")
	}
}

// twoRegionNeedsDiscoveryCloud is the shared setup for issue #283's two
// guards: a region-partitioned cloud holding one aws_vpc in each of the two
// regions the live-plan-multi-provider-needs-discovery fixture's provider
// configurations name, each carrying the marker of the resource block that
// declares it under that configuration.
//
// Both types are server-assigned, so both resources are ClassNeedsDiscovery
// and neither can be found any way but by listing. Placing them in different
// regions is what makes a pass that lists through the wrong provider
// configuration measurably different from one that lists through the right
// one - see [statelessTestCloud.regionOf].
func twoRegionNeedsDiscoveryCloud() *statelessTestCloud {
	cloud := newStatelessTestCloud()

	cloud.putMarked("aws_vpc", "vpc-in-east", "multi-provider-unit", "aws_vpc.east", map[string]string{
		"id": "vpc-in-east", "cidr_block": "10.0.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-in-east", "the default configuration's VPC",
		map[string]string{"tofu-estate": "multi-provider-unit", "tofu-address": "aws_vpc.east"},
		map[string]string{"cidr_block": "10.0.0.0/16"})
	cloud.inRegion("aws_vpc", "vpc-in-east", "us-east-1")

	cloud.putMarked("aws_vpc", "vpc-in-west", "multi-provider-unit", "aws_vpc.west", map[string]string{
		"id": "vpc-in-west", "cidr_block": "10.1.0.0/16",
	})
	cloud.list("aws_vpc", "vpc-in-west", "the aliased configuration's VPC",
		map[string]string{"tofu-estate": "multi-provider-unit", "tofu-address": "aws_vpc.west"},
		map[string]string{"cidr_block": "10.1.0.0/16"})
	cloud.inRegion("aws_vpc", "vpc-in-west", "us-west-2")

	return cloud
}

// TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider is issue #283's
// first guard: every resource waiting on marker discovery is looked for
// through the provider configuration ITS OWN resource block names.
//
// This is the case the fork used to refuse outright ("Marker discovery
// across several provider configurations"). The refusal's reasoning - a list
// issued against the wrong account or region reports an estate as missing
// rather than as unreachable - is exactly what this test now demands be
// satisfied per resource instead of by narrowing every estate to one
// configuration. A CloudFront estate cannot be narrowed that way at all:
// WAFv2 and ACM for CloudFront live in one region and the distribution's own
// dependencies do not.
//
// It asserts the RENDERED live identity each resource bound to, not merely
// that the plan came back empty. An empty plan is reachable by binding both
// resources to the wrong objects, by binding neither and having both look
// unchanged, and by several sorts of silence; the identity strings are not.
//
// Mutation: route every pass through the default provider configuration -
// pass sweepProviders[0], or the default's own address, as both providerAddr
// and scopeProvider in statelessDiscover's loop - and the aliased
// configuration's VPC is never listed at all, so aws_vpc.west comes back
// unbound and is proposed as a create.
func TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-multi-provider-needs-discovery"), td)
	t.Chdir(td)

	cloud := twoRegionNeedsDiscoveryCloud()

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=multi-provider-unit"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 - an estate whose discovery-needing resources span two provider configurations must plan\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stdout := output.Stdout()

	if strings.Contains(stdout, "Marker discovery across several provider configurations") {
		t.Errorf("the lifted needs-discovery refusal fired:\n%s", stdout)
	}

	// The identities, not the verdict. Each address must have been read back
	// from the one live object its own provider configuration's region
	// holds, and from no other.
	for _, want := range []struct{ addr, id string }{
		{"aws_vpc.east", "vpc-in-east"},
		{"aws_vpc.west", "vpc-in-west"},
	} {
		if !cloud.imported("aws_vpc", want.id) {
			t.Errorf("%s: %q was never read from the live system, so nothing bound it", want.addr, want.id)
		}
	}
	// And nothing else was read. A third import would mean some pass listed
	// an object outside its own region and bound it.
	if len(cloud.imports) != 2 {
		t.Errorf("want exactly the two regional VPCs imported, got %v", cloud.imports)
	}

	for _, addr := range []string{"aws_vpc.east", "aws_vpc.west"} {
		if strings.Contains(stdout, addr+" will be created") {
			t.Errorf("%s is proposed as a create; it already exists in its own configuration's region and carries this estate's marker:\n%s", addr, stdout)
		}
	}
	if !strings.Contains(stdout, "No changes.") {
		t.Errorf("both VPCs are owned and unchanged; want a clean plan:\n%s", stdout)
	}
}

// TestLivePlan_needsDiscoveryDoesNotBindAcrossProviders is issue #283's
// second guard, and the one that matters more: an object only ONE provider
// configuration can see is never bound to a resource belonging to another.
//
// A wrong marker outranks a missing one. Lifting the refusal means several
// passes now run over one estate, each handed the whole estate's resolutions
// (discovery.Request.ScopeProvider's own doc comment explains why they must
// be), so the failure this replaces the refusal's safety with is a pass
// binding somebody else's resource through its own account. Here the only
// live object carries aws_vpc.east's marker but sits in the region the
// ALIASED configuration reaches, and aws_vpc.east belongs to the default
// one. The correct answer is that aws_vpc.east is not found: the default
// configuration cannot see it, and the aliased configuration must not bind
// it however plainly the marker names it.
//
// A refusal is the visible, safe failure; a silent bind to an object in
// another region is the invisible, unsafe one. This test demands the first.
//
// GitHub issue #906 moved which visible failure it is. When this test was
// written the run proposed a create for aws_vpc.east and said nothing about
// vpc-misplaced, which is #906's defect standing right here in this fixture:
// the estate would end up with two live objects carrying one address's
// marker, and no later run could bind or destroy either. The refusal
// replaces it, and every property this test exists for is unchanged - the
// misplaced object is still never read, aws_vpc.west is still bound through
// its own configuration - while the third assertion moved from "the create
// is proposed" to "the run names the object and stops". The escape hatch, if
// this were really what an operator meant, is
// strict { provider_change = "recreate" }: see
// internal/live/discovery/outofscope_test.go, which exercises both settings.
//
// Mutation: drop ScopeProvider from statelessDiscover's multi-provider loop
// (pass addrs.AbsProviderConfig{} as scopeProvider, the single-provider
// path's own value) and the aliased configuration's pass binds
// aws_vpc.east to vpc-misplaced - which is a binding, not a stranding, so
// the refusal below never fires either and every assertion goes red.
func TestLivePlan_needsDiscoveryDoesNotBindAcrossProviders(t *testing.T) {
	// misplacedEstate lays the fixture down and puts the two objects in it:
	// aws_vpc.west's own, where it belongs, and one carrying aws_vpc.east's
	// marker in the region only the ALIASED configuration reaches.
	// providerChange, when non-empty, is written into a live block as the
	// strict toggle for the arm that exercises it.
	misplacedEstate := func(t *testing.T, providerChange string) *statelessTestCloud {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan-multi-provider-needs-discovery"), td)
		if providerChange != "" {
			// Written here rather than in a second fixture directory: the
			// estate, the providers and the two resource blocks must be the
			// SAME ones the refusing arm runs over, or the two arms would be
			// comparing different configurations rather than two settings.
			path := filepath.Join(td, "main.tf")
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the copied fixture: %s", err)
			}
			live := "terraform {\n  live {\n    estate = \"multi-provider-unit\"\n    strict {\n      provider_change = \"" + providerChange + "\"\n    }\n  }\n}\n\n"
			if err := os.WriteFile(path, append([]byte(live), src...), 0o600); err != nil {
				t.Fatalf("writing the fixture with the toggle: %s", err)
			}
		}
		t.Chdir(td)

		cloud := newStatelessTestCloud()

		// aws_vpc.west's own object, where it belongs.
		cloud.putMarked("aws_vpc", "vpc-in-west", "multi-provider-unit", "aws_vpc.west", map[string]string{
			"id": "vpc-in-west", "cidr_block": "10.1.0.0/16",
		})
		cloud.list("aws_vpc", "vpc-in-west", "the aliased configuration's VPC",
			map[string]string{"tofu-estate": "multi-provider-unit", "tofu-address": "aws_vpc.west"},
			map[string]string{"cidr_block": "10.1.0.0/16"})
		cloud.inRegion("aws_vpc", "vpc-in-west", "us-west-2")

		// An object carrying aws_vpc.east's marker, in the region only the
		// ALIASED configuration reaches. aws_vpc.east's own block uses the
		// default configuration, so no pass may bind this.
		cloud.putMarked("aws_vpc", "vpc-misplaced", "multi-provider-unit", "aws_vpc.east", map[string]string{
			"id": "vpc-misplaced", "cidr_block": "10.0.0.0/16",
		})
		cloud.list("aws_vpc", "vpc-misplaced", "an east-marked VPC in the west",
			map[string]string{"tofu-estate": "multi-provider-unit", "tofu-address": "aws_vpc.east"},
			map[string]string{"cidr_block": "10.0.0.0/16"})
		cloud.inRegion("aws_vpc", "vpc-misplaced", "us-west-2")

		return cloud
	}

	// The default. The run refuses and names the object, and #283's own
	// hazard - reading vpc-misplaced, which is what binding it looks like -
	// is what it must not do on the way there.
	t.Run("default: refused by name, nothing bound across configurations", func(t *testing.T) {
		cloud := misplacedEstate(t, "")
		c, done := newLivePlanCommand(t, cloud)

		code := c.Run([]string{"-no-color", "-estate=multi-provider-unit"})
		output := done(t)
		all := output.Stdout() + output.Stderr()
		if code == 0 {
			t.Fatalf("exit code 0: an object carrying aws_vpc.east's marker sits where only the aliased configuration reaches it, and the run neither bound it nor said so (GitHub issue #906):\n%s", all)
		}
		if cloud.imported("aws_vpc", "vpc-misplaced") {
			t.Errorf("vpc-misplaced was read back, so a pass bound an object visible only to the aliased provider configuration to aws_vpc.east, whose block uses the default one. That is a wrong marker, which is worse than the missing one this replaces:\n%s", all)
		}
		if !strings.Contains(all, "Marked resource outside its address's provider configuration") {
			t.Errorf("the run did not refuse by name; aws_vpc.east's marked object is somewhere its own provider configuration cannot reach and the plan must say so:\n%s", all)
		}
		for _, want := range []string{"vpc-misplaced", "aws_vpc.east", `strict { provider_change = "recreate" }`} {
			if !strings.Contains(all, want) {
				t.Errorf("the refusal does not name %q:\n%s", want, all)
			}
		}
		if strings.Contains(all, "aws_vpc.east will be created") {
			t.Errorf("aws_vpc.east was proposed as a create over the top of vpc-misplaced, which would leave two live objects carrying one address's marker:\n%s", all)
		}
	})

	// The toggle. This is the arm that still measures #283's own property in
	// full, because it is the one where the run gets all the way to reading
	// the live system: aws_vpc.west is bound through its own configuration,
	// vpc-misplaced is never read, and aws_vpc.east is the one create.
	t.Run("provider_change = recreate: proceeds, still binding nothing across configurations", func(t *testing.T) {
		cloud := misplacedEstate(t, "recreate")
		c, done := newLivePlanCommand(t, cloud)

		// No -estate here: the live block this arm writes is what names the
		// estate, and naming it twice is refused on purpose.
		code := c.Run([]string{"-no-color"})
		output := done(t)
		stdout, stderr := output.Stdout(), output.Stderr()
		if code != 0 {
			t.Fatalf("exit code %d, want 0 under the toggle\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
		}
		all := stdout + stderr

		if cloud.imported("aws_vpc", "vpc-misplaced") {
			t.Errorf("vpc-misplaced was read back, so a pass bound an object visible only to the aliased provider configuration to aws_vpc.east, whose block uses the default one. That is a wrong marker, which is worse than the missing one this replaces:\n%s", all)
		}
		if !cloud.imported("aws_vpc", "vpc-in-west") {
			t.Errorf("aws_vpc.west was not bound to its own region's object; this test proves nothing if discovery found nothing at all: %v", cloud.imports)
		}
		if !strings.Contains(stdout, "aws_vpc.east will be created") {
			t.Errorf("aws_vpc.east has no object its own provider configuration can see, so the plan must propose creating it. Anything quieter means it bound something:\n%s", all)
		}
		if strings.Contains(stdout, "aws_vpc.west will be created") {
			t.Errorf("aws_vpc.west exists in its own configuration's region and must not be proposed as a create:\n%s", all)
		}
		if !strings.Contains(stdout, "Plan: 1 to add, 0 to change, 0 to destroy") {
			t.Errorf("want exactly one create (aws_vpc.east) and nothing else:\n%s", all)
		}
		// The toggle buys the create, not silence: vpc-misplaced is still
		// named, and the coverage line beside the create no longer promises
		// that marker discovery will find it.
		if !strings.Contains(all, "Marked resource abandoned by a provider configuration change") {
			t.Errorf("the toggle silenced the finding entirely; the abandoned object has to be named on every plan that sees it:\n%s", all)
		}
		if strings.Contains(collapseSpace(all), "Marker discovery will find it") {
			t.Errorf("the coverage line still promises marker discovery will find aws_vpc.east; it lists us-east-1 now, where vpc-misplaced is not:\n%s", all)
		}
	})
}

// collapseSpace folds every run of whitespace into one space, so an
// assertion about a SENTENCE is not defeated by the width the diagnostic
// renderer happened to wrap it at.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestStatelessTestCloud_recordsEveryImportUnderConcurrency is GitHub issue
// #629's guard, and it guards the HARNESS rather than the product: the mock
// cloud every live-plan test asserts against has to record every call made
// to it, including the ones two provider instances make at the same time.
//
// It exists because the thing that made
// TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider fail 16 times in
// 5000 runs was not a wrong bind - the plan was correct in all sixteen - but
// a lost append on statelessTestCloud.imports. tofu.MockProvider's own mutex
// does not cover it: newLivePlanCommand hands out one instance per provider
// configuration, so a multi-provider fixture has two instances with two
// mutexes writing one shared slice, and projection's read prefetch calls
// them concurrently.
//
// Mutation: drop the c.mu.Lock/Unlock pair around the append in
// ImportResourceStateFn and this test loses records on essentially every
// run, with or without -race.
func TestStatelessTestCloud_recordsEveryImportUnderConcurrency(t *testing.T) {
	const (
		instances  = 2
		goroutines = 8
		perRoutine = 250
	)

	cloud := newStatelessTestCloud()
	cloud.allowRegion("us-west-2")

	// One provider instance per configuration, exactly as
	// newLivePlanCommand's factory produces them - which is what puts the
	// two callers behind two different MockProvider mutexes.
	provs := make([]providers.Interface, 0, instances)
	for _, region := range []string{"us-east-1", "us-west-2"} {
		p := cloud.provider()
		resp := p.ConfigureProvider(t.Context(), providers.ConfigureProviderRequest{
			Config: cty.ObjectVal(map[string]cty.Value{"region": cty.StringVal(region)}),
		})
		if resp.Diagnostics.HasErrors() {
			t.Fatalf("configuring the %s instance: %s", region, resp.Diagnostics.Err())
		}
		provs = append(provs, p)
	}

	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p := provs[g%instances]
			for i := range perRoutine {
				resp := p.ImportResourceState(t.Context(), providers.ImportResourceStateRequest{
					TypeName: "aws_vpc",
					Target:   providers.ImportTarget{ID: fmt.Sprintf("vpc-%d-%d", g, i)},
				})
				if resp.Diagnostics.HasErrors() {
					t.Errorf("import %d/%d: %s", g, i, resp.Diagnostics.Err())
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if want := goroutines * perRoutine; len(cloud.imports) != want {
		t.Fatalf("the cloud recorded %d imports, want %d: concurrent calls through separate provider instances are losing records, which is what makes an identity assertion fail while the plan itself is correct", len(cloud.imports), want)
	}
	// Every record distinct and present, not merely the right count: a
	// torn append can also duplicate one entry over another.
	seen := make(map[string]bool, len(cloud.imports))
	for _, key := range cloud.imports {
		if seen[key] {
			t.Fatalf("%q was recorded twice; a lost append overwrote another record", key)
		}
		seen[key] = true
	}
	for g := range goroutines {
		for i := range perRoutine {
			if want := fmt.Sprintf("aws_vpc/vpc-%d-%d", g, i); !seen[want] {
				t.Fatalf("%q was imported but never recorded", want)
			}
		}
	}
}

func statelessTestLoadConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// ---------------------------------------------------------------------------
// A mock AWS provider with a cloud behind it.
// ---------------------------------------------------------------------------

func newLivePlanCommand(t *testing.T, cloud *statelessTestCloud) (*LivePlanCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()

	view, done := testView(t)
	c := &LivePlanCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{
				Providers: map[addrs.Provider]providers.Factory{
					// A fresh instance per call, not FactoryFixed's one
					// shared one. internal/plugins' provider manager starts
					// one instance per provider CONFIGURATION, so a fresh
					// instance is what a real run has, and it is what lets
					// each configuration remember the region it was
					// configured with (statelessTestProvider.region). The
					// cloud behind them is still the single shared one, so
					// every existing test's cloud.imports, cloud.applied and
					// cloud.objects read exactly as before.
					addrs.NewDefaultProvider("aws"): func() (providers.Interface, error) {
						return cloud.provider(), nil
					},
				},
			},
		},
	}
	return c, done
}

// statelessTestCloud is the same shape as the projection package's fake
// cloud: a map of live objects keyed by type and import identity, served
// through a mock provider that speaks the import/read pair the projection
// builder uses and the plan/read pair the plan engine uses.
type statelessTestCloud struct {
	// mu guards this cloud's three RECORDING fields - imports, applied and
	// destroyed - which the mock provider's callbacks write while the
	// command under test is running. GitHub issue #629.
	//
	// tofu.MockProvider carries a mutex of its own, so one provider
	// instance's callbacks never overlap. That is not enough here for two
	// reasons that compound: newLivePlanCommand's factory hands out a FRESH
	// instance per provider configuration (each one has to remember the
	// region it was configured with), so a multi-provider fixture has two
	// instances holding two different mutexes; and projection's read
	// prefetch (internal/live/projection/readconcurrency.go's
	// startReadPrefetch) issues its import/read pairs from a pool of
	// goroutines. Two instances plus a concurrent caller means two
	// unsynchronised `c.imports = append(c.imports, key)` calls onto one
	// shared slice, and a lost append is the visible result: both VPCs bind
	// correctly, the plan is clean, and one of the two import records is
	// simply gone.
	//
	// That is what made TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider
	// fail 16 times in 5000 runs, alternating which VPC's record was lost.
	// The BINDING was right in every one of those failures - no create was
	// ever proposed and "No changes." was always printed; only the test's
	// own bookkeeping dropped an entry.
	//
	// The setup fields below (objects, tags, listed, allowedRegions,
	// regionOf) are written by the test body before the command runs and
	// only read afterwards, so they need no lock. The recording fields are
	// likewise read by the test body only after the command has returned,
	// which is why the accessors take the lock but the direct field reads
	// in assertions do not need it.
	mu sync.Mutex

	objects map[string]map[string]string
	imports []string

	// tags holds the tags each stored object carries, which is what a
	// resource stamped by an earlier run looks like when it is read back.
	tags map[string]map[string]string

	// listed holds what the list protocol serves, per type, which is where
	// ownership markers live: discovery reads tags off listed objects, and
	// the projection reads objects back through import/read.
	listed map[string][]statelessTestListed

	// applied records the tags each address was created or updated with,
	// keyed by the tofu-address marker the object carries. It is what an
	// apply test reads to check that the markers stamping injected actually
	// reached the cloud rather than only the rendered plan.
	applied map[string]map[string]string

	// destroyed records, in the order ApplyResourceChange actually saw
	// them, the tofu-address of every instance destroyed - GitHub issue
	// #320's DestroyMode test reads this to check that a whole-estate
	// "apply -destroy" reached the cloud for every owned instance, not just
	// that the rendered plan proposed it. Keyed off PriorState, the only
	// side of the request a destroy still carries any tags on: PlannedState
	// is null for a destroy, exactly like the real provider protocol.
	destroyed []string

	// allowedRegions is what the mock provider's ConfigureProviderFn insists
	// a provider block's region argument be, one of. Every test but the
	// multi-provider ones only ever configures one provider - see
	// allowRegion - and us-east-1 is the region every existing fixture's
	// provider block already names, so a fresh cloud accepting only it
	// keeps every one of those tests' own check ("the command evaluated the
	// provider block") exactly as strict as it always was.
	allowedRegions map[string]bool

	// regionOf says which region a listed object lives in, keyed the same
	// way objects and tags are (type + "/" + id). It is what makes this
	// cloud partitioned rather than global: a provider configured for one
	// region does not ENUMERATE another region's objects, which is what a
	// real regional AWS service does and the only way a test can tell
	// "discovery listed through this resource's own provider configuration"
	// apart from "discovery listed through whichever one came first and got
	// lucky".
	//
	// An object with no entry here is region-free and every provider
	// configuration lists it, which is what keeps every fixture written
	// before this existed behaving exactly as it did.
	//
	// Read-back (ImportResourceState, ReadResource) is deliberately NOT
	// partitioned. A wrong bind in discovery must stay visible as a wrong
	// bind rather than being rescued by a second layer noticing the object
	// is not in the region it was read through: IDs do collide across
	// regions in practice, and a guard that only fires when they do not is
	// not a guard. See TestLivePlan_needsDiscoveryBindsThroughItsOwnProvider
	// and its wrong-region twin.
	regionOf map[string]string

	// onImport, when non-nil, is called on entry to every
	// ImportResourceState and again as that call returns, on the calling
	// goroutine. It is the only way a test can see how WIDE the read pass
	// is rather than only which calls it made: the recorded imports say
	// what was read, and nothing in this cloud otherwise says how many were
	// in flight at once. See [readWidthRecorder] in
	// live_read_parallelism_test.go, which is the only thing that sets it.
	//
	// A setup field like objects and tags above, not a recording field: it
	// is written by the test body before the command runs and only read
	// while it runs, so it needs no lock. Whatever it points at is called
	// concurrently and owns its own synchronisation.
	onImport func(entering bool)
}

// allowRegion widens the set of regions this cloud's mock provider accepts
// being configured with, for a test whose fixture declares more than one
// provider configuration (issue #69's multi-provider sweep).
func (c *statelessTestCloud) allowRegion(region string) {
	c.allowedRegions[region] = true
}

// statelessTestListed is one live resource as the list protocol serves it.
type statelessTestListed struct {
	id          string
	displayName string
	tags        map[string]string
	attrs       map[string]string
}

func newStatelessTestCloud() *statelessTestCloud {
	return &statelessTestCloud{
		objects:        make(map[string]map[string]string),
		tags:           make(map[string]map[string]string),
		listed:         make(map[string][]statelessTestListed),
		applied:        make(map[string]map[string]string),
		allowedRegions: map[string]bool{"us-east-1": true},
		regionOf:       make(map[string]string),
	}
}

// inRegion places an already-stored object in one region, so only a provider
// configured for that region enumerates it. See [statelessTestCloud.regionOf].
func (c *statelessTestCloud) inRegion(typeName, id, region string) {
	c.regionOf[typeName+"/"+id] = region
	c.allowedRegions[region] = true
}

func (c *statelessTestCloud) put(typeName, importID string, attrs map[string]string) {
	c.objects[typeName+"/"+importID] = attrs
}

// putMarked is put for a live resource that already carries this estate's
// ownership markers - the shape every resource has once a stamped plan has
// been applied to it. Stamping such a resource again changes nothing, so a
// test that wants a clean plan uses this rather than put.
func (c *statelessTestCloud) putMarked(typeName, importID, estate, addr string, attrs map[string]string) {
	c.put(typeName, importID, attrs)
	c.tags[typeName+"/"+importID] = map[string]string{
		"tofu-estate":  estate,
		"tofu-address": addr,
	}
}

// list adds a live resource the provider will enumerate.
func (c *statelessTestCloud) list(typeName, id, displayName string, tags, attrs map[string]string) {
	c.listed[typeName] = append(c.listed[typeName], statelessTestListed{
		id: id, displayName: displayName, tags: tags, attrs: attrs,
	})
}

func (c *statelessTestCloud) imported(typeName, importID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := typeName + "/" + importID
	for _, got := range c.imports {
		if got == want {
			return true
		}
	}
	return false
}

// statelessTestSchemas is a caricature of the AWS provider: the two resource
// types the fixtures use, with the attributes their bodies set.
func statelessTestSchemas() map[string]providers.Schema {
	schema := func(names ...string) providers.Schema {
		attrs := map[string]*configschema.Attribute{
			"tags": {Type: cty.Map(cty.String), Optional: true},
		}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		return providers.Schema{Block: &configschema.Block{Attributes: attrs}}
	}
	base := map[string]providers.Schema{
		"aws_s3_bucket": schema("id", "bucket", "arn"),
		"aws_vpc":       schema("id", "cidr_block"),
		// The keyed type: a for_each member whose key lives in its
		// tofu-address marker and nowhere else.
		"aws_subnet": schema("id", "cidr_block", "availability_zone"),
		// The fungible type. Nothing in its schema distinguishes one
		// instance from another, which is the whole reason a count set needs
		// slot markers.
		"aws_eip": schema("id", "domain"),
		// The unresolvable-identity fixture (live-plan-no-identity,
		// live-plan-target-scope): "name" is the real provider's own
		// argument the identity table's aws_iam_group row reads
		// (table_generated.go), left unset by both fixtures on purpose so
		// resolution has nothing to derive an identity from. Real, not a
		// caricature gap: unlike the other four types here, this table has
		// never needed a schema for it before CHOUDOUFU_NODE_RESOLVE
		// defaulted on, because a static-evaluator refusal always aborted
		// the run before OpenTofu's own Validate pass ever asked for
		// aws_iam_group's schema at all - see TestLivePlan_identityFatal's
		// own comment. Downgraded to a warning under the flag, this reaches
		// Validate for the first time, and Validate needs a schema for
		// every declared type regardless of what this fork does with it -
		// the real provider always has one.
		"aws_iam_group": schema("id", "name", "arn"),
	}
	// A write-only settable attribute on the bucket, so command-level tests
	// can pin that lint.CheckResidueAttributes is actually WIRED into the
	// live entry points - the wave-3 audit removed the call and watched
	// this suite stay green, which is the unpinned-wiring shape wave 1
	// found once already.
	//
	// Write-only rather than Sensitive since GitHub issue #365 slice 3.
	// The sensitive half of that warning now fires only under
	// strict { secrets = "refuse" }, because under the default such an
	// argument IS remembered - so a Sensitive attribute here would make this
	// wiring guard depend on a setting the fixture does not write, and the
	// guard would go quiet for the wrong reason. Write-only is the half no
	// setting reaches: the plugin protocol forbids the provider ever
	// returning the value.
	base["aws_s3_bucket"].Block.Attributes["secret_policy_seed"] = &configschema.Attribute{
		Type: cty.String, Optional: true, WriteOnly: true,
	}
	return base
}

// statelessTestListSchemas is the list-protocol half of the caricature: the
// EC2-shaped list configuration (a region and repeatable filter blocks) for
// the one type in the fixture whose identity is server-assigned.
func statelessTestListSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		// aws_eip's real list schema has no filter argument, so its estate
		// filter is applied client-side. Modelled here rather than smoothed
		// over, because the count set lives on this type.
		"aws_eip": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{},
		}},
		"aws_subnet": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"filter": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"name":   {Type: cty.String, Required: true},
							"values": {Type: cty.List(cty.String), Required: true},
						},
					},
				},
			},
		}},
		"aws_vpc": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"filter": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"name":   {Type: cty.String, Required: true},
							"values": {Type: cty.List(cty.String), Required: true},
						},
					},
				},
			},
		}},
	}
}

func statelessTestIdentitySchemas() map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(statelessTestSchemas()))
	for name, schema := range statelessTestSchemas() {
		schema.IdentitySchema = &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"id":         {Type: cty.String, Required: true},
				"account_id": {Type: cty.String, Optional: true},
			},
		}
		out[name] = schema
	}
	return out
}

// statelessTestProvider is the mock plus the list protocol. Listing is not
// part of providers.Interface - the stateless list client asks for it by
// assertion - so it is added here rather than on the mock itself.
type statelessTestProvider struct {
	*tofu.MockProvider
	cloud *statelessTestCloud

	// region is what THIS instance was configured with. One instance is
	// created per provider configuration (see newLivePlanCommand's factory),
	// so it is the endpoint a list issued through this configuration reaches
	// - and the whole basis on which this cloud is partitioned.
	region string
}

func (p *statelessTestProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	schema := statelessTestSchemas()[req.TypeName]
	for _, o := range p.cloud.listed[req.TypeName] {
		if home, placed := p.cloud.regionOf[req.TypeName+"/"+o.id]; placed && home != p.region {
			// This object lives somewhere this provider configuration does
			// not reach. A regional AWS service answers a list with its own
			// region's objects and nothing else, and that is the only thing
			// that can distinguish "swept through this resource's own
			// provider configuration" from "swept through some provider
			// configuration".
			continue
		}
		attrs := map[string]string{"id": o.id}
		for k, v := range o.attrs {
			attrs[k] = v
		}
		ev := providers.ListResourceEvent{
			DisplayName: o.displayName,
			Identity: cty.ObjectVal(map[string]cty.Value{
				"id":         cty.StringVal(o.id),
				"account_id": cty.StringVal("000000000000"),
			}),
		}
		if req.IncludeResourceObject {
			ev.ResourceObject = statelessTestObjectWithTags(schema, attrs, o.tags)
		}
		if !emit(ev) {
			break
		}
	}
	return diags
}

func (c *statelessTestCloud) provider() providers.Interface {
	// inst is built first so ConfigureProviderFn below can record the region
	// THIS instance was configured with on it. One instance exists per
	// provider configuration, which is what lets ListResourceStream serve
	// each configuration only its own region's objects.
	inst := &statelessTestProvider{cloud: c}

	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"region": {Type: cty.String, Optional: true},
				},
			}},
			ResourceTypes:     statelessTestIdentitySchemas(),
			ListResourceTypes: statelessTestListSchemas(),
		},
	}

	p.ConfigureProviderFn = func(req providers.ConfigureProviderRequest) (resp providers.ConfigureProviderResponse) {
		// The command has to evaluate the provider block before it can read
		// anything, so a provider that insists on its configuration is the
		// check that it did. allowedRegions is one entry (us-east-1) for
		// every ordinary fixture and widened by allowRegion for a
		// multi-provider one.
		region := req.Config.GetAttr("region")
		if region.IsNull() || !c.allowedRegions[region.AsString()] {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("provider was configured with region %#v", region))
			return resp
		}
		inst.region = region.AsString()
		return resp
	}

	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
		// GitHub issue #626's width probe, nil for every test but the one
		// that measures how many of these calls overlap. Entered before any
		// work so that a call this hook holds is holding a slot the read
		// pass has actually issued.
		if c.onImport != nil {
			c.onImport(true)
			defer c.onImport(false)
		}
		// Both forms, because a real provider serving an identity schema
		// gets both: an identity object where the run has one, and the
		// import-ID string otherwise. The two are exclusive on the wire
		// (providers.ImportTarget), and every type in this caricature is
		// identified by its id, so they name the same object.
		id := req.Target.ID
		if req.Target.IsIdentityBased() {
			id = req.Target.Identity.GetAttr("id").AsString()
		}
		key := req.TypeName + "/" + id
		c.mu.Lock()
		c.imports = append(c.imports, key)
		c.mu.Unlock()
		schema := statelessTestSchemas()[req.TypeName]
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: req.TypeName,
			State:    statelessTestObject(schema, map[string]string{"id": id}),
		}}
		return resp
	}

	p.ReadResourceFn = func(req providers.ReadResourceRequest) (resp providers.ReadResourceResponse) {
		schema := statelessTestSchemas()[req.TypeName]
		id := req.PriorState.GetAttr("id")
		key := req.TypeName + "/" + id.AsString()
		attrs, ok := c.objects[key]
		if !ok {
			resp.NewState = cty.NullVal(schema.Block.ImpliedType())
			return resp
		}
		resp.NewState = statelessTestObjectWithTags(schema, attrs, c.tags[key])
		return resp
	}

	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) (resp providers.PlanResourceChangeResponse) {
		resp.PlannedState = req.ProposedNewState
		return resp
	}

	// The apply half of the caricature. The planned object is returned
	// unchanged, which keeps the provider consistent with its own plan, and
	// the tags it carries are recorded under the address in its marker: that
	// is how an apply test sees whether stamping's markers reached the cloud
	// or only the rendered diff.
	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) (resp providers.ApplyResourceChangeResponse) {
		resp.NewState = req.PlannedState
		if req.PlannedState.IsNull() {
			// A destroy. PlannedState carries nothing to key off, so the
			// address comes from what was there before.
			key := statelessTestTagsOf(req.PriorState)["tofu-address"]
			if key == "" {
				key = req.TypeName
			}
			c.mu.Lock()
			c.destroyed = append(c.destroyed, key)
			c.mu.Unlock()
			return resp
		}
		tags := statelessTestTagsOf(req.PlannedState)
		key := tags["tofu-address"]
		if key == "" {
			key = req.TypeName
		}
		c.mu.Lock()
		c.applied[key] = tags
		c.mu.Unlock()
		return resp
	}

	inst.MockProvider = p
	return inst
}

// statelessTestTagsOf reads a resource object's tags map back out as plain
// strings, skipping anything unknown or null.
func statelessTestTagsOf(obj cty.Value) map[string]string {
	out := make(map[string]string)
	if obj.IsNull() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute("tags") {
		return out
	}
	tags := obj.GetAttr("tags")
	if tags.IsNull() || !tags.IsKnown() {
		return out
	}
	for k, v := range tags.AsValueMap() {
		if v.IsKnown() && !v.IsNull() {
			out[k] = v.AsString()
		}
	}
	return out
}

func statelessTestObject(schema providers.Schema, attrs map[string]string) cty.Value {
	return statelessTestObjectWithTags(schema, attrs, nil)
}

func statelessTestObjectWithTags(schema providers.Schema, attrs, tags map[string]string) cty.Value {
	vals := make(map[string]cty.Value, len(schema.Block.Attributes))
	for name, at := range schema.Block.Attributes {
		if v, ok := attrs[name]; ok && at.Type == cty.String {
			vals[name] = cty.StringVal(v)
			continue
		}
		vals[name] = cty.NullVal(at.Type)
	}
	if _, ok := schema.Block.Attributes["tags"]; ok {
		vals["tags"] = cty.NullVal(cty.Map(cty.String))
		if len(tags) > 0 {
			tagVals := make(map[string]cty.Value, len(tags))
			for k, v := range tags {
				tagVals[k] = cty.StringVal(v)
			}
			vals["tags"] = cty.MapVal(tagVals)
		}
	}
	return cty.ObjectVal(vals)
}

// progressRecordingView is a minimal views.StatelessPlan stub that records
// every call to Progress and discards everything else, for testing
// statelessProgress's throttling in isolation from any real rendering.
type progressRecordingView struct {
	progress []views.StatelessProgress
}

var _ views.StatelessPlan = (*progressRecordingView)(nil)

func (v *progressRecordingView) Progress(p views.StatelessProgress) {
	v.progress = append(v.progress, p)
}
func (v *progressRecordingView) Omissions([]views.StatelessOmission)   {}
func (v *progressRecordingView) Unowned([]views.StatelessUnowned)      {}
func (v *progressRecordingView) Foreign(views.StatelessForeign)        {}
func (v *progressRecordingView) Policy(views.StatelessPolicyReport)    {}
func (v *progressRecordingView) GuidedFallback(string)                 {}
func (v *progressRecordingView) Lookalikes([]views.StatelessLookalike) {}
func (v *progressRecordingView) Adoption(views.StatelessAdoption)      {}
func (v *progressRecordingView) Document(views.LivePlanDocument) bool  { return true }

// TestStatelessProgress_throttlesButAlwaysShowsTheFirstEvent pins
// statelessProgress's whole job: discovery reports every type it scans,
// which for a fast-listing provider is too fine-grained to print, so this
// is where "how often" is decided. The first event has to pass through
// unthrottled - it is a reader's first evidence the run has not hung - and
// anything arriving within statelessProgressInterval of the last one shown
// has to be dropped.
func TestStatelessProgress_throttlesButAlwaysShowsTheFirstEvent(t *testing.T) {
	rec := &progressRecordingView{}
	report := statelessProgress(rec)

	report(discovery.ProgressEvent{TypeName: "aws_vpc", TypesScanned: 1, ResourcesFound: 1})
	report(discovery.ProgressEvent{TypeName: "aws_subnet", TypesScanned: 2, ResourcesFound: 3})

	if len(rec.progress) != 1 {
		t.Fatalf("got %d events immediately after the first, want 1 (the second is inside the throttle window): %+v", len(rec.progress), rec.progress)
	}
	if rec.progress[0].TypeName != "aws_vpc" {
		t.Errorf("the event that passed through is %q, want the first one", rec.progress[0].TypeName)
	}

	time.Sleep(statelessProgressInterval + 50*time.Millisecond)
	report(discovery.ProgressEvent{TypeName: "aws_eip", TypesScanned: 3, ResourcesFound: 5})

	if len(rec.progress) != 2 {
		t.Fatalf("got %d events after the throttle window elapsed, want 2: %+v", len(rec.progress), rec.progress)
	}
	if rec.progress[1].TypeName != "aws_eip" {
		t.Errorf("the second event that passed through is %q, want aws_eip", rec.progress[1].TypeName)
	}
}

// TestStatelessNeedsDiscovery_keyedModuleKeyForm is GitHub issue #111's
// regression guard.
//
// The map statelessNeedsDiscovery builds is consumed by two readers that both
// key on an addrs.ConfigResource, which carries no instance keys:
// stamp.mustStamp builds its lookup from one, and stamp.Skip.Addr (what
// statelessStampGaps compares) is one. stamp.Request.NeedsDiscovery documents
// the contract as "module-qualified block address".
//
// Identity resolution walks KEYED module instances, so producing the map from
// AbsResource.String() emitted module.wrapped["a"].aws_eip.app while both
// readers looked up module.wrapped.aws_eip.app. Inside a for_each'd module
// the two could never match, so mustStamp returned false for every resource
// there: the must-stamp error silently became a warning, and a
// server-assigned resource could be created carrying no ownership marker,
// which nothing later can find. live/LIMITATIONS.md documents that error as
// firing.
func TestStatelessNeedsDiscovery_keyedModuleKeyForm(t *testing.T) {
	cfg := statelessTestLoadConfigWithModules(t, "../../live/e2e/estate-module-keyed")

	resolutions, diags := identity.Resolve(t.Context(), cfg)
	if diags.HasErrors() {
		t.Fatalf("resolving the keyed-module fixture: %s", diags.Err())
	}
	if len(resolutions.NeedsDiscovery()) == 0 {
		t.Fatal("fixture resolved no needs-discovery instances; it can no longer guard this")
	}

	got := statelessNeedsDiscovery(resolutions)
	if len(got) == 0 {
		t.Fatal("statelessNeedsDiscovery produced no keys")
	}

	for key := range got {
		if strings.Contains(key, `["`) {
			t.Errorf("key %q carries an instance key; both readers look up an addrs.ConfigResource, which never has one", key)
		}
	}

	// The positive half: the key the readers will actually build for the
	// block inside the keyed module has to be present.
	const want = "module.wrapped.aws_eip.app"
	if _, ok := got[want]; !ok {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		t.Errorf("missing %q, which is what stamp.mustStamp looks up; got %v", want, keys)
	}
}

// statelessTestLoadConfigWithModules is statelessTestLoadConfig for a fixture
// that calls local child modules, which the plain helper refuses on purpose.
func statelessTestLoadConfigWithModules(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	vars := func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil }

	load := func(addr addrs.Module, srcDir string) (*configs.Module, hcl.Diagnostics) {
		call := configs.NewStaticModuleCall(addr, hcl.Range{}, vars, srcDir, "default")
		return parser.LoadConfigDir(srcDir, call)
	}

	mod, diags := load(addrs.RootModule, dir)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}

	cfg, cfgDiags := configs.BuildConfig(t.Context(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			child, childDiags := load(req.Path, filepath.Join(dir, req.SourceAddr.String()))
			return child, nil, childDiags
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

// TestStatelessStampGaps_trustedKeyedModuleIsNotAGap is the regression guard
// for the defect the #111 key fix introduced and this test's predecessor
// missed.
//
// stamp's keyed-module handling files two very different outcomes: a resource
// inside a for_each'd module that ALREADY declares tags is skipped as trusted
// (its markers are the operator's own, written by hand per the idiom
// live/LIMITATIONS.md documents), while one declaring none gets the must-stamp
// error. Both used to carry SkipModuleKeyed, and statelessStampGaps exempts
// only by Reason - so once #111 made needsDiscovery actually match inside a
// keyed module, the benign half started producing a hard error telling the
// operator their marker was missing while it sat in the file above.
//
// TestStatelessNeedsDiscovery_keyedModuleKeyForm did not catch it because it
// asserts the key FORM and stops one layer short of the consumer. This test is
// that layer.
func TestStatelessStampGaps_trustedKeyedModuleIsNotAGap(t *testing.T) {
	addr := addrs.ConfigResource{
		Module:   addrs.Module{"wrapped"},
		Resource: addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_eip", Name: "app"},
	}
	// The zero BlockDiscovery on purpose: presence in the map is what makes
	// a resource marker-only, and an entry whose cause was never established
	// must escalate exactly as one whose cause is known does. A reader that
	// indexed the map and compared against the zero value instead of using
	// the comma-ok form would pass every assertion above and silently
	// downgrade this one.
	needsDiscovery := map[string]identity.BlockDiscovery{addr.String(): {}}

	t.Run("trusted hand-stamped markers are not a gap", func(t *testing.T) {
		res := &stamp.Result{Skipped: []stamp.Skip{{
			Addr:   addr,
			Reason: stamp.SkipModuleKeyedTrusted,
			Detail: "Declared inside a for_each'd module; its markers are trusted as written, not verified.",
		}}}
		if diags := statelessStampGaps(res, needsDiscovery, nil); diags.HasErrors() {
			t.Errorf("a resource carrying its own hand-written markers was reported as an unstamped gap: %s", diags.Err())
		}
	})

	t.Run("a keyed-module resource with no tags argument is still a gap", func(t *testing.T) {
		res := &stamp.Result{Skipped: []stamp.Skip{{
			Addr:   addr,
			Reason: stamp.SkipModuleKeyed,
			Detail: "declares no tags argument",
		}}}
		diags := statelessStampGaps(res, needsDiscovery, nil)
		if !diags.HasErrors() {
			t.Error("a marker-discovered resource with no tags argument inside a keyed module was not reported; that is the guarantee #111 exists to restore")
		}
	})
}

// TestLivePlan_targetScopesTheStatelessPipeline is GitHub issue #352, at the
// level the report was written from: the whole live-plan pipeline, over a
// configuration holding one resource whose identity this fork cannot resolve.
//
// Untargeted, the run refuses on that resource - the first subtest is the
// behaviour that must not change, and it is also the mutation check on the
// second, because a fixture that had stopped refusing would let the targeted
// run pass for no reason at all.
//
// Targeted at the OTHER resource, the run succeeds, because stock OpenTofu's
// plan graph drops the unresolvable one before anything evaluates it and the
// passes in front of the plan now agree. Before the fix, identity resolution,
// the data-read phase, discovery and stamping all walked the whole
// configuration regardless of -target, so this exited 1 with the identity
// refusal below while a plain "tofu plan" over the same -target set exited 0.
func TestLivePlan_targetScopesTheStatelessPipeline(t *testing.T) {
	run := func(t *testing.T, args ...string) (int, *terminal.TestOutput) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan-target-scope"), td)
		t.Chdir(td)

		cloud := newStatelessTestCloud()
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
		// "No source for this instance's identity", not "Identity argument
		// not set": with CHOUDOUFU_NODE_RESOLVE defaulting on (2026-08-25),
		// the static evaluator's own error downgrades to a warning
		// (identity.DowngradeForNodeResolution) and the node resolver's own
		// #365 refusal is what stays fatal - see TestLivePlan_identityFatal
		// for the parallel default/opt-out proof over the single-resource
		// fixture this one shares its shape with.
		if !strings.Contains(output.Stderr(), "No source for this instance's identity") {
			t.Errorf("an untargeted run no longer refuses the unresolvable resource, so the targeted case below proves nothing:\n%s", output.Stderr())
		}
	})

	t.Run("targeted proceeds", func(t *testing.T) {
		code, output := run(t, "-target=aws_s3_bucket.data")
		if code != 0 {
			t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
		}
		if strings.Contains(output.Stderr(), "Identity argument not set") || strings.Contains(output.Stderr(), "No source for this instance's identity") {
			t.Errorf("a targeted run still refused on a resource outside its own target set:\n%s", output.Stderr())
		}
		if strings.Contains(output.Stdout(), "aws_iam_group.orphaned") {
			t.Errorf("the untargeted resource reached the plan:\n%s", output.Stdout())
		}
	})
}

// ---------------------------------------------------------------------------
// GitHub issue #894: the document on a configuration that names its estate
// ---------------------------------------------------------------------------

// TestLivePlan_jsonDocumentReachesADeclaredEstate is GitHub issue #894's
// first half. Before it, GitHub issue #788's document was reachable only
// through "live-plan -estate=NAME -json", and that flag is refused on a
// configuration that names its own estate - so the shape the docs recommend
// (the estate declared in the configuration, the estate.chdf.hcl sidecar
// being the leading form) was the one shape the document could not be
// produced for. Both commands and both declaration shapes are driven here,
// because #894 was filed by a consumer (INTENTIUS/chant's terraform
// lexicon, chant #2104) that has to pick one and get the same answer.
//
// The claim is not just "some JSON came out". It is that the document is
// the SAME document the -estate form produces over the same cloud, compared
// as bytes: chant's reader is written against #788's document as recorded,
// and a route that produced a differently-shaped one would satisfy the
// letter of the issue and break the reader anyway.
func TestLivePlan_jsonDocumentReachesADeclaredEstate(t *testing.T) {
	// The reference: the one route that already worked. The live-plan and
	// live-block fixtures are identical apart from the live block (see
	// testdata/live-block/main.tf's own comment), so the same cloud serves
	// both and the two documents have to agree.
	reference := func() string {
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-plan"), td)
		t.Chdir(td)

		c, done := newLivePlanCommand(t, liveBlockCloud())
		code := c.Run([]string{"-no-color", "-estate=stateless-unit", "-json"})
		out := done(t)
		if code != 0 {
			t.Fatalf("the -estate form exited %d, want 0 - this test's reference is broken\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
		}
		return out.Stdout()
	}()
	if !strings.HasPrefix(reference, "{") {
		t.Fatalf("the -estate form's stdout does not start with the document:\n%s", reference)
	}

	for _, tc := range []struct {
		name    string
		fixture string
		// run is the command under test, taking the same arguments every
		// route gets and returning its exit code.
		run func(t *testing.T, cloud *statelessTestCloud) (int, *terminal.TestOutput)
		// sameAsReference is false for the sidecar fixture, which declares
		// only the bucket and so cannot produce the same document as a
		// fixture declaring bucket and VPC.
		sameAsReference bool
	}{
		{
			name:    "live-plan over a live block",
			fixture: "live-block",
			run: func(t *testing.T, cloud *statelessTestCloud) (int, *terminal.TestOutput) {
				view, done := testView(t)
				c := &LivePlanCommand{Meta: liveBlockMeta(view, cloud)}
				return c.Run([]string{"-no-color", "-json"}), done(t)
			},
			sameAsReference: true,
		},
		{
			name:    "plan over a live block",
			fixture: "live-block",
			run: func(t *testing.T, cloud *statelessTestCloud) (int, *terminal.TestOutput) {
				c, done := newLiveBlockPlanCommand(t, cloud)
				return c.Run([]string{"-no-color", "-json"}), done(t)
			},
			sameAsReference: true,
		},
		{
			name:    "live-plan over an estate.chdf.hcl sidecar",
			fixture: "live-plan-sidecar",
			run: func(t *testing.T, cloud *statelessTestCloud) (int, *terminal.TestOutput) {
				view, done := testView(t)
				c := &LivePlanCommand{Meta: liveBlockMeta(view, cloud)}
				return c.Run([]string{"-no-color", "-json"}), done(t)
			},
		},
		{
			name:    "plan over an estate.chdf.hcl sidecar",
			fixture: "live-plan-sidecar",
			run: func(t *testing.T, cloud *statelessTestCloud) (int, *terminal.TestOutput) {
				c, done := newLiveBlockPlanCommand(t, cloud)
				return c.Run([]string{"-no-color", "-json"}), done(t)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath(tc.fixture), td)
			t.Chdir(td)

			cloud := liveBlockCloud()
			code, out := tc.run(t, cloud)
			if code != 0 {
				t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
			}

			// What a consumer does: read stdout, parse it, use it. No
			// skipping to the first bare "{".
			stdout := out.Stdout()
			var doc views.LivePlanDocument
			if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("stdout does not parse as one JSON document: %s\nstdout:\n%s", err, stdout)
			}
			if doc.Estate != "stateless-unit" {
				t.Errorf("document estate = %q, want %q - the estate name did not come from the configuration", doc.Estate, "stateless-unit")
			}
			if doc.UpstreamVersion == "" {
				t.Error("document's upstream_version is empty")
			}
			if len(doc.Bound) == 0 {
				t.Errorf("bound is empty, so this route read nothing from the live system: %#v", doc)
			}
			if strings.Contains(out.Stderr(), "\"estate\"") {
				t.Errorf("the document, or part of it, was written to stderr:\n%s", out.Stderr())
			}

			if tc.sameAsReference && stdout != reference {
				t.Errorf("this route's document differs from the -estate form's over the same cloud.\n--- this route ---\n%s\n--- -estate ---\n%s", stdout, reference)
			}
		})
	}
}

// TestLivePlan_jsonOnADeclaredEstateKeepsItsRefusals is the other side of
// GitHub issue #894: the route it opens does not become a hole in the
// refusals, and the one refusal the issue is NOT about stays exactly where
// it was.
//
// -estate beside a live block is still the ambiguity the block exists to
// remove, whatever the output format. -out and -destroy are refused because
// LivePlanCommand.livePlan writes no plan file and calls the planner with
// plans.NormalMode hardcoded, so either flag would be accepted and then
// ignored - and each has to say so in wording that is true of a
// configuration WITH a live block, which is the drift GitHub issue #619 was
// about.
func TestLivePlan_jsonOnADeclaredEstateKeepsItsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
		// absent is wording that must NOT appear: the -estate surface's own
		// -out refusal asserts "this configuration has no live block",
		// which is exactly false here.
		absent string
	}{
		{
			name: "-estate beside the live block",
			args: []string{"-no-color", "-json", "-estate=other-estate"},
			want: "Estate named by both the live block and -estate",
		},
		{
			name:   "-out",
			args:   []string{"-no-color", "-json", "-out=tfplan"},
			want:   "-out and -json cannot be combined",
			absent: "this configuration has no live block",
		},
		{
			name:   "-destroy",
			args:   []string{"-no-color", "-json", "-destroy"},
			want:   "-destroy and -json cannot be combined",
			absent: "live-plan's -estate form",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
			code := c.Run(tc.args)
			out := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
			}
			if !strings.Contains(out.Stderr(), tc.want) {
				t.Errorf("stderr does not carry %q:\n%s", tc.want, out.Stderr())
			}
			if tc.absent != "" && strings.Contains(unwrapped(out.Stderr()), tc.absent) {
				t.Errorf("the refusal still says %q, which is false of a configuration with a live block:\n%s", tc.absent, out.Stderr())
			}
			if got := out.Stdout(); got != "" {
				t.Errorf("a refused -json run wrote to stdout, which a consumer parses:\n%s", got)
			}
		})
	}
}

// TestLivePlan_jsonStdoutCarriesOnlyTheDocument is GitHub issue #894's
// second half at the command level: the plan graph's own [views.UiHook]
// lines land on stderr, and stdout holds exactly one JSON document.
//
// Before the fix those lines went through streams.Stdout (hook_ui.go's
// println, which the -json run's forced-Human plan view still installed),
// so against a live emulator a configuration with one data source printed
//
//	data.aws_caller_identity.current: Reading...
//
// ahead of the document and "choudoufu live-plan -json | jq ." died with
// "Invalid numeric literal at line 1, column 33". The mechanism is pinned
// in views (TestStdoutOnStderr_hooksAndWarningsLeaveStdoutAlone); this
// pins that the command actually uses it.
//
// The cloud is EMPTY on purpose. Every other -json test here runs against
// an estate that is already applied, where the plan binds everything from
// the projection and the hooks never fire - so it could not tell a fixed
// run from a broken one. An empty cloud makes the plan attempt the import
// and the hooks speak, which is what the stderr assertion below checks
// before the stdout assertion is allowed to mean anything.
func TestLivePlan_jsonStdoutCarriesOnlyTheDocument(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-block"), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, newStatelessTestCloud())
	code := c.Run([]string{"-no-color", "-json"})
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}

	const hookLine = "aws_s3_bucket.data: Preparing import..."
	switch {
	case strings.Contains(out.Stdout(), hookLine):
		t.Fatalf("the plan graph's hook line %q went to stdout, ahead of the document a consumer parses - GitHub issue #894's own defect:\nstdout:\n%s", hookLine, out.Stdout())
	case !strings.Contains(out.Stderr(), hookLine):
		t.Fatalf("the plan graph's own hooks printed %q nowhere, so this test cannot tell a fixed run from a broken one:\nstdout:\n%s\nstderr:\n%s", hookLine, out.Stdout(), out.Stderr())
	}

	stdout := out.Stdout()
	var doc views.LivePlanDocument
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout does not parse as one JSON document - something printed beside it: %s\nstdout:\n%s", err, stdout)
	}
	if doc.Estate != "stateless-unit" {
		t.Errorf("document estate = %q, want %q", doc.Estate, "stateless-unit")
	}
	if len(doc.Omissions) != 2 {
		t.Errorf("omissions has %d entries, want 2 (nothing exists in this cloud): %#v", len(doc.Omissions), doc.Omissions)
	}
}
