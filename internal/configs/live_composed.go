// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import "github.com/hashicorp/hcl/v2"

// ComposedEstate is one module call of a composed root (GitHub issue #2059):
// a call to a directory that carries a live block of its own. That block, not
// anything on the call, names the estate and the record store, so an estate's
// files are the same whether it runs alone or composed.
type ComposedEstate struct {
	// Call is the module call's name; the estate's resources live at
	// module.<Call> in the composed run.
	Call string

	// Dir is the called directory, relative to the root's.
	Dir string

	// Live is the called module's own live configuration.
	Live *Live

	// DeclRange is the module call's header.
	DeclRange hcl.Range
}

// NewComposedLive builds the Live a composed root runs under. A root that has
// no live block of its own but calls modules that do is a composed run: it
// carries no estate and no record store itself, so Estate is empty and
// RecordStore is nil, and the estates are the calls listed in Composed.
func NewComposedLive(estates []ComposedEstate) *Live {
	l := &Live{Composed: estates}
	if len(estates) > 0 {
		l.DeclRange = estates[0].DeclRange
	}
	return l
}

// IsComposed reports whether this is a composed root's Live.
func (l *Live) IsComposed() bool {
	return l != nil && len(l.Composed) > 0
}
