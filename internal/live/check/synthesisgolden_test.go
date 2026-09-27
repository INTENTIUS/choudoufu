// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1608. TestIdentityGolden runs with no provider schemas
// ("No schemas, deliberately" in identityGoldenAnalyze), so
// synthesizeTypeIdentity returns before it ever reaches a substrate: every
// row in testdata/identity-golden.txt is either a ratified table entry or a
// refusal, never the output of [substrate.Substrate.SynthesizeIdentity].
// Reordering [substrate.All] or breaking a family's SynthesizeIdentity is
// therefore invisible to that golden - #1586 (admission asks each substrate
// to synthesize) could not use it as a red proof for exactly this reason.
//
// This file is the schema-backed companion: a handful of unratified
// resource types, one schema fixture per type, resolved through the same
// [Dir] -> [Analyze] path TestIdentityGolden uses, with [Context.Schemas]
// populated so synthesis actually runs. Three types are shaped like real
// hashicorp/kubernetes 3.2.1 resources (see internal/live/identity's
// objectMetaSchema and manifestSchema, which this mirrors) - one namespaced
// object-metadata type, one cluster-scoped object-metadata type, one
// dynamic-manifest type - and one is shaped like a typical AWS resource
// whose identity schema names a single required argument, the same shape
// internal/live/identity/synthesize_test.go's fallbackSchemas uses for the
// AWS identity-schema route.
//
// The type names are fictitious (synthgolden-prefixed) rather than real
// provider types on purpose, the same reason fallbackSchemas uses "aws_thing"
// rather than a real AWS type: a real type can gain a ratified row at any
// time (table.go's admission table grows every provider bump), which would
// silently retarget this golden at DefaultTable's answer instead of
// [identity.SynthesizeTypeIdentity]'s. A fictitious type has no row and never
// will.
//
// The schemas are hand-written literals, not a live internal/live/pluginschema
// acquisition at test time: every other schema-shaped fixture in this
// repository (fallbackSchemas, routeSchema, objectMetaSchema, manifestSchema)
// is a pinned literal for the same reason TestIdentityGolden gives for
// staying schema-less in the first place - acquiring real schemas costs
// minutes and needs a network, and this golden has to be cheap enough that
// nobody skips it. The two Kubernetes-shaped literals below (metadata block
// and identity schema) were checked against the real
// hashicorp/kubernetes 3.2.1 plugin on 2026-09-26, offline, through
// internal/live/pluginschema.ResourceTypes against the warm
// TF_PLUGIN_CACHE_DIR (no network, no cluster): kubernetes_config_map's
// metadata block and identity schema match synthgoldenObjectMetaSchema(true)
// attribute for attribute, kubernetes_cluster_role's match
// synthgoldenObjectMetaSchema(false), and kubernetes_manifest serves
// "manifest" (required, dynamic) and "object" (computed, dynamic) exactly as
// synthgoldenManifestSchema declares them - the extra wait_for/wait/timeouts
// surface the real type also carries plays no part in
// [markers.ManifestSurface]'s shape test, so it is left out here the same
// way internal/live/identity's own manifestSchema fixture leaves it out.
//
// Red proof (recorded 2026-09-26, see PR body for the actual run): swap
// substrate.All to []Substrate{AWS, Kubernetes} in
// internal/live/substrate/substrate.go. The AWS answer
// (SynthesizeIdentity's FromIdentitySchema route) claims every type
// unconditionally the moment it is asked - see [aws.SynthesizeIdentity]'s
// own doc comment - so the loop over substrate.All breaks on the first
// type it is asked about and the Kubernetes object-metadata and manifest
// routes never run. All three Kubernetes-shaped instances stop resolving
// (their identity schema, where they carry one, names attributes no
// top-level argument supplies) and TestSynthesisGolden fails with three
// removed rows; the AWS-shaped instance is unaffected, because AWS was
// always going to answer it either way. Restoring substrate.All to
// []Substrate{Kubernetes, AWS} makes it pass again.

const synthesisGoldenPath = "testdata/synthesis-golden.txt"

