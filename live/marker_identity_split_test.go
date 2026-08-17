// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package residue

import (
	"sort"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// The marker answers "may I delete this". It does not answer "which object
// is this". HANDOFF.md's "Two questions, not one" section is the rule; this
// file is the part of it a reader cannot skip.
//
// Why it needs a guard at all. Admission has drifted toward refusing a type
// because no marker can be written on it, which is a different claim from
// "nothing says which instance this is". A resource can be perfectly
// identified by its own declaration and still have nowhere to hang a tag -
// every association, attachment and membership in the provider is that
// shape, and the identity table already carries such rows.
// aws_iam_group_policy_attachment is one: untaggable, no ARN, and admitted
// with a composite of {group} "/" {policy_arn}, both client-supplied.
//
// So untaggability alone must never decide admission, and the derived rule
// agrees - tools/row-gen's markerless() requires BOTH an untaggable type and
// a server-minted identity before it vetoes, and [identity.MarkerlessReason]
// states the pair. The failure this test exists for is subtler than someone
// deleting a condition: it is the two generators disagreeing about the
// second fact while each stays internally consistent.
//
// tools/survey-gen classifies a type "client-named" from the provider's
// identity schema, when every required-for-import attribute is a required
// argument. tools/row-gen reaches the opposite verdict from its own
// classifier bucket or the documentation's minted-segment leg. Where those
// two disagree, a type is simultaneously "its identity is fully client
// assigned" and "the provider mints its identity", and the veto wins
// silently - which is exactly the fusion this file forbids.
//
// The check reads two artifacts neither of which is derived from the other,
// which is what the ratchet rule in this repository asks for: survey-gen
// writes live/survey-full.json from GetProviderSchema, row-gen writes
// MarkerlessTypes from the registry, its own proposals and the doc grammar.
// Nothing an author can edit in one makes this test agree with itself.
func TestMarkerlessVetoNeverContradictsClientNaming(t *testing.T) {
	paths := surveyPathByType(t)

	var got []string
	for typeName := range identity.MarkerlessTypes {
		if paths[typeName] == surveyPathClientNamed {
			got = append(got, typeName)
		}
	}
	sort.Strings(got)

	seen := make(map[string]bool, len(got))
	for _, typeName := range got {
		seen[typeName] = true
		if _, allowed := markerlessClientNamedContradictions[typeName]; allowed {
			continue
		}
		t.Errorf("%s is vetoed as markerless while live/survey-full.json classifies it %q.\n"+
			"Those are contradictory claims about the same fact: the survey says every "+
			"required-for-import identity attribute is a required argument, and the veto says "+
			"the provider mints the identity. One of the two generators is wrong, and until that "+
			"is settled the veto is refusing a type whose identity its own declaration supplies.\n"+
			"A marker is delete permission, not identity - see HANDOFF.md, \"Two questions, not one\".\n"+
			"Fix the generator that is wrong, or record the contradiction in "+
			"markerlessClientNamedContradictions with the reason it stands.",
			typeName, surveyPathClientNamed)
	}

	// The other direction, which is the half that rots. An exception whose
	// contradiction has been resolved reads as a live one to the next reader
	// and quietly licenses the fusion coming back for that type.
	for typeName, reason := range markerlessClientNamedContradictions {
		if seen[typeName] {
			continue
		}
		if _, inSurvey := paths[typeName]; !inSurvey {
			t.Errorf("markerlessClientNamedContradictions names %s (%q), which live/survey-full.json "+
				"does not describe at all - the provider roster moved under the exception. Delete it.",
				typeName, reason)
			continue
		}
		t.Errorf("markerlessClientNamedContradictions names %s (%q) and it no longer contradicts: "+
			"it is either not vetoed as markerless, or no longer classified %q. Delete the entry - "+
			"an exception that no longer applies reads as a live one.",
			typeName, reason, surveyPathClientNamed)
	}
}

// markerlessClientNamedContradictions are the types where the two generators
// disagree today, each with the reason the disagreement is tolerated rather
// than fixed. It is not a place to park new ones: every entry is a type
// whose identity one generator says the configuration supplies, being
// refused admission by the other.
var markerlessClientNamedContradictions = map[string]string{
	"aws_datazone_user_profile": "survey-gen reads the provider's identity schema and finds both " +
		"required-for-import attributes (domain_identifier, user_identifier) are required arguments, " +
		"so it classifies client-named with admission evidence \"schema\". row-gen vetoes it, which " +
		"means its classifier bucketed the type server-assigned or the documentation leg found a " +
		"minted segment. live/rowgen-buckets.json carries only counts, not per-type membership, so " +
		"which of the two legs fired cannot be read off a committed artifact and needs a row-gen run " +
		"to settle. Recorded rather than fixed because settling it is a generator change, not a " +
		"ledger edit, and the type appears in no corpus configuration - so it costs no estate today.",
}

const surveyPathClientNamed = "client-named"

// surveyPathByType is live/survey-full.json's per-type Path column, the
// vocabulary live/SURVEY.md promises. It is read here rather than recomputed
// so this test consults survey-gen's verdict rather than a second opinion of
// its own - the point being to catch two generators disagreeing, which a
// re-derivation would hide by replacing one of them.
func surveyPathByType(t *testing.T) map[string]string {
	t.Helper()
	var survey struct {
		Counts struct {
			Types int `json:"types"`
		} `json:"counts"`
		Types []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"types"`
	}
	decodeInto(t, "survey-full.json", &survey)

	paths := make(map[string]string, len(survey.Types))
	for _, e := range survey.Types {
		paths[e.Type] = e.Path
	}
	if len(paths) != survey.Counts.Types {
		t.Fatalf("live/survey-full.json lists %d distinct types but its own counts.types says %d; "+
			"one of the two is stale", len(paths), survey.Counts.Types)
	}
	return paths
}
