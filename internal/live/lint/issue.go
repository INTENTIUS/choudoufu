// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Rule identifies which stateless-mode rule an [Issue] reports. It is a stable
// string so that callers can filter, group, or suppress by rule without
// matching prose, and so tests assert on the rule rather than the message.
type Rule string

const (
	// RuleProvisioner covers provisioner blocks and the connection blocks that
	// configure them, on any resource of any type.
	RuleProvisioner Rule = "provisioner"

	// RuleRemoteState covers the terraform_remote_state data source.
	RuleRemoteState Rule = "remote-state"

	// RuleMovedBlock covers moved blocks.
	RuleMovedBlock Rule = "moved-block"

	// RuleLogicalResource covers logical, store-only resource types.
	RuleLogicalResource Rule = "logical-resource"

	// RuleUnadmittedType covers managed resources whose type is outside the v0
	// admission table.
	RuleUnadmittedType Rule = "unadmitted-type"

	// RuleStateBackend covers backend and cloud blocks in terraform settings.
	RuleStateBackend Rule = "state-backend"

	// RuleCountIndex covers count.index referenced anywhere in a managed
	// resource's own configuration body (arguments, tag values, nested
	// blocks) — never the count expression itself or the other
	// meta-argument positions, which cannot leak into identity. See
	// count_index.go.
	RuleCountIndex Rule = "count-index"

	// RuleForEachKey covers a for_each instance key carrying a character
	// that cannot survive the trip through a tofu-address marker: anything
	// outside the AWS tag-value set, and the two escaped-address separators
	// "." and ":" within it. See foreach_key.go.
	RuleForEachKey Rule = "for-each-key"

	// RuleChildModule covers a module call this mode cannot expand: count,
	// or a for_each whose keys it cannot pin down. Static calls and
	// statically-keyed for_each calls are admitted and never reach it (#59).
	// See child_module.go.
	RuleChildModule Rule = "child-module"

	// RuleOverlongAddress covers a resource instance whose escaped
	// tofu-address does not fit in a tag value: more than 256 Unicode
	// characters, the AWS hard cap live/MARKERS.md adopts. See
	// overlong_address.go.
	RuleOverlongAddress Rule = "overlong-address"

	// RuleReceiptLeaf covers a direct reference, from another managed
	// resource or an output, into a resource that is statically
	// recognizable as a receipt (live/RECEIPTS.md's naming
	// convention). See receipt_leaf.go.
	RuleReceiptLeaf Rule = "receipt-leaf"

	// RuleReceiptValue covers a statically recognizable receipt declared
	// with a literal type = "SecureString", or whose value expression is
	// not visibly one of the two documented flavors: a hash call over the
	// effect's declared inputs, or a constant literal. See
	// receipt_value.go.
	RuleReceiptValue Rule = "receipt-value"

	// RuleReceiptSecret covers a statically recognizable receipt whose
	// value expression directly references an input variable declared
	// sensitive. See receipt_secret.go.
	RuleReceiptSecret Rule = "receipt-secret"

	// RulePolicyVerb covers a live block's policy block assigning a verb to
	// a quadrant that internal/live/policy.ValidVerbs does not allow there
	// - either an unrecognized verb, or a real verb this fork refuses in
	// that quadrant (delete outside the two undeclared quadrants, for
	// example). See policy.go.
	RulePolicyVerb Rule = "policy-verb"

	// RulePolicyScope covers undeclared_untagged = "delete" with no scope
	// block: an unscoped account-wide purge, which GitHub issue #67 makes a
	// lint refusal rather than a default. Only that quadrant -
	// undeclared_tagged's delete is the ordinary orphan sweep, scoped by the
	// estate's own marker, and needs no scope block (#101). See policy.go.
	RulePolicyScope Rule = "policy-scope"

	// RuleIgnoreChanges covers a lifecycle block that discards the changes
	// this mode makes to write ownership markers: ignore_changes = all, or
	// an ignore_changes entry covering the whole tags argument or one of
	// the marker keys inside it. See ignore_changes.go.
	RuleIgnoreChanges Rule = "ignore-changes"

	// RulePolicyThreshold covers a policy block's threshold argument set to
	// zero or a negative number. It exists to be raised deliberately once a
	// delete quadrant's roster has been reviewed, which a non-positive
	// value cannot mean. See policy.go.
	RulePolicyThreshold Rule = "policy-threshold"
)

