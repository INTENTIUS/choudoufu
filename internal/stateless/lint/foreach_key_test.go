// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"testing"
)

// TestForEachKeyRule is the regression for finding F-FE: a for_each key
// carrying "." or ":" used to pass lint, stamp a marker, and apply, and only
// then wedge every later run. Each of these keys must be a named
// RuleForEachKey issue before anything is written anywhere.
func TestForEachKeyRule(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/foreach-key")
	issues := CheckContext(t.Context(), cfg)

	assertIssues(t, issues, []wantIssue{
		{
			rule:      RuleForEachKey,
			construct: `for_each key "a.b" in aws_subnet.dotted`,
			file:      "testdata/foreach-key/main.tf",
			line:      18,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "2001:db8::/64" in aws_subnet.colon`,
			file:      "testdata/foreach-key/main.tf",
			line:      26,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "10.0.0.0/24" in aws_subnet.from_local`,
			file:      "testdata/foreach-key/main.tf",
			line:      32,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "10.0.1.0/24" in aws_subnet.from_local`,
			file:      "testdata/foreach-key/main.tf",
			line:      32,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "bad%key" in aws_s3_bucket.punctuation`,
			file:      "testdata/foreach-key/main.tf",
			line:      38,
		},
	})
}

// TestForEachKeyRuleClean is the false-positive guard. The keys are the whole
// admitted set, and the three unevaluable for_each shapes (over another
// resource, over a data source, over a variable a test module call refuses to
// supply) must produce silence rather than a guess or a panic.
func TestForEachKeyRuleClean(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/foreach-key-clean")
	if issues := CheckContext(t.Context(), cfg); len(issues) != 0 {
		t.Errorf("CheckContext() reported %d issues for a clean for_each fixture, want 0", len(issues))
		for _, issue := range issues {
			t.Logf("  got: %s", issue)
		}
	}
}

// TestValidForEachKey pins the character set itself, which is the part
// stateless/LIMITATIONS.md and stateless/MARKERS.md quote: the AWS tag-value
// set (letters, digits, space, and + - = . _ : / @) less the two characters
// that separate the segments of an escaped address.
func TestValidForEachKey(t *testing.T) {
	valid := []string{
		"a", "web", "Web", "0", "007", "eu-west-1a", "team_one", "eu/west",
		"size=1", "plus+one", "at@sign", "with space", "été", "日本",
		"a-very-long-but-perfectly-ordinary-key",
	}
	for _, key := range valid {
		if !ValidForEachKey(key) {
			r, _ := InvalidForEachKeyRune(key)
			t.Errorf("ValidForEachKey(%q) = false, want true (rejected on %q)", key, string(r))
		}
	}

	invalid := []string{
		"",              // no key at all: an escaped address ending in ":"
		"a.b",           // the segment separator
		"2001:db8::/64", // the index introducer; AWS-legal, which is the trap
		"a[0]",          // rewritten by the escaping rule
		`a"b`,           // dropped by the escaping rule
		`a\b`,           // backslash-escaped on the declared side only
		"a\nb", "a\tb",  // ditto
		"a${b", "a%{b", // template introducers, doubled on the declared side
		"bad%key", "50%", // outside the AWS tag-value set
		"a,b", "a;b", "a!b", "a*b", "a#b", "a(b)",
		"a\u200bb", // zero-width space: printable-ish, not a letter or digit
	}
	for _, key := range invalid {
		if ValidForEachKey(key) {
			t.Errorf("ValidForEachKey(%q) = true, want false", key)
		}
	}
}
