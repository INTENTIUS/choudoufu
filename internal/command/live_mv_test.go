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
	"strings"
	"testing"

	"github.com/mitchellh/cli"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The live-mv tests drive the whole rename through a mock AWS provider
// standing in for a cloud, over both identity paths:
//
//   - aws_security_group, which the mock can list, is the server-assigned
//     path: the live resource is found by enumerating the type and reading
//     the ownership markers off the objects.
//   - aws_s3_bucket, which the mock cannot list, is the client-named path:
//     the identity comes from configuration, so the resource is materialized
//     from it and its marker is read off the object that comes back.
//
// The write is the same in both: one tags-only apply through the provider.

// TestLiveMv_renamesByMarker is the list path end to end - the marker on
// the live security group is rewritten, and a second run proves it by finding
// nothing at the old address.
func TestLiveMv_renamesByMarker(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	// The write landed on the live resource, and on nothing else.
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.renamed" {
		t.Errorf("the live security group carries tofu-address = %q, want aws_security_group.renamed", got)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-estate"]; got != "stateless-unit" {
		t.Errorf("the estate marker was lost: tofu-estate = %q", got)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["Name"]; got != "keep-me" {
		t.Errorf("a tag that is not a marker was disturbed: Name = %q", got)
	}
	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-address"]; got != "aws_s3_bucket.data" {
		t.Errorf("another resource's marker was rewritten: %q", got)
	}
	if len(cloud.applied) != 1 || cloud.applied[0] != "aws_security_group/sg-owned" {
		t.Errorf("expected exactly one apply against the renamed resource, got %v", cloud.applied)
	}

	// The report says what was written, and says it was a cloud write.
	report := output.Stdout()
	for _, want := range []string{
		"This was a cloud write.",
		"aws_security_group.main",
		"aws_security_group.renamed",
		"sg-owned",
		`"aws_security_group.main" -> "aws_security_group.renamed"`,
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}

	// The follow-up read: the same rename now finds nothing at the old
	// address, because a tag value cannot hold two addresses at once.
	c2, done2 := newLiveMvCommand(t, cloud)
	code2 := c2.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
	second := done2(t)
	if code2 != 1 {
		t.Fatalf("the second rename exited %d, want 1\nstdout:\n%s", code2, second.Stdout())
	}
	if !strings.Contains(second.Stderr(), "No live resource at the old address") {
		t.Errorf("the second run does not report the old address as gone:\n%s", second.Stderr())
	}
	if !strings.Contains(second.Stderr(), "already run") {
		t.Errorf("the second run does not recognize a rename that already happened:\n%s", second.Stderr())
	}
}

// TestLiveMv_renamesByIdentity is the client-named path: the provider
// cannot list aws_s3_bucket, so the bucket is read through the identity its
// configuration names and its marker is rewritten just the same.
func TestLiveMv_renamesByIdentity(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-address"]; got != "aws_s3_bucket.archive" {
		t.Errorf("the live bucket carries tofu-address = %q, want aws_s3_bucket.archive", got)
	}
	if len(cloud.applied) != 1 || cloud.applied[0] != "aws_s3_bucket/tofu-mv-unit-data" {
		t.Errorf("expected exactly one apply against the bucket, got %v", cloud.applied)
	}
	if report := output.Stdout(); !strings.Contains(report, "whose identity this configuration names") {
		t.Errorf("the report does not say which path found the resource:\n%s", report)
	}
}

// TestLiveMv_dryRunWritesNothing: every read happens, every check runs,
// and no plan or apply call is made.
func TestLiveMv_dryRunWritesNothing(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-dry-run", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.main" {
		t.Errorf("-dry-run rewrote the marker anyway: %q", got)
	}
	if len(cloud.planned) != 0 || len(cloud.applied) != 0 {
		t.Errorf("-dry-run called the provider's write path: planned=%v applied=%v", cloud.planned, cloud.applied)
	}

	report := output.Stdout()
	if !strings.Contains(report, "Nothing was written (-dry-run)") {
		t.Errorf("the report does not say nothing was written:\n%s", report)
	}
	if !strings.Contains(report, "sg-owned") {
		t.Errorf("the dry run does not name the resource it found:\n%s", report)
	}
	if strings.Contains(report, "This was a cloud write.") {
		t.Errorf("a dry run claimed to have written:\n%s", report)
	}
}

// TestLiveMv_notFound: nothing carries the old address, and the message
// says so as a fact about the live system rather than about the search.
func TestLiveMv_notFound(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_security_group.absent", "aws_security_group.renamed"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "No live resource at the old address") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "the type was enumerated") {
		t.Errorf("the message does not distinguish absence from an unsearched type:\n%s", stderr)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a failed lookup still wrote: %v", cloud.applied)
	}
}

// TestLiveMv_typeNotListable: a server-assigned identity the provider
// cannot list is unfindable, and that is a different answer from absence.
func TestLiveMv_typeNotListable(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	// -allow-missing-config so that the run reaches the search rather than
	// stopping on the destination block this fixture does not declare.
	code := c.Run([]string{"-no-color", "-allow-missing-config", "aws_vpc.main", "aws_vpc.core"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "No marker search path for this resource type") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "not a report that no such resource exists") {
		t.Errorf("the message does not separate 'nothing looked' from 'nothing exists':\n%s", stderr)
	}
}

