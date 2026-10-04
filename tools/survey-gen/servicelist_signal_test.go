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

	"github.com/intentius/choudoufu/internal/live/servicetags"
	"github.com/intentius/choudoufu/internal/providers"
)

// TestServiceListRouteIsAThirdEnumerationSignal is GitHub issue #1496's
// signal check. When internal/live/servicetags has both a list route and a
// tag-read route for a type, internal/live/discovery's service-list leg
// enumerates it and binds it by its marker, and the survey row says so with
// signals.service_list. A type in the same service with no list route does
// not carry the signal: it is the route table, not the service prefix.
func TestServiceListRouteIsAThirdEnumerationSignal(t *testing.T) {
	const routed = "aws_iam_service_linked_role"
	const unrouted = "aws_iam_no_such_listed_type"

	r := servicetags.NewIAM(nil)
	if !r.ListRoute(routed) || !r.Route(routed) {
		t.Fatalf("%s lost its servicetags list or read route; this test's premise is gone, re-read #1477 before changing it", routed)
	}
	if r.ListRoute(unrouted) {
		t.Fatalf("%s unexpectedly has a list route", unrouted)
	}

	schemas := providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			routed:   fakeAllSchema(true),
			unrouted: fakeAllSchema(true),
		},
	}
	full := buildSurvey(schemas, allResourceTypeNames(schemas), nil)
	rows := map[string]Row{}
	for _, row := range full.Types {
		rows[row.Type] = row
	}

	if !rows[routed].Signals.ServiceList {
		t.Errorf("%s: Signals.ServiceList is false, want true", routed)
	}
	if rows[unrouted].Signals.ServiceList {
		t.Errorf("%s: Signals.ServiceList is true for a type with no list route", unrouted)
	}
}

// TestCommittedSurveyServiceListRowsMatchTheRouteTable reads the committed
// live/survey-full.json, an external source this package does not write at
// test time, and checks it agrees with the route table discovery drives in
// both directions: every type servicetags can both list and tag-read is a
// row carrying the signal, and no row carries the signal for a type
// the table does not route. A stale artifact (the state #1496 was filed
// against) fails the first half; a classifier that set the signal from
// anything but the table fails the second.
func TestCommittedSurveyServiceListRowsMatchTheRouteTable(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v", surveyFullJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyFullJSONRel, err)
	}

	r := servicetags.NewIAM(nil)
	var want []string
	for tn := range servicetags.IAMListRoutes {
		if r.Route(tn) {
			want = append(want, tn)
		}
	}
	sort.Strings(want)
	if len(want) == 0 {
		t.Fatal("servicetags routes no type for both listing and tag reads, so this test would pass by seeing nothing")
	}

	rows := map[string]Row{}
	var flagged []string
	for _, row := range survey.Types {
		rows[row.Type] = row
		if row.Signals.ServiceList {
			flagged = append(flagged, row.Type)
		}
	}
	sort.Strings(flagged)

	if strings.Join(flagged, ",") != strings.Join(want, ",") {
		t.Errorf("%s flags service_list on %v, and servicetags routes %v; regenerate with `just survey`", surveyFullJSONRel, flagged, want)
	}
	for _, tn := range want {
		if _, ok := rows[tn]; !ok {
			t.Errorf("%s has no row for routed type %s", surveyFullJSONRel, tn)
		}
	}
}
