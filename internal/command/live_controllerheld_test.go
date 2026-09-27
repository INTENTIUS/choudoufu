// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/foreign"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/terminal"
)

// TestLiveLs_controllerHeldItemIsNamed (GitHub issue #1606): a resource the
// listing finds under the estate's marker that also carries an ACK
// controller's tags gets a line naming it controller-held and the object
// that made it, in the prose and in the JSON document.
func TestLiveLs_controllerHeldItemIsNamed(t *testing.T) {
	item := liveLsItemFromTags("arn:aws:s3:::ack-made-bucket", map[string]string{
		"tofu-estate":                         "prod",
		"tofu-address":                        "aws_s3_bucket.assets",
		"services.k8s.aws/controller-version": "s3-v1.0.14",
		"services.k8s.aws/namespace":          "team-a",
	}, "tagging")
	rep := views.LiveLsReport{Estate: "prod", Items: []views.LiveLsItem{item}}

	streams, done := terminal.StreamsForTesting(t)
	views.NewLiveLs(arguments.ViewOptions{ViewType: arguments.ViewHuman}, views.NewView(streams)).Report(rep)
	human := done(t).Stdout()
	want := "controller-held: made by ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a"
	if !strings.Contains(human, want) {
		t.Errorf("live-ls does not name the ACK-made bucket controller-held; want %q in:\n%s", want, human)
	}

	streams, done = terminal.StreamsForTesting(t)
	views.NewLiveLs(arguments.ViewOptions{ViewType: arguments.ViewJSON}, views.NewView(streams)).Report(rep)
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(done(t).Stdout()), &doc); err != nil {
		t.Fatalf("decoding the live-ls document: %v", err)
	}
	if len(doc.Items) != 1 || doc.Items[0]["controller_held"] == nil {
		t.Errorf("the live-ls document carries no controller_held for the ACK-made bucket: %v", doc.Items)
	}

	// An ordinary item says nothing about controllers.
	plain := liveLsItemFromTags("arn:aws:s3:::plain", map[string]string{
		"tofu-estate":  "prod",
		"tofu-address": "aws_s3_bucket.plain",
	}, "tagging")
	streams, done = terminal.StreamsForTesting(t)
	views.NewLiveLs(arguments.ViewOptions{ViewType: arguments.ViewHuman}, views.NewView(streams)).Report(views.LiveLsReport{Estate: "prod", Items: []views.LiveLsItem{plain}})
	if out := done(t).Stdout(); strings.Contains(out, "controller-held") {
		t.Errorf("an ordinary item is reported controller-held:\n%s", out)
	}
}

// TestLivePlan_controllerHeldSection (GitHub issue #1606): the plan's
// report carries every controller-held resource discovery set aside, in
// its own section and in the -json document, and none of them in the
// foreign or adoptable ones.
func TestLivePlan_controllerHeldSection(t *testing.T) {
	ack, _ := markers.ControllerHeld(map[string]string{
		"services.k8s.aws/controller-version": "s3-v1.0.14",
		"services.k8s.aws/namespace":          "team-a",
	})
	xp, _ := markers.ControllerHeld(map[string]string{
		"crossplane-kind":           "bucket.s3.aws.upbound.io",
		"crossplane-name":           "assets",
		"crossplane-providerconfig": "default",
	})
	orphanAddr, diags := addrs.ParseAbsResourceInstanceStr("aws_s3_bucket.gone")
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	res := &foreign.Result{
		Estate: "prod",
		Swept:  []string{"aws_s3_bucket"},
		ControllerHeld: []discovery.ControllerHeldResource{
			{TypeName: "aws_s3_bucket", ImportID: "ack-made-bucket", Hold: ack},
			{TypeName: "aws_s3_bucket", ImportID: "xp-made-bucket", Hold: xp, Marked: true, Addr: orphanAddr},
		},
	}
	rep := statelessForeignReport(res, nil)
	if len(rep.Items) != 0 || len(rep.Candidates) != 0 || len(rep.Removals) != 0 {
		t.Fatalf("controller-held resources leaked into another section: %+v", rep)
	}

	streams, done := terminal.StreamsForTesting(t)
	views.NewStatelessPlan(views.NewView(streams).SetRunningInAutomation(true)).Foreign(rep)
	human := done(t).Stdout()
	// The renderer word-wraps; compare with whitespace collapsed.
	flat := strings.Join(strings.Fields(human), " ")
	for _, want := range []string{
		"Controller-held: 2 live resources made by an in-cluster controller",
		"aws_s3_bucket ack-made-bucket [CONTROLLER-HELD]",
		"made by ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a",
		`made by Crossplane managed resource bucket.s3.aws.upbound.io "assets" (providerconfig default)`,
		"carries this estate's marker for aws_s3_bucket.gone, which the configuration does not declare; not destroyed.",
		"carries an ownership marker or is controller-held",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the plan report does not say %q:\n%s", want, human)
		}
	}

	rows := livePlanControllerHeld(rep)
	if len(rows) != 2 || rows[0].Controller != "ACK" || rows[1].Controller != "Crossplane" || rows[1].Addr != "aws_s3_bucket.gone" {
		t.Errorf("the -json document's controller_held rows are wrong: %+v", rows)
	}
	if got := livePlanForeign(rep); got != nil {
		t.Errorf("the -json document reports a controller-held resource as foreign: %+v", got)
	}
}
