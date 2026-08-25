// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// GitHub issue #402. Both fixtures below are reduced from hashicorp/aws
// 6.59.0's real schemas (fetched via `terraform providers schema -json`
// against the pinned provider version, and confirmed directly against a
// live floci with TF_LOG=trace - see the PR description for the reproduce
// command), not invented:
//
//   - corsLikeSchema is aws_s3_bucket_cors_configuration's shape:
//     expected_bucket_owner is Optional, NOT Computed - a plugin-protocol
//     promise that its true value can never differ from configuration -
//     yet the provider's Read echoes the run's own ambient AWS account id
//     back for it UNCONDITIONALLY, even from a bare identity-only prior.
//     Its aws_s3_bucket_object_lock_configuration, _server_side_
//     encryption_configuration and _versioning siblings share the
//     identical shape.
//   - lifecycleLikeSchema is aws_s3_bucket_lifecycle_configuration's shape:
//     the SAME argument, but Optional AND Computed - the one difference
//     that routes it through classifyResidue's format-only-widening branch
//     instead of build.go's raw read path, and the one this file's
//     TestClassifyResidue* cases guard.
//
// Both declare a native Resource Identity ("bucket" required, "account_id"
// and "region" optional) - the exact two literal attribute names
// [ambientIdentityValues] reads, matching live/identity's own generated
// table, which already keys every {Cloud: "account-id"} / {Cloud: "region"}
// component on those same two names.
func corsLikeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                    {Type: cty.String, Optional: true, Computed: true},
				"bucket":                {Type: cty.String, Required: true},
				"expected_bucket_owner": {Type: cty.String, Optional: true},
			},
		},
		IdentitySchema: &configschema.Object{
			Nesting: configschema.NestingSingle,
			Attributes: map[string]*configschema.Attribute{
				"bucket":     {Type: cty.String, Required: true},
				"account_id": {Type: cty.String, Optional: true},
				"region":     {Type: cty.String, Optional: true},
			},
		},
	}
}

func lifecycleLikeSchema() providers.Schema {
	s := corsLikeSchema()
	s.Block.Attributes["expected_bucket_owner"] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
	return s
}

func s3AmbientIdentity(bucket, accountID, region string) cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"bucket":     cty.StringVal(bucket),
		"account_id": cty.StringVal(accountID),
		"region":     cty.StringVal(region),
	})
}

// TestClassifyResidueRefusesAnAmbientAccountEcho is the
// aws_s3_bucket_lifecycle_configuration shape: read A (a bare
// identity-only prior) comes back null, read B (given the applied value)
// echoes it back exactly - ordinarily classifyResidue's own documented
// residue shape, an SDKv2-style resource that only ever preserves an
// attribute from whatever prior it was handed. Without the ambient guard
// this records "000000000000" and replays it on every future plan even
// though nothing in configuration ever set it - GitHub issue #402's
// corpus-s3-bucket-complete regression's own root cause for this one type
// (see build.go's [scrubAmbientEcho] for the other four).
func TestClassifyResidueRefusesAnAmbientAccountEcho(t *testing.T) {
	schema := lifecycleLikeSchema()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal("bucket-name"),
		"bucket":                cty.StringVal("bucket-name"),
		"expected_bucket_owner": cty.StringVal("000000000000"),
	})
	candidates := residueCandidates(schema, applied, "store")
	identityObj := s3AmbientIdentity("bucket-name", "000000000000", "eu-west-1")
	ambient := ambientIdentityValues(schema, identityObj)

	read := func(prior cty.Value) (cty.Value, error) {
		if prior.GetAttr("expected_bucket_owner").IsNull() {
			// read A: the provider's Read only ever preserves what the
			// prior held for this Computed argument - it never derives it
			// fresh from a bare identity-only prior.
			return cty.ObjectVal(map[string]cty.Value{
				"id":                    prior.GetAttr("id"),
				"bucket":                prior.GetAttr("bucket"),
				"expected_bucket_owner": cty.NullVal(cty.String),
			}), nil
		}
		return prior, nil
	}

	got, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), residueConfigSourced(schema), read, ambient)
	if ok {
		t.Fatalf("expected the ambient echo to be refused as residue, got recorded: %#v", got)
	}

	// Mutation check, both directions:
	//
	//  1. Without the guard (ambient=nil, simulating every call site before
	//     this fix threaded an identity object through), the exact same
	//     read pattern SHOULD be recorded - proving the guard, not some
	//     other branch, is what changed the verdict above.
	gotUnguarded, okUnguarded := classifyResidue(applied, candidates, residueIdentityAttrs(schema), residueConfigSourced(schema), read, nil)
	if !okUnguarded {
		t.Fatalf("expected the unguarded call (ambient=nil) to still record residue, proving the guard - not some other branch - is what refuses it above")
	}
	if v, ok := gotUnguarded["expected_bucket_owner"]; !ok || !v.RawEquals(cty.StringVal("000000000000")) {
		t.Fatalf("expected the unguarded call to record expected_bucket_owner=000000000000, got %#v", gotUnguarded)
	}

	//  2. A value that does NOT match ambient (genuine, provider-blind
	//     residue unrelated to account/region) must still be recorded -
	//     proving the guard keys on the VALUE matching ambient context,
	//     never on the attribute's name.
	otherApplied := cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal("bucket-name"),
		"bucket":                cty.StringVal("bucket-name"),
		"expected_bucket_owner": cty.StringVal("999999999999"),
	})
	otherCandidates := residueCandidates(schema, otherApplied, "store")
	otherRead := func(prior cty.Value) (cty.Value, error) {
		if prior.GetAttr("expected_bucket_owner").IsNull() {
			return cty.ObjectVal(map[string]cty.Value{
				"id":                    prior.GetAttr("id"),
				"bucket":                prior.GetAttr("bucket"),
				"expected_bucket_owner": cty.NullVal(cty.String),
			}), nil
		}
		return prior, nil
	}
	gotCrossAccount, okCrossAccount := classifyResidue(otherApplied, otherCandidates, residueIdentityAttrs(schema), residueConfigSourced(schema), otherRead, ambient)
	if !okCrossAccount {
		t.Fatalf("expected a value that does not match ambient context to still be recorded as residue")
	}
	if v, ok := gotCrossAccount["expected_bucket_owner"]; !ok || !v.RawEquals(cty.StringVal("999999999999")) {
		t.Fatalf("expected expected_bucket_owner=999999999999 to be recorded untouched, got %#v", gotCrossAccount)
	}
}

