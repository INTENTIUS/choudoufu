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
	// name_prefix sibling at all in the body. Nothing names this role, and
	// the peek this fix adds must never turn that into a false "fine" -
	// [resolutionAt] itself requires an OK resolution, so a config that
	// still needs to fail here has to be checked directly against diags,
	// not through a resolution class.
	foundBroken := false
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary != "Null identity argument" {
			continue
		}
		foundBroken = true
		if !strings.Contains(desc.Detail, "aws_iam_role.broken_no_prefix.name") {
			t.Errorf("Null identity argument diagnostic did not name broken_no_prefix: %q", desc.Detail)
		}
	}
	if !foundBroken {
		t.Fatal("aws_iam_role.broken_no_prefix has no name_prefix sibling and its name evaluates to null; it must still raise \"Null identity argument\", not resolve or defer silently")
	}
	for _, r := range result.All() {
		if r.Addr.String() == "aws_iam_role.broken_no_prefix" {
			t.Errorf("broken_no_prefix resolved (%s); a genuinely unnamed resource must not resolve at all", r.Class)
		}
	}
}
