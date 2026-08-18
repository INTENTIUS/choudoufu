// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The name/name_prefix convention [TestNamePrefixDefersToDiscovery] covers
// for an OMITTED base argument is at least as often spelled through a pair
// of complementary conditionals instead - both "name" and "name_prefix"
// written in every instance, exactly one evaluating non-null - which is
// terraform-aws-modules' own aws_iam_role shape (and eks's, and several
// others; GitHub issue #184's corpus scan found this pattern behind roughly
// a sixth of that issue's "Unresolvable identity" cascades, from
// aws_iam_role_policy_attachment.role and siblings depending on an
// aws_iam_role whose own "name" argument evaluated to null). Both arguments
// are syntactically present either way, so firstPresent alone cannot tell
// "named through name_prefix" apart from "named", and a naive evaluation of
// whichever was found reports "Null identity argument" - a hard, wrong
// refusal for an instance that resolves to ClassNeedsDiscovery under every
// other spelling of the identical convention.
//
// GitHub issue #289 changed the adversarial case below: aws_iam_role is
// taggable and enumerable, so [resolver.markerFallback] now withdraws
// "Null identity argument" for it too, the same way it withdraws every
// other identity-VALUE refusal on a [DiscoverableFallbackTypes] member.
// What this test still pins is that the withdrawal happens - the peek
// added for the two cases above must never turn the null value itself
// into a false "fine" (a CONCRETE resolution built from nothing) - not
// that the type keeps refusing outright, which it no longer does.
func TestNamePrefixConditionalNullDefersToDiscovery(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "name-prefix-conditional-null"), nil)

	result, diags := Resolve(context.Background(), cfg)

	prefixed := resolutionAt(t, result, "aws_iam_role.prefixed")
	if prefixed.Class != ClassNeedsDiscovery {
		t.Fatalf("prefixed resolved %s; name evaluated to null with a name_prefix sibling present and non-null, so it should be NEEDS_DISCOVERY like the omitted-argument spelling", prefixed.Class)
	}

	named := resolutionAt(t, result, "aws_iam_role.named_conditional")
	if named.Class != ClassConcrete {
		t.Fatalf("named_conditional resolved %s; its name argument evaluates to a real string and should stay CONCRETE regardless of its null name_prefix sibling", named.Class)
	}
	if named.ImportID != "explicit-name-2" {
		t.Errorf("named_conditional resolved to %q, want %q", named.ImportID, "explicit-name-2")
	}

	// The adversarial case: name evaluates to null and there is no
	// name_prefix sibling at all in the body. Nothing names this role from
	// its OWN configuration, and the peek added for the two cases above
	// must never turn that into a false "fine" straight off the null value:
	// the only acceptable way out is through the ordinary
	// [DiscoveryMarkerFallback] door every other unresolvable identity on a
	// [DiscoverableFallbackTypes] member takes, never a fabricated CONCRETE
	// resolution built from nothing.
	//
	// diags no longer carries "Null identity argument" for this instance -
	// [resolver.markerFallback] withdraws it once it decides every
	// diagnostic the instance raised is answerable - so this can no longer
	// be checked by finding the diagnostic. What is checked instead is that
	// no error diagnostic AT ALL still names broken_no_prefix (the peek
	// really did let resolution run its ordinary course rather than
	// special-casing the null value itself) alongside the resolution class.
	for _, d := range diags {
		desc := d.Description()
		if strings.Contains(desc.Detail, "aws_iam_role.broken_no_prefix") {
			t.Errorf("a diagnostic still names broken_no_prefix after the marker fallback should have withdrawn it: %q: %q", desc.Summary, desc.Detail)
		}
	}
	broken := resolutionAt(t, result, "aws_iam_role.broken_no_prefix")
	if broken.Class != ClassNeedsDiscovery {
		t.Fatalf("broken_no_prefix resolved %s; a name that evaluates to null with no name_prefix sibling must defer to the marker (aws_iam_role is taggable and enumerable), never resolve CONCRETE from nothing", broken.Class)
	}
	if broken.Cause != DiscoveryMarkerFallback {
		t.Errorf("broken_no_prefix's discovery cause is %s, want %s - it must have reached NEEDS_DISCOVERY through the withdrawn-refusal door, not some other path", broken.Cause, DiscoveryMarkerFallback)
	}
	if broken.ImportID != "" {
		t.Errorf("broken_no_prefix has import ID %q; a NEEDS_DISCOVERY resolution must never carry one", broken.ImportID)
	}
}
