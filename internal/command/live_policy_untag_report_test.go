// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/markers"
)

// GitHub issue #1002: the plan's "Policy untag" section names the instances
// declared_tagged = "untag" actually released a marker key from. #949
// ported the suppression to the node writer and left this list empty, so a
// reader could see the verb was declared and not which instances it reached.
//
// The fixture has three declared instances and the verb reaches them three
// different ways: pool["owned"] is live and marked, so its tofu-estate is
// released; pool["fresh"] is a create no quadrant governs, stamped in full;
// pinned is governed but writes the key by hand, which the writer leaves
// alone, so nothing was released there either. The assertion is the whole
// list by value: a report naming pinned or pool["fresh"] is as wrong as one
// naming nothing.

const untagReportFixture = "live-untag-report-1002"

// untagReportWant is the released list the fixture must produce, in the
// order the view prints it.
var untagReportWant = []untagReportLine{
	{addr: `aws_s3_bucket.pool["owned"]`, key: "tofu-estate", leavesManagement: true},
}

type untagReportLine struct {
	addr             string
	key              string
	leavesManagement bool
}

// untagReportLineRE matches one entry of the view's "Policy untag" section
// (views/live_plan.go): two spaces, the address, `releases "<key>"`, and the
// leaves-management flag when the key is tofu-estate.
var untagReportLineRE = regexp.MustCompile(`(?m)^  (\S.*) releases "([^"]+)"( \[LEAVES MANAGEMENT\])?$`)

func untagReportLines(stdout string) []untagReportLine {
	var out []untagReportLine
	for _, m := range untagReportLineRE.FindAllStringSubmatch(stdout, -1) {
		out = append(out, untagReportLine{addr: m[1], key: m[2], leavesManagement: m[3] != ""})
	}
	return out
}

func untagReportCloud() *statelessTestCloud {
	cloud := newStatelessTestCloud()
	cloud.putMarked("aws_s3_bucket", "tofu-untag-1002-owned", "stateless-unit", markers.EscapeAddress(`aws_s3_bucket.pool["owned"]`), map[string]string{
		"id": "tofu-untag-1002-owned", "bucket": "tofu-untag-1002-owned",
	})
	cloud.putMarked("aws_s3_bucket", "tofu-untag-1002-pinned", "stateless-unit", "aws_s3_bucket.pinned", map[string]string{
		"id": "tofu-untag-1002-pinned", "bucket": "tofu-untag-1002-pinned",
	})
	return cloud
}

func assertUntagReport(t *testing.T, stdout string) {
	t.Helper()

	// The precondition that makes the list meaningful: the policy was
	// declared and the run saw it govern both live instances. Without this
	// an empty list and a correct list of one are not distinguishable from
	// a fixture that never reached the quadrant.
	for _, governed := range []string{`aws_s3_bucket.pool["owned"]`, "aws_s3_bucket.pinned"} {
		if !strings.Contains(stdout, "  "+governed+" <- aws_s3_bucket [declared_tagged=untag]\n") {
			t.Fatalf("the policy's Declared section does not show %s under declared_tagged=untag, so this fixture never reached the quadrant:\n%s", governed, stdout)
		}
	}

	got := untagReportLines(stdout)
	t.Logf("released list = %+v", got)
	if !reflect.DeepEqual(got, untagReportWant) {
		t.Errorf("released list = %+v, want %+v\nstdout:\n%s", got, untagReportWant, stdout)
	}
	if !strings.Contains(stdout, "Policy untag: 1 resource instance releasing a tag") {
		t.Errorf("the section header does not count exactly one release (got list %+v)", got)
	}
	if strings.Contains(stdout, `[declared_tagged=untag]`+"\n") && strings.Contains(stdout, `pool["fresh"] <- `) {
		t.Errorf("pool[\"fresh\"] is a create and no quadrant governs it, but the Declared section lists it:\n%s", stdout)
	}
}

// TestLivePlan_untagReportNamesReleasedInstances is the "live-plan" form.
func TestLivePlan_untagReportNamesReleasedInstances(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath(untagReportFixture), td)
	t.Chdir(td)

	c, done := newLivePlanCommand(t, untagReportCloud())
	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	assertUntagReport(t, output.Stdout())
}

// TestStatelessMode_untagReportNamesReleasedInstances is plain "plan" under
// the live block, which reaches the writer through the backend's walk and
// reports from [statelessRunner.AfterPlan].
func TestStatelessMode_untagReportNamesReleasedInstances(t *testing.T) {
	td := t.TempDir()
	testCopyDir(t, testFixturePath(untagReportFixture), td)
	t.Chdir(td)

	c, done := newLiveBlockPlanCommand(t, untagReportCloud())
	code := c.Run([]string{"-no-color"})
	output := done(t)
	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, output.Stdout(), output.Stderr())
	}
	assertUntagReport(t, output.Stdout())
}
