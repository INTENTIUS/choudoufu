// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// lambdaLikeSchema is aws_lambda_function's shape reduced to the attributes
// issue #275 turns on, taken from hashicorp/aws 6.58.0's own schema rather
// than invented:
//
//	filename          optional, not computed, not sensitive   (the founding case)
//	source_code_hash  optional AND computed                   (the one a settable-and-not-computed rule misses)
//	publish           optional bool                            (the one a "+ false" can only come from a null prior)
//	description       optional, not computed                   (bit-for-bit identical to filename in the schema,
//	                                                            and the provider DOES return it - which is the whole
//	                                                            reason no schema rule can separate the two)
//	id                optional AND computed                    (the identity, never a candidate)
func lambdaLikeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":               {Type: cty.String, Optional: true, Computed: true},
				"function_name":    {Type: cty.String, Required: true},
				"filename":         {Type: cty.String, Optional: true},
				"source_code_hash": {Type: cty.String, Optional: true, Computed: true},
				"publish":          {Type: cty.Bool, Optional: true},
				"description":      {Type: cty.String, Optional: true},
				"arn":              {Type: cty.String, Computed: true},
			},
		},
	}
}

// lambdaIdentityAttrs is what residueIdentityAttrs derives for this schema:
// the "id" attribute and nothing else, since it declares no identity schema.
func lambdaIdentityAttrs() map[string]bool {
	return residueIdentityAttrs(lambdaLikeSchema())
}

func lambdaApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":               cty.StringVal("check-links"),
		"function_name":    cty.StringVal("check-links"),
		"filename":         cty.StringVal("check_links.py.zip"),
		"source_code_hash": cty.StringVal("82e750d3"),
		"publish":          cty.False,
		"description":      cty.StringVal("link checker"),
		"arn":              cty.StringVal("arn:aws:lambda:eu-west-1:000000000000:function:check-links"),
	})
}

// sdkv2LikeRead is the behavior the AWS provider actually shows for a
// filename-deployed Lambda: description and arn come back from the API,
// filename / source_code_hash / publish are never touched, so whatever the
// prior held for them passes straight through - null included.
func sdkv2LikeRead(prior cty.Value) (cty.Value, error) {
	out := map[string]cty.Value{}
	for name, v := range prior.AsValueMap() {
		out[name] = v
	}
	out["description"] = cty.StringVal("link checker")
	out["arn"] = cty.StringVal("arn:aws:lambda:eu-west-1:000000000000:function:check-links")
	out["id"] = cty.StringVal("check-links")
	out["function_name"] = cty.StringVal("check-links")
	return cty.ObjectVal(out), nil
}

// TestClassifyResidueSeparatesFilenameFromDescription is issue #275's whole
// claim in one assertion, and it is set up so that a WRONG classifier
// cannot pass it.
//
// filename and description are bit-for-bit identical in the schema:
// optional, not computed, not sensitive, not write-only, both set, both
// non-null after the apply. Every static rule measured against
// hashicorp/aws 6.59.0 for issue #275 either takes both or neither. Only
// asking the provider separates them, and this asserts the separation in
// the direction that matters: description is NOT recorded, because
// recording a value the provider gives back is how a record comes to
// contradict the cloud.
func TestClassifyResidueSeparatesFilenameFromDescription(t *testing.T) {
	schema := lambdaLikeSchema()
	applied := lambdaApplied()

	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)
	// function_name is in the set: required, settable, non-null. arn is in
	// the set too: purely Computed, which residueCandidates no longer
	// excludes on its own (see its doc comment) - only id is never a
	// candidate. That any of these is a candidate and not necessarily
	// residue is exactly the point - candidacy is a filter and the
	// provider is what decides.
	wantCandidates := []string{"arn", "description", "filename", "function_name", "publish", "source_code_hash"}
	if !reflect.DeepEqual(candidates, wantCandidates) {
		t.Fatalf("residueCandidates = %v, want %v", candidates, wantCandidates)
	}

	got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), sdkv2LikeRead)
	if !ok {
		t.Fatal("classifyResidue proved nothing for an object whose provider plainly leaves three arguments alone")
	}
	want := map[string]cty.Value{
		"filename":         cty.StringVal("check_links.py.zip"),
		"source_code_hash": cty.StringVal("82e750d3"),
		"publish":          cty.False,
	}
	if len(got) != len(want) {
		t.Fatalf("classified %v, want exactly %v", sortedNames(got), sortedNames(want))
	}
	for name, wv := range want {
		gv, ok := got[name]
		if !ok {
			t.Errorf("%s was not classified as residue, so a cold replan will keep proposing it forever", name)
			continue
		}
		if !gv.RawEquals(wv) {
			t.Errorf("%s classified as %#v, want the APPLIED value %#v", name, gv, wv)
		}
	}
	if _, bad := got["description"]; bad {
		t.Error("description was classified as residue. The provider returns it, so a stored copy can only ever be a second opinion about a value the cloud already answers - and the plan would go empty over real drift.")
	}
}

