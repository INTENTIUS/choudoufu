// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/markers"
)

// TestLivePlanForeignCarriesTheUnclaimedIntoTheDocument is GitHub issue
// #1197's prerequisite: the estate-wide sweep already computes which live
// resources nobody claims, and the human render already prints them, but the
// -json document carried no such section - so live-discover and chant's
// choudoufuLivePlan activity had no way to ask the question even though the
// run held the answer.
func TestLivePlanForeignCarriesTheUnclaimedIntoTheDocument(t *testing.T) {
	rep := views.StatelessForeign{
		Items: []views.StatelessForeignItem{
			{
				TypeName:    "aws_iam_role",
				LiveID:      "left-behind",
				DisplayName: "left-behind",
				Why:         "no declared instance names this identity",
			},
			{
				TypeName: "aws_s3_bucket",
				LiveID:   "other-estates-bucket",
				Tags: []views.StatelessTag{
					{Key: markers.TagEstate, Value: "platform-prod"},
					{Key: "Name", Value: "logs"},
				},
				Why: "carries another estate's marker",
			},
		},
	}

	got := livePlanForeign(rep)
	if len(got) != 2 {
		t.Fatalf("projected %d row(s), want 2: %#v", len(got), got)
	}

	if got[0].TypeName != "aws_iam_role" || got[0].LiveID != "left-behind" {
		t.Errorf("first row = %+v, want the iam role verbatim", got[0])
	}
	if got[0].HeldBy != "" {
		t.Errorf("HeldBy = %q for a resource carrying no marker, want empty", got[0].HeldBy)
	}
	if got[0].Why == "" {
		t.Error("Why is empty; the sweep's own reason must be carried rather than re-derived")
	}

	// The case a reader chasing a mis-owned resource actually needs: the
	// marker is another estate's, and that estate's name is the answer.
	if got[1].HeldBy != "platform-prod" {
		t.Errorf("HeldBy = %q, want the other estate's marker value", got[1].HeldBy)
	}
}

// TestLivePlanForeignDistinguishesNoSweepFromNothingFound: an observation run
// never asks the account-bounded question, so it must not report an empty list
// that reads as "nothing foreign exists". Swept is the field that says which
// types were listed; this one returns nil so the two are distinguishable in
// the document itself.
func TestLivePlanForeignDistinguishesNoSweepFromNothingFound(t *testing.T) {
	if got := livePlanForeign(views.StatelessForeign{}); got != nil {
		t.Errorf("a run that swept nothing projected %#v, want nil", got)
	}
}

// TestLivePlanForeignIsInTheDocumentJSON pins the wire shape, because the
// consumers are other programs: chant's terraform lexicon reads this document
// by field name, so a rename is a break for them rather than a refactor here.
func TestLivePlanForeignIsInTheDocumentJSON(t *testing.T) {
	doc := views.LivePlanDocument{
		Estate: "app",
		Foreign: []views.LivePlanForeign{
			{TypeName: "aws_iam_role", LiveID: "x", HeldBy: "other", Why: "because"},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	for _, want := range []string{`"foreign"`, `"type":"aws_iam_role"`, `"identity":"x"`, `"tofu_estate":"other"`, `"why":"because"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("document JSON is missing %s:\n%s", want, b)
		}
	}

	// tofu_estate is omitempty, matching StatelessUnowned's own field, so a
	// resource nobody owns does not carry an empty string that reads as an
	// estate named "".
	plain, err := json.Marshal(views.LivePlanDocument{
		Foreign: []views.LivePlanForeign{{TypeName: "aws_iam_role", LiveID: "x"}},
	})
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if strings.Contains(string(plain), `"tofu_estate"`) {
		t.Errorf("an unmarked foreign resource emitted tofu_estate:\n%s", plain)
	}
}
