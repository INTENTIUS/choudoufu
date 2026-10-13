// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// recordedOutputs is a record store as terraform_estate_outputs sees it.
type recordedOutputs map[string]map[string]cty.Value

func (r recordedOutputs) ReadEstateOutputs(_ context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics
	out := map[string]cty.Value{}
	for _, n := range names {
		v, ok := r[estate][n]
		if !ok {
			return nil, diags.Append(tfdiags.Sourceless(tfdiags.Error, projection.SummaryEstateOutputNotRecorded, "not recorded"))
		}
		out[n] = v
	}
	return out, diags
}

// GitHub issue #2051: a plan given -estate-outputs-from answers that
// estate's reads from its plan and records what it read for the saved plan.
func TestLiveEstateOutputs_plannedSourceAndRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "network.json")
	doc := `{"format_version":"1.2","planned_values":{"outputs":{"vpc_id":{"sensitive":false,"type":"string","value":"vpc-new"}}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &liveEstateOutputs{}
	l.open(nil, nil, "app", "no store in this test")
	if diags := l.usePlanned([]arguments.EstateOutputsFrom{{Estate: "network", Path: path}}); diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	values, diags := l.ReadEstateOutputs(context.Background(), "network", []string{"vpc_id"})
	if diags.HasErrors() {
		t.Fatal(diags.Err())
	}
	if !values["vpc_id"].RawEquals(cty.StringVal("vpc-new")) {
		t.Fatalf("vpc_id = %#v, want the planned value", values["vpc_id"])
	}
	var rec estateOutputsFromPlanRecord
	if err := json.Unmarshal(l.planFileRecord(), &rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.Estates["network"]["vpc_id"]; got != `"vpc-new"` {
		t.Errorf("recorded %q, want the planned value as JSON", got)
	}

	// Another estate still goes to the store, which this test has none of.
	if _, diags := l.ReadEstateOutputs(context.Background(), "dns", []string{"zone"}); !diags.HasErrors() {
		t.Error("a read of an estate with no plan given was answered without a store")
	}
}

// A plan read from nothing but the store embeds no record, so its file is
// what it always was.
func TestLiveEstateOutputs_noRecordWithoutPlannedReads(t *testing.T) {
	if rec := (&liveEstateOutputs{}).planFileRecord(); rec != nil {
		t.Fatalf("record %s, want none", rec)
	}
}

// The apply-time half: the record store must hold what the plan was made on.
func TestCompareEstateOutputsFromPlan(t *testing.T) {
	planned := map[string]map[string]string{"network": {"vpc_id": `"vpc-new"`}}
	ctx := context.Background()

	t.Run("applied equals planned", func(t *testing.T) {
		store := recordedOutputs{"network": {"vpc_id": cty.StringVal("vpc-new")}}
		diags, mismatch := compareEstateOutputsFromPlan(ctx, "app.tfplan", planned, store)
		if mismatch || diags.HasErrors() {
			t.Fatalf("refused a match: %v", diags.Err())
		}
	})
	for name, store := range map[string]recordedOutputs{
		"applied differs": {"network": {"vpc_id": cty.StringVal("vpc-old")}},
		"not recorded":    {},
	} {
		t.Run(name, func(t *testing.T) {
			diags, mismatch := compareEstateOutputsFromPlan(ctx, "app.tfplan", planned, store)
			if !mismatch || !diagsHaveSummary(diags, summaryApprovalEstateOutputs) {
				t.Fatalf("not refused: %v", diags)
			}
			detail := diags[len(diags)-1].Description().Detail
			for _, want := range []string{`estate "network"`, `output "vpc_id"`, `planned "vpc-new"`, "applied "} {
				if !strings.Contains(detail, want) {
					t.Errorf("detail lacks %s:\n%s", want, detail)
				}
			}
		})
	}
}