// TestClassifyResidueLeavesAZeroValueTheProviderAnswers pins the FIRST of
// the classifier's two read-A tests, and it exists because a mutation run
// found nothing else covering it.
//
// The crossing is where this shape actually lives. hashicorp/aws is built
// on the legacy SDK, which cannot represent an absent string, so it answers
// an unset description with "" rather than null - and this estate's own
// aws_cloudwatch_event_target has role_arn = "", input = "", input_path =
// "". Read A therefore "carries no information" for all of them by the
// zero-value rule, and without the applied-value test every one would be
// recorded: a residue record full of empty strings, describing attributes
// the provider answers perfectly well.
//
// The first test catches them because read A already produced exactly the
// applied value. That is the definition of an attribute the provider
// manages, whatever the value happens to be.
func TestClassifyResidueLeavesAZeroValueTheProviderAnswers(t *testing.T) {
	schema := lambdaLikeSchema()
	applied := lambdaApplied().AsValueMap()
	applied["description"] = cty.StringVal("")
	obj := cty.ObjectVal(applied)

	legacyRead := func(prior cty.Value) (cty.Value, error) {
		out := map[string]cty.Value{}
		for name, v := range prior.AsValueMap() {
			out[name] = v
		}
		// The legacy SDK's answer for an unset optional string, from both
		// priors, exactly as floci's AWS provider gives it.
		out["description"] = cty.StringVal("")
		out["arn"] = cty.StringVal("arn:aws:lambda:eu-west-1:000000000000:function:check-links")
		out["id"] = cty.StringVal("check-links")
		out["function_name"] = cty.StringVal("check-links")
		return cty.ObjectVal(out), nil
	}

	got, ok := classifyResidue(obj, residueCandidates(schema, obj, strict.DefaultSecrets), lambdaIdentityAttrs(), legacyRead)
	if !ok {
		t.Fatal("classifyResidue proved nothing at all; the three real residue arguments should still classify")
	}
	if _, bad := got["description"]; bad {
		t.Error("an empty description was recorded. The provider answered it - with the only answer its SDK can give for an unset string - so there is nothing here to remember, and a record full of empty strings is how this mechanism turns into a copy of the object.")
	}
	if _, bad := got["function_name"]; bad {
		t.Error("function_name was recorded even though read A produced it")
	}
}

// TestClassifyResidueRefusesTheFrameworkNull is the discriminator's whole
// reason for existing, and the case a one-read classifier gets wrong.
//
// A plugin-framework resource that maps remote -> state sets an attribute
// to null when the remote genuinely lacks it, REGARDLESS of what the prior
// held. Read A alone cannot tell that apart from an SDKv2 resource leaving
// the prior untouched: both answer null. Read B is where they diverge, and
// recording this one would fill prior state with a value the remote does
// not have - which makes the plan go empty over drift that is real.
func TestClassifyResidueRefusesTheFrameworkNull(t *testing.T) {
	schema := lambdaLikeSchema()
	applied := lambdaApplied()

	frameworkRead := func(prior cty.Value) (cty.Value, error) {
		out := map[string]cty.Value{}
		for name, v := range prior.AsValueMap() {
			out[name] = v
		}
		// The remote has no filename, and the provider says so on every
		// read, whatever the prior held.
		out["filename"] = cty.NullVal(cty.String)
		out["description"] = cty.StringVal("link checker")
		return cty.ObjectVal(out), nil
	}

	got, ok := classifyResidue(applied, residueCandidates(schema, applied, strict.DefaultSecrets), lambdaIdentityAttrs(), frameworkRead)
	if ok {
		if _, bad := got["filename"]; bad {
			t.Fatal("filename was recorded from a provider that answers null for it on EVERY read. " +
				"That is the remote's real answer, and filling it from a record masks real drift - the exact failure the two-read discriminator exists to avoid.")
		}
	}
	if _, bad := got["filename"]; bad {
		t.Fatal("filename was recorded despite the provider clearing it on a read with the applied prior")
	}
}

// TestClassifyResidueFailsClosed is acceptance (c) of issue #275's brief:
// a candidate the classifier could not classify is not stored, and an
// instance whose reads did not work is not stored AT ALL.
//
// Each case is a different way for the answer to be missing, and every one
// of them has to end in "nothing recorded" rather than "record what we
// have". A wrong stored value produces an EMPTY plan that is wrong, and an
// empty plan that is wrong is invisible to every verdict-level check this
// repository has.
func TestClassifyResidueFailsClosed(t *testing.T) {
	schema := lambdaLikeSchema()
	applied := lambdaApplied()
	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)

	// Each case builds its own reader, so a counter in one cannot leak into
	// the next. The first two exist separately because the two reads have
	// separate guards, and a mutation that removes ONE of them has to be
	// caught by a case where the OTHER read succeeds - otherwise the
	// surviving guard closes the door and the mutation reads as caught when
	// nothing caught it. That mistake happened while writing this test: the
	// first pass had no "only the first read fails" case, and removing read
	// A's guard left the suite green.
	for _, tc := range []struct {
		name string
		read func() residueReader
	}{
		{
			name: "both reads error",
			read: func() residueReader {
				return func(cty.Value) (cty.Value, error) { return cty.NilVal, errors.New("connection reset") }
			},
		},
		{
			name: "only the first read errors",
			read: func() residueReader {
				n := 0
				return func(prior cty.Value) (cty.Value, error) {
					n++
					if n == 1 {
						return cty.NilVal, errors.New("connection reset")
					}
					return sdkv2LikeRead(prior)
				}
			},
		},
		{
			name: "only the second read errors",
			read: func() residueReader {
				n := 0
				return func(prior cty.Value) (cty.Value, error) {
					n++
					if n == 2 {
						return cty.NilVal, errors.New("throttled")
					}
					return sdkv2LikeRead(prior)
				}
			},
		},
		{
			name: "only the first read finds the object gone",
			read: func() residueReader {
				n := 0
				return func(prior cty.Value) (cty.Value, error) {
					n++
					if n == 1 {
						return cty.NullVal(cty.EmptyObject), nil
					}
					return sdkv2LikeRead(prior)
				}
			},
		},
		{
			name: "the object is gone",
			read: func() residueReader {
				return func(cty.Value) (cty.Value, error) { return cty.NullVal(cty.EmptyObject), nil }
			},
		},
		{
			name: "the provider answers with no object at all",
			read: func() residueReader {
				return func(cty.Value) (cty.Value, error) { return cty.NilVal, nil }
			},
		},
		{
			name: "the provider answers with a string",
			read: func() residueReader {
				return func(cty.Value) (cty.Value, error) { return cty.StringVal("what"), nil }
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), tc.read())
			if ok || len(got) != 0 {
				t.Fatalf("classifyResidue returned ok=%v with %v. Every one of these is a missing answer, and a missing answer must record NOTHING.", ok, sortedNames(got))
			}
		})
	}
}

