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

	// RuleMovedBlock covers moved blocks.
	RuleMovedBlock Rule = "moved-block"

	// RuleLogicalResource covers logical, store-only resource types.
	RuleLogicalResource Rule = "logical-resource"

	// RuleUnadmittedType covers managed resources whose type is outside the v0
	// admission table.
	RuleUnadmittedType Rule = "unadmitted-type"

	// RuleMarkerlessType covers managed resources whose type
	// [identity.MarkerlessTypes] vetoes: the provider mints the identity and
	// the type has no tags argument, so the marker that is the only remaining
	// handle has nowhere to be written. See admission.go's markerlessVetoed.
	//
	// It is a separate rule from [RuleUnadmittedType] because the two report
	// the same absence with opposite promises. RuleUnadmittedType means the
	// type is not in the table yet and ends by inviting the reader to open an
	// issue naming the type and its documented import ID; this rule means the
	// type is out by a derived rule that has already weighed that evidence,
	// and no batch reaches it. An operator who acts on the wrong one of those
	// two spends a round trip to be told no.
	RuleMarkerlessType Rule = "markerless-type"

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

	// RuleChildModule covers a module call this mode cannot expand: a count
	// or for_each expression it cannot statically evaluate, or a static
	// count whose module body reads count.index (issue #195). Static
	// calls, statically-keyed for_each calls, and statically-evaluated
	// count calls with no count.index leak are admitted and never reach
	// it. See child_module.go.
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

	// RuleModuleProviders covers a module call whose providers mapping
	// sends the module's resources to an aliased provider configuration
	// that live mode does not read. See module_providers_mapping.go.
	RuleModuleProviders Rule = "module-providers"

	// RuleIgnoreChanges covers a lifecycle block that discards the changes
	// this mode makes to write ownership markers: ignore_changes = all, or
	// an ignore_changes entry covering the whole tags argument or one of
	// the marker keys inside it. See ignore_changes.go.
	RuleIgnoreChanges Rule = "ignore-changes"

	// RuleStrictMarkerRepair covers a live block's strict block whose
	// marker_repair argument names something other than the one setting a
	// build implements. Two shapes reach it: a value outside
	// internal/live/strict's vocabulary altogether, and one inside it whose
	// mechanism has not been built yet ("report" and "never", GitHub issue
	// #365). See strict.go.
	RuleStrictMarkerRepair Rule = "strict-marker-repair"

	// RuleStrictMarkers covers a live block's `strict { markers "record" }`
	// selection this build cannot read as a selection at all: an empty
	// block, a block with no record_store to hold the identities it moves,
	// an address entry outside the -target grammar or naming an instance
	// rather than a resource block, and an address naming no resource this
	// configuration declares. GitHub issue #365. See strict.go.
	//
	// It is a separate rule from [RuleStrictMarkersUnrecordable] because the
	// two have different authors. This one is the block itself being wrong,
	// which the author can read off their own file; that one is a fact about
	// a provider resource type, which they cannot.
	RuleStrictMarkers Rule = "strict-markers"

	// RuleStrictMarkersUnrecordable covers a resource type a
	// `markers "record"` selection reaches whose identity the estate's
	// record store cannot hold: the provider will not import the type back,
	// or its documented import string is composite with nothing proving the
	// exported "id" is the whole of it, or the one attribute a record would
	// hold is one the provider marks sensitive. GitHub issue #365, and
	// internal/live/identity.SelectedLocatedRefusal is where the conditions
	// live. See strict.go.
	//
	// Refused rather than quietly not applied, because the two failure modes
	// are not symmetric: not applying the selection leaves the marker where
	// it is and is invisible, while an operator who believes the tag budget
	// they were buying back has been bought back will spend it again
	// somewhere else.
	RuleStrictMarkersUnrecordable Rule = "strict-markers-unrecordable"

	// RuleStrictSecrets covers a live block's strict block whose secrets
	// argument names something outside internal/live/strict's vocabulary.
	// Both settings the vocabulary defines are implemented, so unlike
	// [RuleStrictMarkerRepair] there is only one shape here: a typo. GitHub
	// issue #365. See strict.go.
	RuleStrictSecrets Rule = "strict-secrets"

	// RulePolicyThreshold covers a policy block's threshold argument set to
	// zero or a negative number. It exists to be raised deliberately once a
	// delete quadrant's roster has been reviewed, which a non-positive
	// value cannot mean. See policy.go.
	RulePolicyThreshold Rule = "policy-threshold"

	// RuleChildLiveConfig covers a live configuration - block or sidecar -
	// declared inside a child module, which live mode would silently ignore.
	// See child_live_config.go.
	RuleChildLiveConfig Rule = "child-live-config"

	// RuleUndeclaredProviderAlias covers a root resource whose provider
	// argument names an alias no root provider block declares, which live
	// mode used to configure silently from the environment. See
	// undeclared_provider_alias.go.
	RuleUndeclaredProviderAlias Rule = "undeclared-provider-alias"

	// RuleModuleProviderBlock covers a provider block declared inside a
	// child module, which live mode never consults - the module's resources
	// are served by the root configuration's provider config instead. See
	// module_provider_block.go.
	RuleModuleProviderBlock Rule = "module-provider-block"
)

