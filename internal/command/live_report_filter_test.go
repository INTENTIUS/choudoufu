// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/command/arguments"
	"github.com/intentius/choudoufu/internal/command/views"
)

// TestLivePlanFilter_unknownWordIsAUsageError is GitHub issue #1197's red
// arm, first half: an unknown predicate fails, with a usage error naming the
// accepted words, before any plan runs. Without it "-filter drifted" would
// be indistinguishable from a filter that matched nothing.
func TestLivePlanFilter_unknownWordIsAUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"-no-color", "--filter", "nonsense"},
		{"-no-color", "-filter=drifted"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			td := t.TempDir()
			testCopyDir(t, testFixturePath("live-block"), td)
			t.Chdir(td)

			view, done := testView(t)
			c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
			code := c.Run(args)
			output := done(t)
			if code != 1 {
				t.Fatalf("exit code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
			}
			stderr := output.Stderr()
			for _, want := range []string{"unowned", "adoptable", "foreign"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("usage error does not name %q:\n%s", want, stderr)
				}
			}
			if strings.Contains(output.Stdout(), "No changes.") || strings.Contains(output.Stdout(), "Plan:") {
				t.Errorf("a plan ran under an unknown filter:\n%s", output.Stdout())
			}
		})
	}
}

// TestLivePlanFilter_emptyMatchIsVisible is the red arm's second half: a
// valid filter that matches nothing prints a line saying so, never silence,
// and the plan below it is the same plan with the same exit code. The
// live-block fixture owns everything it declares, so no category has
// anything in it.
func TestLivePlanFilter_emptyMatchIsVisible(t *testing.T) {
	run := func(t *testing.T, args ...string) (int, string) {
		t.Helper()
		td := t.TempDir()
		testCopyDir(t, testFixturePath("live-block"), td)
		t.Chdir(td)

		view, done := testView(t)
		c := &LivePlanCommand{Meta: liveBlockMeta(view, liveBlockCloud())}
		code := c.Run(append([]string{"-no-color", "-detailed-exitcode"}, args...))
		output := done(t)
		return code, output.Stdout()
	}

	baseCode, base := run(t)
	if strings.Contains(base, "No unowned resources.") || strings.Contains(base, "No adoptable resources") {
		t.Fatalf("an unfiltered run printed a filter's empty-match line:\n%s", base)
	}

	for _, tc := range []struct {
		filter string
		want   string
	}{
		{"unowned", "No unowned resources."},
		{"adoptable", "No adoptable resources"},
		{"foreign", "Foreign resources:"},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			code, out := run(t, "-filter="+tc.filter)
			if code != baseCode {
				t.Errorf("exit code %d under -filter=%s, %d without: a filter changed the exit code's meaning", code, tc.filter, baseCode)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("-filter=%s matched nothing and did not say so (want %q):\n%s", tc.filter, tc.want, out)
			}
			if !strings.Contains(out, "No changes.") {
				t.Errorf("-filter=%s suppressed the plan:\n%s", tc.filter, out)
			}
		})
	}
}

// TestPlanRejectReportFilter: refused where it would do nothing, accepted
// where it narrows something.
func TestPlanRejectReportFilter(t *testing.T) {
	f := arguments.ReportFilter{arguments.ReportForeign}
	if diags := planRejectReportFilter(f, false, false); !diags.HasErrors() {
		t.Error("-filter was accepted on a state-backed plan")
	}
	if diags := planRejectReportFilter(f, true, true); !diags.HasErrors() {
		t.Error("-filter was accepted alongside -adoption-only")
	}
	if diags := planRejectReportFilter(f, false, true); diags.HasErrors() {
		t.Errorf("-filter was refused on a live run: %s", diags.Err())
	}
	if diags := planRejectReportFilter(nil, true, false); diags.HasErrors() {
		t.Errorf("no -filter was refused: %s", diags.Err())
	}
}

// TestLivePlanFilterDocument pins the -json half: a left-out category is
// null, a kept empty one is [], the "filter" key names what was kept, and
// the plan-describing fields are never narrowed.
func TestLivePlanFilterDocument(t *testing.T) {
	full := views.LivePlanDocument{
		Estate:    "app",
		Bound:     []views.LivePlanBound{{Addr: "aws_s3_bucket.a"}},
		Omissions: []views.StatelessOmission{{Addr: "aws_vpc.x", Reason: "NEEDS_DISCOVERY"}},
		Unowned:   []views.StatelessUnowned{{Addr: "aws_s3_bucket.b"}},
		Adoptable: []views.LivePlanAdoptable{},
		Foreign:   nil,
		Swept:     []string{"aws_s3_bucket"},
	}

	if got := livePlanFilterDocument(full, nil); got.Filter != nil || got.Unowned == nil {
		t.Errorf("no filter changed the document: %+v", got)
	}

	got := livePlanFilterDocument(full, arguments.ReportFilter{arguments.ReportForeign})
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"filter":    `["foreign"]`,
		"foreign":   `[]`,
		"unowned":   `null`,
		"adoptable": `null`,
		"swept":     `["aws_s3_bucket"]`,
	} {
		if string(m[key]) != want {
			t.Errorf("%s = %s, want %s", key, m[key], want)
		}
	}
	if len(got.Bound) != 1 || len(got.Omissions) != 1 {
		t.Errorf("the filter narrowed the plan-describing fields: bound %d, omissions %d", len(got.Bound), len(got.Omissions))
	}
}