// TestClassifyResidueOverAProviderThatReadsNothing is the pathological case
// my own fail-closed test first mistook for a failure, written down rather
// than smoothed over, because the reasoning is the load-bearing part.
//
// A provider whose Read echoes its prior manages NOTHING, so by the rule
// every candidate is residue and every candidate is recorded. That looks
// alarming - a residue record holding most of an object is close to the
// second state file issue #73 removes - and it is nonetheless right. For
// such a type stock OpenTofu's state file is the only source of truth too,
// and choudoufu without a record would re-propose the whole object on every
// cold replan and never converge. Parity is the bar.
//
// What bounds it is the candidate filter, not a size limit: no identity, no
// secrets, no write-only attributes, and nothing the provider answers for.
// A record that grows is a provider that reads nothing, which is a fact
// about that provider. "arn" is Computed only in [lambdaLikeSchema] and
// [residueCandidates] no longer excludes an attribute for that reason alone
// (see its own doc comment) - a provider that manages nothing does not
// derive arn from the remote either, so it is exactly as much residue as
// every other candidate here, and this test now expects it recorded rather
// than treating it as a second stand-in for the identity.
func TestClassifyResidueOverAProviderThatReadsNothing(t *testing.T) {
	schema := lambdaLikeSchema()
	applied := lambdaApplied()
	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)

	echo := func(prior cty.Value) (cty.Value, error) { return prior, nil }
	got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), echo)
	if !ok {
		t.Fatal("classifyResidue proved nothing for a provider that manages nothing; that estate would never converge")
	}
	if !reflect.DeepEqual(sortedNames(got), candidates) {
		t.Fatalf("classified %v, want every candidate %v", sortedNames(got), candidates)
	}
	if _, bad := got["id"]; bad {
		t.Error("\"id\" was recorded even from an echo provider. The candidate filter, not the classifier, is what keeps the identity out.")
	}
}

// secretLambdaSchema is [lambdaLikeSchema] with filename marked Sensitive,
// which is aws_db_instance.password's shape reduced to this file's fixture:
// a settable argument the provider marks sensitive, on a type whose other
// arguments are ordinary. It is what GitHub issue #365's secrets setting is
// about at both granularities at once - the attribute itself, and the
// whole-type identity.CredentialMaterial veto its presence triggers.
func secretLambdaSchema() providers.Schema {
	s := lambdaLikeSchema()
	s.Block.Attributes["filename"] = &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}
	return s
}

// secretLambdaApplied is [lambdaApplied] with filename marked the way
// [importAndRead] marks it: [markSchemaSensitive] over the provider's
// unmarked wire answer, so the mark is on the whole attribute value and is
// exactly the one the schema produces. Building it through that function
// rather than by hand is the point - a mark this fixture invented would not
// prove [residueMarkRecoverable] recognises the real one.
func secretLambdaApplied() cty.Value {
	return markSchemaSensitive(lambdaApplied(), secretLambdaSchema().Block)
}

// TestResidueCandidatesUnderSecretsStore is the other half of
// [TestResidueCandidatesExcludeSecretsAndIdentity]: what the default setting
// admits, asserted attribute by attribute rather than as a count.
//
// Three claims, and the third is the one that makes the first two safe.
func TestResidueCandidatesUnderSecretsStore(t *testing.T) {
	t.Run("the sensitive attribute and its type are both admitted", func(t *testing.T) {
		got := residueCandidates(secretLambdaSchema(), secretLambdaApplied(), strict.Store)
		want := []string{"arn", "description", "filename", "function_name", "publish", "source_code_hash"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("candidates under secrets=store: %v, want %v.\nThe whole-type identity.CredentialMaterial veto must not fire, and the sensitive attribute itself must be in the set: stock OpenTofu's state file holds this value, so refusing it is HANDOFF.md's first difference row.", got, want)
		}
	})

	t.Run("secrets=refuse takes the whole type back out", func(t *testing.T) {
		if got := residueCandidates(secretLambdaSchema(), secretLambdaApplied(), strict.Refuse); len(got) != 0 {
			t.Fatalf("candidates under secrets=refuse: %v, want none", got)
		}
	})

	t.Run("a mark the schema cannot put back is refused under either setting", func(t *testing.T) {
		// The sensitive-VARIABLE case: the value is marked, the schema is
		// not. Nothing in a later read could restore this mark, so storing
		// the value would fill an unmarked prior against a marked planned
		// value - a perpetual sensitivity-only update. See
		// [residueMarkRecoverable].
		s := lambdaLikeSchema()
		applied := lambdaApplied()
		attrs := applied.AsValueMap()
		attrs["description"] = attrs["description"].Mark(marks.Sensitive)
		applied = cty.ObjectVal(attrs)

		for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
			for _, name := range residueCandidates(s, applied, secrets) {
				if name == "description" {
					t.Fatalf("an attribute marked sensitive by something OTHER than the schema reached the candidate set under secrets=%s; markSchemaSensitive could never put that mark back", secrets)
				}
			}
		}
	})
}

