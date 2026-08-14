// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is GitHub issue #106's third criterion: the explicit
// [identity.Component.IdentityAttr] values in the ratified table are derived
// by a rule, not listed as per-row judgments. The issue counted "76 arn
// mappings" - those are component sites; per row it is 17 explicit values,
// and 12 of them follow one rule this file states. The other five, plus two
// rows the rule must deliberately NOT fire on, are in
// [identityAttrEvidence] with the raw evidence each ruling rests on.
//
// The rule: a row whose components assemble one string with a leading
// literal that names its own scheme - "arn:" for an Amazon Resource Name,
// "https://" for a URL - supplies exactly one identity attribute with the
// same shape, "arn" or "url", and every component of the row (literals and
// cloud values included: their rendered strings concatenate in order to
// form the attribute, see [identity.Component.IdentityAttr]) carries that
// name. An SNS topic's arn, an SQS queue's url.
//
// A rule that reaches further than this was tried and measured before this
// one was written down, because the provider's own identity schema looks
// like the stronger source: "when the schema requires exactly one attribute,
// the components supply it". Over the 846 ratified rows it mis-fires six
// times - aws_route and aws_route_table_association assemble a composite
// whose components each supply their own same-named attribute even though
// the schema's required list has one entry, and aws_codebuild_project,
// aws_codebuild_fleet and the two codeartifact permissions-policy rows
// client-name by an argument that is not the schema's attribute at all. The
// leading literal is the property the ratified table actually keys on; the
// schema is evidence for the five hand rows, not a rule.
const (
	arnLiteralPrefix = "arn:"
	urlLiteralPrefix = "https://"
)

// deriveAssembledIdentityAttr returns the identity attribute every component
// of an assembled row supplies, when the leading literal settles it.
func deriveAssembledIdentityAttr(components []identity.Component) (string, bool) {
	if len(components) == 0 {
		return "", false
	}
	switch {
	case strings.HasPrefix(components[0].Literal, arnLiteralPrefix):
		return "arn", true
	case strings.HasPrefix(components[0].Literal, urlLiteralPrefix):
		return "url", true
	}
	return "", false
}

// applyDerivedIdentityAttrs is the proposal side of the rule: any proposal
// whose components the rule covers gets the derived name on every component.
// No current proposal shape produces a leading literal - bucketComposite
// joins plain arguments with a separator - so today this fires only in its
// own tests; it exists so that the first rule that DOES propose an
// ARN-template row cannot ship without the join, and so the ratchet test
// and the proposal path share one implementation.
func applyDerivedIdentityAttrs(components []identity.Component) []identity.Component {
	attr, ok := deriveAssembledIdentityAttr(components)
	if !ok {
		return components
	}
	out := make([]identity.Component, len(components))
	for i, c := range components {
		c.IdentityAttr = attr
		out[i] = c
	}
	return out
}

// identityAttrEvidence is the hand half of criterion 3: every ratified row
// whose explicit IdentityAttr the rule above cannot produce - or would
// produce wrongly - with the evidence inspected for each. A row appears here
// for exactly one of two reasons, and the ratchet test holds the set exact:
// stale entries and undocumented exceptions both fail it.
var identityAttrEvidence = map[string]struct {
	// attr is the explicit IdentityAttr the row's components carry; empty
	// means the row deliberately carries none where the rule would derive
	// one.
	attr string
	// evidence is what was actually inspected, not a restatement of the
	// ruling.
	evidence string
}{
	"aws_glue_catalog_database": {
		attr: "id",
		evidence: "Provider docs (6.59.0, cached page): import by `catalog_id:name`, example `123456789012:my_database`; the doc has no `### Identity Schema` section and live/survey-full.json carries no identity for the type, so the joined string is the resource's own `id` attribute and nothing narrower exists to name.",
	},
	"aws_glue_catalog_table": {
		attr: "id",
		evidence: "Provider docs (6.59.0): import by catalog ID, database name and table name joined with `:`; no Identity Schema section, no wire identity in live/survey-full.json. Same shape as aws_glue_catalog_database.",
	},
	"aws_glue_connection": {
		attr: "id",
		evidence: "Provider docs (6.59.0): import by `CATALOG-ID` and `NAME` joined with `:`; no Identity Schema section, no wire identity in live/survey-full.json. Same shape as aws_glue_catalog_database.",
	},
	"aws_glue_data_catalog_encryption_settings": {
		attr: "id",
		evidence: "Provider docs (6.59.0): import by `CATALOG-ID` alone; no Identity Schema section, no wire identity in live/survey-full.json. The single component is a cloud value (the account ID), so no leading literal exists for the rule to read.",
	},
	"aws_securityhub_member": {
		attr: "member_account_id",
		evidence: "Both remaining sources name it: live/survey-full.json's identity requires exactly [member_account_id], and the doc page's `### Identity Schema` section lists `member_account_id` as required. The row's single component reads the `account_id` argument - a rename the schemas state outright, not a judgment.",
	},
	"aws_codeartifact_domain_permissions_policy": {
		attr: "",
		evidence: "The components assemble an `arn:aws:codeartifact:` string, but live/survey-full.json's identity for this type requires exactly [resource_arn], not [arn] - deriving \"arn\" from the leading literal would name an attribute the identity schema does not have. The ratified row leaves every component unnamed and imports by the joined string.",
	},
	"aws_codeartifact_repository_permissions_policy": {
		attr: "",
		evidence: "Same as aws_codeartifact_domain_permissions_policy: the wire's identity schema requires [resource_arn], so the leading-literal derivation of \"arn\" would be wrong, and the ratified row carries no component IdentityAttr at all.",
	},
}
