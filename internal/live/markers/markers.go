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
	"regexp"
	"strconv"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
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
// An escaped address longer than this cannot be stored as a marker at all,
// so a value at the limit is suspicious enough to report as malformed
// rather than to compare.
const MaxTagValue = 256

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
func ValidMarkerAddress(escaped string) bool {
	if escaped == "" || len([]rune(escaped)) > MaxTagValue {
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
// pairs with no key of their own - the module.a.module.b prefix a static
// module call contributes to the address - or the value is refused rather
// than guessed at. A keyed module step ("module.a:x") is well-formed
// (escapedAddress does not distinguish module segments from the resource
// segment) but is not decoded: nothing this fork admits today writes one -
// phase 1 covers static module calls only, and a keyed module instance is
// phase 2's concern (issue #59, 59c) - so a value carrying one is refused
// the same way an unrecognized shape is, rather than silently accepted.
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
		modName := prefix[i+1]
		if modName == "" || strings.Contains(modName, ":") {
			return zero, false
		}
		modInst = append(modInst, addrs.ModuleInstanceStep{Name: modName, InstanceKey: addrs.NoKey})
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
