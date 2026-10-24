// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"net/url"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
)

// armSegmentValue is the per-component half of [Component.ARMSegmentIndex]
// (GitHub issue #2206): the argument's value is an ARM id and the component
// is the one path segment at that position. A literal id is split here and
// the segment before the wanted one has to be [Component.ARMSegmentAfter].
// A reference to a parent's live id is kept as the reference, with the
// same split and element taken when it renders. Anything else is refused by
// name.
func (r *resolver) armSegmentValue(comp Component, addr addrs.AbsResourceInstance, attr *hcl.Attribute, got []Part) ([]Part, bool) {
	idx := comp.ARMSegmentIndex
	if idx <= 0 {
		return got, true
	}
	if v, known := literal(got); known {
		pieces := strings.Split(v, "/")
		if len(pieces) <= idx || pieces[idx] == "" || (comp.ARMSegmentAfter != "" && !strings.EqualFold(pieces[idx-1], comp.ARMSegmentAfter)) {
			r.errorf(attr.Range, "Identity argument is not the expected ARM id",
				"%s sets %s to %q, which has no %q segment at position %d to take the name from, so the data-plane id built from it is refused rather than guessed (#2206).",
				addr.String(), attr.Name, v, comp.ARMSegmentAfter, idx)
			return nil, false
		}
		return []Part{{Literal: pieces[idx]}}, true
	}
	if len(got) == 1 && got[0].Parent != nil && got[0].Parent.Attr == "id" && len(got[0].Parent.Transform) == 0 {
		ref := *got[0].Parent
		ref.Transform = []Op{{Kind: OpSplit, Sep: "/"}, {Kind: OpElement, Index: idx}}
		return []Part{{Parent: &ref}}, true
	}
	r.errorf(attr.Range, "Identity argument is not a plain reference",
		"%s sets %s from an expression that is neither a literal ARM id nor a bare reference to a parent's id, so the segment at position %d cannot be taken from it (#2206).",
		addr.String(), attr.Name, idx)
	return nil, false
}

// urlEscapeValue is the per-component half of [Component.URLEscape].
func (r *resolver) urlEscapeValue(comp Component, addr addrs.AbsResourceInstance, attr *hcl.Attribute, got []Part) ([]Part, bool) {
	if comp.URLEscape == "" {
		return got, true
	}
	v, known := literal(got)
	if !known {
		r.errorf(attr.Range, "Escaped identity argument not known before apply",
			"%s sets %s from a value that is not known until apply, and azurerm escapes it (url.%s) before it becomes part of the id, which reads the value's text, so the id cannot be built until the value exists (#2206). Write a literal to resolve it now.",
			addr.String(), attr.Name, map[string]string{"path": "PathEscape", "query": "QueryEscape"}[comp.URLEscape])
		return nil, false
	}
	switch comp.URLEscape {
	case "path":
		return []Part{{Literal: url.PathEscape(v)}}, true
	case "query":
		return []Part{{Literal: url.QueryEscape(v)}}, true
	}
	r.errorf(attr.Range, "Unknown identity escape", "%s: the escape %q is not one this package knows (#2206).", addr.String(), comp.URLEscape)
	return nil, false
}
