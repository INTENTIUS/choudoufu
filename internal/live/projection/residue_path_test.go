// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #401 family 2's own fixture and tests:
// aws_lb_listener.default_action.authenticate_oidc.client_secret, Required
// and Sensitive two levels below the schema's own top level - the shape
// [residueCandidates]' flat-attribute loop and whole-block loop neither one
// reaches (see [residueLeafPathCandidates]'s own doc comment for why).
//
// listenerLikeSchema is reduced from hashicorp/aws 6.59.0's real
// aws_lb_listener schema (fetched directly via `terraform providers schema
// -json` against the pinned provider version, not guessed): default_action
// is NestingList, authenticate_oidc is NestingList nested inside it with
// max_items 1, and client_secret is the one Required+Sensitive leaf among
// six Required siblings - client_id is the negative control, identical in
// every schema respect (Required, String, not Sensitive) except the one
// flag that matters.
func listenerLikeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":  {Type: cty.String, Optional: true, Computed: true},
				"arn": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"default_action": {
					Nesting: configschema.NestingList,
					Block: configschema.Block{
						Attributes: map[string]*configschema.Attribute{
							"type":             {Type: cty.String, Required: true},
							"target_group_arn": {Type: cty.String, Optional: true, Computed: true},
							"order":            {Type: cty.Number, Optional: true, Computed: true},
						},
						BlockTypes: map[string]*configschema.NestedBlock{
							"authenticate_oidc": {
								Nesting: configschema.NestingList,
								Block: configschema.Block{
									Attributes: map[string]*configschema.Attribute{
										"authorization_endpoint": {Type: cty.String, Required: true},
										"client_id":              {Type: cty.String, Required: true},
										"client_secret":          {Type: cty.String, Required: true, Sensitive: true},
										"issuer":                 {Type: cty.String, Required: true},
										"token_endpoint":         {Type: cty.String, Required: true},
										"user_info_endpoint":     {Type: cty.String, Required: true},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func listenerApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":  cty.StringVal("arn:aws:elasticloadbalancing:eu-west-1:000000000000:listener/app/ex-oidc/abc/def"),
		"arn": cty.StringVal("arn:aws:elasticloadbalancing:eu-west-1:000000000000:listener/app/ex-oidc/abc/def"),
		"default_action": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"type":             cty.StringVal("authenticate-oidc"),
			"target_group_arn": cty.StringVal(""),
			"order":            cty.NumberIntVal(1),
			"authenticate_oidc": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"authorization_endpoint": cty.StringVal("https://example.com/authorize"),
				"client_id":              cty.StringVal("client-id-123"),
				"client_secret":          cty.StringVal("super-secret-value"),
				"issuer":                 cty.StringVal("https://example.com"),
				"token_endpoint":         cty.StringVal("https://example.com/token"),
				"user_info_endpoint":     cty.StringVal("https://example.com/userinfo"),
			})}),
		})}),
	})
}

// listenerClientSecretPath is the path [residueLeafPathCandidates] must
// find for the fixture above: default_action[0].authenticate_oidc[0].client_secret.
func listenerClientSecretPath() cty.Path {
	return cty.Path{
		cty.GetAttrStep{Name: "default_action"},
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: "authenticate_oidc"},
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: "client_secret"},
	}
}

