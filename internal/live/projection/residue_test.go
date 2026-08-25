// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

	got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), nil, sdkv2LikeRead, nil)
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

	got, ok := classifyResidue(obj, residueCandidates(schema, obj, strict.DefaultSecrets), lambdaIdentityAttrs(), nil, legacyRead, nil)
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

	got, ok := classifyResidue(applied, residueCandidates(schema, applied, strict.DefaultSecrets), lambdaIdentityAttrs(), nil, frameworkRead, nil)
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
			got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), nil, tc.read(), nil)
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
	got, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), nil, echo, nil)
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

// serviceLikeSchema is aws_ecs_service's shape reduced to the attribute
// GitHub issue #395 is about, taken from hashicorp/aws 6.59.0's own
// schema: task_definition is Optional, never Computed - the plugin
// protocol's own contract says configuration is the only thing that can
// ever set it.
func serviceLikeSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":              {Type: cty.String, Computed: true},
				"name":            {Type: cty.String, Required: true},
				"cluster":         {Type: cty.String, Required: true},
				"task_definition": {Type: cty.String, Optional: true},
			},
		},
	}
}

func serviceApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":              cty.StringVal("arn:aws:ecs:eu-west-1:000000000000:service/ex-fargate/ex-fargate"),
		"name":            cty.StringVal("ex-fargate"),
		"cluster":         cty.StringVal("arn:aws:ecs:eu-west-1:000000000000:cluster/ex-fargate"),
		"task_definition": cty.StringVal("arn:aws:ecs:eu-west-1:000000000000:task-definition/ex-fargate:1"),
	})
}

func ecsServiceIdentityAttrs() map[string]bool {
	return residueIdentityAttrs(serviceLikeSchema())
}

// taskDefFormatRead is the CONFIRMED hashicorp/aws 6.59.0 aws_ecs_service
// Read quirk, reduced to task_definition alone and verified directly
// against a live floci build with no tofu in the loop (see the PR
// description for the reproduce command): given a prior whose
// task_definition already looks like an ARN, echo the live wire value -
// floci's DescribeServices always answers with the full ARN, confirmed
// independently through the AWS CLI - in ARN form; given anything else
// (null, or a non-ARN string), answer with the short "family:revision"
// form instead, discarding whatever the prior actually held. Every other
// attribute is echoed from applied, standing in for "the rest of the
// object reads normally".
func taskDefFormatRead(applied cty.Value, liveWireARN, shortForm string) residueReader {
	return func(prior cty.Value) (cty.Value, error) {
		out := map[string]cty.Value{}
		for name, v := range applied.AsValueMap() {
			out[name] = v
		}
		td := prior.GetAttr("task_definition")
		if !td.IsNull() && strings.HasPrefix(td.AsString(), "arn:") {
			out["task_definition"] = cty.StringVal(liveWireARN)
		} else {
			out["task_definition"] = cty.StringVal(shortForm)
		}
		return cty.ObjectVal(out), nil
	}
}

// TestClassifyResidueRecordsAFormatOnlyDivergenceOnANonComputedAttribute is
// GitHub issue #395's classify-side fix. Read A's answer (the short form,
// from the identity-only prior [identityOnly] builds) is real and
// non-empty, so it fails the unwidened carriesNoInformation test and used
// to be rejected outright as unrecordable drift - masking #395 from
// [RecordResidueForInstance] at MIGRATE time as thoroughly as it masked it
// from the live read itself. [residueConfigSourced]'s widening, plus the
// read-B safety leg, records it instead.
func TestClassifyResidueRecordsAFormatOnlyDivergenceOnANonComputedAttribute(t *testing.T) {
	schema := serviceLikeSchema()
	applied := serviceApplied()
	wireARN := applied.GetAttr("task_definition").AsString()
	const shortForm = "ex-fargate:1"

	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)
	read := taskDefFormatRead(applied, wireARN, shortForm)

	got, ok := classifyResidue(applied, candidates, ecsServiceIdentityAttrs(), residueConfigSourced(schema), read, nil)
	if !ok {
		t.Fatal("classifyResidue proved nothing; task_definition's format-only divergence should have been recorded")
	}
	td, recorded := got["task_definition"]
	if !recorded {
		t.Fatal("task_definition was not classified as residue - #395's whole migrate-time fix depends on it being recorded here")
	}
	if !td.RawEquals(applied.GetAttr("task_definition")) {
		t.Errorf("task_definition classified as %#v, want the applied ARN %#v", td, applied.GetAttr("task_definition"))
	}
}

// TestClassifyResidueRefusesTheSameShapeWithoutConfigSourced is the
// control for the test above: the identical read pair, with configSourced
// nil (residueConfigSourced's answer for a schema this test never
// consults), reproduces the PRE-#395 behavior - task_definition is
// real-but-different and unrecorded. This is what proves the widening
// itself, and not merely the read function, is what changed the outcome.
func TestClassifyResidueRefusesTheSameShapeWithoutConfigSourced(t *testing.T) {
	schema := serviceLikeSchema()
	applied := serviceApplied()
	wireARN := applied.GetAttr("task_definition").AsString()
	read := taskDefFormatRead(applied, wireARN, "ex-fargate:1")
	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)

	got, _ := classifyResidue(applied, candidates, ecsServiceIdentityAttrs(), nil, read, nil)
	if _, bad := got["task_definition"]; bad {
		t.Error("task_definition was classified as residue with configSourced=nil; the widening must be " +
			"opt-in through the schema property, not the default")
	}
}

// TestClassifyResidueStillRejectsGenuineDriftOnANonComputedAttribute is the
// widening's own safety proof, and the reason it is sound rather than
// merely convenient. If the live object's task_definition has genuinely
// changed out of band (not a format artifact), read B - given the STALE
// applied value as its prior - still looks ARN-shaped, so the provider
// still echoes the TRUE current wire value in ARN form; it just is not
// applied's value any more, because the object actually drifted. That
// must NOT be classified as residue: recording it would make every future
// plan silently discard the real drift and keep reasserting the stale,
// no-longer-true task_definition forever - the exact hazard
// [classifyResidue]'s own "No sentinel" section already reasons about for
// the unwidened rule.
func TestClassifyResidueStillRejectsGenuineDriftOnANonComputedAttribute(t *testing.T) {
	schema := serviceLikeSchema()
	staleApplied := serviceApplied() // task_definition = .../ex-fargate:1
	const driftedARN = "arn:aws:ecs:eu-west-1:000000000000:task-definition/ex-fargate:2"

	read := func(prior cty.Value) (cty.Value, error) {
		out := map[string]cty.Value{}
		for name, v := range staleApplied.AsValueMap() {
			out[name] = v
		}
		td := prior.GetAttr("task_definition")
		if !td.IsNull() && strings.HasPrefix(td.AsString(), "arn:") {
			// The live object drifted to revision 2 between migrate/apply
			// and this read. The provider always answers with the TRUE
			// current wire value; it only chooses ARN form because the
			// prior looked like one.
			out["task_definition"] = cty.StringVal(driftedARN)
		} else {
			out["task_definition"] = cty.StringVal("ex-fargate:2")
		}
		return cty.ObjectVal(out), nil
	}

	candidates := residueCandidates(schema, staleApplied, strict.DefaultSecrets)
	got, _ := classifyResidue(staleApplied, candidates, ecsServiceIdentityAttrs(), residueConfigSourced(schema), read, nil)
	if _, bad := got["task_definition"]; bad {
		t.Error("task_definition was classified as residue despite genuine drift (read B, given the stale " +
			"applied value, answered a DIFFERENT value) - this would silently hide real drift from every " +
			"future plan, forever")
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
	attrs, ok := classifyResidue(unmarked, candidates, lambdaIdentityAttrs(), nil, sdkv2LikeRead, nil)
	if !ok {
		t.Fatal("classifyResidue proved nothing for a type whose sensitive argument the provider never returns")
	}
	if got, want := attrs["filename"], cty.StringVal("check_links.py.zip"); !got.RawEquals(want) {
		t.Fatalf("classified filename = %#v, want %#v", got, want)
	}

	store := newTestResidueStore(localHintStore(t), "my-estate")
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

	filled, n := fillResidue(cold, schema.Block, back, strict.Store, cty.NilVal)
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
	strictFilled, _ := fillResidue(cold, schema.Block, back, strict.Refuse, cty.NilVal)
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

	got, n := fillResidue(read, block, rec, strict.DefaultSecrets, cty.NilVal)
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
	}, strict.DefaultSecrets, cty.NilVal)
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
			_, n := fillResidue(read, s.Block, map[string]cty.Value{"filename": cty.StringVal("a.zip")}, tc.secrets, cty.NilVal)
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
	got, n := fillResidue(read, s.Block, map[string]cty.Value{"filename": cty.StringVal("a.zip")}, strict.DefaultSecrets, cty.NilVal)
	if n != 1 {
		t.Fatalf("filled %d attributes, want 1 - a Computed-only attribute whose read carries no information must be fillable from its record", n)
	}
	if got.GetAttr("filename").AsString() != "a.zip" {
		t.Fatal("filename was not filled from the record")
	}
}

