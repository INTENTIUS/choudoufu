// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

// GitHub issue #1044. live-ls resolved its region from -region, else the
// AWS SDK's default chain, while live-plan and live-check on the same DIR
// read it from the root's own provider block (`region = var.aws_region`,
// so TF_VAR_aws_region). With the two disagreeing, `live-ls DIR` and
// `live-plan` in DIR read different regions of the same account and their
// answers could not be compared. With a DIR given, the root's provider
// region is now the default; the human report says which source the
// region came from, so a mismatch is visible; without a DIR the SDK chain
// stands; -region still wins.

// writeLiveLsRoot writes one main.tf into a fresh directory and returns
// the directory.
func writeLiveLsRoot(t *testing.T, mainTF string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mainTF), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const liveLsRootWithLiteralRegion = `
provider "aws" {
  region = "eu-west-1"
}
resource "aws_s3_bucket" "data" {
  bucket = "my-bucket"
}
`

const liveLsRootWithVariableRegion = `
variable "aws_region" {
  type = string
}
provider "aws" {
  region = var.aws_region
}
resource "aws_s3_bucket" "data" {
  bucket = "my-bucket"
}
`

// loadLiveLsRoot loads dir the way the command does, through Meta.loadConfig,
// so the root's StaticEvaluator carries whatever TF_VAR_* the test set.
func loadLiveLsRoot(t *testing.T, dir string) liveLsRegion {
	t.Helper()
	view, _ := testView(t)
	c := &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view}}
	c.Meta.input = false
	ctx := context.Background()
	config, cfgDiags := c.loadConfig(ctx, dir)
	if cfgDiags.HasErrors() {
		t.Fatalf("the root did not load: %s", cfgDiags.Err())
	}
	return liveLsRootRegion(ctx, dir, config, cfgDiags)
}

// Case 1: a literal `region = "eu-west-1"` in the root's provider block is
// the default region, and the source names the block.
func TestLiveLsRootRegion_literal(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	dir := writeLiveLsRoot(t, liveLsRootWithLiteralRegion)

	got := loadLiveLsRoot(t, dir)
	if got.Region != "eu-west-1" {
		t.Errorf("region = %q, want eu-west-1 from the provider block (AWS_REGION is us-east-1 and must not win)", got.Region)
	}
	if got.Source != "provider" {
		t.Errorf("source = %q, want \"provider\"", got.Source)
	}
	if got.Note != `provider "aws"` {
		t.Errorf("note = %q, want the block's own name, `provider \"aws\"`", got.Note)
	}
}

// Case 2: `region = var.aws_region` with TF_VAR_aws_region set resolves
// through the same variable binding live-plan and live-check use.
func TestLiveLsRootRegion_variable(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("TF_VAR_aws_region", "eu-central-1")
	dir := writeLiveLsRoot(t, liveLsRootWithVariableRegion)

	got := loadLiveLsRoot(t, dir)
	if got.Region != "eu-central-1" {
		t.Errorf("region = %q, want eu-central-1 from TF_VAR_aws_region through the provider block", got.Region)
	}
	if got.Source != "provider" {
		t.Errorf("source = %q, want \"provider\"", got.Source)
	}
}

// An unresolvable expression - the variable has no default and nothing set
// it - falls through to the SDK chain and says so. Never a guess.
func TestLiveLsRootRegion_unresolvableFallsThroughAndSaysSo(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	os.Unsetenv("TF_VAR_aws_region")
	dir := writeLiveLsRoot(t, liveLsRootWithVariableRegion)

	got := loadLiveLsRoot(t, dir)
	if got.Region != "" {
		t.Errorf("region = %q, want empty: an unresolvable expression must not be guessed", got.Region)
	}
	if got.Source != "sdk" {
		t.Errorf("source = %q, want \"sdk\"", got.Source)
	}
	if !strings.Contains(got.Note, `provider "aws"`) || !strings.Contains(got.Note, "could not resolve") {
		t.Errorf("note = %q, want it to name the block and say the region could not be resolved", got.Note)
	}
	// live-ls never prompts (Meta.input is false), so the variable machinery's
	// own "Failed to request input from user" is not the reason a reader
	// needs; the note says the variable has no value and what supplies one.
	if !strings.Contains(got.Note, "var.aws_region has no value") {
		t.Errorf("note = %q, want it to say var.aws_region has no value rather than that a prompt failed", got.Note)
	}
}

