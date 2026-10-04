// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #1873. Under `strict { secrets = "refuse" }` the residue
// mechanism used to record nothing at all for a type whose schema carries
// any sensitive attribute, so kubernetes_secret_v1's
// wait_for_service_account_token - an ordinary boolean only configuration
// ever sets - was re-proposed on every plan and never converged. The rule
// now is per attribute: a type's non-sensitive arguments are recorded, every
// sensitive one is dropped.
//
// The tests below pin both halves. The positive half is that one boolean.
// The negative half is the one that matters for safety, and it is asserted
// against a reader that preserves EVERYTHING from its prior, which is the
// worst case: every candidate the filters let through classifies as
// residue, so anything a filter missed would land in the record and fail
// here.

// k8sSecretLikeSchema is kubernetes_secret_v1's shape at the attributes that
// matter here: two sensitive maps, a write-only twin, an ordinary
// config-only boolean, and the metadata block.
func k8sSecretLikeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                             {Type: cty.String, Optional: true, Computed: true},
				"data":                           {Type: cty.Map(cty.String), Optional: true, Sensitive: true},
				"binary_data":                    {Type: cty.Map(cty.String), Optional: true, Sensitive: true},
				"data_wo":                        {Type: cty.Map(cty.String), Optional: true, WriteOnly: true},
				"type":                           {Type: cty.String, Optional: true},
				"immutable":                      {Type: cty.Bool, Optional: true},
				"wait_for_service_account_token": {Type: cty.Bool, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"metadata": {
					Nesting:  configschema.NestingList,
					MinItems: 1,
					MaxItems: 1,
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"name":      {Type: cty.String, Optional: true},
							"namespace": {Type: cty.String, Optional: true},
						},
					},
				},
			},
		},
	}
}

const k8sSecretValue = "k8s-secret-payload-1873"

func k8sSecretLikeApplied() cty.Value {
	return markSchemaSensitive(cty.ObjectVal(map[string]cty.Value{
		"id":                             cty.StringVal("default/app-creds"),
		"data":                           cty.MapVal(map[string]cty.Value{"password": cty.StringVal(k8sSecretValue)}),
		"binary_data":                    cty.MapVal(map[string]cty.Value{"blob": cty.StringVal(k8sSecretValue + "-bin")}),
		"data_wo":                        cty.NullVal(cty.Map(cty.String)),
		"type":                           cty.StringVal("Opaque"),
		"immutable":                      cty.False,
		"wait_for_service_account_token": cty.True,
		"metadata": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"name":      cty.StringVal("app-creds"),
			"namespace": cty.StringVal("default"),
		})}),
	}), k8sSecretLikeSchema().Block)
}

// k8sSecretLikeRead is the kubernetes provider's Read for that type: every
// attribute comes back from the cluster except wait_for_service_account_token,
// which passes its prior straight through.
func k8sSecretLikeRead(prior cty.Value) (cty.Value, error) {
	if prior == cty.NilVal || prior.IsNull() {
		return cty.NilVal, nil
	}
	live, _ := k8sSecretLikeApplied().UnmarkDeep()
	attrs := live.AsValueMap()
	attrs["wait_for_service_account_token"] = prior.GetAttr("wait_for_service_account_token")
	return cty.ObjectVal(attrs), nil
}

// preserveEverythingRead is the worst-case provider: its Read never sources
// anything from the remote and hands the prior back unchanged, unmarked as
// a wire answer always is. Every candidate classifies as residue under it.
func preserveEverythingRead(prior cty.Value) (cty.Value, error) {
	if prior == cty.NilVal || prior.IsNull() {
		return cty.NilVal, nil
	}
	plain, _ := prior.UnmarkDeep()
	return plain, nil
}

func TestRefuseRecordsTheConfigOnlyBooleanBesideAKubernetesSecret(t *testing.T) {
	schema := k8sSecretLikeSchema()
	applied := k8sSecretLikeApplied()

	attrs, ok := classifyResidueAll(schema, applied, strict.Refuse, k8sSecretLikeRead, cty.NilVal)
	if !ok {
		t.Fatal("nothing classified under secrets=refuse; wait_for_service_account_token is never returned by the provider and is not secret, so it must be recorded (#1873)")
	}
	if got := attrs["wait_for_service_account_token"]; !got.RawEquals(cty.True) {
		t.Fatalf("wait_for_service_account_token = %#v, want true; recorded keys %v", got, keysOf(attrs))
	}
	assertNoSecretRecorded(t, schema, attrs, k8sSecretValue)
}

