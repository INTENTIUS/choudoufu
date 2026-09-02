// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markerkey holds the for_each instance key rule that lint and
// identity both enforce.
//
// The rule has two enforcement points - lint checks the for_each
// expressions it can evaluate from the configuration text, and identity
// resolution is where an expression lint declined to guess at has actually
// been evaluated - and it has to be one rule rather than two, or the two
// points drift. Neither package may import the other (lint calls into
// identity for the schema fallback; identity calling back into lint would
// be a cycle), so the rule itself lives here, one level below both.
package markerkey

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Extras is the punctuation a for_each instance key may contain without
// [Encode]'s help: the full AWS tag value set from live/MARKERS.md,
// "+ - = . _ : / @". "." and ":" are the two characters an escaped address
// uses to separate its own segments, so admitting them in a key depends on
// the key-escaping rule in internal/live/markers
// ([markers.EscapeKey]/[markers.UnescapeKey]) rather than on passing a key
// through unmodified the way the rest of this set does (issue #178). This
// package only says which characters a key may contain; it is markers, not
// this package, that says how "." and ":" survive the trip.
const Extras = "+-=_/@.:"

// Introducer is the rune [Encode] uses to introduce an escaped, out-of-
// charset rune, and to mark a doubled instance of itself (issue #210).
//
// It is one of the eight characters in Extras - already legal in a marker
// on its own, so introducing it costs nothing new in the AWS-legal output
// charset - chosen over the other seven because it is the one a for_each
// key realistically drawn from an infrastructure identifier, a DNS name, an
// ARN, or a tag-shaped label is least likely to already contain: this
// package's own tests and internal/live/stamp's already exercise real
// examples of "-", "_", ".", ":", "/" and "@" in ordinary keys ("eu-west-1a",
// "team_one", "alice.smith", "2001:db8::/64", "eu/west", "at@sign"), while
// "+" shows up in practice mostly inside an email address's plus-addressing
// or a base64 fragment - both rare in a for_each key, which is usually built
// from a resource name, an availability zone, a CIDR, or similar. No choice
// among the eight is free of this cost (see Encode's doc comment for what
// happens to a key that already contains it); "+" is simply the cheapest.
const Introducer = '+'

// Excluded is the set of printable, non-space runes [Encode] cannot safely
// carry into a marker even though every other printable rune is safe: not
// because AWS's tag-value charset objects to them (Encode's whole point is
// that AWS's opinion stops mattering once a rune is escaped), but because
// each one collides with a DIFFERENT escaping rule this package does not
// own and cannot change.
//
//   - `"` and `\` are two of the six cases addrs' toHCLQuotedString
//     backslash-escapes when OpenTofu itself renders a for_each instance
//     key into the "declared" side of an address comparison (the other
//     four - tab, CR, LF, and every other non-printable rune - are covered
//     by the isPrint half of [encodable], not this set). A key containing
//     either would decode to a DIFFERENT string on the declared side (built
//     from the backslash-escaped rendering) than on the stamped side (built
//     from the raw key), because Encode runs after toHCLQuotedString has
//     already rewritten the text it sees, not before.
//   - `$` and `%` are the other two: toHCLQuotedString doubles either one
//     when it immediately precedes `{`, a transformation with no per-rune
//     inverse Encode could apply to just one occurrence of the pair without
//     knowing what follows it.
//   - `[` and `]` are the delimiters internal/live/markers' own
//     EscapeAddress scans for to find an instance key's boundaries inside a
//     full address string, before Encode ever sees the key's content. A raw
//     `[` or `]` inside the key corrupts that scan itself - EscapeAddress
//     finds the wrong closing bracket - so no amount of escaping applied
//     downstream of the scan can repair it.
//
// Every one of these six is already refused today (none is in Extras), so
// excluding them here changes nothing for a key that already passes; it is
// the boundary of what widens, not a new restriction.
const Excluded = "\"\\$%[]"

