// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

// SchemaSourceProvider and SchemaSourceBuiltin are the two values the
// "schemas" field carries on every JSON document this package prints whose
// content depends on having read a provider's schemas: "choudoufu
// live-check -json"'s rungs and "choudoufu live-ls -json DIR"'s
// declared-instance gaps, both of which degrade rather than refuse when the
// directory was never initialized.
//
// GitHub issue #966: before this, the degraded answer and the accurate one
// were the same shape, the same keys and the same exit code, so a scripted
// reader could not tell them apart. The remedy the help text names is
// "choudoufu init"; these values say whether it is still needed.
const (
	// SchemaSourceProvider means the provider's own schemas were read and
	// backed the classification in this document.
	SchemaSourceProvider = "provider"

	// SchemaSourceBuiltin means no provider schema was available, so
	// whatever fallback the classification has - internal/live/check's
	// built-in admission table for a rung, no taggability answer at all for
	// a gap - is what produced the answer.
	SchemaSourceBuiltin = "builtin"
)

// schemaSource maps "were there any provider schemas" onto the two values
// above. It is one function rather than two literals at two call sites so
// that live-check and live-ls cannot drift into spelling the same fact
// differently.
func schemaSource(loaded bool) string {
	if loaded {
		return SchemaSourceProvider
	}
	return SchemaSourceBuiltin
}