// TestLiveMv_ambiguity: two live resources carrying one address is the
// collision the marker spec names, and both live IDs are in the message.
func TestLiveMv_ambiguity(t *testing.T) {
	cloud := mvRenamedFixture(t)
	cloud.put("aws_security_group", "sg-twin", map[string]string{"id": "sg-twin", "name": "twin"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.main"})

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Two live resources claiming one address") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	for _, id := range []string{"sg-owned", "sg-twin"} {
		if !strings.Contains(stderr, id) {
			t.Errorf("the collision does not name %s:\n%s", id, stderr)
		}
	}
	if len(cloud.applied) != 0 {
		t.Errorf("an ambiguous lookup still wrote: %v", cloud.applied)
	}
}

// TestLiveMv_destinationClaimed: rewriting onto an address something else
// already carries would manufacture a collision, so it is refused.
func TestLiveMv_destinationClaimed(t *testing.T) {
	cloud := mvRenamedFixture(t)
	cloud.put("aws_security_group", "sg-squatter", map[string]string{"id": "sg-squatter", "name": "squatter"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.renamed"})

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Destination address already claimed") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "sg-squatter") {
		t.Errorf("the refusal does not name the resource holding the destination:\n%s", stderr)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.main" {
		t.Errorf("the source was rewritten despite the refusal: %q", got)
	}
}

// TestLiveMv_crossType: a rename points one live resource at a new
// address; it does not turn one kind of cloud object into another.
func TestLiveMv_crossType(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_vpc.main"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "Mismatched resource types in a rename") {
		t.Errorf("wrong diagnostic:\n%s", output.Stderr())
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a cross-type rename still wrote: %v", cloud.applied)
	}
}

// TestLiveMv_crossEstate: a live resource carrying another estate's tag
// is not this estate's to rename. Moving it across the ownership boundary
// would be a transfer, which is adoption's business rather than a rename's.
func TestLiveMv_crossEstate(t *testing.T) {
	cloud := mvRenamedFixture(t)
	cloud.tags["aws_s3_bucket/tofu-mv-unit-data"]["tofu-estate"] = "somebody-else"

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Live resource owned by another estate") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "somebody-else") {
		t.Errorf("the refusal does not name the estate that owns it:\n%s", stderr)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a cross-estate rename still wrote: %v", cloud.applied)
	}
}

// TestLiveMv_missingConfigRefused: the destination address has to exist
// in configuration, because a marker naming an address nothing declares is an
// orphan. The refusal names the flag that overrides it.
func TestLiveMv_missingConfigRefused(t *testing.T) {
	cloud := mvUnrenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Destination address missing from the configuration") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "-allow-missing-config") {
		t.Errorf("the refusal does not name the flag that overrides it:\n%s", stderr)
	}
	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-address"]; got != "aws_s3_bucket.data" {
		t.Errorf("the marker was rewritten despite the refusal: %q", got)
	}
}

// TestLiveMv_missingConfigOverride: with the flag, the rename runs
// through the old address's still-present resource block, and warns that the
// configuration has not caught up.
func TestLiveMv_missingConfigOverride(t *testing.T) {
	cloud := mvUnrenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-allow-missing-config", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-address"]; got != "aws_s3_bucket.archive" {
		t.Errorf("the marker was not rewritten: %q", got)
	}
	// A warning, so the standard view sends it to stdout.
	if !strings.Contains(output.Stdout(), "Configuration still naming the old address") {
		t.Errorf("no warning about the configuration lagging the marker:\n%s", output.Stdout())
	}
	if !strings.Contains(output.Stdout(), "This was a cloud write.") {
		t.Errorf("the report does not say the write happened:\n%s", output.Stdout())
	}
}

// TestLiveMv_lintFatal mirrors TestLivePlan_lintFatal: a configuration
// outside the stateless subset is refused before the provider is started or
// anything is read from the live system, which is the property this issue
// (#50) threads schemas into lint to preserve.
func TestLiveMv_lintFatal(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-lint"), td)
	t.Chdir(td)

	cloud := mvNewCloud()
	c, done := newLiveMvCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-estate=stateless-unit", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
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
	if len(cloud.planned) > 0 || len(cloud.applied) > 0 {
		t.Errorf("a rejected configuration still wrote to the live system: planned=%v applied=%v", cloud.planned, cloud.applied)
	}
}

// TestLiveMv_needsAnEstateName: unlike a plan, a rename cannot degrade
// when nothing names the estate - it would have nothing to search for.
func TestLiveMv_needsAnEstateName(t *testing.T) {
	cloud := mvNewCloud()
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-no-estate"), td)
	t.Chdir(td)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "No estate name") {
		t.Errorf("wrong diagnostic:\n%s", output.Stderr())
	}
	if !strings.Contains(output.Stderr(), "-estate=<name>") {
		t.Errorf("the error does not name the flag that fixes it:\n%s", output.Stderr())
	}
}