// ruleInfo is the fixed part of every issue a rule produces: the one-line
// summary and the live/LIMITATIONS.md entry that documents the rule. The
// variable part (which construct tripped it, where, and what to do about this
// particular one) lives on the Issue.
var ruleInfo = map[Rule]struct {
	summary string
	docsRef string
}{
	RuleProvisioner: {
		summary: "Provisioners are not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "local-exec" / "remote-exec"`,
	},
	RuleRemoteState: {
		summary: "terraform_remote_state is not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "remote-state"`,
	},
	RuleMovedBlock: {
		summary: "moved blocks are not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "moved-block"`,
	},
	RuleLogicalResource: {
		// Not "logical resources are not available": a RECORD_ADMITTED type
		// is admitted whenever a live block declares a record_store (#73
		// phase (d)), so the absolute was false for the commonest members of
		// this rule - null_resource and terraform_data. The three classes
		// differ enough that only the Detail can name a remedy; the summary's
		// job is to avoid claiming there isn't one - and equally to avoid
		// promising one, since SECRET_REFUSED is permanent and OTHER_REFUSED
		// has had no per-type review. "as configured" leaned too far toward
		// promising; the detail is where the class-specific answer lives.
		// See #101.
		summary: "Logical resource is not admitted",
		docsRef: `live/LIMITATIONS.md, "null-resource" / "terraform-data" / "local-file" / "random-password" / "time-sleep"`,
	},
	RuleUnadmittedType: {
		summary: "Resource type is outside the live-markers subset",
		docsRef: `live/LIMITATIONS.md, "unadmitted-type"`,
	},
	RuleStateBackend: {
		summary: "State backends are not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "backend-block" / "cloud-block"`,
	},
	RuleCountIndex: {
		summary: "count.index is not available in resource arguments",
		docsRef: `live/LIMITATIONS.md, "count-index-in-tag"`,
	},
	RuleForEachKey: {
		summary: "for_each key is outside the marker character set",
		docsRef: `live/LIMITATIONS.md, "foreach-dotted-key"`,
	},
	RuleChildModule: {
		// Child modules ARE available: childModuleDetail admits a static call
		// and a for_each call whose keys are statically evaluable, reporting
		// neither. This rule fires only on the two shapes it cannot address -
		// count, refused permanently because positional renumbering moves
		// addresses out from under their markers, and a non-static for_each.
		// The old summary said modules were unavailable outright, which sent
		// operators looking for a workaround they did not need.
		//
		// Nor is it only count and non-static for_each: identity's
		// ChildModuleKeys also refuses a for_each that IS static but has the
		// wrong type or shape - `for_each = ["a", "b"]` is a tuple, and an
		// audit found the user being told they wrote a non-static expression
		// when they had written a perfectly static one of the wrong type. The
		// summary now says what is true of every shape and leaves the
		// specifics to the detail, which names them accurately. See #101.
		summary: "This module call cannot be expanded under live resource markers",
		docsRef: `live/LIMITATIONS.md, "child-module"`,
	},
	RuleOverlongAddress: {
		summary: "Resource address does not fit in a marker",
		docsRef: `live/LIMITATIONS.md, "overlong-address"`,
	},
	RuleReceiptLeaf: {
		// No live/LIMITATIONS.md entry exists for this rule; cite the
		// docs page that defines it by title instead.
		summary: "Nothing may reference a receipt's attributes",
		docsRef: `live/RECEIPTS.md, "Guard 4. The leaf rule"`,
	},
	RuleReceiptValue: {
		// No live/LIMITATIONS.md entry exists for this rule; cite the
		// docs page that defines it by title instead.
		summary: "A receipt's value is a hash or a constant, and its type is never SecureString",
		docsRef: `live/RECEIPTS.md, "Guard 2. Hash-only values, and never SecureString"`,
	},
	RuleReceiptSecret: {
		// No live/LIMITATIONS.md entry exists for this rule; cite the
		// docs page that defines it by title instead.
		summary: "Receipt inputs reference secrets by pointer, never by value",
		docsRef: `live/RECEIPTS.md, "Secrets discipline"`,
	},
	RulePolicyVerb: {
		summary: "Policy verb is not valid for its quadrant",
		docsRef: `live/LIMITATIONS.md, "policy-verb"`,
	},
	RulePolicyScope: {
		summary: "Delete quadrant has no scope block",
		docsRef: `live/LIMITATIONS.md, "policy-scope"`,
	},
	RulePolicyThreshold: {
		summary: "Policy threshold is not a positive number",
		docsRef: `live/LIMITATIONS.md, "policy-threshold"`,
	},
	RuleIgnoreChanges: {
		summary: "Ownership markers would be ignored",
		docsRef: `live/LIMITATIONS.md, "ignore-changes"`,
	},
}

