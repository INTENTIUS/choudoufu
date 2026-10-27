// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package markerkeys is the ownership-marker vocabulary a caller hands the
// public sweeps (pkg/gcpsweep, pkg/azsweep): which tag or label key names
// the owner of a cloud object, which names the object inside that owner,
// and how a value is spelled on the object (GitHub issue #2281).
//
// The sweeps read no vocabulary of their own. choudoufu passes
// "tofu-estate" and "tofu-address" with values as given; plunge passes
// "plunge-stack" and "plunge-urn", and for a label family reduces both
// values to digests. Both engines then run the same sweep code.
package markerkeys

import "errors"

// Keys is one engine's marker vocabulary.
type Keys struct {
	// Owner is the key that names the estate or stack owning an object:
	// choudoufu's "tofu-estate", plunge's "plunge-stack".
	Owner string
	// Address is the key that names the object inside its owner:
	// choudoufu's "tofu-address", plunge's "plunge-urn".
	Address string
	// Encode, when set, turns an owner or address value into the value
	// key carries on the object, for a family whose values cannot hold
	// the name as written (plunge's label digests). A sweep calls it on
	// every value it searches for or compares against. nil carries every
	// value as given, which is choudoufu's case: its callers already pass
	// the spelling the label holds.
	Encode func(key, value string) string
}

// Value is value as key carries it: [Keys.Encode]'s answer, or value
// itself when Encode is nil.
func (k Keys) Value(key, value string) string {
	if k.Encode == nil {
		return value
	}
	return k.Encode(key, value)
}

// OwnerValue is [Keys.Value] for the owner key.
func (k Keys) OwnerValue(owner string) string { return k.Value(k.Owner, owner) }

// AddressValue is [Keys.Value] for the address key.
func (k Keys) AddressValue(address string) string { return k.Value(k.Address, address) }

// Validate refuses a vocabulary with no owner key: a sweep built from one
// would search for objects labelled with an empty key and find nothing,
// which reads as an empty estate.
func (k Keys) Validate() error {
	if k.Owner == "" {
		return errors.New("markerkeys: no owner key: the caller names the tag or label key that marks an owned object")
	}
	return nil
}
