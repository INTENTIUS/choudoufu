// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"
)

// TestIgnoreTags pins [checkIgnoreTags]: keys match a marker key exactly,
// key_prefixes match when a marker key starts with the prefix, and a prefix
// that only partly overlaps is no match. GitHub issue #2290.
func TestIgnoreTags(t *testing.T) {
	provider := func(body string) string {
		return `
provider "aws" {
  region = "us-east-1"
  ignore_tags {
` + body + `
  }
}

resource "aws_s3_bucket" "b" {
  bucket = "b"
}
`
	}
	cases := []struct {
		name    string
		src     string
		refuse  bool
		mention string
	}{
		{name: "a prefix covering every marker", src: provider(`    key_prefixes = ["tofu-"]`), refuse: true, mention: `key_prefixes "tofu-" covers tofu-estate`},
		{name: "a shorter prefix", src: provider(`    key_prefixes = ["to"]`), refuse: true, mention: "covers tofu-address"},
		{name: "a marker key named", src: provider(`    keys = ["Owner", "tofu-address"]`), refuse: true, mention: `keys "tofu-address" covers tofu-address`},
		{name: "a continuation key named", src: provider(`    keys = ["tofu-address-2"]`), refuse: true, mention: "covers tofu-address-2"},
		{name: "tofu-slot named", src: provider(`    keys = ["tofu-slot"]`), refuse: true, mention: "covers tofu-slot"},
		{name: "a prefix that only partly overlaps", src: provider(`    key_prefixes = ["tofu-x"]`)},
		{name: "unrelated keys and prefixes", src: provider(`    keys = ["Owner"]
    key_prefixes = ["kubernetes.io/"]`)},
		{
			name: "a value read from a variable with a default",
			src: `
variable "ignored" {
  type    = list(string)
  default = ["tofu-estate"]
}
` + provider(`    keys = var.ignored`),
			refuse:  true,
			mention: "covers tofu-estate",
		},
		{
			name: "a value that cannot be read is skipped",
			src: `
variable "ignored" {
  type = list(string)
}
` + provider(`    keys = var.ignored`),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := loadGCPFiles(t, map[string]string{"main.tf": tc.src})
			got := onlyRule(CheckContext(t.Context(), cfg), RuleIgnoreTags)
			if !tc.refuse {
				if len(got) != 0 {
					t.Fatalf("want no %s, got %v", RuleIgnoreTags, got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one %s, got %d: %v", RuleIgnoreTags, len(got), got)
			}
			if !strings.Contains(got[0].Detail, tc.mention) {
				t.Errorf("Detail does not say %q:\n%s", tc.mention, got[0].Detail)
			}
			if !HasErrors(got) {
				t.Errorf("%s must refuse", RuleIgnoreTags)
			}
		})
	}
}
