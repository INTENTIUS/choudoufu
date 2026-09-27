// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/terminal"
)

// TestForeign_controllerHeldNamesTheRelease (GitHub issue #1607): an object
// a Helm release holds is listed with its release under its own heading,
// never under "Owned and undeclared".
func TestForeign_controllerHeldNamesTheRelease(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	NewStatelessPlan(NewView(streams).SetRunningInAutomation(true)).Foreign(StatelessForeign{
		Estate: "smoke-k8s",
		ControllerHeld: []StatelessControllerHeld{
			{Kind: "ConfigMap", ID: "smoke-k8s/web-greeting", HeldBy: "Helm release smoke-k8s/web"},
		},
	})
	out := done(t).Stdout()
	for _, want := range []string{
		"Controller-held: 1 live object carries this estate's label and is not swept",
		"ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web",
		"never proposed for destruction",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the plan does not say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "will be destroyed") {
		t.Errorf("a controller-held object is rendered as a removal:\n%s", out)
	}
}

func TestLiveLsHuman_ControllerHeldObject(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsHuman{view: NewView(streams)}).Report(LiveLsReport{
		Estate: "smoke-k8s",
		Items: []LiveLsItem{{
			ID: "smoke-k8s/web-greeting", Type: "kubernetes_config_map_v1", Kind: "ConfigMap", APIVersion: "v1",
			Source: "kubernetes", Tags: map[string]string{"tofu-estate": "smoke-k8s"},
			HeldBy: "Helm release smoke-k8s/web",
		}},
	})
	out := done(t).Stdout()
	const want = `
kubernetes_config_map_v1     smoke-k8s/web-greeting
  kind:    ConfigMap (v1)
  held by: Helm release smoke-k8s/web (controller-held: never swept, never adopted)
  address: (none - the holder above owns this object, not a block)
  found by: kubernetes
  labels:  tofu-estate=smoke-k8s
`
	if !strings.Contains(out, want) {
		t.Errorf("a controller-held object renders differently.\n--- got ---\n%s\n--- want (contained) ---\n%s", out, want)
	}
}

func TestLiveLsJSON_ControllerHeldCarriesHeldBy(t *testing.T) {
	out := renderLiveLsJSON(t, LiveLsReport{
		Estate: "smoke-k8s",
		Items: []LiveLsItem{
			{ID: "smoke-k8s/web-greeting", Type: "kubernetes_config_map_v1", Kind: "ConfigMap", APIVersion: "v1", Source: "kubernetes", HeldBy: "Helm release smoke-k8s/web"},
			{ID: "smoke-k8s/app", Type: "kubernetes_config_map_v1", Kind: "ConfigMap", APIVersion: "v1", Source: "kubernetes"},
		},
	})
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %s\n%s", err, out)
	}
	if doc.Items[0]["held_by"] != "Helm release smoke-k8s/web" {
		t.Errorf("held item = %v, want held_by", doc.Items[0])
	}
	if _, present := doc.Items[1]["held_by"]; present {
		t.Errorf("an ordinary item carries held_by: %v", doc.Items[1])
	}
}
