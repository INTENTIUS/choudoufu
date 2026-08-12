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
		objects: make(map[string]map[string]string),
		tags:    make(map[string]map[string]string),
		listed:  make(map[string][]statelessTestListed),
		applied: make(map[string]map[string]string),
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
		// check that it did.
		region := req.Config.GetAttr("region")
		if region.IsNull() || region.AsString() != "us-east-1" {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("provider was configured with region %#v", region))
		}
		return resp
	}

	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
		key := req.TypeName + "/" + req.Target.ID
		c.imports = append(c.imports, key)
		schema := statelessTestSchemas()[req.TypeName]
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: req.TypeName,
			State:    statelessTestObject(schema, map[string]string{"id": req.Target.ID}),
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
