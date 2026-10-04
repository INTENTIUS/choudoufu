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
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/terminal"
)

// TestLiveLs_controllerHeldItemIsNamed (GitHub issue #1606): a resource the
// listing finds under the estate's marker that also carries an ACK
// controller's tags gets a line naming it controller-held and the object
// that made it, in the prose and in the JSON document, in the same words
// and under the same key a Helm-held Kubernetes object gets (#1607;
// TestLiveLsHuman_ControllerHeldObject): "held by:" and "held_by".
func TestLiveLs_controllerHeldItemIsNamed(t *testing.T) {
	item := liveLsItemFromTags("arn:aws:s3:::ack-made-bucket", map[string]string{
		"tofu-estate":                         "prod",
		"tofu-address":                        "aws_s3_bucket.assets",
		"services.k8s.aws/controller-version": "s3-v1.0.14",
		"services.k8s.aws/namespace":          "team-a",
	}, "tagging")
	rep := views.LiveLsReport{Estate: "prod", Items: []views.LiveLsItem{item}}

	streams, done := terminal.StreamsForTesting(t)
	views.NewLiveLs(&arguments.View{ViewType: arguments.ViewHuman}, views.NewView(streams)).Report(rep)
	human := done(t).Stdout()
	want := "  held by: ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a (controller-held: never swept, never adopted)"
	if !strings.Contains(human, want) {
		t.Errorf("live-ls does not name the ACK-made bucket controller-held; want %q in:\n%s", want, human)
	}

	streams, done = terminal.StreamsForTesting(t)
	views.NewLiveLs(&arguments.View{ViewType: arguments.ViewJSON}, views.NewView(streams)).Report(rep)
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(done(t).Stdout()), &doc); err != nil {
		t.Fatalf("decoding the live-ls document: %v", err)
	}
	if len(doc.Items) != 1 || doc.Items[0]["held_by"] != "ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a" {
		t.Errorf("the live-ls document carries no held_by for the ACK-made bucket: %v", doc.Items)
	}
	if _, two := doc.Items[0]["controller_held"]; two {
		t.Errorf("the live-ls document names a controller-held item under a second key: %v", doc.Items)
	}

	// An ordinary item says nothing about controllers.
	plain := liveLsItemFromTags("arn:aws:s3:::plain", map[string]string{
		"tofu-estate":  "prod",
		"tofu-address": "aws_s3_bucket.plain",
	}, "tagging")
	streams, done = terminal.StreamsForTesting(t)
	views.NewLiveLs(&arguments.View{ViewType: arguments.ViewHuman}, views.NewView(streams)).Report(views.LiveLsReport{Estate: "prod", Items: []views.LiveLsItem{plain}})
	if out := done(t).Stdout(); strings.Contains(out, "controller-held") {
		t.Errorf("an ordinary item is reported controller-held:\n%s", out)
	}
}

// TestLivePlan_controllerHeldSection (GitHub issues #1606, #1607): the
// plan's report carries every controller-held resource discovery set
// aside, AWS and Kubernetes in one section with one line shape and in one
// -json list, and none of them in the foreign or adoptable ones.
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
			{TypeName: "aws_s3_bucket", ImportID: "ack-made-bucket", Controller: string(ack.Controller), HeldBy: ack.Describe()},
			{TypeName: "aws_s3_bucket", ImportID: "xp-made-bucket", Controller: string(xp.Controller), HeldBy: xp.Describe(), Marked: true, Addr: orphanAddr},
			{TypeName: "kubernetes_config_map_v1", ImportID: "smoke-k8s/web-greeting", Kind: "ConfigMap", Controller: kubesweep.ControllerHelm, HeldBy: "Helm release smoke-k8s/web"},
		},
	}
	rep := liveForeignReport(res, nil)
	if len(rep.Items) != 0 || len(rep.Candidates) != 0 || len(rep.Removals) != 0 {
		t.Fatalf("controller-held resources leaked into another section: %+v", rep)
	}

	streams, done := terminal.StreamsForTesting(t)
	views.NewLivePlan(views.NewView(streams).SetRunningInAutomation(true)).Foreign(rep)
	human := done(t).Stdout()
	// The renderer word-wraps; compare with whitespace collapsed.
	flat := strings.Join(strings.Fields(human), " ")
	for _, want := range []string{
		"Controller-held: 3 live resources held by a controller, not a block",
		"aws_s3_bucket ack-made-bucket held by ACK s3 controller (s3-v1.0.14), custom resource in namespace team-a",
		`aws_s3_bucket xp-made-bucket held by Crossplane managed resource bucket.s3.aws.upbound.io "assets" (providerconfig default)`,
		"ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web",
		"take tofu-estate out of the chart's values",
		"carries this estate's marker for aws_s3_bucket.gone, which the configuration does not declare; not destroyed.",
		"carries an ownership marker or is controller-held",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the plan report does not say %q:\n%s", want, human)
		}
	}

	rows := livePlanControllerHeld(rep)
	if len(rows) != 3 || rows[0].Controller != "ACK" || rows[1].Controller != "Crossplane" || rows[1].Addr != "aws_s3_bucket.gone" ||
		rows[2].Controller != "Helm" || rows[2].Kind != "ConfigMap" || rows[2].HeldBy != "Helm release smoke-k8s/web" || rows[0].HeldBy != ack.Describe() {
		t.Errorf("the -json document's controller_held rows are wrong: %+v", rows)
	}
	encoded, err := json.Marshal(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"held_by":`) || strings.Contains(string(encoded), `"made_by"`) {
		t.Errorf("a controller_held row names its holder under a key other than live-ls's held_by: %s", encoded)
	}

	// The Helm remedy is for Helm: an AWS-only section does not print it.
	awsOnly := rep
	awsOnly.ControllerHeld = rep.ControllerHeld[:2]
	streams, done = terminal.StreamsForTesting(t)
	views.NewLivePlan(views.NewView(streams).SetRunningInAutomation(true)).Foreign(awsOnly)
	if out := done(t).Stdout(); strings.Contains(out, "chart's values") {
		t.Errorf("an AWS-only controller-held section carries the Helm remedy:\n%s", out)
	}
	if got := livePlanForeign(rep); got != nil {
		t.Errorf("the -json document reports a controller-held resource as foreign: %+v", got)
	}
}
