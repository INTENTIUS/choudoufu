// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package servicetags

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/registry"
)

// The external source this package's one hand-written list is checked
// against. [IAMRoutes] is two entries; without this test that is an
// assertion, and the shape CLAUDE.md warns about - a ratchet that measures
// agreement with itself - is exactly what a table plus a test asserting the
// table's own contents would be. So the set is recomputed here from the
// committed artifacts, and the table has to equal it.
//
// The derivation, and what each clause is doing:
//
//	live/mapping.json      the TF type is mapped to a CloudFormation type
//	live/registry.json     that CFN type's list handler needs no input, so
//	                       internal/live/discovery's Cloud Control leg is
//	                       what enumerates it ...
//	live/registry.json     ... and tagging.taggable is FALSE, so the CFN
//	                       schema has no Tags property and neither
//	                       ListResources nor GetResource can EVER return a
//	                       marker for it
//	live/survey-full.json  the provider type IS taggable, so
//	                       internal/live/stamp writes a marker onto it and
//	                       there is a marker there to miss
//
// Those three are the whole of #1129's SweepGapMarkerUnreadable condition on
// the Cloud Control leg, restated from the artifacts rather than from the
// code.

// surveySignals is the shape of live/survey-full.json this test reads.
type surveySignals struct {
	Types []struct {
		Type    string `json:"type"`
		Signals struct {
			Taggable bool `json:"taggable"`
		} `json:"signals"`
	} `json:"types"`
}

// markerUnreadableOnTheCloudControlLeg recomputes the set of resource types
// the Cloud Control leg can enumerate and can never tag-read.
func markerUnreadableOnTheCloudControlLeg(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "live", "survey-full.json"))
	if err != nil {
		t.Fatalf("reading live/survey-full.json: %v", err)
	}
	var survey surveySignals
	if err := json.Unmarshal(raw, &survey); err != nil {
		t.Fatalf("parsing live/survey-full.json: %v", err)
	}
	if len(survey.Types) == 0 {
		t.Fatal("live/survey-full.json parsed to zero types, so this test would pass by seeing nothing")
	}

	roster, err := registry.Embedded()
	if err != nil {
		t.Fatalf("registry.Embedded: %v", err)
	}

	var out []string
	for _, e := range survey.Types {
		if !e.Signals.Taggable {
			continue
		}
		cfnType, listable := roster.EnumerationSource(e.Type)
		if !listable {
			continue
		}
		if taggable, known := roster.TaggableKnown(cfnType); !known || taggable {
			continue
		}
		out = append(out, e.Type)
	}
	sort.Strings(out)
	return out
}

// TestIAMRoutesMatchTheDerivedSet is the check: every IAM type in the
// derived set has an entry in [IAMRoutes], and [IAMRoutes] has no entry that
// is not in it.
//
// Proved red before green by adding "aws_iam_role" to the table (it is
// mapped to AWS::IAM::Role, whose CFN schema IS taggable, so it is not in
// the derived set) and by deleting the aws_iam_instance_profile entry.
func TestIAMRoutesMatchTheDerivedSet(t *testing.T) {
	var wantIAM []string
	for _, tn := range markerUnreadableOnTheCloudControlLeg(t) {
		if strings.HasPrefix(tn, "aws_iam_") {
			wantIAM = append(wantIAM, tn)
		}
	}
	if len(wantIAM) == 0 {
		t.Fatal("the derivation found no IAM type at all, so this test cannot be measuring what it claims: check live/survey-full.json and the embedded roster before touching IAMRoutes")
	}

	var got []string
	for tn := range IAMRoutes {
		got = append(got, tn)
	}
	sort.Strings(got)

	if strings.Join(got, ",") != strings.Join(wantIAM, ",") {
		t.Fatalf("IAMRoutes covers %v, and the artifacts derive %v.\nAn entry the derivation does not name is a call this leg can never usefully make; a derived type with no entry is an object whose marker is readable and is not being read - which is #881, one type over.", got, wantIAM)
	}
}

// TestDerivedSetBeyondIAMIsNamedNotSilent keeps the OTHER services in the
// derived set visible. They are not wired, and the reason is the gate in
// internal/live/discovery rather than anything about them: the Resource
// Groups Tagging API does index their services, so [markerIndex.servesType]
// answers and the leg never runs. If that ever stops being true for one of
// them, it needs its own service wired here - and this test is what puts
// the list in front of whoever is reading.
//
// It asserts the SIZE and the membership of the non-IAM remainder rather
// than pinning a number in prose, so a provider or artifact bump that
// changes the set fails here instead of quietly widening a claim this
// package makes in its own doc comment.
func TestDerivedSetBeyondIAMIsNamedNotSilent(t *testing.T) {
	want := map[string]bool{
		"aws_appautoscaling_target":                 true,
		"aws_appstream_image_builder":               true,
		"aws_cloudwatch_log_anomaly_detector":       true,
		"aws_config_config_rule":                    true,
		"aws_dms_certificate":                       true,
		"aws_docdb_event_subscription":              true,
		"aws_ec2_fleet":                             true,
		"aws_elastic_beanstalk_application":         true,
		"aws_elastic_beanstalk_application_version": true,
		"aws_glue_catalog_database":                 true,
		"aws_inspector_assessment_template":         true,
		"aws_launch_template":                       true,
		"aws_sagemaker_monitoring_schedule":         true,
		"aws_spot_fleet_request":                    true,
		"aws_vpc_security_group_egress_rule":        true,
		"aws_vpc_security_group_ingress_rule":       true,
	}

	got := map[string]bool{}
	for _, tn := range markerUnreadableOnTheCloudControlLeg(t) {
		if !strings.HasPrefix(tn, "aws_iam_") {
			got[tn] = true
		}
	}

	var missing, extra []string
	for tn := range want {
		if !got[tn] {
			missing = append(missing, tn)
		}
	}
	for tn := range got {
		if !want[tn] {
			extra = append(extra, tn)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		t.Fatalf("the non-IAM half of the derived set moved: no longer derived %v, newly derived %v.\nEach of these is a type Cloud Control enumerates and can never tag-read. None is wired here because the tagging index covers their services; a newly derived one is worth a look before this list is simply updated.", missing, extra)
	}
}