// UnfindableClause is what an apply with no usable handle costs, as a
// predicate with no subject, so that every sentence saying it can supply
// its own subject and none of them can say it differently.
//
// Two layers say it. This package says it to an author whose resource type
// can never carry a marker at all ([RuleMarkerlessType]); internal/live/stamp
// says it in four sentences to an operator who reached apply with a
// resource it could not stamp, one per [identity.DiscoveryCause], each with
// its own subject - "Applying it unmarked", "leaving it out and applying",
// "applying as written". Five hand copies of the text existed before it was
// named. #111 is what one fact with two wordings costs: the fork's own
// documentation came to describe a guarantee it did not hold because one
// copy was updated and the other was not.
//
// It lives here rather than in internal/live/stamp, where four of the five
// callers are, because this package is the lower of the two: stamp's own
// tests already import it for [ValidForEachKey], so an edge the other way
// is a cycle the compiler refuses.
const UnfindableClause = "would create a resource this configuration can never see again, and every later plan would propose creating another one."

// Severity is how fatal a rule's issues are treated as once they reach a
// command. [SeverityError] is the zero value on purpose: every rule declared
// before this type existed has no severity field set in its ruleInfo entry,
// and a struct literal that never sets the field gets the zero value - so
// adding this type changes no rule's behavior except the one that opts in by
// writing severity: SeverityWarning.
type Severity int

const (
	// SeverityError is fatal: [Diagnostics] renders it as hcl.DiagError, and
	// a caller that treats any issue as blocking (LivePlanCommand,
	// internal/live/check's Findings) keeps doing so.
	SeverityError Severity = iota

	// SeverityWarning is not fatal: [Diagnostics] renders it as
	// hcl.DiagWarning, and a caller built on [HasErrors] or on
	// internal/live/check's Warnings/Findings split lets a run with only
	// this severity's issues proceed.
	SeverityWarning
)