// listenerLikeRead is the real ELBv2 API shape (AWS's own DescribeListeners
// documentation: AuthenticateOidcConfig is returned in full EXCEPT
// ClientSecret, which the API never includes in a response): every
// attribute of default_action and authenticate_oidc except client_secret
// is echoed from the live object regardless of what prior held, and
// client_secret passes its PRIOR straight through unread - the textbook
// residue shape [classifyResidue]'s own doc comment describes for the flat
// case, reproduced here one level deeper.
//
// echoClientID, when true, makes client_id ALSO pass its prior through
// unread instead of being echoed from the live object - the mutation this
// file's own boundary test uses to prove the discriminator, not a
// hardcoded attribute name, is what keeps client_id out of a real run's
// residue.
func listenerLikeRead(echoClientID bool) residueReader {
	return func(prior cty.Value) (cty.Value, error) {
		if prior == cty.NilVal || prior.IsNull() {
			return cty.NilVal, nil
		}
		priorActs := priorDefaultActionSlice(prior)
		live := listenerApplied()
		liveActs := live.GetAttr("default_action").AsValueSlice()
		outActs := make([]cty.Value, len(liveActs))
		for i, liveAct := range liveActs {
			actAttrs := liveAct.AsValueMap()
			liveOIDC := actAttrs["authenticate_oidc"].AsValueSlice()[0].AsValueMap()

			priorSecret := cty.NullVal(cty.String)
			priorClientID := cty.NullVal(cty.String)
			if i < len(priorActs) && !priorActs[i].IsNull() {
				priorOIDCList := priorActs[i].AsValueMap()["authenticate_oidc"]
				if !priorOIDCList.IsNull() {
					priorOIDCSlice := priorOIDCList.AsValueSlice()
					if len(priorOIDCSlice) > 0 {
						priorOIDC := priorOIDCSlice[0].AsValueMap()
						priorSecret = priorOIDC["client_secret"]
						priorClientID = priorOIDC["client_id"]
					}
				}
			}
			liveOIDC["client_secret"] = priorSecret // never returned by the API
			if !echoClientID {
				liveOIDC["client_id"] = priorClientID
			}
			actAttrs["authenticate_oidc"] = cty.ListVal([]cty.Value{cty.ObjectVal(liveOIDC)})
			outActs[i] = cty.ObjectVal(actAttrs)
		}
		return cty.ObjectVal(map[string]cty.Value{
			"id":             live.GetAttr("id"),
			"arn":            live.GetAttr("arn"),
			"default_action": cty.ListVal(outActs),
		}), nil
	}
}

func priorDefaultActionSlice(prior cty.Value) []cty.Value {
	if !prior.Type().HasAttribute("default_action") {
		return nil
	}
	da := prior.GetAttr("default_action")
	if da.IsNull() || !da.IsWhollyKnown() {
		return nil
	}
	return da.AsValueSlice()
}