// synthesisGoldenConfig declares one resource per fixture schema below.
// None of the four types is real; each is shaped like a real provider
// convention (see the schema functions) so that the shape, not the name,
// is what a family's SynthesizeIdentity has to recognise.
const synthesisGoldenConfig = `
terraform {
  required_providers {
    kubernetes = { source = "hashicorp/kubernetes" }
    aws        = { source = "hashicorp/aws" }
  }
}

resource "kubernetes_synthgolden_namespaced" "a" {
  metadata {
    name      = "app-config"
    namespace = "smoke-k8s"
  }
}

resource "kubernetes_synthgolden_clusterscoped" "b" {
  metadata {
    name = "cluster-thing"
  }
}

resource "kubernetes_synthgolden_manifest" "c" {
  manifest = {
    apiVersion = "stable.example.com/v1"
    kind       = "Widget"
    metadata = {
      name      = "my-widget"
      namespace = "smoke-k8s"
    }
  }
}

resource "aws_synthgolden_thing" "d" {
  name = "alpha"
}
`

// synthgoldenObjectMetaSchema is hashicorp/kubernetes 3.2.1's object-metadata
// shape (internal/live/identity's objectMetaSchema pins the identical
// fixture): a "metadata" nested block of list nesting with at most one item,
// a settable "name", a computed "uid" the identity's own IdentitySchema
// cannot reach from any top-level argument, and - for a namespaced kind - a
// settable "namespace". The IdentitySchema requiring api_version, kind and
// name is the real provider's own: none of the three is a configuration
// argument this schema exposes, which is exactly why
// [substrate.Kubernetes.SynthesizeIdentity]'s object-metadata route has to
// run before the AWS identity-schema route ever sees this shape.
func synthgoldenObjectMetaSchema(namespaced bool) providers.Schema {
	attrs := map[string]*configschema.Attribute{
		"name":             {Type: cty.String, Optional: true, Computed: true},
		"generation":       {Type: cty.Number, Computed: true},
		"labels":           {Type: cty.Map(cty.String), Optional: true},
		"annotations":      {Type: cty.Map(cty.String), Optional: true},
		"generate_name":    {Type: cty.String, Optional: true},
		"resource_version": {Type: cty.String, Computed: true},
		"uid":              {Type: cty.String, Computed: true},
	}
	if namespaced {
		attrs["namespace"] = &configschema.Attribute{Type: cty.String, Optional: true}
	}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: configschema.Block{Attributes: attrs}, Nesting: configschema.NestingList, MinItems: 1, MaxItems: 1},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

// synthgoldenManifestSchema is hashicorp/kubernetes 3.2.1's kubernetes_manifest
// shape (internal/live/identity's manifestSchema pins the identical
// fixture): one required dynamic manifest, one computed dynamic object, and
// the provider's own field_manager block beside them. No IdentitySchema, the
// real provider's own shape for this type - the natural key lives inside the
// dynamic manifest argument, which is what
// [substrate.Kubernetes.SynthesizeIdentity]'s manifest route reads.
func synthgoldenManifestSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"manifest":        {Type: cty.DynamicPseudoType, Required: true},
			"object":          {Type: cty.DynamicPseudoType, Optional: true, Computed: true},
			"computed_fields": {Type: cty.List(cty.String), Optional: true},
		},
		BlockTypes: map[string]*configschema.NestedBlock{
			"field_manager": {Nesting: configschema.NestingList, MaxItems: 1, Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
				"name":            {Type: cty.String, Optional: true},
				"force_conflicts": {Type: cty.Bool, Optional: true},
			}}},
		},
	}}
}

