// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
// Issue #283 is the second half. Until it, the fixture deliberately held
// only CLIENT-NAMED resources, because live-plan still refused outright when
// the resources WAITING ON marker discovery spanned more than one provider
// configuration - so the one thing a multi-region estate most needs to do,
// find its own server-assigned objects, was exactly what could not be
// tested. The fixture now carries an aws_vpc on each side as well, and this
// test asserts each one's RENDERED IMPORT IDENTITY against the vpc- id the
// AWS CLI reports for that resource's own region. Nothing weaker would do:
// a clean plan is reachable by binding both VPCs to each other's objects,
// and the identity strings are the only thing that separates the two.
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

	// --- Each region holds exactly its own VPC ---------------------------
	//
	// ec2 DescribeVpcs is region-scoped, unlike s3api list-buckets above, so
	// this is where the fixture's two regions are actually two regions. Both
	// ids are read here, from the emulator directly, and are what the
	// rendered identities below have to match.
	eastVPC := soleTaggedVPC(t, flociPort, "us-east-1", "aws_vpc.east")
	westVPC := soleTaggedVPC(t, flociPort, "us-west-2", "aws_vpc.west")
	t.Logf("us-east-1 holds %s for aws_vpc.east; us-west-2 holds %s for aws_vpc.west", eastVPC, westVPC)
	if eastVPC == westVPC {
		t.Fatalf("both regions reported the same VPC id %q, so this run cannot tell one provider configuration's objects from the other's", eastVPC)
	}
	// Neither region may hold the other's. If floci served DescribeVpcs
	// region-blind, every identity assertion below would pass whichever
	// object each pass happened to bind, and this test would prove nothing.
	if other := taggedVPCs(t, flociPort, "us-east-1", "aws_vpc.west"); other != "" {
		t.Fatalf("us-east-1 also lists aws_vpc.west's object (%s), so DescribeVpcs is not region-scoped here and the identity assertions below cannot distinguish the two configurations", other)
	}
	if other := taggedVPCs(t, flociPort, "us-west-2", "aws_vpc.east"); other != "" {
		t.Fatalf("us-west-2 also lists aws_vpc.east's object (%s), so DescribeVpcs is not region-scoped here and the identity assertions below cannot distinguish the two configurations", other)
	}

	// --- The claim under test: a real live-plan over both aliases --------
	//
	// TF_LOG=trace so the projection's own "materialized <addr> from import
	// identity <id>" line is in the output: the rendered identity is the
	// thing under test, and a plan summary cannot carry it.
	cmd := exec.Command(tofuBin, "live-plan", "-no-color", "-input=false") //nolint:gosec // paths are this test's own temp dirs
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TF_LOG=trace")
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

	for _, addr := range []string{"aws_s3_bucket.east", "aws_s3_bucket.west", "aws_vpc.east", "aws_vpc.west"} {
		if strings.Contains(output, "# "+addr+" will be created") {
			t.Errorf("%s is proposed as a create; it already exists and carries this estate's marker, so it should have materialized instead:\n%s", addr, output)
		}
	}

	// --- Issue #283: each VPC bound through its OWN configuration --------
	//
	// The identity, not the verdict. Both VPCs carry this estate's markers
	// and nothing in either configuration says which vpc- id either one is,
	// so the only way an address gets an identity at all is a marker list -
	// and the only way it gets the RIGHT one is a list issued in the region
	// that resource's own provider configuration names. Swapping the two
	// leaves the plan just as empty and every count just as it was.
	for _, want := range []struct{ addr, id string }{
		{"aws_vpc.east", eastVPC},
		{"aws_vpc.west", westVPC},
	} {
		got := materializedIdentity(output, want.addr)
		switch {
		case got == "":
			t.Errorf("%s materialized from no import identity at all; discovery never bound it. Its own provider configuration's region holds %s.\n%s", want.addr, want.id, output)
		case got != want.id:
			t.Errorf("%s materialized from import identity %q, but the only object its own provider configuration's region holds is %q. A pass bound an object through the wrong configuration, which is a wrong marker rather than a missing one.", want.addr, got, want.id)
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

// materializedIdentityLine matches the projection's own trace line for a
// resolution it materialized from a live object, which is the only place a
// rendered import identity appears in output at all - a clean plan says
// "No changes." and names nothing. Same line the corpus crossings under
// live/e2e read (`grep 'from import identity'`).
var materializedIdentityLine = regexp.MustCompile(`materialized ([^ ]+) from import identity "([^"]*)"`)

// materializedIdentity is the import identity a run rendered for one
// address, or "" when the run rendered none.
func materializedIdentity(output, addr string) string {
	for _, m := range materializedIdentityLine.FindAllStringSubmatch(output, -1) {
		if m[1] == addr {
			return m[2]
		}
	}
	return ""
}

// taggedVPCs is every VPC id one region holds carrying the given
// tofu-address marker, space-separated, read from the emulator through the
// AWS CLI with no choudoufu code in the path. Empty means that region holds
// none.
func taggedVPCs(t *testing.T, flociPort string, region, marker string) string {
	t.Helper()
	return strings.TrimSpace(flocitest.AWSCLI(t, flociPort,
		"--region", region, "ec2", "describe-vpcs",
		"--filters", "Name=tag:tofu-address,Values="+marker,
		"--query", "Vpcs[].VpcId", "--output", "text"))
}

// soleTaggedVPC is [taggedVPCs] where exactly one is expected: the object
// the resource at that marker's address was applied as. More than one, or
// none, means the stock apply did not produce the estate this test's later
// assertions read against, and there is nothing to compare identities to.
func soleTaggedVPC(t *testing.T, flociPort string, region, marker string) string {
	t.Helper()
	ids := strings.Fields(taggedVPCs(t, flociPort, region, marker))
	if len(ids) != 1 {
		t.Fatalf("%s holds %d VPC(s) marked %s, want exactly 1: %v", region, len(ids), marker, ids)
	}
	return ids[0]
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
