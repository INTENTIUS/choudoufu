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
	"strings"
	"unicode"
)

// Extras is the punctuation a for_each instance key may contain, being the
// AWS tag value set from live/MARKERS.md minus the two escaped-address
// separators, "." and ":". Their absence is the whole rule: both are
// AWS-legal in a tag value, and both would produce a marker that cannot be
// split back into the address it came from.
const Extras = "+-=_/@"

// Valid reports whether a for_each instance key survives the round trip
// through a tofu-address marker: escapable to a marker value, and
// unescapable back to the address it came from.
func Valid(key string) bool {
	_, bad := InvalidRune(key)
	return !bad
}

// InvalidRune returns the first character of key that puts it outside the
// set, and whether there was one. An empty key is invalid with a zero rune
// as its offender: nothing about a character is wrong with it, but it is
// still unrepresentable, because an escaped address ending in a bare ":"
// does not parse as a marker.
func InvalidRune(key string) (rune, bool) {
	if key == "" {
		return 0, true
	}
	for _, r := range key {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == ' ':
		case strings.ContainsRune(Extras, r):
		default:
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
