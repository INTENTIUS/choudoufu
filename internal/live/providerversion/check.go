// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package providerversion answers issue #63: a configuration's provider
// pin and the admission table's evidence basis are two independent facts
// that can drift apart with nobody told. Every admitted type's import
// grammar and identity schema were verified against one provider version
// (live/survey.json's provider_version header, read here through
// [residue.EvidenceVersion]); a configuration can pin any version its own
// required_providers block or dependency lock file names. Nothing checked
// that the two agreed before this package existed.
//
// [Check] is the whole package: one comparison, one diagnostic. It is
// deliberately not a gate - a mismatch does not fail a run, because the
// admission table's evidence is a caution about import grammar and
// identity schemas, not a compatibility promise the provider itself makes
// or breaks at any particular version boundary. Callers decide when in
// their pipeline to run it (internal/command's live-plan, live-mode, and
// live-mv entry points each call it once, from the resolved version their
// own dependency lock file reports) and render whatever it returns through
// their ordinary diagnostics path.
package providerversion

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/getproviders"
	"github.com/intentius/choudoufu/internal/tfdiags"
	residue "github.com/intentius/choudoufu/live"
)

// Check compares resolved - the hashicorp/aws provider version this run's
// dependency lock file selected, in any form [getproviders.ParseVersion]
// accepts - against the admission evidence version
// ([residue.EvidenceVersion], live/survey.json's provider_version header).
//
// An exact match returns nil: silence is the whole answer, deliberately,
// so that the overwhelming common case (a configuration pinned to the
// version the table was verified against) produces nothing for a user to
// read. Any difference - major, minor, or patch - returns exactly one
// warning diagnostic naming both versions and what the difference does and
// does not put at risk. It is never an error: see the package doc comment
// for why this stays a warning no matter how far the versions have
// drifted.
//
// Patch-level differences warn too, on purpose, rather than being folded
// into "close enough". tools/admission-pipeline/detect.go's own drift
// check already treats a patch bump as significant: detectProvider flags
// Drifted on any string difference between the pinned and current
// versions ("Drifted: latest != pinned", no major/minor carve-out), which
// is what sends the admission table through re-verification in the first
// place. That policy exists because import grammar and identity-schema
// behavior are not covered by the AWS provider's own semver promises -
// those promises are about its Terraform-facing schema, not about
// `terraform import` ID formats or identity-schema details - so a patch
// release is exactly as capable of moving them as a minor one. A user
// pinned one patch version off the evidence basis is in the same position
// this repository's own pipeline treats as worth re-checking, and warning
// only on major/minor differences would tell that user nothing is wrong
// when the pipeline's own policy says otherwise. Warning on any difference
// costs nothing extra for the common case, where match is exact and Check
// stays silent regardless.
//
// resolved == "" is silence, not a warning: it means the caller found no
// locked hashicorp/aws version to compare (no lock entry, no aws provider
// in the configuration at all, or a dev-override run with nothing written
// to the lock file), and Check has nothing to compare against. Reporting a
// version mismatch with no version to name would be a false alarm rather
// than a finding.
func Check(resolved string) tfdiags.Diagnostics {
	if resolved == "" {
		return nil
	}
	evidence := residue.EvidenceVersion()
	if evidence == "" {
		return nil
	}
	if versionsEqual(resolved, evidence) {
		return nil
	}

	provider := residue.EvidenceProvider()
	return tfdiags.Diagnostics{
		tfdiags.Sourceless(
			tfdiags.Warning,
			"Provider version does not match the admission evidence version",
			fmt.Sprintf(
				"This run resolved %s %s. Live resource markers' admission table - which resource types are accepted, what their import IDs look like, and how their identity is resolved - was verified against %s %s (live/survey.json). "+
					"Import grammar and identity schemas are the parts of that table a provider version change can invalidate: an import ID format or an identity attribute that changed between %[2]s and %[4]s would misbehave here with no further warning. "+
					"Taggability judgments are not version-sensitive in the same way and rarely change between provider releases. "+
					"This is a caution, not a failure: the plan continues.",
				provider, resolved, provider, evidence,
			),
		),
	}
}

// versionsEqual parses both strings as provider versions and compares them
// exactly (major, minor, patch, and any prerelease segment), so that
// formatting differences alone - "6.58.0" against a hypothetical "v6.58.0"
// - never manufacture a false warning. A string either side fails to parse
// falls back to a literal comparison rather than treating "cannot compare"
// as either a silent match or a forced warning.
func versionsEqual(a, b string) bool {
	if a == b {
		return true
	}
	va, errA := getproviders.ParseVersion(a)
	vb, errB := getproviders.ParseVersion(b)
	if errA != nil || errB != nil {
		return false
	}
	return va.Same(vb)
}