// TestRefuseRecordsNoSensitiveValue is the safety half, over the shapes a
// secret can take on an AWS type as well as the Kubernetes one: a sensitive
// top-level argument (aws_db_instance.password, reduced to
// [secretLambdaSchema]), a sensitive leaf two blocks down
// (aws_lb_listener's client_secret, [listenerLikeSchema]), a sensitive leaf
// inside a top-level nested block, and an ordinary argument fed by a
// `sensitive = true` variable, whose mark the schema cannot put back.
func TestRefuseRecordsNoSensitiveValue(t *testing.T) {
	type tc struct {
		schema  providers.Schema
		applied cty.Value
		secrets []string
	}

	dbSchema := secretLambdaSchema()
	dbSchema.Block.Attributes["master_password"] = &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}
	dbSchema.Block.BlockTypes = map[string]*configschema.NestedBlock{
		"auth": {
			Nesting: configschema.NestingSingle,
			Block: configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"username": {Type: cty.String, Optional: true},
					"token":    {Type: cty.String, Optional: true, Sensitive: true},
				},
			},
		},
	}
	dbAttrs := lambdaApplied().AsValueMap()
	dbAttrs["master_password"] = cty.StringVal("db-master-1873")
	dbAttrs["auth"] = cty.ObjectVal(map[string]cty.Value{
		"username": cty.StringVal("admin"),
		"token":    cty.StringVal("nested-token-1873"),
	})
	// description carries a mark from a sensitive variable, not from the
	// schema.
	dbAttrs["description"] = cty.StringVal("var-secret-1873").Mark(marks.Sensitive)
	dbApplied := markSchemaSensitive(cty.ObjectVal(dbAttrs), dbSchema.Block)

	cases := map[string]tc{
		"aws top-level, nested and variable-marked": {
			schema:  dbSchema,
			applied: dbApplied,
			secrets: []string{"check_links.py.zip", "db-master-1873", "nested-token-1873", "var-secret-1873"},
		},
		"aws nested leaf two blocks down": {
			schema:  listenerLikeSchema(),
			applied: markSchemaSensitive(listenerApplied(), listenerLikeSchema().Block),
			secrets: []string{"super-secret-value"},
		},
		"kubernetes secret": {
			schema:  k8sSecretLikeSchema(),
			applied: k8sSecretLikeApplied(),
			secrets: []string{k8sSecretValue},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for _, n := range residueCandidates(c.schema, c.applied, strict.Refuse) {
				if a := c.schema.Block.Attributes[n]; a != nil && (a.Sensitive || a.WriteOnly) {
					t.Fatalf("sensitive or write-only attribute %q is a candidate under secrets=refuse", n)
				}
			}
			for _, pc := range residueLeafPathCandidates(c.schema, c.applied, strict.Refuse) {
				if pc.Attr != nil && pc.Attr.Sensitive {
					t.Fatalf("sensitive leaf %s is a candidate under secrets=refuse", pathString(pc.Path))
				}
			}
			attrs, ok := classifyResidueAll(c.schema, c.applied, strict.Refuse, preserveEverythingRead, cty.NilVal)
			if !ok || len(attrs) == 0 {
				t.Fatal("nothing classified under a reader that preserves everything; the type's ordinary arguments must still be recorded under secrets=refuse (#1873)")
			}
			for _, secret := range c.secrets {
				assertNoSecretRecorded(t, c.schema, attrs, secret)
			}
		})
	}
}

// assertNoSecretRecorded fails when any recorded value carries a mark, sits
// at a schema-sensitive attribute, or contains secret anywhere in its
// rendering.
func assertNoSecretRecorded(t *testing.T, schema providers.Schema, attrs map[string]cty.Value, secret string) {
	t.Helper()
	for key, v := range attrs {
		if v.ContainsMarked() {
			t.Fatalf("recorded value at %s carries a mark under secrets=refuse: %#v", key, v)
		}
		if strings.Contains(v.GoString(), secret) {
			t.Fatalf("recorded value at %s contains the secret %q under secrets=refuse", key, secret)
		}
		var attr *configschema.Attribute
		if isResiduePathKey(key) {
			p, err := decodeResiduePathKey(key)
			if err != nil {
				t.Fatalf("decoding recorded path key %s: %s", key, err)
			}
			attr, _ = schemaAttrAtPath(schema.Block, p)
		} else {
			attr = schema.Block.Attributes[key]
		}
		if attr != nil && (attr.Sensitive || attr.WriteOnly) {
			t.Fatalf("recorded %s, which the schema marks sensitive or write-only, under secrets=refuse", key)
		}
	}
}
