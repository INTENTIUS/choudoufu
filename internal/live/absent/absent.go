// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package absent answers one question for any caller that has just asked a
// provider to import a resource and been handed an error: is the provider
// saying "there is no such object", or failing to answer at all.
//
// Textual is all the answer can be. A diagnostic that has crossed the
// provider plugin protocol carries only Summary and Detail strings; nothing
// structured about the underlying provider error survives the wire.
//
// Two callers need exactly this, on two different paths to the same
// provider RPC: internal/live/projection/build.go's pre-walk projection
// (importAndRead) and internal/tofu/node_resource_plan_instance.go's
// plan-node seam (importState under a resolver-supplied target). Neither
// may import the other - internal/tofu must never import the fork's
// live-mode package, and projection already imports internal/tofu - and
// until GitHub issue #1064 each carried its own copy of the shapes, kept in
// sync by hand. This package is the leaf both import, the same shape
// internal/live/noimporter takes for the "no Importer at all" question.
package absent

import (
	"regexp"
	"strings"
)

// Signals are substrings of a provider's own ImportResourceState diagnostic
// that mean "no such object":
//
//   - "couldn't find resource" is the exact, hardcoded default message
//     terraform-plugin-sdk's retry.NotFoundError renders when a provider's
//     internal finder comes back empty and sets no more specific text. It
//     is a generic SDK convention used across the whole of
//     terraform-provider-aws wherever a resource's Read is built on a
//     "find the live object or report NotFoundError" finder.
//     aws_lambda_permission - whose import lookup calls GetPolicy on the
//     function, not the permission, and so 404s with this shape the moment
//     the function itself does not exist yet either - is a confirmed
//     instance (GitHub issue #297), not the only one this covers.
//   - "ResourceNotFoundException" is AWS's own API error code, for a
//     provider that surfaces the untranslated API error instead of going
//     through the generic finder convention above.
var Signals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
}

// Patterns are the shapes that need more than a substring to recognise
// safely:
//
//   - `<resource> "<name>" not found` is k8s.io/apimachinery's own NotFound
//     status message (errors.NewNotFound renders `%s %q not found`), which
//     hashicorp/kubernetes surfaces untranslated out of a type-specific
//     importer: `serviceaccounts "app" not found` (GitHub issue #1064, the
//     first type admitted through the object-metadata rule to reach a
//     greenfield plan). Matched as a shape, because "not found" on its own
//     is ordinary English a genuine failure could carry.
var Patterns = []*regexp.Regexp{
	regexp.MustCompile(`\b[a-z][a-z0-9.-]*(?:/[a-z0-9.-]+)? "[^"]+" not found\b`),
}

// Matches reports whether one diagnostic's summary or detail is a
// not-found shape.
func Matches(summary, detail string) bool {
	for _, signal := range Signals {
		if strings.Contains(summary, signal) || strings.Contains(detail, signal) {
			return true
		}
	}
	for _, pattern := range Patterns {
		if pattern.MatchString(summary) || pattern.MatchString(detail) {
			return true
		}
	}
	return false
}
