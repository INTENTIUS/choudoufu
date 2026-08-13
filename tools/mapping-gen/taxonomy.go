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
// There is no mechanical cfn-unmodeled classifier here on purpose. Proving a
// real resource has no CFN model at all - as opposed to simply not having
// been found by name - is exactly the per-family judgment call issue #53's
// own workplan defers to the follow-up sweeps; a classifier that mechanically
// promoted every still-unmatched row to cfn-unmodeled would just be via:none
// relabeled; via:cfn-unmodeled stays curated-only (overlay.go's CFNUnmodeled
// table) until a sweep supplies that judgment with its evidence.
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
func classifyTaxonomy(tf string, eligibleDeprecated map[string]residue.DeprecatedService, identitySchema map[string]bool) Row {
	unexplainedNote := unexplainedNoteText

	if d, ok := deprecatedServiceFor(tf, eligibleDeprecated); ok {
		note := deprecatedServiceNote(d)
		return Row{TFType: tf, Via: viaDeprecatedService, Note: &note}
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