// TestResidueRoundTripsASensitiveArgumentWithItsMark is population 3 of
// GitHub issue #365 slice 3 asserted BY VALUE, end to end, which is what
// HANDOFF.md's safety rule requires of anything that changes what a record
// holds: "convergence is never evidence an identity is right".
//
// Two values are asserted and both matter. The filled attribute must come
// back with the value the apply sent - a record that fills the wrong string
// is worse than one that fills nothing - and it must come back MARKED, or
// the plan's "before" side disagrees with its re-marked "after" side on
// sensitivity alone and proposes an update forever (sensitivepaths.go's
// header is the long form).
//
// The store leg is real rather than mocked: the value goes through
// ResidueStore.Put, which refuses a marked value outright, so this also pins
// that the unmarking happens before the write and not after.
func TestResidueRoundTripsASensitiveArgumentWithItsMark(t *testing.T) {
	ctx := context.Background()
	schema := secretLambdaSchema()
	applied := secretLambdaApplied()
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")

	candidates := residueCandidates(schema, applied, strict.Store)
	unmarked, _ := applied.UnmarkDeep()
	attrs, ok := classifyResidue(unmarked, candidates, lambdaIdentityAttrs(), sdkv2LikeRead)
	if !ok {
		t.Fatal("classifyResidue proved nothing for a type whose sensitive argument the provider never returns")
	}
	if got, want := attrs["filename"], cty.StringVal("check_links.py.zip"); !got.RawEquals(want) {
		t.Fatalf("classified filename = %#v, want %#v", got, want)
	}

	store := NewResidueStore(localHintStore(t), "my-estate")
	if _, err := store.Put(ctx, addr, attrs, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	back, _, exists, err := store.Get(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("Get: err=%v exists=%v", err, exists)
	}

	cold, err := identityOnly(unmarked, lambdaIdentityAttrs())
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}
	cold, err = sdkv2LikeRead(cold)
	if err != nil {
		t.Fatalf("cold read: %s", err)
	}

	filled, n := fillResidue(cold, schema.Block, back, strict.Store)
	if n == 0 {
		t.Fatal("filled nothing from a record written under secrets=store")
	}
	// The caller's own step, asserted here because it is what restores the
	// mark: see builder.fillResidueFor.
	filled = markSchemaSensitive(filled, schema.Block)

	got := filled.GetAttr("filename")
	if !got.IsMarked() {
		t.Fatal("filename came back from the record unmarked. An unmarked prior against a marked planned value is a sensitivity-only update the estate proposes on every run.")
	}
	if plain, _ := got.Unmark(); !plain.RawEquals(cty.StringVal("check_links.py.zip")) {
		t.Fatalf("filename came back as %#v, want the value the apply sent", plain)
	}

	// And the refusing setting declines the SENSITIVE attribute out of the
	// same record while still filling the ordinary ones beside it. Asserted
	// per attribute rather than as a count, because "filled nothing" would
	// pass for the wrong reason if the record were empty.
	strictFilled, _ := fillResidue(cold, schema.Block, back, strict.Refuse)
	if !strictFilled.GetAttr("filename").IsNull() {
		t.Fatalf("secrets=refuse filled filename = %#v from a record; that setting is \"sensitive settable arguments never recorded\", and never read back either", strictFilled.GetAttr("filename"))
	}
	if got, want := strictFilled.GetAttr("source_code_hash"), cty.StringVal("82e750d3"); !got.RawEquals(want) {
		t.Fatalf("secrets=refuse also declined the ordinary attribute beside it: source_code_hash = %#v, want %#v", got, want)
	}
}

// TestResidueCandidatesExcludeSecretsAndIdentity pins the populations
// residueCandidates keeps out entirely, each for a different reason. It is
// deliberately not a "the filter works" test: it asserts the exclusions
// individually so removing any ONE of them turns this red.
//
// The two sensitivity exclusions are asserted under strict.Refuse, which is
// what they now depend on - GitHub issue #365 slice 3 made them the toggle
// rather than the default. Their other half, that strict.Store lets exactly
// these two through and nothing else, is
// [TestResidueCandidatesUnderSecretsStore].
func TestResidueCandidatesExcludeSecretsAndIdentity(t *testing.T) {
	base := lambdaLikeSchema()

	t.Run("a sensitive attribute is never a candidate under secrets=refuse", func(t *testing.T) {
		s := lambdaLikeSchema()
		s.Block.Attributes["filename"] = &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}
		for _, name := range residueCandidates(s, lambdaApplied(), strict.Refuse) {
			if name == "filename" {
				t.Fatal("a sensitive attribute reached the candidate set under secrets=refuse. That setting is HANDOFF.md's \"sensitive settable arguments never recorded\", and a residue record is a record.")
			}
		}
	})

	t.Run("a write-only attribute is never a candidate", func(t *testing.T) {
		s := lambdaLikeSchema()
		s.Block.Attributes["filename"] = &configschema.Attribute{Type: cty.String, Optional: true, WriteOnly: true}
		for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
			for _, name := range residueCandidates(s, lambdaApplied(), secrets) {
				if name == "filename" {
					t.Fatalf("a write-only attribute reached the candidate set under secrets=%s. The protocol forbids a provider ever returning it, and writing it down does not make it returnable - no setting may reach this exclusion.", secrets)
				}
			}
		}
	})

	t.Run("credential material excludes the whole type under secrets=refuse", func(t *testing.T) {
		s := lambdaLikeSchema()
		s.Block.Attributes["secret"] = &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}
		if got := residueCandidates(s, lambdaApplied(), strict.Refuse); len(got) != 0 {
			t.Fatalf("a type carrying secret material produced candidates %v under secrets=refuse. The whole-type form of identity.CredentialMaterial is what that setting turns on.", got)
		}
	})

	t.Run("the identity is never a candidate", func(t *testing.T) {
		s := lambdaLikeSchema()
		s.IdentitySchema = &configschema.Object{Attributes: map[string]*configschema.Attribute{
			"function_name": {Type: cty.String, Required: true},
		}}
		for _, name := range residueCandidates(s, lambdaApplied(), strict.DefaultSecrets) {
			if name == "function_name" || name == "id" {
				t.Fatalf("%q reached the candidate set. An identity attribute says WHICH object this is; a residue record must never be able to move an instance onto a different object.", name)
			}
		}
	})

	t.Run("a computed-only attribute is a candidate", func(t *testing.T) {
		// Reversed 2026-08-21 (corpus-xancloud-iac): this used to assert the
		// opposite, on the reasoning that a purely Computed attribute "cannot
		// be set in configuration, so there is nothing to remember". That
		// reasoning was wrong for aws_nat_gateway.regional_nat_gateway_address,
		// which is Computed only and whose provider does not re-derive it
		// from a bare identity-only prior - see [residueCandidates]'s own doc
		// comment. "arn" (also Computed only in lambdaLikeSchema) now reaches
		// the candidate set; whether it is ever actually RECORDED is
		// [classifyResidue]'s question, not this filter's.
		found := false
		for _, name := range residueCandidates(base, lambdaApplied(), strict.DefaultSecrets) {
			if name == "arn" {
				found = true
			}
		}
		if !found {
			t.Fatal("arn did not reach the candidate set; a purely Computed attribute must still be considered, since safety comes from classifyResidue's two-read discriminator and not from this schema-shape filter")
		}
	})
}