// TestClassifyResiduePathsRefusesAnAmbientAccountEcho is the identical
// guard at [classifyResiduePaths]' nested-path granularity, reusing
// listenerLikeSchema's own authenticate_oidc.client_secret fixture (issue
// #401 family 2) with its identity schema widened to carry account_id -
// there is no real AWS type with an ambient-echoing nested leaf today, so
// this proves the mechanism generalizes rather than reproducing a second
// live defect.
func TestClassifyResiduePathsRefusesAnAmbientAccountEcho(t *testing.T) {
	schema := listenerLikeSchema()
	schema.IdentitySchema = &configschema.Object{
		Nesting: configschema.NestingSingle,
		Attributes: map[string]*configschema.Attribute{
			"arn":        {Type: cty.String, Required: true},
			"account_id": {Type: cty.String, Optional: true},
		},
	}
	applied := listenerApplied()
	candidates := residueLeafPathCandidates(schema, applied, "store")
	key, err := encodeResiduePathKey(listenerClientSecretPath())
	if err != nil {
		t.Fatalf("encoding the fixture's own client_secret path: %s", err)
	}

	// listenerApplied's own client_secret is "super-secret-value", which
	// does not match ambient context - the ordinary case, proving the
	// guard leaves a real residue candidate alone when it should.
	ambientElsewhere := ambientIdentityValues(schema, cty.ObjectVal(map[string]cty.Value{
		"arn":        cty.StringVal(applied.GetAttr("arn").AsString()),
		"account_id": cty.StringVal("000000000000"),
	}))
	got, ok := classifyResiduePaths(applied, candidates, residueIdentityAttrs(schema), listenerLikeRead(true), ambientElsewhere)
	if !ok {
		t.Fatalf("expected client_secret (not ambient-matching) to still classify as residue, got none")
	}
	if _, stillThere := got[key]; !stillThere {
		t.Fatalf("expected client_secret to still be recorded (it does not match ambient context), got %#v", got)
	}

	// Now make the fixture's OWN captured value equal the ambient value
	// under test - proving the guard fires once the value genuinely
	// matches, not merely because an identity object is present.
	ambientMatchesSecret := ambientIdentityValues(schema, cty.ObjectVal(map[string]cty.Value{
		"arn":        cty.StringVal(applied.GetAttr("arn").AsString()),
		"account_id": cty.StringVal("super-secret-value"),
	}))
	gotGuarded, okGuarded := classifyResiduePaths(applied, candidates, residueIdentityAttrs(schema), listenerLikeRead(true), ambientMatchesSecret)
	if okGuarded {
		if _, stillThere := gotGuarded[key]; stillThere {
			t.Fatalf("expected client_secret to be refused once its value equals ambient context, got %#v", gotGuarded)
		}
	}
}

