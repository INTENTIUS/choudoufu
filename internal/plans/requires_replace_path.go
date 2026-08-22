// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package plans

import (
	"github.com/zclconf/go-cty/cty"
)

// RequiresReplacePathIsDegenerate reports whether path contains a
// cty.GetAttrStep whose Name is the empty string, at any position.
//
// No provider schema, present or future, ever defines an attribute whose
// name is the empty string - attribute names always come from schema keys,
// which are always non-empty identifiers. A RequiresReplace path containing
// such a step therefore cannot be naming any real attribute in any schema:
// it is not merely a name this particular resource type doesn't happen to
// have, it is not a well-formed reference to begin with.
//
// This is deliberately narrower than "the path could not be resolved
// against either the prior or planned value" (which also happens for an
// ordinary but wrong or since-removed attribute name, a case this function
// does not report). It is also distinct from the empty path (zero steps),
// which is a well-formed way for a provider to say "replace the whole
// object" and resolves without error. A one-step path with an empty name is
// neither: it is the shape a provider produces when a plan modifier builds
// an attribute path at runtime and, due to its own bug, never fills in
// which attribute it means.
func RequiresReplacePathIsDegenerate(path cty.Path) bool {
	for _, step := range path {
		if attr, ok := step.(cty.GetAttrStep); ok && attr.Name == "" {
			return true
		}
	}
	return false
}