// TestFillResidueNeverOverwritesTheCloud is the plan side's one safety
// rule. A record may only ever speak where the provider said nothing.
func TestFillResidueNeverOverwritesTheCloud(t *testing.T) {
	block := lambdaLikeSchema().Block

	read := cty.ObjectVal(map[string]cty.Value{
		"id":               cty.StringVal("check-links"),
		"function_name":    cty.StringVal("check-links"),
		"filename":         cty.NullVal(cty.String),
		"source_code_hash": cty.NullVal(cty.String),
		"publish":          cty.NullVal(cty.Bool),
		"description":      cty.StringVal("what the cloud says"),
		"arn":              cty.StringVal("arn:..."),
	})
	rec := map[string]cty.Value{
		"filename":    cty.StringVal("check_links.py.zip"),
		"publish":     cty.False,
		"description": cty.StringVal("what the record says"),
	}

	got, n := fillResidue(read, block, rec, strict.DefaultSecrets)
	if n != 2 {
		t.Fatalf("filled %d attributes, want 2 (filename and publish; description was answered by the provider)", n)
	}
	if got.GetAttr("description").AsString() != "what the cloud says" {
		t.Error("fillResidue overwrote a value the provider returned. A record that can outrank the cloud is a plan that goes empty over real drift.")
	}
	if got.GetAttr("filename").AsString() != "check_links.py.zip" {
		t.Error("filename was not filled from the record, so the plan will propose sending the zip again")
	}
	if got.GetAttr("source_code_hash").IsNull() != true {
		t.Error("source_code_hash was filled from a record that does not mention it")
	}
}

// TestFillResidueRefusesAMismatchedType pins the provider-upgrade case: a
// record written when an attribute was a string, read back when it is a
// list, is skipped rather than converted. Converting would be guessing at
// what an older provider meant, and a wrong prior-state value is invisible.
func TestFillResidueRefusesAMismatchedType(t *testing.T) {
	block := lambdaLikeSchema().Block
	read := cty.ObjectVal(map[string]cty.Value{
		"id":               cty.StringVal("x"),
		"function_name":    cty.StringVal("x"),
		"filename":         cty.NullVal(cty.String),
		"source_code_hash": cty.NullVal(cty.String),
		"publish":          cty.NullVal(cty.Bool),
		"description":      cty.NullVal(cty.String),
		"arn":              cty.NullVal(cty.String),
	})
	got, n := fillResidue(read, block, map[string]cty.Value{
		"filename": cty.ListVal([]cty.Value{cty.StringVal("a.zip")}),
	}, strict.DefaultSecrets)
	if n != 0 || !got.GetAttr("filename").IsNull() {
		t.Fatalf("filled %d attributes from a record whose recorded type no longer fits the schema", n)
	}
}

// TestFillResidueRefusesASensitiveOrWriteOnlyTarget is the fill side of the
// candidate filter, and it exists because the two are separated in time. A
// record written months ago against a schema where filename was ordinary
// must not be applied after a provider release marks it sensitive or
// write-only - both are hard, protocol-level rules unrelated to whether the
// attribute happens to be Computed, which [fillResidue] no longer asks (see
// its own doc comment and [TestFillResidueFillsAComputedOnlyAttribute]
// below, which used to be a third case here expecting the opposite).
func TestFillResidueRefusesASensitiveOrWriteOnlyTarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		attr    *configschema.Attribute
		secrets strict.Secrets
	}{
		// Sensitive is now the toggle's business (GitHub issue #365 slice
		// 3), so the refusal is asserted under the setting that makes it
		// one. Under strict.Store it is meant to fill, which is what
		// TestResidueRoundTripsASensitiveArgumentWithItsMark asserts by
		// value.
		{"sensitive now, under secrets=refuse", &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}, strict.Refuse},
		// Write-only is not, and both settings are asserted so that a
		// future widening of the toggle cannot reach it silently.
		{"write-only now, under secrets=store", &configschema.Attribute{Type: cty.String, Optional: true, WriteOnly: true}, strict.Store},
		{"write-only now, under secrets=refuse", &configschema.Attribute{Type: cty.String, Optional: true, WriteOnly: true}, strict.Refuse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := lambdaLikeSchema()
			s.Block.Attributes["filename"] = tc.attr
			read := cty.ObjectVal(map[string]cty.Value{
				"id":               cty.StringVal("x"),
				"function_name":    cty.StringVal("x"),
				"filename":         cty.NullVal(cty.String),
				"source_code_hash": cty.NullVal(cty.String),
				"publish":          cty.NullVal(cty.Bool),
				"description":      cty.NullVal(cty.String),
				"arn":              cty.NullVal(cty.String),
			})
			_, n := fillResidue(read, s.Block, map[string]cty.Value{"filename": cty.StringVal("a.zip")}, tc.secrets)
			if n != 0 {
				t.Fatalf("filled filename from a record even though the current schema says %s", tc.name)
			}
		})
	}
}

// TestFillResidueFillsAComputedOnlyAttribute is aws_nat_gateway's
// regional_nat_gateway_address case reduced to [fillResidue] alone: a
// purely Computed attribute (never Required, never Optional) whose current
// read carries no information gets filled from its record exactly like an
// Optional+Computed one would. Reversed 2026-08-21 from the opposite
// expectation - see [TestFillResidueRefusesASensitiveOrWriteOnlyTarget]'s
// doc comment for why the old assumption undercounted a real defect.
func TestFillResidueFillsAComputedOnlyAttribute(t *testing.T) {
	s := lambdaLikeSchema()
	s.Block.Attributes["filename"] = &configschema.Attribute{Type: cty.String, Computed: true}
	read := cty.ObjectVal(map[string]cty.Value{
		"id":               cty.StringVal("x"),
		"function_name":    cty.StringVal("x"),
		"filename":         cty.NullVal(cty.String),
		"source_code_hash": cty.NullVal(cty.String),
		"publish":          cty.NullVal(cty.Bool),
		"description":      cty.NullVal(cty.String),
		"arn":              cty.NullVal(cty.String),
	})
	got, n := fillResidue(read, s.Block, map[string]cty.Value{"filename": cty.StringVal("a.zip")}, strict.DefaultSecrets)
	if n != 1 {
		t.Fatalf("filled %d attributes, want 1 - a Computed-only attribute whose read carries no information must be fillable from its record", n)
	}
	if got.GetAttr("filename").AsString() != "a.zip" {
		t.Fatal("filename was not filled from the record")
	}
}

