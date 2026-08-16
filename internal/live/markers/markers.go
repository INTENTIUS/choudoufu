// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markers is live/MARKERS.md in code: the ownership tag keys,
// the escaping rule that lets a resource address live in a tag value, and the
// reading of those tags off a live object.
//
// It is a leaf package with no stateless-mode dependencies on purpose. The
// marker vocabulary is the one integration surface the whole fork - and
// anything outside it that honors the spec - agrees on, so every package that
// writes a marker (stamp), reads one (discovery, projection) or rewrites one
// (mv) has to mean the same thing by it, and the only way to guarantee that is
// one implementation they all import. It used to live in discovery, which
// worked until the projection needed to verify ownership too and could not
// import the package whose own test builds a projection.
package markers

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markerkey"
)

// The three marker tag keys, from live/MARKERS.md. They are the entire
// integration surface between this package and anything else that manages
// resources in a stateless estate.
const (
	// TagEstate names the estate that owns the resource.
	TagEstate = "tofu-estate"

	// TagAddress carries the resource's canonical config address, escaped.
	TagAddress = "tofu-address"

	// TagSlot carries a count instance's stable slot. Phase 3 assigns and
	// consumes it; this package only reads it to tell a plain collision
	// apart from a set of count instances that predate slot markers.
	TagSlot = "tofu-slot"
)

// MaxTagValue is the AWS hard limit on a tag value, in Unicode characters.
// A single marker tag - tofu-address itself, or one continuation tag - can
// carry no more than this.
const MaxTagValue = 256

// MaxContinuations is the highest number of tag values one tofu-address may
// span (issue #71's k). tofu-address carries the first MaxTagValue
// characters of the escaped address; tofu-address-2 through
// tofu-address-<MaxContinuations> carry the rest, in order. See
// live/MARKERS.md, "tofu-address continuation tags".
const MaxContinuations = 4

// MaxAddressLen is the longest escaped address a resource's markers may
// carry in total, across tofu-address and every continuation tag. An
// address longer than this is refused at lint time (RuleOverlongAddress) -
// the same refusal a value over a single MaxTagValue got before
// continuation tags existed, just at a wider ceiling.
const MaxAddressLen = MaxTagValue * MaxContinuations

// ContinuationTag names the n-th continuation tag key, for n in
// [2, MaxContinuations]. n=1 is TagAddress itself, which has no
// continuation form; a caller that wants "the key holding chunk i" for any
// 0-based i, including 0, wants [AddressTagKey] instead.
func ContinuationTag(n int) string {
	return fmt.Sprintf("%s-%d", TagAddress, n)
}

// AddressTagKey names the tag key that carries the i-th (0-based) chunk of
// a split tofu-address value: TagAddress for i=0, the (i+1)-th continuation
// tag otherwise. It is the indexing [SplitAddress] and [GatherAddress]
// agree on.
func AddressTagKey(i int) string {
	if i == 0 {
		return TagAddress
	}
	return ContinuationTag(i + 1)
}

// SplitAddress divides an escaped address into the ordered chunks its
// markers would carry: chunk 0 is the TagAddress value, chunk 1 is
// tofu-address-2, and so on ([AddressTagKey] names each chunk's tag key). A
// value that fits in one tag returns a single chunk, unchanged - the common
// case, and the only one before continuation tags existed. It never returns
// more than MaxContinuations chunks; a caller with a longer value has one
// lint's RuleOverlongAddress already refuses to admit.
func SplitAddress(escaped string) []string {
	runes := []rune(escaped)
	if len(runes) <= MaxTagValue {
		return []string{escaped}
	}
	chunks := make([]string, 0, MaxContinuations)
	for len(runes) > 0 && len(chunks) < MaxContinuations {
		n := MaxTagValue
		if n > len(runes) {
			n = len(runes)
		}
		chunks = append(chunks, string(runes[:n]))
		runes = runes[n:]
	}
	return chunks
}

