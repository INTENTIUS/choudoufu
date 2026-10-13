// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// A "show -json" planned_values.outputs as jsonplan prints it: a known
// output carries value and type, an unknown one neither (#2051).
const plannedDoc = `{
  "format_version": "1.2",
  "planned_values": {
    "outputs": {
      "vpc_id":   {"sensitive": false, "type": "string", "value": "vpc-new"},
      "subnets":  {"sensitive": false, "type": ["list", "string"], "value": ["a", "b"]},
      "arn":      {"sensitive": false},
      "password": {"sensitive": true, "type": "string", "value": "hunter2"}
    }
  }
}`

func parsedPlanned(t *testing.T) *PlannedEstateOutputs {
	t.Helper()
	p, err := ParsePlannedEstateOutputs("network", "network.json", []byte(plannedDoc))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadPlannedEstateOutputs_known(t *testing.T) {
	values, diags := ReadPlannedEstateOutputs(parsedPlanned(t), []string{"vpc_id", "subnets"})
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if got := values["vpc_id"]; !got.RawEquals(cty.StringVal("vpc-new")) {
		t.Errorf("vpc_id = %#v, want the planned value", got)
	}
	if got := values["subnets"]; !got.RawEquals(cty.ListVal([]cty.Value{cty.StringVal("a"), cty.StringVal("b")})) {
		t.Errorf("subnets = %#v, want the planned list", got)
	}
	if len(diags) != 1 || diags[0].Description().Summary != SummaryEstateOutputsFromPlan {
		t.Errorf("want one %q warning, got %v", SummaryEstateOutputsFromPlan, diags)
	}
}

func TestReadPlannedEstateOutputs_refusals(t *testing.T) {
	for name, tc := range map[string]struct {
		output, summary string
	}{
		"unknown":   {"arn", SummaryEstateOutputUnknownInPlan},
		"sensitive": {"password", SummaryEstateOutputSensitiveInPlan},
		"absent":    {"nope", SummaryEstateOutputNotInPlan},
	} {
		t.Run(name, func(t *testing.T) {
			values, diags := ReadPlannedEstateOutputs(parsedPlanned(t), []string{"vpc_id", tc.output})
			if !diags.HasErrors() {
				t.Fatalf("read of %q was answered: %#v", tc.output, values)
			}
			if values != nil {
				t.Errorf("a refused read returned values: %#v", values)
			}
			d := diags[0].Description()
			if d.Summary != tc.summary {
				t.Errorf("summary %q, want %q", d.Summary, tc.summary)
			}
			for _, want := range []string{`"network"`, `"` + tc.output + `"`} {
				if !strings.Contains(d.Detail, want) {
					t.Errorf("detail does not name %s: %s", want, d.Detail)
				}
			}
		})
	}
}
