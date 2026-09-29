// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// TestParsePlan_filter covers GitHub issue #1197's -filter: the accepted
// words, that repeats union in a fixed order, and that any other word is a
// parse error naming the accepted ones - so "-filter nonsense" can never be
// mistaken for a valid filter that matched nothing.
func TestParsePlan_filter(t *testing.T) {
	testCases := map[string]struct {
		args []string
		want ReportFilter
		bad  string
	}{
		"absent":          {nil, nil, ""},
		"one":             {[]string{"-filter=foreign"}, ReportFilter{"foreign"}, ""},
		"space form":      {[]string{"-filter", "unowned"}, ReportFilter{"unowned"}, ""},
		"double dash":     {[]string{"--filter", "adoptable"}, ReportFilter{"adoptable"}, ""},
		"repeats union":   {[]string{"-filter=foreign", "-filter=unowned"}, ReportFilter{"unowned", "foreign"}, ""},
		"duplicates fold": {[]string{"-filter=foreign", "-filter=foreign"}, ReportFilter{"foreign"}, ""},
		"all three":       {[]string{"-filter=foreign", "-filter=adoptable", "-filter=unowned"}, ReportFilter{"unowned", "adoptable", "foreign"}, ""},
		"nonsense":        {[]string{"--filter", "nonsense"}, nil, "nonsense"},
		// The selectors the issue was filed with and the ruling deferred
		// or renamed: none of them is a word this flag knows.
		"drifted":   {[]string{"-filter=drifted"}, nil, "drifted"},
		"unclaimed": {[]string{"-filter=unclaimed"}, nil, "unclaimed"},
		"untagged":  {[]string{"-filter=untagged"}, nil, "untagged"},
		"owned-by":  {[]string{"-filter=owned-by:prod"}, nil, "owned-by:prod"},
		"case":      {[]string{"-filter=Foreign"}, nil, "Foreign"},
		"comma":     {[]string{"-filter=unowned,foreign"}, nil, "unowned,foreign"},
		"empty":     {[]string{"-filter="}, nil, `""`},
	}
	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			got, closer, diags := ParsePlan(tc.args)
			defer closer()
			if tc.bad == "" {
				if diags.HasErrors() {
					t.Fatalf("unexpected error: %s", diags.Err())
				}
				if diff := cmp.Diff(tc.want, got.Filter); diff != "" {
					t.Errorf("wrong filter (-want +got):\n%s", diff)
				}
				return
			}
			if !diags.HasErrors() {
				t.Fatalf("-filter accepted %v as %v, want a usage error", tc.args, got.Filter)
			}
			msg := diags.Err().Error()
			for _, want := range append([]string{tc.bad}, ReportFilterWords...) {
				if !strings.Contains(msg, want) {
					t.Errorf("usage error does not name %q:\n%s", want, msg)
				}
			}
		})
	}
}

// TestReportFilter_shows: no filter shows everything, which is what keeps an
// unfiltered run's report exactly what it was before -filter existed.
func TestReportFilter_shows(t *testing.T) {
	var none ReportFilter
	for _, w := range ReportFilterWords {
		if !none.Shows(w) {
			t.Errorf("an empty filter hides %q", w)
		}
	}
	if none.Active() {
		t.Error("an empty filter reports itself active")
	}
	f := ReportFilter{ReportForeign}
	if f.Shows(ReportUnowned) || f.Shows(ReportAdoptable) || !f.Shows(ReportForeign) {
		t.Errorf("filter %v shows the wrong set", f)
	}
}