// TestResidueLeafPathCandidatesFindsEveryLeafAndMarksClientSecretApart pins
// [residueLeafPathCandidates]'s whole output for the fixture: the schema
// half asks nothing about Sensitive except to gate WriteOnly/Sensitive on
// the secrets setting, symmetrically with [residueCandidates]' own
// documented stance ("no longer asks whether schemaAttr is Required or
// Optional... a record for a purely Computed attribute is exactly as safe
// to fill") - so every settable leaf at any depth is a schema-level
// candidate, Sensitive or not, and it is [classifyResiduePaths]'s two-read
// discriminator, not this function, whose job is narrowing that down to
// what the provider genuinely never echoes (see
// TestClassifyResiduePathsCapturesTheSecretAndLeavesTheEchoedSiblingUnrecorded
// for that half). This test's own job is the schema-only questions
// candidacy actually decides: every leaf two levels deep is reached at
// all (id and arn, the schema's own top-level identity, are excluded by
// construction - [residueLeafPathCandidates] only ever walks BlockTypes),
// and ConfigSourced tracks Required/Optional-not-Computed correctly
// (target_group_arn and order are Optional+Computed and so answer false;
// every Required leaf, client_secret included, answers true).
func TestResidueLeafPathCandidatesFindsEveryLeafAndMarksClientSecretApart(t *testing.T) {
	schema := listenerLikeSchema()
	applied := listenerApplied()

	candidates := residueLeafPathCandidates(schema, applied, strict.DefaultSecrets)
	byKey := make(map[string]residuePathCandidate, len(candidates))
	for _, c := range candidates {
		key, err := encodeResiduePathKey(c.Path)
		if err != nil {
			t.Fatalf("encodeResiduePathKey: %s", err)
		}
		byKey[key] = c
	}

	wantConfigSourced := map[string]bool{
		"type":                   true,
		"target_group_arn":       false, // Optional+Computed
		"order":                  false, // Optional+Computed
		"authorization_endpoint": true,
		"client_id":              true,
		"client_secret":          true,
		"issuer":                 true,
		"token_endpoint":         true,
		"user_info_endpoint":     true,
	}
	if len(candidates) != len(wantConfigSourced) {
		t.Fatalf("residueLeafPathCandidates returned %d candidate(s), want %d: %v", len(candidates), len(wantConfigSourced), keysOfCandidates(byKey))
	}

	leafPath := func(names ...string) cty.Path {
		p := cty.Path{cty.GetAttrStep{Name: "default_action"}, cty.IndexStep{Key: cty.NumberIntVal(0)}}
		if len(names) == 2 {
			p = append(p, cty.GetAttrStep{Name: "authenticate_oidc"}, cty.IndexStep{Key: cty.NumberIntVal(0)}, cty.GetAttrStep{Name: names[1]})
			return p
		}
		p = append(p, cty.GetAttrStep{Name: names[0]})
		return p
	}
	for name, wantCS := range wantConfigSourced {
		var path cty.Path
		switch name {
		case "type", "target_group_arn", "order":
			path = leafPath(name)
		default:
			path = leafPath("authenticate_oidc", name)
		}
		key, err := encodeResiduePathKey(path)
		if err != nil {
			t.Fatalf("encodeResiduePathKey(%s): %s", name, err)
		}
		got, held := byKey[key]
		if !held {
			t.Fatalf("no candidate at %s's path; got %v", name, keysOfCandidates(byKey))
		}
		if got.ConfigSourced != wantCS {
			t.Errorf("%s: ConfigSourced = %v, want %v", name, got.ConfigSourced, wantCS)
		}
	}

	secretKey, err := encodeResiduePathKey(listenerClientSecretPath())
	if err != nil {
		t.Fatalf("encodeResiduePathKey: %s", err)
	}
	secret, held := byKey[secretKey]
	if !held {
		t.Fatal("client_secret's own path is not a candidate")
	}
	if secret.Attr == nil || !secret.Attr.Sensitive {
		t.Fatal("client_secret's candidate must carry its own Sensitive schema Attribute")
	}
	for name, c := range byKey {
		if name == secretKey {
			continue
		}
		if c.Attr != nil && c.Attr.Sensitive {
			t.Errorf("%s is Sensitive in this fixture's schema and should not be - client_secret must be the only one", name)
		}
	}
}

