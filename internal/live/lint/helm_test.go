// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// helmReleaseSchema is hashicorp/helm 3.2.0's helm_release, reduced to
// the attributes the admission routes look at: no resource identity
// schema at all, and a metadata that is a computed nested attribute of
// release facts (chart, version, revision) rather than the object-
// metadata block the Kubernetes rule keys on - no labels map, no uid, no
// nested block. Read off `providers schema -json` for the provider on
// 2026-09-12.
func helmReleaseSchema() map[string]providers.Schema {
	return map[string]providers.Schema{
		"helm_release": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"id":         {Type: cty.String, Computed: true},
					"name":       {Type: cty.String, Required: true},
					"namespace":  {Type: cty.String, Optional: true, Computed: true},
					"chart":      {Type: cty.String, Required: true},
					"repository": {Type: cty.String, Optional: true},
					"version":    {Type: cty.String, Optional: true, Computed: true},
					"values":     {Type: cty.List(cty.String), Optional: true},
					"manifest":   {Type: cty.String, Computed: true},
					"resources":  {Type: cty.Map(cty.String), Computed: true},
					"status":     {Type: cty.String, Computed: true},
					"metadata": {
						Computed: true,
						NestedType: &configschema.Object{
							Nesting: configschema.NestingSingle,
							Attributes: map[string]*configschema.Attribute{
								"app_version": {Type: cty.String, Computed: true},
								"chart":       {Type: cty.String, Computed: true},
								"name":        {Type: cty.String, Computed: true},
								"namespace":   {Type: cty.String, Computed: true},
								"revision":    {Type: cty.Number, Computed: true},
								"version":     {Type: cty.String, Computed: true},
							},
						},
					},
				},
			},
		},
	}
}

// TestHelmReleaseIsRefusedAsUnadmitted pins the mechanism GitHub issue
// #1081's fourth item settled on: a helm_release is refused, and the
// refusal is the ordinary unadmitted-type one, with or without the
// provider's schema, so no Helm-specific machinery exists or is needed.
// With the schema offered the refusal says why in the identity layer's
// own words - the provider serves no resource identity schema for the
// type - which is the sentence an operator reads before deciding to
// render the chart into kubernetes_manifest blocks instead
// (site/content/kubernetes/compatibility.md, "Refused").
//
// Proving it red: admit "helm_release" by name at the top of admitted()
// and both halves fail on an empty issue list.
func TestHelmReleaseIsRefusedAsUnadmitted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`
terraform {
  required_providers {
    helm = { source = "hashicorp/helm" }
  }
}

resource "helm_release" "dex" {
  name       = "dex"
  namespace  = "dex"
  repository = "https://charts.dexidp.io"
  chart      = "dex"
  version    = "0.19.1"
}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := loadConfigDir(t, dir)

	without := CheckContext(t.Context(), cfg)
	if len(without) != 1 || without[0].Rule != RuleUnadmittedType || without[0].Type != "helm_release" {
		t.Fatalf("helm_release with no schemas: want exactly one unadmitted-type issue, got %v", without)
	}

	with := CheckWith(t.Context(), cfg, Context{Schemas: helmReleaseSchema()})
	if len(with) != 1 || with[0].Rule != RuleUnadmittedType || with[0].Type != "helm_release" {
		t.Fatalf("helm_release with its schema: want exactly one unadmitted-type issue, got %v", with)
	}
	const why = "serves no resource identity schema for helm_release"
	if !strings.Contains(with[0].Detail, why) {
		t.Errorf("the schema-backed refusal does not say why in the identity layer's words (%q): %s", why, with[0].Detail)
	}
}