// TestFillResidueDistinguishesAnUnconfirmedStubDefaultFromARealRead is GitHub
// issue #393, reduced to [fillResidue] alone. aws_db_instance.skip_final_snapshot
// is Optional, not Computed, and hashicorp/aws's own SDKv2 schema seeds it
// with an internal Default of `true` - the opposite of the type's zero value,
// which is what [carriesNoInformation]'s legacy-SDK convention treats as
// "nothing". [importAndRead]'s ImportResourceState stub answers `true`
// before any live read runs, and a legacy-SDK ReadResource that never
// sources the attribute from the remote at all (residueCandidates and
// classifyResidue's whole reason for existing) leaves that stub value
// completely alone - so the plan-time read comes back bit-for-bit unchanged
// from the stub importAndRead fed it, for every instance of the type,
// regardless of what was actually configured or what a correctly-recorded
// residue value says.
//
// "publish" stands in for skip_final_snapshot here: [lambdaLikeSchema]
// already declares it Optional and not Computed, exactly the shape a
// residue candidate needs, and reusing it keeps this test free of a
// hand-invented schema. Its own real-world SDK default happens to be the
// zero value, so what "read" and "stub" answer below is overridden per
// sub-test to model the non-zero-default case directly - the schema never
// dictates what a fake provider says.
func TestFillResidueDistinguishesAnUnconfirmedStubDefaultFromARealRead(t *testing.T) {
	block := lambdaLikeSchema().Block
	rec := map[string]cty.Value{"publish": cty.False}

	t.Run("stub-seeded and never confirmed by Read: the record wins", func(t *testing.T) {
		// importAndRead fed the provider "publish: true" as PriorState -
		// ImportResourceState's own stub, before any live read - and
		// ReadResource walked it straight through unchanged: the RDS shape
		// this issue is about.
		stub := cty.ObjectVal(map[string]cty.Value{
			"id":               cty.StringVal("x"),
			"function_name":    cty.StringVal("x"),
			"filename":         cty.NullVal(cty.String),
			"source_code_hash": cty.NullVal(cty.String),
			"publish":          cty.True,
			"description":      cty.NullVal(cty.String),
			"arn":              cty.NullVal(cty.String),
		})
		read := stub // ReadResource echoed it back bit-for-bit, unconfirmed.

		got, n := fillResidue(read, block, rec, strict.DefaultSecrets, stub)
		if n != 1 {
			t.Fatalf("filled %d attributes, want 1 (publish): a value the provider only ever echoed from the import stub must not outrank a correctly recorded residue value", n)
		}
		if got.GetAttr("publish").True() {
			t.Fatal("publish stayed true: the stub-seeded default outranked the record, exactly the phantom update issue #393 reports (true -> false forever)")
		}
	})

	t.Run("mutation check: a genuinely confirmed read must never be overwritten", func(t *testing.T) {
		// The provider was handed "publish: false" as PriorState this time,
		// and its Read independently answered "true" anyway - proof this
		// run's answer came from the provider's own computation and not
		// from echoing what it was given.
		stub := cty.ObjectVal(map[string]cty.Value{
			"id":               cty.StringVal("x"),
			"function_name":    cty.StringVal("x"),
			"filename":         cty.NullVal(cty.String),
			"source_code_hash": cty.NullVal(cty.String),
			"publish":          cty.False,
			"description":      cty.NullVal(cty.String),
			"arn":              cty.NullVal(cty.String),
		})
		read := cty.ObjectVal(map[string]cty.Value{
			"id":               cty.StringVal("x"),
			"function_name":    cty.StringVal("x"),
			"filename":         cty.NullVal(cty.String),
			"source_code_hash": cty.NullVal(cty.String),
			"publish":          cty.True,
			"description":      cty.NullVal(cty.String),
			"arn":              cty.NullVal(cty.String),
		})

		got, n := fillResidue(read, block, rec, strict.DefaultSecrets, stub)
		if n != 0 {
			t.Fatalf("filled %d attributes, want 0: a read that disagrees with its own stub is a real answer, and a residue record must never outrank one", n)
		}
		if !got.GetAttr("publish").True() {
			t.Fatal("a genuinely-read true was overwritten by a residue false - exactly the direction fillResidue's own doc comment says must never happen")
		}
	})

	t.Run("no stub available: falls back to the pre-#393 conservative behavior", func(t *testing.T) {
		read := cty.ObjectVal(map[string]cty.Value{
			"id":               cty.StringVal("x"),
			"function_name":    cty.StringVal("x"),
			"filename":         cty.NullVal(cty.String),
			"source_code_hash": cty.NullVal(cty.String),
			"publish":          cty.True,
			"description":      cty.NullVal(cty.String),
			"arn":              cty.NullVal(cty.String),
		})
		got, n := fillResidue(read, block, rec, strict.DefaultSecrets, cty.NilVal)
		if n != 0 || !got.GetAttr("publish").True() {
			t.Fatalf("filled %d attributes with no provenance signal at all; with cty.NilVal for importStub, a non-zero read must stay untouched exactly as it did before this fix", n)
		}
	})
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

	residue := newTestResidueStore(store, "my-estate")
	if _, err := residue.Put(ctx, theirs, map[string]cty.Value{"filename": cty.StringVal("theirs.zip")}, ""); err != nil {
		t.Fatalf("Put: %s", err)
	}
	prefix := RecordKeyPrefix("my-estate")
	raw, _, _, err := store.Get(ctx, RecordKey(prefix, theirs))
	if err != nil {
		t.Fatalf("Get: %s", err)
	}
	// Copy their payload onto my key, which is what a hand-edit or a
	// careless rename looks like from here.
	if _, err := store.PutIfVersion(ctx, RecordKey(prefix, mine), raw, ""); err != nil {
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
	residue := newTestResidueStore(localHintStore(t), "my-estate")
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

	attrs, ok := classifyResidue(applied, residueCandidates(schema, applied, strict.DefaultSecrets), lambdaIdentityAttrs(), nil, sdkv2LikeRead, nil)
	if !ok {
		t.Fatal("classifyResidue proved nothing")
	}
	residue := newTestResidueStore(localHintStore(t), "my-estate")
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

	filled, n := fillResidue(cold, schema.Block, back, strict.DefaultSecrets, cty.NilVal)
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
	attrs, ok := classifyResidue(applied, candidates, lambdaIdentityAttrs(), nil, sdkv2LikeRead, nil)
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
	// GitHub issue #364 collapsed the located, residue and provisioned
	// roots into the one record envelope (recordNamespaceRoot,
	// "tofu-records"): what used to be four disjoint namespaces (plus the
	// hint and root-output ones) is now one, still disjoint from the
	// per-estate namespaces that were never part of the merge - the hint
	// (issue #109), the root-output cache (issue #349) and the receipts
	// pattern.
	roots := []string{
		recordNamespaceRoot,
		hintNamespaceRoot,
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

// sgLikeSchema is aws_security_group's shape reduced to what the
// corpus-rds-complete-postgres crossing turns on, taken from hashicorp/aws
// 6.59.0's own schema rather than invented:
//
//	timeouts   a NestingSingle block, config-only, never read from the API
//	ingress    a NestingSet block, real cloud data, read from the API
//	egress     a NestingSet block, real cloud data, read from the API
//
// The two nesting modes sitting side by side is the point: the rule under
// test admits the first and must keep refusing the other two, and it must
// do that from the nesting mode rather than from either block's name.
func sgLikeSchema() providers.Schema {
	rule := configschema.Block{Attributes: map[string]*configschema.Attribute{
		"from_port":   {Type: cty.Number, Optional: true},
		"to_port":     {Type: cty.Number, Optional: true},
		"cidr_blocks": {Type: cty.List(cty.String), Optional: true},
	}}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":                     {Type: cty.String, Optional: true, Computed: true},
				"name":                   {Type: cty.String, Optional: true, Computed: true},
				"revoke_rules_on_delete": {Type: cty.Bool, Optional: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"timeouts": {
					Nesting: configschema.NestingSingle,
					Block: configschema.Block{Attributes: map[string]*configschema.Attribute{
						"create": {Type: cty.String, Optional: true},
						"delete": {Type: cty.String, Optional: true},
					}},
				},
				"ingress": {Nesting: configschema.NestingSet, Block: rule},
				"egress":  {Nesting: configschema.NestingSet, Block: rule},
			},
		},
	}
}

func sgTimeoutsType() cty.Type {
	return cty.Object(map[string]cty.Type{"create": cty.String, "delete": cty.String})
}

func sgRuleSetType() cty.Type {
	return cty.Set(cty.Object(map[string]cty.Type{
		"from_port":   cty.Number,
		"to_port":     cty.Number,
		"cidr_blocks": cty.List(cty.String),
	}))
}

func sgApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":                     cty.StringVal("sg-823b494443f0e3e7a"),
		"name":                   cty.StringVal("complete-postgresql-d9972d37dfe1552a1323196f98"),
		"revoke_rules_on_delete": cty.False,
		"timeouts": cty.ObjectVal(map[string]cty.Value{
			"create": cty.StringVal("10m"),
			"delete": cty.StringVal("15m"),
		}),
		"ingress": cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"from_port":   cty.NumberIntVal(5432),
			"to_port":     cty.NumberIntVal(5432),
			"cidr_blocks": cty.ListVal([]cty.Value{cty.StringVal("10.99.0.0/18")}),
		})}),
		"egress": cty.NullVal(sgRuleSetType()),
	})
}

