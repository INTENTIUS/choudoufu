// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package markers

import (
	"fmt"
	"strings"
)

// This file is the ownership marker of a resource that owns fields rather
// than an object (GitHub issue #1191, ruled 2026-10-03): the server-side
// apply field manager its writes are made under.
//
// # Why it is not a Surface
//
// A [Surface] is a place ON AN OBJECT a marker is written into and read
// back from - a tags map, a labels map, a manifest's labels. The six
// field-granular hashicorp/kubernetes types (substrate.FieldGranularShape)
// write into an object some other estate, tool or controller created, and
// the one thing that says which of the object's fields a write of theirs
// owns is the API server's own metadata.managedFields, keyed by manager
// name. The estate is the manager. Nothing is stamped into a map, so none
// of the per-surface questions (which map, which path, does the create
// carry it, is an address beside it) has an answer here, and adding a
// fourth Surface would make every one of those seams grow an arm that
// returns "not applicable". The functions below take and return strings
// only, so the completeness guard in seams_test.go does not count them as
// surface members, deliberately.
//
// # The rule
//
// A field-granular block in estate E writes under the manager
// [FieldManagerFor](E), "choudoufu:E". A field owned by "choudoufu:F", F
// another estate, belongs to F; one owned by any other manager - a
// controller, kubectl, the provider's own default "Terraform" - belongs to
// no estate. The patched object's own tofu-estate label, if it carries one,
// is irrelevant: two estates may each own one field of an object neither
// of them owns.

// FieldManagerPrefix is what every estate's field manager begins with.
const FieldManagerPrefix = "choudoufu:"

// FieldManagerMaxLen is the API server's limit on a field manager name
// (apimachinery's validation of metav1.PatchOptions.FieldManager).
const FieldManagerMaxLen = 128

// FieldManagerFor is the field manager estate's field-granular writes are
// made under.
func FieldManagerFor(estate string) string {
	return FieldManagerPrefix + estate
}

// EstateOfFieldManager reads the estate a field manager name speaks for,
// or ok false for a manager that is not an estate's.
func EstateOfFieldManager(manager string) (estate string, ok bool) {
	estate, ok = strings.CutPrefix(manager, FieldManagerPrefix)
	if !ok || estate == "" {
		return "", false
	}
	return estate, true
}

// ValidFieldManagerEstate reports why estate cannot be carried as a field
// manager name, or "" when it can.
func ValidFieldManagerEstate(estate string) string {
	if estate == "" {
		return "the estate name is empty"
	}
	if n := len(FieldManagerFor(estate)); n > FieldManagerMaxLen {
		return fmt.Sprintf("%q is %d characters as a field manager name, and the API server accepts at most %d", FieldManagerFor(estate), n, FieldManagerMaxLen)
	}
	return ""
}