func keysOfCandidates(m map[string]residuePathCandidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func pathString(p cty.Path) string {
	key, err := encodeResiduePathKey(p)
	if err != nil {
		return err.Error()
	}
	return key
}

// TestResidueLeafPathCandidatesRefusesUnderSecretsRefuse is the write-time
// half of the secrets=refuse boundary: under [strict.Refuse],
// [identity.CredentialMaterial] sees the schema-Sensitive client_secret
// leaf (its own walk descends nested blocks, unlike residueEligibleBlock's
// shallower per-block question) and the whole schema is refused before any
// candidate is built - the same gate [residueCandidates] is already
// subject to for a flat Sensitive attribute, now reached from a nested one
// too.
func TestResidueLeafPathCandidatesRefusesUnderSecretsRefuse(t *testing.T) {
	schema := listenerLikeSchema()
	applied := listenerApplied()

	candidates := residueLeafPathCandidates(schema, applied, strict.Refuse)
	if len(candidates) != 0 {
		t.Fatalf("residueLeafPathCandidates under secrets=refuse returned %d candidate(s), want 0: %v", len(candidates), candidates)
	}
}

// TestClassifyResiduePathsCapturesTheSecretAndLeavesTheEchoedSiblingUnrecorded
// is this file's own mutation check (HANDOFF item 7: "prove your checks can
// fail"). client_id sits at the identical depth, through the identical
// nesting chain, as client_secret - the only schema difference is the
// Sensitive flag - so if this test only ever ran with echoClientID=true (a
// provider that genuinely echoes it, the real one) and never asserted the
// negative, a discriminator that accidentally captured EVERY leaf in the
// block regardless of what the provider does would pass it just as easily.
// Flipping echoClientID to false (case "provider never echoes client_id
// either", a hypothetical bug or a different provider) is what proves the
// exclusion comes from the two-read discriminator actually asking the
// provider, not from a name check that happens to skip "client_id".
func TestClassifyResiduePathsCapturesTheSecretAndLeavesTheEchoedSiblingUnrecorded(t *testing.T) {
	schema := listenerLikeSchema()
	applied := listenerApplied()
	candidates := residueLeafPathCandidates(schema, applied, strict.DefaultSecrets)
	identityAttrs := residueIdentityAttrs(schema)

	t.Run("real provider (client_id echoed)", func(t *testing.T) {
		attrs, ok := classifyResiduePaths(applied, candidates, identityAttrs, listenerLikeRead(true), nil)
		if !ok {
			t.Fatal("classifyResiduePaths proved nothing, want the client_secret leaf")
		}
		if len(attrs) != 1 {
			t.Fatalf("classified %d attribute(s), want exactly 1 (client_secret): %v", len(attrs), attrs)
		}
		key, err := encodeResiduePathKey(listenerClientSecretPath())
		if err != nil {
			t.Fatalf("encodeResiduePathKey: %s", err)
		}
		got, held := attrs[key]
		if !held {
			t.Fatalf("client_secret's path was not classified as residue; got keys %v", keysOf(attrs))
		}
		want := cty.StringVal("super-secret-value")
		if !got.RawEquals(want) {
			t.Fatalf("classified value = %#v, want %#v", got, want)
		}
	})

	t.Run("mutation: provider never echoes client_id either", func(t *testing.T) {
		// client_id is not itself a residueLeafPathCandidates candidate
		// under the real schema (it is not Sensitive), so to see the
		// discriminator actually exercise it, ask it about a candidate
		// list that ALSO names client_id's own path - the schema-eligibility
		// question residueLeafPathCandidates already answers is orthogonal
		// to the read-A/read-B question classifyResiduePaths answers, and
		// this subtest isolates the second one.
		clientIDPath := cty.Path{
			cty.GetAttrStep{Name: "default_action"},
			cty.IndexStep{Key: cty.NumberIntVal(0)},
			cty.GetAttrStep{Name: "authenticate_oidc"},
			cty.IndexStep{Key: cty.NumberIntVal(0)},
			cty.GetAttrStep{Name: "client_id"},
		}
		widened := append([]residuePathCandidate{}, candidates...)
		widened = append(widened, residuePathCandidate{Path: clientIDPath, ConfigSourced: true})

		attrs, ok := classifyResiduePaths(applied, widened, identityAttrs, listenerLikeRead(false), nil)
		if !ok {
			t.Fatal("classifyResiduePaths proved nothing, want both leaves under the mutated read")
		}
		if len(attrs) != 2 {
			t.Fatalf("classified %d attribute(s) under the mutated read, want 2 (client_secret AND client_id): %v", len(attrs), attrs)
		}
		key, err := encodeResiduePathKey(clientIDPath)
		if err != nil {
			t.Fatalf("encodeResiduePathKey: %s", err)
		}
		got, held := attrs[key]
		if !held {
			t.Fatal("under a provider that never echoes client_id, its path must be classified as residue too - proving the exclusion in the real-provider subtest came from the discriminator, not a name check")
		}
		if want := cty.StringVal("client-id-123"); !got.RawEquals(want) {
			t.Fatalf("classified client_id = %#v, want %#v", got, want)
		}
	})
}

// TestFillResiduePathsFillsTheNestedSecretAndPreservesEverythingEchoed is
// the read side of the round trip: a cold read (client_secret null,
// everything else populated, exactly what [listenerLikeRead] produces from
// a bare identity-only prior) is filled from the classified record, and
// only the one recorded leaf changes.
func TestFillResiduePathsFillsTheNestedSecretAndPreservesEverythingEchoed(t *testing.T) {
	schema := listenerLikeSchema()
	applied := listenerApplied()
	candidates := residueLeafPathCandidates(schema, applied, strict.DefaultSecrets)
	attrs, ok := classifyResiduePaths(applied, candidates, residueIdentityAttrs(schema), listenerLikeRead(true), nil)
	if !ok {
		t.Fatal("classifyResiduePaths proved nothing")
	}

	stub, err := identityOnly(applied, residueIdentityAttrs(schema))
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}
	cold, err := listenerLikeRead(true)(stub)
	if err != nil {
		t.Fatalf("listenerLikeRead: %s", err)
	}
	coldSecret, err := listenerClientSecretPath().Apply(cold)
	if err != nil {
		t.Fatalf("applying the client_secret path to the cold read: %s", err)
	}
	if !coldSecret.IsNull() {
		t.Fatal("the cold read already answered client_secret, so this fixture is not reproducing the defect")
	}

	filled, n := fillResiduePaths(cold, schema.Block, attrs, strict.DefaultSecrets)
	if n != 1 {
		t.Fatalf("fillResiduePaths filled %d, want 1", n)
	}
	filledSecret, err := listenerClientSecretPath().Apply(filled)
	if err != nil {
		t.Fatalf("applying the client_secret path to the filled object: %s", err)
	}
	if want := cty.StringVal("super-secret-value"); !filledSecret.RawEquals(want) {
		t.Fatalf("filled client_secret = %#v, want %#v", filledSecret, want)
	}

	// Every attribute the provider DID echo must be byte-for-byte the cold
	// read's own answer, unmoved - the same "a record can only ever speak
	// where the cloud said nothing" rule [fillResidue]'s doc comment
	// states for the flat case.
	for _, name := range []string{"authorization_endpoint", "client_id", "issuer", "token_endpoint", "user_info_endpoint"} {
		p := cty.Path{
			cty.GetAttrStep{Name: "default_action"},
			cty.IndexStep{Key: cty.NumberIntVal(0)},
			cty.GetAttrStep{Name: "authenticate_oidc"},
			cty.IndexStep{Key: cty.NumberIntVal(0)},
			cty.GetAttrStep{Name: name},
		}
		coldVal, err := p.Apply(cold)
		if err != nil {
			t.Fatalf("applying %s to the cold read: %s", name, err)
		}
		filledVal, err := p.Apply(filled)
		if err != nil {
			t.Fatalf("applying %s to the filled object: %s", name, err)
		}
		if !filledVal.RawEquals(coldVal) {
			t.Errorf("%s moved from %#v to %#v; fillResiduePaths must never touch an attribute the provider echoed", name, coldVal, filledVal)
		}
	}

	// And the mark this file's own class of defect is about: after
	// fillResidueFor's own trailing markSchemaSensitive call (reproduced
	// here directly, since this test drives fillResiduePaths below that
	// layer), the filled client_secret carries exactly the Sensitive mark
	// the schema declares - restored generically, at this depth, by the
	// SAME call every flat residue fill already goes through.
	marked := markSchemaSensitive(filled, schema.Block)
	markedSecret, err := listenerClientSecretPath().Apply(marked)
	if err != nil {
		t.Fatalf("applying the client_secret path to the marked object: %s", err)
	}
	if !markedSecret.IsMarked() {
		t.Fatal("the filled client_secret must carry the schema's Sensitive mark after markSchemaSensitive, and does not")
	}
	unmarkedSecret, pvms := markedSecret.UnmarkDeepWithPaths()
	if !unmarkedSecret.RawEquals(cty.StringVal("super-secret-value")) {
		t.Fatalf("the marked value's own content = %#v, want %#v", unmarkedSecret, cty.StringVal("super-secret-value"))
	}
	sawSensitive := false
	for _, pvm := range pvms {
		for m := range pvm.Marks {
			if m == marks.Sensitive {
				sawSensitive = true
			}
		}
	}
	if !sawSensitive {
		t.Fatal("no marks.Sensitive mark found on the filled client_secret")
	}
}

