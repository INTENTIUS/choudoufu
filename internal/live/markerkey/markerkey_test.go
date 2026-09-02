// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markerkey

import (
	"math/rand"
	"strings"
	"testing"
	"unicode"
)

// ---------------------------------------------------------------------------
// Issue #210: Encode/Decode, the out-of-charset for_each key escaping.
// ---------------------------------------------------------------------------

// TestEncode_identityOnLegalKeys is requirement 4, pinned: every key already
// inside the pre-#210 admitted set (legalRune, the raw AWS tag-value
// charset) encodes to itself, unchanged, with the one documented exception -
// a key containing a literal Introducer - called out explicitly rather than
// swept into the general case. A future change that widens or narrows
// Encode's behavior on an already-legal key fails here first, loudly, which
// is the point: every marker this fork has ever written for a key without
// "+" in it depends on this holding.
func TestEncode_identityOnLegalKeys(t *testing.T) {
	unaffected := []string{
		"a", "web", "Web", "0", "007", "eu-west-1a", "team_one", "eu/west",
		"size=1", "at@sign", "with space", "été", "日本",
		"a-very-long-but-perfectly-ordinary-key",
		"a.b", "2001:db8::/64", "user@example.com", "a:b.c@d",
		"", // Encode itself is total; InvalidRune is what refuses "" elsewhere
	}
	for _, key := range unaffected {
		if got := Encode(key); got != key {
			t.Errorf("Encode(%q) = %q, want %q unchanged (no Introducer, all-legalRune)", key, got, key)
		}
	}

	// The one documented exception: a literal Introducer always doubles,
	// even though "+" was already legal (in Extras) before issue #210.
	affected := []struct{ in, want string }{
		{"plus+one", "plus++one"},
		{"+", "++"},
		{"++", "++++"},
		{"a+b+c", "a++b++c"},
	}
	for _, c := range affected {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q (Introducer always doubles)", c.in, got, c.want)
		}
	}
}

