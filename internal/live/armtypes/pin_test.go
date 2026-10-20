// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"bytes"
	"os"
	"testing"
)

// SourceEnv names an azure/terraform-provider-azapi checkout at the pinned
// tag. CI has none, so the regeneration leg skips there; the always-on
// tests in armtypes_test.go pin the counts and known rows instead.
const SourceEnv = "ARMTYPES_AZAPI_SRC"

// TestTableMatchesPinnedSource regenerates the table from the checkout
// named by ARMTYPES_AZAPI_SRC and requires it byte-identical to the
// committed one. A checkout at any other commit is a failure, not a skip:
// it would compare against data the table does not claim to describe.
func TestTableMatchesPinnedSource(t *testing.T) {
	src := os.Getenv(SourceEnv)
	if src == "" {
		t.Skipf("%s is not set: the committed table is not regenerated. Set it to an azapi checkout at %s (%s) to run this leg.",
			SourceEnv, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	version, commit, err := SourcePin(src)
	if err != nil {
		t.Fatal(err)
	}
	if version != pinnedAzapiVersion || commit != pinnedAzapiCommit {
		t.Fatalf("%s is azapi %s (%s); the table is pinned to %s (%s)", src, version, commit, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	tbl, err := Generate(src, version, commit)
	if err != nil {
		t.Fatal(err)
	}
	if got := tbl.Render(); !bytes.Equal(got, committedTable) {
		t.Fatalf("regenerating from %s does not reproduce %s (%d bytes regenerated, %d committed); run go run ./tools/armtypes-gen -azapi-src %s",
			src, TablePath, len(got), len(committedTable), src)
	}
}
