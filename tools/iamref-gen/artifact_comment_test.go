// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestActionsListingResourceTagCommentClaimHoldsAgainstTheData is GitHub
// issue #658's guard for Row's ActionsTotal/ActionsListingResourceTag doc
// comment (artifact.go), which used to type live/iam-reference.json's
// resolved and services-listing-resource-tag counts as a literal pair -
// "142 of 160 resolved services list aws:ResourceTag on no action at all"
// and "the count of services that DO name it (18)" - that drifted silently
// to 163 resolved and 18 listing with nothing to notice, because a Go doc
// comment is not rendered or diffed by anything.
//
// The fix removed the literal figures rather than retyping them (there is
// no render step for a source comment to hook into the way render.go's
// generated prose has one), and pointed at the artifact's own field names
// instead. What is left for this test to hold is the comment's two
// qualitative claims about the CURRENT artifact: that most resolved
// services list aws:ResourceTag on no action at all, and that Lambda -
// named as the concrete example that makes the "lists vs. supports"
// asymmetry legible - is one of them. If either claim stops being true, a
// maintainer has to touch the sentence again, on purpose, rather than the
// comment quietly going stale a second time.
func TestActionsListingResourceTagCommentClaimHoldsAgainstTheData(t *testing.T) {
	root := repoRootForTest(t)
	raw, err := os.ReadFile(filepath.Join(root, "live", "iam-reference.json"))
	if err != nil {
		t.Fatalf("reading the artifact: %v", err)
	}
	var art Artifact
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatalf("decoding the artifact: %v", err)
	}

	if art.Counts.Resolved == 0 {
		t.Fatal("no resolved services in the artifact; this test measures nothing")
	}
	if art.Counts.ServicesListingResourceTag*2 >= art.Counts.Resolved {
		t.Errorf("the sparseness artifact.go's doc comment describes no longer holds: "+
			"%d of %d resolved services list aws:ResourceTag on at least one action, which "+
			"is no longer a small minority. Update the comment on Row.ActionsTotal / "+
			"Row.ActionsListingResourceTag (tools/iamref-gen/artifact.go) rather than leaving "+
			"it to describe a shape the data has moved away from.",
			art.Counts.ServicesListingResourceTag, art.Counts.Resolved)
	}

	found := false
	for _, r := range art.Rows {
		if r.IAMPrefix != "lambda" {
			continue
		}
		found = true
		if r.ActionsListingResourceTag != 0 {
			t.Errorf("artifact.go's doc comment names Lambda as a resolved service that lists "+
				"aws:ResourceTag on no action at all, but the artifact's lambda row now lists it "+
				"on %d action(s). Update the comment: Lambda is its concrete example of the "+
				"'lists vs. supports' asymmetry, so it has to stay a true example, or be replaced "+
				"with one that still is.", r.ActionsListingResourceTag)
		}
		break
	}
	if !found {
		t.Fatal("no lambda row in the artifact; this test measures nothing")
	}
}