// GatherAddress reads a marker's tofu-address value back off a tag map,
// concatenating any continuation tags (tofu-address-2, tofu-address-3, ...)
// in order. It is the read-side twin of [SplitAddress]: a value SplitAddress
// wrote across several tags comes back as the one string it started from. A
// tag map with no continuation tags at all - every marker before issue #71,
// and every marker whose address fits in one tag today - reads back exactly
// as tags[TagAddress] always did.
//
// The second return is true when the continuation chain has a gap: some
// tofu-address-n is present while tofu-address-(n-1) (or tofu-address
// itself, for n=2) is missing. That can only happen by hand-editing tags -
// deleting one continuation out of a set this package always writes as a
// whole - and it is reported the same way any other malformed marker is:
// loud and named, rather than silently read as the address up to the gap.
// It is never set merely because tags[TagAddress] itself is absent; that is
// the ordinary "no marker at all" case every existing caller already
// handles by getting back an empty string.
func GatherAddress(tags map[string]string) (raw string, corrupt bool) {
	primary, ok := tags[TagAddress]
	if !ok {
		// tofu-address itself is missing. That is the ordinary "no marker at
		// all" case only if no continuation tag is present either; a
		// continuation tag surviving without the primary it continues is the
		// n=2 gap this function's doc comment names explicitly.
		for n := 2; n <= MaxContinuations; n++ {
			if _, present := tags[ContinuationTag(n)]; present {
				return "", true
			}
		}
		return "", false
	}

	var b strings.Builder
	b.WriteString(primary)

	n := 2
	for ; n <= MaxContinuations; n++ {
		v, present := tags[ContinuationTag(n)]
		if !present {
			break
		}
		b.WriteString(v)
	}
	for m := n + 1; m <= MaxContinuations; m++ {
		if _, present := tags[ContinuationTag(m)]; present {
			return "", true
		}
	}
	return b.String(), false
}

// estateNamePattern is the tofu-estate grammar from the marker spec:
// a lowercase ASCII letter followed by up to 127 letters, digits or hyphens.
var estateNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,127}$`)

// ValidEstateName reports whether s is a well-formed estate name.
func ValidEstateName(s string) bool { return estateNamePattern.MatchString(s) }

// escapedAddress is the shape an escaped address has: dot-separated
// identifiers, each optionally carrying an index introduced by ':', with at
// least two segments (a resource type and a name). It is deliberately loose
// about which segments are module names and which are the resource, because
// this is a well-formedness check on a tag value, not a parser: nothing in
// this package ever turns the value back into an address.
var escapedAddress = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(:[^.:]+)?(\.[A-Za-z_][A-Za-z0-9_-]*(:[^.:]+)?)+$`)

// EscapeKey applies the for_each-key half of the escaping rule to one raw
// instance key, in two layers, in this order:
//
//  1. [markerkey.Encode] (issue #210): carries any character outside the
//     raw AWS tag-value charset into it, so a key like `a(b)` becomes
//     representable at all. This layer's own doc comment has the full
//     escaping rule; the short version is that it is a no-op for a key that
//     was already inside the charset, except for a literal `+`, which it
//     always doubles.
//
//  2. The doubling substitution issue #178 introduced, applied to Encode's
//     output: [markerkey.Extras] admits "." and ":" in a key, and those are
//     also the two characters an escaped address uses to separate its own
//     segments, so a key carrying either has to be transformed, not
//     embedded raw, or it could not be split back out again.
//
//  1. every "@" becomes "@@"
//
//  2. every "." becomes "@d"
//
//  3. every ":" becomes "@c"
//
//     The order is load-bearing: doubling "@" first guarantees that every
//     "@" steps 2 and 3 introduce is never itself mistaken for one that
//     needs doubling, because step 1 has already run and will not run
//     again.
//
// Encode has to run first: its own output is guaranteed to stay inside the
// AWS-legal charset only because nothing downstream of it looks for "@",
// "." or ":" INSIDE what it produces the way step 2 does, and Encode never
// emits those three characters as part of an escape sequence (its own
// Introducer is "+", not any of them), so the two layers never collide.
// Any key [markerkey.Valid] admits round-trips through this and
// [UnescapeKey].
//
// The each.key-templated per-instance case is the one place this
// implementation is not the whole story: a for_each key needing Encode's
// help cannot be reproduced as a replace()-chain HCL expression the way the
// pre-#210 three-substitution rule could (Encode's hex escape is a function
// of each rune's own code point, which replace() cannot compute), so
// internal/live/stamp's addressExpr uses a different mechanism - a
// precomputed lookup table keyed by each.key - for a for_each block where at
// least one static key needs it, falling back to the old
// replace()-chain template (still exactly correct, since Encode is a no-op
// there) everywhere else. See internal/live/stamp/foreach_escape_test.go
// for the proof the two mechanisms agree with this function wherever both
// apply.
func EscapeKey(key string) string {
	return doubleAtDotColon(markerkey.Encode(key))
}

