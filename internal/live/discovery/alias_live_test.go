// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// This is issue #64's provider-alias e2e, closed by issue #69: two aws
// provider configurations - the default and one aliased "west" - each
// pointed at a different region against floci's single endpoint
// (testdata/alias-e2e), stood up by stock terraform, and then
// `choudoufu live-plan` over the lot. It drives the real command, the same
// way foreign_live_test.go's TestForeignAgainstFloci does, because the
// claim under test - "a plan over resources from two aliased provider
// configurations works" - is a property of the command, not of a function
// this package exports.
//
// History: this test used to skip with a recorded finding rather than pass
// or fail outright, because live-plan refused any configuration whose
// managed resources spanned more than one provider configuration at all
// (internal/command/live_plan.go's now-removed statelessDiscoveryProvider) -
// even here, where neither resource needs marker-based discovery in the
// first place. Issue #69 made the estate-wide sweep provider-aware
// (statelessDiscover loops it once per distinct managed-resource provider
// configuration and internal/live/discovery.Merge combines the results),
// and this is that fix's own acceptance test: it now asserts a real,
// passing plan rather than recording why one could not be produced.
//
//	TF_FLOCI_TEST=1 go test ./internal/live/discovery/ -run TestAliasedProvidersAgainstFloci -v
func TestAliasedProvidersAgainstFloci(t *testing.T) {
	flocitest.Gate(t, "discovery/provider-alias")
	flocitest.RequireBinary(t, "docker")
	flocitest.RequireBinary(t, "go")
	flocitest.RequireBinary(t, terraformBin)

	flociPort := flocitest.StartFloci(t, "cdf-alias")
	endpoint := flocitest.Endpoint(flociPort)

	t.Setenv("AWS_ENDPOINT_URL", endpoint)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	// No AWS_REGION: both provider blocks name their own region explicitly
	// (versions.tf), which is the whole point - a default region here would
	// mask a bug where the aliased provider's own region argument was
	// silently ignored.
	flocitest.PluginCacheDir(t)

	tofuBin := flocitest.BuildTofu(t)
	dir := copyAliasFixture(t)

	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")
	flocitest.Run(t, dir, terraformBin, "apply", "-auto-approve", "-input=false", "-no-color")
	flocitest.Run(t, dir, tofuBin, "init", "-input=false", "-no-color")

	stateFile := filepath.Join(dir, "terraform.tfstate")
	if _, err := os.Stat(stateFile); err != nil {
		t.Fatalf("stock apply left no state file: %v", err)
	}
	if err := os.Remove(stateFile); err != nil {
		t.Fatalf("removing the state file: %v", err)
	}
	_ = os.Remove(stateFile + ".backup")

	// --- Both regions really did get their own bucket ---------------------
	//
	// Checked independently of the command: the AWS CLI reads each region
	// back directly, with no choudoufu code in the path, so this confirms
	// stock terraform's apply actually exercised two distinct provider
	// configurations against floci before live-plan is asked to do
	// anything with the result. Both queries are expected to list *both*
	// bucket names - s3api list-buckets is account-global in real AWS (a
	// bucket's name is unique across every region, and listing it is not
	// scoped to the region the request was signed for), so this is not the
	// region-scope claim internal/live/cloudcontrol/doc.go's "Signing"
	// section makes for Cloud Control; that machinery lives in a code path
	// this fixture (aws_s3_bucket, listed through the AWS provider's own
	// native list resource) never reaches at all.
	eastList := flocitest.AWSCLI(t, flociPort, "--region", "us-east-1", "s3api", "list-buckets", "--query", "Buckets[].Name", "--output", "text")
	westList := flocitest.AWSCLI(t, flociPort, "--region", "us-west-2", "s3api", "list-buckets", "--query", "Buckets[].Name", "--output", "text")
	t.Logf("us-east-1 sees: %q", eastList)
	t.Logf("us-west-2 sees: %q", westList)
	for _, list := range []string{eastList, westList} {
		for _, name := range []string{"tofu-alias-e2e-east", "tofu-alias-e2e-west"} {
			if !strings.Contains(list, name) {
				t.Errorf("expected %s among the account's buckets, got %q", name, list)
			}
		}
	}

	// --- The claim under test: a real live-plan over both aliases --------
	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("choudoufu live-plan:\n%s", output)

	if err != nil {
		// Issue #69's whole point: this must not happen any more. Before
		// the fix this failed with "Marker discovery across several
		// provider configurations" - live-plan refusing any configuration
		// whose managed resources spanned more than one provider
		// configuration, even though neither resource here needs
		// marker-based discovery at all. A real, unexplained failure here
		// is this test doing its job.
		t.Fatalf("live-plan failed over a two-alias estate (issue #69 regression): %v\n%s", err, output)
	}

	for _, addr := range []string{"aws_s3_bucket.east", "aws_s3_bucket.west"} {
		if strings.Contains(output, "# "+addr+" will be created") {
			t.Errorf("%s is proposed as a create; it already exists and carries this estate's marker, so it should have materialized instead:\n%s", addr, output)
		}
	}

	add, change, destroy, ok := flocitest.PlanSummary(output)
	switch {
	case strings.Contains(output, "No changes."):
		t.Log("the estate planned clean across both aliases: no changes at all")
	case !ok:
		t.Fatalf("could not read a plan summary out of the output")
	default:
		t.Logf("plan summary: %d to add, %d to change, %d to destroy", add, change, destroy)
	}
	if ok && destroy != 0 {
		t.Errorf("the plan proposes %d destroy(s); both buckets are owned and untouched out of band:\n%s", destroy, output)
	}

	// Nothing was written: the estate is recovered from tags every run.
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Errorf("a state file exists after live-plan (err = %v)", err)
	}
}

// copyAliasFixture makes a scratch copy of testdata/alias-e2e's *.tf files
// and its lock file, the same shape flocitest.CopyEstate gives the P0.1
// estate fixture, so that terraform's own artifacts never touch the
// checkout.
func copyAliasFixture(t *testing.T) string {
	t.Helper()

	src := filepath.Join("testdata", "alias-e2e")
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("reading the alias fixture: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".tf") && e.Name() != ".terraform.lock.hcl") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name())) //nolint:gosec // a fixed path in the checkout
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", e.Name(), err)
		}
	}
	return dst
}
