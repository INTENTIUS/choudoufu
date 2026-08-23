// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"reflect"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is HANDOFF.md's safety rule applied to the two types
// tools/importdocs-gen's possessive-of reader newly describes: never write a
// wrong marker, and convergence is never evidence an identity is right -
// assert the RENDERED identity by value.
//
// The reason these two need it and the rest of the roster does not is the
// shape of the sentence they were read out of. Both segments are spelled the
// same in the prose - "the `id` of the Cognito User Pool, and the `id` of
// the Cognito User Pool Client" - so a reading that resolved the
// qualification backwards would still produce two segments, still satisfy
// every arity and distinctness gate the scrape holds, and still compose a
// two-part string of exactly the documented shape. It would just name the
// wrong object. Only the composed VALUE separates the two readings, and the
// documentation states that value.
//
// The subject is read out of the generated roster rather than written down
// here, the way this package's other real-type assertion is: what is pinned
// is the answer, and if the scrape stops producing a grammar for these
// pages the test says so rather than passing vacuously.

// cognitoUserPoolClientBlock is the shape hashicorp/aws 6.59.0 serves for
// both user-pool-client types, reduced to the top-level string attributes
// this resolution reads: the parent the configuration supplies, the leaf the
// service mints, the name, and the secret.
func cognitoUserPoolClientBlock() *configschema.Block {
	return &configschema.Block{Attributes: map[string]*configschema.Attribute{
		"user_pool_id":  {Type: cty.String, Required: true},
		"name":          {Type: cty.String, Required: true},
		"id":            {Type: cty.String, Computed: true},
		"client_secret": {Type: cty.String, Computed: true, Sensitive: true},
	}}
}

// TestPossessiveOfGrammarComposesTheDocumentedImportString pins, by value,
// the import string a located record would hold for every type whose grammar
// came out of a possessive-of import sentence.
//
// The expected string is the provider's own documented import example, taken
// from the same Import section the grammar was scraped from:
//
//	id = "us-west-2_abc123/3ho4ek12345678909nh3fmhpko"
//
// where us-west-2_abc123 is the user pool and 3ho4ek... the client. A reading
// that swapped the segments composes
// "3ho4ek12345678909nh3fmhpko/us-west-2_abc123", which is the same length,
// the same shape and a different object.
func TestPossessiveOfGrammarComposesTheDocumentedImportString(t *testing.T) {
	const (
		poolID   = "us-west-2_abc123"
		clientID = "3ho4ek12345678909nh3fmhpko"
		want     = poolID + "/" + clientID
	)

	obj := cty.ObjectVal(map[string]cty.Value{
		"user_pool_id":  cty.StringVal(poolID),
		"name":          cty.StringVal("client"),
		"id":            cty.StringVal(clientID),
		"client_secret": cty.StringVal("s3cr3t"),
	})

	// Both pages carry the same sentence; the second spells "managed" into
	// its type name and not into its prose, which is the case the
	// self-reference test has to see through.
	for _, subject := range []string{
		"aws_cognito_user_pool_client",
		"aws_cognito_managed_user_pool_client",
	} {
		t.Run(subject, func(t *testing.T) {
			g, known := DocumentedImportIDs[subject]
			if !known {
				t.Fatalf("%s carries no documented import grammar. tools/importdocs-gen's possessive-of "+
					"reader is what produces one for this page; if that reading has been withdrawn, withdraw "+
					"this assertion with it rather than leaving it passing vacuously.", subject)
			}
			if g.Separator != "/" {
				t.Errorf("separator = %q, want %q - the character the page's own import example joins with", g.Separator, "/")
			}
			wantParts := []DocumentedImportIDPart{
				{Name: "userpoolid", Argument: true},
				{Name: "id", Argument: false},
			}
			if !reflect.DeepEqual(g.Parts, wantParts) {
				t.Errorf("parts = %+v, want %+v - the parent first and the minted leaf second, "+
					"which is the order the page states and the order its example demonstrates", g.Parts, wantParts)
			}

			parts, variadicGroup, alternatives, sep, ok := resolveDocumentedImportID(subject, cognitoUserPoolClientBlock())
			if !ok {
				t.Fatal("the grammar resolved against no attribute of the schema the provider serves")
			}
			if wantResolved := []string{"user_pool_id", "id"}; !reflect.DeepEqual(parts, wantResolved) {
				t.Errorf("resolved = %v, want %v", parts, wantResolved)
			}

			got, ok := LocatedComposedImportID(obj, parts, variadicGroup, alternatives, sep)
			if !ok {
				t.Fatal("refused an applied object carrying every segment")
			}
			if got != want {
				t.Errorf("composed import ID = %q, want %q - the provider's own documented import example, "+
					"segment for segment. A swapped reading composes %q, which is the same shape and a "+
					"different object.", got, want, clientID+"/"+poolID)
			}
		})
	}
}

