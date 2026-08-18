// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package main (taxonomy.go): issue #53's two mechanical classifiers, run by
// buildMapping (mapping.go) over whatever is still via:none with the generic
// unexplainedNoteText after every mapping source - overlay, both name
// heuristics, and former2 - has had a turn. Each classifier requires
// corroboration beyond a name match before it assigns a terminal via; a
// pattern match with no corroborating evidence is left via:none for a later
// family sweep to judge by hand (tools/mapping-gen/overlay.json's tf_only or
// cfn_unmodeled tables), rather than guessed at here. Being conservative is
// the point: a wrong terminal classification is worse than an honestly
// counted unclassified row.
//
// There is no general mechanical cfn-unmodeled classifier here, on purpose.
// Proving a real resource has no CFN model at all - as opposed to simply not
// having been found by name - is exactly the per-family judgment call issue
// #53's own workplan defers to the follow-up sweeps; a classifier that
// mechanically promoted every still-unmatched row to cfn-unmodeled would just
// be via:none relabeled. via:cfn-unmodeled stays curated-only (overlay.go's
// CFNUnmodeled table) for that general population, until a sweep supplies the
// judgment with its evidence.
//
// The deprecated-service branch is a narrow, evidenced exception (issue
// #246): it already requires a type's TF prefix to sit on live/residue.go's
// curated DeprecatedServices list AND its family's entire registered CFN
// footprint to be handler-less (deprecatedServiceEligible) before it says
// anything at all. Given that much corroboration, a further per-type check -
// does any real CFN type anywhere in the registry actually name this
// resource, per the same global name index every other row in this package
// is joined against - is strong enough to settle cfn-unmodeled for that type
// specifically, without generalizing to "unmatched implies unmodeled" for the
// rest of the unclassified population.
package main

import (
	"fmt"
	"regexp"
	"strings"

	residue "github.com/intentius/choudoufu/live"
)

// classifyTaxonomy tries issue #53's mechanical classifiers, in order, for
// one TF type already known to be via:none with the generic unexplained
// note: deprecated-service first (a curated, closed set - see
// deprecatedServiceEligible), then tf-only (open-ended name patterns, each
// requiring the provider's own schema to corroborate). Returns the row
// unclassified (via:none, the generic note) when neither corroborates.
//
// nameIndex is the same global TF-candidate-name -> CFN-type map
// buildMapping builds once (heuristic.go's buildNameIndex) and already
// tried against tf before this function is ever called (classifyRow's own
// name-heuristic branch). It is threaded through anyway, rather than
// trusted to have already answered the question, because
// ov.HeuristicOverrides can make classifyRow skip that branch for a tf
// whose candidate name IS in the index - see the deprecated-service branch
// below for why that distinction is load-bearing (issue #246).
func classifyTaxonomy(tf string, eligibleDeprecated map[string]residue.DeprecatedService, nameIndex map[string]string, identitySchema map[string]bool) Row {
	unexplainedNote := unexplainedNoteText

	if d, ok := deprecatedServiceFor(tf, eligibleDeprecated); ok {
		// Eligibility (deprecatedServiceEligible) is a family-level fact:
		// every registered CFN type under d.CFNPrefix ships no working
		// handler. It says nothing about whether THIS tf has a registered
		// CFN type at all - and issue #246 found seven WAF Classic types
		// where it does not (live/registry.json ships no
		// AWS::WAF::GeoMatchSet, no AWS::WAF::RateBasedRule, ... at all),
		// while eighteen siblings in the same two services do have one and
		// map normally via viaName/viaServiceAlias before ever reaching
		// this function. deprecated-service was being read off the
		// family's status; it needs to be read off this type's own
		// registry presence instead.
		//
		// nameIndex is exactly that per-type signal, already built from
		// the FULL cfnTypes roster (every service, not just this family):
		// if it has an entry for tf, some real CFN type names this
		// resource and the family's deprecation is the honest reason no
		// other source could reach it. If it has none, no CFN type
		// anywhere derives this candidate name, and this type is
		// cfn-unmodeled regardless of which deprecated-services list its
		// TF prefix sits on.
		if _, hasModel := nameIndex[tf]; hasModel {
			note := deprecatedServiceNote(d)
			return Row{TFType: tf, Via: viaDeprecatedService, Note: &note}
		}
		note := cfnUnmodeledInDeprecatedFamilyNote(d)
		return Row{TFType: tf, Via: viaCFNUnmodeled, Note: &note}
	}

	if label, ok := matchTFOnlyPattern(tf); ok {
		// hasSchema is only meaningful when ok is true: identitySchema is
		// nil (Sources.IdentitySchema not supplied, e.g. a test using a
		// minimal Sources) reports ok=false for every type, which correctly
		// classifies nothing rather than treating "no data" the same as
		// "corroborated no identity schema."
		if hasSchema, known := identitySchema[tf]; known && !hasSchema {
			note := tfOnlyNote(label)
			return Row{TFType: tf, Via: viaTFOnly, Note: &note}
		}
	}

	return Row{TFType: tf, Via: viaNone, Note: &unexplainedNote}
}

// --- deprecated-service ---------------------------------------------------