// legalRune reports whether r is already representable in a marker without
// Encode's help: a letter, a digit, space, or one of Extras. This is the
// set the marker grammar admitted before issue #210, and Encode leaves
// every rune in it untouched except Introducer itself (see Encode's doc
// comment).
func legalRune(r rune) bool {
	switch {
	case unicode.IsLetter(r), unicode.IsDigit(r):
		return true
	case r == ' ':
		return true
	default:
		return strings.ContainsRune(Extras, r)
	}
}

// encodable reports whether r is a rune [Encode] can safely carry into the
// AWS-legal charset: printable, and not one of the six characters in
// Excluded that collide with a rule outside this package's control. Every
// legalRune is encodable (Extras, letters, digits and space are all
// printable and none of them is in Excluded), so this is the wider set:
// admitted() in every doc comment in this package is a synonym for it.
func encodable(r rune) bool {
	return unicode.IsPrint(r) && !strings.ContainsRune(Excluded, r)
}

// Valid reports whether a for_each instance key survives the round trip
// through a tofu-address marker: escapable to a marker value via [Encode],
// and unescapable back to the address it came from.
func Valid(key string) bool {
	_, bad := InvalidRune(key)
	return !bad
}

// InvalidRune returns the first character of key that puts it outside the
// set [Encode] can represent, and whether there was one. An empty key is
// invalid with a zero rune as its offender: nothing about a character is
// wrong with it, but it is still unrepresentable, because an escaped
// address ending in a bare ":" does not parse as a marker.
//
// Before issue #210 this set was legalRune: a key needed every character
// already inside the raw AWS tag-value charset, because nothing carried
// anything else across the trip. [Encode] does that carrying now, so the
// boundary widened to encodable - every printable rune except the six
// excluded lists, each excluded for a documented, structural reason rather
// than because AWS's own charset objects to it.
func InvalidRune(key string) (rune, bool) {
	if key == "" {
		return 0, true
	}
	for _, r := range key {
		if !encodable(r) {
			return r, true
		}
	}
	return 0, false
}

// NeedsEncode reports whether key requires [Encode]'s help to become a
// marker - true for exactly the keys [Encode] does not leave byte-for-byte
// unchanged: one containing a rune outside legalRune, or a literal
// Introducer (which Encode always doubles even though it is already
// legalRune on its own).
//
// This is a narrower question than [Valid]. Valid asks whether a key can
// become a marker AT ALL; NeedsEncode asks whether doing so depends on
// Encode specifically having run. The two enforcement points this package
// serves diverge exactly here: identity resolution's own evaluator can see
// a for_each key no matter how it was computed, but stamp only reaches
// Encode's actual hex-escaping through a precomputed lookup table it can
// build solely from a for_each expression it can also evaluate statically
// (issue #210's forEachNeedsKeyLookup); a for_each stamp cannot read
// statically at all (rooted at a data source, another resource, or
// anything else only known once the cloud is read) falls back to a
// narrower template that reproduces only legalRune's own doubling rule,
// with no Encode step. A key that needs Encode's help is stamped wrong
// there, silently, which is why identity checks this set as a hard refusal
// for exactly that shape of for_each (issue #227) rather than only
// [Valid]'s wider one.
func NeedsEncode(key string) bool {
	return Encode(key) != key
}

// FirstEncodeRune returns the first rune in key that [NeedsEncode] would
// flag - the first one outside legalRune, or the first literal Introducer -
// and whether it found one. It is [NeedsEncode]'s sibling the way
// [InvalidRune] is [Valid]'s: the same boundary, but naming the offending
// character for a diagnostic instead of collapsing it to a bool.
func FirstEncodeRune(key string) (rune, bool) {
	for _, r := range key {
		if r == Introducer || !legalRune(r) {
			return r, true
		}
	}
	return 0, false
}

// DescribeRune renders an offending character for a diagnostic: the
// character itself when it is printable, and its code point either way, so
// a key that failed on a zero-width or control character says something
// useful rather than printing nothing.
func DescribeRune(r rune) string {
	if r == 0 {
		return "nothing at all (an empty key)"
	}
	if unicode.IsPrint(r) && !unicode.IsSpace(r) {
		return fmt.Sprintf("%q (U+%04X)", string(r), r)
	}
	return fmt.Sprintf("U+%04X", r)
}