// doubleAtDotColon is step 2 of [EscapeKey] on its own: the "@" / "." / ":"
// doubling issue #178 introduced, with no [markerkey.Encode] step before it.
// It exists separately from EscapeKey for [pre210EscapeAddress], which needs
// exactly this half - not EscapeKey's - to reconstruct what a marker looked
// like in the one grammar window issue #210 can otherwise break
// compatibility with (see that function's doc comment).
func doubleAtDotColon(key string) string {
	key = strings.ReplaceAll(key, "@", "@@")
	key = strings.ReplaceAll(key, ".", "@d")
	key = strings.ReplaceAll(key, ":", "@c")
	return key
}

// UnescapeKey reverses [EscapeKey] for any value it produced: the "." / ":"
// / "@" doubling first, with a single left-to-right scan rather than three
// reverse substitutions ("@@", "@d" and "@c" have to be read as one unit
// each even where two of them sit back to back in the output - "@@"
// immediately followed by "@d" is one escaped "@" then one escaped "." -
// and a blind sequence of whole-string replacements cannot make that
// guarantee once the first pass has already introduced new "@" characters
// for the second to trip over), and then [markerkey.Decode] on the result,
// reversing Encode's own hex escaping (issue #210). The order is the exact
// reverse of EscapeKey's: EscapeKey runs Encode first and the doubling
// second, so undoing it runs the doubling's inverse first and Decode
// second.
//
// It never fails, on purpose. A "@" not immediately followed by "@", "d" or
// "c" cannot appear in anything [EscapeKey] produced, but it is exactly
// what a key legally written before issue #178 admitted "." and ":" looks
// like on the wire: "@" was already in [markerkey.Extras] and was never
// escaped before this issue, so an old marker's raw "@" is read back as
// itself, the same character it always was. That is a best-effort reading,
// not a guarantee both ways - a pre-#178 key that happened to contain the
// literal two-character sequence "@d", "@c" or "@@" is indistinguishable,
// in the tag text alone, from a post-#178 key whose escaping produced the
// same bytes, and there is no way to tell the two apart from the marker
// alone (see live/MARKERS.md, "for_each key migration"). Nothing that
// identifies or acts on a live resource depends on this function picking
// the right side of that coincidence: ownership is decided by
// [AddressMatches] comparing computed strings, never by decoding an
// observed one, and every caller of [UnescapeAddress] already treats its
// output as a label rather than a selector (see that function's own doc
// comment). The worst a coincidence like this can do is misdisplay a key in
// a message.
func UnescapeKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r != '@' || i+1 >= len(runes) {
			b.WriteRune(r)
			continue
		}
		switch runes[i+1] {
		case '@':
			b.WriteRune('@')
			i++
		case 'd':
			b.WriteRune('.')
			i++
		case 'c':
			b.WriteRune(':')
			i++
		default:
			// Not a recognized escape continuation: "@" is read as itself,
			// and the next rune is left for the following iteration, which
			// handles it exactly as it would anywhere else in the string.
			b.WriteRune('@')
		}
	}
	return markerkey.Decode(b.String())
}

