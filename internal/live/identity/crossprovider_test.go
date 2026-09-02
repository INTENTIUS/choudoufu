// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// ---------------------------------------------------------------------------
// GitHub issue #218's own measurement found kubernetes, github, fastly and
// tfe gaining zero types from the schema fallback, and attributed it to "no
// type in those providers passes the earlier derivable() bar at all" rather
// than to the (now-fixed) onlyContext AWS assumption. The fixtures below are
// not invented to agree with the rule: they are transcribed from schemas
// read from the real, latest-release provider plugins (hashicorp/kubernetes
// 3.2.1, hashicorp/tfe 0.80.0, integrations/github 6.13.0, fastly/fastly
// 9.6.0, acquired 2026-08-16 via internal/live/pluginschema against
// TF_PLUGIN_CACHE_DIR) - the same acquisition path tools/corpus-gen and
// tools/survey-gen use. Each test pins one shape that keeps a whole provider
// off the schema fallback and is NOT an AWS assumption in disguise:
//
//   - kubernetes: every one of the 41 resource types that carries an
//     identity schema (out of 81 total) requires the identical triple
//     api_version/kind/name for import. name is reachable through the
//     type's own nested "metadata" block (metadata.name), but api_version
//     and kind appear nowhere in the type's configuration schema, nested or
//     not - they are Kubernetes GroupVersionKind constants the provider's
//     Go code hardcodes per typed resource (a kubernetes_config_map is
//     always apiVersion "v1", kind "ConfigMap"), never written by any
//     configuration and never given a description, default, or any other
//     schema-carried hint of the constant's value. There is no rule over
//     the two schemas that can produce "v1"/"ConfigMap" without a
//     per-type table entry naming them - which is exactly the kind of
//     hand-maintenance the schema fallback exists to avoid, not reproduce.
//   - tfe: of the 13 resource types with an identity schema, 12 require
//     only "id", and every one of those marks id Computed-only in its own
//     block - the identical "id is Optional+Computed/Computed-only, so the
//     API mints it" shape that already (correctly) refuses aws_vpc. That
//     is provider-neutral SDK convention, not an AWS-specific one; TFE's
//     own schema draws the same line the AWS legacy SDK does. The 13th,
//     tfe_variable, requires "configurable_id", a name that appears
//     nowhere in tfe_variable's own block (a variable is scoped to
//     *either* workspace_id *or* variable_set_id, and the identity schema
//     flattens the choice to one synthetic name neither argument shares),
//     stacked on the same Computed-only id.
//   - github and fastly serve NO resource identity schema at all, on any
//     of their combined 139 resource types, at their latest published
//     releases. [SynthesizeTypeIdentity] and [Derivable] both start by
//     requiring schema.IdentitySchema != nil; there is no rule change here
//     that manufactures identity data a provider has not published. The
//     schema fallback is unavailable to these two providers entirely,
//     independent of derivable()'s clauses - the earliest possible refusal
//     already fires correctly.
// ---------------------------------------------------------------------------