// TestLiveMv_estateFromTheLiveBlock: a live-block
// configuration stamps no tofu-estate tag of its own, because stamping
// happens to the configuration a plan reads rather than to the file on disk.
// The rename command a plan prints beside a renamed for_each key therefore
// has nothing in the file to derive the estate from, and would not run at all
// unless the block itself is consulted. It is, and it is.
func TestLiveMv_estateFromTheLiveBlock(t *testing.T) {
	cloud := mvNewCloud()
	cloud.listable("aws_security_group")
	cloud.put("aws_security_group", "sg-1",
		map[string]string{"id": "sg-1", "name": "tofu-mv-unit"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.main"})

	td := t.TempDir()
	body := `terraform {
  live {
    estate = "stateless-unit"
  }

  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_security_group" "renamed" {
  name = "tofu-mv-unit"
}
`
	if err := os.WriteFile(filepath.Join(td, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(td)

	c, done := newLiveMvCommand(t, cloud)
	// No -estate: the block is the only thing that names it.
	code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	if got := cloud.tagsOf("aws_security_group", "sg-1")["tofu-address"]; got != "aws_security_group.renamed" {
		t.Errorf("the live security group carries tofu-address = %q, want aws_security_group.renamed", got)
	}
}

// TestLiveMv_badArguments covers the argument surface: two addresses are
// required, and both have to parse.
//
// The exit code splits the two kinds of bad command line, the way every other
// command in this package splits them. A command line the flag parser could
// not make sense of returns cli.RunResultHelp, which prints the usage text
// after the diagnostic; anything the parser accepted and a later check
// rejected - an address that will not parse, two addresses that name the same
// resource - is an ordinary failure, and printing the usage text at it would
// be answering a question nobody asked.
func TestLiveMv_badArguments(t *testing.T) {
	for name, tc := range map[string]struct {
		args     []string
		want     string
		wantCode int
	}{
		"no addresses":  {[]string{}, "Two resource addresses are required", cli.RunResultHelp},
		"one address":   {[]string{"aws_vpc.main"}, "Two resource addresses are required", cli.RunResultHelp},
		"three":         {[]string{"a.b", "c.d", "e.f"}, "Two resource addresses are required", cli.RunResultHelp},
		"unknown flag":  {[]string{"-nope", "aws_vpc.main", "aws_vpc.other"}, "Invalid option", cli.RunResultHelp},
		"unparseable":   {[]string{"aws_vpc", "aws_vpc.main"}, "Invalid address", 1},
		"same address":  {[]string{"aws_vpc.main", "aws_vpc.main"}, "Identical source and destination addresses", 1},
		"data resource": {[]string{"data.aws_vpc.a", "data.aws_vpc.b"}, "Unsupported address for a rename", 1},
	} {
		t.Run(name, func(t *testing.T) {
			cloud := mvRenamedFixture(t)
			c, done := newLiveMvCommand(t, cloud)

			code := c.Run(append([]string{"-no-color"}, tc.args...))
			output := done(t)
			if code != tc.wantCode {
				t.Fatalf("exit code %d, want %d\nstdout:\n%s", code, tc.wantCode, output.Stdout())
			}
			if !strings.Contains(output.Stderr(), tc.want) {
				t.Errorf("wrong diagnostic:\n%s", output.Stderr())
			}
		})
	}
}

// TestLiveMv_readParallelism is the behavioural half of GitHub issue #640:
// live-mv's read pass now honours TOFU_LIVE_READ_PARALLELISM, which until this
// change it ignored - internal/live/mv's projection.Options was the one
// construction in the tree issue #626 left unwired, so a rename read at
// projection.DefaultReadParallelism however far down an operator had turned the
// variable.
//
// What a mock cloud can and cannot show here is worth stating, because it is
// why the assertions are the ones they are. The WIDTH of the pass is not
// observable through this double: it changes no output, no diagnostic and no
// call count, and live-plan's own equivalent has to instrument the provider
// with a parking hook to see any difference at all
// (TestLivePlan_readParallelismBoundsTheReadPass). Two things are observable,
// and they are the two that matter for a command that WRITES:
//
//   - a setting the run cannot honour stops it, before the cloud is touched at
//     all, rather than being silently replaced with ten;
//   - the sequential setting still produces the same rename, so honouring the
//     variable did not degrade the operation an operator reaches for during a
//     migration.
//
// The structural half - that the value reaches the projection from the run
// rather than from a constant, on this site and on every other one in the tree
// - is TestReadParallelismReachesEveryProjectionOptions and
// TestEveryProjectionOptionsInTheTreeIsWiredOrExcluded.
func TestLiveMv_readParallelism(t *testing.T) {
	t.Run("sequential", func(t *testing.T) {
		t.Setenv(readParallelismEnvVar, "1")
		cloud := mvRenamedFixture(t)

		c, done := newLiveMvCommand(t, cloud)
		code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
		output := done(t)
		if code != 0 {
			t.Fatalf("exit code %d, want 0 - %s=1 must reproduce the sequential read pass, not break the rename\nstdout:\n%s\nstderr:\n%s", code, readParallelismEnvVar, output.Stdout(), output.Stderr())
		}
		if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.renamed" {
			t.Errorf("the live security group carries tofu-address = %q, want aws_security_group.renamed - the same answer the default produces", got)
		}
		if len(cloud.applied) != 1 || cloud.applied[0] != "aws_security_group/sg-owned" {
			t.Errorf("expected exactly one apply against the renamed resource, got %v", cloud.applied)
		}
	})

	t.Run("refused", func(t *testing.T) {
		t.Setenv(readParallelismEnvVar, "0")
		cloud := mvRenamedFixture(t)

		c, done := newLiveMvCommand(t, cloud)
		code := c.Run([]string{"-no-color", "aws_security_group.main", "aws_security_group.renamed"})
		output := done(t)
		if code != 1 {
			t.Fatalf("%s=0 exited %d, want 1; a non-positive bound must be refused, never read as \"no limit\"\nstdout:\n%s\nstderr:\n%s", readParallelismEnvVar, code, output.Stdout(), output.Stderr())
		}
		if got := output.Stderr(); !strings.Contains(got, "The parallelism must be a positive value. Not 0.") {
			t.Errorf("the run failed, but not with stock's refusal:\n%s", got)
		}
		// The refusal lands before the cloud is touched. This is the reason
		// liveMv resolves the setting at the top of the function rather than
		// beside the mv.Request it fills in: a rename that had already read
		// the estate before deciding it could not honour its own bound would
		// have spent exactly the calls the operator turned the bound down to
		// avoid.
		if len(cloud.imports) != 0 {
			t.Errorf("a refused setting still read %v from the cloud; the refusal has to land before anything is read", cloud.imports)
		}
		if len(cloud.planned) != 0 || len(cloud.applied) != 0 {
			t.Errorf("a refused setting still planned %v and applied %v", cloud.planned, cloud.applied)
		}
		if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.main" {
			t.Errorf("a refused setting still rewrote the marker: tofu-address = %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// -json (GitHub issue #791)
// ---------------------------------------------------------------------------

// TestLiveMv_jsonRenamesByMarker is TestLiveMv_renamesByMarker's report
// read back as the document -json prints instead of the labelled rows: one
// completed move, with the resource, both endpoints, and the two proofs
// (Written, Verified) a receipt reader needs.
func TestLiveMv_jsonRenamesByMarker(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-json", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	rep := decodeMvJSON(t, output.Stdout())
	if rep.Resource.TypeName != "aws_security_group" || rep.Resource.LiveID != "sg-owned" {
		t.Errorf("resource = %+v, want aws_security_group/sg-owned", rep.Resource)
	}
	if rep.From.Estate != "stateless-unit" || rep.From.Address != "aws_security_group.main" || rep.From.Marker != "aws_security_group.main" {
		t.Errorf("from = %+v", rep.From)
	}
	if rep.To.Estate != "stateless-unit" || rep.To.Address != "aws_security_group.renamed" || rep.To.Marker != "aws_security_group.renamed" {
		t.Errorf("to = %+v", rep.To)
	}
	if rep.DryRun {
		t.Error("dry_run is true for a real write")
	}
	if !rep.Written {
		t.Error("written is false after a completed apply")
	}
	if !rep.Verified {
		t.Error("verified is false even though the mock provider serves tags back on the read")
	}
	if rep.FoundBy != "LIST" {
		t.Errorf("found_by = %q, want LIST", rep.FoundBy)
	}
	if len(rep.Followers) != 0 {
		t.Errorf("followers = %v, want none - this fixture declares no parent-derived children", rep.Followers)
	}
	if rep.Refusal != nil {
		t.Errorf("refusal = %+v, want nil on a completed move", rep.Refusal)
	}
	if rep.RequestID != "" {
		t.Errorf("request_id = %q, want empty - no plugin-protocol plumbing carries one yet", rep.RequestID)
	}

	// The write actually happened; -json is a different rendering of the
	// same run, not a different code path that skips the cloud.
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.renamed" {
		t.Errorf("the live security group carries tofu-address = %q, want aws_security_group.renamed", got)
	}

	// -json's document is the only thing on stdout: a machine reading it
	// does not have to skip past the human report's prose first.
	if strings.Contains(output.Stdout(), "This was a cloud write.") {
		t.Errorf("the human report's prose leaked into -json's stdout:\n%s", output.Stdout())
	}
}

// TestLiveMv_jsonDryRun proves -dry-run and -json compose: every field
// -json reports on a real write is still reported on a rehearsal, with
// DryRun true and Written/Verified false, which is what lets a preview
// (the workbench's own use case, issue #791's "Why") read the same shape a
// receipt does.
func TestLiveMv_jsonDryRun(t *testing.T) {
	cloud := mvRenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-json", "-dry-run", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	rep := decodeMvJSON(t, output.Stdout())
	if !rep.DryRun {
		t.Error("dry_run is false under -dry-run")
	}
	if rep.Written {
		t.Error("written is true under -dry-run")
	}
	if rep.Resource.LiveID != "sg-owned" {
		t.Errorf("the dry run still has to name what it found: resource = %+v", rep.Resource)
	}
	if rep.To.Marker != "aws_security_group.renamed" {
		t.Errorf("the dry run does not preview the marker it would write: to = %+v", rep.To)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.main" {
		t.Errorf("-json -dry-run rewrote the marker anyway: %q", got)
	}
}

// TestLiveMv_jsonRefusals is the refusal half: for every one of
// mv.RefusalCode's five named shapes this test can drive through the
// command layer, -json still prints one document - Refusal set, Written
// false - rather than leaving a caller with nothing but stderr prose to
// parse. Each case reuses the fixture and the exact scenario an existing
// human-report test above already covers, so the assertions here are only
// about what -json adds: the stable code and the fact that a document was
// printed at all.
func TestLiveMv_jsonRefusals(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) *mvCloud
		args     []string
		wantCode string
	}{
		{
			name:     "nothing at the old address",
			setup:    mvRenamedFixture,
			args:     []string{"aws_security_group.absent", "aws_security_group.renamed"},
			wantCode: "nothing_at_old_address",
		},
		{
			name: "two resources claiming the old address",
			setup: func(t *testing.T) *mvCloud {
				cloud := mvRenamedFixture(t)
				cloud.put("aws_security_group", "sg-twin", map[string]string{"id": "sg-twin", "name": "twin"},
					map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.main"})
				return cloud
			},
			args:     []string{"aws_security_group.main", "aws_security_group.renamed"},
			wantCode: "two_at_old_address",
		},
		{
			name: "the destination is already claimed",
			setup: func(t *testing.T) *mvCloud {
				cloud := mvRenamedFixture(t)
				cloud.put("aws_security_group", "sg-squatter", map[string]string{"id": "sg-squatter", "name": "squatter"},
					map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.renamed"})
				return cloud
			},
			args:     []string{"aws_security_group.main", "aws_security_group.renamed"},
			wantCode: "new_address_claimed",
		},
		{
			name:     "the destination is not declared",
			setup:    mvUnrenamedFixture,
			args:     []string{"aws_s3_bucket.data", "aws_s3_bucket.archive"},
			wantCode: "destination_not_declared",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cloud := tc.setup(t)
			c, done := newLiveMvCommand(t, cloud)
			code := c.Run(append([]string{"-no-color", "-json"}, tc.args...))
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}

			rep := decodeMvJSON(t, output.Stdout())
			if rep.Refusal == nil {
				t.Fatalf("refusal is nil on a refused move")
			}
			if rep.Refusal.Code != tc.wantCode {
				t.Errorf("refusal.code = %q, want %q (refusal = %+v)", rep.Refusal.Code, tc.wantCode, rep.Refusal)
			}
			if rep.Refusal.Summary == "" || rep.Refusal.Detail == "" {
				t.Errorf("refusal is missing its text: %+v", rep.Refusal)
			}
			// The same text a human run would have seen on stderr, not a
			// rephrasing invented for JSON - one diagnostic, two renderings.
			if !strings.Contains(output.Stderr(), rep.Refusal.Summary) {
				t.Errorf("refusal.summary %q does not appear in stderr:\n%s", rep.Refusal.Summary, output.Stderr())
			}
			if rep.Written {
				t.Error("written is true on a refused move")
			}
		})
	}
}

// TestLiveMv_jsonRefusalOutsideTheFiveCodes proves the fallback: a refusal
// this package never gives a RefusalCode (TestLiveMv_lintFatal's own
// scenario, raised in internal/command before mv.Move is even reached) still
// gets a document, with Refusal set and an empty Code rather than a made-up
// sixth value - and Resource, understandably, is the JSON zero value, since
// nothing was ever found.
func TestLiveMv_jsonRefusalOutsideTheFiveCodes(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-lint"), td)
	t.Chdir(td)

	cloud := mvNewCloud()
	c, done := newLiveMvCommand(t, cloud)

	code := c.Run([]string{"-no-color", "-json", "-estate=stateless-unit", "aws_s3_bucket.data", "aws_s3_bucket.archive"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	rep := decodeMvJSON(t, output.Stdout())
	if rep.Refusal == nil {
		t.Fatalf("refusal is nil on a refused move")
	}
	if rep.Refusal.Code != "" {
		t.Errorf("refusal.code = %q, want empty - a lint refusal is outside the five named shapes", rep.Refusal.Code)
	}
	if !strings.Contains(rep.Refusal.Summary, "Logical resource is not admitted") {
		t.Errorf("refusal.summary = %q, want the lint diagnostic's own summary", rep.Refusal.Summary)
	}
	if rep.Resource.TypeName != "" || rep.Resource.LiveID != "" {
		t.Errorf("resource = %+v, want the zero value - lint refused before mv.Move ever ran", rep.Resource)
	}
	if rep.From.Address != "aws_s3_bucket.data" || rep.To.Address != "aws_s3_bucket.archive" {
		t.Errorf("from/to still have to name the addresses this run was given: from=%+v to=%+v", rep.From, rep.To)
	}
}

// decodeMvJSON parses -json's stdout as one views.StatelessMvJSONReport, the
// same struct live_mv.go builds and views.StatelessMvJSONHuman prints -
// decoding into it, rather than into a map, is what makes this test fail to
// compile the day a field is renamed instead of failing at runtime with a
// silently missing key.
func decodeMvJSON(t *testing.T, stdout string) views.StatelessMvJSONReport {
	t.Helper()
	var rep views.StatelessMvJSONReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("-json's stdout does not parse as JSON: %s\nstdout:\n%s", err, stdout)
	}
	return rep
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// mvRenamedFixture is the ordinary case: the configuration has already been
// renamed (aws_s3_bucket.data -> .archive, aws_security_group.main ->
// .renamed) and the live resources still carry the old markers, which is
// exactly the state live-mv exists to resolve.
func mvRenamedFixture(t *testing.T) *mvCloud {
	t.Helper()

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-renamed"), td)
	t.Chdir(td)

	cloud := mvNewCloud()
	cloud.listable("aws_security_group")
	cloud.put("aws_s3_bucket", "tofu-mv-unit-data",
		map[string]string{"id": "tofu-mv-unit-data", "bucket": "tofu-mv-unit-data"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_s3_bucket.data"})
	cloud.put("aws_security_group", "sg-owned",
		map[string]string{"id": "sg-owned", "name": "mv-unit"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_security_group.main", "Name": "keep-me"})
	// Somebody else's, carrying the same address in a different estate: the
	// estate is the ownership boundary, so this must never be picked.
	cloud.put("aws_security_group", "sg-elsewhere",
		map[string]string{"id": "sg-elsewhere", "name": "elsewhere"},
		map[string]string{"tofu-estate": "other-estate", "tofu-address": "aws_security_group.main"})
	return cloud
}

// mvUnrenamedFixture is the other ordering: the marker is rewritten first and
// the configuration still declares the old address.
func mvUnrenamedFixture(t *testing.T) *mvCloud {
	t.Helper()

	td := t.TempDir()
	testCopyDir(t, testFixturePath("live-mv-unrenamed"), td)
	t.Chdir(td)

	cloud := mvNewCloud()
	cloud.put("aws_s3_bucket", "tofu-mv-unit-data",
		map[string]string{"id": "tofu-mv-unit-data", "bucket": "tofu-mv-unit-data"},
		map[string]string{"tofu-estate": "stateless-unit", "tofu-address": "aws_s3_bucket.data"})
	return cloud
}

func newLiveMvCommand(t *testing.T, cloud *mvCloud) (*LiveMvCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()

	view, done := testView(t)
	c := &LiveMvCommand{
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

// ---------------------------------------------------------------------------
// A mock AWS provider with a mutable cloud behind it
// ---------------------------------------------------------------------------

// mvCloud is a map of live objects keyed by type and identity, served through
// a provider that speaks the four calls a rename makes: list, import, read,
// and the plan/apply pair that performs the tag write. Unlike the plan
// tests' cloud, this one is mutable: an apply changes what a later read and a
// later list report, which is what makes "the marker was rewritten" something
// a test can observe rather than assume.
type mvCloud struct {
	order []string
	attrs map[string]map[string]string
	tags  map[string]map[string]string
	lists map[string]bool

	planned []string
	applied []string

	// imports records every ImportResourceState the run made, in order. It
	// is the read pass's own first call, so an empty slice after a run is
	// "this run read nothing from the cloud" - which is what GitHub issue
	// #640's refused-setting case has to be able to say.
	imports []string
}

func mvNewCloud() *mvCloud {
	return &mvCloud{
		attrs: make(map[string]map[string]string),
		tags:  make(map[string]map[string]string),
		lists: make(map[string]bool),
	}
}

func (c *mvCloud) put(typeName, id string, attrs, tags map[string]string) {
	key := typeName + "/" + id
	if _, exists := c.attrs[key]; !exists {
		c.order = append(c.order, key)
	}
	c.attrs[key] = attrs
	c.tags[key] = tags
}

// listable marks a type as one the provider serves a list schema for.
func (c *mvCloud) listable(typeName string) { c.lists[typeName] = true }

func (c *mvCloud) tagsOf(typeName, id string) map[string]string {
	return c.tags[typeName+"/"+id]
}

func (c *mvCloud) keysOf(typeName string) []string {
	var out []string
	for _, key := range c.order {
		if strings.HasPrefix(key, typeName+"/") {
			out = append(out, key)
		}
	}
	return out
}

// mvSchemas is a caricature of the AWS provider: the three resource types the
// fixtures use, with the attributes their bodies set. Every attribute other
// than tags is optional and computed, which is the shape that lets the
// mock's plan be "whatever was proposed".
func mvSchemas() map[string]providers.Schema {
	schema := func(names ...string) providers.Schema {
		attrs := map[string]*configschema.Attribute{
			"tags":     {Type: cty.Map(cty.String), Optional: true},
			"tags_all": {Type: cty.Map(cty.String), Computed: true},
		}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		return providers.Schema{
			Block: &configschema.Block{Attributes: attrs},
			IdentitySchema: &configschema.Object{
				Nesting: configschema.NestingSingle,
				Attributes: map[string]*configschema.Attribute{
					"id":         {Type: cty.String, Required: true},
					"account_id": {Type: cty.String, Optional: true},
				},
			},
		}
	}
	return map[string]providers.Schema{
		"aws_s3_bucket":      schema("id", "bucket", "arn"),
		"aws_security_group": schema("id", "name", "description"),
		"aws_vpc":            schema("id", "cidr_block"),
	}
}

func mvListSchemas(types map[string]bool) map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(types))
	for name := range types {
		out[name] = providers.Schema{Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"region": {Type: cty.String, Optional: true},
			},
		}}
	}
	return out
}

// mvProvider adds the list protocol, which is not part of providers.Interface
// - the stateless list client asks for it by assertion.
type mvProvider struct {
	*tofu.MockProvider
	cloud *mvCloud
}

func (p *mvProvider) ListResourceStream(_ context.Context, req providers.ListResourceRequest, emit func(providers.ListResourceEvent) bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	schema := mvSchemas()[req.TypeName]
	for _, key := range p.cloud.keysOf(req.TypeName) {
		id := strings.TrimPrefix(key, req.TypeName+"/")
		ev := providers.ListResourceEvent{
			DisplayName: p.cloud.attrs[key]["name"],
			Identity: cty.ObjectVal(map[string]cty.Value{
				"id":         cty.StringVal(id),
				"account_id": cty.StringVal("000000000000"),
			}),
		}
		if req.IncludeResourceObject {
			ev.ResourceObject = mvObject(schema, p.cloud.attrs[key], p.cloud.tags[key])
		}
		if !emit(ev) {
			break
		}
	}
	return diags
}

func (c *mvCloud) provider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"region": {Type: cty.String, Optional: true},
				},
			}},
			ResourceTypes:     mvSchemas(),
			ListResourceTypes: mvListSchemas(c.lists),
		},
	}

	p.ConfigureProviderFn = func(req providers.ConfigureProviderRequest) (resp providers.ConfigureProviderResponse) {
		region := req.Config.GetAttr("region")
		if region.IsNull() || region.AsString() != "us-east-1" {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("provider was configured with region %#v", region))
		}
		return resp
	}

	p.ImportResourceStateFn = func(req providers.ImportResourceStateRequest) (resp providers.ImportResourceStateResponse) {
		// A provider serving an identity schema is asked by identity object
		// wherever the run has one, and by the import-ID string otherwise.
		// Every type here is identified by its id, so the two name the same
		// object; the mock has to answer both because the wire carries
		// exactly one of them (providers.ImportTarget).
		id := req.Target.ID
		if req.Target.IsIdentityBased() {
			id = req.Target.Identity.GetAttr("id").AsString()
		}
		c.imports = append(c.imports, req.TypeName+"/"+id)
		schema := mvSchemas()[req.TypeName]
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: req.TypeName,
			State:    mvObject(schema, map[string]string{"id": id}, nil),
		}}
		return resp
	}

	p.ReadResourceFn = func(req providers.ReadResourceRequest) (resp providers.ReadResourceResponse) {
		schema := mvSchemas()[req.TypeName]
		key := req.TypeName + "/" + req.PriorState.GetAttr("id").AsString()
		attrs, ok := c.attrs[key]
		if !ok {
			resp.NewState = cty.NullVal(schema.Block.ImpliedType())
			return resp
		}
		resp.NewState = mvObject(schema, attrs, c.tags[key])
		return resp
	}

	p.PlanResourceChangeFn = func(req providers.PlanResourceChangeRequest) (resp providers.PlanResourceChangeResponse) {
		// A null PriorState is an ordinary synthetic create-shaped plan -
		// internal/live/projection's normalizeIdentityAttrs asks one of
		// every materialized instance with a string identity attribute
		// (issue #281), during the read-only locate phase every mv run
		// does whether or not it ends up writing anything. GetAttr on a
		// null object panics rather than erroring, which every real
		// provider already has to tolerate (a genuine create sends exactly
		// this). It is deliberately left OUT of c.planned: every assertion
		// in this file reads that slice as "the write-preparation call
		// rewrite.go makes", which a read-only self-heal check is not, and
		// counting it here would make a -dry-run test see a "write" that
		// never happened.
		if req.PriorState.IsNull() {
			resp.PlannedState = req.ProposedNewState
			return resp
		}
		c.planned = append(c.planned, req.TypeName+"/"+req.PriorState.GetAttr("id").AsString())
		resp.PlannedState = req.ProposedNewState
		return resp
	}

	p.ApplyResourceChangeFn = func(req providers.ApplyResourceChangeRequest) (resp providers.ApplyResourceChangeResponse) {
		key := req.TypeName + "/" + req.PlannedState.GetAttr("id").AsString()
		c.applied = append(c.applied, key)

		tags := make(map[string]string)
		if v := req.PlannedState.GetAttr("tags"); !v.IsNull() {
			for it := v.ElementIterator(); it.Next(); {
				k, val := it.Element()
				tags[k.AsString()] = val.AsString()
			}
		}
		c.tags[key] = tags
		resp.NewState = req.PlannedState
		return resp
	}

	return &mvProvider{MockProvider: p, cloud: c}
}