// EscapeAddress applies the marker spec's escaping rule to an address:
// every "[...]" instance key becomes ":" followed by the key run through
// [EscapeKey], and any "]" or '"' outside a key (there should never be one)
// is dropped defensively rather than passed through. An address never
// legally contains "[" except to open an instance key - resource types,
// names and module names are HCL identifiers, which cannot contain it - so
// a plain rune scan tracking bracket state cannot mistake structural text
// for key content or the reverse.
//
// It is idempotent: an already-escaped value contains no "[", so escaping
// it again returns it unchanged without ever looking inside what used to be
// a key. That property is what lets [Discover] normalize an observed tag
// value with the same function it uses on a declared address, without ever
// decoding anything - and it is also why an already-escaped value written
// under the pre-#178 grammar normalizes to itself here exactly as it always
// did: this function only transforms text still inside a "[...]" pair, and
// a marker already on a live resource has none left.
func EscapeAddress(addr string) string {
	return escapeAddressWith(addr, EscapeKey)
}

// pre210EscapeAddress is what [EscapeAddress] computed after issue #178
// admitted "." and ":" into a for_each key but before issue #210 admitted
// anything outside the AWS-legal charset: the "@" / "." / ":" doubling
// applied directly, with no [markerkey.Encode] step in front of it. It
// exists only for [AddressMatches] - the same reason [LegacyEscapeAddress]
// exists - because [LegacyEscapeAddress] does not cover this window on its
// own: it applies NO key-level escaping at all, which happens to reproduce
// a pre-#178 marker correctly (nothing was ever escaped then) but does NOT
// reproduce a marker written after #178 landed and before #210 did, for any
// key combining "+" with "." / ":" / "@" - "a.b+c" stamped as "a@db+c" in
// that window, which pre210EscapeAddress reconstructs and LegacyEscapeAddress
// does not ("a.b+c", raw dot and all). A key containing only "+" and letters
// otherwise happens to produce the same string under all three functions,
// which is why the gap this closes is narrow: it is exactly the combination
// this doc comment names, not "+" on its own. See live/MARKERS.md, "for_each
// key migration".
func pre210EscapeAddress(addr string) string {
	return escapeAddressWith(addr, doubleAtDotColon)
}

// escapeAddressWith is [EscapeAddress]'s address-level escaping - every
// "[...]" instance key becomes ":" followed by the key run through
// escapeKey, and any "]" or '"' outside a key (there should never be one)
// is dropped defensively rather than passed through - parameterized over
// which key-escaping function to apply, so [EscapeAddress] and
// [pre210EscapeAddress] share the one bracket-scanning implementation
// rather than drifting as two copies of it. An address never legally
// contains "[" except to open an instance key - resource types, names and
// module names are HCL identifiers, which cannot contain it - so a plain
// rune scan tracking bracket state cannot mistake structural text for key
// content or the reverse.
//
// It is idempotent for any escapeKey: an already-escaped value contains no
// "[", so escaping it again returns it unchanged without ever looking
// inside what used to be a key. That property is what lets [Discover]
// normalize an observed tag value with the same function it uses on a
// declared address, without ever decoding anything - and it is also why an
// already-escaped value written under an older grammar normalizes to
// itself here exactly as it always did: this function only transforms text
// still inside a "[...]" pair, and a marker already on a live resource has
// none left.
func escapeAddressWith(addr string, escapeKey func(string) string) string {
	if !strings.ContainsRune(addr, '[') {
		return addr
	}
	var b strings.Builder
	b.Grow(len(addr))
	runes := []rune(addr)
	for i := 0; i < len(runes); {
		r := runes[i]
		if r != '[' {
			if r != ']' && r != '"' {
				b.WriteRune(r)
			}
			i++
			continue
		}
		j := i + 1
		for j < len(runes) && runes[j] != ']' {
			j++
		}
		inner := strings.Trim(string(runes[i+1:j]), `"`)
		b.WriteRune(':')
		b.WriteString(escapeKey(inner))
		if j < len(runes) {
			j++ // step past the ']'
		}
		i = j
	}
	return b.String()
}