// Encode makes a for_each key representable inside the AWS tag-value
// charset, so that every key [Valid] admits - not just the narrower
// legalRune set the marker grammar accepted before issue #210 - can become
// a marker. It is the layer internal/live/markers' EscapeKey applies before
// its own "." / ":" / "@" doubling, never after, so the two escaping layers
// never see each other's output; EscapeKey's doc comment names the exact
// composition.
//
// Two rules, applied per rune, left to right:
//
//  1. Introducer doubles: every literal Introducer becomes two Introducers.
//  2. Anything else legalRune does not already admit becomes Introducer
//     followed by its Unicode code point as six uppercase hex digits (six
//     because the highest legal code point, U+10FFFF, needs exactly six -
//     "10FFFF" - so the width never varies and [Decode] never has to guess
//     how many digits to consume).
//
// Every other rune - every legalRune except Introducer - passes through
// unchanged. That is requirement 4 of issue #210: a key already inside the
// pre-#210 admitted set encodes to itself, UNCHANGED, with one narrow,
// explicit exception. A key that legitimately contains a literal Introducer
// character does NOT encode to itself: "plus+one" becomes "plus++one", the
// same doubling issue #178 already accepted for "@" one layer up, for the
// same reason - Introducer has to be legal AWS-tag-value punctuation for
// its own escape sequences to survive the trip, and every character that
// satisfies that was already in real use by something. No choice removes
// this cost; Introducer's own doc comment is why "+" is the cheapest
// candidate. A key without a literal Introducer character is unaffected,
// which is every admitted key before issue #210 except the ones containing
// "+" - see live/MARKERS.md, "for_each key escaping", for the full
// accounting, mirroring "for_each key migration"'s accounting of the same
// cost when issue #178 spent it on "@".
//
// Encode never fails: every rune legalRune does not already admit is
// encodable() by construction whenever the caller has already checked
// [Valid] (which is InvalidRune's job to enforce before a key reaches
// here), so nothing this function is actually asked to encode is outside
// what it can represent.
func Encode(key string) string {
	needsEncoding := false
	for _, r := range key {
		if r == Introducer || !legalRune(r) {
			needsEncoding = true
			break
		}
	}
	if !needsEncoding {
		return key
	}

	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch {
		case r == Introducer:
			b.WriteRune(Introducer)
			b.WriteRune(Introducer)
		case legalRune(r):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%c%06X", Introducer, r)
		}
	}
	return b.String()
}

// Decode reverses [Encode]: two Introducers become one literal Introducer,
// Introducer followed by six hex digits becomes the rune those digits name,
// and everything else is copied unchanged. It is a single left-to-right
// scan for the same reason internal/live/markers' UnescapeKey is one: two
// escape sequences sitting back to back (an encoded rune immediately
// followed by a doubled Introducer, say) have to be read as two fixed-width
// units, not reprocessed as if the first's output could be the second's
// input.
//
// It never fails, on purpose, mirroring UnescapeKey's own defensive
// default: an Introducer not immediately followed by another Introducer or
// by six hex digits cannot appear in anything Encode produced, so it is
// read as a literal Introducer and the scan continues from the very next
// rune, rather than the function erroring on input that should never occur
// in practice.
func Decode(s string) string {
	runes := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != Introducer {
			b.WriteRune(r)
			continue
		}
		if i+1 < len(runes) && runes[i+1] == Introducer {
			b.WriteRune(Introducer)
			i++
			continue
		}
		if i+6 < len(runes) {
			if cp, err := strconv.ParseUint(string(runes[i+1:i+7]), 16, 32); err == nil {
				b.WriteRune(rune(cp))
				i += 6
				continue
			}
		}
		// Not a recognized escape continuation: Introducer is read as
		// itself, exactly as UnescapeKey reads an unrecognized "@".
		b.WriteRune(Introducer)
	}
	return b.String()
}
