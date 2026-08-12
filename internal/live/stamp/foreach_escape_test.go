// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// The each.key escaping regression (audit finding F-ESC).
//
// Two sides have to produce the same string or a for_each instance binds to
// the wrong live resource, or to none:
//
//   - the DECLARED side, where discovery escapes the address OpenTofu
//     renders for the instance, and OpenTofu renders an instance key through
//     addrs' toHCLQuotedString: backslash escapes for `"`, `\`, tab, CR, LF,
//     `$`/`%` before `{`, and `\uXXXX` for anything non-printable;
//   - the STAMPED side, which is [addressExpr]'s template interpolating
//     each.key raw into an already-escaped block prefix.
//
// The audit found 1180 keys where those disagree. None of them is reachable
// through a lint-clean configuration: every character that makes the two
// sides differ is outside the for_each key set the lint rule admits
// (internal/live/lint, RuleForEachKey), because that set is the AWS
// tag-value characters less "." and ":", and every character
// toHCLQuotedString touches is outside it already.
//
// So the fix for F-ESC is the key restriction, not a second escaping pass in
// the template. A faithful one is not expressible as an HCL expression
// anyway: replace() cannot condition on what follows a `$`, and cannot
// produce `\uXXXX` for a non-printable rune. What is expressible is the
// invariant, and these two tests are it - the first that the two sides agree
// for every admitted key, the second that they disagree for the rejected
// ones, so the rule is provably buying something.

// forEachEscapeConfig is one for_each block; the keys under test are supplied
// by the evaluation scope rather than by the for_each expression, exactly as
// the plan engine supplies them.
const forEachEscapeConfig = `
resource "aws_subnet" "this" {
  for_each   = { a = "10.42.1.0/24" }
  cidr_block = each.value
}
`

// TestStamp_eachKeyEscapingRoundTrips is the F-ESC regression: for every key
// the subset admits, the marker this package stamps is byte-identical to the
// marker discovery derives from the declared address, and reading it back
// yields the instance it came from.
func TestStamp_eachKeyEscapingRoundTrips(t *testing.T) {
	cfg := loadSource(t, forEachEscapeConfig)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	keys := []string{
		"a", "Web", "eu-west-1a", "team_one", "eu/west", "size=1",
		"plus+one", "at@sign", "with space", "été", "日本", "007",
	}
	for _, key := range keys {
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
	cfg := loadSource(t, forEachEscapeConfig)

	_, diags := Stamp(t.Context(), Request{Estate: "stamp-unit", Config: cfg, Schemas: testSchemas()})
	assertNoErrors(t, diags)

	for _, key := range []string{"a.b", "2001:db8::/64", `a"b`, `a\b`, "a[0]", "a${b", "a\tb"} {
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
// sides are per-rune maps over the admitted set. discovery.EscapeAddress
// rewrites three characters and copies the rest. addrs' toHCLQuotedString is
// per-rune too, with one lookahead - it doubles a "$" or "%" that precedes a
// "{" - and neither "$", "%" nor "{" is in the admitted set, so no admitted
// key can contain the pair and the lookahead can never fire. Every remaining
// interaction is between one input rune and one output rune, which is what
// this sweeps.
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

		stamped := prefix + key
		declared := discovery.EscapeAddress(forEachInstanceAddr(key))
		if stamped != declared {
			t.Fatalf("U+%04X (%q) is admitted but stamps %q while the declared address escapes to %q", r, key, stamped, declared)
		}
		if !discovery.ValidMarkerAddress(stamped) {
			t.Fatalf("U+%04X (%q) is admitted but produces the unreadable marker %q", r, key, stamped)
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
