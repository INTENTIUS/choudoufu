// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// The each.key escaping regression (audit finding F-ESC), and issue #178's
// widening of it to admit "." and ":".
//
// Two sides have to produce the same string or a for_each instance binds to
// the wrong live resource, or to none:
//
//   - the DECLARED side, where discovery escapes the address OpenTofu
//     renders for the instance, and OpenTofu renders an instance key through
//     addrs' toHCLQuotedString: backslash escapes for `"`, `\`, tab, CR, LF,
//     `$`/`%` before `{`, and `\uXXXX` for anything non-printable;
//   - the STAMPED side, which is [addressExpr]'s template interpolating
//     each.key through [eachKeyEscapedExpr]'s replace() chain into an
//     already-escaped block prefix.
//
// The audit found 1180 keys where those disagree, for the grammar as it
// stood before issue #178. None of them is reachable through a lint-clean
// configuration: every character that makes the two sides differ is outside
// the for_each key set the lint rule admits (internal/live/lint,
// RuleForEachKey), and every character toHCLQuotedString touches is outside
// it too - a claim now proved by construction rather than by exclusion,
// since "." and ":" are back in the admitted set (live/MARKERS.md, "for_each
// key escaping") and eachKeyEscapedExpr's replace() chain is what keeps them
// from reaching toHCLQuotedString's territory: neither side's escaping ever
// touches `"`, `\`, tab/CR/LF, `$`/`%` before `{`, or a non-printable rune,
// so the two sides still cannot collide on those.
//
// What is expressible as an HCL expression is the invariant this rule
// enforces, not toHCLQuotedString's own escaping (replace() cannot condition
// on what follows a `$`, and cannot produce `\uXXXX` for a non-printable
// rune) - and these tests are it. The first proves the two sides agree for
// every admitted key, including one containing "." or ":" and round-tripping
// through the address unescaper. The second proves they still disagree for
// whatever remains rejected, so the rule is provably buying something even
// after the widening.

// forEachEscapeKeys is every key TestStamp_eachKeyEscapingRoundTrips checks,
// and also the block's own for_each set (see [forEachEscapeConfig]): a key
// needing [markerkey.Encode]'s help only takes the lookup-table path
// ([forEachNeedsKeyLookup]) when this pass can see it as one of the block's
// own statically-known keys, so a key exercised here has to actually be in
// the for_each map, not merely injected into the evaluation scope the way
// [eachData] used to supply every key on its own, decoupled from the
// resource's declared for_each. See eachData's own remaining callers for
// the negative control, where that decoupling is still exactly the point.
var forEachEscapeKeys = []string{
	"a", "Web", "eu-west-1a", "team_one", "eu/west", "size=1",
	"plus+one", "at@sign", "with space", "été", "日本", "007",
	// issue #178: "." and ":" are admitted, escaped via eachKeyEscapedExpr
	// (or, when combined with an out-of-charset rune elsewhere in the same
	// block, folded into the lookup table alongside it) rather than
	// embedded raw.
	"alice.smith", "2001:db8::/64", "a.b.c", "a:b:c",
	"user@example.com", "group.role@team:prod", "@leading.dot",
	"trailing.dot.", "..", "::",
	// issue #210: outside the pre-#210 admitted set, but printable and none
	// of the six characters excluded for colliding with a DIFFERENT
	// escaping rule (addrs' toHCLQuotedString, or EscapeAddress's own "["
	// / "]" delimiters) - see markerkey.Encode's doc comment.
	"a(b)", "a;b", "a!b", "a*b", "a#b", "a,b", "€uro", "a(b) (c)",
}

// forEachEscapeConfig builds one for_each block whose keys are exactly
// [forEachEscapeKeys], as a toset() literal so each.value == each.key for
// every instance, matching what [eachData] injects for the negative
// control below. Every key here has already passed lint.ValidForEachKey by
// construction (TestStamp_eachKeyEscapingRoundTrips checks that too, as a
// premise guard), and none of the admitted set needs backslash or quote
// escaping to render as an HCL string literal, so %q is a safe, plain
// quoting - it is never asked to escape anything more exotic than that.
func forEachEscapeConfig() string {
	quoted := make([]string, len(forEachEscapeKeys))
	for i, k := range forEachEscapeKeys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return fmt.Sprintf(`
resource "aws_subnet" "this" {
  for_each   = toset([%s])
  cidr_block = each.value
}
`, strings.Join(quoted, ", "))
}