// deprecatedServiceEligible narrows live/residue.go's own curated
// DeprecatedServices (reused, not duplicated - the issue's own instruction)
// to the subset whose entire CFN Registry footprint is handler-less: every
// registry type under the service's CFNPrefix has create, read, update,
// delete and list all false, the same predicate live/registry.json's own
// registry-laggard cohort applies per type. A deprecated service the
// registry still serves a working handler for (Pinpoint, AppStream, in the
// roster this was written against) is left out of this map on purpose - a
// TF type in that service might still get a real, working mapping from a
// future heuristic or overlay entry, so the mechanical pass does not sweep
// it into deprecated-service just because its service is out of policy
// scope. handlerless nil (Sources.RegistryHandlerless not supplied) makes
// every service ineligible, the same "no data, classify nothing" rule
// classifyTaxonomy's tf-only branch follows.
func deprecatedServiceEligible(cfnTypes []string, handlerless map[string]bool) map[string]residue.DeprecatedService {
	if handlerless == nil {
		return nil
	}
	eligible := make(map[string]residue.DeprecatedService, len(residue.DeprecatedServices))
outer:
	for _, d := range residue.DeprecatedServices {
		found := false
		for _, cfn := range cfnTypes {
			if !strings.HasPrefix(cfn, d.CFNPrefix) {
				continue
			}
			found = true
			if !handlerless[cfn] {
				continue outer
			}
		}
		if found {
			eligible[d.TFPrefix] = d
		}
	}
	return eligible
}

// deprecatedServiceFor returns the eligible DeprecatedService whose TFPrefix
// matches tf, if any - the same prefix match live/residue.go's own
// deprecatedPrefixFor applies, scoped to the handler-less-verified subset.
func deprecatedServiceFor(tf string, eligible map[string]residue.DeprecatedService) (residue.DeprecatedService, bool) {
	for _, d := range eligible {
		if strings.HasPrefix(tf, d.TFPrefix) {
			return d, true
		}
	}
	return residue.DeprecatedService{}, false
}

func deprecatedServiceNote(d residue.DeprecatedService) string {
	return fmt.Sprintf("%s: %s (live/residue.go's DeprecatedServices; every %s registry type ships no working handler)", d.Service, d.Reason, d.CFNPrefix)
}

// cfnUnmodeledInDeprecatedFamilyNote is classifyTaxonomy's deprecated-service
// branch when the family-level eligibility check (deprecatedServiceEligible)
// passes but the per-type registry-presence check (nameIndex) does not: the
// TF type's prefix sits on live/residue.go's curated DeprecatedServices list,
// but that is not why it has no row - the CFN Registry simply ships no type
// under d.CFNPrefix that names this specific resource at all (issue #246).
func cfnUnmodeledInDeprecatedFamilyNote(d residue.DeprecatedService) string {
	return fmt.Sprintf("no %s registry type names this specific resource (absent from the CFN Registry roster entirely, not merely unmatched by name) - %s is on live/residue.go's DeprecatedServices list (%s), but that family-level judgment is not why this type has no row; other %s siblings that do have a registry type map normally", d.CFNPrefix, d.Service, d.Reason, d.CFNPrefix)
}

// --- tf-only ---------------------------------------------------------------

// tfOnlyPattern is one name-shape issue #53 names as a tf-only candidate:
// waiters, aws_ami_copy-style one-shot operations, and default_* adopters.
// A pattern match alone is never enough - see classifyTaxonomy's
// identitySchema corroboration requirement.
type tfOnlyPattern struct {
	label string
	re    *regexp.Regexp
	kind  string // what the type is instead, for tfOnlyNote
}

var tfOnlyPatterns = []tfOnlyPattern{
	{
		label: "accepter",
		re:    regexp.MustCompile(`_accepte[rd]$`),
		kind:  "an acceptance-side waiter: flips a pending cross-account request (an invitation, a peering or attachment offer) to accepted, with no cloud resource of its own",
	},
	{
		label: "default_ adopter",
		re:    regexp.MustCompile(`^aws_default_`),
		kind:  "a default_* adopter: brings an AWS-created default resource under management rather than creating one, with no CFN resource of its own",
	},
	{
		label: "validation",
		re:    regexp.MustCompile(`_validation$`),
		kind:  "a validation waiter: records that a validation finished, with no CFN resource of its own",
	},
	{
		label: "copy operation",
		re:    regexp.MustCompile(`_copy$`),
		kind:  "an aws_ami_copy-style copy operation: starts a copy and tracks its result, with no CFN resource of its own",
	},
	{
		label: "invocation",
		re:    regexp.MustCompile(`_invocation$`),
		kind:  "an invocation action: triggers a call and records its result, with no CFN resource of its own",
	},
	{
		label: "confirmation",
		re:    regexp.MustCompile(`_confirmation$`),
		kind:  "a confirmation waiter: flips a pending request to confirmed, with no CFN resource of its own",
	},
	{
		label: "activation",
		re:    regexp.MustCompile(`_activation$`),
		kind:  "an activation action: flips a pending registration to active, with no CFN resource of its own",
	},
	{
		label: "registration",
		re:    regexp.MustCompile(`_registration$`),
		kind:  "a registration action: enrolls an account or resource into a feature, with no CFN resource of its own",
	},
}

// matchTFOnlyPattern returns the first tfOnlyPatterns entry whose regexp
// matches tf, and its kind text, or ok=false when none match.
func matchTFOnlyPattern(tf string) (kind string, ok bool) {
	for _, p := range tfOnlyPatterns {
		if p.re.MatchString(tf) {
			return p.kind, true
		}
	}
	return "", false
}

func tfOnlyNote(kind string) string {
	return fmt.Sprintf("%s (no identity schema in the provider's own schema, so no importable identity either)", kind)
}