func mvObject(schema providers.Schema, attrs, tags map[string]string) cty.Value {
	vals := make(map[string]cty.Value, len(schema.Block.Attributes))
	for name, at := range schema.Block.Attributes {
		if v, ok := attrs[name]; ok && at.Type == cty.String {
			vals[name] = cty.StringVal(v)
			continue
		}
		vals[name] = cty.NullVal(at.Type)
	}
	tagVal := cty.NullVal(cty.Map(cty.String))
	if len(tags) > 0 {
		tagVals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			tagVals[k] = cty.StringVal(v)
		}
		tagVal = cty.MapVal(tagVals)
	}
	if _, ok := schema.Block.Attributes["tags"]; ok {
		vals["tags"] = tagVal
	}
	if _, ok := schema.Block.Attributes["tags_all"]; ok {
		vals["tags_all"] = tagVal
	}
	return cty.ObjectVal(vals)
}

// TestLiveMv_movesAcrossEstates is the split, on the client-named path: the
// live bucket carries another estate's tag, this configuration declares the
// same address, and -from-estate moves it here. The address is unchanged;
// the estate tag is what was written.
func TestLiveMv_movesAcrossEstates(t *testing.T) {
	cloud := mvUnrenamedFixture(t)
	cloud.tags["aws_s3_bucket/tofu-mv-unit-data"]["tofu-estate"] = "monolith"

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-from-estate=monolith", "aws_s3_bucket.data", "aws_s3_bucket.data"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-estate"]; got != "stateless-unit" {
		t.Errorf("the live bucket carries tofu-estate = %q, want stateless-unit", got)
	}
	if got := cloud.tagsOf("aws_s3_bucket", "tofu-mv-unit-data")["tofu-address"]; got != "aws_s3_bucket.data" {
		t.Errorf("the address moved too: tofu-address = %q", got)
	}
	if len(cloud.applied) != 1 || cloud.applied[0] != "aws_s3_bucket/tofu-mv-unit-data" {
		t.Errorf("expected exactly one apply against the moved resource, got %v", cloud.applied)
	}

	report := output.Stdout()
	for _, want := range []string{
		"Moved one live resource into this estate. This was a cloud write.",
		`"monolith" -> "stateless-unit"`,
		"tofu-mv-unit-data",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not mention %q:\n%s", want, report)
		}
	}

	// Run again: the resource now carries this estate, so the source has
	// nothing at the old address and the message says the move already ran.
	c2, done2 := newLiveMvCommand(t, cloud)
	code2 := c2.Run([]string{"-no-color", "-from-estate=monolith", "aws_s3_bucket.data", "aws_s3_bucket.data"})
	second := done2(t)
	if code2 != 1 {
		t.Fatalf("the second move exited %d, want 1\nstdout:\n%s", code2, second.Stdout())
	}
	if !strings.Contains(second.Stderr(), "already run") {
		t.Errorf("the second run does not recognize a move that already happened:\n%s", second.Stderr())
	}
	if len(cloud.applied) != 1 {
		t.Errorf("the second run wrote again: %v", cloud.applied)
	}
}