// TestPossessiveOfGrammarClearsBothConditionsWithoutRecordingTheSecret pins,
// by value, what the located route actually records for
// aws_cognito_user_pool_client - the type that was corpus-alb-complete's last
// stage-3 wall.
//
// [LocatedType] used to refuse it on two independent conditions, and both are
// now answered. Condition 3 is the one the grammar above answers: the
// identity can be recorded in full. Condition 2 was [credentialMaterial],
// which fires on client_secret; commit 80666bc1c0 (issue #365 population 2)
// narrowed it to [sensitiveIdentityAttr], because this route records
// [locatedImportIDAttr] or the plan's own components and nothing else, so the
// only secret it can leak is one that IS an identity component. This type's
// is user_pool_id/id, which never touches client_secret.
//
// This test used to assert the opposite, under the name
// ...StillRefusesTheCredentialCondition. What is kept from it is the part
// that is still true and still worth guarding: [credentialMaterial]'s
// whole-schema sweep DOES fire on this block, and that is the right answer to
// internal/live/projection's residue question. The narrowing is that
// [LocatedType] no longer asks it. If someone widens the veto back, the plan
// assertions below go red rather than the estate quietly re-blocking.
func TestPossessiveOfGrammarClearsBothConditionsWithoutRecordingTheSecret(t *testing.T) {
	block := cognitoUserPoolClientBlock()
	if !credentialMaterial(block) {
		t.Error("credentialMaterial no longer fires on a block carrying a Sensitive, non-deprecated " +
			"client_secret. That whole-schema sweep is still internal/live/projection's question; " +
			"only the located route stopped consulting it.")
	}
	if _, _, _, _, ok := resolveDocumentedImportID("aws_cognito_user_pool_client", block); !ok {
		t.Fatal("the documented grammar no longer resolves, so condition 3's half of the refusal is back")
	}

	schema := providers.Schema{Block: block}
	plan, recordable := LocatedIdentityPlanFor("aws_cognito_user_pool_client", schema)
	if !recordable {
		t.Fatal("LocatedIdentityPlanFor refuses the type, so condition 3 is back")
	}
	// BY VALUE, and in order: a reading that swapped these two segments
	// would be the same shape, the same length and a different object.
	if want := []string{"user_pool_id", "id"}; !reflect.DeepEqual(plan.ImportIDParts, want) {
		t.Errorf("recorded identity parts = %v, want %v", plan.ImportIDParts, want)
	}
	if plan.ImportIDSeparator != "/" {
		t.Errorf("separator = %q, want %q", plan.ImportIDSeparator, "/")
	}
	if plan.Composite() {
		t.Errorf("plan carries a provider identity object %v; 6.59.0 serves none for this type, "+
			"so the record is the composed string and nothing else", plan.Components)
	}
	// The whole point of the narrowing: the record this route would write
	// does not include client_secret, so there is no secret to refuse over.
	if got := sensitiveIdentityAttr(plan, schema); got != "" {
		t.Errorf("sensitiveIdentityAttr = %q, want \"\" - the recorded identity must carry no secret", got)
	}
}
