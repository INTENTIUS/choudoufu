// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/discovery"
)

// TestSurveyDoesNotClaimTagFilteredListForAnUnservedService is issue
// #1133's guard: it fails a committed survey artifact that puts a type on
// [pathMarker] with the "recoverable by tag-filtered list" sentence while
// that type's service is one internal/live/discovery routes away from the
// tagging leg entirely ([discovery.TaggingAPIUnservedTypeInRegion], issues
// #692 and #1144).
//
// Before this commit it failed against the committed live/survey-full.json
// and live/survey.json, naming eight types in the aws_iam_ service -
// aws_iam_instance_profile, aws_iam_openid_connect_provider, aws_iam_policy,
// aws_iam_role, aws_iam_saml_provider, aws_iam_server_certificate,
// aws_iam_service_linked_role and aws_iam_virtual_mfa_device in
// live/survey-full.json, and the three of those eight in the curated
// 68-type live/survey.json (aws_iam_instance_profile, aws_iam_policy,
// aws_iam_role). Every one of them was taggable per the provider's own
// schema, and every one inherited the marker path - and its "recoverable by
// tag-filtered list" evidence sentence - purely from that, because the
// classifier never asked whether the tagging API's sweep leg would ever
// have reached it. RGTA never indexes IAM roles anywhere, and issue #1134's
// real-AWS measurement found it indexes IAM policies and instance profiles
// only in us-east-1. Since #1144 the classifier CAN say which of the two a
// given type is, and does, in its evidence sentence; this guard still asks
// the same question it always asked, with the empty region the survey has -
// that no UNQUALIFIED claim of the route survives in an artifact that
// belongs to no region.
//
// It reads the classifier's own vocabulary constant and evidence sentence
// rather than restating them, so a future wording change cannot silently
// desync the guard from what it is meant to catch.
func TestSurveyDoesNotClaimTagFilteredListForAnUnservedService(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{surveyJSONRel, surveyFullJSONRel} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Fatalf("reading %s: %v", rel, err)
			}
			var survey Survey
			if err := json.Unmarshal(data, &survey); err != nil {
				t.Fatalf("decoding %s: %v", rel, err)
			}

			var offenders []string
			for _, row := range survey.Types {
				if row.Path != pathMarker {
					continue
				}
				if !strings.Contains(row.Evidence, "recoverable by tag-filtered list") {
					continue
				}
				if discovery.TaggingAPIUnservedTypeInRegion("", row.Type) {
					offenders = append(offenders, row.Type+": "+row.Evidence)
				}
			}
			sort.Strings(offenders)

			if len(offenders) > 0 {
				t.Fatalf("%s claims tag-filtered-list recovery for %d type(s) whose service "+
					"internal/live/discovery routes away from the tagging leg entirely "+
					"from a caller region this artifact does not have "+
					"(discovery.TaggingAPIUnservedTypeInRegion, issues #692 and #1144) - the sweep never takes this "+
					"route, so the survey must not promise it (issue #1133):\n%s",
					rel, len(offenders), strings.Join(offenders, "\n"))
			}
		})
	}
}