// A provider block that sets no region at all is the SDK chain, and the
// note says that is why.
func TestLiveLsRootRegion_noRegionInBlockSaysSo(t *testing.T) {
	dir := writeLiveLsRoot(t, `
provider "aws" {}
resource "aws_s3_bucket" "data" {
  bucket = "my-bucket"
}
`)

	got := loadLiveLsRoot(t, dir)
	if got.Region != "" || got.Source != "sdk" {
		t.Errorf("region = %q, source = %q; want empty and \"sdk\"", got.Region, got.Source)
	}
	if !strings.Contains(got.Note, "sets no region") {
		t.Errorf("note = %q, want it to say the block sets no region", got.Note)
	}
}

// liveLsHumanRun runs live-ls in its human (non-JSON) view against the fake
// tagging server, with dir as DIR when non-empty and extra flags appended.
func liveLsHumanRun(t *testing.T, dir string, flags ...string) (string, string) {
	t.Helper()
	srv := &fakeLiveLsServer{
		t: t,
		tagged: []cloudcontrol.TaggedResource{
			{
				ResourceARN: "arn:aws:s3:::my-bucket",
				Tags:        map[string]string{"tofu-estate": "prod", "tofu-address": "aws_s3_bucket.data"},
			},
		},
	}
	server := srv.start()

	t.Setenv("TOFU_LIVE_CLOUDCONTROL", "")
	t.Setenv("AWS_ENDPOINT_URL", server.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	view, done := testView(t)
	c := &LiveLsCommand{Meta: Meta{WorkingDir: workdir.NewDir("."), View: view}}
	args := append([]string{"-no-color", "-estate=prod"}, flags...)
	if dir != "" {
		args = append(args, dir)
	}
	code := c.Run(args)
	out := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out.Stdout(), out.Stderr())
	}
	return out.Stdout(), out.Stderr()
}

// With DIR, the root's region is the listing's region, and the source line
// names the block and the directory.
func TestLiveLsCommand_Run_regionFromRootIsPrintedWithItsSource(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	dir := writeLiveLsRoot(t, liveLsRootWithLiteralRegion)

	stdout, _ := liveLsHumanRun(t, dir)
	want := `Region eu-west-1 (from provider "aws" in ` + dir + `).`
	if !strings.Contains(stdout, want) {
		t.Errorf("the source line is missing or worded differently.\nwant: %s\n--- stdout ---\n%s", want, stdout)
	}
	if !strings.Contains(stdout, "carry its marker in eu-west-1.") {
		t.Errorf("the header does not carry the root's region:\n%s", stdout)
	}
}

// Case 4: an explicit -region beats the root, and the source line says the
// flag supplied it.
func TestLiveLsCommand_Run_regionFlagBeatsRoot(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	dir := writeLiveLsRoot(t, liveLsRootWithLiteralRegion)

	stdout, _ := liveLsHumanRun(t, dir, "-region=us-west-2")
	want := `Region us-west-2 (from -region).`
	if !strings.Contains(stdout, want) {
		t.Errorf("the source line is missing or worded differently.\nwant: %s\n--- stdout ---\n%s", want, stdout)
	}
	if strings.Contains(stdout, "eu-west-1") {
		t.Errorf("the root's region leaked into a run that named -region:\n%s", stdout)
	}
}

// Case 3: without a DIR, the SDK chain stands, and the line says so with the
// region the chain resolved.
func TestLiveLsCommand_Run_noDirKeepsSDKChain(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")

	stdout, _ := liveLsHumanRun(t, "")
	want := `Region us-east-1 (from the AWS SDK's default chain).`
	if !strings.Contains(stdout, want) {
		t.Errorf("the source line is missing or worded differently.\nwant: %s\n--- stdout ---\n%s", want, stdout)
	}
}

// With DIR whose region cannot be resolved, the SDK chain stands and the
// line says why the root did not supply one.
func TestLiveLsCommand_Run_unresolvableRootRegionSaysWhy(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	os.Unsetenv("TF_VAR_aws_region")
	dir := writeLiveLsRoot(t, liveLsRootWithVariableRegion)

	stdout, _ := liveLsHumanRun(t, dir)
	if !strings.Contains(stdout, "Region us-east-1 (from the AWS SDK's default chain; provider \"aws\" in "+dir+" sets a region this command could not resolve") {
		t.Errorf("the source line does not say the SDK chain was used because the root's region could not be resolved:\n%s", stdout)
	}
}