// TestFillResiduePathsRefusesUnderSecretsRefuse is fillResiduePaths' own
// re-check of today's schema (the same re-ask [fillResidue]'s doc comment
// explains): even a record already written, from a run where secrets was
// "store", must stop being filled the moment this run's own setting is
// "refuse".
func TestFillResiduePathsRefusesUnderSecretsRefuse(t *testing.T) {
	schema := listenerLikeSchema()
	applied := listenerApplied()
	candidates := residueLeafPathCandidates(schema, applied, strict.DefaultSecrets)
	attrs, ok := classifyResiduePaths(applied, candidates, residueIdentityAttrs(schema), listenerLikeRead(true), nil)
	if !ok {
		t.Fatal("classifyResiduePaths proved nothing")
	}

	stub, err := identityOnly(applied, residueIdentityAttrs(schema))
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}
	cold, err := listenerLikeRead(true)(stub)
	if err != nil {
		t.Fatalf("listenerLikeRead: %s", err)
	}

	_, n := fillResiduePaths(cold, schema.Block, attrs, strict.Refuse)
	if n != 0 {
		t.Fatalf("fillResiduePaths filled %d attribute(s) under secrets=refuse, want 0", n)
	}
}

// TestRecordResidueForInstanceWritesTheNestedSecretEnvelope is the
// end-to-end write: a real on-disk RecordStore, RecordResidueForInstance
// exactly as live-import's ratify.go calls it, and a byte-level assertion
// on the envelope written - the nested path present as its own key,
// alongside the record's own type-tagged value, per this file's own
// [residuePathCandidate] design (see [encodeResiduePathKey]'s doc comment
// for why the key can never collide with a flat attribute name).
func TestRecordResidueForInstanceWritesTheNestedSecretEnvelope(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	backing, err := staterecord.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := NewRecordEnvelopeStore(backing, RecordKeyPrefix("alb-oidc-estate"))

	schema := listenerLikeSchema()
	applied := listenerApplied()
	addr := locatedTestAddr(t, "aws_lb_listener", "ex-oidc")

	recorded, err := RecordResidueForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, schema, applied, strict.DefaultSecrets, listenerLikeRead(true), cty.NilVal)
	if err != nil {
		t.Fatalf("RecordResidueForInstance: %s", err)
	}
	if !recorded {
		t.Fatal("recorded=false: this fixture reproduces the #401 family-2 shape (client_secret never echoed) and must classify as residue")
	}

	var files []string
	if walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && !strings.HasSuffix(path, ".lock") {
			files = append(files, path)
		}
		return nil
	}); walkErr != nil {
		t.Fatalf("walking the store directory: %s", walkErr)
	}
	if len(files) != 1 {
		t.Fatalf("found %d file(s), want exactly 1: %v", len(files), files)
	}

	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading %s: %s", files[0], err)
	}
	var env struct {
		Residue struct {
			Attributes map[string]struct {
				Type  json.RawMessage `json:"attrType"`
				Value json.RawMessage `json:"attrValue"`
			} `json:"attributes"`
		} `json:"residue"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decoding the raw record: %s\nraw: %s", err, raw)
	}
	if len(env.Residue.Attributes) != 1 {
		t.Fatalf("recorded %d residue attribute(s), want exactly 1 (client_secret's path): %v", len(env.Residue.Attributes), env.Residue.Attributes)
	}
	wantKey, err := encodeResiduePathKey(listenerClientSecretPath())
	if err != nil {
		t.Fatalf("encodeResiduePathKey: %s", err)
	}
	entry, held := env.Residue.Attributes[wantKey]
	if !held {
		t.Fatalf("no entry at the expected path key %s; got keys %v", wantKey, rawKeys(env.Residue.Attributes))
	}
	if !strings.HasPrefix(wantKey, "[") {
		t.Fatalf("the path key %q does not look like the documented JSON-array encoding", wantKey)
	}
	if string(entry.Value) != `"super-secret-value"` {
		t.Errorf("recorded client_secret value = %s, want %q", entry.Value, `"super-secret-value"`)
	}

	// Re-reading through the store's own API must decode the identical
	// value at the identical path - the write and the read side of this
	// change agreeing with each other, not just with the raw bytes.
	attrs, _, _, found, err := store.GetResidue(ctx, addr)
	if err != nil || !found {
		t.Fatalf("GetResidue: err=%v found=%v", err, found)
	}
	got, held := attrs[wantKey]
	if !held {
		t.Fatalf("GetResidue did not return the path-keyed entry; got %v", keysOf(attrs))
	}
	if want := cty.StringVal("super-secret-value"); !got.RawEquals(want) {
		t.Fatalf("GetResidue value = %#v, want %#v", got, want)
	}
}

func rawKeys(m map[string]struct {
	Type  json.RawMessage `json:"attrType"`
	Value json.RawMessage `json:"attrValue"`
}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestEncodeDecodeResiduePathKeyRoundTrips pins [encodeResiduePathKey] and
// [decodeResiduePathKey] against each other and against
// [isResiduePathKey]'s own discriminator, for a path mixing every step
// kind this file's fixture uses: two GetAttrStep names and one IndexStep
// with a number key.
func TestEncodeDecodeResiduePathKeyRoundTrips(t *testing.T) {
	path := listenerClientSecretPath()
	key, err := encodeResiduePathKey(path)
	if err != nil {
		t.Fatalf("encodeResiduePathKey: %s", err)
	}
	if !isResiduePathKey(key) {
		t.Fatalf("isResiduePathKey(%q) = false, want true", key)
	}
	if isResiduePathKey("client_secret") {
		t.Fatal("isResiduePathKey(\"client_secret\") = true, want false - a bare attribute name must never be read as a path key")
	}
	back, err := decodeResiduePathKey(key)
	if err != nil {
		t.Fatalf("decodeResiduePathKey: %s", err)
	}
	if !back.Equals(path) {
		t.Fatalf("round-tripped path = %s, want %s", pathString(back), pathString(path))
	}
}

// TestSchemaAttrAtPathResolvesTheNestedLeafAndRefusesAMovedOne pins
// [schemaAttrAtPath] both ways: it finds today's client_secret leaf at the
// path this file's fixture uses, and it refuses a path that no longer
// matches - the same defensive re-ask [fillResiduePaths] and
// [builder.residueSeedFor] both depend on when a schema changes shape
// between when a record was written and when it is read.
func TestSchemaAttrAtPathResolvesTheNestedLeafAndRefusesAMovedOne(t *testing.T) {
	schema := listenerLikeSchema()

	attr, ok := schemaAttrAtPath(schema.Block, listenerClientSecretPath())
	if !ok || attr == nil {
		t.Fatal("schemaAttrAtPath did not resolve the fixture's own client_secret path")
	}
	if !attr.Sensitive {
		t.Fatal("resolved attribute is not Sensitive; wrong leaf")
	}

	stale := cty.Path{
		cty.GetAttrStep{Name: "default_action"},
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: "authenticate_cognito"}, // never existed in this fixture's schema
		cty.IndexStep{Key: cty.NumberIntVal(0)},
		cty.GetAttrStep{Name: "client_secret"},
	}
	if _, ok := schemaAttrAtPath(schema.Block, stale); ok {
		t.Fatal("schemaAttrAtPath resolved a path naming a block this schema does not have")
	}
}