// TestResidueKeysAreInvisibleToOrphanDiscovery is issue #275's version of
// issue #270's central safety property, and the reason a fifth namespace
// root exists rather than a subdirectory of an existing one.
//
// builder.discoverOrphanedRecords lists [Options.RecordKeyPrefix] and
// materializes every key it can decode as an UNDECLARED prior-state entry,
// which makes the plan propose DESTROYING it. A residue key names arguments
// of a live cloud object the estate owns and the record namespace has no
// authority over, so a residue key reaching that listing is a cloud
// deletion driven by a note about a filename.
//
// Proven three ways, exactly as the located version is: lexically,
// functionally through the real List call, and by decoding.
func TestResidueKeysAreInvisibleToOrphanDiscovery(t *testing.T) {
	const estate = "my-estate"
	ctx := context.Background()

	recordPrefix := RecordKeyPrefix(estate)
	residuePrefix := ResidueKeyPrefix(estate)

	if strings.HasPrefix(residuePrefix, recordPrefix+"/") || residuePrefix == recordPrefix {
		t.Fatalf("ResidueKeyPrefix(%q) = %q lives under RecordKeyPrefix %q; orphan discovery would list it", estate, residuePrefix, recordPrefix)
	}
	if strings.HasPrefix(recordPrefix, residueNamespaceRoot+"/") {
		t.Fatalf("RecordKeyPrefix(%q) = %q lives under the residue namespace %q", estate, recordPrefix, residueNamespaceRoot)
	}
	for _, other := range []string{hintNamespaceRoot, locatedNamespaceRoot, "tofu-receipts"} {
		if residueNamespaceRoot == other || strings.HasPrefix(residueNamespaceRoot, other+"/") {
			t.Errorf("the residue namespace %q collides with %q", residueNamespaceRoot, other)
		}
	}

	store := localHintStore(t)
	recAddr := locatedTestAddr(t, "terraform_data", "seed")
	recordKey := RecordKey(recordPrefix, recAddr)
	if _, err := store.PutIfAbsent(ctx, recordKey, []byte(`{"value_type":"\"string\"","attrs":"\"x\""}`)); err != nil {
		t.Fatalf("writing the record fixture: %s", err)
	}
	resAddr := locatedTestAddr(t, "aws_lambda_function", "check-links")
	residue := NewResidueStore(store, estate)
	if _, err := residue.Put(ctx, resAddr, map[string]cty.Value{"filename": cty.StringVal("check_links.py.zip")}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}

	keys, err := store.List(ctx, recordPrefix)
	if err != nil {
		t.Fatalf("List(%q): %s", recordPrefix, err)
	}
	if len(keys) != 1 || keys[0] != recordKey {
		t.Errorf("List(%q) = %v, want exactly the one record key %q.\n"+
			"A residue key reaching orphan discovery is a cloud deletion driven by a note about arguments.", recordPrefix, keys, recordKey)
	}

	if got, ok := RecordAddr(recordPrefix, ResidueKey(estate, resAddr)); ok {
		t.Errorf("RecordAddr decoded a residue key into %s under the record prefix; it must refuse it", got)
	}
}

// TestResidueStoreExposesNoEnumeration is the other half of the same
// construction, and the load-bearing assertion is the second one: if
// *ResidueStore satisfied staterecord.Store it could be handed straight to
// builder.discoverOrphanedRecords, and every argument about which prefix
// that function lists would be beside the point.
func TestResidueStoreExposesNoEnumeration(t *testing.T) {
	ty := reflect.TypeOf(&ResidueStore{})
	want := map[string]bool{"Get": true, "Put": true, "Delete": true}
	for i := 0; i < ty.NumMethod(); i++ {
		name := ty.Method(i).Name
		if !want[name] {
			t.Errorf("*ResidueStore has an exported method %q. The only three permitted are Get, Put and Delete, all keyed by a declared address; "+
				"anything that can return a set of keys re-creates the enumeration this namespace exists to stay out of.", name)
		}
		delete(want, name)
	}
	for name := range want {
		t.Errorf("*ResidueStore no longer has a %q method", name)
	}

	storeIface := reflect.TypeOf((*staterecord.Store)(nil)).Elem()
	if ty.Implements(storeIface) {
		t.Error("*ResidueStore satisfies staterecord.Store, so it can be handed to builder.discoverOrphanedRecords directly. " +
			"It must not: the whole point is that a residue store has no List for a destroy path to walk.")
	}
}