// TestScrubAmbientEchoNullsAnUnconfiguredAmbientValue is the
// aws_s3_bucket_cors_configuration shape: expected_bucket_owner is
// Optional and NOT Computed, so the plugin protocol's own contract says
// its true value can never differ from configuration - yet the raw read
// this function is handed carries the run's own ambient account id, and
// configuredSeed (this instance's own genuinely-evaluated configuration)
// has nothing for the name at all. This is GitHub issue #402's actual
// corpus-s3-bucket-complete regression: cors_configuration, object_lock_
// configuration, server_side_encryption_configuration and versioning all
// share this exact shape, and classifyResidue's own read-A/read-B
// discriminator never even sees it, because the value never goes anywhere
// near the residue store.
func TestScrubAmbientEchoNullsAnUnconfiguredAmbientValue(t *testing.T) {
	schema := corsLikeSchema()
	identityObj := s3AmbientIdentity("bucket-name", "000000000000", "eu-west-1")
	raw := cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal("bucket-name"),
		"bucket":                cty.StringVal("bucket-name"),
		"expected_bucket_owner": cty.StringVal("000000000000"),
	})

	scrubbed := scrubAmbientEcho(schema, raw, identityObj, nil)
	if got := scrubbed.GetAttr("expected_bucket_owner"); !got.IsNull() {
		t.Fatalf("expected expected_bucket_owner to be nulled as an ambient echo config never set, got %#v", got)
	}
	if got := scrubbed.GetAttr("bucket"); !got.RawEquals(cty.StringVal("bucket-name")) {
		t.Fatalf("scrub must not touch an attribute it was not asked about, got bucket=%#v", got)
	}

	// Mutation check 1: configuration genuinely sets the attribute - even
	// to the very value that happens to equal this run's own ambient
	// account (a deliberate same-account expected_bucket_owner is real,
	// not a guess) - and it must be left exactly as read.
	configuredSeed := map[string]cty.Value{"expected_bucket_owner": cty.StringVal("000000000000")}
	notScrubbed := scrubAmbientEcho(schema, raw, identityObj, configuredSeed)
	if got := notScrubbed.GetAttr("expected_bucket_owner"); !got.RawEquals(cty.StringVal("000000000000")) {
		t.Fatalf("expected a configured value to survive untouched even though it matches ambient context, got %#v", got)
	}

	// Mutation check 2: a real cross-account value - config-absent, but
	// NOT equal to this run's own ambient account - must also survive
	// untouched, proving the guard keys on ambient-match, not merely on
	// "configuration said nothing".
	crossAccount := cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal("bucket-name"),
		"bucket":                cty.StringVal("bucket-name"),
		"expected_bucket_owner": cty.StringVal("111111111111"),
	})
	notAmbient := scrubAmbientEcho(schema, crossAccount, identityObj, nil)
	if got := notAmbient.GetAttr("expected_bucket_owner"); !got.RawEquals(cty.StringVal("111111111111")) {
		t.Fatalf("expected a value that does not match ambient context to survive untouched, got %#v", got)
	}

	// Mutation check 3: no identity object at all (every pre-identity
	// provider, or a type whose identity schema names neither attribute) -
	// scrub must be a complete no-op rather than nulling on no evidence.
	noIdentity := scrubAmbientEcho(schema, raw, cty.NilVal, nil)
	if got := noIdentity.GetAttr("expected_bucket_owner"); !got.RawEquals(cty.StringVal("000000000000")) {
		t.Fatalf("expected no scrub at all with no identity object in hand, got %#v", got)
	}

	// Mutation check 4: the Computed sibling (lifecycleLikeSchema) must be
	// left untouched by this function - a real, provider-managed value is
	// exactly what Computed promises, and classifyResidue's own guard
	// (tested above) is what covers this population instead.
	computedSchema := lifecycleLikeSchema()
	computedRaw := cty.ObjectVal(map[string]cty.Value{
		"id":                    cty.StringVal("bucket-name"),
		"bucket":                cty.StringVal("bucket-name"),
		"expected_bucket_owner": cty.StringVal("000000000000"),
	})
	computedScrubbed := scrubAmbientEcho(computedSchema, computedRaw, identityObj, nil)
	if got := computedScrubbed.GetAttr("expected_bucket_owner"); !got.RawEquals(cty.StringVal("000000000000")) {
		t.Fatalf("expected a Computed candidate to be left untouched by scrubAmbientEcho, got %#v", got)
	}
}
