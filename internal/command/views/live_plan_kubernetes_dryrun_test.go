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

// TestStatelessPlan_kubernetesDryRun pins the evidence section (GitHub
// issue #1081, item 3): one line per planned object naming the address,
// the kind and natural key, the verb, the server's acceptance and the
// defaulted count; a rejected object points at the error below; an object
// with nothing to submit says why. The heading counts the accepted ones.
func TestStatelessPlan_kubernetesDryRun(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))

	v.KubernetesDryRun([]StatelessKubernetesDryRun{
		{Addr: "kubernetes_manifest.crontab", Kind: "CronTab", Namespace: "smoke-crd", Name: "my-crontab", Defaulted: 2},
		{Addr: "kubernetes_manifest.broken", Kind: "CronTab", Namespace: "smoke-crd", Name: "broken", Update: true, NotSubmitted: "rejected: spec.replicas in body must be of type integer"},
		{Addr: "kubernetes_manifest.later", NotSubmitted: "the planned manifest holds values not known until apply, so there is no object to submit yet"},
	})

	got := done(t).Stdout()
	for _, want := range []string{
		"Server-side dry run: 1 of 3 planned Kubernetes objects accepted by the API server",
		"kubernetes_manifest.crontab [ACCEPTED] CronTab smoke-crd/my-crontab: create accepted by the server's admission, dry run, nothing written (2 fields defaulted by the server)",
		"kubernetes_manifest.broken [REJECTED] CronTab smoke-crd/broken: update refused by the server; see the error below",
		"kubernetes_manifest.later [NOT SUBMITTED] the planned manifest holds values not known until apply",
		"dryRun=All",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the section does not contain %q:\n%s", want, got)
		}
	}
}

// TestStatelessPlan_kubernetesDryRunEmpty: a plan with no manifest object
// renders no section at all.
func TestStatelessPlan_kubernetesDryRunEmpty(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))
	v.KubernetesDryRun(nil)
	if got := done(t).Stdout(); got != "" {
		t.Errorf("an empty dry-run list rendered output:\n%s", got)
	}
}
