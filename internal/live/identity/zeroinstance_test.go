// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestZeroInstanceBlocks pins what [ZeroInstanceBlocks] answers, and - the
// half that matters more - what it declines to answer.
//
// Its output becomes an empty collection in a state a plan diffs against
// (internal/live/projection's withZeroInstanceBlocks), so a block reported
// here that actually has instances would put a confidently wrong prior
// value in front of the diff and render it as clean. The strictness cases
// below are therefore the load-bearing ones: an expansion this pass cannot
// evaluate must contribute NOTHING, which is a stricter reading than
// internal/live/lint's blockHasNoInstances takes of the same question for
// its own, differently-shaped purpose.
func TestZeroInstanceBlocks(t *testing.T) {
	dir := t.TempDir()
	const src = `
variable "create" {
  type    = bool
  default = false
}

variable "names" {
  type    = map(string)
  default = {}
}

# Zero: count evaluates to 0 from configuration and variables alone.
resource "aws_cloudwatch_log_group" "counted_zero" {
  count = var.create ? 1 : 0
  name  = "/zero"
}

# Zero: for_each evaluates to an empty map.
resource "aws_cloudwatch_log_group" "each_zero" {
  for_each = var.names
  name     = each.value
}

# Not zero: count evaluates to 1.
resource "aws_cloudwatch_log_group" "counted_one" {
  count = 1
  name  = "/one"
}

# Not zero, and not reportable either: no count, no for_each, no
# lifecycle.enabled means exactly one instance, always.
resource "aws_cloudwatch_log_group" "plain" {
  name = "/plain"
}

# Not reportable: the count reads a resource attribute, so the expansion
# does not resolve. "Could not evaluate" is not "zero".
resource "aws_cloudwatch_log_group" "unknowable" {
  count = aws_cloudwatch_log_group.plain.arn == "never-matches" ? 1 : 0
  name  = "/unknowable"
}

# Data sources are visited too: half of issue #349 is a zero-instance
# ` + "`data`" + ` block a try() has to fall through.
data "aws_caller_identity" "absent" {
  count = var.create ? 1 : 0
}

data "aws_caller_identity" "present" {
  count = 1
}

module "child" {
  source = "./child"
}
`
	const child = `
variable "create" {
  type    = bool
  default = false
}

resource "aws_cloudwatch_log_group" "counted_zero" {
  count = var.create ? 1 : 0
  name  = "/child-zero"
}

resource "aws_cloudwatch_log_group" "counted_one" {
  count = 1
  name  = "/child-one"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child", "main.tf"), []byte(child), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := loadConfigTree(t, dir, map[string]cty.Value{})

	var got []string
	for _, block := range ZeroInstanceBlocks(t.Context(), cfg) {
		got = append(got, block.Addr.String())
	}
	slices.Sort(got)

	want := []string{
		"aws_cloudwatch_log_group.counted_zero",
		"aws_cloudwatch_log_group.each_zero",
		"data.aws_caller_identity.absent",
		"module.child.aws_cloudwatch_log_group.counted_zero",
	}
	if !slices.Equal(got, want) {
		t.Errorf("ZeroInstanceBlocks =\n  %v\nwant\n  %v", got, want)
	}
}

// TestZeroInstanceBlocksNeedsAStaticEvaluator pins the cheap guard: a
// configuration loaded without one has no way to evaluate a count, and the
// answer is "nothing to report" rather than a panic or a guess.
func TestZeroInstanceBlocksNeedsAStaticEvaluator(t *testing.T) {
	if got := ZeroInstanceBlocks(t.Context(), nil); got != nil {
		t.Errorf("ZeroInstanceBlocks(nil) = %v, want nil", got)
	}
}