// sgLikeRead is the behavior the AWS provider actually shows for a security
// group: name and ingress come back from DescribeSecurityGroups, while
// timeouts and revoke_rules_on_delete are never touched by Read at all, so
// whatever prior it was handed passes straight through - null included.
func sgLikeRead(prior cty.Value) (cty.Value, error) {
	out := map[string]cty.Value{}
	for name, v := range prior.AsValueMap() {
		out[name] = v
	}
	out["id"] = cty.StringVal("sg-823b494443f0e3e7a")
	out["name"] = cty.StringVal("complete-postgresql-d9972d37dfe1552a1323196f98")
	out["ingress"] = sgApplied().GetAttr("ingress")
	return cty.ObjectVal(out), nil
}

// TestResidueCarriesASingleNestedBlockByValue is the
// corpus-rds-complete-postgres finding in one assertion. Before this rule,
// residueCandidates walked schema.Block.Attributes only, so
// terraform-aws-modules' `timeouts { create = "10m" delete = "15m" }` was
// never a candidate, never recorded, and every stateless replan after a
// clean migrate proposed `+ timeouts {...}` on that security group forever -
// against a stock plan that renders the identical block unchanged.
//
// It asserts the recorded value BY VALUE rather than "the diff went away":
// an empty record and a record holding the wrong durations both make the
// candidate list non-empty, and only the exact object the apply sent is a
// pass.
//
// ingress is the negative control, and what it proves moved when
// residueEligibleBlock widened past NestingSingle (GitHub issue #365
// slice 2's own crossing needed NestingList and NestingSet too - see that
// function's doc comment): ingress is now a CANDIDATE, because nothing
// about its own schema (no sensitive or write-only argument, a nesting mode
// whose absence [carriesNoInformation] can tell from "empty") disqualifies
// it any more than timeouts's does. It must still never be RECORDED,
// because it is real cloud data the provider reads back - and asserting
// that split (candidate, yet excluded by classifyResidue) is a stronger
// claim than the old one ("never even a candidate"), not a weaker one: it
// is the proof that safety here comes from the two-read discriminator and
// not from which nesting modes reach it. egress stays out of the candidate
// list too, but for an unrelated, unchanged reason - this fixture's own
// egress is null, and a null applied block is filtered before nesting mode
// is ever asked, in either the old rule or this one.
//
// Both `strict { secrets = ... }` settings are run, and the assertion is
// that the answer does not move between them. The two populations residue
// now carries were built for different questions - the secrets setting
// decides whether a SENSITIVE flat attribute may be recorded, and this
// block rule decides whether a config-only nested block may be - and this
// schema holds nothing sensitive at all, so the setting has nothing to
// reach here. A future change that threads the setting into the block half
// fails this test rather than passing quietly.
func TestResidueCarriesASingleNestedBlockByValue(t *testing.T) {
	for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
		t.Run(string(secrets), func(t *testing.T) {
			schema := sgLikeSchema()
			applied := sgApplied()

			candidates := residueCandidates(schema, applied, secrets)
			want := []string{"ingress", "name", "revoke_rules_on_delete", "timeouts"}
			if !reflect.DeepEqual(candidates, want) {
				t.Fatalf("residueCandidates = %v, want %v - ingress (NestingSet, non-null applied value) is now a structural candidate; egress (null applied value) is not, for its own unrelated reason", candidates, want)
			}

			attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, sgLikeRead, nil)
			if !ok {
				t.Fatal("classifyResidue recorded nothing")
			}
			got, held := attrs["timeouts"]
			if !held {
				t.Fatalf("timeouts was not recorded; recorded %v", attrs)
			}
			wantVal := cty.ObjectVal(map[string]cty.Value{
				"create": cty.StringVal("10m"),
				"delete": cty.StringVal("15m"),
			})
			if !got.RawEquals(wantVal) {
				t.Fatalf("recorded timeouts = %#v, want %#v", got, wantVal)
			}
			if _, held := attrs["ingress"]; held {
				t.Fatal("ingress was recorded - a block the provider reads from the remote must never reach a record")
			}
			if _, held := attrs["name"]; held {
				t.Fatal("name was recorded - the filter lets it through and the classifier is what drops it, because the provider answers it")
			}

			// And the other half of the round trip: a cold read carrying no
			// timeouts at all is filled back to exactly the same object, which is
			// what makes the replan render "(1 unchanged block hidden)" the way
			// stock's does.
			cold := cty.ObjectVal(map[string]cty.Value{
				"id":                     cty.StringVal("sg-823b494443f0e3e7a"),
				"name":                   cty.StringVal("complete-postgresql-d9972d37dfe1552a1323196f98"),
				"revoke_rules_on_delete": cty.NullVal(cty.Bool),
				"timeouts":               cty.NullVal(sgTimeoutsType()),
				"ingress":                sgApplied().GetAttr("ingress"),
				"egress":                 cty.NullVal(sgRuleSetType()),
			})
			filled, n := fillResidue(cold, schema.Block, attrs, secrets, cty.NilVal)
			if n != 2 {
				t.Fatalf("fillResidue filled %d, want 2 (revoke_rules_on_delete and timeouts)", n)
			}
			if !filled.GetAttr("timeouts").RawEquals(wantVal) {
				t.Fatalf("filled timeouts = %#v, want %#v", filled.GetAttr("timeouts"), wantVal)
			}
		})
	}
}

// TestResidueRefusesASingleNestedBlockHoldingASecret holds the one
// exclusion a block cannot express with a flag of its own: the flat-
// attribute filter reads attr.Sensitive and attr.WriteOnly directly, and a
// block has to be walked for the same answer. Both directions are asserted,
// because a rule that excluded every block would also pass a test that only
// checked the secret case.
//
// The `strict { secrets = ... }` axis is the reconciliation of #365 slice 3
// with this rule, and it is asserted rather than described. Under
// [strict.Store] a sensitive flat ATTRIBUTE becomes recordable, and the
// obvious guess is that a block containing one becomes recordable with it.
// It does not, and the reason is not a stricter reading of the toggle: the
// sensitive attribute is admitted under Store only because
// [residueMarkRecoverable] can prove the mark comes back - one mark, on the
// whole attribute value, reproduced from the schema by
// [markSchemaSensitive] - and a sensitive argument INSIDE a block puts its
// mark at a path inside the block's value, the one shape that predicate
// names as unrecoverable. Store and Refuse therefore give the same verdict
// from two different supports, and the write-only case never depended on
// the setting at all.
func TestResidueRefusesASingleNestedBlockHoldingASecret(t *testing.T) {
	for _, tc := range []struct {
		name string
		attr *configschema.Attribute
	}{
		{"sensitive leaf", &configschema.Attribute{Type: cty.String, Optional: true, Sensitive: true}},
		{"write-only leaf", &configschema.Attribute{Type: cty.String, Optional: true, WriteOnly: true}},
	} {
		for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
			t.Run(tc.name+"/"+string(secrets), func(t *testing.T) {
				schema := sgLikeSchema()
				schema.Block.BlockTypes["timeouts"].Block.Attributes["create"] = tc.attr
				for _, name := range residueCandidates(schema, sgApplied(), secrets) {
					if name == "timeouts" {
						t.Fatalf("timeouts is a residue candidate under secrets=%q even though it holds a %s", secrets, tc.name)
					}
				}
				if residueEligibleBlock(schema.Block, "timeouts") {
					t.Fatalf("residueEligibleBlock admitted a block with a %s", tc.name)
				}
				// fillResidue must refuse it too, from the same predicate, so a
				// record written before the schema grew the secret stops being
				// filled the day it does.
				cold := cty.ObjectVal(map[string]cty.Value{
					"id":                     cty.StringVal("sg-1"),
					"name":                   cty.StringVal("n"),
					"revoke_rules_on_delete": cty.NullVal(cty.Bool),
					"timeouts":               cty.NullVal(sgTimeoutsType()),
					"ingress":                cty.NullVal(sgRuleSetType()),
					"egress":                 cty.NullVal(sgRuleSetType()),
				})
				_, n := fillResidue(cold, schema.Block, map[string]cty.Value{
					"timeouts": cty.ObjectVal(map[string]cty.Value{
						"create": cty.StringVal("10m"),
						"delete": cty.StringVal("15m"),
					}),
				}, secrets, cty.NilVal)
				if n != 0 {
					t.Fatalf("fillResidue filled a block holding a %s under secrets=%q", tc.name, secrets)
				}
			})
		}
	}
}

