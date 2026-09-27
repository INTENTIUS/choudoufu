// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"os"
	"testing"
)

// substratesPath and siteSubstratesCopy mirror smokeClaimsPath and
// siteClaimsCopy (smoke_claims_test.go): live/substrates.json is generated
// (tools/substrates-gen, GitHub issue #1588) rather than hand-owned like
// live/smoke/claims.json, but the site still renders a byte-identical
// copy, kept in sync the same way - `go run ./tools/substrates-gen -render`
// - rather than by hand.
const (
	substratesPath     = "substrates.json"
	siteSubstratesCopy = "../site/data/substrates.json"
)

// TestSubstratesSiteCopyMatches: site/data/substrates.json is a
// byte-for-byte copy of live/substrates.json. Proving it red: edit either
// file, or run `go run ./tools/substrates-gen` without the `-render` step
// afterward.
func TestSubstratesSiteCopyMatches(t *testing.T) {
	src, err := os.ReadFile(substratesPath)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := os.ReadFile(siteSubstratesCopy)
	if err != nil {
		t.Fatalf("read %s: %v (go run ./tools/substrates-gen -render)", siteSubstratesCopy, err)
	}
	if string(src) != string(cp) {
		t.Errorf("%s differs from %s; run `go run ./tools/substrates-gen -render` (live/substrates.json is generated, the site copy is not regenerated independently)", siteSubstratesCopy, substratesPath)
	}
}
