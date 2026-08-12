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

	"github.com/mitchellh/cli"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/command/workdir"
	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/terminal"
	"github.com/opentofu/opentofu/internal/tfdiags"
	"github.com/opentofu/opentofu/internal/tofu"
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
		schema := mvSchemas()[req.TypeName]
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: req.TypeName,
			State:    mvObject(schema, map[string]string{"id": req.Target.ID}, nil),
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