// TestResidueRefusesASingleNestedBlockCarryingAVariableMark is the
// reconciliation's own regression test, and it is about a mark the SCHEMA
// does not produce.
//
// #365 slice 3 replaced residueCandidates' flat `v.IsMarked()` with
// [residueMarkRecoverable], because a value that picked up sensitivity from
// a `sensitive = true` VARIABLE has no schema fact to restore it from: it
// would be stored unmarked, filled back unmarked, and left disagreeing with
// the planned value on sensitivity alone, forever. The block loop this
// branch added arrived with the older, flat spelling - and cty's
// Value.IsMarked is SHALLOW. A `timeouts { create = var.secret_duration }`
// carries its mark on the create argument inside the block value, not on
// the block value itself, so IsMarked answers false and the block would be
// recorded and filled with the mark silently dropped.
//
// Routing the block loop through residueMarkRecoverable with a nil
// attribute is what closes it, because that function's walk is
// UnmarkDeepWithPaths. Both settings are asserted: an unrecoverable mark is
// not a policy question, so `secrets = "store"` does not admit one either.
func TestResidueRefusesASingleNestedBlockCarryingAVariableMark(t *testing.T) {
	for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
		t.Run(string(secrets), func(t *testing.T) {
			schema := sgLikeSchema()

			// The block schema says nothing is sensitive - it is the VALUE
			// that carries the mark, which is what a sensitive variable
			// feeding one of the block's arguments produces.
			if !residueEligibleBlock(schema.Block, "timeouts") {
				t.Fatal("the fixture's block is refused on the schema alone, so this test cannot see the mark rule")
			}

			applied := sgApplied().AsValueMap()
			applied["timeouts"] = cty.ObjectVal(map[string]cty.Value{
				"create": cty.StringVal("10m").Mark(marks.Sensitive),
				"delete": cty.StringVal("15m"),
			})
			marked := cty.ObjectVal(applied)
			if marked.GetAttr("timeouts").IsMarked() {
				t.Fatal("the fixture's mark is on the block value itself, so a shallow IsMarked would already catch it and this test would prove nothing")
			}

			for _, name := range residueCandidates(schema, marked, secrets) {
				if name == "timeouts" {
					t.Fatalf("timeouts is a residue candidate under secrets=%q although an argument inside it carries a mark no schema read can put back", secrets)
				}
			}
		})
	}
}

// TestResidueAdmitsEveryCollectionNestingModeExceptGroup is the widened
// bound, asserted the same deliberate way [TestResidueRefusesACollectionNestedBlock]
// (this test's own former name and shape) pinned the narrower one: a
// boundary is worth a test whichever direction it was last moved, so that
// widening it further, or narrowing it back, is a deliberate act and not a
// side effect either way.
//
// NestingList, NestingSet and NestingMap are now admitted (GitHub issue
// #365 slice 2's corpus-sumaform-aws crossing: aws_instance's
// ephemeral_block_device is NestingSet and its root_block_device is
// NestingList), because none of the three shares NestingGroup's real
// ambiguity - see [residueEligibleBlock]'s own doc comment for exactly
// which question separates the four. NestingGroup is the one mode this
// still refuses, and the sole surviving member of what used to be a
// four-mode list.
func TestResidueAdmitsEveryCollectionNestingModeExceptGroup(t *testing.T) {
	for _, tc := range []struct {
		nesting configschema.NestingMode
		want    bool
	}{
		{configschema.NestingList, true},
		{configschema.NestingSet, true},
		{configschema.NestingMap, true},
		{configschema.NestingGroup, false},
	} {
		schema := sgLikeSchema()
		schema.Block.BlockTypes["timeouts"].Nesting = tc.nesting
		got := residueEligibleBlock(schema.Block, "timeouts")
		if got != tc.want {
			t.Fatalf("residueEligibleBlock(%v) = %v, want %v", tc.nesting, got, tc.want)
		}
	}
}

// hostLikeSchema is aws_instance's own shape for the two block types that
// motivated widening residueEligibleBlock past NestingSingle, reduced to
// what this test needs, taken from hashicorp/aws 6.59.0's own schema rather
// than invented:
//
//	root_block_device      a NestingList block (one element), config-only,
//	                        never read back on a bare import - see this
//	                        provider's own Import section
//	ephemeral_block_device a NestingSet block, config-only, never read back
//	                        on a bare import, for the identical reason
//
// Neither block's name appears in any production control-flow branch this
// change touches - residueEligibleBlock is asked with the SCHEMA's block
// name generically, whatever it is called, which is what this fixture is
// here to prove: renaming either block below would not change the outcome.
func hostLikeSchema() providers.Schema {
	rootBlockDevice := configschema.Block{Attributes: map[string]*configschema.Attribute{
		"device_name": {Type: cty.String, Optional: true, Computed: true},
		"volume_size": {Type: cty.Number, Optional: true, Computed: true},
		"volume_type": {Type: cty.String, Optional: true, Computed: true},
	}}
	ephemeralBlockDevice := configschema.Block{Attributes: map[string]*configschema.Attribute{
		"device_name":  {Type: cty.String, Optional: true},
		"virtual_name": {Type: cty.String, Optional: true},
	}}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":            {Type: cty.String, Optional: true, Computed: true},
				"ami":           {Type: cty.String, Required: true},
				"instance_type": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"root_block_device":      {Nesting: configschema.NestingList, Block: rootBlockDevice},
				"ephemeral_block_device": {Nesting: configschema.NestingSet, Block: ephemeralBlockDevice},
			},
		},
	}
}

func hostRootBlockDeviceType() cty.Type {
	return cty.List(cty.Object(map[string]cty.Type{
		"device_name": cty.String, "volume_size": cty.Number, "volume_type": cty.String,
	}))
}

func hostEphemeralBlockDeviceType() cty.Type {
	return cty.Set(cty.Object(map[string]cty.Type{
		"device_name": cty.String, "virtual_name": cty.String,
	}))
}

func hostApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal("i-0123456789abcdef0"),
		"ami":           cty.StringVal("ami-ubuntu2204"),
		"instance_type": cty.StringVal("m5.large"),
		"root_block_device": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"device_name": cty.StringVal("/dev/xvda"),
			"volume_size": cty.NumberIntVal(200),
			"volume_type": cty.StringVal("gp3"),
		})}),
		"ephemeral_block_device": cty.SetVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdb"), "virtual_name": cty.StringVal("ephemeral0")}),
			cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdc"), "virtual_name": cty.StringVal("ephemeral1")}),
		}),
	})
}

// hostBareImportRead is aws_instance's own documented behavior on a bare
// import (no full prior state): the provider reads ami/instance_type/id
// from the live object and never touches root_block_device or
// ephemeral_block_device at all, so whatever the prior held for them
// passes straight through unchanged - including a null identity-only
// prior, which is why both read null from a bare import today.
func hostBareImportRead(prior cty.Value) (cty.Value, error) {
	out := map[string]cty.Value{}
	for name, v := range prior.AsValueMap() {
		out[name] = v
	}
	out["id"] = cty.StringVal("i-0123456789abcdef0")
	out["ami"] = cty.StringVal("ami-ubuntu2204")
	out["instance_type"] = cty.StringVal("m5.large")
	return cty.ObjectVal(out), nil
}

