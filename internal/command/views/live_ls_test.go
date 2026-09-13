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

// GitHub issue #966, second half. "choudoufu live-ls -json DIR"'s
// declared-instance comparison needs provider schemas to tell a
// declaration-carried instance (no tags argument, so no marker was ever
// written, so the listing structurally cannot see it) from a real absence,
// and without them liveLsRung classifies nothing at all. The document said
// neither that, nor that the comparison had been skipped: it simply had no
// "gaps" key, which reads as "no gaps".

func renderLiveLsJSON(t *testing.T, rep LiveLsReport) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsJSON{view: NewView(streams)}).Report(rep)
	return done(t).Stdout()
}

func decodeLiveLsDocument(t *testing.T, out string) liveLsJSONReport {
	t.Helper()
	var doc liveLsJSONReport
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %s\n%s", err, out)
	}
	return doc
}

// TestLiveLsJSON_SaysWhetherProviderSchemasBackedTheComparison asserts the
// field by VALUE in both directions, for the reason
// [TestJSONSaysWhetherProviderSchemasBackedTheRungs] does: the accurate
// answer and the degraded one are otherwise the same document.
func TestLiveLsJSON_SaysWhetherProviderSchemasBackedTheComparison(t *testing.T) {
	withSchemas := decodeLiveLsDocument(t, renderLiveLsJSON(t, LiveLsReport{Estate: "dev", ConfigDir: ".", Schemas: true}))
	if withSchemas.Schemas != "provider" {
		t.Errorf("schemas = %q for a comparison that read provider schemas, want \"provider\"", withSchemas.Schemas)
	}

	withoutSchemas := decodeLiveLsDocument(t, renderLiveLsJSON(t, LiveLsReport{Estate: "dev", ConfigDir: "."}))
	if withoutSchemas.Schemas != "builtin" {
		t.Errorf("schemas = %q for a comparison with no provider schemas, want \"builtin\"", withoutSchemas.Schemas)
	}
}

// TestLiveLsJSON_GapsIsNeverAbsentOrNull is the issue's own observation
// ("gaps: (key absent) ... reads as no gaps") turned into a check. An
// absent key and an empty list are different claims and the document only
// ever knew how to make the first one.
func TestLiveLsJSON_GapsIsNeverAbsentOrNull(t *testing.T) {
	for _, rep := range []LiveLsReport{
		{Estate: "dev"},
		{Estate: "dev", ConfigDir: ".", Schemas: true},
		{Estate: "dev", ConfigDir: ".", GapsSkipped: "the configuration would not load."},
	} {
		out := renderLiveLsJSON(t, rep)
		if !strings.Contains(out, `"gaps": [`) {
			t.Errorf("gaps is absent or null for %+v:\n%s", rep, out)
		}
	}
}

// TestLiveLsJSON_SkippedComparisonStatesItsReason: an empty gap list means
// two opposite things - the comparison ran and found nothing, or it never
// ran - and a reader has to be able to tell them apart without parsing the
// prose warning that accompanies the second.
func TestLiveLsJSON_SkippedComparisonStatesItsReason(t *testing.T) {
	skipped := decodeLiveLsDocument(t, renderLiveLsJSON(t, LiveLsReport{
		Estate: "dev", ConfigDir: ".", GapsSkipped: "./x is outside the stateless subset (3 issue(s)).",
	}))
	if skipped.GapsSkipped == "" {
		t.Error("a skipped comparison printed no reason, so an empty gaps list is indistinguishable from a complete one")
	}
	if len(skipped.Gaps) != 0 {
		t.Errorf("a skipped comparison printed gaps: %+v", skipped.Gaps)
	}

	ran := decodeLiveLsDocument(t, renderLiveLsJSON(t, LiveLsReport{Estate: "dev", ConfigDir: ".", Schemas: true}))
	if ran.GapsSkipped != "" {
		t.Errorf("gaps_skipped = %q for a comparison that ran to completion, want it omitted", ran.GapsSkipped)
	}
}

