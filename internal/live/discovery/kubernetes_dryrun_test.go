// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/tfdiags"
)

func crontabObject(name string) map[string]any {
	return map[string]any{
		"apiVersion": "stable.example.com/v1",
		"kind":       "CronTab",
		"metadata":   map[string]any{"name": name, "namespace": "smoke-crd", "labels": map[string]any{"tofu-estate": "smoke-crd"}},
		"spec":       map[string]any{"cronSpec": "* * * * */5"},
	}
}

func diagSummaries(diags tfdiags.Diagnostics) []string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Description().Summary)
	}
	return out
}

// TestDryRunRejectionIsARefusalByName (GitHub issue #1081, item 3): the
// server's no is an error naming the instance and quoting the server;
// its yes is one evidence line carrying the defaulted count; both are
// asked once, in address order, with the verb the plan proposed.
func TestDryRunRejectionIsARefusalByName(t *testing.T) {
	sweeper := &stubSweeper{
		reject:    map[string]string{"bad": `CronTab.stable.example.com "bad" is invalid: spec.replicas: Invalid value: "string": spec.replicas in body must be of type integer`},
		defaulted: 2,
	}
	objects := []DryRunObject{
		{Addr: k8sInstance(t, "kubernetes_manifest", "second"), Manifest: crontabObject("bad"), Update: true},
		{Addr: k8sInstance(t, "kubernetes_manifest", "first"), Manifest: crontabObject("good")},
	}
	evidence, diags := DryRunKubernetesManifests(context.Background(), sweeper, nil, objects)
	if !diags.HasErrors() {
		t.Fatalf("the server's rejection was not an error: %v", diagSummaries(diags))
	}
	if got := diagSummaries(diags); len(got) != 1 || got[0] != SummaryKubernetesDryRunRejected {
		t.Fatalf("diagnostics = %v, want exactly [%s]", got, SummaryKubernetesDryRunRejected)
	}
	detail := diags[0].Description().Detail
	for _, want := range []string{"kubernetes_manifest.second", "update", "spec.replicas in body must be of type integer", "dryRun=All", "nothing is applied"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, detail)
		}
	}
	if len(evidence) != 2 || evidence[0].Addr.String() != "kubernetes_manifest.first" || evidence[1].Addr.String() != "kubernetes_manifest.second" {
		t.Fatalf("evidence is not one line per object in address order: %+v", evidence)
	}
	if e := evidence[0]; e.NotSubmitted != "" || e.Kind != "CronTab" || e.Namespace != "smoke-crd" || e.Name != "good" || e.Defaulted != 2 || e.Update {
		t.Errorf("the accepted object's evidence: %+v", e)
	}
	if e := evidence[1]; !strings.HasPrefix(e.NotSubmitted, "rejected: ") || !e.Update {
		t.Errorf("the rejected object's evidence: %+v", e)
	}
	if want := []string{"good update=false", "bad update=true"}; strings.Join(sweeper.dryRuns, ";") != strings.Join(want, ";") {
		t.Errorf("DryRun asked %v, want %v", sweeper.dryRuns, want)
	}
}

// TestDryRunUnreachableServerIsAWarning: a cluster that cannot answer is a
// coverage gap, and an object with values unknown until apply is never
// submitted; neither refuses the plan.
func TestDryRunUnreachableServerIsAWarning(t *testing.T) {
	sweeper := &stubSweeper{dryRunErr: errors.New("connection refused")}
	objects := []DryRunObject{
		{Addr: k8sInstance(t, "kubernetes_manifest", "a"), Manifest: crontabObject("a")},
		{Addr: k8sInstance(t, "kubernetes_manifest", "b"), NotSubmitted: "the planned manifest holds values not known until apply, so there is no object to submit yet"},
	}
	evidence, diags := DryRunKubernetesManifests(context.Background(), sweeper, nil, objects)
	if diags.HasErrors() {
		t.Fatalf("a server that cannot answer refused the plan: %v", diagSummaries(diags))
	}
	if got := diagSummaries(diags); len(got) != 1 || got[0] != SummaryKubernetesDryRunUnavailable {
		t.Fatalf("diagnostics = %v, want exactly [%s]", got, SummaryKubernetesDryRunUnavailable)
	}
	if !strings.Contains(diags[0].Description().Detail, "connection refused") {
		t.Errorf("the warning does not carry the server's failure: %s", diags[0].Description().Detail)
	}
	if len(evidence) != 2 {
		t.Fatalf("evidence = %+v, want two lines", evidence)
	}
	if !strings.Contains(evidence[0].NotSubmitted, "could not answer") {
		t.Errorf("the unreachable object's evidence: %+v", evidence[0])
	}
	if !strings.Contains(evidence[1].NotSubmitted, "not known until apply") {
		t.Errorf("the unknown object's evidence: %+v", evidence[1])
	}
	if len(sweeper.dryRuns) != 1 {
		t.Errorf("DryRun asked %v; the unknown manifest must never be submitted", sweeper.dryRuns)
	}
	// Nothing to submit, nothing said.
	if ev, d := DryRunKubernetesManifests(context.Background(), sweeper, nil, nil); ev != nil || len(d) != 0 {
		t.Errorf("no objects produced %+v %v", ev, d)
	}
}