// synthgoldenAWSThingSchema is the AWS identity-schema route's ordinary
// case: one required top-level argument is the whole identity, and nothing
// else is marked optional-for-import, so the schema alone settles it with
// no corroboration question to ask. internal/live/identity/synthesize_test.go's
// fallbackSchemas gives aws_thing an account_id/region pair too, but only
// because that file also has aws_child_thing to corroborate them as context
// ([isContextAttr] requires at least one OTHER type in the schema map making
// the same claim); this golden's schema map carries exactly one AWS-shaped
// type, so account_id/region are left out rather than tripping that
// ambiguity on a single, uncorroborated type.
func synthgoldenAWSThingSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"name": {Type: cty.String, Required: true},
				"id":   {Type: cty.String, Optional: true, Computed: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"name": {Type: cty.String, Required: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

func synthesisGoldenSchemas() map[string]providers.Schema {
	return map[string]providers.Schema{
		"kubernetes_synthgolden_namespaced":    synthgoldenObjectMetaSchema(true),
		"kubernetes_synthgolden_clusterscoped": synthgoldenObjectMetaSchema(false),
		"kubernetes_synthgolden_manifest":      synthgoldenManifestSchema(),
		"aws_synthgolden_thing":                synthgoldenAWSThingSchema(),
	}
}

// TestSynthesisGolden pins what each fixture resolves TO, not merely
// whether it resolves, for the same reason TestIdentityGolden's own doc
// comment gives: a wrong rendered value refuses nothing and moves no count.
// Regenerate with:
//
//	env -u PWD go test -C "$(git rev-parse --show-toplevel)" ./internal/live/check -run TestSynthesisGolden -update
//
// then read the diff - see this file's package doc comment above for what a
// reorder of substrate.All does to it.
func TestSynthesisGolden(t *testing.T) {
	for _, typeName := range []string{
		"kubernetes_synthgolden_namespaced",
		"kubernetes_synthgolden_clusterscoped",
		"kubernetes_synthgolden_manifest",
		"aws_synthgolden_thing",
	} {
		if _, has := identity.LookupType(typeName); has {
			t.Fatalf("%s gained a ratified row; this golden exists to measure synthesis and needs a type with none - pick another fictitious name", typeName)
		}
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(synthesisGoldenConfig), 0o600); err != nil {
		t.Fatalf("writing fixture: %s", err)
	}

	report := Dir(context.Background(), dir, Context{Schemas: synthesisGoldenSchemas()})
	got := synthesisGoldenRender(report)

	if *updateIdentityGolden {
		if err := os.WriteFile(synthesisGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("writing %s: %s", synthesisGoldenPath, err)
		}
		t.Logf("wrote %s (%d bytes)", synthesisGoldenPath, len(got))
		return
	}

	wantBytes, err := os.ReadFile(synthesisGoldenPath)
	if err != nil {
		t.Fatalf("reading %s: %s\nRun the test with -update to create it.", synthesisGoldenPath, err)
	}
	if want := string(wantBytes); got != want {
		t.Errorf("synthesis golden mismatch (see this file's -update comment to regenerate after a deliberate change):\n--- want ---\n%s--- got ---\n%s", want, got)
	}
}

// synthesisGoldenRender is deliberately independent of
// identityGoldenFile/identityGoldenRows' machinery (the shape block, the
// digest, the two-section split): this golden covers four fixed fixtures,
// not a 375-directory sweep, so a flat, small, hand-readable file serves it
// better than reusing the larger instrument's format. It does reuse
// renderedIdentity and renderedIdentityAttrs, the two functions that turn a
// [identity.Resolution] into the string this fork would actually write into
// a tag or hand to an import - reimplementing them here is exactly the kind
// of drift #1608 is about.
func synthesisGoldenRender(report Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# testdata/synthesis-golden.txt - GitHub issue #1608\n")
	fmt.Fprintf(&b, "# Generated by TestSynthesisGolden -update. Do not hand-edit.\n")
	fmt.Fprintf(&b, "# instances=%d blocked=%v findings=%d\n", report.Instances, report.Blocked(), len(report.Findings))

	findings := append([]Finding(nil), report.Findings...)
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	for _, f := range findings {
		fmt.Fprintf(&b, "# finding %s sites=%d\n", f.ID, len(f.Sites))
	}

	ids := append([]identity.Resolution(nil), report.Identities...)
	sort.Slice(ids, func(i, j int) bool { return ids[i].Addr.String() < ids[j].Addr.String() })
	fmt.Fprintf(&b, "#\n# address <TAB> class <TAB> rendered identity <TAB> identity attributes\n")
	for _, res := range ids {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", res.Addr.String(), res.Class, renderedIdentity(res), renderedIdentityAttrs(res))
	}
	return b.String()
}