// TestResiduePayloadIsNotAReadableRecord closes the last step of the chain a
// careless caller would have to complete to turn a residue key into a
// destroy proposal. Even having reached past ResidueStore to the raw store,
// listed the residue prefix by name, and fed the result to
// materializeRecord, the payload does not decode as a record.
func TestResiduePayloadIsNotAReadableRecord(t *testing.T) {
	ctx := context.Background()
	store := localHintStore(t)
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")
	residue := NewResidueStore(store, "my-estate")
	if _, err := residue.Put(ctx, addr, map[string]cty.Value{"filename": cty.StringVal("a.zip")}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	raw, _, exists, err := store.Get(ctx, ResidueKey("my-estate", addr))
	if err != nil || !exists {
		t.Fatalf("reading the residue key back: err=%v exists=%v", err, exists)
	}
	if _, _, _, err := decodeRecordPayload(raw); err == nil {
		t.Fatal("a residue payload decoded as a record payload. That is the third of three independent stops between a residue key and a destroy proposal, and it just stopped stopping anything.")
	}
}

// TestResidueStoreRefusesAnotherAddressesRecord pins the key/payload
// agreement check. Filling one instance's prior state from another's
// arguments would produce an EMPTY plan that is wrong, which no
// verdict-level check in this repository can see.
func TestResidueStoreRefusesAnotherAddressesRecord(t *testing.T) {
	ctx := context.Background()
	store := localHintStore(t)
	mine := locatedTestAddr(t, "aws_lambda_function", "check-links")
	theirs := locatedTestAddr(t, "aws_lambda_function", "salesforce-api")

	residue := NewResidueStore(store, "my-estate")
	if _, err := residue.Put(ctx, theirs, map[string]cty.Value{"filename": cty.StringVal("theirs.zip")}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	raw, _, _, err := store.Get(ctx, ResidueKey("my-estate", theirs))
	if err != nil {
		t.Fatalf("Get: %s", err)
	}
	// Copy their payload onto my key, which is what a hand-edit or a
	// careless rename looks like from here.
	if _, err := store.PutIfVersion(ctx, ResidueKey("my-estate", mine), raw, ""); err != nil {
		t.Fatalf("PutIfVersion: %s", err)
	}
	if _, _, _, err := residue.Get(ctx, mine); err == nil {
		t.Fatal("a residue record naming another address was accepted; it must be refused")
	}
}

// TestResidueStoreRefusesAnEmptyOrUnstorableWrite is acceptance (c) at the
// store boundary. The classifier already fails closed; this is the second
// lock, because the two are separated by a function call and a caller that
// stopped checking ok would otherwise write a record that claims an
// instance has no residue.
func TestResidueStoreRefusesAnEmptyOrUnstorableWrite(t *testing.T) {
	ctx := context.Background()
	residue := NewResidueStore(localHintStore(t), "my-estate")
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")

	for _, tc := range []struct {
		name  string
		attrs map[string]cty.Value
	}{
		{"empty", map[string]cty.Value{}},
		{"nil", nil},
		{"a null value", map[string]cty.Value{"filename": cty.NullVal(cty.String)}},
		{"an unknown value", map[string]cty.Value{"filename": cty.UnknownVal(cty.String)}},
		{"a marked value", map[string]cty.Value{"filename": cty.StringVal("x").Mark("sensitive")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := residue.Put(ctx, addr, tc.attrs, ""); err == nil {
				t.Fatal("the store accepted a write it cannot honestly make a record out of")
			}
		})
	}
}

// TestResidueRoundTripsThroughTheStore is the pair working end to end at
// the unit level: classify, store, read back, fill. It is not a substitute
// for the floci crossing - only a real cloud can say the plan is empty -
// but it is what makes a break in the middle name itself.
func TestResidueRoundTripsThroughTheStore(t *testing.T) {
	ctx := context.Background()
	schema := lambdaLikeSchema()
	applied := lambdaApplied()
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")

	attrs, ok := classifyResidue(applied, residueCandidates(schema, applied, strict.DefaultSecrets), lambdaIdentityAttrs(), sdkv2LikeRead)
	if !ok {
		t.Fatal("classifyResidue proved nothing")
	}
	residue := NewResidueStore(localHintStore(t), "my-estate")
	if _, err := residue.Put(ctx, addr, attrs, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	back, _, exists, err := residue.Get(ctx, addr)
	if err != nil || !exists {
		t.Fatalf("Get: err=%v exists=%v", err, exists)
	}

	// The cold replan: read with a bare prior, which is what the plan path
	// hands the provider, and fill from the record.
	stub, err := identityOnly(applied, lambdaIdentityAttrs())
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}
	cold, err := sdkv2LikeRead(stub)
	if err != nil {
		t.Fatalf("read: %s", err)
	}
	for _, name := range []string{"filename", "source_code_hash", "publish"} {
		if !cold.GetAttr(name).IsNull() {
			t.Fatalf("the cold read answered %s, so this fixture is not reproducing the defect", name)
		}
	}

	filled, n := fillResidue(cold, schema.Block, back, strict.DefaultSecrets)
	if n != 3 {
		t.Fatalf("filled %d attributes, want 3", n)
	}
	for name, want := range map[string]cty.Value{
		"filename":         cty.StringVal("check_links.py.zip"),
		"source_code_hash": cty.StringVal("82e750d3"),
		"publish":          cty.False,
	} {
		if got := filled.GetAttr(name); !got.RawEquals(want) {
			t.Errorf("after the round trip %s = %#v, want %#v", name, got, want)
		}
	}
}

// TestIdentityOnlyKeepsTheIdentityAndNothingElse pins read A's prior, which
// is the whole of what makes the classifier's answer mean anything.
//
// The identity stays real, so read A addresses the same object read B does.
// Everything else goes null, because the question read A asks is "what does
// this provider produce with nothing to go on", and the plan path asks that
// same question with that same prior on every run - [importAndRead] reads
// with the bare imported object.
//
// The first version of this test asserted the opposite: that everything
// outside the CANDIDATES stayed real. The floci crossing refuted it in one
// run. aws_lambda_function.source_code_hash came back populated from that
// richer prior, so it was classified provider-managed and not recorded,
// while the actual cold replan got null for it and proposed the update
// anyway. The classifier was answering a question the plan does not ask.
func TestIdentityOnlyKeepsTheIdentityAndNothingElse(t *testing.T) {
	applied := lambdaApplied()
	stub, err := identityOnly(applied, lambdaIdentityAttrs())
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}
	if !stub.GetAttr("id").RawEquals(applied.GetAttr("id")) {
		t.Fatal("identityOnly nulled the identity. Read A would then address no object at all, and its answer would say nothing about this one.")
	}
	for _, name := range []string{"arn", "function_name", "description", "source_code_hash", "filename", "publish"} {
		if !stub.GetAttr(name).IsNull() {
			t.Errorf("identityOnly left %s populated. Anything the provider can read out of the prior is something read A's answer stops being evidence about.", name)
		}
	}
	if !stub.Type().Equals(applied.Type()) {
		t.Error("identityOnly changed the object type, so the prior no longer conforms to the provider's schema")
	}

	// And an object with no identity at all produces no prior, rather than
	// one that addresses nothing. internal/command's stateless test
	// provider found this the hard way: its caricature objects carry no id,
	// and a read with a null-id prior panicked inside the provider before
	// it could answer anything.
	m := applied.AsValueMap()
	m["id"] = cty.NullVal(cty.String)
	if _, err := identityOnly(cty.ObjectVal(m), lambdaIdentityAttrs()); err == nil {
		t.Fatal("identityOnly built a prior whose every identity attribute is null. That prior addresses no object, and handing it to a provider is exactly the bogus input this design exists to never produce.")
	}
}

// TestNoSentinelValueExists is acceptance (d) of issue #275's brief, taken
// literally. The brief anticipated a sentinel and asked for a guard that
// one can never reach a plan or a record. The implementation has none, and
// the strongest available guard on "no sentinel leaks" is that no sentinel
// exists: every value this package hands a provider comes from the applied
// object or is a typed null of the applied object's own attribute type.
//
// This asserts that property where it is decidable - over the whole of the
// prior read A is built from - rather than by grepping for a word.
func TestNoSentinelValueExists(t *testing.T) {
	applied := lambdaApplied()
	candidates := residueCandidates(lambdaLikeSchema(), applied, strict.DefaultSecrets)
	stub, err := identityOnly(applied, lambdaIdentityAttrs())
	if err != nil {
		t.Fatalf("identityOnly: %s", err)
	}

	appliedAttrs := applied.AsValueMap()
	for name, v := range stub.AsValueMap() {
		switch {
		case v.IsNull():
			// A typed null of the attribute's own type. Ordinary.
		case v.RawEquals(appliedAttrs[name]):
			// Straight from the apply. Also ordinary.
		default:
			t.Errorf("the prior handed to the provider carries a value for %q that came from neither the applied object nor a typed null: %#v. "+
				"Any such value is a sentinel by another name, and a sentinel can be misread by a provider, can collide with a real answer, and can leak.", name, v)
		}
	}

	// And the values that can reach a RECORD are the applied values
	// themselves, never anything constructed.
	attrs, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), sdkv2LikeRead)
	if !ok {
		t.Fatal("classifyResidue proved nothing")
	}
	for name, v := range attrs {
		if !v.RawEquals(appliedAttrs[name]) {
			t.Errorf("the value recorded for %q is not the value the apply produced: %#v vs %#v", name, v, appliedAttrs[name])
		}
	}
}

