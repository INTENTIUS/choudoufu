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

// EscapeAddress applies the marker spec's escaping rule to an address:
// every '[' becomes ':', and every ']' and '"' is dropped.
//
// It is idempotent: an already-escaped value contains none of the three
// characters it rewrites, so escaping it again returns it unchanged. That
// property is what lets [Discover] normalize an observed tag value with the
// same function it uses on a declared address, without ever decoding
// anything.
func EscapeAddress(addr string) string {
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
// The escaping rule in live/MARKERS.md is lossy in two ways, and this
// function is honest about both rather than guessing through them:
//
//   - A key carrying a '.' or a ':' cannot be located, because those are the
//     two characters that separate the segments of an escaped address. Such a
//     value is refused.
//   - An instance key of all digits could have been written as a count index
//     or as a quoted string key of the same digits. It is decoded as a count
//     index, which is the reading that is right in every estate a lint-clean
//     configuration can produce, and which cannot mislead anything that acts
//     on the result: the only consumer is removal planning, which identifies
//     the resource to destroy by its live import ID and uses this address as
//     the label the plan prints. A live resource whose declared instance
//     really is the string key would have bound in the scan rather than
//     reaching here, because the comparison discovery makes is between two
//     escaped values and those two are the same string.
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
			step.InstanceKey = addrs.StringKey(modKey)
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
		if n, err := strconv.Atoi(key); err == nil && key == strconv.Itoa(n) && n >= 0 {
			instKey = addrs.IntKey(n)
		} else {
			instKey = addrs.StringKey(key)
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
// set.
//
// Read from the schema, never from a list of type names. A list would be
// wrong the day a provider adds tags to a type, and it would be wrong in the
// other direction for every provider nobody thought about. The
// tags-as-nested-blocks shape some types use (an aws_autoscaling_group's tag
// blocks) is not this, and is deliberately not stamped: those blocks are not
// the tag map this package's spec describes.
//
// It lives here rather than in internal/live/stamp, where it was written,
// because internal/live/lint needs the same answer and a second copy of a
// fifteen-line predicate is a second answer waiting to happen. This package
// is the one both can import: it is the marker vocabulary, and "which types
// can carry a marker" is part of it.
func Taggable(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return false
	}
	if !attr.Optional && !attr.Required {
		// Computed-only: the provider owns the value and configuration
		// cannot set it.
		return false
	}
	ty := attr.Type
	if !ty.IsMapType() {
		return false
	}
	et := ty.ElementType()
	return et == cty.String || et == cty.DynamicPseudoType
}