// TestResidueCarriesListAndSetNestedBlocksByValue is corpus-sumaform-aws's
// own crossing in one assertion, over the exact two nesting modes
// [TestResidueAdmitsEveryCollectionNestingModeExceptGroup] proved
// residueEligibleBlock now accepts: aws_instance's root_block_device
// (NestingList) and ephemeral_block_device (NestingSet) are both
// documented, creation-only arguments the provider never repopulates on a
// bare import - the recovery a record-located identity gives an instance
// with no full prior state. Before this rule, a migrate-and-replan lost
// both, and the replan proposed replacing the instance
// ("+ ephemeral_block_device { # forces replacement }") against a stock
// plan that would show the same blocks unchanged if it had a full state to
// read them from.
//
// Asserted BY VALUE, per HANDOFF.md's safety rule: an empty record and one
// holding the wrong device names both leave the candidate list non-empty,
// and only the exact list/set the apply produced is a pass. The list's
// ORDER and the set's ELEMENT order are both asserted exactly as recorded -
// RawEquals for a cty.Set does not care about element order (verified
// independently: two cty.SetVals built from the same elements in opposite
// orders are RawEquals), so recording and filling a set is not a promise
// about any particular iteration order surviving, only about the recorded
// set being the same set.
func TestResidueCarriesListAndSetNestedBlocksByValue(t *testing.T) {
	for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
		t.Run(string(secrets), func(t *testing.T) {
			schema := hostLikeSchema()
			applied := hostApplied()

			// ami and instance_type are structural candidates too (ordinary,
			// non-identity, non-null flat attributes) and are exactly what
			// proves the split this test is about: candidate is a STRUCTURAL
			// question, and classifyResidue below is what tells "the
			// provider reads this back" (ami, instance_type) apart from
			// "the provider never touches this" (both blocks) - the same
			// split TestResidueCarriesASingleNestedBlockByValue's own
			// ingress proves for a NestingSet block already in this file.
			candidates := residueCandidates(schema, applied, secrets)
			want := []string{"ami", "ephemeral_block_device", "instance_type", "root_block_device"}
			if !reflect.DeepEqual(candidates, want) {
				t.Fatalf("residueCandidates = %v, want %v - both a NestingList and a NestingSet block should now be structural candidates alongside the ordinary flat ones", candidates, want)
			}

			attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, hostBareImportRead, nil)
			if !ok {
				t.Fatal("classifyResidue recorded nothing")
			}

			wantRoot := applied.GetAttr("root_block_device")
			gotRoot, held := attrs["root_block_device"]
			if !held {
				t.Fatalf("root_block_device was not recorded; recorded %v", attrs)
			}
			if !gotRoot.RawEquals(wantRoot) {
				t.Fatalf("recorded root_block_device = %#v, want %#v", gotRoot, wantRoot)
			}

			wantEphemeral := applied.GetAttr("ephemeral_block_device")
			gotEphemeral, held := attrs["ephemeral_block_device"]
			if !held {
				t.Fatalf("ephemeral_block_device was not recorded; recorded %v", attrs)
			}
			if !gotEphemeral.RawEquals(wantEphemeral) {
				t.Fatalf("recorded ephemeral_block_device = %#v, want %#v", gotEphemeral, wantEphemeral)
			}

			// The round trip: a bare-import cold read carries neither block
			// at all (both empty, exactly as hostBareImportRead's own doc
			// comment says a real bare import answers today), and fillResidue
			// must restore both to the exact applied value - not an
			// equivalent one, the same cty.Value RawEquals proves it.
			cold := cty.ObjectVal(map[string]cty.Value{
				"id":                     cty.StringVal("i-0123456789abcdef0"),
				"ami":                    cty.StringVal("ami-ubuntu2204"),
				"instance_type":          cty.StringVal("m5.large"),
				"root_block_device":      cty.ListValEmpty(hostRootBlockDeviceType().ElementType()),
				"ephemeral_block_device": cty.SetValEmpty(hostEphemeralBlockDeviceType().ElementType()),
			})
			filled, n := fillResidue(cold, schema.Block, attrs, secrets, cty.NilVal)
			if n != 2 {
				t.Fatalf("fillResidue filled %d, want 2 (root_block_device and ephemeral_block_device)", n)
			}
			if !filled.GetAttr("root_block_device").RawEquals(wantRoot) {
				t.Fatalf("filled root_block_device = %#v, want %#v", filled.GetAttr("root_block_device"), wantRoot)
			}
			if !filled.GetAttr("ephemeral_block_device").RawEquals(wantEphemeral) {
				t.Fatalf("filled ephemeral_block_device = %#v, want %#v", filled.GetAttr("ephemeral_block_device"), wantEphemeral)
			}
		})
	}
}

// TestResidueSetOrderDoesNotAffectClassification is the independent RawEquals
// check the previous test's doc comment cites, pinned as its own test so a
// future cty upgrade that changed set-equality semantics would fail here
// rather than inside a larger, harder-to-read failure.
func TestResidueSetOrderDoesNotAffectClassification(t *testing.T) {
	a := cty.SetVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdb"), "virtual_name": cty.StringVal("ephemeral0")}),
		cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdc"), "virtual_name": cty.StringVal("ephemeral1")}),
	})
	b := cty.SetVal([]cty.Value{
		cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdc"), "virtual_name": cty.StringVal("ephemeral1")}),
		cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdb"), "virtual_name": cty.StringVal("ephemeral0")}),
	})
	if !a.RawEquals(b) {
		t.Fatal("two cty.Sets built from the same elements in opposite orders are not RawEquals - classifyResidue's whole-value comparison would then depend on element order, which residueEligibleBlock's doc comment claims it does not")
	}
}

// hostEphemeralBlockDeviceWithNoDeviceSchema is hostLikeSchema's own
// ephemeral_block_device with the ONE attribute that fixture leaves out:
// no_device (Optional, bool) - real in hashicorp/aws's actual aws_instance
// schema, and the omission is what let
// TestResidueCarriesListAndSetNestedBlocksByValue's hostBareImportRead pass
// every prior straight through unchanged, hiding the SDKv2 quirk this
// file's own residueNormalizeSDKZeroLeaves exists for: a leaf never set in
// configuration plans as null, but the legacy SDK's flatten step has no way
// to write anything but false for it, on every read, forever.
func hostEphemeralBlockDeviceWithNoDeviceSchema() providers.Schema {
	ephemeralBlockDevice := configschema.Block{Attributes: map[string]*configschema.Attribute{
		"device_name":  {Type: cty.String, Optional: true},
		"virtual_name": {Type: cty.String, Optional: true},
		"no_device":    {Type: cty.Bool, Optional: true},
	}}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":            {Type: cty.String, Optional: true, Computed: true},
				"ami":           {Type: cty.String, Required: true},
				"instance_type": {Type: cty.String, Optional: true, Computed: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"ephemeral_block_device": {Nesting: configschema.NestingSet, Block: ephemeralBlockDevice},
			},
		},
	}
}

// hostEphemeralNoDeviceApplied is corpus-sumaform-aws's own two-device
// shape: no_device never set in configuration for either element, so
// OpenTofu's own planned/applied value holds a genuine null there, not
// hashicorp/aws's own false default.
func hostEphemeralNoDeviceApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":            cty.StringVal("i-0123456789abcdef0"),
		"ami":           cty.StringVal("ami-ubuntu2204"),
		"instance_type": cty.StringVal("m5.large"),
		"ephemeral_block_device": cty.SetVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdb"), "virtual_name": cty.StringVal("ephemeral0"), "no_device": cty.NullVal(cty.Bool)}),
			cty.ObjectVal(map[string]cty.Value{"device_name": cty.StringVal("xvdc"), "virtual_name": cty.StringVal("ephemeral1"), "no_device": cty.NullVal(cty.Bool)}),
		}),
	})
}

// hostEphemeralNoDeviceRead reproduces, exactly, what
// gauntlet:sumaform-clear's own TF_LOG=trace capture against a real
// hashicorp/aws 6.59.0 + floci apply showed for these two reads: an
// identity-only prior (read A) answers with an EMPTY set - Read() never
// populates ephemeral_block_device from the live object at all, there is
// nothing in DescribeInstances to populate it from - and the full-applied
// prior (read B) answers with the SAME two elements, EXCEPT every no_device
// that was null in the prior comes back false, deterministically: the
// legacy SDK's flatten step has no other value to give a bool leaf nothing
// ever set.
func hostEphemeralNoDeviceRead(prior cty.Value) (cty.Value, error) {
	out := map[string]cty.Value{}
	for name, v := range prior.AsValueMap() {
		out[name] = v
	}
	out["id"] = cty.StringVal("i-0123456789abcdef0")
	out["ami"] = cty.StringVal("ami-ubuntu2204")
	out["instance_type"] = cty.StringVal("m5.large")

	ebd := prior.GetAttr("ephemeral_block_device")
	if ebd.IsNull() || ebd.LengthInt() == 0 {
		out["ephemeral_block_device"] = ebd
		return cty.ObjectVal(out), nil
	}
	var elems []cty.Value
	for it := ebd.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		m := ev.AsValueMap()
		if m["no_device"].IsNull() {
			m["no_device"] = cty.False
		}
		elems = append(elems, cty.ObjectVal(m))
	}
	out["ephemeral_block_device"] = cty.SetVal(elems)
	return cty.ObjectVal(out), nil
}