// kubernetesConfigMapSchema is kubernetes_config_map's real shape (provider
// 3.2.1): api_version/kind/name required for import, namespace optional
// (context, like AWS's account_id/region), and a "metadata" nested block
// that carries name/namespace/labels/annotations - but no top-level
// api_version or kind anywhere in the block.
func kubernetesConfigMapSchema() providers.Schema {
	metadata := configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"name":             {Type: cty.String, Optional: true, Computed: true},
			"namespace":        {Type: cty.String, Optional: true},
			"generation":       {Type: cty.Number, Computed: true},
			"labels":           {Type: cty.Map(cty.String), Optional: true},
			"annotations":      {Type: cty.Map(cty.String), Optional: true},
			"generate_name":    {Type: cty.String, Optional: true},
			"resource_version": {Type: cty.String, Computed: true},
			"uid":              {Type: cty.String, Computed: true},
		},
	}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":          {Type: cty.String, Optional: true, Computed: true},
				"binary_data": {Type: cty.Map(cty.String), Optional: true},
				"data":        {Type: cty.Map(cty.String), Optional: true},
				"immutable":   {Type: cty.Bool, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {Block: metadata, Nesting: configschema.NestingList},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"api_version": {Type: cty.String, Required: true},
				"kind":        {Type: cty.String, Required: true},
				"name":        {Type: cty.String, Required: true},
				"namespace":   {Type: cty.String, Optional: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

func TestKubernetesAPIVersionAndKindHaveNoConfigSource(t *testing.T) {
	schemas := map[string]providers.Schema{"kubernetes_config_map": kubernetesConfigMapSchema()}

	if der := Derivable(schemas); len(der) != 0 {
		t.Errorf("kubernetes_config_map admitted as %v; api_version and kind name nothing in the block, nested or not", der)
	}
	if _, ok := SynthesizeTypeIdentity("kubernetes_config_map", schemas, nil); ok {
		t.Error("kubernetes_config_map was synthesized despite api_version/kind having no schema-derivable source")
	}
	block := schemas["kubernetes_config_map"].Block
	if _, ok := block.Attributes["api_version"]; ok {
		t.Fatal("test fixture drifted: api_version now exists at top level, this test no longer exercises the real shape")
	}
	if _, ok := block.BlockTypes["metadata"].Block.Attributes["api_version"]; ok {
		t.Fatal("test fixture drifted: api_version now exists nested under metadata, this test no longer exercises the real shape")
	}
}

// tfeWorkspaceSchema is tfe_workspace's real shape (provider 0.80.0): the
// only required identity attribute is "id", and id is Computed-only in the
// type's own block - the provider mints it, exactly like aws_vpc.
func tfeWorkspaceSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":   {Type: cty.String, Computed: true},
				"name": {Type: cty.String, Required: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Required: true},
				"hostname": {Type: cty.String, Optional: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

// tfeVariableSchema is tfe_variable's real shape: identity requires
// "configurable_id" and "id", but the type's own block has neither a
// configurable_id argument (it has workspace_id and variable_set_id
// instead - the identity schema flattens the either/or choice to a name the
// block does not share) nor a settable id (Computed-only, same as
// tfe_workspace).
func tfeVariableSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":              {Type: cty.String, Computed: true},
				"key":             {Type: cty.String, Required: true},
				"category":        {Type: cty.String, Required: true},
				"variable_set_id": {Type: cty.String, Optional: true},
				"workspace_id":    {Type: cty.String, Optional: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"configurable_id": {Type: cty.String, Required: true},
				"id":              {Type: cty.String, Required: true},
				"hostname":        {Type: cty.String, Optional: true},
			},
		},
		IdentitySchemaVersion: 1,
	}
}

func TestTFEComputedOnlyIDRefusesLikeAWSVPC(t *testing.T) {
	schemas := map[string]providers.Schema{
		"tfe_workspace": tfeWorkspaceSchema(),
		"tfe_variable":  tfeVariableSchema(),
	}

	if der := Derivable(schemas); len(der) != 0 {
		t.Errorf("tfe types admitted as %v; tfe_workspace's id is Computed-only (server-minted) and tfe_variable names configurable_id, which is in neither type's own block", der)
	}
	for _, tn := range []string{"tfe_workspace", "tfe_variable"} {
		if _, ok := SynthesizeTypeIdentity(tn, schemas, nil); ok {
			t.Errorf("%s was synthesized despite its identity not being schema-derivable", tn)
		}
	}
}

// TestGitHubAndFastlyServeNoIdentitySchema pins the most basic of the four
// findings: at their latest published releases, integrations/github (6.13.0)
// and fastly/fastly (9.6.0) attach no resource identity schema to any
// resource type at all. This is not a derivable() clause to loosen - it is
// the schema.IdentitySchema == nil short-circuit at the very top of
// derivable() and SynthesizeTypeIdentity firing correctly on data the
// provider genuinely does not publish.
func TestGitHubAndFastlyServeNoIdentitySchema(t *testing.T) {
	schemas := map[string]providers.Schema{
		"github_repository": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name": {Type: cty.String, Required: true},
					"id":   {Type: cty.String, Optional: true, Computed: true},
				},
			},
			// No IdentitySchema: this is the real shape, not an omission.
		},
		"fastly_service_vcl": {
			Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name": {Type: cty.String, Required: true},
					"id":   {Type: cty.String, Optional: true, Computed: true},
				},
			},
		},
	}

	if der := Derivable(schemas); len(der) != 0 {
		t.Errorf("admitted %v from types with no identity schema at all", der)
	}
	for tn := range schemas {
		if _, ok := SynthesizeTypeIdentity(tn, schemas, nil); ok {
			t.Errorf("%s was synthesized despite carrying no identity schema", tn)
		}
		refusal := SchemaRefusal(tn, schemas, nil)
		if refusal == "" {
			t.Errorf("%s: SchemaRefusal returned no explanation", tn)
		}
	}
}
