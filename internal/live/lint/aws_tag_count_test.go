// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"
	"testing"
)

// awsTags writes n literal tags, tag-0 = "x" onwards.
func awsTags(n int) string {
	var b strings.Builder
	b.WriteString("{\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "    tag-%d = \"x\"\n", i)
	}
	b.WriteString("  }")
	return b.String()
}

// TestAWSTagCount pins the count [checkAWSTagCount] makes: the block's tags
// and its provider's default_tags as a union, plus two markers, or three on
// a count block. Each case sits one tag either side of AWS's 50 where it
// can. GitHub issue #2131.
func TestAWSTagCount(t *testing.T) {
	bucket := func(tags string, extra string) string {
		return fmt.Sprintf(`
resource "aws_s3_bucket" "b" {
  bucket = "b"
  tags   = %s
%s
}
`, tags, extra)
	}

	cases := []struct {
		name    string
		src     string
		refuse  bool
		mention string
	}{
		{name: "48 tags and two markers fit", src: bucket(awsTags(48), "")},
		{name: "49 tags and two markers do not", src: bucket(awsTags(49), ""), refuse: true, mention: "would carry 51 tags"},
		{name: "a count block counts tofu-slot", src: bucket(awsTags(48), "  count = 2"), refuse: true, mention: "would carry 51 tags"},
		{
			name: "terraform-aws-modules' count = var.create ? 1 : 0 is three markers",
			src: `
variable "create" {
  type    = bool
  default = true
}
` + bucket(awsTags(48), "  count = var.create ? 1 : 0"),
			refuse:  true,
			mention: "tofu-slot",
		},
		{
			name: "default_tags join the union, and a shared key counts once",
			src: `
provider "aws" {
  region = "us-east-1"
  default_tags {
    tags = {
      tag-0 = "shared"
      Team  = "a"
    }
  }
}
` + bucket(awsTags(48), ""),
			refuse:  true,
			mention: "would carry 51 tags",
		},
		{
			name: "a key whose value is unknown is counted",
			src: `
resource "aws_s3_bucket" "src" {
  bucket = "src"
}
` + bucket(strings.Replace(awsTags(49), `tag-0 = "x"`, `tag-0 = aws_s3_bucket.src.arn`, 1), ""),
			refuse:  true,
			mention: "counted, since the key takes a slot",
		},
		{
			name: "merge() is the union of its arguments",
			src: `
locals {
  common = { for i in range(46) : "tag-${i}" => "x" }
}
` + bucket(`merge(local.common, { a = "1", b = "2", c = "3" })`, ""),
			refuse:  true,
			mention: "would carry 51 tags",
		},
		{
			name: "keys that cannot be read are skipped",
			src: `
variable "tags" {
  type = map(string)
}
` + bucket(`var.tags`, ""),
		},
		{
			name: "an unreadable default_tags is skipped",
			src: `
variable "defaults" {
  type = map(string)
}
provider "aws" {
  default_tags {
    tags = var.defaults
  }
}
` + bucket(awsTags(49), ""),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadGCPFiles(t, map[string]string{"main.tf": tc.src})
			got := onlyRule(CheckContext(t.Context(), cfg), RuleAWSTagCount)
			if !tc.refuse {
				if len(got) != 0 {
					t.Fatalf("want no %s, got %v", RuleAWSTagCount, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one %s, got %d: %v", RuleAWSTagCount, len(got), got)
			}
			if !strings.Contains(got[0].Detail, tc.mention) {
				t.Errorf("Detail does not say %q:\n%s", tc.mention, got[0].Detail)
			}
			if !HasErrors(got) {
				t.Errorf("%s must refuse: the create would fail", RuleAWSTagCount)
			}
		})
	}
}