// TestResidueRecordsANestedBlockAfterAnSDKZeroValueNormalization is
// gauntlet:sumaform-clear's own greenfield finding: PART GREENFIELD's own
// apply wrote this instance's record, and the VERY NEXT plan proposed
// replacing it - "+ ephemeral_block_device { # forces replacement }" -
// because write-back's own residue classification never recorded the block
// at all. hostBareImportRead's pure passthrough (used by every OTHER test
// in this file) cannot reproduce that: it never disagrees with applied in
// the first place, so read B's RawEquals check always trivially passed.
// hostEphemeralNoDeviceRead's read B does not pass through - it normalizes
// no_device the way the real provider does - which is exactly what made
// read B disagree with the raw applied value and made classifyResidue
// refuse this candidate before residueNormalizeSDKZeroLeaves existed.
//
// Asserted BY VALUE: the recorded value must be the ORIGINAL applied value
// (no_device still null, what configuration actually said), not read B's
// own normalized echo - residue records what to put back on the next
// plan's read, and what to put back is what was configured, not a
// provider-internal implementation detail of how it stores "unset".
func TestResidueRecordsANestedBlockAfterAnSDKZeroValueNormalization(t *testing.T) {
	schema := hostEphemeralBlockDeviceWithNoDeviceSchema()
	applied := hostEphemeralNoDeviceApplied()

	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)
	want := []string{"ami", "ephemeral_block_device", "instance_type"}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("residueCandidates = %v, want %v", candidates, want)
	}

	attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, hostEphemeralNoDeviceRead, nil)
	if !ok {
		t.Fatal("classifyResidue recorded nothing - a real AWS SDKv2 provider normalizing ephemeral_block_device.no_device from null to false on every read must not silently make this whole block unrecordable")
	}
	got, held := attrs["ephemeral_block_device"]
	if !held {
		t.Fatalf("ephemeral_block_device was not recorded; recorded %v", attrs)
	}
	wantVal := applied.GetAttr("ephemeral_block_device")
	if !got.RawEquals(wantVal) {
		t.Fatalf("recorded ephemeral_block_device = %#v, want the exact applied value %#v - residue must record what configuration said (no_device left null), not the provider's own normalized echo", got, wantVal)
	}
}

// TestResidueStillRejectsGenuineDriftInsideANestedBlockAfterZeroLeafTolerance
// is the previous test's control, proving residueNormalizeSDKZeroLeaves's
// tolerance is narrow: a read B that disagrees with applied for a reason
// OTHER than the null-to-zero-value substitution - here, no_device
// genuinely reads back true where configuration never set it at all, an
// answer no normalization of applied's own null could ever produce - must
// still be rejected as real drift, exactly as before this file's fix.
func TestResidueStillRejectsGenuineDriftInsideANestedBlockAfterZeroLeafTolerance(t *testing.T) {
	schema := hostEphemeralBlockDeviceWithNoDeviceSchema()
	applied := hostEphemeralNoDeviceApplied()

	driftingRead := func(prior cty.Value) (cty.Value, error) {
		v, err := hostEphemeralNoDeviceRead(prior)
		if err != nil {
			return v, err
		}
		ebd := v.GetAttr("ephemeral_block_device")
		if ebd.IsNull() || ebd.LengthInt() == 0 {
			return v, nil
		}
		var elems []cty.Value
		for it := ebd.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			m := ev.AsValueMap()
			// Genuine drift: true, not the null-to-false substitution this
			// candidate's own applied value could ever explain.
			m["no_device"] = cty.True
			elems = append(elems, cty.ObjectVal(m))
		}
		out := v.AsValueMap()
		out["ephemeral_block_device"] = cty.SetVal(elems)
		return cty.ObjectVal(out), nil
	}

	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)
	attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, driftingRead, nil)
	if ok {
		if _, bad := attrs["ephemeral_block_device"]; bad {
			t.Fatal("ephemeral_block_device was recorded despite read B genuinely disagreeing with applied (no_device=true, not the null-to-false SDK substitution) - residueNormalizeSDKZeroLeaves's tolerance must not swallow real drift")
		}
	}
}

