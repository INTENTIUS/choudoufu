// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
)

// declaredChildImportIDs is the import identity of every instance of
// typeName this pass already holds a resolution for, in the form a parent
// read composes a listed child's identity in, so a read that finds a child
// the configuration declares can skip it whichever way the resolution
// carries that identity.
//
// The way matters, and it is what this replaces. The parent-read legs used
// to key the declared set on [identity.Resolution.ImportID] of concrete
// resolutions alone. That string is empty by design for a type the
// schema-aware resolver classifies identity-object-only
// ([identity.TypeIdentity.IdentityObjectOnly]: several identity attributes
// and no separator any schema documents), where the identity lives in
// [identity.Resolution.IdentityValues] instead - and aws_iam_role_policy is
// such a type once the provider's identity schema ({role, name}) is in hand,
// which is every plan the command runs. The declared set then read {"":
// true}, no listed child could ever match it, and every plan of a one-role
// estate reported the role's own declared inline policy as an undeclared
// removal: an address minted from the policy's name, a "[WILL BE
// DESTROYED]" line in the parent-read section, and a "[SUPERSEDED]" entry
// once the projection noticed the declared instance had claimed the same
// object. Moved or not, refreshed or not.
//
// Three forms, one composition. A concrete resolution's ImportID is used
// as-is when present; when it is empty, IdentityValues are composed by the
// type's own table entry ([composeImportIDFromComponents]), the same
// grammar [childImportID] composes a list result's identity attributes
// with, so the two sides agree by construction. A parent-derived resolution
// renders its formula against the parents this pass resolved concrete
// (their ImportID, or their own single identity value), then composes the
// same way; a formula with an unresolved parent contributes nothing rather
// than a guess. Anything else has no identity to offer.
func declaredChildImportIDs(typeName string, res *Result) map[string]bool {
	byAddr := make(map[string]identity.Resolution, len(res.Resolutions))
	for _, r := range res.Resolutions {
		byAddr[r.Addr.String()] = r
	}
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		pr, ok := byAddr[parent.String()]
		if !ok || pr.Class != identity.ClassConcrete {
			return "", false
		}
		if pr.ImportID != "" {
			return pr.ImportID, true
		}
		if v, ok := pr.IdentityValues[attr]; ok && v != "" {
			return v, true
		}
		if len(pr.IdentityValues) == 1 {
			for _, v := range pr.IdentityValues {
				return v, v != ""
			}
		}
		return "", false
	}

	out := make(map[string]bool)
	for _, r := range res.Resolutions {
		if r.Type() != typeName {
			continue
		}
		if id, ok := declaredImportID(typeName, r, lookup); ok {
			out[id] = true
		}
	}
	return out
}

// declaredImportID is one resolution's import identity in the parent-read
// legs' composed form, or false when the resolution cannot name one. See
// [declaredChildImportIDs] for the three forms.
func declaredImportID(typeName string, r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (string, bool) {
	switch r.Class {
	case identity.ClassConcrete:
		if r.ImportID != "" {
			return r.ImportID, true
		}
		if len(r.IdentityValues) > 0 {
			return composeImportIDFromComponents(typeName, r.IdentityValues)
		}
	case identity.ClassParentDerived:
		if r.Formula == nil {
			return "", false
		}
		if vals, ok := r.Formula.RenderAttrs(lookup); ok && len(vals) > 0 {
			if id, ok := composeImportIDFromComponents(typeName, vals); ok {
				return id, true
			}
		}
		if id, ok := r.Formula.Render(lookup); ok && id != "" {
			return id, true
		}
	}
	return "", false
}
