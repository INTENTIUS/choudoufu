// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// admitted reports whether the given provider-local resource type may appear
// in a stateless configuration: first by the generated table, and - only when
// the caller supplied provider schemas - by whatever
// [identity.SynthesizeTypeIdentity] can derive from those schemas and the
// configuration's own naming signal.
//
// The table lookup runs first and unconditionally, so a type the table
// already covers never depends on schemas being present at all. The
// fallback only ever admits a type the table refuses; it never revokes one
// the table already grants. That asymmetry is the whole point: a caller
// with no schemas gets exactly the table's answer, and a caller with
// schemas gets the table's answer plus whatever the schemas additionally
// justify, never less.
func admitted(resourceType string, schemas map[string]providers.Schema, signal *identity.ConfigSignal) bool {
	if _, ok := admittedTypesV0[resourceType]; ok {
		return true
	}
	if len(schemas) == 0 {
		return false
	}
	_, ok := identity.SynthesizeTypeIdentity(resourceType, schemas, signal)
	return ok
}
