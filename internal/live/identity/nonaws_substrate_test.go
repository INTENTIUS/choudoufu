// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/providers"
)

// TestOrphanWarningAsksTheSubstrateNotTheFlag is GitHub issue #1581.
//
// [resolver.warnUnsweepableTypes] used to suppress its warning whenever
// entry.NonAWSProvider was true, on the assumption that this meant "the
// Kubernetes sweep covers it" (GitHub issue #980's own fix). But
// NonAWSProvider is set the moment a type's SCHEMA matches the Kubernetes
// object-metadata or manifest shape ([synthesizeMetadataIdentity],
// [synthesizeManifestIdentity]) - it says nothing about which provider
// actually serves the type, and internal/command/live_plan.go only ever
// builds a Kubernetes sweep leg for a provider configuration whose own
// address has Type == "kubernetes" (see providerAddr.Provider.Type ==
// "kubernetes" there). A resource from a different, unrelated provider
// whose schema happens to share that exact shape gets NonAWSProvider set
// too, and used to lose its warning even though no sweep leg will ever
// reach it.
//
// acme_widget below is exactly that: a fake type, on no admission-table
// row, whose schema is object-metadata shaped ([objectMetaSchema], already
// used by metadata_test.go for the real Kubernetes rows) but which is not a
// Kubernetes type at all. On main this test fails: the warning is wrongly
// suppressed. After the fix, [internal/live/substrate.Sweeps] answers by
// type name rather than by shape, so acme_widget keeps its warning and a
// real kubernetes_* type keeps today's silence.
func TestOrphanWarningAsksTheSubstrateNotTheFlag(t *testing.T) {
	t.Run("a non-AWS, non-Kubernetes type sharing the Kubernetes shape keeps the warning", func(t *testing.T) {
		if _, hasRow := LookupType("acme_widget"); hasRow {
			t.Fatal("acme_widget now has a row in DefaultTable; this fixture no longer exercises the schema-fallback path")
		}

		schema := objectMetaSchema(false)
		schemas := map[string]providers.Schema{"acme_widget": schema}

		// Confirm the fixture really does synthesize with NonAWSProvider
		// set, or this test proves nothing about the bug it targets.
		synthesized, ok := SynthesizeTypeIdentity("acme_widget", schemas, nil)
		if !ok {
			t.Fatal("SynthesizeTypeIdentity refused acme_widget against its own object-metadata schema; the fixture no longer exercises the shape this bug depends on")
		}
		if !synthesized.NonAWSProvider {
			t.Fatal("acme_widget did not synthesize with NonAWSProvider set; the fixture no longer exercises the flag this bug depends on")
		}

		dir := t.TempDir()
		main := `resource "acme_widget" "x" {
  metadata {
    name = "foo"
  }
}
`
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o644); err != nil {
			t.Fatalf("writing fixture: %s", err)
		}

		cfg := loadConfig(t, dir, nil)
		_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		assertNoErrors(t, diags)

		warned := orphanWarnings(diags)
		if len(warned) != 1 {
			t.Fatalf("got %d no-orphan-recovery warnings for acme_widget, want 1 (no substrate claims it, so the warning must fire):\n%s", len(warned), renderDiags(diags))
		}
	})

	t.Run("a Kubernetes type outside the table keeps today's silence", func(t *testing.T) {
		// kubernetes_namespace already has a row, so it would skip the
		// substrate check entirely (the outer "no row" gate) and prove
		// nothing about this fix. A Kubernetes-shaped type with NO row is
		// what actually exercises the substrate answering "yes" instead of
		// the flag.
		if _, hasRow := LookupType("kubernetes_fake_widget"); hasRow {
			t.Fatal("kubernetes_fake_widget now has a row in DefaultTable; this fixture no longer exercises the schema-fallback path")
		}

		schemas := map[string]providers.Schema{"kubernetes_fake_widget": objectMetaSchema(false)}

		dir := t.TempDir()
		main := `resource "kubernetes_fake_widget" "x" {
  metadata {
    name = "foo"
  }
}
`
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(main), 0o644); err != nil {
			t.Fatalf("writing fixture: %s", err)
		}

		cfg := loadConfig(t, dir, nil)
		_, diags := ResolveWith(context.Background(), cfg, Context{Schemas: schemas})
		assertNoErrors(t, diags)

		if warned := orphanWarnings(diags); len(warned) != 0 {
			t.Errorf("kubernetes_fake_widget is a Kubernetes-shaped type under the kubernetes_ prefix, which the Kubernetes sweep leg's own provider-type gate covers, so no warning should fire, but got %d:\n%s", len(warned), renderDiags(diags))
		}
	})
}
