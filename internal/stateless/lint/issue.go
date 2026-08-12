// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/tfdiags"
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

	// RuleChildModule covers module calls: stateless mode v0 is a
	// root-module mode. See child_module.go.
	RuleChildModule Rule = "child-module"

	// RuleReceiptLeaf covers a direct reference, from another managed
	// resource or an output, into a resource that is statically
	// recognizable as a receipt (stateless/RECEIPTS.md's naming
	// convention). See receipt_leaf.go.
	RuleReceiptLeaf Rule = "receipt-leaf"
)

// ruleInfo is the fixed part of every issue a rule produces: the one-line
// summary and the stateless/LIMITATIONS.md entry that documents the rule. The
// variable part (which construct tripped it, where, and what to do about this
// particular one) lives on the Issue.
var ruleInfo = map[Rule]struct {
	summary string
	docsRef string
}{
	RuleProvisioner: {
		summary: "Provisioners are not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "local-exec" / "remote-exec"`,
	},
	RuleRemoteState: {
		summary: "terraform_remote_state is not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "remote-state"`,
	},
	RuleMovedBlock: {
		summary: "moved blocks are not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "moved-block"`,
	},
	RuleLogicalResource: {
		summary: "Logical resources are not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "null-resource" / "local-file" / "random-password" / "time-sleep"`,
	},
	RuleUnadmittedType: {
		summary: "Resource type is outside the stateless subset",
		docsRef: `stateless/LIMITATIONS.md, "unadmitted-type"`,
	},
	RuleStateBackend: {
		summary: "State backends are not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "backend-block" / "cloud-block"`,
	},
	RuleCountIndex: {
		summary: "count.index is not available in resource arguments",
		docsRef: `stateless/LIMITATIONS.md, "count-index-in-tag"`,
	},
	RuleForEachKey: {
		summary: "for_each key is outside the marker character set",
		docsRef: `stateless/LIMITATIONS.md, "foreach-dotted-key"`,
	},
	RuleChildModule: {
		summary: "Child modules are not available in stateless mode",
		docsRef: `stateless/LIMITATIONS.md, "child-module"`,
	},
	RuleReceiptLeaf: {
		// No stateless/LIMITATIONS.md entry exists for this rule; cite the
		// docs page that defines it by title instead.
		summary: "Nothing may reference a receipt's attributes",
		docsRef: `stateless/RECEIPTS.md, "Guard 4 — the leaf rule"`,
	},
}

// Summary is the one-line description of what the rule rejects, used as the
// diagnostic summary.
func (r Rule) Summary() string {
	if info, ok := ruleInfo[r]; ok {
		return info.summary
	}
	return fmt.Sprintf("Stateless-mode rule %q", string(r))
}

// DocsRef names the shipped doc — almost always a stateless/LIMITATIONS.md
// entry — that explains the rule, so that every rejection can be traced to a
// written reason a user actually has, rather than to this package's opinion
// or to a planning ledger that never ships.
func (r Rule) DocsRef() string {
	if info, ok := ruleInfo[r]; ok {
		return info.docsRef
	}
	return "stateless/LIMITATIONS.md"
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