// TestResidueNamespaceRootsAreDisjoint states the whole set in one place, so
// that adding another root has to come here and say which of the others it
// must stay clear of. The roots are compared at the segment level, which is
// the level both SSM parameter names and S3 key prefixes are hierarchical
// at.
//
// The list is also the set internal/configs' validateRecordStoreKeyPrefix
// refuses a key_prefix override rooted at, which is the other direction of
// the same disjointness: this test holds the defaults apart, and that check
// stops an operator moving one on top of another.
func TestResidueNamespaceRootsAreDisjoint(t *testing.T) {
	roots := []string{
		recordNamespaceRoot,
		hintNamespaceRoot,
		locatedNamespaceRoot,
		residueNamespaceRoot,
		provisionedNamespaceRoot,
		rootOutputNamespaceRoot,
		"tofu-receipts",
	}
	sort.Strings(roots)
	for i, a := range roots {
		for j, b := range roots {
			if i == j {
				continue
			}
			if a == b || strings.HasPrefix(a, b+"/") {
				t.Errorf("namespace root %q is not disjoint from %q", a, b)
			}
		}
	}
}

// TestFillResidueSeesThroughASensitivityMark is the regression test for the
// defect that made GitHub issue #365 slice 3's sensitive residue a no-op on
// the read side, found by an adversarial audit of the slice rather than by
// any check in this repository.
//
// The write side works on an unmarked copy of the applied object, so it
// records a sensitive attribute correctly. The read side is handed
// builder.materialize's object, which [importAndRead] has already marked from
// the schema - and [carriesNoInformation] answers false for a marked value on
// purpose, because it must not draw a conclusion from one. For a legacy-SDK
// provider, whose answer for an unset argument is the empty string rather
// than a null, that meant "the provider answered something", so the record
// was never filled: an attribute recorded on every apply and re-proposed on
// every plan, forever, for exactly the population the setting was added to
// cover.
//
// The fixture reproduces both halves of the shape deliberately. filename is
// Sensitive and its cold read is the legacy SDK's EMPTY STRING, not a null -
// a null unmarks itself inside cty.Value.IsNull, so a null-shaped fixture
// passes whether or not the defect is present, which is why the first version
// of TestResidueRoundTripsASensitiveArgumentWithItsMark missed it.
func TestFillResidueSeesThroughASensitivityMark(t *testing.T) {
	schema := secretLambdaSchema()

	// The cold read, in the shape hashicorp/aws actually produces and then
	// marked the way importAndRead marks it.
	cold := markSchemaSensitive(cty.ObjectVal(map[string]cty.Value{
		"id":               cty.StringVal("check-links"),
		"function_name":    cty.StringVal("check-links"),
		"filename":         cty.StringVal(""),
		"source_code_hash": cty.StringVal(""),
		"publish":          cty.NullVal(cty.Bool),
		"description":      cty.StringVal("link checker"),
		"arn":              cty.StringVal("arn:aws:lambda:eu-west-1:000000000000:function:check-links"),
	}), schema.Block)
	if !cold.GetAttr("filename").IsMarked() {
		t.Fatal("the fixture's sensitive attribute is not marked, so this test cannot see the defect it exists for")
	}

	record := map[string]cty.Value{"filename": cty.StringVal("check_links.py.zip")}
	filled, n := fillResidue(cold, schema.Block, record, strict.Store)
	if n != 1 {
		t.Fatalf("filled %d attributes, want 1.\nA sensitive attribute whose cold read is the legacy SDK's empty "+
			"string carries no information, marked or not - and refusing to fill it leaves the estate proposing "+
			"the update this mechanism exists to remove.", n)
	}
	got := filled.GetAttr("filename")
	if plain, _ := got.Unmark(); !plain.RawEquals(cty.StringVal("check_links.py.zip")) {
		t.Errorf("filename = %#v, want the recorded value", plain)
	}

	// The attributes nothing filled keep their marks, which is the other
	// half: this function must not launder a value on its way through.
	if !filled.GetAttr("source_code_hash").IsMarked() == cold.GetAttr("source_code_hash").IsMarked() {
		t.Error("an untouched attribute's marks changed on the way through fillResidue")
	}
}
