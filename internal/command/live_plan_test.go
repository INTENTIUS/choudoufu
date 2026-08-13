// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
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
	if !strings.Contains(stderr, "Logical resources are not available under live resource markers") {
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
func TestLivePlan_identityFatal(t *testing.T) {
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
	if !strings.Contains(stderr, "Identity argument not set") {
		t.Errorf("no identity diagnostic:\n%s", stderr)
	}
	if strings.Contains(output.Stdout(), "will be created") {
		t.Errorf("an unresolvable identity produced a plan anyway:\n%s", output.Stdout())
	}
	if len(cloud.imports) > 0 {
		t.Errorf("a configuration with an unresolvable identity still read from the live system: %v", cloud.imports)
	}
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
		{[]string{"-json"}, "Machine-readable output is not available under live resource markers yet"},
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

// TestLivePlan_sweepGapsAreReported: a type the sweep could not
// enumerate is named, because "nothing undeclared was found" is only
// meaningful beside the list of types that were searched.
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

	if !strings.Contains(stdout, "Not swept for removal") {
		t.Errorf("the plan does not report the types the sweep could not cover:\n%s", stdout)
	}
	// The mock provider lists three types and no more, so every other
	// admitted type is a gap - reported as one group rather than as a
	// paragraph each.
	if !strings.Contains(stdout, "TYPE_NOT_LISTABLE") {
		t.Errorf("the unlistable types are not named:\n%s", stdout)
	}
	if strings.Contains(stdout, "Owned and undeclared") {
		t.Errorf("a removal was reported with nothing undeclared:\n%s", stdout)
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

// TestLivePlan_needsDiscoveryAcrossProvidersIsRefused pins the rule issue
// #69 leaves unchanged on purpose: an estate whose *needs-discovery*
// resolutions (not merely its managed resources generally) span more than
// one provider configuration is still refused, because a list against the
// wrong account or region for a type actually waiting on marker discovery
// would misreport an estate as missing rather than merely unreachable -
// see statelessNeedsDiscoveryProvider's own doc comment. Only the
// estate-wide sweep gained provider-awareness; this hazard is unrelated to
// it and is not something issue #69 touches.
func TestLivePlan_needsDiscoveryAcrossProvidersIsRefused(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-plan-multi-provider-needs-discovery"), td)
	t.Chdir(td)

	cloud := newStatelessTestCloud()
	cloud.allowRegion("us-west-2")

	c, done := newLivePlanCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=multi-provider-unit"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1 - needs-discovery across providers must still be refused\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Marker discovery across several provider configurations") {
		t.Errorf("the needs-discovery refusal did not fire:\n%s", stderr)
	}
	// The message names the provider configurations in conflict, not the
	// resource addresses waiting on them - it names the default provider by
	// its bare provider[...] address and the aliased one with ".west"
	// appended.
	if !strings.Contains(stderr, `provider["registry.opentofu.org/hashicorp/aws"]`) || !strings.Contains(stderr, ".west") {
		t.Errorf("the refusal does not name both provider configurations:\n%s", stderr)
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
					addrs.NewDefaultProvider("aws"): providers.FactoryFixed(cloud.provider()),
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

	// allowedRegions is what the mock provider's ConfigureProviderFn insists
	// a provider block's region argument be, one of. Every test but the
	// multi-provider ones only ever configures one provider - see
	// allowRegion - and us-east-1 is the region every existing fixture's
	// provider block already names, so a fresh cloud accepting only it
	// keeps every one of those tests' own check ("the command evaluated the
	// provider block") exactly as strict as it always was.
	allowedRegions map[string]bool
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
	}
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
	return map[string]providers.Schema{
		"aws_s3_bucket": schema("id", "bucket", "arn"),
		"aws_vpc":       schema("id", "cidr_block"),
		// The keyed type: a for_each member whose key lives in its
		// tofu-address marker and nowhere else.
		"aws_subnet": schema("id", "cidr_block", "availability_zone"),
		// The fungible type. Nothing in its schema distinguishes one
		// instance from another, which is the whole reason a count set needs
		// slot markers.
		"aws_eip": schema("id", "domain"),
	}
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
}

func (p *statelessTestProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	schema := statelessTestSchemas()[req.TypeName]
	for _, o := range p.cloud.listed[req.TypeName] {
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
		}
		return resp
	}

	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
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
		c.imports = append(c.imports, key)
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
			// A destroy.
			return resp
		}
		tags := statelessTestTagsOf(req.PlannedState)
		key := tags["tofu-address"]
		if key == "" {
			key = req.TypeName
		}
		c.applied[key] = tags
		return resp
	}

	return &statelessTestProvider{MockProvider: p, cloud: c}
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
