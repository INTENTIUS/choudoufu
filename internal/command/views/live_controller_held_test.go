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
			{TypeName: "kubernetes_config_map_v1", Kind: "ConfigMap", LiveID: "smoke-k8s/web-greeting", Controller: "Helm", HeldBy: "Helm release smoke-k8s/web"},
		},
	})
	out := done(t).Stdout()
	// The renderer word-wraps; compare with whitespace collapsed.
	flat := strings.Join(strings.Fields(out), " ")
	for _, want := range []string{
		"Controller-held: 1 live resource held by a controller, not a block",
		"ConfigMap smoke-k8s/web-greeting held by Helm release smoke-k8s/web",
		"never proposes destroying one",
		"take tofu-estate out of the chart's values",
	} {
		if !strings.Contains(flat, want) {
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

// GitHub issue #1738 item 4: an object annotated with a live Helm release
// whose manifest does not list it is listed with the release named, and
// is not rendered as held.
func TestLiveLs_HelmNotInManifest(t *testing.T) {
	item := LiveLsItem{
		ID: "web/web-old", Type: "kubernetes_config_map_v1", Kind: "ConfigMap", APIVersion: "v1",
		Source: "kubernetes", Tags: map[string]string{"tofu-estate": "smoke-k8s"},
		NotInManifest: "Helm release web/web",
	}
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsHuman{view: NewView(streams)}).Report(LiveLsReport{Estate: "smoke-k8s", Items: []LiveLsItem{item}})
	out := done(t).Stdout()
	if !strings.Contains(out, "  annotated with live Helm release web/web, not in its manifest (not swept)\n") || strings.Contains(out, "held by:") {
		t.Errorf("human output:\n%s", out)
	}
	js := renderLiveLsJSON(t, LiveLsReport{Estate: "smoke-k8s", Items: []LiveLsItem{item}})
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(js), &doc); err != nil {
		t.Fatalf("not JSON: %s\n%s", err, js)
	}
	if doc.Items[0]["not_in_release_manifest"] != "Helm release web/web" {
		t.Errorf("item = %v, want not_in_release_manifest", doc.Items[0])
	}
	if _, present := doc.Items[0]["held_by"]; present {
		t.Errorf("an unlisted item carries held_by: %v", doc.Items[0])
	}
}