// TestEncode_knownCases pins the hex form itself: an out-of-charset rune
// becomes Introducer followed by six uppercase hex digits, and a run of
// several is each escaped independently.
func TestEncode_knownCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"(", "+000028"},
		{")", "+000029"},
		{"a(b)", "a+000028b+000029"},
		{"日本", "日本"}, // CJK ideographs are unicode.IsLetter: legalRune
		{"!", "+000021"},
		{";", "+00003B"},
		{"a;b", "a+00003Bb"},
		{"€", "+0020AC"}, // BMP symbol, not a letter/digit
		{"😀", "+01F600"}, // astral-plane rune, exercises the full 6-digit width
	}
	for _, c := range cases {
		if got := Encode(c.in); got != c.want {
			t.Errorf("Encode(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEncode_outputStaysInCharset is requirement 3: for every key [Valid]
// admits, Encode's output contains nothing but letters, digits, space and
// Extras - the AWS-legal tag-value charset - regardless of what the input
// contained. It is swept over the full Unicode range one rune at a time
// (every admitted single-rune key), the same exhaustiveness
// internal/live/stamp's TestStamp_noAdmittedRuneMisbinds already uses for
// the adjacent claim, plus a randomized sweep over multi-rune combinations
// so the property is checked on strings Encode's doubling and hex-escaping
// actually have to interact across, not just in isolation.
func TestEncode_outputStaysInCharset(t *testing.T) {
	checkLegal := func(t *testing.T, key string) {
		t.Helper()
		encoded := Encode(key)
		for _, r := range encoded {
			if !legalRune(r) {
				t.Fatalf("Encode(%q) = %q contains %q, which is outside the AWS-legal charset", key, encoded, string(r))
			}
		}
	}

	admitted := 0
	for r := rune(0); r <= 0x10FFFF; r++ {
		if r >= 0xD800 && r <= 0xDFFF {
			continue // surrogate halves are not characters
		}
		key := string(r)
		if !Valid(key) {
			continue
		}
		admitted++
		checkLegal(t, key)
	}
	if admitted < 1000 {
		t.Fatalf("only %d runes admitted; the sweep is not exercising the rule", admitted)
	}

	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		checkLegal(t, randomKey(rng))
	}
}

// TestEncode_Decode_roundTrips is requirement 1: decode(encode(k)) == k for
// every key, generated rather than sampled by hand, including ones
// containing Introducer itself (doubled, or adjacent to what looks like a
// hex escape) and ones mixing legal and illegal runes in the same string.
func TestEncode_Decode_roundTrips(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 50000; i++ {
		key := randomKey(rng)
		encoded := Encode(key)
		back := Decode(encoded)
		if back != key {
			t.Fatalf("key %q: Encode -> %q -> Decode -> %q, want %q", key, encoded, back, key)
		}
	}

	// Adversarial: keys built to look like they contain a hex escape
	// already, so a naive decoder that does not require Introducer to be
	// doubled before trusting a literal "+" would misparse them.
	adversarial := []string{
		"+000028", "a+000028b", "++000028", "+00", "+0000000",
		"+ABCDEF", "+abcdef", "+GGGGGG", "a+b", "+", "++", "+++",
		"++++++", "a++++b",
	}
	for _, key := range adversarial {
		encoded := Encode(key)
		back := Decode(encoded)
		if back != key {
			t.Errorf("adversarial key %q: Encode -> %q -> Decode -> %q, want %q", key, encoded, back, key)
		}
	}
}

// TestEncode_injective is requirement 2, the one that writes a wrong marker
// if it fails: two different admitted keys must never produce the same
// Encode output, or two different for_each instances collide on one marker.
// Encode is a per-rune transducer whose three output shapes - a plain rune,
// a doubled Introducer, and Introducer-plus-six-hex-digits - have distinct
// lengths (1, 2, 7) and Introducer never appears as a "plain rune" output
// (that case is routed to the doubling branch) nor inside a hex block (hex
// digits are never Introducer), so the concatenation is uniquely
// parseable: [Decode] recovers exactly one rune sequence from any string
// Encode could have produced, which is what makes Encode's inverse a
// function at all. This test checks the property empirically, over a large
// random sample plus the near-miss adversarial cases most likely to collide
// if that per-rune argument were wrong.
func TestEncode_injective(t *testing.T) {
	seen := make(map[string]string)
	check := func(key string) {
		encoded := Encode(key)
		if prior, ok := seen[encoded]; ok && prior != key {
			t.Fatalf("collision: Encode(%q) = Encode(%q) = %q", prior, key, encoded)
		}
		seen[encoded] = key
	}

	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 50000; i++ {
		check(randomKey(rng))
	}

	// Near misses: pairs that would collide under a scheme that doubled
	// Introducer AFTER hex-escaping (wrong order) or that used a
	// variable-width hex encoding.
	nearMisses := []string{
		"+", "++", "(", ")", "+000028", "a+000028", "a(",
		"++000028", "+00002800", "+0000280", " +000028",
	}
	for _, key := range nearMisses {
		check(key)
	}
}

// TestEncode_backwardCompatIntroducerException documents, in an executable
// form, exactly what requirement 4 concedes: keys with a literal Introducer
// are the one already-legal shape that does NOT survive unchanged. Every
// other single already-legal rune round-trips as itself.
func TestEncode_backwardCompatIntroducerException(t *testing.T) {
	for r := rune(0); r < unicode.MaxASCII; r++ {
		if !legalRune(r) {
			continue
		}
		key := string(r)
		got := Encode(key)
		if r == Introducer {
			if got == key {
				t.Errorf("Encode(%q) = %q, want it CHANGED (the documented Introducer exception)", key, got)
			}
			continue
		}
		if got != key {
			t.Errorf("Encode(%q) = %q, want %q unchanged", key, got, key)
		}
	}
}

// randomKey builds a short random string over a mix of legalRune characters,
// excluded characters, other printable runes Encode must hex-escape, and
// Introducer itself - so both the "no-op" and "encode" paths, and their
// interaction, are exercised.
func randomKey(rng *rand.Rand) string {
	pool := []rune("abcZZ019 +-=_/@.:()!;#*,€" + string(Introducer))
	n := rng.Intn(12)
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteRune(pool[rng.Intn(len(pool))])
	}
	return b.String()
}