// ruleInfo is the fixed part of every issue a rule produces: the one-line
// summary, the live/LIMITATIONS.md entry that documents the rule, and how
// fatal it is. The variable part (which construct tripped it, where, and
// what to do about this particular one) lives on the Issue.
var ruleInfo = map[Rule]struct {
	summary  string
	docsRef  string
	severity Severity
}{
	RuleProvisioner: {
		summary: "Provisioners are not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "local-exec" / "remote-exec"`,
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
	RuleMarkerlessType: {
		// Deliberately not a variation on RuleUnadmittedType's summary. The
		// two rules share an audience and a symptom, and a reader skimming a
		// findings list has only these lines to tell "not wired yet" from
		// "never will be" apart. This one names the mechanism that is
		// missing rather than the table the type is absent from.
		summary: "Resource type has nowhere to write an ownership marker",
		docsRef: `live/LIMITATIONS.md, "markerless-type"`,
	},
	RuleStateBackend: {
		summary: "State backends are not available under live resource markers",
		docsRef: `live/LIMITATIONS.md, "backend-block" / "cloud-block"`,
		// GitHub issue #210's ruling: demoted from fatal to a warning. Every
		// estate on the onboarding ladder's rungs above language-blocked
		// (internal/live/check/ladder.go, #175) carries only this finding, so
		// treating it as fatal was gating a clean verdict on an edit
		// choudoufu does not need: the live path never reads mod.Backend or
		// mod.CloudConfig (see checkStateBackends and
		// internal/command/live_plan.go's design note on avoiding rather
		// than stubbing the backend), so the block is already inert rather
		// than merely unwise.
		severity: SeverityWarning,
	},
	RuleCountIndex: {
		summary: "count.index is not available in resource arguments",
		docsRef: `live/LIMITATIONS.md, "count-index-in-tag"`,
	},
	RuleForEachKey: {
		summary: "for_each key is outside the marker character set",
		docsRef: `live/LIMITATIONS.md, "foreach-invalid-key"`,
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
	RuleStrictMarkerRepair: {
		summary: "Marker repair setting is not one this build implements",
		docsRef: `live/LIMITATIONS.md, "strict-marker-repair"`,
	},
	RuleStrictMarkers: {
		summary: "Markers selection cannot be read as a selection",
		docsRef: `live/LIMITATIONS.md, "strict-markers"`,
	},
	RuleStrictMarkersUnrecordable: {
		summary: "Markers selection reaches a type no record can identify",
		docsRef: `live/LIMITATIONS.md, "strict-markers-unrecordable"`,
	},
	RuleStrictSecrets: {
		summary: "Secrets setting is not one this fork's schema defines",
		docsRef: `live/LIMITATIONS.md, "strict-secrets"`,
	},
	RuleIgnoreChanges: {
		summary: "Ownership markers would be ignored",
		docsRef: `live/LIMITATIONS.md, "ignore-changes"`,
	},
	RuleModuleProviders: {
		summary: "Module provider mapping is not honoured",
		docsRef: `live/LIMITATIONS.md, "module-providers"`,
	},
	RuleChildLiveConfig: {
		summary: "Live configuration in a child module is not consulted",
		docsRef: `live/LIMITATIONS.md, "child-live-config"`,
	},
	RuleUndeclaredProviderAlias: {
		summary: "Provider configuration is not declared",
		docsRef: `live/LIMITATIONS.md, "undeclared-provider-alias"`,
	},
	RuleModuleProviderBlock: {
		summary: "Provider block in a child module needs count, for_each, enabled or depends_on removed from its call chain",
		docsRef: `live/LIMITATIONS.md, "module-provider-block"`,
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

// Severity reports how fatal an issue this rule produces should be treated
// as. An unregistered rule answers [SeverityError], the same default a
// registered rule gets when its ruleInfo entry never sets the field: see
// [Severity].
func (r Rule) Severity() Severity {
	if info, ok := ruleInfo[r]; ok {
		return info.severity
	}
	return SeverityError
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
	// [RuleUnadmittedType], [RuleMarkerlessType] and [RuleLogicalResource].
	// Every other rule leaves it empty.
	//
	// It exists so that a report can group these rules by type without
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
// issue becomes one diagnostic whose summary is the rule's summary, whose
// detail names the construct, explains the rule, and cites the doc entry
// that documents it, and whose subject is the construct's source range. The
// diagnostic's severity is [Rule.Severity]: hcl.DiagError for every rule
// that has not declared otherwise, hcl.DiagWarning for one that has.
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
		severity := hcl.DiagError
		if issue.Rule.Severity() == SeverityWarning {
			severity = hcl.DiagWarning
		}
		diags = diags.Append(&hcl.Diagnostic{
			Severity: severity,
			Summary:  issue.Rule.Summary(),
			Detail:   detail,
			Subject:  &subject,
		})
	}
	return diags
}

// HasErrors reports whether any issue in issues is error severity - the
// question a caller must ask before treating [CheckWith]'s result as fatal,
// now that a rule can declare [SeverityWarning]. Every issue whose rule has
// not declared otherwise is error severity, so a caller that used to test
// len(issues) > 0 and now tests HasErrors(issues) reads identically for
// every rule except one that has explicitly opted into warning severity.
func HasErrors(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Rule.Severity() != SeverityWarning {
			return true
		}
	}
	return false
}