// TestLiveMv_movesAcrossEstatesByList is the same move on the list path,
// combined with a rename: the security group is found by sweeping the
// SOURCE estate for the old address, the destination address is checked
// free in the DESTINATION estate, and a same-address neighbour in a third
// estate is never picked.
func TestLiveMv_movesAcrossEstatesByList(t *testing.T) {
	cloud := mvRenamedFixture(t)
	cloud.tags["aws_security_group/sg-owned"]["tofu-estate"] = "monolith"

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-from-estate=monolith", "aws_security_group.main", "aws_security_group.renamed"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}

	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-estate"]; got != "stateless-unit" {
		t.Errorf("the live security group carries tofu-estate = %q, want stateless-unit", got)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["tofu-address"]; got != "aws_security_group.renamed" {
		t.Errorf("the live security group carries tofu-address = %q, want aws_security_group.renamed", got)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-owned")["Name"]; got != "keep-me" {
		t.Errorf("a tag that is not a marker was disturbed: Name = %q", got)
	}
	if got := cloud.tagsOf("aws_security_group", "sg-elsewhere")["tofu-estate"]; got != "other-estate" {
		t.Errorf("the third estate's same-address neighbour was touched: tofu-estate = %q", got)
	}
	if len(cloud.applied) != 1 || cloud.applied[0] != "aws_security_group/sg-owned" {
		t.Errorf("expected exactly one apply against the moved resource, got %v", cloud.applied)
	}
}