// TestClassifyResidueLeavesAnEmptyCollectionBlockUnrecorded is the
// corpus-mastino-dns regression: NestingList/NestingSet/NestingMap's own
// widening (residueEligibleBlock, above) made every collection-nested block
// on a schema a structural candidate, including one an instance never
// configures at all. aws_route53_record's six routing-policy blocks
// (alias, cidr_routing_policy, failover_routing_policy,
// geolocation_routing_policy, geoproximity_routing_policy,
// latency_routing_policy, weighted_routing_policy - all NestingList) are
// exactly this on a plain MX or CNAME record: applied holds an empty list
// for every one of them because none is declared, hashicorp/aws's read
// answers null from the identity-only stub (an SDKv2 resource that never
// touches an untouched list leaves it at whatever the prior held, and the
// stub's prior is null there), and the full-prior read echoes the prior's
// own empty list straight back - which is precisely the two-read pattern
// classifyResidue records as residue. The corpus-mastino-dns crossing
// caught it live: migrate went from 14 residue records (the ones that
// actually mean something - wp-prod-staging's ten allow_overwrite = true
// plus DELTA 5's four apex NS blocks) to 59 (every record in the estate),
// each one recording six empty lists that a bare read already reproduces.
//
// Reusing hostLikeSchema's own root_block_device (NestingList) fixture
// rather than declaring a sixth aws_route53_record-shaped one: the rule
// under test does not know or care what the block is called, only that its
// applied value is empty, which is the same property
// TestResidueAdmitsEveryCollectionNestingModeExceptGroup already isolates
// for admission and this test isolates for classification.
func TestClassifyResidueLeavesAnEmptyCollectionBlockUnrecorded(t *testing.T) {
	schema := hostLikeSchema()
	applied := cty.ObjectVal(map[string]cty.Value{
		"id":                     cty.StringVal("i-0123456789abcdef0"),
		"ami":                    cty.StringVal("ami-ubuntu2204"),
		"instance_type":          cty.StringVal("m5.large"),
		"root_block_device":      cty.ListValEmpty(hostRootBlockDeviceType().ElementType()),
		"ephemeral_block_device": cty.SetValEmpty(hostEphemeralBlockDeviceType().ElementType()),
	})

	candidates := residueCandidates(schema, applied, strict.DefaultSecrets)
	want := []string{"ami", "ephemeral_block_device", "instance_type", "root_block_device"}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("residueCandidates = %v, want %v - an empty collection block is still a STRUCTURAL candidate (eligibility is about the block's shape, not this instance's value); classifyResidue is what must exclude it", candidates, want)
	}

	// hostBareImportRead is the SDKv2-untouched-prior shape both reads take
	// here: read A (the identity-only stub) passes its own null through
	// unchanged, and read B (the full applied prior) passes applied's own
	// empty collections through unchanged - the exact pattern that made
	// this recordable before this test's fix.
	attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, hostBareImportRead, nil)
	if ok {
		if _, bad := attrs["root_block_device"]; bad {
			t.Error("root_block_device (empty, never configured) was recorded as residue. Nothing was ever configured here - filling it back reproduces exactly what a bare read already gives - and recording it anyway is the corpus-mastino-dns regression: 14 real residue records became 59, six empty routing-policy blocks per record, none of them carrying any information a plain read did not already have.")
		}
		if _, bad := attrs["ephemeral_block_device"]; bad {
			t.Error("ephemeral_block_device (empty, never configured) was recorded as residue for the same reason root_block_device should not have been")
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
	filled, n := fillResidue(cold, schema.Block, record, strict.Store, cty.NilVal)
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

// TestResidueLandsUnderTheMergedNamespaceOnRealDisk is GitHub issue #390's
// regression test: it is the one this package was missing, and its absence
// is why #364 unit A1's on-disk namespace collapse could break every
// crossing script that reads a residue record's PATH without any test in
// this package - the one the gate runs on every unit - going red.
//
// #390 was bisected, by the numbers, to "corpus-mastino-dns's migrate
// writes zero residue records" after A1 landed. Read directly against the
// store rather than trusted from that bisection (HANDOFF.md: "read the API
// directly, with no tofu in the loop"), the migrate wrote all fourteen
// records, correctly. What had gone stale was live/e2e/corpus-mastino-dns/
// run.sh's own path assertion, still pointed at the pre-A1 layout
// (.tofu-records/tofu-residue/<estate>/<type>/<key>) that issue #364 unit
// A1 replaced with one merged namespace
// (tofu-records/<estate>/<type>/<key>, see [recordNamespaceRoot] and
// [RecordKeyPrefix]) - a defect in the ORACLE, not in the write path,
// exactly the shape HANDOFF.md's "a fixed wall makes stale scripts fail"
// describes. Nothing here names aws_route53_record: this exercises the
// same classifier and the same lambda-shaped fixture every other test in
// this file uses, because the property under test - where an instance's
// residue lands on a REAL local store - has nothing to do with which type
// carries it.
//
// This uses a real [staterecord.LocalStore], not [localHintStore]'s
// in-memory fake or a bare map: the whole point is to catch a regression in
// the physical key layout, which only shows up once bytes actually go to a
// filesystem through [RecordKey].
//
// The mutation this guards against: temporarily reverting
// [recordNamespaceRoot] from "tofu-records" back to the pre-A1
// "tofu-residue" literal (verified by hand while writing this test) moves
// the file this test finds and the RawPath assertion below fails
// immediately - loudly, in `go test`, rather than three stages deep into a
// live gauntlet run against a real emulator.
func TestResidueLandsUnderTheMergedNamespaceOnRealDisk(t *testing.T) {
	ctx := context.Background()
	const estate = "ondisk-residue-estate"
	dir := t.TempDir()

	backing, err := staterecord.NewLocalStore(dir)
	if err != nil {
		t.Fatalf("NewLocalStore: %s", err)
	}
	store := NewRecordEnvelopeStore(backing, RecordKeyPrefix(estate))

	schema := lambdaLikeSchema()
	applied := lambdaApplied()
	addr := locatedTestAddr(t, "aws_lambda_function", "check-links")

	recorded, err := RecordResidueForInstance(ctx, store, addr, addrs.AbsProviderConfig{}, schema, applied, strict.DefaultSecrets, sdkv2LikeRead, cty.NilVal)
	if err != nil {
		t.Fatalf("RecordResidueForInstance: %s", err)
	}
	if !recorded {
		t.Fatal("recorded=false: this fixture reproduces #275's classic shape (filename/source_code_hash/publish never echoed) and must classify as residue")
	}

	// COUNT: one instance's residue is one envelope, one file - not one file
	// per attribute, and not zero because the classifier found nothing.
	var files []string
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && !strings.HasSuffix(path, ".lock") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking the store directory: %s", err)
	}
	if len(files) != 1 {
		t.Fatalf("found %d file(s) under the store directory, want exactly 1: %v", len(files), files)
	}

	// PATH: pins the merged single-namespace layout against a HARDCODED
	// literal, deliberately not built through [RecordKeyPrefix] or
	// [RecordKey] - going through the same helpers the production code uses
	// would make this half of the test pass even if [recordNamespaceRoot]
	// regressed to a pre-A1 value, since both sides would move together.
	// This is the literal every crossing script that reads a residue
	// record's path is written against, spelled out independently so a
	// change to the constant is what this test is FOR catching.
	wantRel := "tofu-records/" + estate + "/aws_lambda_function/" + base64.RawURLEncoding.EncodeToString([]byte(addr.String()))
	wantPath := filepath.Join(dir, filepath.FromSlash(wantRel))
	if files[0] != wantPath {
		t.Fatalf("residue record written to %s, want %s (the merged tofu-records/<estate>/<type>/<key> layout) - the on-disk namespace moved", files[0], wantPath)
	}

	// VALUE: one record's attribute content, not just its existence. A
	// record written with the wrong value produces an empty plan that is
	// wrong, which no count-only check can see.
	raw, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading %s: %s", files[0], err)
	}
	var env struct {
		Kind    string `json:"kind"`
		Residue struct {
			Attributes map[string]struct {
				AttrType  json.RawMessage `json:"attrType"`
				AttrValue json.RawMessage `json:"attrValue"`
			} `json:"attributes"`
		} `json:"residue"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decoding the raw record: %s\nraw: %s", err, raw)
	}
	if env.Kind != recordKindIdentity {
		t.Errorf("kind = %q, want %q", env.Kind, recordKindIdentity)
	}
	if len(env.Residue.Attributes) != 3 {
		t.Fatalf("recorded %d residue attribute(s), want 3 (filename, source_code_hash, publish): %v", len(env.Residue.Attributes), env.Residue.Attributes)
	}
	filename, ok := env.Residue.Attributes["filename"]
	if !ok {
		t.Fatalf("no filename attribute recorded; got %v", env.Residue.Attributes)
	}
	if string(filename.AttrValue) != `"check_links.py.zip"` {
		t.Errorf("filename attrValue = %s, want %q", filename.AttrValue, `"check_links.py.zip"`)
	}

	// Re-reading through the store's own API must agree with what is on
	// disk - the two are supposed to be the same fact seen two ways.
	attrs, _, _, found, err := store.GetResidue(ctx, addr)
	if err != nil || !found {
		t.Fatalf("GetResidue: found=%v err=%v", found, err)
	}
	if got := attrs["filename"]; !got.RawEquals(cty.StringVal("check_links.py.zip")) {
		t.Errorf("GetResidue filename = %#v, want %#v", got, cty.StringVal("check_links.py.zip"))
	}
}

// asgLikeSchema is aws_autoscaling_group's own shape for
// initial_lifecycle_hook, GitHub issue #385's block: NestingSet, seven
// attributes, one of them ("default_result") Optional AND Computed, the
// rest plain Required or Optional, none Sensitive or WriteOnly - confirmed
// against hashicorp/aws 6.59.0's own wire schema (`terraform providers
// schema -json` against the pinned provider binary, offline, no estate or
// emulator involved) rather than assumed from the issue's paraphrase.
//
// And against the provider's own Go source at the same tag
// (internal/service/autoscaling/group.go): resourceGroupCreate sends the
// hook set to a separate PutLifecycleHook call per hook, and
// resourceGroupFlatten - the WHOLE of this resource's Read - never once
// calls d.Set("initial_lifecycle_hook", ...). DescribeAutoScalingGroups
// does not return lifecycle hooks at all; they are a distinct object this
// resource's Read never queries (DescribeLifecycleHooks). That is exactly
// the shape [residueEligibleBlock]'s own doc comment already describes for
// `timeouts`, `root_block_device` and `ephemeral_block_device`: a block
// declared in configuration that the provider's Read leaves completely
// untouched, so an untouched ResourceData field carries through whatever
// its prior held.
//
// Named after the real type deliberately, unlike hostLikeSchema's blocks
// two tests above which the derivation-guard discipline keeps type-name-free
// in PRODUCTION control flow: this is a test fixture pinning one concrete
// crossing's regression, and residueEligibleBlock itself still takes no
// name literal from this file - the two populations stay derived from the
// schema's shape (Nesting mode, Sensitive, WriteOnly), never from "is this
// aws_autoscaling_group".
func asgLikeSchema() providers.Schema {
	hook := configschema.Block{Attributes: map[string]*configschema.Attribute{
		"default_result":          {Type: cty.String, Optional: true, Computed: true},
		"heartbeat_timeout":       {Type: cty.Number, Optional: true},
		"lifecycle_transition":    {Type: cty.String, Required: true},
		"name":                    {Type: cty.String, Required: true},
		"notification_metadata":   {Type: cty.String, Optional: true},
		"notification_target_arn": {Type: cty.String, Optional: true},
		"role_arn":                {Type: cty.String, Optional: true},
	}}
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"id":       {Type: cty.String, Optional: true, Computed: true},
				"arn":      {Type: cty.String, Computed: true},
				"name":     {Type: cty.String, Optional: true, Computed: true},
				"max_size": {Type: cty.Number, Required: true},
				"min_size": {Type: cty.Number, Required: true},
			},
			BlockTypes: map[string]*configschema.NestedBlock{
				"initial_lifecycle_hook": {Nesting: configschema.NestingSet, Block: hook},
			},
		},
	}
}

func asgHookType() cty.Type {
	return cty.Object(map[string]cty.Type{
		"default_result":          cty.String,
		"heartbeat_timeout":       cty.Number,
		"lifecycle_transition":    cty.String,
		"name":                    cty.String,
		"notification_metadata":   cty.String,
		"notification_target_arn": cty.String,
		"role_arn":                cty.String,
	})
}

const asgARN = "arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:d15f0293-0d4e-4b8a-8f7a-example:autoScalingGroupName/my-asg"

// asgApplied is the object a real apply produces: two lifecycle hooks, the
// exact shape #385's own repro quotes (default_result/heartbeat_timeout/
// lifecycle_transition/name/notification_metadata all set on the first,
// only lifecycle_transition and name set on the second - default_result
// left at its Computed zero value, "", the way an un-configured
// Optional+Computed argument this provider never actually populates a
// default for reads back after a real apply).
func asgApplied() cty.Value {
	return cty.ObjectVal(map[string]cty.Value{
		"id":       cty.StringVal("my-asg"),
		"arn":      cty.StringVal(asgARN),
		"name":     cty.StringVal("my-asg"),
		"max_size": cty.NumberIntVal(5),
		"min_size": cty.NumberIntVal(1),
		"initial_lifecycle_hook": cty.SetVal([]cty.Value{
			cty.ObjectVal(map[string]cty.Value{
				"default_result":          cty.StringVal("CONTINUE"),
				"heartbeat_timeout":       cty.NumberIntVal(180),
				"lifecycle_transition":    cty.StringVal("autoscaling:EC2_INSTANCE_TERMINATING"),
				"name":                    cty.StringVal("ExampleTerminationLifeCycleHook"),
				"notification_metadata":   cty.StringVal(`{"goodbye":"world"}`),
				"notification_target_arn": cty.StringVal(""),
				"role_arn":                cty.StringVal(""),
			}),
			cty.ObjectVal(map[string]cty.Value{
				"default_result":          cty.StringVal(""),
				"heartbeat_timeout":       cty.NumberIntVal(0),
				"lifecycle_transition":    cty.StringVal("autoscaling:EC2_INSTANCE_LAUNCHING"),
				"name":                    cty.StringVal("ExampleLaunchLifeCycleHook"),
				"notification_metadata":   cty.StringVal(""),
				"notification_target_arn": cty.StringVal(""),
				"role_arn":                cty.StringVal(""),
			}),
		}),
	})
}

