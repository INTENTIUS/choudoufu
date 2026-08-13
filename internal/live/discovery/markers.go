// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// The marker vocabulary this package reads with. It lives in
// [github.com/intentius/choudoufu/internal/live/markers] and is named here
// so that discovery's callers - which is most of the fork - keep reading the
// marker spec through the package that binds by it.
//
// The move out of this package was forced by ownership verification: the
// projection has to read the same tags with the same rule before it admits a
// live object into prior state, and it cannot import discovery, whose own
// integration test builds a projection. Two implementations of one spec is
// how a resource comes to be discoverable and unverifiable at the same time,
// so there is one, in a leaf package, and these are the names for it.
const (
	// TagEstate names the estate that owns the resource.
	TagEstate = markers.TagEstate

	// TagAddress carries the resource's canonical config address, escaped.
	TagAddress = markers.TagAddress

	// TagSlot carries a count instance's stable slot.
	TagSlot = markers.TagSlot

	// MaxTagValue is the AWS hard limit on a tag value, in Unicode
	// characters.
	MaxTagValue = markers.MaxTagValue

	// MaxContinuations is the highest number of tag values one tofu-address
	// may span (issue #71's k).
	MaxContinuations = markers.MaxContinuations

	// MaxAddressLen is the longest escaped address a resource's markers may
	// carry in total, across tofu-address and every continuation tag.
	MaxAddressLen = markers.MaxAddressLen
)

// ValidEstateName reports whether s is a well-formed estate name.
func ValidEstateName(s string) bool { return markers.ValidEstateName(s) }

// EscapeAddress applies the marker spec's escaping rule to an address.
func EscapeAddress(addr string) string { return markers.EscapeAddress(addr) }

// ValidMarkerAddress reports whether an escaped address is well-formed enough
// to be compared.
func ValidMarkerAddress(escaped string) bool { return markers.ValidMarkerAddress(escaped) }

// UnescapeAddress turns an escaped marker value back into a resource instance
// address, and reports whether it could.
func UnescapeAddress(escaped string) (addrs.AbsResourceInstance, bool) {
	return markers.UnescapeAddress(escaped)
}

// ContinuationTag names the n-th continuation tag key, for n in
// [2, MaxContinuations].
func ContinuationTag(n int) string { return markers.ContinuationTag(n) }

// AddressTagKey names the tag key that carries the i-th (0-based) chunk of a
// split tofu-address value.
func AddressTagKey(i int) string { return markers.AddressTagKey(i) }

// SplitAddress divides an escaped address into the ordered chunks its
// markers would carry.
func SplitAddress(escaped string) []string { return markers.SplitAddress(escaped) }

// GatherAddress reads a marker's tofu-address value back off a tag map,
// concatenating any continuation tags in order. See [markers.GatherAddress].
func GatherAddress(tags map[string]string) (raw string, corrupt bool) {
	return markers.GatherAddress(tags)
}