// TestLiveMv_fromEstateSameAsDestination: naming this configuration's own
// estate as the source describes no boundary, and is refused before any
// read.
func TestLiveMv_fromEstateSameAsDestination(t *testing.T) {
	cloud := mvUnrenamedFixture(t)

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-from-estate=stateless-unit", "aws_s3_bucket.data", "aws_s3_bucket.data"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	if !strings.Contains(output.Stderr(), "Source and destination estates are the same") {
		t.Errorf("wrong diagnostic:\n%s", output.Stderr())
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a refused move still wrote: %v", cloud.applied)
	}
}

// TestLiveMv_crossEstateWrongSource: the resource carries a third estate's
// tag, so neither the source named nor this estate owns it. Refused, naming
// both estates.
func TestLiveMv_crossEstateWrongSource(t *testing.T) {
	cloud := mvUnrenamedFixture(t)
	cloud.tags["aws_s3_bucket/tofu-mv-unit-data"]["tofu-estate"] = "somebody-else"

	c, done := newLiveMvCommand(t, cloud)
	code := c.Run([]string{"-no-color", "-from-estate=monolith", "aws_s3_bucket.data", "aws_s3_bucket.data"})
	output := done(t)
	if code != 1 {
		t.Fatalf("exit code %d, want 1\nstdout:\n%s", code, output.Stdout())
	}
	stderr := output.Stderr()
	if !strings.Contains(stderr, "Live resource owned by another estate") {
		t.Errorf("wrong diagnostic:\n%s", stderr)
	}
	if !strings.Contains(stderr, "somebody-else") || !strings.Contains(stderr, "monolith") {
		t.Errorf("the refusal does not name both estates:\n%s", stderr)
	}
	if len(cloud.applied) != 0 {
		t.Errorf("a refused move still wrote: %v", cloud.applied)
	}
}