// asgFlattenRead is hashicorp/aws's own resourceGroupFlatten, reduced to
// this fixture's five flat attributes plus the one block under test, and
// with the legacy SDK's own null-collection quirk modeled explicitly:
// TypeSet has no flatmap encoding for "absent", so a null collection
// surviving the shim's round trip comes back at length zero rather than as
// null - [carriesNoInformation]'s own doc comment names the identical
// quirk for aws_lambda_function.source_code_hash. arn, name, max_size and
// min_size are the attributes DescribeAutoScalingGroups genuinely answers,
// so this function overwrites them unconditionally from what it takes to
// be the live object, exactly as resourceGroupFlatten's d.Set calls do;
// initial_lifecycle_hook is not one of the names resourceGroupFlatten's
// own d.Set calls list, so it passes through whatever the prior held.
func asgFlattenRead(prior cty.Value) (cty.Value, error) {
	out := map[string]cty.Value{}
	for name, v := range prior.AsValueMap() {
		out[name] = v
	}
	out["arn"] = cty.StringVal(asgARN)
	out["name"] = cty.StringVal("my-asg")
	out["max_size"] = cty.NumberIntVal(5)
	out["min_size"] = cty.NumberIntVal(1)
	if hook := out["initial_lifecycle_hook"]; hook.IsNull() {
		out["initial_lifecycle_hook"] = cty.SetValEmpty(asgHookType())
	}
	return cty.ObjectVal(out), nil
}

// TestResidueCarriesTheAutoscalingLifecycleHookSet is GitHub issue #385's
// own crossing, reproduced offline against the real schema and the real
// Read behavior rather than against a live estate: aws_autoscaling_group's
// initial_lifecycle_hook is a NestingSet block the provider's own Read
// never sources from the remote at all (confirmed above, at the pinned
// provider tag, with no tofu and no emulator in the loop), so a stateless
// prior has nothing to fill it from and a plan built from that prior
// proposes "+ initial_lifecycle_hook { # forces replacement }" forever.
//
// The issue's own text names GitHub issue #275's residueEligibleBlock as
// "exactly the mechanism for this shape" and then quotes its doc comment
// EXCLUDING every collection-nested block ("Nested object ATTRIBUTES ...
// are out of scope, and so is every block whose nesting mode is a list, a
// set, a map or a group"). That quote predates commit 6452c3baf6 (GitHub
// issue #365 slice 2, "residue now covers list- and set-nested blocks"),
// which is already on this branch's ancestry (main, merged before this
// unit started) and which residueEligibleBlock's CURRENT doc comment
// reflects: NestingSingle, NestingList, NestingSet and NestingMap are all
// admitted today, NestingGroup is the sole holdout, and the change note
// says corpus-sumaform-aws's ephemeral_block_device (NestingSet, exactly
// initial_lifecycle_hook's own nesting mode) is what forced the widening.
// So the premise the issue was filed under - "residueEligibleBlock excludes
// this shape" - is no longer true of the code on this branch, and this
// test is the value-asserted proof rather than a re-reading of the doc
// comment: it fails the moment residueEligibleBlock (or classifyResidue,
// or fillResidue) regresses on THIS EXACT real-world shape, independent of
// whatever the doc comment currently claims.
//
// Asserted by value throughout, per HANDOFF.md's safety rule: an empty
// record and a record holding the wrong hooks would both leave the
// candidate list non-empty and a naive "was something recorded" check
// green, so every assertion below compares against asgApplied's own
// cty.Value with RawEquals, both on the way in (classifyResidue) and on the
// way back out (fillResidue) - and the negative claim (arn/name/max_size/
// min_size must NOT be recorded, because the provider genuinely answers
// them) is checked with the same rigor as the positive one, since a fix
// that recorded EVERY attribute unconditionally would pass a check that
// only looked for the block.
//
// Mutation-checked by hand: commenting configschema.NestingSet out of
// residueEligibleBlock's admitted-modes switch makes this test fail
// immediately with "initial_lifecycle_hook was not a residue candidate",
// confirming the assertion is load-bearing and not vacuously true; restored
// afterward, and `git diff` over residue.go for this commit is empty.
func TestResidueCarriesTheAutoscalingLifecycleHookSet(t *testing.T) {
	for _, secrets := range []strict.Secrets{strict.Store, strict.Refuse} {
		t.Run(string(secrets), func(t *testing.T) {
			schema := asgLikeSchema()
			applied := asgApplied()

			candidates := residueCandidates(schema, applied, secrets)
			want := []string{"arn", "initial_lifecycle_hook", "max_size", "min_size", "name"}
			if !reflect.DeepEqual(candidates, want) {
				t.Fatalf("residueCandidates = %v, want %v - initial_lifecycle_hook (NestingSet) should be a structural candidate alongside the ordinary flat ones", candidates, want)
			}

			attrs, ok := classifyResidue(applied, candidates, residueIdentityAttrs(schema), nil, asgFlattenRead, nil)
			if !ok {
				t.Fatal("classifyResidue recorded nothing; initial_lifecycle_hook was not a residue candidate")
			}

			wantHook := applied.GetAttr("initial_lifecycle_hook")
			gotHook, held := attrs["initial_lifecycle_hook"]
			if !held {
				t.Fatalf("initial_lifecycle_hook was not recorded as residue; recorded %v", attrs)
			}
			if !gotHook.RawEquals(wantHook) {
				t.Fatalf("recorded initial_lifecycle_hook = %#v, want %#v", gotHook, wantHook)
			}

			// The negative claim: every attribute the provider's Read
			// genuinely answers (arn, name, max_size, min_size - all
			// overwritten unconditionally by asgFlattenRead, exactly as
			// resourceGroupFlatten's own d.Set calls do) must NOT be
			// recorded, even though residueCandidates lists all four as
			// structural candidates.
			for _, managed := range []string{"arn", "name", "max_size", "min_size"} {
				if v, bad := attrs[managed]; bad {
					t.Errorf("%s was recorded as residue (%#v), but the provider's own Read answers it - recording it would mask real drift", managed, v)
				}
			}
			if len(attrs) != 1 {
				t.Fatalf("classifyResidue recorded %d attribute(s) %v, want exactly 1 (initial_lifecycle_hook)", len(attrs), attrs)
			}

			// The round trip: a cold, state-file-free prior carries no
			// hooks at all (SetValEmpty - the identical "provider answered
			// nothing" shape read A itself produces above), and
			// fillResidue must restore the exact two hooks the apply
			// produced, not an equivalent pair - RawEquals over the whole
			// set, order-independent per TestResidueSetOrderDoesNotAffectClassification.
			cold := cty.ObjectVal(map[string]cty.Value{
				"id":                     cty.StringVal("my-asg"),
				"arn":                    cty.StringVal(asgARN),
				"name":                   cty.StringVal("my-asg"),
				"max_size":               cty.NumberIntVal(5),
				"min_size":               cty.NumberIntVal(1),
				"initial_lifecycle_hook": cty.SetValEmpty(asgHookType()),
			})
			filled, n := fillResidue(cold, schema.Block, attrs, secrets, cty.NilVal)
			if n != 1 {
				t.Fatalf("fillResidue filled %d, want 1 (initial_lifecycle_hook)", n)
			}
			if !filled.GetAttr("initial_lifecycle_hook").RawEquals(wantHook) {
				t.Fatalf("filled initial_lifecycle_hook = %#v, want %#v", filled.GetAttr("initial_lifecycle_hook"), wantHook)
			}

			// A stateless replan's own emptiness check - the shape #385's
			// repro quotes ("+/- resource ... must be replaced") - is a
			// mismatch between the filled prior and the planned value. If
			// fillResidue put the exact applied set back, the prior and a
			// plan built from unchanged configuration agree on this
			// attribute and nothing forces a replacement over it.
			if !filled.GetAttr("initial_lifecycle_hook").RawEquals(applied.GetAttr("initial_lifecycle_hook")) {
				t.Fatalf("filled prior's initial_lifecycle_hook does not match a plan built from unchanged configuration - this is #385's forced replacement, still reproducing")
			}
		})
	}
}
