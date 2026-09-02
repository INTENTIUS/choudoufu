// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestForEachKeyRule is the regression for finding F-FE: a for_each key
// outside the set [markerkey.Encode] can carry into a marker must be a named
// RuleForEachKey issue before anything is written anywhere. "." and ":"
// moved out of this fixture in issue #178, and ";" and "!" moved out in
// issue #210, each because the widened set admits them now; see
// TestForEachKeyRuleClean for their coverage.
func TestForEachKeyRule(t *testing.T) {
	cfg := loadConfigDir(t, "testdata/foreach-key")
	issues := CheckContext(t.Context(), cfg)

	assertIssues(t, issues, []wantIssue{
		{
			rule:      RuleForEachKey,
			construct: `for_each key "a\"b" in aws_subnet.quote`,
			file:      "testdata/foreach-key/main.tf",
			line:      29,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "a\\b" in aws_subnet.backslash`,
			file:      "testdata/foreach-key/main.tf",
			line:      37,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "10.0.0.0/24[10.0.1.0/24" in aws_subnet.from_local`,
			file:      "testdata/foreach-key/main.tf",
			line:      43,
		},
		{
			rule:      RuleForEachKey,
			construct: `for_each key "bad%key" in aws_s3_bucket.punctuation`,
			file:      "testdata/foreach-key/main.tf",
			line:      49,
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
// live/LIMITATIONS.md and live/MARKERS.md quote. Before issue #210 this was
// the full AWS tag-value set (letters, digits, space, and + - = . _ : / @)
// with nothing else admitted. Issue #210 widened it to any printable
// character except the six in [markerkey.Excluded], each excluded because
// it collides with a DIFFERENT escaping rule ([markerkey.Encode]'s doc
// comment has the accounting) rather than because the AWS tag-value
// charset itself objects to it - Encode carries everything else across the
// trip now, the same way internal/live/markers has escaped a key's own "."
// and ":" (and "@") since issue #178.
func TestValidForEachKey(t *testing.T) {
	valid := []string{
		"a", "web", "Web", "0", "007", "eu-west-1a", "team_one", "eu/west",
		"size=1", "plus+one", "at@sign", "with space", "été", "日本",
		"a-very-long-but-perfectly-ordinary-key",
		"a.b", "2001:db8::/64", "user@example.com", "a:b.c@d",
		// issue #210: outside the pre-#210 AWS-legal set, but printable and
		// none of the six characters [markerkey.Excluded] names - [Encode]
		// carries every one of these into a marker now.
		"a,b", "a;b", "a!b", "a*b", "a#b", "a(b)", "a (b) (c)", "€uro",
	}
	for _, key := range valid {
		if !ValidForEachKey(key) {
			r, _ := InvalidForEachKeyRune(key)
			t.Errorf("ValidForEachKey(%q) = false, want true (rejected on %q)", key, string(r))
		}
	}

	invalid := []string{
		"",             // no key at all: an escaped address ending in ":"
		"a[0]",         // corrupts EscapeAddress's own "[...]" bracket scan
		`a"b`,          // backslash-escaped on the declared side, before Encode runs
		`a\b`,          // ditto
		"a\nb", "a\tb", // non-printable: \u-escaped on the declared side
		"a${b", "a%{b", // template introducers, doubled on the declared side
		"bad%key", "50%", // "%" collides with the same doubling, unconditionally
		"a\u200bb", // zero-width space: printable-ish, not a letter or digit
	}
	for _, key := range invalid {
		if ValidForEachKey(key) {
			t.Errorf("ValidForEachKey(%q) = true, want false", key)
		}
	}
}

// TestForEachKeyEncodedLengthIsRefusedNotTruncated is issue #210's
// requirement 5: [markerkey.Encode] expands a key (each out-of-charset rune
// becomes seven characters), and a key long enough for that expansion to
// push the escaped address past markers.MaxAddressLen must be a clear
// refusal, never a silent truncation. This rule (RuleForEachKey) does not
// itself carry a length budget - it only says which characters a key may
// contain - but internal/live/markers' EscapeKey now runs Encode before its
// own doubling (see EscapeKey's doc comment), so the PRE-EXISTING
// overlong-address rule, which already measured the escaped address
// generically, measures the expanded length correctly too, without this
// package needing a second, narrower length check of its own. ValidForEachKey
// admits the key on its own (every rune is individually representable);
// RuleOverlongAddress is what refuses the block once its worst-case escaped
// address is measured.
func TestForEachKeyEncodedLengthIsRefusedNotTruncated(t *testing.T) {
	longKey := strings.Repeat("(", 300) // encodes to 7 * 300 = 2100 runes
	if !ValidForEachKey(longKey) {
		t.Fatalf("ValidForEachKey(%q) = false; this test's premise (a long but individually valid key) is wrong", longKey)
	}

	src := `
resource "aws_subnet" "this" {
  for_each   = toset(["` + longKey + `"])
  cidr_block = "10.0.0.0/24"
}
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}
	issues := CheckContext(t.Context(), loadConfigDir(t, dir))

	var overlong *Issue
	for i := range issues {
		if issues[i].Rule == RuleOverlongAddress {
			overlong = &issues[i]
		}
	}
	if overlong == nil {
		t.Fatalf("a for_each key that encodes to 2100+ characters produced no RuleOverlongAddress issue; it would have been silently truncated instead")
	}
}