// TestLiveLsDocument_topLevelShapeIsPinned pins this document by VALUE for
// the reason [TestLiveCheckDocument_topLevelShapeIsPinned] pins that one:
// behold (GitHub issue #789 names it, and INTENTIUS/behold#366 filed #966)
// parses it and does not recompile when this struct does.
//
// If a field is deliberately added, this test fails and the fix is to
// update `want` below AND to say in the pull request which consumers were
// told. Do not update it to make a red run green.
func TestLiveLsDocument_topLevelShapeIsPinned(t *testing.T) {
	out := renderLiveLsJSON(t, LiveLsReport{
		Estate:     "dev",
		Region:     "us-east-1",
		Consistent: true,
		Stabilized: true,
		Attempts:   2,
		ConfigDir:  "/srv/estate",
		Schemas:    true,
		Items: []LiveLsItem{{
			ID:       "arn:aws:s3:::my-bucket",
			Type:     "aws_s3_bucket",
			Address:  "aws_s3_bucket.data",
			Slot:     "0",
			Declared: true,
			Source:   "tagging",
			Tags:     map[string]string{"tofu-estate": "dev"},
		}},
		Gaps: []LiveLsGap{{
			Address: "aws_iam_role_policy.inline",
			Type:    "aws_iam_role_policy",
			Rung:    "declaration-carried",
			Detail:  "it has no settable tags argument.",
		}},
	})

	const want = `{
  "estate": "dev",
  "region": "us-east-1",
  "consistent": true,
  "stabilized": true,
  "attempts": 2,
  "config_dir": "/srv/estate",
  "schemas": "provider",
  "items": [
    {
      "id": "arn:aws:s3:::my-bucket",
      "type": "aws_s3_bucket",
      "address": "aws_s3_bucket.data",
      "slot": "0",
      "declared": true,
      "source": "tagging",
      "tags": {
        "tofu-estate": "dev"
      }
    }
  ],
  "gaps": [
    {
      "address": "aws_iam_role_policy.inline",
      "type": "aws_iam_role_policy",
      "rung": "declaration-carried",
      "detail": "it has no settable tags argument."
    }
  ]
}
`
	if out != want {
		t.Errorf("the live-ls -json document changed shape.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

// TestLiveLsHuman_SkippedComparisonSaysSo: the prose report printed "0
// declared instance(s) ... the listing itself cannot see" for a skipped
// comparison, which is the same sentence a complete comparison with
// nothing to report prints.
func TestLiveLsHuman_SkippedComparisonSaysSo(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsHuman{view: NewView(streams)}).Report(LiveLsReport{
		Estate: "dev", ConfigDir: "/srv/estate",
		GapsSkipped: "/srv/estate is outside the stateless subset (3 issue(s)).",
	})
	out := done(t).Stdout()

	if !strings.Contains(out, "outside the stateless subset") {
		t.Errorf("the prose report does not say why the comparison was skipped:\n%s", out)
	}
	if strings.Contains(out, "0 declared instance(s)") {
		t.Errorf("the prose report reported a zero count for a comparison that never ran:\n%s", out)
	}
}

// TestLiveLsHuman_NoSchemasSaysSo: the same degradation live-check's prose
// report has always named, on the command that also depends on it.
func TestLiveLsHuman_NoSchemasSaysSo(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsHuman{view: NewView(streams)}).Report(LiveLsReport{
		Estate: "dev", ConfigDir: "/srv/estate", Schemas: false,
	})
	out := done(t).Stdout()

	if !strings.Contains(out, "choudoufu init") {
		t.Errorf("the prose report does not name the init remedy for a comparison that read no provider schemas:\n%s", out)
	}
}

// GitHub issue #1081, item 1: a Kubernetes object in the listing. The
// human report says the kind and the API version, calls the map "labels",
// and explains an empty address as "undeclared" - a Kubernetes object
// carries no address by design, and one is only ever listed with DIR in
// hand - rather than as an unreadable marker. The JSON item carries
// "kind" and "api_version" for such an object and neither key for an AWS
// one, so TestLiveLsDocument_topLevelShapeIsPinned above still holds
// byte for byte.
func TestLiveLsHuman_KubernetesObject(t *testing.T) {
	streams, done := terminal.StreamsForTesting(t)
	(&LiveLsHuman{view: NewView(streams)}).Report(LiveLsReport{
		Estate:    "smoke-k8s",
		ConfigDir: ".",
		Items: []LiveLsItem{
			{
				ID: "smoke-k8s/app-config", Type: "kubernetes_config_map", Kind: "ConfigMap", APIVersion: "v1",
				Address: "kubernetes_config_map.app", Declared: true, Source: "kubernetes",
				Tags: map[string]string{"tofu-estate": "smoke-k8s", "app": "web"},
			},
			{
				ID: "smoke-k8s/stray", Type: "kubernetes_config_map", Kind: "ConfigMap", APIVersion: "v1",
				Source: "kubernetes", Tags: map[string]string{"tofu-estate": "smoke-k8s"},
			},
		},
	})
	out := done(t).Stdout()

	const want = `
Estate "smoke-k8s": 2 resource(s) carry its marker.

kubernetes_config_map        smoke-k8s/app-config
  kind:    ConfigMap (v1)
  address: kubernetes_config_map.app  (declared)
  found by: kubernetes
  labels:  app=web, tofu-estate=smoke-k8s

kubernetes_config_map        smoke-k8s/stray
  kind:    ConfigMap (v1)
  address: (undeclared - no block in DIR names this object)
  found by: kubernetes
  labels:  tofu-estate=smoke-k8s

0 declared instance(s) in . the listing itself cannot see:
None - every declared instance this configuration knows how to check for is either in the listing above or on a rung this run could not classify.

No provider schemas were available, so no resource type's taggability could be read.
Instances whose type carries no tags argument - the declaration-carried rung, which this
listing structurally cannot see - are missing from the section above rather than reported
in it. Run "choudoufu init" in that directory for the accurate answer.
`
	if out != want {
		t.Errorf("Kubernetes objects render differently.\n--- got ---\n%s\n--- want ---\n%s", out, want)
	}
}

func TestLiveLsJSON_KubernetesObjectCarriesKindAndAPIVersion(t *testing.T) {
	out := renderLiveLsJSON(t, LiveLsReport{
		Estate: "smoke-k8s",
		Items: []LiveLsItem{
			{ID: "smoke-k8s/app-config", Type: "kubernetes_config_map", Kind: "ConfigMap", APIVersion: "v1", Source: "kubernetes", Tags: map[string]string{"tofu-estate": "smoke-k8s"}},
			{ID: "arn:aws:s3:::my-bucket", Type: "aws_s3_bucket", Source: "tagging", Tags: map[string]string{"tofu-estate": "smoke-k8s"}},
		},
	})
	var doc struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("not JSON: %s\n%s", err, out)
	}
	if len(doc.Items) != 2 {
		t.Fatalf("items = %v", doc.Items)
	}
	if doc.Items[0]["kind"] != "ConfigMap" || doc.Items[0]["api_version"] != "v1" {
		t.Errorf("the Kubernetes item lacks kind/api_version: %v", doc.Items[0])
	}
	for _, key := range []string{"kind", "api_version"} {
		if _, present := doc.Items[1][key]; present {
			t.Errorf("the AWS item carries %q, which changes the pinned document shape: %v", key, doc.Items[1])
		}
	}
}
