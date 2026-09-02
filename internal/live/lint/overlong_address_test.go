// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestOverlongAddressRule covers the four ways an instance address is
// measured against the 1024-character marker budget - MaxContinuations x
// MaxTagValue, issue #71 - (a plain resource, a for_each key, a count
// index, and a count'd MODULE step's index), plus the two silences the
// fixture pins: an address inside the budget, and a long-labeled resource
// whose for_each is not statically evaluable, which the rule skips rather
// than guesses at.
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
		{
			rule:      RuleOverlongAddress,
			construct: "module.counted[11].aws_s3_bucket." + strings.Repeat("q", 993),
			module:    "module.counted",
			file:      "testdata/overlong-address/counted/main.tf",
			line:      16,
		},
	})
}

// TestOverlongAddressCountedModuleStep is the value-level half of the
// count'd-module case TestOverlongAddressRule pins by construct alone: it
// asserts the character count the rule reports, because the whole defect
// this fixture was written for was a length that came out too SMALL while
// every boolean in sight stayed correct.
//
// worstCaseChildKey read only a module call's for_each and treated a count'd
// call as static, so every address inside one measured with no module
// instance key at all. testdata/overlong-address/counted's label is sized so
// that the three candidate readings straddle the budget:
//
//	module.counted.aws_s3_bucket.<label>     1022  the unkeyed reading (was)
//	module.counted:0.aws_s3_bucket.<label>   1024  the lowest index
//	module.counted:11.aws_s3_bucket.<label>  1025  the highest index (is)
//
// The budget is 1024, so the first two are silent and only the third
// reports. That makes this test fail three distinct ways: silently under the
// old unkeyed reading, silently under a fix that picked the lowest index
// instead of the highest, and loudly with the wrong number under any fix
// that got the escaping wrong.
func TestOverlongAddressCountedModuleStep(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/overlong-address")
	issues := CheckContext(t.Context(), cfg)

	const wantAddr = "module.counted[11].aws_s3_bucket."
	var got *Issue
	for i := range issues {
		if issues[i].Rule == RuleOverlongAddress && strings.HasPrefix(issues[i].Construct, wantAddr) {
			got = &issues[i]
			break
		}
	}
	if got == nil {
		t.Fatalf("no RuleOverlongAddress issue for an address under %q; a count'd module call's "+
			"instance key is missing from the measured address", wantAddr)
	}

	// The escaped form is what the marker carries, and what the Detail
	// reports: "module.counted:11.aws_s3_bucket." plus the 993-character
	// label, one character past the 1024-character budget.
	escaped := markers.EscapeAddress(got.Construct)
	if wantLen := 1025; utf8.RuneCountInString(escaped) != wantLen {
		t.Errorf("escaped address is %d characters, want %d", utf8.RuneCountInString(escaped), wantLen)
	}
	if want := fmt.Sprintf("is %d characters", 1025); !strings.Contains(got.Detail, want) {
		t.Errorf("Detail = %q, want it to report %q", got.Detail, want)
	}
}
