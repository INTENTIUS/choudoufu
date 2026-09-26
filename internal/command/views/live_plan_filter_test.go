// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package views

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/terminal"
)

// filterReport is one run's worth of every category -filter selects among,
// plus a removal, which is a planned destroy and must print whatever the
// filter says.
func filterReport() ([]StatelessUnowned, StatelessForeign) {
	unowned := []StatelessUnowned{{Addr: "aws_s3_bucket.unowned_row", TypeName: "aws_s3_bucket", LiveID: "u", MarkerEstate: "app", MarkerAddress: "aws_s3_bucket.unowned_row"}}
	rep := StatelessForeign{
		Estate: "app",
		Items:  []StatelessForeignItem{{TypeName: "aws_iam_role", LiveID: "foreign-row"}},
		Candidates: []StatelessBindCandidate{{
			Addr: "aws_vpc.adoptable_row", TypeName: "aws_vpc", LiveID: "vpc-1",
			MarkerEstate: "app", MarkerAddress: "aws_vpc.adoptable_row",
		}},
		Removals: []StatelessRemoval{{Addr: "aws_sqs_queue.removal_row", TypeName: "aws_sqs_queue", LiveID: "q"}},
		Swept:    []string{"aws_iam_role", "aws_vpc"},
	}
	return unowned, rep
}

func renderFiltered(t *testing.T, filter arguments.ReportFilter) string {
	t.Helper()
	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlanFiltered(NewView(streams).SetRunningInAutomation(true), filter)
	unowned, rep := filterReport()
	v.Unowned(unowned)
	v.Foreign(rep)
	return done(t).Stdout()
}

// TestStatelessPlan_filterNarrowsOnlyTheCategories (GitHub issue #1197):
// each filter prints its own category and none of the others, and the
// removal - a planned destroy, part of the plan rather than of the
// categories - prints under every filter.
func TestStatelessPlan_filterNarrowsOnlyTheCategories(t *testing.T) {
	rows := map[string]string{
		arguments.ReportUnowned:   "unowned_row",
		arguments.ReportAdoptable: "adoptable_row",
		arguments.ReportForeign:   "foreign-row",
	}

	all := renderFiltered(t, nil)
	for cat, row := range rows {
		if !strings.Contains(all, row) {
			t.Errorf("unfiltered render is missing the %s row %q:\n%s", cat, row, all)
		}
	}

	for _, only := range arguments.ReportFilterWords {
		t.Run(only, func(t *testing.T) {
			out := renderFiltered(t, arguments.ReportFilter{only})
			for cat, row := range rows {
				if got := strings.Contains(out, row); got != (cat == only) {
					t.Errorf("-filter=%s: %s row present = %v:\n%s", only, cat, got, out)
				}
			}
			if !strings.Contains(out, "removal_row") {
				t.Errorf("-filter=%s hid a planned destroy:\n%s", only, out)
			}
		})
	}

	union := renderFiltered(t, arguments.ReportFilter{arguments.ReportUnowned, arguments.ReportForeign})
	if !strings.Contains(union, "unowned_row") || !strings.Contains(union, "foreign-row") || strings.Contains(union, "adoptable_row") {
		t.Errorf("-filter=unowned -filter=foreign did not union to exactly those two:\n%s", union)
	}
}

// TestStatelessPlan_filterEmptyMatchSaysSo: a selected category with nothing
// in it prints a line, while the same empty category unfiltered stays as
// quiet as it always was.
func TestStatelessPlan_filterEmptyMatchSaysSo(t *testing.T) {
	for _, tc := range []struct {
		filter arguments.ReportFilter
		want   string
	}{
		{arguments.ReportFilter{arguments.ReportUnowned}, "No unowned resources."},
		{arguments.ReportFilter{arguments.ReportAdoptable}, "No adoptable resources."},
		{arguments.ReportFilter{arguments.ReportForeign}, "Foreign resources: none among the 2 types swept"},
	} {
		streams, done := terminal.StreamsForTesting(t)
		v := NewStatelessPlanFiltered(NewView(streams).SetRunningInAutomation(true), tc.filter)
		v.Unowned(nil)
		v.Foreign(StatelessForeign{Estate: "app", Swept: []string{"aws_iam_role", "aws_vpc"}})
		if out := done(t).Stdout(); !strings.Contains(out, tc.want) {
			t.Errorf("-filter=%v matched nothing and did not say %q:\n%s", tc.filter, tc.want, out)
		}
	}

	streams, done := terminal.StreamsForTesting(t)
	v := NewStatelessPlan(NewView(streams).SetRunningInAutomation(true))
	v.Unowned(nil)
	v.Foreign(StatelessForeign{Estate: "app", Swept: []string{"aws_iam_role"}})
	out := done(t).Stdout()
	if strings.Contains(out, "No unowned resources.") || strings.Contains(out, "No adoptable resources") {
		t.Errorf("an unfiltered render printed a filter's empty-match line:\n%s", out)
	}
}