// TestStamp_eachKeyEscapingRoundTrips is the F-ESC regression: for every key
// the subset admits, the marker this package stamps is byte-identical to the
// marker discovery derives from the declared address, and reading it back
// yields the instance it came from.
func TestStamp_eachKeyEscapingRoundTrips(t *testing.T) {
	cfg := loadSource(t, forEachEscapeConfig())

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, key := range forEachEscapeKeys {
		if !lint.ValidForEachKey(key) {
			t.Fatalf("key %q is not in the admitted set; this test's premise is wrong", key)
		}

		stamped := evalTags(t, cfg, "aws_subnet.this", eachData(key))["tofu-address"]
		declared := discovery.EscapeAddress(forEachInstanceAddr(key))

		if stamped != declared {
			t.Errorf("key %q: stamped marker %q != declared marker %q", key, stamped, declared)
			continue
		}
		if !discovery.ValidMarkerAddress(stamped) {
			t.Errorf("key %q: stamped marker %q is not a well-formed marker address", key, stamped)
			continue
		}
		back, ok := discovery.UnescapeAddress(stamped)
		if !ok {
			t.Errorf("key %q: stamped marker %q cannot be read back into an address", key, stamped)
			continue
		}
		// An all-digit key reads back as a count index. That is the one
		// ambiguity live/MARKERS.md keeps on purpose, and the audit
		// listed it among the things that HELD; it is not a mis-bind,
		// because binding compares two escaped values and those two are the
		// same string.
		if want := forEachInstanceAddr(key); back.String() != want && !allDigits(key) {
			t.Errorf("key %q: marker read back as %q, want %q", key, back.String(), want)
		}
	}
}

// TestStamp_rejectedEachKeysWouldMisbind is the negative control: each of
// these keys is one of the audit's mis-bind pairs, and each is rejected by
// the lint rule, which is why none of them can reach this code path in a
// lint-clean run.
func TestStamp_rejectedEachKeysWouldMisbind(t *testing.T) {
	cfg := loadSource(t, forEachEscapeConfig())

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, key := range []string{`a"b`, `a\b`, "a[0]", "a${b", "a\tb"} {
		if lint.ValidForEachKey(key) {
			t.Fatalf("key %q is admitted by the lint rule; this test's premise is wrong", key)
		}

		stamped := evalTags(t, cfg, "aws_subnet.this", eachData(key))["tofu-address"]
		declared := discovery.EscapeAddress(forEachInstanceAddr(key))

		broken := stamped != declared || !discovery.ValidMarkerAddress(stamped)
		if !broken {
			if _, ok := discovery.UnescapeAddress(stamped); !ok {
				broken = true
			}
		}
		if !broken {
			t.Errorf("key %q: stamped and declared markers both equal %q and it reads back cleanly, so the lint rule rejects it for no reason", key, stamped)
		}
	}
}

// TestStamp_noAdmittedRuneMisbinds is the exhaustive form of the claim the
// two tests above make by example: NO key the subset admits can mis-bind.
//
// A per-rune sweep is a complete proof rather than a sample, because both
// sides are per-rune maps over the admitted set. discovery.EscapeKey (the
// Go mirror of the replace() chain [eachKeyEscapedExpr] builds) rewrites
// three characters - "@", "." and ":" - and copies the rest; the single-rune
// keys this test constructs never trigger more than one substitution, so
// simulating the stamped side as prefix+EscapeKey(key) here is exactly what
// evaluating the real HCL expression would produce. addrs' toHCLQuotedString
// is per-rune too, with one lookahead - it doubles a "$" or "%" that
// precedes a "{" - and neither "$", "%" nor "{" is in the admitted set, so
// no admitted key can contain the pair and the lookahead can never fire.
// Every remaining interaction is between one input rune and one output rune,
// which is what this sweeps.
func TestStamp_noAdmittedRuneMisbinds(t *testing.T) {
	prefix := discovery.EscapeAddress(`aws_subnet.this["`)

	admitted := 0
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogate halves are not characters
		}
		key := string(r)
		if !lint.ValidForEachKey(key) {
			continue
		}
		admitted++

		stamped := prefix + discovery.EscapeKey(key)
		declared := discovery.EscapeAddress(forEachInstanceAddr(key))
		if stamped != declared {
			t.Fatalf("U+%04X (%q) is admitted but stamps %q while the declared address escapes to %q", r, key, stamped, declared)
		}
		if !discovery.ValidMarkerAddress(stamped) {
			t.Fatalf("U+%04X (%q) is admitted but produces the unreadable marker %q", r, key, stamped)
		}
		if back := discovery.UnescapeKey(discovery.EscapeKey(key)); back != key {
			t.Fatalf("U+%04X (%q) does not round-trip through EscapeKey/UnescapeKey: got %q", r, key, back)
		}
	}

	// A floor, so that a rule change that accidentally admitted nothing
	// would fail here instead of passing vacuously.
	if admitted < 1000 {
		t.Fatalf("only %d runes admitted; the sweep is not exercising the rule", admitted)
	}
}

// forEachInstanceAddr renders the declared address of aws_subnet.this[key]
// the way OpenTofu renders it, quoting rules and all.
func forEachInstanceAddr(key string) string {
	res := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "aws_subnet", Name: "this"}
	return res.Instance(addrs.StringKey(key)).String()
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
