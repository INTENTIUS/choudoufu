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

// TestTagLimitsMatchPinnedSource is TestTableMatchesPinnedSource for
// taglimits_generated.txt, from the same checkout.
func TestTagLimitsMatchPinnedSource(t *testing.T) {
	src := os.Getenv(SourceEnv)
	if src == "" {
		t.Skipf("%s is not set: the committed tag limits are not regenerated. Set it to an azapi checkout at %s (%s) to run this leg.",
			SourceEnv, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	version, commit, err := SourcePin(src)
	if err != nil {
		t.Fatal(err)
	}
	if version != pinnedAzapiVersion || commit != pinnedAzapiCommit {
		t.Fatalf("%s is azapi %s (%s); the limits are pinned to %s (%s)", src, version, commit, pinnedAzapiVersion, pinnedAzapiCommit)
	}
	lims, err := GenerateTagLimits(src, version, commit)
	if err != nil {
		t.Fatal(err)
	}
	if got := lims.Render(); !bytes.Equal(got, committedTagLimits) {
		t.Fatalf("regenerating from %s does not reproduce %s (%d bytes regenerated, %d committed); run go run ./tools/armtypes-gen -azapi-src %s",
			src, TagLimitsPath, len(got), len(committedTagLimits), src)
	}
}

// RMSourceEnv and RMSchemaEnv name an azurerm checkout at the pinned tag
// and the `tofu providers schema -json` output for it: the variables
// tools/azurerm-id-gen's pin test reads, so one setting runs both.
const (
	RMSourceEnv = "AZURERM_SRC"
	RMSchemaEnv = "AZURERM_SCHEMA"
)

// TestRMTagsMatchPinnedSource regenerates the azurerm tag facts and
// requires them byte-identical to the committed file. A checkout or
// schema other than the pinned one is a failure, not a skip.
func TestRMTagsMatchPinnedSource(t *testing.T) {
	src, schema := os.Getenv(RMSourceEnv), os.Getenv(RMSchemaEnv)
	if src == "" || schema == "" {
		t.Skipf("%s and %s are not both set: the committed azurerm tag facts are not regenerated. Set them to an azurerm checkout at %s (%s) and its schema to run this leg.",
			RMSourceEnv, RMSchemaEnv, pinnedAzurermVersion, pinnedAzurermCommit)
	}
	version, commit, err := AzurermSourcePin(src)
	if err != nil {
		t.Fatal(err)
	}
	if version != pinnedAzurermVersion || commit != pinnedAzurermCommit {
		t.Fatalf("%s is azurerm %s (%s); the facts are pinned to %s (%s)", src, version, commit, pinnedAzurermVersion, pinnedAzurermCommit)
	}
	facts, err := GenerateAzurermTags(src, schema, version, commit)
	if err != nil {
		t.Fatal(err)
	}
	if facts.SchemaSHA256 != pinnedAzurermSchemaSHA256 {
		t.Fatalf("%s digests to %s; the facts are pinned to schema %s", schema, facts.SchemaSHA256, pinnedAzurermSchemaSHA256)
	}
	if got := facts.Render(); !bytes.Equal(got, committedRMTags) {
		t.Fatalf("regenerating from %s does not reproduce %s (%d bytes regenerated, %d committed); run go run ./tools/azurerm-tags-gen -src %s -schema %s",
			src, RMTagsPath, len(got), len(committedRMTags), src, schema)
	}
}
