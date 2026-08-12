// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/stateless/markers"
)

// The marker vocabulary this package reads with. It lives in
// [github.com/opentofu/opentofu/internal/stateless/markers] and is named here
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
