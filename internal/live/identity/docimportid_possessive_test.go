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

			parts, sep, ok := resolveDocumentedImportID(subject, cognitoUserPoolClientBlock())
			if !ok {
				t.Fatal("the grammar resolved against no attribute of the schema the provider serves")
			}
			if wantResolved := []string{"user_pool_id", "id"}; !reflect.DeepEqual(parts, wantResolved) {
				t.Errorf("resolved = %v, want %v", parts, wantResolved)
			}

			got, ok := LocatedComposedImportID(obj, parts, sep)
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

// TestPossessiveOfGrammarStillRefusesTheCredentialCondition records what this
// grammar does NOT move, so nobody reads the assertion above as an admission.
//
// [LocatedType] refuses aws_cognito_user_pool_client on two independent
// conditions. Condition 3 is the one the grammar answers - the identity can
// now be recorded in full. Condition 2 is [credentialMaterial], which fires
// on client_secret and is a maintainer call on the exclusion's breadth, not a
// scrape question. So the type stays refused, and the estate that motivated
// the grammar stays blocked.
func TestPossessiveOfGrammarStillRefusesTheCredentialCondition(t *testing.T) {
	block := cognitoUserPoolClientBlock()
	if !credentialMaterial(block) {
		t.Fatal("credentialMaterial no longer fires on a block carrying a Sensitive, non-deprecated " +
			"client_secret. If the exclusion has been narrowed, this test's premise is gone and " +
			"corpus-alb-complete's stage-3 site should be re-measured rather than assumed.")
	}
	if _, _, ok := resolveDocumentedImportID("aws_cognito_user_pool_client", block); !ok {
		t.Error("the documented grammar no longer resolves, so condition 3's half of the refusal is back")
	}
}
