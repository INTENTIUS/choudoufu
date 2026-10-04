// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
)

// TestLivePlan_policyWithheldNamesItsQuadrant (#1885): a withheld orphan's
// line named [undeclared_tagged=keep] whatever quadrant its verb came
// from, beside a sentence naming policy.undeclared_untagged. The line
// names the quadrant the verb was read from, and an entry that names none
// keeps the old reading.
func TestLivePlan_policyWithheldNamesItsQuadrant(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewLivePlan(NewView(streams).SetRunningInAutomation(true))
	v.Policy(LivePolicyReport{Withheld: []LivePolicyWithheld{
		{TypeName: "kubernetes_annotations", LiveID: "sc", Verb: "keep", Quadrant: "undeclared_untagged",
			Withheld: `policy.undeclared_untagged = "keep": kept`},
		{TypeName: "aws_s3_bucket", LiveID: "b", Verb: "report"},
	}})
	got := done(t).Stdout()
	if !strings.Contains(got, "kubernetes_annotations sc [undeclared_untagged=keep]") {
		t.Errorf("the untagged quadrant's line does not name it:\n%s", got)
	}
	if strings.Contains(got, "kubernetes_annotations sc [undeclared_tagged") {
		t.Errorf("the line names undeclared_tagged, whose verb was not applied:\n%s", got)
	}
	if !strings.Contains(got, "aws_s3_bucket b [undeclared_tagged=report]") {
		t.Errorf("an entry with no quadrant lost the undeclared_tagged reading:\n%s", got)
	}
}