// LegacyEscapeAddress is what [EscapeAddress] computed before issue #178
// admitted "." and ":" into a for_each key: "[" becomes ":", and "]" and
// '"' are dropped, with nothing inside an instance key otherwise
// transformed. It exists only for [AddressMatches], so that a marker a
// prior run wrote for a key containing "@" - already legal, and never
// escaped before this issue - is still recognized as naming the instance it
// always named, even though a fresh stamp of the same key now writes
// something different. See live/MARKERS.md, "for_each key migration".
func LegacyEscapeAddress(addr string) string {
	if !strings.ContainsAny(addr, `[]"`) {
		return addr
	}
	var b strings.Builder
	b.Grow(len(addr))
	for _, r := range addr {
		switch r {
		case '[':
			b.WriteRune(':')
		case ']', '"':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// AddressMatches reports whether observed - an already-escaped marker value
// read off a live resource, never decoded - names the same instance as
// declared, the address before escaping (addr.String()). It tries three
// escapings of declared, in order - [EscapeAddress] (current), then
// [pre210EscapeAddress] (issue #178's grammar without issue #210's), then
// [LegacyEscapeAddress] (pre-#178, no key escaping at all) - so a marker
// written under any of the three grammars this fork has ever stamped with
// is still recognized, even for a key whose current escaping differs from
// what a prior run wrote. That is only possible for a key containing "@"
// (escaped differently by the pre-#178 and current grammars) or "+"
// (escaped differently by the pre-#210 and current grammars, and by the
// pre-#178 and current grammars whenever the same key also has a "." or
// ":" for #178's own grammar to touch) - see live/MARKERS.md, "for_each key
// migration", for the accounting. Every declared-vs-observed comparison
// that decides ownership - "is this live resource the one this instance
// address names" - should go through this rather than a bare EscapeAddress
// equality, or a resource stamped under an older grammar reads as unowned
// under the new code.
//
// Before trusting a fallback grammar (pre210 or Legacy), it checks whether
// observed is itself a well-formed CURRENT marker for some address - it
// round-trips through [UnescapeAddress] and, re-escaped with [EscapeAddress],
// reproduces observed exactly. When it is, that other address is the only
// one observed can name: the two older grammars are strictly less specific
// than the current one (neither escapes "@", "." or ":" the way
// [EscapeKey] does), so a string that is unambiguously canonical for one
// address can also coincide with an older-grammar encoding of a completely
// different address with no exotic characters at all - two ordinary
// for_each string keys in the same resource block, one like "a.b" and one
// like "a@db", collide exactly this way (issue #225). Preferring the
// canonical reading is what a live marker's own grammar promises: the
// current escaping is the one this fork writes today, so a value that
// parses as one address under it is that address's marker, full stop,
// never a different address's leftover from a retired grammar. This check
// can only fire once the canonical check just above it has already failed,
// which proves the address it finds is not declared - so it is always safe
// to reject the fallback match outright rather than compare the two
// addresses.
//
// This does not weaken the fallback grammars for the marker they were each
// added to widen: a genuine pre-#178 marker for a key containing "." or
// ":" fails [UnescapeAddress] outright (that function refuses a key with an
// un-escaped "." or ":" rather than guess), so it never reaches this check;
// a genuine pre-#178 or pre-#210 marker for a key containing only "@" or
// "+" round-trips back to the SAME address it was written for (its own
// re-escaping under the current grammar necessarily differs, since that
// difference is the only reason the fallback exists), so this check does
// not fire for it either. It fires only when the fallback interpretation
// and a genuine current-grammar interpretation disagree about which address
// owns the string - exactly the case a fallback must lose.
func AddressMatches(observed, declared string) bool {
	if observed == "" {
		return false
	}
	if observed == EscapeAddress(declared) {
		return true
	}
	if inst, ok := UnescapeAddress(observed); ok && EscapeAddress(inst.String()) == observed {
		// observed is somebody's genuine current-grammar marker - and, by
		// the canonical check above having already failed, not declared's.
		// No older grammar gets to override that.
		return false
	}
	if observed == pre210EscapeAddress(declared) {
		return true
	}
	return observed == LegacyEscapeAddress(declared)
}

// ValidMarkerAddress reports whether an escaped address is well-formed
// enough to be compared. A value that is not is the marker spec's
// "unparseable tofu-address": malformed, and a named error rather than a
// resource treated as unowned.
//
// The length bound is MaxAddressLen, not MaxTagValue: every caller hands
// this the logical address - the declared value before splitting, or the
// value [GatherAddress] already concatenated back together - never one raw
// tag's worth on its own, so the wider continuation-tag budget is the right
// ceiling here.
func ValidMarkerAddress(escaped string) bool {
	if escaped == "" || len([]rune(escaped)) > MaxAddressLen {
		return false
	}
	return escapedAddress.MatchString(escaped)
}

// UnescapeAddress turns an escaped marker value back into a resource
// instance address, and reports whether it could.
//
// It never identifies or selects a live resource on its own - every caller
// uses its result as a label (removal planning's plan-printed address,
// markerTypeOf's best-effort type guess) after the resource itself was
// already found by its live import ID or by a direct marker comparison
// ([AddressMatches]). That is what lets the two remaining ambiguities below
// stay ambiguities instead of becoming refusals or, worse, silent
// mis-selections:
//
//   - An instance key of all digits could have been written as a count index
//     or as a quoted string key of the same digits. It is decoded as a count
//     index, which is the reading that is right in every estate a lint-clean
//     configuration can produce. A live resource whose declared instance
//     really is the string key would have bound during discovery, since the
//     comparison discovery makes is between two escaped values and those two
//     are the same string.
//   - A key containing "." or ":" decodes as whatever [UnescapeKey] makes of
//     it, which is exactly right for a key escaped under the current rule and
//     a best-effort, possibly wrong reading for a key written before issue
//     #178 that happened to contain "@d", "@c" or "@@" as literal bytes.
//     [UnescapeKey]'s own doc comment has the full reasoning; the label this
//     produces can be wrong in that one narrow, coincidental case, and
//     nothing downstream trusts it enough for that to matter.
//
// A key literally containing an un-escaped "." or ":" - impossible for
// anything [EscapeKey] produced, and equally impossible for a legal
// pre-#178 key, which could never contain either - is refused rather than
// guessed at: that shape means the value was never validly escaped in the
// first place, by either grammar.
//
// A root-module address is the two trailing segments: type, then
// name(+key). Anything before them has to be a run of "module", "<name>"
// pairs, each name optionally carrying its own key - the
// module.a.module.b["x"] prefix a static or for_each-keyed module call
// contributes to the address (issue #59: 59b for the unkeyed form, 59c for
// the keyed one) - or the value is refused rather than guessed at.
//
// A module step's key, unlike a resource instance's, is never ambiguous
// between a count index and a quoted string of the same digits: count on a
// module block is refused permanently (RuleChildModule; live/LIMITATIONS.md,
// "child-module"), so every module instance key this fork ever writes came
// from a for_each, which only ever produces string keys. A module step's
// key therefore always decodes as [addrs.StringKey], with no digit-string
// special case - unlike the trailing resource segment just below, where
// count and for_each really do collide on the wire.
func UnescapeAddress(escaped string) (addrs.AbsResourceInstance, bool) {
	var zero addrs.AbsResourceInstance
	if !ValidMarkerAddress(escaped) {
		return zero, false
	}

	parts := strings.Split(escaped, ".")
	total := len(parts)
	typeName := parts[total-2]
	name, key, hasKey := strings.Cut(parts[total-1], ":")
	if typeName == "" || name == "" {
		return zero, false
	}

	prefix := parts[:total-2]
	if len(prefix)%2 != 0 {
		// An odd number of leading segments cannot be "module", "<name>"
		// pairs.
		return zero, false
	}
	var modInst addrs.ModuleInstance
	for i := 0; i < len(prefix); i += 2 {
		if prefix[i] != "module" {
			return zero, false
		}
		modName, modKey, modHasKey := strings.Cut(prefix[i+1], ":")
		if modName == "" {
			return zero, false
		}
		step := addrs.ModuleInstanceStep{Name: modName, InstanceKey: addrs.NoKey}
		if modHasKey {
			if modKey == "" || strings.ContainsAny(modKey, ".:") {
				return zero, false
			}
			step.InstanceKey = addrs.StringKey(UnescapeKey(modKey))
		}
		modInst = append(modInst, step)
	}

	res := addrs.Resource{
		Mode: addrs.ManagedResourceMode,
		Type: typeName,
		Name: name,
	}
	instKey := addrs.NoKey
	if hasKey {
		if key == "" || strings.ContainsAny(key, ".:") {
			return zero, false
		}
		decoded := UnescapeKey(key)
		if n, err := strconv.Atoi(decoded); err == nil && decoded == strconv.Itoa(n) && n >= 0 {
			instKey = addrs.IntKey(n)
		} else {
			instKey = addrs.StringKey(decoded)
		}
	}
	return addrs.AbsResourceInstance{
		Module:   modInst,
		Resource: res.Instance(instKey),
	}, true
}

// TagsOf reads a resource object's ownership-relevant tags.
//
// Where the tags live is per-type knowledge, and for every type in the v0
// subset the answer is the same: EC2 resource objects carry a top-level
// "tags" attribute typed as a map of string, alongside the "tags_all"
// attribute that merges in the provider's default_tags. The plain "tags"
// map is what a marker is written to and read from; "tags_all" is the
// fallback for a provider configuration whose default_tags supply the
// markers, which is not how the estate fixture writes them but is a legal
// way to write them.
//
// The second return distinguishes "this object has no tags attribute at
// all" - an untaggable type, or a list result that came back without its
// object - from "the object is tagged with nothing".
func TagsOf(obj cty.Value) (map[string]string, bool) {
	if obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() {
		return nil, false
	}
	ty := obj.Type()
	if !ty.IsObjectType() {
		return nil, false
	}

	var found bool
	tags := make(map[string]string)
	for _, name := range []string{"tags_all", "tags"} {
		if !ty.HasAttribute(name) {
			continue
		}
		v := obj.GetAttr(name)
		if v.IsNull() || !v.IsKnown() {
			// The attribute exists, so the type is taggable; an object with
			// no tags set is a real answer.
			found = true
			continue
		}
		if !v.CanIterateElements() {
			continue
		}
		found = true
		for it := v.ElementIterator(); it.Next(); {
			k, val := it.Element()
			if k.Type() != cty.String || k.IsNull() || val.IsNull() || !val.IsKnown() || val.Type() != cty.String {
				continue
			}
			// "tags" is read second on purpose: an explicitly set tag wins
			// over the same key arriving through tags_all.
			tags[k.AsString()] = val.AsString()
		}
	}
	if !found {
		return nil, false
	}
	return tags, true
}

// Taggable reports whether a resource type can carry an ownership marker: a
// top-level "tags" attribute of a map type that configuration is allowed to
// set, whose keys the configuration is allowed to choose.
//
// Read from the schema, never from a list of type names. A list would be
// wrong the day a provider adds tags to a type, and it would be wrong in the
// other direction for every provider nobody thought about. The
// tags-as-nested-blocks shape some types use (an aws_autoscaling_group's tag
// blocks) is not this, and is deliberately not stamped: those blocks are not
// the tag map this package's spec describes.
//
// The last clause of the first sentence was missing until GitHub issue #243,
// and its absence was the difference between the question this predicate
// claims to answer and the one it actually answered. "Does this resource
// have an attribute named tags of type map(string)" and "can this resource
// carry a free-form key/value marker" coincide on AWS and come apart on
// GCP, where the identically-shaped attribute holds resource-manager tag
// bindings whose keys must name TagKey objects that already exist. Stamping
// one wrote a marker the API rejects, into a field several types document as
// forcing replacement when mutated. See [VocabularyRefusal] for how the
// two are told apart, which is still from the schema and still not from a
// list of type names.
//
// It lives here rather than in internal/live/stamp, where it was written,
// because internal/live/lint needs the same answer and a second copy of a
// fifteen-line predicate is a second answer waiting to happen. This package
// is the one both can import: it is the marker vocabulary, and "which types
// can carry a marker" is part of it.
func Taggable(block *configschema.Block) bool {
	_, ok := TagSurface(block)
	return ok
}

// TagSurface is [Taggable] with its reasoning, for a caller that has to
// explain a refusal rather than only make one. It returns the tag map's
// attribute schema when the type can carry a marker.
//
// [NotAMarkerSurface] turns the failing case into a sentence.
func TagSurface(block *configschema.Block) (*configschema.Attribute, bool) {
	if block == nil {
		return nil, false
	}
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return nil, false
	}
	if !attr.Optional && !attr.Required {
		// Computed-only: the provider owns the value and configuration
		// cannot set it.
		return nil, false
	}
	ty := attr.Type
	if !ty.IsMapType() {
		return nil, false
	}
	if et := ty.ElementType(); et != cty.String && et != cty.DynamicPseudoType {
		return nil, false
	}
	if _, refused := VocabularyRefusal(attr.Description); refused {
		return nil, false
	}
	return attr, true
}

// NotAMarkerSurface explains, for a resource type that cannot carry a
// marker, which of the two reasons applies: there is no settable tag map at
// all, or there is one and its keys are the provider's to choose rather than
// the configuration's.
//
// The distinction is worth a sentence because the two lead somewhere
// different. The first is a type identified some other way, or one this
// scheme cannot own. The second is a field that looks available and is not,
// and a message that says "no tags map this configuration can set" about a
// resource whose schema plainly has one sends the reader looking for a
// typo.
//
// The returned string is a clause, not a sentence: a caller fits it after
// naming the resource.
func NotAMarkerSurface(block *configschema.Block, resourceType string) string {
	if reason, refused := RefusedTagSurface(block); refused {
		return fmt.Sprintf(
			"%s has a tags map, but this fork's ownership markers cannot round-trip through it: %s. There is nowhere on this resource to carry a marker.",
			resourceType, reason)
	}
	return fmt.Sprintf(
		"%s has no tags map this configuration can set, so there is nowhere to carry an ownership marker.",
		resourceType)
}

// RefusedTagSurface reports whether this type has a settable tag map that
// the marker vocabulary cannot use, and why.
//
// It is the second of the two reasons [Taggable] can be false, separated out
// because a caller deciding how loudly to speak needs to tell them apart. A
// type with no tag surface at all is ordinary - hundreds of AWS resource
// types are identified by an argument rather than a marker and always have
// been - while a type whose tag surface exists and is unusable is a field
// the author can see in their own configuration and reasonably expects this
// fork to be using.
//
// The reason is a clause with no terminating punctuation, so a caller can
// set it in a sentence of its own.
func RefusedTagSurface(block *configschema.Block) (string, bool) {
	if block == nil {
		return "", false
	}
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return "", false
	}
	if !attr.Optional && !attr.Required {
		return "", false
	}
	if !attr.Type.IsMapType() {
		return "", false
	}
	if et := attr.Type.ElementType(); et != cty.String && et != cty.DynamicPseudoType {
		return "", false
	}
	return VocabularyRefusal(attr.Description)
}