// Summary is the one-line description of what the rule rejects, used as the
// diagnostic summary.
func (r Rule) Summary() string {
	if info, ok := ruleInfo[r]; ok {
		return info.summary
	}
	return fmt.Sprintf("Live-markers rule %q", string(r))
}

// DocsRef names the shipped doc — almost always a live/LIMITATIONS.md
// entry — that explains the rule, so that every rejection can be traced to a
// written reason a user actually has, rather than to this package's opinion
// or to a planning ledger that never ships.
func (r Rule) DocsRef() string {
	if info, ok := ruleInfo[r]; ok {
		return info.docsRef
	}
	return "live/LIMITATIONS.md"
}

// Issue is a single rejection: one construct in one configuration that puts
// the configuration outside the stateless subset.
type Issue struct {
	// Rule is which rule fired.
	Rule Rule

	// Construct names the offending construct as the author wrote it, in
	// address form where there is one: `aws_instance.web`,
	// `provisioner "local-exec" on aws_s3_bucket.data`, `backend "s3"`.
	Construct string

	// Type is the managed resource type name, set only by the rules whose
	// verdict is about the type rather than about the construct:
	// [RuleUnadmittedType] and [RuleLogicalResource]. Every other rule
	// leaves it empty.
	//
	// It exists so that a report can group these two rules by type without
	// picking the name back out of Construct or Detail. Both are prose a
	// campaign has already rewritten once (#101) and #110 will rewrite
	// again, and an instrument that measures which refusals fire must not
	// be reading either. See internal/live/check.
	Type string

	// Module is the path of the module the construct was found in. Empty for
	// the root module.
	Module addrs.Module

	// Detail explains why this construct cannot work without authoritative
	// state and, where there is one, what replaces it.
	Detail string

	// Subject is where the construct is declared.
	Subject hcl.Range
}

// Error renders the issue as a single line, for log output and test failure
// messages. Command output goes through [Diagnostics] instead.
func (i Issue) Error() string {
	where := i.Subject.String()
	if len(i.Module) > 0 {
		where = fmt.Sprintf("%s (in %s)", where, i.Module.String())
	}
	return fmt.Sprintf("%s: [%s] %s: %s", where, i.Rule, i.Construct, i.Detail)
}

// Diagnostics converts issues into diagnostics for a command to render. Each
// issue becomes one error diagnostic whose summary is the rule's summary,
// whose detail names the construct, explains the rule, and cites the doc
// entry that documents it, and whose subject is the construct's source
// range.
func Diagnostics(issues []Issue) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	for _, issue := range issues {
		subject := issue.Subject
		detail := fmt.Sprintf(
			"%s: %s\n\nRule: %s. See %s.",
			issue.Construct, issue.Detail, issue.Rule, issue.Rule.DocsRef(),
		)
		if len(issue.Module) > 0 {
			detail = fmt.Sprintf("In %s, %s", issue.Module.String(), detail)
		}
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  issue.Rule.Summary(),
			Detail:   detail,
			Subject:  &subject,
		})
	}
	return diags
}
