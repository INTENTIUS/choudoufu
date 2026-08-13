// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"strings"
	"testing"
)

// TestOverlongAddressRule covers the three ways an instance address is
// measured against the 1024-character marker budget - MaxContinuations x
// MaxTagValue, issue #71 - (a plain resource, a for_each key, a count
// index), plus the two silences the fixture pins: an address inside the
// budget, and a long-labeled resource whose for_each is not statically
// evaluable, which the rule skips rather than guesses at.
func TestOverlongAddressRule(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/overlong-address")
	issues := CheckContext(t.Context(), cfg)

	assertIssues(t, issues, []wantIssue{
		{
			rule:      RuleOverlongAddress,
			construct: "aws_s3_bucket." + strings.Repeat("x", 1011),
			file:      "testdata/overlong-address/main.tf",
			line:      16,
		},
		{
			rule:      RuleOverlongAddress,
			construct: `aws_subnet.wide["` + strings.Repeat("k", 1009) + `"]`,
			file:      "testdata/overlong-address/main.tf",
			line:      24,
		},
		{
			rule:      RuleOverlongAddress,
			construct: "aws_vpc." + strings.Repeat("y", 1015) + "[1]",
			file:      "testdata/overlong-address/main.tf",
			line:      32,
		},
	})
}
